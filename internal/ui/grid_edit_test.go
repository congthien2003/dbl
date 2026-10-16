package ui

import (
	"strings"
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/grid"
	"dbl/internal/storage"
)

// selectNameCell opens users, selects the first plain name cell and returns
// the tab, its grid view and the row.
func selectNameCell(t *testing.T) (*Shell, *mygoui.Tester, *app.Tab, *gridView, int) {
	t.Helper()
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	g := gridOf(t, sh)
	row := plainNameRow(t, tab.Dataset)
	clickCell(t, tt, tab.Dataset, row, 1)
	return sh, tt, tab, g, row
}

func TestF2OpensEditorEnterCommitsDraft(t *testing.T) {
	_, tt, tab, g, row := selectNameCell(t)
	tt.Key(0, mygoui.KeyF2)
	if g.edit == nil || g.edit.pos != (cellPos{row, 1}) {
		t.Fatalf("F2 did not open the editor on the selected cell: %+v", g.edit)
	}
	tt.Type("hello")
	tt.Key(0, mygoui.KeyEnter)
	if g.edit != nil {
		t.Error("Enter left the editor open")
	}
	draft, ok := draftAt(tab, row, 1)
	if !ok || !strings.Contains(draft.Text, "hello") || tab.Drafts.Len() != 1 {
		t.Fatalf("draft = %+v, %v, %d drafts", draft, ok, tab.Drafts.Len())
	}
	if !tt.HasText("Unsaved changes") {
		t.Error("tab does not show the unsaved marker")
	}
}

func TestDoubleClickOpensEditor(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	g := gridOf(t, sh)
	row := plainNameRow(t, tab.Dataset)
	cell := clickCell(t, tt, tab.Dataset, row, 1)
	x, y := center(cell)
	tt.ClickAt(x, y)
	if g.edit == nil {
		t.Error("a double click did not open the editor")
	}
}

func TestEscapeDiscardsEditorValue(t *testing.T) {
	_, tt, tab, g, _ := selectNameCell(t)
	tt.Key(0, mygoui.KeyF2)
	tt.Type("typed")
	tt.Key(0, mygoui.KeyEscape)
	if g.edit != nil || tab.Drafts.Len() != 0 {
		t.Errorf("after Escape: editor %+v, %d drafts", g.edit, tab.Drafts.Len())
	}
	if tt.HasText("Unsaved changes") {
		t.Error("Escape left an unsaved marker")
	}
}

func TestEnterWithoutChangingTextCreatesNoDraft(t *testing.T) {
	_, tt, tab, g, _ := selectNameCell(t)
	tt.Key(0, mygoui.KeyF2)
	tt.Key(0, mygoui.KeyEnter)
	if g.edit != nil || tab.Drafts.Len() != 0 {
		t.Errorf("unchanged edit: editor %+v, %d drafts", g.edit, tab.Drafts.Len())
	}
}

func TestEditBackToOriginalClearsDirty(t *testing.T) {
	_, tt, tab, g, row := selectNameCell(t)
	original := tab.Dataset.Value(row, 1).Text
	tt.Key(0, mygoui.KeyF2)
	tt.Type("x")
	tt.Key(0, mygoui.KeyEnter)
	if tab.Drafts.Len() != 1 {
		t.Fatalf("setup: %d drafts, want 1", tab.Drafts.Len())
	}
	tt.Key(0, mygoui.KeyF2)
	if g.edit == nil {
		t.Fatal("second F2 did not open the editor")
	}
	g.edit.text = original
	tt.Frame()
	tt.Key(0, mygoui.KeyEnter)
	if tab.Drafts.Len() != 0 || tab.Dirty() {
		t.Errorf("editing back to %q left %d drafts", original, tab.Drafts.Len())
	}
	if tt.HasText("Unsaved changes") {
		t.Error("tab still shows the unsaved marker")
	}
}

