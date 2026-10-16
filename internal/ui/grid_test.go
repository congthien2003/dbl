package ui

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/grid"
)

func TestOnlyVisibleRowsAreBuilt(t *testing.T) {
	sh, tt := newTestShell(t)
	openTable(t, sh, tt, "orders") // 1,000,000 rows
	if n := len(tt.Texts()); n >= 1500 {
		t.Errorf("%d texts on screen, want fewer than 1500 for a million rows", n)
	}
	if _, ok := tt.Find("1"); !ok {
		t.Error("the first row's id is not on screen")
	}
	if _, ok := tt.Find("500000"); ok {
		t.Error("row 500000 is on screen without scrolling")
	}
}

func TestScrollingShowsLaterRows(t *testing.T) {
	sh, tt := newTestShell(t)
	openTable(t, sh, tt, "orders")
	first, ok := tt.Find("1")
	if !ok {
		t.Fatal("the first row's id is not on screen")
	}
	x, y := center(first)
	for range 40 {
		tt.Scroll(x, y, 0, 400)
	}
	if _, ok := tt.Find("1"); ok {
		t.Error("row 1 is still on screen after scrolling far down")
	}
	if n := len(tt.Texts()); n >= 1500 {
		t.Errorf("%d texts on screen after scrolling, want fewer than 1500", n)
	}
}

func TestSelectCellMarksOneCellAndRow(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	g := gridOf(t, sh)
	r1 := plainNameRow(t, tab.Source)
	clickCell(t, tt, tab.Source, r1, 1)
	if g.cell != (cellPos{r1, 1}) || g.row != r1 {
		t.Fatalf("after the first click: cell %+v row %d, want cell {%d 1} row %d", g.cell, g.row, r1, r1)
	}
	r2 := r1 + 1
	for ; r2 < 40; r2++ {
		if v := tab.Source.Value(r2, 1); v.Kind == grid.KindText && len(v.Text) > 5 && v.Text[:5] == "Name " {
			break
		}
	}
	clickCell(t, tt, tab.Source, r2, 1)
	if g.cell != (cellPos{r2, 1}) || g.row != r2 {
		t.Errorf("after the second click: cell %+v row %d, want cell {%d 1} row %d", g.cell, g.row, r2, r2)
	}
}

func TestCopySelectedCellCopiesFullValue(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	ds := tab.Source

	// A plain text cell.
	r := plainNameRow(t, ds)
	clickCell(t, tt, ds, r, 1)
	tt.SetClipboard("seed")
	tt.Key(mygoui.Cmd, mygoui.KeyC)
	if got, want := tt.Clipboard(), ds.Value(r, 1).CopyText(); got != want {
		t.Errorf("copied %q, want %q", got, want)
	}

	// The first NULL on screen copies as the empty string.
	nullRow, nullCol := -1, -1
find:
	for row := 0; row < 25; row++ {
		for col := 1; col < len(ds.Columns()); col++ {
			if ds.Value(row, col).Kind == grid.KindNull {
				nullRow, nullCol = row, col
				break find
			}
		}
	}
	if nullRow < 0 {
		t.Fatal("no NULL among the first rows")
	}
	clickCell(t, tt, ds, nullRow, nullCol)
	tt.SetClipboard("seed")
	tt.Key(mygoui.Cmd, mygoui.KeyC)
	if got := tt.Clipboard(); got != "" {
		t.Errorf("NULL copied %q, want the empty string", got)
	}
}

func TestCopyBinaryCellCopiesPlaceholder(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	ds := tab.Source
	row := -1
	for r := 0; r < 25; r++ {
		if ds.Value(r, 6).Kind == grid.KindBinary {
			row = r
			break
		}
	}
	if row < 0 {
		t.Fatal("no binary value among the first rows")
	}
	cell := clickCell(t, tt, ds, row, 6)
	_ = cell
	tt.SetClipboard("seed")
	tt.Key(mygoui.Cmd, mygoui.KeyC)
	if got, want := tt.Clipboard(), ds.Value(row, 6).CopyText(); got != want {
		t.Errorf("copied %q, want the placeholder %q", got, want)
	}
}

func TestCopyWithoutSelectionKeepsClipboard(t *testing.T) {
	sh, tt := newTestShell(t)
	openTable(t, sh, tt, "users")
	tt.SetClipboard("keep")
	tt.Key(mygoui.Cmd, mygoui.KeyC)
	if got := tt.Clipboard(); got != "keep" {
		t.Errorf("clipboard is %q, want it unchanged", got)
	}
}

func TestWideTableBuildsAndScrolls(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "wide_events") // 200 columns
	if n := len(tt.Texts()); n >= 6000 {
		t.Errorf("%d texts on screen for 200 columns, want fewer than 6000", n)
	}
	last := tab.Source.Columns()[len(tab.Source.Columns())-1].Name
	// id is the primary key: its header keeps room for the key icon with an
	// em space before the name.
	first, ok := tt.Find(" id")
	if !ok {
		t.Fatal("first column header is not on screen")
	}
	x, y := center(first)
	for range 120 {
		tt.Scroll(x, y, 400, 0)
	}
	if _, ok := tt.Find(last); !ok {
		t.Errorf("last column %q is not reachable by scrolling right", last)
	}
}

func TestDatasetWithPageOfDraftsShowsDraftText(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	ds := tab.Source
	r := plainNameRow(t, ds)
	ref := grid.CellRef{RowID: ds.RowID(r), Col: 1}
	tab.Drafts.Set(ref, ds.Value(r, 1), grid.Value{Kind: grid.KindText, Text: "Edited name"})
	tt.Frame()
	if !tt.HasText("Edited name") {
		t.Errorf("grid does not show the draft; texts %q", tt.Texts())
	}
	if !tt.HasText("Unsaved changes") {
		t.Error("tab does not show the unsaved marker")
	}
}

func TestLastRowKeepsRoomForTheScrollBarOnlyWhileItShows(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	g := gridOf(t, sh)
	setWidths := func(w float32) {
		widths := map[string]float32{}
		for _, col := range tab.Source.Columns() {
			widths[col.Name] = w
		}
		g.list.Columns.Widths = widths
		// The edges and the bar are seen one frame after the layout.
		tt.Frame()
		tt.Frame()
	}
	setWidths(60)
	if g.barShown {
		t.Error("narrow columns fit the window, yet room is kept for the scroll bar")
	}
	setWidths(900)
	if !g.barShown {
		t.Error("columns wider than the window, yet no room is kept for the scroll bar")
	}
	setWidths(60)
	if g.barShown {
		t.Error("the columns fit again, yet room is still kept for the scroll bar")
	}
}
