package mock

import "dbl/internal/grid"

// Source adapts the dataset to the grid's data source.
func (d *Dataset) Source() grid.Source { return datasetSource{d} }

type datasetSource struct{ d *Dataset }

func (s datasetSource) Len() int { return s.d.Rows }

func (s datasetSource) Columns() []grid.Column {
	cols := make([]grid.Column, len(s.d.Columns))
	for i, c := range s.d.Columns {
		cols[i] = grid.Column{Name: c.Name, Kind: c.Kind, Width: c.Width, Editable: c.Editable, PrimaryKey: c.PrimaryKey}
	}
	return cols
}

func (s datasetSource) Value(row, col int) grid.Value { return s.d.Value(row, col) }
func (s datasetSource) RowID(row int) int64           { return s.d.RowID(row) }
func (s datasetSource) RowKey(row int) any            { return s.d.RowID(row) }
func (s datasetSource) ReadOnly() bool                { return s.d.ReadOnly }
