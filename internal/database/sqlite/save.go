package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/ncruces/go-sqlite3"

	"dbl/internal/database"
	"dbl/internal/grid"
)

var _ database.SQLAdapter = (*Adapter)(nil)

// buildUpdate builds the UPDATE of one changed row. Table and column names
// are quoted, and every value, the identity included, is a bound parameter,
// so the text holds only names the metadata returned.
//
// The row is found by its original identity and written only if each column
// being changed still holds the value the user saw (optimistic concurrency,
// spec FR-005). A SQLite column can hold any type, so that comparison is on
// the storage class as well as the value: the integer 1 and the text '1' are
// different values, and a change from one to the other is a conflict.
func buildUpdate(m *database.TableMetadata, rc database.RowChange) (string, []any, error) {
	var a args
	var q strings.Builder
	q.WriteString("UPDATE " + quoteTable(m.Ref) + " SET ")
	for i, ch := range rc.Cells {
		c, ok := m.Column(ch.Column)
		if !ok {
			return "", nil, fmt.Errorf("the table has no column %q", ch.Column)
		}
		v, err := newValue(c, ch.Current)
		if err != nil {
			return "", nil, err
		}
		if i > 0 {
			q.WriteString(", ")
		}
		q.WriteString(quoteIdent(ch.Column) + " = " + a.add(v))
	}
	q.WriteString(" WHERE ")
	first := true
	and := func() {
		if !first {
			q.WriteString(" AND ")
		}
		first = false
	}
	for _, k := range rc.Key {
		and()
		q.WriteString(quoteIdent(k.Column) + " = " + a.add(storedValue(k.Value)))
	}
	for _, ch := range rc.Cells {
		and()
		q.WriteString(sameValue(quoteIdent(ch.Column), ch.Original, &a))
	}
	return q.String(), a.list, nil
}

// storedValue is the bound value for a value read from the database: what
// the cell showed is what SQLite stored, by its kind.
func storedValue(v grid.Value) any {
	switch v.Kind {
	case grid.KindNull:
		return nil
	case grid.KindNumber:
		if n, err := strconv.ParseInt(v.Text, 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(v.Text, 64); err == nil {
			return f
		}
	}
	return v.Text
}

// sameValue is the condition that col still holds the value v was read as.
func sameValue(col string, v grid.Value, a *args) string {
	switch v.Kind {
	case grid.KindNull:
		return col + " IS NULL"
	case grid.KindNumber:
		return "typeof(" + col + ") IN ('integer', 'real') AND " + col + " = " + a.add(storedValue(v))
	case grid.KindBinary:
		return "typeof(" + col + ") = 'blob'"
	}
	// Text of every sort: compared as text, byte for byte, whatever the
	// collation of the column says.
	return "typeof(" + col + ") = 'text' AND CAST(" + col + " AS TEXT) = " + a.add(v.Text) + " COLLATE BINARY"
}

// newValue is the bound value for what the user typed, by the class of the
// column: a number in a numeric column is stored as a number, anything else
// as the text typed.
func newValue(c database.ColumnMetadata, v grid.Value) (any, error) {
	if v.Kind == grid.KindNull {
		return nil, nil
	}
	if c.Class != database.ClassNumber {
		return v.Text, nil
	}
	text := strings.TrimSpace(v.Text)
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n, nil
	}
	if c.IntBits > 0 {
		return nil, fmt.Errorf("%q is not a whole number", v.Text)
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		if math.IsNaN(f) {
			return nil, errors.New("NaN cannot be stored in SQLite")
		}
		return f, nil
	}
	return v.Text, nil // a NUMERIC column keeps what is not a number as text
}

// commitGate carries the cancellation of the caller's context to the
// transaction until COMMIT is about to be sent, and not after. Canceling a
// Save is possible only before the commit: once COMMIT is on its way,
// interrupting it would leave the outcome unknown for no gain.
type commitGate struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	committing bool
	done       chan struct{}
}

// newCommitGate returns the context the transaction runs on. Release the
// gate when the Save ends.
func newCommitGate(ctx context.Context) (context.Context, *commitGate) {
	txCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	g := &commitGate{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			g.mu.Lock()
			if !g.committing {
				cancel()
			}
			g.mu.Unlock()
		case <-g.done:
		}
	}()
	return txCtx, g
}

// beginCommit crosses the point of no return: it reports false when the
// caller has already canceled, in which case the Save must roll back.
func (g *commitGate) beginCommit() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx.Err() != nil {
		return false
	}
	g.committing = true
	return true
}

