package ui

import (
	"slices"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/database"
	"dbl/internal/grid"
)

// maxCellRunes is how much of a value a cell shows; the value itself is
// never cut.
const maxCellRunes = 120

// barRoom is the room the last row has under its cells, where the
// horizontal scroll bar (6 DIPs, 3 above the edge) shows without covering
// the values. The rows are a scroll container inside the table, out of reach
// of Padding and ScrollbarInsets, so the cells of the last row are taller.
const barRoom = 12

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
	colSig  string // the columns cols was made for
	refocus bool   // give the keyboard back to the table after an edit
	sort    mygoui.SortOrder
	seq     int // the PageSeq of the rows the selection belongs to

	// edges are the x positions of the right edges of the columns, taken
	// from the cells of the last frame; the lines between columns are drawn
	// at them over the whole table, header and empty room included.
	edges []float32
	next  map[int]float32
	// left are the x positions of the left edges of the columns, taken the
	// same way; the key icons of a header are drawn at them.
	left, nextLeft map[int]float32
	// lastBottom is the bottom of the lowest cell painted in the last frame,
	// where the lines between columns carry on below the rows.
	lastBottom, paintBottom float32
}

func newGridView() *gridView { return &gridView{row: -1, cell: noCell} }

// columns returns the table columns of the source, made again only when the
// source's columns change; the widths the user gave are kept by the list
// state, by column ID.
func (g *gridView) columns(src grid.Source) []mygoui.TableColumn {
	cols := src.Columns()
	sig := ""
	for _, col := range cols {
		sig += col.Name + ""
		if col.PrimaryKey {
			sig += "p"
		}
	}
	if g.cols == nil || g.colSig != sig {
		g.colSig = sig
		g.cols = make([]mygoui.TableColumn, len(cols))
		for i, col := range cols {
			// A header holds text only: the key icon is drawn over it (see
			// drawKeyIcons) in room kept by an em space.
			title := ""
			if col.PrimaryKey {
				title = " "
			}
			g.cols[i] = mygoui.TableColumn{Title: title + col.Name, ID: col.Name, Width: col.Width, MinWidth: 60, Sortable: col.Sortable}
		}
	}
	return g.cols
}

// drawKeyIcons draws the key icon of the headers of the primary key columns,
// in the room their titles keep. A column whose cells were not seen yet (no rows) has no icon.
func (g *gridView) drawKeyIcons(pt *mygoui.Painter, r mygoui.Rect, th *mygoui.Theme, cols []grid.Column) {
	em := th.FontSize
	size := em * 0.9
	headH := th.Space(8)
	k := primaryKeyIcon(th)
	for i, col := range cols {
		left, ok := g.left[i]
		if !ok || !col.PrimaryKey {
			continue
		}
		x := left + th.Space(2.5)
		if x < r.X {
			continue // scrolled under the edge of the table
		}
		pt.Icon(k.svg, mygoui.Rect{X: x, Y: r.Y + (headH-size)/2, W: size, H: size}, k.color)
	}
}

// value returns what a cell shows and whether it holds an unsaved edit.
func (g *gridView) value(tab *app.Tab, row, col int) (v grid.Value, dirty bool) {
	src := tab.Source
	ref := grid.CellRef{RowID: src.RowID(row), Col: col}
	if d, ok := tab.Drafts.Get(ref); ok {
		return d, true
	}
	return src.Value(row, col), false
}

// updateEdges keeps the column edges seen in this frame for the next one,
// and asks for a frame when they moved (a column resized or scrolled).
func (g *gridView) updateEdges(c *mygoui.Context) {
	edges := make([]float32, 0, len(g.next))
	for _, x := range g.next {
		edges = append(edges, x)
	}
	slices.Sort(edges)
	g.left = g.nextLeft
	if !slices.Equal(edges, g.edges) {
		g.edges = edges
		c.Invalidate()
	}
}

// newPage drops what belonged to the rows that were replaced: the
// selection, the editor and the scroll position. The widths the user gave
// the columns stay.
func (g *gridView) newPage() {
	layout := g.list.Columns
	g.list = mygoui.ListState{}
	g.list.Columns = layout
	g.row, g.cell, g.edit = -1, noCell, nil
}

