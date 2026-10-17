package postgres

import (
	"context"
	"errors"
	"strings"

	"dbl/internal/database"
	"dbl/internal/grid"
)

func (s *sqlSession) readExecution(ctx context.Context, text string, out *database.SQLExecution) error {
	reader := s.conn.PgConn().Exec(ctx, text)
	metadata, saved := 0, 0
	for reader.NextResult() {
		if len(out.Results) >= database.SQLMaxResults {
			s.abortSQLTransport()
			return errSQLResultLimit
		}
		rr := reader.ResultReader()
		fields := rr.FieldDescriptions()
		if len(fields) > database.SQLMaxColumns {
			s.abortSQLTransport()
			return errSQLResultLimit
		}
		r := database.SQLResult{}
		for _, field := range fields {
			metadata += len(field.Name) + 32
			if metadata > database.SQLMetadataBytes {
				s.abortSQLTransport()
				return errSQLResultLimit
			}
			r.Columns = append(r.Columns, sqlColumn(string(field.Name), field.DataTypeOID))
		}
		out.Results = append(out.Results, r)
		result := &out.Results[len(out.Results)-1]
		for rr.NextRow() {
			values := rr.Values()
			if len(values) != len(result.Columns) {
				s.abortSQLTransport()
				return errors.New("SQL row has inconsistent metadata")
			}
			bytes := len(values) * 32
			for _, raw := range values {
				bytes += len(raw)
			}
			if len(result.Rows) >= database.SQLSnapshotRows || saved+bytes > database.SQLSnapshotBytes {
				result.Truncated = true
				continue
			}
			row := make([]grid.Value, len(values))
			for i, raw := range values {
				row[i] = sqlValue(raw, result.Columns[i])
			}
			result.Rows = append(result.Rows, row)
			saved += bytes
		}
		tag, err := rr.Close()
		if tag.String() != "" {
			result.CommandTag, result.CommandComplete = tag.String(), true
			result.HasRowsAffected = tag.Select() || tag.Insert() || tag.Update() || tag.Delete() || strings.HasPrefix(tag.String(), "MERGE ") || strings.HasPrefix(tag.String(), "COPY ")
			if result.HasRowsAffected {
				result.RowsAffected = tag.RowsAffected()
			}
		}
		if err != nil {
			terminal := reader.Close()
			out.Ready = !s.conn.IsClosed() && !s.conn.PgConn().IsBusy()
			if terminal != nil {
				return terminal
			}
			return err
		}
	}
	err := reader.Close()
	out.Ready = !s.conn.IsClosed() && !s.conn.PgConn().IsBusy()
	return err
}

func (s *sqlSession) abortSQLTransport() {
	_ = s.transport.Close()
	ctx, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
	defer cancel()
	_ = s.conn.Close(ctx)
}
