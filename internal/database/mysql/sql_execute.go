package mysql

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

var errSQLResultLimit = errors.New("mysql: result limit reached")

// Execute runs the statements of text one after the other, on the session's
// connection, and keeps the result of each. It stops at the first failure;
// what ran before it stays as it is, and the transaction state says whether
// a transaction is still open. A write is never run twice: a result is read
// once, within the limits of the editor.
//
// Canceling ctx, or the editor's time limit, sends KILL QUERY from another
// connection, which stops the statement and keeps the session and its
// transaction. Only when the statement does not stop is the connection
// dropped, and the session is then lost.
func (s *sqlSession) Execute(ctx context.Context, text string) (*database.SQLExecution, error) {
	if !s.op.TryLock() {
		return nil, sqlValidation("A query is already running in this SQL tab.")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, &database.Error{Kind: database.KindConnection, Message: "The SQL session is closed. Open a new session to continue."}
	}
	run, cancel := context.WithTimeout(ctx, database.SQLTimeout)
	s.active = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.active = nil; s.mu.Unlock() }()
	if len(text) > database.SQLMaxInputBytes {
		return nil, sqlValidation("SQL input exceeds 8 MiB.")
	}
	st, _ := s.status()
	// Check the whole block before running any of it.
	statements, err := scanSQLStatements(text, st&statusNoBackslashEscapes != 0)
	if err != nil {
		return nil, err
	}
	if run.Err() != nil {
		return nil, classify(run, run.Err(), "run the SQL")
	}

	// The statements run on a context the driver cannot cancel by itself:
	// its cancellation closes the connection. A watcher kills the
	// statement on the server first.
	stmtCtx, stmtCancel := context.WithCancel(context.WithoutCancel(run))
	finished := make(chan struct{})
	go func() {
		select {
		case <-finished:
			return
		case <-run.Done():
		}
		if s.owner.killQuery(s.id) {
			select {
			case <-finished:
				return
			case <-time.After(killGrace):
			}
		}
		stmtCancel()
	}()

	start := time.Now()
	r := &database.SQLExecution{}
	err = s.runStatements(run, stmtCtx, text, statements, r)
	if err == nil && run.Err() != nil {
		// A killed SLEEP() returns normally; the run was still stopped.
		err = run.Err()
	}
	close(finished)
	stmtCancel()
	r.Elapsed = time.Since(start)
	r.Transaction = s.transaction()
	r.Ready = r.Transaction != database.SQLSessionLost
	if err != nil {
		r.Err = classify(run, err, "run the SQL")
		r.Stop = database.SQLFailed
		switch {
		case errors.Is(err, errSQLResultLimit):
			r.Stop = database.SQLLimitReached
			r.Err = &database.Error{Kind: database.KindValidation, Message: "The query was stopped at the result limit. Results are partial; check the transaction state."}
		case errors.Is(run.Err(), context.DeadlineExceeded) || r.Err.Kind == database.KindTimeout:
			r.Stop = database.SQLTimedOut
		case errors.Is(run.Err(), context.Canceled) || r.Err.Kind == database.KindCanceled:
			r.Stop = database.SQLCanceled
		}
		r.OutcomeUncertain = !r.Ready
		return r, r.Err
	}
	r.Stop = database.SQLCompleted
	return r, nil
}

