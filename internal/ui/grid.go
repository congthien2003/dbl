package ui

import (
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/grid"
	"dbl/internal/mock"
)

// maxCellRunes is how much of a value a cell shows; the value itself is
// never cut.
const maxCellRunes = 120

// cellPos is a cell by the index of its visible row and its column.
type cellPos struct{ Row, Col int }

var noCell = cellPos{Row: -1, Col: -1}

// cellEdit is the editor open on one cell: the text being typed and the
// text it started from.
type cellEdit struct {
	pos     cellPos
	text    string
	initial string
}

// gridView is the view state of one Table Inspector grid: row and cell
// selection, the column layout the user arranged, and the cell editor.
type gridView struct {
	list    mygoui.ListState
	row     int     // the row chosen, -1 for none
	cell    cellPos // the cell chosen, noCell for none
	edit    *cellEdit
	cols    []mygoui.TableColumn
	refocus bool // give the keyboard back to the table after an edit
}

func newGridView() *gridView { return &gridView{row: -1, cell: noCell} }

func (g *gridView) columns(ds *mock.Dataset) []mygoui.TableColumn {
	if g.cols == nil {
		g.cols = make([]mygoui.TableColumn, len(ds.Columns))
		for i, col := range ds.Columns {
			g.cols[i] = mygoui.TableColumn{Title: col.Name, ID: col.Name, Width: col.Width, MinWidth: 60}
		}
	}
	return g.cols
}

// value returns what a cell shows and whether it holds an unsaved edit.
func (g *gridView) value(tab *app.Tab, row, col int) (v grid.Value, dirty bool) {
	ds := tab.Dataset
	ref := grid.CellRef{RowID: ds.RowID(row), Col: col}
	if d, ok := tab.Drafts.Get(ref); ok {
		return d, true
	}
	return ds.Value(row, col), false
}

// build draws the grid and handles its keys.
func (g *gridView) build(c *mygoui.Context, tab *app.Tab, p Palette) {
	ds := tab.Dataset
	g.list.Selected = &g.row
	g.list.Key = func(row int) any { return ds.RowID(row) }
	mygoui.Table(c, &g.list, g.columns(ds), ds.Rows, func(row, col int) {
		g.buildCell(c, tab, p, row, col)
	}).Grow(1).Label("Grid")
	if g.refocus {
		g.refocus = false
		g.list.Focus(c)
	}
	if g.edit != nil || g.cell == noCell {
		return
	}
	// Window-wide shortcuts, guarded so they never fight a text input: no
	// editor is open here, and the grid has no other text field.
	if c.Shortcut(mygoui.Cmd, mygoui.KeyC) {
		v, _ := g.value(tab, g.cell.Row, g.cell.Col)
		c.WriteClipboard(v.CopyText())
	}
	if c.Shortcut(0, mygoui.KeyF2) {
		g.startEdit(c, tab, g.cell)
	}
}

func (g *gridView) buildCell(c *mygoui.Context, tab *app.Tab, p Palette, row, col int) {
	t := c.Theme()
	pos := cellPos{row, col}
	if g.edit != nil && g.edit.pos == pos {
		g.buildEditor(c, tab)
		return
	}
	v, dirty := g.value(tab, row, col)
	box := mygoui.Row(c).Grow(1).AlignItems(mygoui.Center)
	switch {
	case dirty:
		box.Background(p.Dirty)
	case g.cell == pos:
		box.Background(p.ActiveCell)
	}
	if g.cell == pos {
		box.Border(1, t.Accent)
	}
	box.Children(func() {
		text := mygoui.Text(c, v.Display(maxCellRunes)).SingleLine()
		if v.Kind == grid.KindNull || (v.Text == "" && v.Kind != grid.KindBinary) {
			text.TextColor(p.Null).Italic()
		}
	})
	if box.Clicked() {
		if g.edit != nil {
			g.commit(c, tab)
		}
		g.cell, g.row = pos, row
		c.Invalidate()
	}
	if box.DoubleClicked() {
		g.cell, g.row = pos, row
		g.startEdit(c, tab, pos)
	}
}

// startEdit opens the editor on pos unless the cell cannot be edited.
func (g *gridView) startEdit(c *mygoui.Context, tab *app.Tab, pos cellPos) {
	if pos.Row < 0 || pos.Col < 0 || pos.Col >= len(tab.Dataset.Columns) {
		return
	}
	if !tab.Dataset.Columns[pos.Col].Editable {
		return
	}
	v, _ := g.value(tab, pos.Row, pos.Col)
	text := ""
	if v.Kind != grid.KindNull {
		text = v.Text
	}
	g.edit = &cellEdit{pos: pos, text: text, initial: text}
	c.Invalidate()
}

func (g *gridView) buildEditor(c *mygoui.Context, tab *app.Tab) {
	e := g.edit
	in := mygoui.TextInput(c.Key("cell-edit"), &e.text).AutoFocus().Grow(1).Label("Cell editor")
	switch {
	case in.Submitted():
		g.commit(c, tab)
		g.refocus = true
	case in.Shortcut(0, mygoui.KeyEscape):
		g.cancel(c)
		g.refocus = true
	}
}

// commit keeps the edit as a local draft. Nothing is written anywhere:
// Phase 1 has no database. An edit that left the text as it was changes
// nothing, so opening a NULL cell and pressing Enter does not turn it into
// an empty string.
func (g *gridView) commit(c *mygoui.Context, tab *app.Tab) {
	e := g.edit
	g.edit = nil
	c.Invalidate()
	if e == nil || e.text == e.initial {
		return
	}
	ds := tab.Dataset
	original := ds.Value(e.pos.Row, e.pos.Col)
	kind := original.Kind
	if kind == grid.KindNull {
		kind = grid.KindText
	}
	ref := grid.CellRef{RowID: ds.RowID(e.pos.Row), Col: e.pos.Col}
	tab.Drafts.Set(ref, original, grid.Value{Kind: kind, Text: e.text})
}

// cancel throws away what was typed.
func (g *gridView) cancel(c *mygoui.Context) {
	g.edit = nil
	c.Invalidate()
}

// revert drops every unsaved edit of the tab.
func (g *gridView) revert(c *mygoui.Context, tab *app.Tab) {
	g.edit = nil
	tab.Drafts.Revert()
	c.Invalidate()
}
