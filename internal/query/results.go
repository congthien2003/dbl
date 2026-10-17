package query

import (
	"strconv"

	"dbl/internal/database"
	"dbl/internal/grid"
)

type resultPage struct {
	result     *database.SQLResult
	start, end int
	columns    []grid.Column
}

func PageCount(r *database.SQLResult) int {
	if r == nil {
		return 1
	}
	return max(1, (len(r.Rows)+database.SQLPageSize-1)/database.SQLPageSize)
}
func ResultSource(r *database.SQLResult, page int) grid.Source {
	p := &resultPage{result: r}
	if r == nil {
		return p
	}
	page = max(1, min(page, PageCount(r)))
	p.start = min((page-1)*database.SQLPageSize, len(r.Rows))
	p.end = min(p.start+database.SQLPageSize, len(r.Rows))
	for i, c := range r.Columns {
		p.columns = append(p.columns, grid.Column{ID: "sql:" + strconv.Itoa(i), Name: c.Name, NativeType: c.TypeName, Kind: c.Kind, Width: 160, Reason: "SQL results are read-only."})
	}
	return p
}
func (p *resultPage) Len() int                      { return p.end - p.start }
func (p *resultPage) Columns() []grid.Column        { return p.columns }
func (p *resultPage) Value(row, col int) grid.Value { return p.result.Rows[p.start+row][col] }
func (p *resultPage) RowID(row int) int64           { return int64(p.start + row) }
func (p *resultPage) RowKey(row int) any            { return p.start + row }
func (p *resultPage) ReadOnly() bool                { return true }
