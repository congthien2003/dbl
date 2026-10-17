package sqlite

import (
	"context"
	"database/sql"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// FetchPage reads one page. The rows are read completely and the cursor is
// closed before it returns, so nothing of the query outlives the call.
func (a *Adapter) FetchPage(ctx context.Context, req database.PageRequest) (*database.PageResult, error) {
	pq, err := buildPageQuery(req)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, pq.SQL, pq.Args...)
	if err != nil {
		return nil, classify(ctx, err, "read the page")
	}
	defer rows.Close()

	cols := req.Meta.Columns
	vals := make([]any, len(cols))
	blobs := make([]sql.NullInt64, len(cols))
	dest := make([]any, 0, 2*len(cols))
	for i := range cols {
		dest = append(dest, &vals[i], &blobs[i])
	}
	out := make([][]grid.Value, 0, min(pq.Size+1, database.MaxPageSize+1))
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, classify(ctx, err, "read the page")
		}
		row := make([]grid.Value, len(cols))
		for i, c := range cols {
			if blobs[i].Valid {
				row[i] = grid.Value{Kind: grid.KindBinary, Size: int(blobs[i].Int64)}
				continue
			}
			row[i] = toValue(c.Class, vals[i])
		}
		out = append(out, row)
		if len(out) > pq.Size {
			break // the extra row says another page exists
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classify(ctx, err, "read the page")
	}
	res := &database.PageResult{}
	if len(out) > pq.Size {
		res.More = true
		out = out[:pq.Size]
	}
	if pq.Reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	res.Rows = out
	return res, nil
}
