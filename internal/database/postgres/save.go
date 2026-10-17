package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// buildUpdate builds the UPDATE of one changed row. Table and column names
// are quoted, and every value, the identity included, is a bound parameter,
// so the text holds only names the metadata returned.
//
// The row is found by its original identity ("k" = $n, which lets the server
// use the key's index) and written only if each column being changed still
// holds the value the user saw (optimistic concurrency, spec FR-005). That
// comparison is on the text of the value, the way the page was read, and is
// NULL-safe. A parameter is sent as text and the server reads it as the type
// of the column it meets.
func buildUpdate(m *database.TableMetadata, rc database.RowChange) (string, []any) {
	var a args
	var q strings.Builder
	q.WriteString("UPDATE " + quoteTable(m.Ref) + " SET ")
	for i, ch := range rc.Cells {
		if i > 0 {
			q.WriteString(", ")
		}
		q.WriteString(quoteIdent(ch.Column) + " = " + a.add(param(ch.Current)))
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
		q.WriteString(quoteIdent(k.Column) + " = " + a.add(param(k.Value)))
	}
	for _, ch := range rc.Cells {
		and()
		q.WriteString(quoteIdent(ch.Column) + "::text IS NOT DISTINCT FROM " + a.add(param(ch.Original)))
	}
	return q.String(), a.list
}

// param is the bound value of a grid value: NULL, or its text.
func param(v grid.Value) any {
	if v.Kind == grid.KindNull {
		return nil
	}
	return v.Text
}

// commitGate carries the cancellation of the caller's context to the
// transaction until COMMIT is about to be sent, and not after. Canceling a
// Save is possible only before the commit: once COMMIT is on its way, stopping
// the connection would leave the outcome unknown for no gain.
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
// after COMMIT is confirmed; every error is a *database.SaveError.
func (a *Adapter) SaveChanges(ctx context.Context, req database.SaveRequest) error {
	rowFail := func(rc database.RowChange, err *database.Error) error {
		return &database.SaveError{Err: err, RowID: rc.RowID, HasRow: true}
	}
	fail := func(err *database.Error) error { return &database.SaveError{Err: err} }

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
		s, args := buildUpdate(req.Meta, rc)
		stmts[i] = stmt{rc, s, args}
	}

	txCtx, gate := newCommitGate(ctx)
	defer gate.release()

	tx, err := a.db.BeginTx(txCtx, nil)
	if err != nil {
		return fail(classifySave(ctx, err, "Starting the save"))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	// Values are compared as text, so floats must print the same way as when
	// the page was read: shortest exact form. If the session that read them
	// printed another form, the worst that happens is a conflict that is not
	// real; never a missed one.
	if _, err := tx.ExecContext(txCtx, "SET LOCAL extra_float_digits = 1"); err != nil {
		return fail(classifySave(ctx, err, "Starting the save"))
	}
	for _, s := range stmts {
		res, err := tx.ExecContext(txCtx, s.sql, s.args...)
		if err != nil {
			return rowFail(s.rc, classifySave(ctx, err, "Saving the row"))
		}
		n, err := res.RowsAffected()
		if err != nil {
			return rowFail(s.rc, classifySave(ctx, err, "Saving the row"))
		}
		switch {
		case n == 0:
			return rowFail(s.rc, &database.Error{
				Kind:    database.KindConflict,
				Message: "The row was changed or deleted by someone else, or a trigger, rule or row-level security policy skipped the update.",
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
		return fail(classifySave(ctx, ctx.Err(), "Saving"))
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return commitFailed(err)
	}
	return nil
}

// commitFailed classifies an error of COMMIT. When the server answered with
// an error, or said the transaction was rolled back, nothing was written.
// Anything else (the connection died, the reply never came) leaves the
// outcome unknown, and the changes must not be sent again without a look.
func commitFailed(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) || errors.Is(err, pgx.ErrTxCommitRollback) {
		return &database.SaveError{Err: classifySave(context.Background(), err, "Saving")}
	}
	if errors.Is(err, sql.ErrTxDone) {
		return &database.SaveError{Err: classifySave(context.Background(), err, "Saving")}
	}
	return &database.SaveError{
		Outcome: database.SaveUnknown,
		Err: &database.Error{
			Kind:    database.KindConnection,
			Message: "The connection failed while the save was being committed, so it is not known whether the changes were saved.",
			Hint:    "Reload the table to check before saving again.",
			Err:     err,
		},
	}
}

// classifySave turns an error of a write into a *database.Error. The codes
// that matter to a write are classified here; everything else, including
// cancellation and timeouts of ctx, goes through classifyQuery.
func classifySave(ctx context.Context, err error, what string) *database.Error {
	var pg *pgconn.PgError
	if ctx.Err() == nil && errors.As(err, &pg) {
		fail := func(kind database.Kind, msg, hint string) *database.Error {
			return &database.Error{Kind: kind, Message: msg, Hint: hint, Err: err}
		}
		switch {
		case strings.HasPrefix(pg.Code, "23"): // integrity constraint violation
			return fail(database.KindValidation, "A constraint of the table rejected the change: "+pg.Message, "Nothing was saved.")
		case pg.Code == "40001" || pg.Code == "40P01" || pg.Code == "55P03":
			// serialization failure, deadlock, lock not available
			return fail(database.KindConflict, "Another transaction was in the way: "+pg.Message, "Nothing was saved. Try again.")
		case pg.Code == "25006": // read_only_sql_transaction
			return fail(database.KindPermission, "The session is read-only: "+pg.Message, "")
		case pg.Code == "42501":
			return fail(database.KindPermission, "Permission denied: "+pg.Message, "Your user may not update this table or column. Nothing was saved.")
		}
	}
	return classifyQuery(ctx, err, what)
}
