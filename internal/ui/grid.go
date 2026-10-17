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
// of Padding and ScrollbarInsets, so the cells of the last row are taller,
// and only while the bar shows (gridView.barShown).
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
	st      *app.State
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
	// base is the theme of the window. The table is made with a paler
	// accent (gridTheme), which its rows and cells are built with; what
	// needs the full accent takes it from base.
	base *mygoui.Theme
	// viewW is the width of the table, and barShown whether the columns are
	// wider than it, so that the horizontal scroll bar shows: both as the
	// last frame laid them out.
	viewW    float32
	barShown bool
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
	// The columns run from the leftmost left edge to the rightmost right
	// edge, wherever the table is scrolled to.
	shown := false
	if len(edges) > 0 && len(g.left) > 0 {
		first := edges[len(edges)-1]
		for _, x := range g.left {
			first = min(first, x)
		}
		shown = edges[len(edges)-1]-first > g.viewW+0.5
	}
	if shown != g.barShown {
		g.barShown = shown
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
	g.st = st
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
	// The row chosen is paler here than MyGo's full accent: the rows are
	// built with the theme the table is made with, which is put back right
	// after.
	g.base = c.Theme()
	gt := gridTheme(g.base)
	c.SetTheme(&gt)
	tbl := mygoui.Table(c, &g.list, g.columns(src), src.Len(), func(row, col int) {
		g.buildCell(c, tab, p, row, col)
	}).Grow(1).Label("Grid")
	c.SetTheme(g.base)
	g.viewW = tbl.LayoutBounds().W
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
	// Shift+Delete makes the cell NULL, which typing cannot: an empty editor
	// is the empty string.
	if c.Shortcut(mygoui.Shift, mygoui.KeyDelete) {
		g.setNull(c, tab, g.cell)
	}
}

