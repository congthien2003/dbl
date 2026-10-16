package ui

import (
	"strings"
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/database"
	"dbl/internal/grid"
	"dbl/internal/mock"
	"dbl/internal/storage"
)

// newTestShell returns a shell on the default settings with the local-pg
// connection and its public schema opened in the explorer, drawn in a
// 1280x800 window.
func newTestShell(t *testing.T) (*Shell, *mygoui.Tester) {
	t.Helper()
	st := &app.State{Settings: storage.DefaultSettings()}
	sh := NewShell(st)
	sh.explorer.state.Open.Add("local-pg")
	sh.explorer.state.Open.Add("local-pg/public")
	return sh, mygoui.NewTester(sh.View, 1280, 800)
}

// openTable opens a table of local-pg in the workspace, as the explorer
// does, and draws a frame.
func openTable(t *testing.T, sh *Shell, tt *mygoui.Tester, name string) *app.Tab {
	t.Helper()
	key := app.TableKey{ConnectionID: "local-pg", Ref: database.TableRef{Catalog: "app", Schema: "public", Name: name}}
	ds, ok := mock.DatasetFor(key.ConnectionID, key.Ref)
	if !ok {
		t.Fatalf("no mock table %q", name)
	}
	tab, _ := sh.st.Workspace.OpenTable(key, name, ds)
	tt.Frame()
	return tab
}

// gridOf returns the grid view of the active tab.
func gridOf(t *testing.T, sh *Shell) *gridView {
	t.Helper()
	tab := sh.st.Workspace.Active()
	if tab == nil {
		t.Fatal("no active tab")
	}
	v := sh.views[tab.ID]
	if v == nil || v.grid == nil {
		t.Fatalf("tab %q has no grid view", tab.Title)
	}
	return v.grid
}

func center(r mygoui.Rect) (x, y float32) { return r.X + r.W/2, r.Y + r.H/2 }

// plainNameRow returns the first row whose name column (1) shows an
// ordinary "Name N" text, so the cell can be found by its text alone.
func plainNameRow(t *testing.T, ds *mock.Dataset) int {
	t.Helper()
	for r := 0; r < 40; r++ {
		if v := ds.Value(r, 1); v.Kind == grid.KindText && strings.HasPrefix(v.Text, "Name ") {
			return r
		}
	}
	t.Fatal("no plain name cell in the first rows")
	return -1
}

// clickCell clicks the cell showing the display text of ds.Value(row, col)
// and returns its box.
func clickCell(t *testing.T, tt *mygoui.Tester, ds *mock.Dataset, row, col int) mygoui.Rect {
	t.Helper()
	text := ds.Value(row, col).Display(maxCellRunes)
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("cell (%d,%d) showing %q is not on screen", row, col, text)
	}
	x, y := center(r)
	tt.ClickAt(x, y)
	return r
}

func doubleClickText(t *testing.T, tt *mygoui.Tester, text string) {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no element shows %q", text)
	}
	x, y := center(r)
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
}

func draftAt(tab *app.Tab, row, col int) (grid.Value, bool) {
	return tab.Drafts.Get(grid.CellRef{RowID: tab.Dataset.RowID(row), Col: col})
}
