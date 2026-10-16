package app

import (
	"testing"

	"dbl/internal/database"
	"dbl/internal/grid"
	"dbl/internal/mock"
)

func key(conn, name string) TableKey {
	return TableKey{ConnectionID: conn, Ref: database.TableRef{Catalog: "app", Schema: "public", Name: name}}
}

func open(t *testing.T, w *Workspace, conn, name string) *Tab {
	t.Helper()
	k := key(conn, name)
	ds, ok := mock.DatasetFor(k.ConnectionID, k.Ref)
	if !ok {
		t.Fatalf("no mock dataset for %s/%s", conn, name)
	}
	tab, _ := w.OpenTable(k, name, ds)
	return tab
}

func makeDirty(tab *Tab) {
	orig := tab.Dataset.Value(0, 1)
	tab.Drafts.Set(grid.CellRef{RowID: tab.Dataset.RowID(0), Col: 1}, orig, grid.Value{Kind: grid.KindText, Text: "edited"})
}

func TestOpenTableFocusesExistingTab(t *testing.T) {
	var w Workspace
	k := key("local-pg", "users")
	ds, _ := mock.DatasetFor(k.ConnectionID, k.Ref)
	first, created := w.OpenTable(k, "users", ds)
	if !created {
		t.Fatal("first open did not create a tab")
	}
	other := open(t, &w, "local-pg", "orders")
	if w.ActiveID != other.ID {
		t.Fatal("opening orders did not activate it")
	}
	again, created := w.OpenTable(k, "users", ds)
	if created || again != first || len(w.Tabs) != 2 || w.ActiveID != first.ID {
		t.Errorf("reopen: created=%v same=%v tabs=%d active=%d", created, again == first, len(w.Tabs), w.ActiveID)
	}
	if _, created := w.OpenTable(key("staging-pg", "users"), "users", ds); !created {
		t.Error("same table on another connection reused the tab")
	}
}

func TestTabsKeepIndependentDrafts(t *testing.T) {
	var w Workspace
	a := open(t, &w, "local-pg", "users")
	b := open(t, &w, "local-pg", "orders")
	makeDirty(a)
	if !a.Dirty() || b.Dirty() {
		t.Errorf("a.Dirty=%v b.Dirty=%v, want true/false", a.Dirty(), b.Dirty())
	}
	if got := w.DirtyTabs(); len(got) != 1 || got[0] != a {
		t.Errorf("DirtyTabs = %v", got)
	}
}

func TestCloseActivatesNeighbour(t *testing.T) {
	var w Workspace
	a := open(t, &w, "local-pg", "users")
	b := open(t, &w, "local-pg", "orders")
	c := open(t, &w, "local-pg", "wide_events")
	w.Select(b.ID)
	w.Close(b.ID)
	if w.ActiveID != c.ID {
		t.Errorf("closing the middle tab activated %d, want the right neighbour %d", w.ActiveID, c.ID)
	}
	w.Close(c.ID)
	if w.ActiveID != a.ID {
		t.Errorf("closing the last tab activated %d, want the left neighbour %d", w.ActiveID, a.ID)
	}
	d := open(t, &w, "local-pg", "active_users")
	w.Select(d.ID)
	w.Close(a.ID)
	if w.ActiveID != d.ID {
		t.Errorf("closing an inactive tab changed the active tab to %d", w.ActiveID)
	}
}

func TestCloseLastTabLeavesEmptyWorkspace(t *testing.T) {
	var w Workspace
	a := open(t, &w, "local-pg", "users")
	w.Close(a.ID)
	if w.Active() != nil || w.ActiveID != 0 || len(w.Tabs) != 0 {
		t.Errorf("workspace not empty: active=%v tabs=%d", w.Active(), len(w.Tabs))
	}
	w.Select(999) // unknown ID must be ignored
	w.Close(999)
	if w.Active() != nil {
		t.Error("Select of an unknown ID activated something")
	}
}

func TestNewSQLNumbering(t *testing.T) {
	var w Workspace
	q1 := w.NewSQL("local-pg")
	q2 := w.NewSQL("local-pg")
	if q1.Title != "Query 1" || q2.Title != "Query 2" {
		t.Errorf("titles %q, %q", q1.Title, q2.Title)
	}
	w.Close(q1.ID)
	if q3 := w.NewSQL("local-pg"); q3.Title != "Query 3" {
		t.Errorf("title after close = %q, want Query 3", q3.Title)
	}
}

func TestTabIDsAreNotReused(t *testing.T) {
	var w Workspace
	a := open(t, &w, "local-pg", "users")
	w.Close(a.ID)
	b := open(t, &w, "local-pg", "users")
	if b.ID == a.ID {
		t.Errorf("ID %d reused", b.ID)
	}
}

func TestSQLTabIsNeverDirty(t *testing.T) {
	var w Workspace
	if w.NewSQL("local-pg").Dirty() {
		t.Error("SQL tab is dirty")
	}
}
