package ui

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/grid"
)

func makeTabDirty(tab *app.Tab) {
	ds := tab.Source
	ref := grid.CellRef{RowID: ds.RowID(0), Col: 1}
	tab.Drafts.Set(ref, ds.Value(0, 1), grid.Value{Kind: grid.KindText, Text: "edited"})
}

func TestClosingDirtyTabAsksFirst(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	makeTabDirty(tab)
	tt.Frame()
	if err := tt.Click("Close users"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Discard changes?") {
		t.Fatalf("no confirmation; texts %q", tt.Texts())
	}
	if sh.st.Workspace.Find(tab.ID) == nil {
		t.Fatal("the tab closed before the user answered")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if sh.st.Workspace.Find(tab.ID) == nil || !tab.Dirty() {
		t.Fatal("Cancel did not keep the tab and its edits")
	}
	if tt.HasText("Discard changes?") {
		t.Error("the confirmation is still shown after Cancel")
	}
	if err := tt.Click("Close users"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatal(err)
	}
	if sh.st.Workspace.Find(tab.ID) != nil {
		t.Error("Discard did not close the tab")
	}
}

func TestEscapeCancelsDiscardPrompt(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	makeTabDirty(tab)
	tt.Frame()
	if err := tt.Click("Close users"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, mygoui.KeyEscape)
	if sh.st.PendingClose != 0 || sh.st.Workspace.Find(tab.ID) == nil || !tab.Dirty() {
		t.Errorf("after Escape: PendingClose=%d, tab present=%v, dirty=%v",
			sh.st.PendingClose, sh.st.Workspace.Find(tab.ID) != nil, tab.Dirty())
	}
}

func TestClosingCleanTabDoesNotAsk(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	if err := tt.Click("Close users"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Discard changes?") || sh.st.Workspace.Find(tab.ID) != nil {
		t.Errorf("clean tab: prompt shown=%v, tab present=%v", tt.HasText("Discard changes?"), sh.st.Workspace.Find(tab.ID) != nil)
	}
}

func TestPromptOnlyForTheTabBeingClosed(t *testing.T) {
	sh, tt := newTestShell(t)
	users := openTable(t, sh, tt, "users")
	orders := openTable(t, sh, tt, "orders")
	makeTabDirty(users)
	makeTabDirty(orders)
	tt.Frame()
	if err := tt.Click("Close users"); err != nil {
		t.Fatal(err)
	}
	if sh.st.PendingClose != users.ID {
		t.Errorf("PendingClose = %d, want %d", sh.st.PendingClose, users.ID)
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatal(err)
	}
	if sh.st.Workspace.Find(users.ID) != nil || sh.st.Workspace.Find(orders.ID) == nil || !orders.Dirty() {
		t.Error("discarding one tab affected the other")
	}
}

func TestQuitPromptWhenDirty(t *testing.T) {
	sh, tt := newTestShell(t)
	confirmed := 0
	sh.OnQuitConfirmed = func() { confirmed++ }
	tab := openTable(t, sh, tt, "users")
	makeTabDirty(tab)
	if sh.st.RequestQuit() {
		t.Fatal("RequestQuit allowed leaving with unsaved edits")
	}
	tt.Frame()
	if !tt.HasText("Discard changes and exit?") {
		t.Fatalf("no quit confirmation; texts %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if sh.st.PendingQuit || confirmed != 0 {
		t.Errorf("after Cancel: PendingQuit=%v confirmed=%d", sh.st.PendingQuit, confirmed)
	}
	sh.st.RequestQuit()
	tt.Frame()
	if err := tt.Click("Discard and exit"); err != nil {
		t.Fatal(err)
	}
	if confirmed != 1 {
		t.Errorf("OnQuitConfirmed called %d times, want 1", confirmed)
	}
}
