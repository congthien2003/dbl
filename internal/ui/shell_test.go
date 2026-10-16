package ui

import (
	"testing"

	"dbl/internal/storage"
)

func TestExplorerShowsMockTree(t *testing.T) {
	_, tt := newTestShell(t)
	for _, want := range []string{"SBDb", "Work", "Personal", "Sandbox", "Local PG", "Staging PG", "public", "users", "orders", "wide_events", "active_users"} {
		if !tt.HasText(want) {
			t.Errorf("explorer does not show %q; texts: %q", want, tt.Texts())
		}
	}
}

func TestOpenTableFromExplorerCreatesTabAndRefocuses(t *testing.T) {
	sh, tt := newTestShell(t)
	ws := &sh.st.Workspace
	doubleClickText(t, tt, "users")
	if len(ws.Tabs) != 1 || ws.Active() == nil || ws.Active().Title != "users" {
		t.Fatalf("after opening users: %d tabs, active %v", len(ws.Tabs), ws.Active())
	}
	doubleClickText(t, tt, "users")
	if len(ws.Tabs) != 1 {
		t.Errorf("opening users again made %d tabs, want 1", len(ws.Tabs))
	}
	doubleClickText(t, tt, "orders")
	if len(ws.Tabs) != 2 || ws.Active().Title != "orders" {
		t.Errorf("after opening orders: %d tabs, active %v", len(ws.Tabs), ws.Active())
	}
}

func TestCloseButtonClosesTabWithoutSelectingIt(t *testing.T) {
	sh, tt := newTestShell(t)
	users := openTable(t, sh, tt, "users")
	orders := openTable(t, sh, tt, "orders")
	sh.st.Workspace.Select(users.ID)
	tt.Frame()
	if err := tt.Click("Close orders"); err != nil {
		t.Fatal(err)
	}
	ws := &sh.st.Workspace
	if ws.Find(orders.ID) != nil {
		t.Error("orders tab is still open")
	}
	if ws.ActiveID != users.ID {
		t.Errorf("active tab is %d, want users (%d)", ws.ActiveID, users.ID)
	}
}

func TestSelectingTabByClick(t *testing.T) {
	sh, tt := newTestShell(t)
	users := openTable(t, sh, tt, "users")
	orders := openTable(t, sh, tt, "orders")
	if sh.st.Workspace.ActiveID != orders.ID {
		t.Fatal("orders should be active after opening it")
	}
	// Click the title of the users tab, just left of its close button.
	closeUsers, ok := tt.Find("Close users")
	if !ok {
		t.Fatalf("no close button for users; texts %q", tt.Texts())
	}
	tt.ClickAt(closeUsers.X-20, closeUsers.Y+closeUsers.H/2)
	if sh.st.Workspace.ActiveID != users.ID {
		t.Errorf("active tab is %d after clicking users' title, want %d", sh.st.Workspace.ActiveID, users.ID)
	}
	if len(sh.st.Workspace.Tabs) != 2 {
		t.Errorf("clicking a tab changed the tab count to %d", len(sh.st.Workspace.Tabs))
	}
}

func TestEmptyWorkspaceShowsEmptyState(t *testing.T) {
	sh, tt := newTestShell(t)
	if !tt.HasText("No tab open") {
		t.Errorf("empty workspace shows %q", tt.Texts())
	}
	tab := openTable(t, sh, tt, "users")
	sh.st.Workspace.Close(tab.ID)
	tt.Frame()
	if !tt.HasText("No tab open") {
		t.Errorf("workspace after closing the last tab shows %q", tt.Texts())
	}
}

func TestSidebarWidthComesFromSettings(t *testing.T) {
	sh, tt := newTestShell(t)
	sh.st.Settings.Window.SidebarWidth = 300
	tt.Frame()
	work, ok := tt.Find("Work")
	if !ok || work.X+work.W > 300 {
		t.Fatalf("Work at %+v, want inside the 300 DIP sidebar", work)
	}
	empty, ok := tt.Find("No tab open")
	if !ok || empty.X < 300 {
		t.Fatalf("empty state at %+v, want right of the sidebar", empty)
	}
	sh.st.Settings.Window.SidebarWidth = 200
	tt.Frame()
	moved, ok := tt.Find("No tab open")
	if !ok || moved.X >= empty.X {
		t.Errorf("empty state did not move left when the sidebar narrowed: %+v then %+v", empty, moved)
	}
}

func TestThemeSwitchKeepsWorkspace(t *testing.T) {
	sh, tt := newTestShell(t)
	var got []storage.ThemePref
	sh.st.SetThemeSource = func(p storage.ThemePref) { got = append(got, p) }
	users := openTable(t, sh, tt, "users")
	openTable(t, sh, tt, "orders")
	sh.st.Workspace.Select(users.ID)
	tt.Frame()
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != storage.ThemeDark {
		t.Fatalf("theme hook calls: %v", got)
	}
	ws := &sh.st.Workspace
	if len(ws.Tabs) != 2 || ws.ActiveID != users.ID {
		t.Errorf("theme change changed the workspace: %d tabs, active %d", len(ws.Tabs), ws.ActiveID)
	}
}

func TestStatusBarShowsNotice(t *testing.T) {
	sh, tt := newTestShell(t)
	sh.st.Notice = "Settings could not be read"
	tt.Frame()
	if !tt.HasText("Settings could not be read") {
		t.Errorf("status bar shows %q", tt.Texts())
	}
}

func TestViewStateIsReleasedWithTheTab(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := openTable(t, sh, tt, "users")
	if sh.views[tab.ID] == nil {
		t.Fatal("no view state for the open tab")
	}
	sh.st.Workspace.Close(tab.ID)
	tt.Frame()
	if len(sh.views) != 0 {
		t.Errorf("%d view states left after closing the tab", len(sh.views))
	}
}

func TestFormatCount(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1000000: "1,000,000", 100000: "100,000"} {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}
