package ui

import (
	"strings"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/database"
	"dbl/internal/database/fakedb"
	"dbl/internal/grid"
)

// typeInCell selects the cell showing text, opens its editor with F2, replaces
// the text with newText and presses Enter, as a user does.
func typeInCell(t *testing.T, e *uiEnv, text, newText string) {
	t.Helper()
	r, ok := e.tt.Find(text)
	if !ok {
		t.Fatalf("no cell shows %q", text)
	}
	x, y := center(r)
	e.tt.ClickAt(x, y)
	e.tt.Key(0, mygoui.KeyF2)
	e.tt.Key(mygoui.Cmd, mygoui.KeyA)
	e.tt.Type(newText)
	e.tt.Key(0, mygoui.KeyEnter)
}

func dbValue(db *fakedb.Adapter, n, col int) grid.Value {
	return db.Table("public", "users").Rows[n-1][col]
}

func TestEditingACellAndSavingWritesTheDatabase(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 5", "renamed")
	if !tab.Dirty() || !e.tt.HasText("1 unsaved edit(s)") || !e.tt.HasText("renamed") {
		t.Fatalf("after the edit: dirty %v texts %q", tab.Dirty(), e.tt.Texts())
	}
	if got := dbValue(db, 5, 1).Text; got != "user 5" {
		t.Fatalf("Enter wrote to the database: %q", got)
	}
	if err := e.tt.Click("Save changes"); err != nil {
		t.Fatal(err)
	}
	if !tab.Dirty() || !e.tt.HasText("Saving…") {
		t.Errorf("while saving: dirty %v saving text %v", tab.Dirty(), e.tt.HasText("Saving…"))
	}
	e.runPost() // the save is committed
	if tab.Dirty() || dbValue(db, 5, 1).Text != "renamed" {
		t.Fatalf("after commit: dirty %v value %q", tab.Dirty(), dbValue(db, 5, 1).Text)
	}
	if !e.tt.HasText("Saved. Reading the rows again…") {
		t.Errorf("no sign that the rows are read again: %q", e.tt.Texts())
	}
	e.runPost() // the rows are read again
	if tab.Data.Stale || e.tt.HasText("Saved. Reading the rows again…") || !e.tt.HasText("renamed") {
		t.Errorf("after the reload: stale %v texts %q", tab.Data.Stale, e.tt.Texts())
	}
}

func TestCtrlSSavesAndRevertDiscards(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 6", "by shortcut")
	e.tt.Key(mygoui.Cmd, mygoui.KeyS)
	e.runPost()
	if tab.Dirty() || dbValue(db, 6, 1).Text != "by shortcut" {
		t.Fatalf("Ctrl+S: dirty %v value %q", tab.Dirty(), dbValue(db, 6, 1).Text)
	}
	e.runPost()

	typeInCell(t, e, "user 7", "never saved")
	if err := e.tt.Click("Revert changes"); err != nil {
		t.Fatal(err)
	}
	if tab.Dirty() || e.tt.HasText("never saved") || dbValue(db, 7, 1).Text != "user 7" {
		t.Errorf("after Revert: dirty %v", tab.Dirty())
	}
	if len(db.Saves()) != 1 {
		t.Errorf("%d saves", len(db.Saves()))
	}
}

func TestKeyAndBinaryCellsDoNotEdit(t *testing.T) {
	e, _ := newTablesEnv(t)
	tab := openUsers(t, e)
	g := e.sh.views[tab.ID].grid
	r, ok := e.tt.Find("<binary 3 bytes>")
	if !ok {
		t.Fatal("no binary cell")
	}
	x, y := center(r)
	e.tt.ClickAt(x, y)
	e.tt.Key(0, mygoui.KeyF2)
	if g.edit != nil || !e.tt.HasText("Cannot edit this cell: Binary values cannot be edited.") {
		t.Errorf("binary cell: editor %v, texts %q", g.edit, e.tt.Texts())
	}
	// The key column.
	if r, ok := e.tt.Find("3"); ok {
		x, y := center(r)
		e.tt.ClickAt(x, y)
		e.tt.Key(0, mygoui.KeyF2)
		if g.edit != nil {
			t.Fatal("the key column opened an editor")
		}
	}
	if tab.Dirty() {
		t.Error("a cell that cannot be edited became dirty")
	}
}

func TestShiftDeleteMakesACellNullAndEmptyIsNotNull(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	r, _ := e.tt.Find("user 8")
	x, y := center(r)
	e.tt.ClickAt(x, y)
	e.tt.Key(mygoui.Shift, mygoui.KeyDelete)
	if v, ok := draftAt(tab, 7, 1); !ok || v.Kind != grid.KindNull {
		t.Fatalf("draft %+v %v, want NULL", v, ok)
	}
	typeInCell(t, e, "user 9", "")
	if v, ok := draftAt(tab, 8, 1); ok && v.Kind == grid.KindNull {
		t.Errorf("clearing the editor made the cell NULL: %+v", v)
	}
	// The score column is nullable too, but a key column is not editable at all.
	e.tt.Key(mygoui.Cmd, mygoui.KeyS)
	e.runPost()
	if dbValue(db, 8, 1).Kind != grid.KindNull {
		t.Errorf("saved value %+v, want NULL", dbValue(db, 8, 1))
	}
	if v := dbValue(db, 9, 1); v.Kind == grid.KindNull || v.Text != "" {
		t.Errorf("saved value %+v, want the empty string", v)
	}
}