// build draws the grid and handles its keys.
func (g *gridView) build(c *mygoui.Context, st *app.State, tab *app.Tab, p Palette) {
	src := tab.Source
	var d *app.TableData
	if tab.Data != nil {
		d = tab.Data
		if g.seq != d.PageSeq {
			g.seq = d.PageSeq
			g.newPage()
		}
	}
	g.list.Selected = &g.row
	g.list.Key = src.RowKey
	// The sort the table shows is the one the server applies; a click on a
	// header changes g.sort, which is then sent as the new sort.
	var applied mygoui.SortOrder
	if d != nil && d.Sort != nil {
		applied = mygoui.SortOrder{Column: d.Sort.Column, Descending: d.Sort.Desc}
	}
	g.sort = applied
	g.list.Sort = &g.sort
	g.next, g.nextLeft = map[int]float32{}, map[int]float32{}
	tbl := mygoui.Table(c, &g.list, g.columns(src), src.Len(), func(row, col int) {
		g.buildCell(c, tab, p, row, col)
	}).Grow(1).Label("Grid")
	// The lines of the rows are drawn by the cells themselves, so they move
	// with the rows. The lines between columns continue where there are no
	// cells: under the last row, drawn below the table's children so the
	// scroll bars stay on top, and in the header, drawn last so they also
	// replace the short resize mark MyGo draws at the end of each header.
	// DrawOver replaces the table's own, so the focus ring is drawn here.
	th := c.Theme()
	headH := th.Space(8) // the height of a table header (MyGo's table.go)
	tbl.Draw(func(pt *mygoui.Painter, r mygoui.Rect) {
		// The cells paint after this, so what they report is for the next paint.
		g.lastBottom, g.paintBottom = g.paintBottom, 0
		top := max(g.lastBottom, r.Y+headH)
		if g.lastBottom == 0 || top >= r.Y+r.H {
			return
		}
		for _, x := range g.edges {
			pt.Fill(mygoui.Rect{X: x - 1, Y: top, W: 1, H: r.Y + r.H - top}, p.GridLine, 0)
		}
	})
	tbl.DrawOver(func(pt *mygoui.Painter, r mygoui.Rect) {
		if tbl.FocusVisible() {
			pt.FocusRing(r, [4]float32{})
		}
		for _, x := range g.edges {
			pt.Fill(mygoui.Rect{X: x - 1, Y: r.Y, W: 1, H: headH}, p.GridLine, 0)
		}
		g.drawKeyIcons(pt, r, th, src.Columns())
	})
	g.updateEdges(c)
	if d != nil && g.sort != applied {
		var s *database.Sort
		if g.sort.Column != "" {
			s = &database.Sort{Column: g.sort.Column, Desc: g.sort.Descending}
		}
		_ = st.SetSort(tab, s)
		c.Invalidate()
	}
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
	// The box fills the content of its cell, so it is as high as the row
	// less the padding MyGo's table gives a cell (Space(1.5) above and
	// below, Space(2.5) at the sides).
	box := mygoui.Row(c).Grow(1).AlignSelf(mygoui.Stretch).AlignItems(mygoui.Center)
	if row == tab.Source.Len()-1 {
		box.Padding(0, 0, barRoom, 0)
	}
	padX, padY := t.Space(2.5), t.Space(1.5)
	if r := box.LayoutBounds(); r.W > 0 {
		g.next[col] = r.X + r.W + padX
		g.nextLeft[col] = r.X - padX
	}
	// A faint line below and to the right of the cell, over its whole
	// padded area so neighbours join into rules.
	box.Draw(func(pt *mygoui.Painter, r mygoui.Rect) {
		x0, y0 := r.X-padX, r.Y-padY
		w, h := r.W+2*padX, r.H+2*padY
		pt.Fill(mygoui.Rect{X: x0, Y: y0 + h - 1, W: w, H: 1}, p.GridLine, 0)
		pt.Fill(mygoui.Rect{X: x0 + w - 1, Y: y0, W: 1, H: h}, p.GridLine, 0)
		g.paintBottom = max(g.paintBottom, y0+h)
	})
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
	cols := tab.Source.Columns()
	if pos.Row < 0 || pos.Col < 0 || pos.Col >= len(cols) || pos.Row >= tab.Source.Len() {
		return
	}
	if !cols[pos.Col].Editable {
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
	src := tab.Source
	original := src.Value(e.pos.Row, e.pos.Col)
	kind := original.Kind
	if kind == grid.KindNull {
		kind = grid.KindText
	}
	ref := grid.CellRef{RowID: src.RowID(e.pos.Row), Col: e.pos.Col}
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