func (g *gridView) buildCell(c *mygoui.Context, tab *app.Tab, p Palette, row, col int) {
	t := c.Theme()
	pos := cellPos{row, col}
	editing := g.edit != nil && g.edit.pos == pos
	v, dirty := g.value(tab, row, col)
	rowErr := ""
	if tab.Data != nil && dirty {
		rowErr = tab.Data.RowErrors[tab.Source.RowID(row)]
	}
	// The box fills the content of its cell, so it is as high as the row
	// less the padding MyGo's table gives a cell (Space(1.5) above and
	// below, Space(2.5) at the sides).
	box := mygoui.Row(c).Grow(1).AlignSelf(mygoui.Stretch).AlignItems(mygoui.Center)
	if row == tab.Source.Len()-1 && g.barShown {
		box.Padding(0, 0, barRoom, 0)
	}
	padX, padY := t.Space(2.5), t.Space(1.5)
	if r := box.LayoutBounds(); r.W > 0 {
		g.next[col] = r.X + r.W + padX
		g.nextLeft[col] = r.X - padX
	}
	var background mygoui.Color
	switch {
	case rowErr != "":
		background = p.Error
		box.Tooltip(rowErr)
	case dirty:
		background = p.Dirty
	case g.cell == pos:
		background = p.ActiveCell
	}
	// Paint the background and rules over the whole padded cell, matching
	// the selection border. Rules stay above the background.
	box.Draw(func(pt *mygoui.Painter, r mygoui.Rect) {
		x0, y0 := r.X-padX, r.Y-padY
		w, h := r.W+2*padX, r.H+2*padY
		if background.A > 0 {
			pt.Fill(mygoui.Rect{X: x0, Y: y0, W: w, H: h}, background, 0)
		}
		pt.Fill(mygoui.Rect{X: x0, Y: y0 + h - 1, W: w, H: 1}, p.GridLine, 0)
		pt.Fill(mygoui.Rect{X: x0 + w - 1, Y: y0, W: 1, H: h}, p.GridLine, 0)
		g.paintBottom = max(g.paintBottom, y0+h)
	})
	if g.cell == pos {
		// Selection covers the table's padding without taking layout space.
		box.DrawOver(func(pt *mygoui.Painter, r mygoui.Rect) {
			pt.Stroke(mygoui.Rect{X: r.X - padX, Y: r.Y - padY, W: r.W + 2*padX, H: r.H + 2*padY}, g.base.Accent, 0, 1)
		})
	}
	box.Children(func() {
		if editing {
			g.buildEditor(c, tab)
			return
		}
		text := mygoui.Text(c, v.Display(maxCellRunes)).SingleLine()
		if v.Kind == grid.KindNull || (v.Text == "" && v.Kind != grid.KindBinary) {
			text.TextColor(p.Null).Italic()
		}
	})
	if editing {
		return
	}
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

// editBlock says why the cell at pos cannot be edited now, or "" when it
// can: a column that never can be (the key, a binary column…), or a table that
// is being saved, loading or not yet read again after a save.
func (g *gridView) editBlock(tab *app.Tab, pos cellPos) string {
	src := tab.Source
	cols := src.Columns()
	if pos.Row < 0 || pos.Col < 0 || pos.Col >= len(cols) || pos.Row >= src.Len() {
		return "No cell."
	}
	if !cols[pos.Col].Editable {
		if r := cols[pos.Col].Reason; r != "" {
			return r
		}
		return "This cell cannot be edited."
	}
	if tab.Data != nil {
		return tab.Data.EditLock()
	}
	return ""
}

// note is the line under the toolbar about the cell chosen: why it cannot be
// edited, or how to make it NULL.
func (g *gridView) note(tab *app.Tab) string {
	if g.cell == noCell || g.edit != nil {
		return ""
	}
	if why := g.editBlock(tab, g.cell); why != "" {
		return "Cannot edit this cell: " + why
	}
	return "F2 edits the cell · Shift+Delete makes it NULL"
}

// startEdit opens the editor on pos unless the cell cannot be edited.
func (g *gridView) startEdit(c *mygoui.Context, tab *app.Tab, pos cellPos) {
	if g.editBlock(tab, pos) != "" {
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

// setNull records NULL as the draft of a cell, if the column takes it.
func (g *gridView) setNull(c *mygoui.Context, tab *app.Tab, pos cellPos) {
	if g.editBlock(tab, pos) != "" {
		return
	}
	if d := tab.Data; d != nil && !d.Meta.Columns[pos.Col].Nullable {
		return
	}
	g.setDraft(tab, pos, grid.Value{Kind: grid.KindNull})
	c.Invalidate()
}

// setDraft keeps v as the local draft of a cell. Nothing is written to any
// database: Save does that.
func (g *gridView) setDraft(tab *app.Tab, pos cellPos, v grid.Value) {
	if g.st != nil {
		g.st.SetDraft(tab, pos.Row, pos.Col, v)
		return
	}
	src := tab.Source
	tab.Drafts.Set(grid.CellRef{RowID: src.RowID(pos.Row), Col: pos.Col}, src.Value(pos.Row, pos.Col), v)
}

func (g *gridView) buildEditor(c *mygoui.Context, tab *app.Tab) {
	e := g.edit
	// The cell supplies padding and selection border in both modes. The
	// editor only adds editable text, with the window's selection color.
	in := mygoui.TextInputBase(c.Key("cell-edit"), &e.text).
		AutoFocus().Grow(1).MinWidth(0).AlignSelf(mygoui.Stretch).
		SelectionColor(g.base.Selection).FocusRing(false).Label("Cell editor")
	switch {
	case in.Submitted():
		g.commit(c, tab)
		g.refocus = true
	case in.Shortcut(0, mygoui.KeyEscape):
		g.cancel(c)
		g.refocus = true
	}
}

// commit keeps the edit as a local draft. Nothing is written anywhere: Save
// does that. An edit that left the text as it was changes nothing, so opening
// a NULL cell and pressing Enter does not turn it into an empty string.
func (g *gridView) commit(c *mygoui.Context, tab *app.Tab) {
	e := g.edit
	g.edit = nil
	c.Invalidate()
	if e == nil || e.text == e.initial {
		return
	}
	kind := tab.Source.Columns()[e.pos.Col].Kind
	g.setDraft(tab, e.pos, grid.Value{Kind: kind, Text: e.text})
}

// cancel throws away what was typed.
func (g *gridView) cancel(c *mygoui.Context) {
	g.edit = nil
	c.Invalidate()
}

// revert drops every unsaved edit of the tab.
func (g *gridView) revert(c *mygoui.Context, tab *app.Tab) {
	g.edit = nil
	if g.st != nil {
		g.st.RevertEdits(tab)
	} else {
		tab.Drafts.Revert()
	}
	c.Invalidate()
}