func TestSaveConflictKeepsEditsAndMarksTheRow(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 10", "mine")
	db.Table("public", "users").Rows[9][1] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	if err := e.tt.Click("Save changes"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	if !tab.Dirty() || dbValue(db, 10, 1).Text != "theirs" {
		t.Fatalf("dirty %v value %q", tab.Dirty(), dbValue(db, 10, 1).Text)
	}
	for _, want := range []string{"Nothing was saved.", "Concurrent modification", "mine", "1 unsaved edit(s)"} {
		if !e.tt.HasText(want) && !containsText(e, want) {
			t.Errorf("missing %q: %q", want, e.tt.Texts())
		}
	}
	// The row can be edited again and saved once it is right.
	typeInCell(t, e, "mine", "mine again")
	if tab.Data.RowErrors[tab.Data.RowID(9)] != "" {
		t.Error("editing the row left its error")
	}
}

// containsText reports whether any text on screen contains s.
func containsText(e *uiEnv, s string) bool {
	for _, t := range e.tt.Texts() {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func TestPagingWithEditsAsksSaveDiscardOrCancel(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 11", "edited 11")
	if err := e.tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	if !e.tt.HasText("Unsaved edits") || !containsText(e, "go to the next page") {
		t.Fatalf("no question: %q", e.tt.Texts())
	}
	if tab.Data.Page.Number != 1 {
		t.Fatal("the page changed before the answer")
	}
	// Cancel keeps everything.
	if err := e.tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if e.tt.HasText("Unsaved edits") || tab.Data.Page.Number != 1 || !tab.Dirty() {
		t.Fatalf("after Cancel: page %d dirty %v", tab.Data.Page.Number, tab.Dirty())
	}
	// Save, then the page turns.
	if err := e.tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.Click("Save and continue"); err != nil {
		t.Fatal(err)
	}
	e.runPost() // committed; the reload and the next page start
	if dbValue(db, 11, 1).Text != "edited 11" || tab.Dirty() {
		t.Fatalf("value %q dirty %v", dbValue(db, 11, 1).Text, tab.Dirty())
	}
	e.runPost()
	e.runPost()
	if tab.Data.Page.Number != 2 || !e.tt.HasText("user 101") {
		t.Errorf("page %d: %q", tab.Data.Page.Number, e.tt.Texts())
	}
}

func TestDiscardInThePagingQuestionDropsTheEditsAndTurnsThePage(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 12", "will be dropped")
	if err := e.tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.Click("Discard"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	if tab.Dirty() || tab.Data.Page.Number != 2 || len(db.Saves()) != 0 {
		t.Errorf("dirty %v page %d saves %d", tab.Dirty(), tab.Data.Page.Number, len(db.Saves()))
	}
}

func TestClosingATabWithEditsOffersSave(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 13", "saved on close")
	if err := e.tt.Click("Close users"); err != nil {
		t.Fatal(err)
	}
	if !e.tt.HasText("Close tab with unsaved edits?") || !e.tt.HasText("Save and close") {
		t.Fatalf("no three-way question: %q", e.tt.Texts())
	}
	if err := e.tt.Click("Save and close"); err != nil {
		t.Fatal(err)
	}
	if e.sh.st.Workspace.Find(tab.ID) == nil {
		t.Fatal("the tab closed before the commit")
	}
	e.runPost()
	if e.sh.st.Workspace.Find(tab.ID) != nil || dbValue(db, 13, 1).Text != "saved on close" {
		t.Errorf("tab open %v value %q", e.sh.st.Workspace.Find(tab.ID) != nil, dbValue(db, 13, 1).Text)
	}
}

func TestLeavingTheWindowWithEditsOffersSave(t *testing.T) {
	e, db := newTablesEnv(t)
	openUsers(t, e)
	typeInCell(t, e, "user 14", "saved on exit")
	left := 0
	e.sh.OnQuitConfirmed = func() { left++ }
	if e.sh.st.RequestQuit() {
		t.Fatal("quit allowed with unsaved edits")
	}
	e.tt.Frame()
	if !e.tt.HasText("Exit with unsaved edits?") || !e.tt.HasText("Save and exit") {
		t.Fatalf("no question: %q", e.tt.Texts())
	}
	if err := e.tt.Click("Save and exit"); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatal("left before the commit")
	}
	e.runPost()
	if left != 1 || dbValue(db, 14, 1).Text != "saved on exit" {
		t.Errorf("left %d value %q", left, dbValue(db, 14, 1).Text)
	}
}

func TestDisconnectWithEditsOffersSaveFirst(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 15", "saved before disconnect")
	id := tab.Table.ConnectionID
	if e.sh.st.RequestDisconnect(id) {
		t.Fatal("disconnected without asking")
	}
	e.tt.Frame()
	if !e.tt.HasText("Save changes first") {
		t.Fatalf("no Save option: %q", e.tt.Texts())
	}
	if err := e.tt.Click("Save changes first"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	if dbValue(db, 15, 1).Text != "saved before disconnect" || e.sh.st.Workspace.Find(tab.ID) != nil {
		t.Errorf("value %q tab open %v", dbValue(db, 15, 1).Text, e.sh.st.Workspace.Find(tab.ID) != nil)
	}
}

func TestFilterIsAppliedByHandWhileThereAreEdits(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 16", "edited 16")
	reads := len(db.Requests())
	fb := e.sh.views[tab.ID].filter
	fb.col = "name"
	e.tt.Frame()
	if err := e.tt.Click("Filter value"); err != nil {
		t.Fatal(err)
	}
	e.tt.Type("user 2")
	e.tt.Advance(time.Second)
	if e.tt.HasText("Unsaved edits") || tab.Data.Filter != nil || len(db.Requests()) != reads {
		t.Fatal("typing a filter interrupted the user or ran it")
	}
	if err := e.tt.Click("Apply filter"); err != nil {
		t.Fatal(err)
	}
	if !e.tt.HasText("Unsaved edits") || !containsText(e, "change the filter") {
		t.Fatalf("no question: %q", e.tt.Texts())
	}
	if err := e.tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if fb.text != "" || tab.Data.Filter != nil || !tab.Dirty() {
		t.Errorf("after Cancel: bar text %q filter %v dirty %v", fb.text, tab.Data.Filter, tab.Dirty())
	}
}

func TestViewIsReadOnlyWithAReason(t *testing.T) {
	e, _ := newTablesEnv(t)
	doubleClickText(t, e.tt, "public")
	e.runPost()
	doubleClickText(t, e.tt, "active_users")
	e.runPost()
	e.runPost()
	tab := e.sh.st.Workspace.Active()
	if tab == nil || tab.Data == nil || tab.Data.Meta.Kind != database.ObjectView {
		t.Fatal("the view did not open")
	}
	if !e.tt.HasText("Read-only: Views are read-only.") || e.tt.HasText("Save changes") {
		t.Errorf("texts %q", e.tt.Texts())
	}
	r, _ := e.tt.Find("user 1")
	x, y := center(r)
	e.tt.ClickAt(x, y)
	e.tt.Key(0, mygoui.KeyF2)
	if e.sh.views[tab.ID].grid.edit != nil || tab.Dirty() {
		t.Error("a cell of a view opened an editor")
	}
}

func TestEditorIsLockedWhileSaving(t *testing.T) {
	e, _ := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 17", "x")
	if err := e.tt.Click("Save changes"); err != nil {
		t.Fatal(err)
	}
	g := e.sh.views[tab.ID].grid
	r, ok := e.tt.Find("user 18")
	if !ok {
		t.Fatal("no cell")
	}
	x, y := center(r)
	e.tt.ClickAt(x, y)
	e.tt.Key(0, mygoui.KeyF2)
	if g.edit != nil {
		t.Error("an editor opened while the table was being saved")
	}
	var _ = app.OpLoading
	e.runPost()
	e.runPost()
}

func TestSaveAndContinueKeepsTheFilterThatWasAsked(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 12", "edited 12")
	fb := e.sh.views[tab.ID].filter
	fb.col = "name"
	e.tt.Frame()
	if err := e.tt.Click("Filter value"); err != nil {
		t.Fatal(err)
	}
	e.tt.Type("user 3")
	e.tt.Frame()
	if err := e.tt.Click("Apply filter"); err != nil {
		t.Fatal(err)
	}
	if err := e.tt.Click("Save and continue"); err != nil {
		t.Fatal(err)
	}
	// The commit, then the page read for the filter (and the reload it replaced).
	for tab.Data.Filter == nil || tab.Data.PageState == app.OpLoading || tab.Data.SaveState == app.OpLoading || tab.Dirty() {
		e.runPost()
	}
	e.tt.Advance(filterDebounce * 3)
	e.tt.Frame()
	if dbValue(db, 12, 1).Text != "edited 12" {
		t.Errorf("the edit was not saved: %q", dbValue(db, 12, 1).Text)
	}
	if f := tab.Data.Filter; f == nil || f.Value != "user 3" || fb.text != "user 3" {
		t.Errorf("the filter was lost: filter %+v, bar text %q", tab.Data.Filter, fb.text)
	}
}

func TestSaveButtonsAreNotOfferedWhileASaveRuns(t *testing.T) {
	e, _ := newTablesEnv(t)
	tab := openUsers(t, e)
	typeInCell(t, e, "user 12", "x")
	if err := e.tt.Click("Save changes"); err != nil {
		t.Fatal(err)
	}
	e.sh.st.RequestClose(tab.ID)
	e.tt.Frame()
	if !e.tt.HasText("Close tab with unsaved edits?") || e.tt.HasText("Save and close") {
		t.Errorf("texts %q", e.tt.Texts())
	}
	e.runPost()
	e.runPost()
}
