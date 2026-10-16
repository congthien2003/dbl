package postgres

import (
	"context"
	"database/sql"

	"dbl/internal/database"
	"dbl/internal/grid"
)

var _ database.SQLAdapter = (*Adapter)(nil)

// FetchPage reads one page. The rows are read completely and the cursor is
// closed before it returns, so nothing of the query outlives the call.
func (a *Adapter) FetchPage(ctx context.Context, req database.PageRequest) (*database.PageResult, error) {
	pq, err := buildPageQuery(req)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, pq.SQL, pq.Args...)
	if err != nil {
		return nil, classifyQuery(ctx, err, "Reading the page")
	}
	defer rows.Close()

	cols := req.Meta.Columns
	texts := make([]sql.NullString, len(cols))
	sizes := make([]sql.NullInt64, len(cols))
	dest := make([]any, len(cols))
	for i := range cols {
		if pq.Binary[i] {
			dest[i] = &sizes[i]
		} else {
			dest[i] = &texts[i]
		}
	}
	out := make([][]grid.Value, 0, min(pq.Size+1, database.MaxPageSize+1))
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, classifyQuery(ctx, err, "Reading the page")
		}
		row := make([]grid.Value, len(cols))
		for i, c := range cols {
			row[i] = toValue(c.Class, texts[i], sizes[i])
		}
		out = append(out, row)
		if len(out) > pq.Size {
			break // the extra row says another page exists
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classifyQuery(ctx, err, "Reading the page")
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

// toValue builds the grid value of a column from what the server sent: the
// text of the value, or for a binary column its length. The text is what
// the server printed, so a numeric keeps every digit and a timestamp keeps
// its zone; the class of the column fixes the kind.
func toValue(class database.ColumnClass, text sql.NullString, size sql.NullInt64) grid.Value {
	if class == database.ClassBinary {
		if !size.Valid {
			return grid.Value{Kind: grid.KindNull}
		}
		return grid.Value{Kind: grid.KindBinary, Size: int(size.Int64)}
	}
	if !text.Valid {
		return grid.Value{Kind: grid.KindNull}
	}
	return grid.Value{Kind: class.GridKind(), Text: text.String}
}