func (s *sqlSession) runStatements(run, ctx context.Context, text string, statements []sqlStatement, out *database.SQLExecution) error {
	saved := 0
	for _, st := range statements {
		if err := run.Err(); err != nil {
			return err
		}
		if len(out.Results) >= database.SQLMaxResults {
			return errSQLResultLimit
		}
		res, err := s.runOne(ctx, text[st.span.Start:st.span.End], st.words, &saved, out)
		// A statement that failed before returning rows has no result.
		if res != nil && (err == nil || len(res.Columns) > 0) {
			out.Results = append(out.Results, *res)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runOne runs one statement and reads what it returns. It is sent without
// parameters, so the server runs it as text, as the mysql client would. The
// result is nil when the statement failed before it returned anything.
func (s *sqlSession) runOne(ctx context.Context, text string, words []string, saved *int, out *database.SQLExecution) (*database.SQLResult, error) {
	rows, err := s.conn.QueryContext(ctx, text)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if len(names) > database.SQLMaxColumns {
		return nil, errSQLResultLimit
	}
	res := &database.SQLResult{}
	classes := make([]database.ColumnClass, len(names))
	if len(names) > 0 {
		types, _ := rows.ColumnTypes()
		metadata := 0
		for i, name := range names {
			typeName := ""
			if i < len(types) {
				typeName = types[i].DatabaseTypeName()
			}
			lower := strings.ToLower(typeName)
			classes[i] = classOf(strings.TrimPrefix(lower, "unsigned "), lower).Class
			metadata += len(name) + 32
			if metadata > database.SQLMetadataBytes {
				return nil, errSQLResultLimit
			}
			if lower == "" {
				lower = "any"
			}
			res.Columns = append(res.Columns, database.SQLColumn{Name: name, TypeName: lower, Kind: classes[i].GridKind()})
		}
	}

	vals := make([]any, len(names))
	dest := make([]any, len(names))
	for i := range vals {
		dest[i] = &vals[i]
	}
	count, readBytes := int64(0), 0
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return res, err
		}
		count++
		rowBytes := len(names) * 32
		row := make([]grid.Value, len(names))
		for i, v := range vals {
			row[i] = resultValue(classes[i], v)
			rowBytes += len(row[i].Text) + row[i].Size
		}
		readBytes += rowBytes
		if count > database.SQLReadRows || readBytes > database.SQLReadBytes {
			res.HasRowsAffected, res.RowsAffected = true, count-1
			return res, errSQLResultLimit
		}
		if len(res.Rows) >= database.SQLSnapshotRows || *saved+rowBytes > database.SQLSnapshotBytes {
			res.Truncated = true
			continue
		}
		res.Rows = append(res.Rows, row)
		*saved += rowBytes
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	if err := rows.Close(); err != nil {
		return res, err
	}

	// The rows a statement changed come in its OK packet; read them before
	// anything else runs on the connection.
	affected, hasAffected := s.lastAffected()
	// SHOW WARNINGS is a diagnostic statement: it reads the warnings of the
	// statement before it without clearing them. (SELECT @@warning_count
	// would clear the list it counts.)
	if err := s.readWarnings(ctx, out); err != nil {
		return res, err
	}

	verb := ""
	if len(words) > 0 {
		verb = words[0]
	}
	switch {
	case len(names) > 0:
		res.HasRowsAffected, res.RowsAffected = true, count
		res.CommandTag = strings.TrimSpace(verb+" ") + " " + strconv.FormatInt(count, 10)
	default:
		res.CommandTag = commandTag(words)
		if hasAffected {
			res.HasRowsAffected, res.RowsAffected = true, affected
			res.CommandTag += " " + strconv.FormatInt(affected, 10)
		}
	}
	res.CommandComplete = true
	return res, nil
}

// readWarnings adds the warnings of the last statement to the notices,
// within the editor's limits.
func (s *sqlSession) readWarnings(ctx context.Context, out *database.SQLExecution) error {
	rows, err := s.conn.QueryContext(ctx, "SHOW WARNINGS LIMIT "+strconv.Itoa(database.SQLMaxNotices))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var level, msg string
		var code int
		if err := rows.Scan(&level, &code, &msg); err != nil {
			return err
		}
		if len(out.Notices) >= database.SQLMaxNotices {
			out.DroppedNotices++
			continue
		}
		notice := fmt.Sprintf("%s %d: %s", level, code, msg)
		if len(notice) > database.SQLNoticeBytes {
			notice = notice[:database.SQLNoticeBytes]
		}
		out.Notices = append(out.Notices, notice)
	}
	return rows.Err()
}

// resultValue converts a value of a SQL tab result. The text protocol gives
// integers and floating-point numbers as Go numbers and everything else as
// the bytes the server printed.
func resultValue(class database.ColumnClass, v any) grid.Value {
	switch x := v.(type) {
	case nil:
		return grid.Value{Kind: grid.KindNull}
	case []byte:
		if class == database.ClassBinary {
			return grid.Value{Kind: grid.KindBinary, Size: len(x)}
		}
		return grid.Value{Kind: class.GridKind(), Text: string(x)}
	case int64:
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatInt(x, 10)}
	case uint64:
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatUint(x, 10)}
	case float32:
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatFloat(float64(x), 'g', -1, 32)}
	case float64:
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatFloat(x, 'g', -1, 64)}
	case string:
		return grid.Value{Kind: class.GridKind(), Text: x}
	}
	return grid.Value{Kind: grid.KindText, Text: fmt.Sprint(v)}
}

// commandTag names a statement that returned no rows: its verb and, for
// CREATE, DROP and ALTER, the kind of object.
func commandTag(words []string) string {
	if len(words) == 0 {
		return "SQL"
	}
	switch words[0] {
	case "CREATE", "DROP", "ALTER":
		for _, w := range words[1:] {
			switch w {
			case "TABLE", "INDEX", "VIEW", "TRIGGER", "PROCEDURE", "FUNCTION", "EVENT", "DATABASE", "SCHEMA":
				return words[0] + " " + w
			}
		}
	}
	return words[0]
}
