package grid

// Column describes a column of a grid.
type Column struct {
	// ID distinguishes SQL columns with identical labels; empty uses Name.
	ID         string
	Name       string
	NativeType string
	Kind       Kind
	Width      float32
	// Editable columns open a cell editor. When a column is not editable,
	// Reason says why, for the tooltip of its cells.
	Editable   bool
	Reason     string
	Sortable   bool
	PrimaryKey bool
}

// Source is what the grid shows: the rows of the page in view. The grid
// asks for values only of the rows it draws, so a source can compute or look
// them up cheaply; it never copies the page.
type Source interface {
	// Len is the number of rows in view.
	Len() int
	Columns() []Column
	Value(row, col int) Value
	// RowID is a number that identifies the row for as long as the source
	// does not change; local drafts are keyed by it.
	RowID(row int) int64
	// RowKey identifies the row for the list's selection: it must be
	// canonical (the same row has the same key) and never a display string.
	RowKey(row int) any
	// ReadOnly reports that nothing in the source can be edited.
	ReadOnly() bool
}