func (g *commitGate) release() {
	close(g.done)
	g.cancel()
}

// SaveChanges writes the changes in one transaction. It returns nil only
// after COMMIT is confirmed; every error is a *database.SaveError. A write
// is never retried.
func (a *Adapter) SaveChanges(ctx context.Context, req database.SaveRequest) error {
	rowFail := func(rc database.RowChange, err *database.Error) error {
		return &database.SaveError{Err: err, RowID: rc.RowID, HasRow: true}
	}
	fail := func(err *database.Error) error { return &database.SaveError{Err: err} }

	if a.readOnly {
		return fail(&database.Error{Kind: database.KindPermission, Message: readOnlyReason, Hint: "Open the connection without \"Open read-only\" to edit."})
	}
	if req.Meta == nil || req.Changes.Empty() {
		return fail(&database.Error{Kind: database.KindValidation, Message: "There is nothing to save."})
	}
	if issues := database.ValidateChanges(req.Meta, req.Changes); len(issues) > 0 {
		return fail(&database.Error{Kind: database.KindValidation, Message: issues[0].Message})
	}
	// The statements are built before the transaction begins, so a problem
	// found while building never leaves a transaction open.
	type stmt struct {
		rc   database.RowChange
		sql  string
		args []any
	}
	stmts := make([]stmt, len(req.Changes.Rows))
	for i, rc := range req.Changes.Rows {
		s, args, err := buildUpdate(req.Meta, rc)
		if err != nil {
			return rowFail(rc, &database.Error{Kind: database.KindValidation, Message: err.Error()})
		}
		stmts[i] = stmt{rc, s, args}
	}

	txCtx, gate := newCommitGate(ctx)
	defer gate.release()

	tx, err := a.db.BeginTx(txCtx, nil)
	if err != nil {
		return fail(classifySave(ctx, err, "start the save"))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, s := range stmts {
		res, err := tx.ExecContext(txCtx, s.sql, s.args...)
		if err != nil {
			return rowFail(s.rc, classifySave(ctx, err, "save the row"))
		}
		n, err := res.RowsAffected()
		if err != nil {
			return rowFail(s.rc, classifySave(ctx, err, "save the row"))
		}
		switch {
		case n == 0:
			return rowFail(s.rc, &database.Error{
				Kind:    database.KindConflict,
				Message: "The row was changed or deleted by someone else, or a trigger skipped the update.",
				Hint:    "Reload the table to see the current values.",
			})
		case n > 1:
			return rowFail(s.rc, &database.Error{
				Kind:    database.KindSave,
				Message: fmt.Sprintf("The update would have changed %d rows, so nothing was saved.", n),
				Hint:    "The key of this table does not identify a single row.",
			})
		}
	}
	if !gate.beginCommit() {
		return fail(classifySave(ctx, ctx.Err(), "save"))
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return commitFailed(err)
	}
	return nil
}

// commitFailed classifies an error of COMMIT. When SQLite answered that it
// is busy, read-only or rejected the change, nothing was written. Anything
// else (an I/O error, a connection that went away) leaves the outcome
// unknown, and the changes must not be sent again without a look.
func commitFailed(err error) error {
	var code sqlite3.ErrorCode
	if errors.Is(err, sql.ErrTxDone) {
		return &database.SaveError{Err: classifySave(context.Background(), err, "save")}
	}
	if errors.As(err, &code) {
		switch code {
		case sqlite3.BUSY, sqlite3.LOCKED, sqlite3.CONSTRAINT, sqlite3.READONLY, sqlite3.FULL, sqlite3.PERM, sqlite3.AUTH:
			return &database.SaveError{Err: classifySave(context.Background(), err, "save")}
		}
	}
	return &database.SaveError{
		Outcome: database.SaveUnknown,
		Err: &database.Error{
			Kind:    database.KindConnection,
			Message: "The save failed while it was being committed, so it is not known whether the changes were saved.",
			Hint:    "Reload the table to check before saving again.",
			Err:     err,
		},
	}
}

// classifySave turns an error of a write into a *database.Error. The codes
// that matter to a write are classified here; everything else goes through
// classify.
func classifySave(ctx context.Context, err error, doing string) *database.Error {
	var code sqlite3.ErrorCode
	if ctx.Err() == nil && errors.As(err, &code) && code == sqlite3.CONSTRAINT {
		return &database.Error{
			Kind:    database.KindValidation,
			Message: "A constraint of the table rejected the change: " + statementMessage(err),
			Hint:    "Nothing was saved.",
			Err:     err,
		}
	}
	return classify(ctx, err, doing)
}
