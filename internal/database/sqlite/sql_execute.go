package sqlite

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

var errSQLResultLimit = errors.New("sqlite: result limit reached")

// Execute runs the statements of text one after the other, on the session's
// connection, and keeps the result of each. It stops at the first failure;
// what ran before it stays as it is, and the transaction state says whether
// a transaction is still open. A write is never run twice: a result is read
// once, within the limits of the editor.
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
	// Check the whole block before running any of it.
	statements, err := scanSQLStatements(text)
	if err != nil {
		return nil, err
	}
	if run.Err() != nil {
		return nil, classify(run, run.Err(), "run the SQL")
	}

	start := time.Now()
	r := &database.SQLExecution{}
	err = s.runStatements(run, text, statements, r)
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

func (s *sqlSession) runStatements(ctx context.Context, text string, statements []sqlStatement, out *database.SQLExecution) error {
	saved := 0
	for _, st := range statements {
		if len(out.Results) >= database.SQLMaxResults {
			return errSQLResultLimit
		}
		res, err := s.runOne(ctx, text[st.span.Start:st.span.End], st.words, &saved)
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

// runOne runs one statement and reads what it returns. The result is nil
// when the statement failed before it returned anything.
func (s *sqlSession) runOne(ctx context.Context, text string, words []string, saved *int) (*database.SQLResult, error) {
	before, _, err := s.counters()
	if err != nil {
		return nil, err
	}
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
			declared := ""
			if i < len(types) {
				declared = types[i].DatabaseTypeName()
			}
			classes[i], _ = classOf(declared)
			metadata += len(name) + 32
			if metadata > database.SQLMetadataBytes {
				return nil, errSQLResultLimit
			}
			typeName := strings.ToLower(declared)
			if typeName == "" {
				typeName = "any"
			}
			res.Columns = append(res.Columns, database.SQLColumn{Name: name, TypeName: typeName, Kind: classes[i].GridKind()})
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
			row[i] = toValue(classes[i], v)
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

	verb := ""
	if len(words) > 0 {
		verb = words[0]
	}
	switch {
	case len(names) > 0:
		res.HasRowsAffected, res.RowsAffected = true, count
		res.CommandTag = strings.TrimSpace(verb+" ") + " " + strconv.FormatInt(count, 10)
	default:
		after, last, err := s.counters()
		if err != nil {
			return res, err
		}
		switch {
		case after > before:
			res.HasRowsAffected, res.RowsAffected = true, last
		case isWrite(verb):
			res.HasRowsAffected = true
		}
		res.CommandTag = commandTag(words)
		if res.HasRowsAffected {
			res.CommandTag += " " + strconv.FormatInt(res.RowsAffected, 10)
		}
	}
	res.CommandComplete = true
	return res, nil
}

func isWrite(verb string) bool {
	return verb == "INSERT" || verb == "UPDATE" || verb == "DELETE" || verb == "REPLACE"
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
			case "TABLE", "INDEX", "VIEW", "TRIGGER":
				return words[0] + " " + w
			}
		}
	}
	return words[0]
}