func TestClickingAnotherCellCommitsEdit(t *testing.T) {
	_, tt, tab, g, row := selectNameCell(t)
	other := row + 1
	for ; other < 40; other++ {
		if v := tab.Dataset.Value(other, 1); v.Kind == grid.KindText && strings.HasPrefix(v.Text, "Name ") {
			break
		}
	}
	tt.Key(0, mygoui.KeyF2)
	tt.Type("kept")
	clickCell(t, tt, tab.Dataset, other, 1)
	if g.edit != nil {
		t.Error("clicking another cell left the editor open")
	}
	if draft, ok := draftAt(tab, row, 1); !ok || !strings.Contains(draft.Text, "kept") {
		t.Errorf("draft = %+v, %v; want the typed text kept", draft, ok)
	}
	if g.cell != (cellPos{other, 1}) {
		t.Errorf("selected cell = %+v, want {%d 1}", g.cell, other)
	}
}

func TestNonEditableCellsIgnoreF2(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	g := gridOf(t, sh)
	// The primary key column.
	clickCell(t, tt, tab.Dataset, 0, 0)
	tt.Key(0, mygoui.KeyF2)
	if g.edit != nil {
		t.Error("the id column opened an editor")
	}
	// The binary column.
	for r := 0; r < 25; r++ {
		if tab.Dataset.Value(r, 6).Kind == grid.KindBinary {
			clickCell(t, tt, tab.Dataset, r, 6)
			break
		}
	}
	tt.Key(0, mygoui.KeyF2)
	if g.edit != nil {
		t.Error("a binary cell opened an editor")
	}
	// A view is read-only everywhere.
	view := openTable(t, sh, tt, "active_users")
	vg := gridOf(t, sh)
	r := plainNameRow(t, view.Dataset)
	clickCell(t, tt, view.Dataset, r, 1)
	tt.Key(0, mygoui.KeyF2)
	if vg.edit != nil || view.Drafts.Len() != 0 {
		t.Error("a view cell opened an editor")
	}
}

func TestRevertButtonClearsAllDrafts(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	ds := tab.Dataset
	for r := 0; r < 2; r++ {
		ref := grid.CellRef{RowID: ds.RowID(r), Col: 1}
		tab.Drafts.Set(ref, ds.Value(r, 1), grid.Value{Kind: grid.KindText, Text: "edited"})
	}
	tt.Frame()
	if tab.Drafts.Len() != 2 {
		t.Fatalf("setup: %d drafts", tab.Drafts.Len())
	}
	if err := tt.Click("Revert changes"); err != nil {
		t.Fatal(err)
	}
	if tab.Drafts.Len() != 0 || tt.HasText("Unsaved changes") {
		t.Errorf("after Revert: %d drafts, marker shown: %v", tab.Drafts.Len(), tt.HasText("Unsaved changes"))
	}
	_ = sh
}

func TestCtrlCWhileEditingDoesNotCopyCell(t *testing.T) {
	_, tt, _, g, _ := selectNameCell(t)
	tt.Key(0, mygoui.KeyF2)
	if g.edit == nil {
		t.Fatal("editor did not open")
	}
	tt.Key(0, mygoui.KeyEnd) // collapse any selection
	tt.SetClipboard("keep")
	tt.Key(mygoui.Cmd, mygoui.KeyC)
	if got := tt.Clipboard(); got != "keep" {
		t.Errorf("Ctrl+C in the editor changed the clipboard to %q", got)
	}
}

func TestEditorSurvivesThemeSwitch(t *testing.T) {
	sh, tt, _, g, _ := selectNameCell(t)
	sh.st.SetThemeSource = func(storage.ThemePref) {}
	tt.Key(0, mygoui.KeyF2)
	tt.Type("abc")
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if g.edit == nil || !strings.Contains(g.edit.text, "abc") {
		t.Errorf("editor after the theme switch: %+v", g.edit)
	}
}

func TestDraftsDoNotLeakAcrossTabs(t *testing.T) {
	sh, tt := newTestShell(t)
	users := openTable(t, sh, tt, "users")
	ref := grid.CellRef{RowID: 1, Col: 1}
	users.Drafts.Set(ref, users.Dataset.Value(0, 1), grid.Value{Kind: grid.KindText, Text: "edited"})
	orders := openTable(t, sh, tt, "orders")
	if orders.Dirty() || !users.Dirty() {
		t.Errorf("orders dirty=%v users dirty=%v", orders.Dirty(), users.Dirty())
	}
	sh.st.Workspace.Select(users.ID)
	tt.Frame()
	if users.Drafts.Len() != 1 {
		t.Errorf("users lost its draft: %d", users.Drafts.Len())
	}
}
