package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// saveCleanupTimeout bounds how long restoring a connection after a Save
// may take before the connection is thrown away instead.
const saveCleanupTimeout = 2 * time.Second

// buildUpdate builds the UPDATE of one changed row. Table and column names
// are quoted, and every value, the identity included, is a bound parameter.
//
// The row is found by its original identity and written only if each column
// being changed still holds the value the user saw (optimistic concurrency,
// spec FR-005). Values are compared as the bytes of the text the server
// prints for them, the same text the grid showed: a collation that ignores
// case or trailing spaces cannot make a changed value pass as the same.
// A key column is also compared with "=" (except a floating-point one,
// whose text is not its exact value), so the server can use its index.
func buildUpdate(m *database.TableMetadata, rc database.RowChange) (string, []any, error) {
	var a args
	var q strings.Builder
	q.WriteString("UPDATE " + quoteTable(m.Ref) + " SET ")
	for i, ch := range rc.Cells {
		if _, ok := m.Column(ch.Column); !ok {
			return "", nil, fmt.Errorf("the table has no column %q", ch.Column)
		}
		if i > 0 {
			q.WriteString(", ")
		}
		q.WriteString(quoteIdent(ch.Column) + " = " + a.add(newValue(ch.Current)))
	}
	q.WriteString(" WHERE ")
	var conds []string
	for _, k := range rc.Key {
		c, ok := m.Column(k.Column)
		if !ok {
			return "", nil, fmt.Errorf("the table has no column %q", k.Column)
		}
		if k.Value.Kind == grid.KindNull {
			return "", nil, fmt.Errorf("the identity column %q holds NULL", k.Column)
		}
		col := quoteIdent(k.Column)
		if !floating(c) {
			conds = append(conds, col+" = "+a.add(k.Value.Text))
		}
		conds = append(conds, sameText(col, k.Value.Text, &a))
	}
	for _, ch := range rc.Cells {
		col := quoteIdent(ch.Column)
		switch ch.Original.Kind {
		case grid.KindNull:
			conds = append(conds, col+" IS NULL")
		case grid.KindBinary:
			return "", nil, fmt.Errorf("the column %q holds a binary value, which cannot be compared", ch.Column)
		default:
			conds = append(conds, sameText(col, ch.Original.Text, &a))
		}
	}
	q.WriteString(strings.Join(conds, " AND "))
	return q.String(), a.list, nil
}

// floating reports whether a column holds floating-point numbers.
func floating(c database.ColumnMetadata) bool {
	t := strings.ToLower(c.NativeType)
	return strings.HasPrefix(t, "float") || strings.HasPrefix(t, "double") || strings.HasPrefix(t, "real")
}

// newValue is the bound value for what the user typed. It is sent as text
// and the server converts it to the column's type; the Save runs in strict
// mode, so a value the column cannot hold is an error, not a silent change.
func newValue(v grid.Value) any {
	if v.Kind == grid.KindNull {
		return nil
	}
	return v.Text
}

// commitGate carries the cancellation of the caller's context to the
// transaction until COMMIT is about to be sent, and not after: interrupting
// a COMMIT would leave its outcome unknown for no gain.
type commitGate struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	committing bool
	done       chan struct{}
}

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

// strictMode adds STRICT_ALL_TABLES to a sql_mode, so the server refuses a
// value it would otherwise truncate or convert with only a warning.
func strictMode(mode string) string {
	for _, m := range strings.Split(mode, ",") {
		if strings.EqualFold(strings.TrimSpace(m), "STRICT_ALL_TABLES") {
			return mode
		}
	}
	if mode == "" {
		return "STRICT_ALL_TABLES"
	}
	return mode + ",STRICT_ALL_TABLES"
}

// SaveChanges writes the changes in one transaction. It returns nil only
// after COMMIT is confirmed; every error is a *database.SaveError. A write
// is never retried.
//
// The Save has a connection of its own, in strict mode for its duration;
// the connection's sql_mode is restored before it goes back to the pool,
// or the connection is discarded.
func (a *Adapter) SaveChanges(ctx context.Context, req database.SaveRequest) error {
	rowFail := func(rc database.RowChange, err *database.Error) error {
		return &database.SaveError{Err: err, RowID: rc.RowID, HasRow: true}
	}
	fail := func(err *database.Error) error { return &database.SaveError{Err: err} }

	if req.Meta == nil || req.Changes.Empty() {
		return fail(&database.Error{Kind: database.KindValidation, Message: "There is nothing to save."})
	}
	if req.Meta.ReadOnly != "" {
		return fail(&database.Error{Kind: database.KindUnsupported, Message: req.Meta.ReadOnly})
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

	conn, err := a.db.Conn(txCtx)
	if err != nil {
		return fail(classify(ctx, err, "start the save"))
	}
	var mode string
	if err := conn.QueryRowContext(txCtx, "SELECT @@SESSION.sql_mode").Scan(&mode); err != nil {
		_ = conn.Close()
		return fail(classify(ctx, err, "start the save"))
	}
	if _, err := conn.ExecContext(txCtx, "SET SESSION sql_mode = ?", strictMode(mode)); err != nil {
		_ = conn.Close()
		return fail(classify(ctx, err, "start the save"))
	}
	defer restoreConn(conn, mode)

	tx, err := conn.BeginTx(txCtx, nil)
	if err != nil {
		return fail(classify(ctx, err, "start the save"))
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
		if msg, err := firstWarning(txCtx, tx); err != nil {
			return rowFail(s.rc, classifySave(ctx, err, "save the row"))
		} else if msg != "" {
			return rowFail(s.rc, &database.Error{
				Kind:    database.KindValidation,
				Message: "The server changed the value while writing it: " + msg,
				Hint:    "Nothing was saved.",
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

// firstWarning returns the first warning the last statement left, or "".
// Notes are not warnings: they say nothing about the value written. SHOW
// WARNINGS reads the list without clearing it; SELECT @@warning_count
// would clear it.
func firstWarning(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, "SHOW WARNINGS")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var level, msg string
		var code int
		if err := rows.Scan(&level, &code, &msg); err != nil {
			return "", err
		}
		if !strings.EqualFold(level, "Note") {
			return msg, rows.Close()
		}
	}
	return "", rows.Err()
}

// restoreConn gives the Save's connection its sql_mode back and returns it
// to the pool. When that fails, the connection is discarded, so no other
// work runs in strict mode by accident.
func restoreConn(conn *sql.Conn, mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), saveCleanupTimeout)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "SET SESSION sql_mode = ?", mode); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}

// commitFailed classifies an error of COMMIT. When the server answered with
// an error, the transaction did not commit. When the connection failed
// instead, the outcome is unknown, and the changes must not be sent again
// without a look.
func commitFailed(err error) error {
	var me *gomysql.MySQLError
	if errors.Is(err, sql.ErrTxDone) || errors.As(err, &me) {
		return &database.SaveError{Err: classifySave(context.Background(), err, "save")}
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

// classifySave turns an error of a write into a *database.Error; a value
// the server refused is a validation error, and nothing was saved.
func classifySave(ctx context.Context, err error, doing string) *database.Error {
	e := classify(ctx, err, doing)
	if e.Kind == database.KindValidation {
		c := *e
		c.Message = "The server rejected the change: " + e.Message
		c.Hint = "Nothing was saved."
		return &c
	}
	return e
}
