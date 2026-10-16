package app

import (
	"testing"

	"dbl/internal/storage"
)

func newState() *State {
	return &State{Settings: storage.DefaultSettings()}
}

func TestRequestCloseDirtyTabAsks(t *testing.T) {
	s := newState()
	tab := open(t, &s.Workspace, "local-pg", "users")
	makeDirty(tab)
	if s.RequestClose(tab.ID) {
		t.Fatal("dirty tab closed without asking")
	}
	if s.PendingClose != tab.ID || s.Workspace.Find(tab.ID) == nil {
		t.Fatalf("PendingClose=%d, tab present=%v", s.PendingClose, s.Workspace.Find(tab.ID) != nil)
	}
	s.CancelClose()
	if s.PendingClose != 0 || !tab.Dirty() {
		t.Errorf("after cancel: PendingClose=%d dirty=%v", s.PendingClose, tab.Dirty())
	}
	s.RequestClose(tab.ID)
	s.ConfirmClose()
	if s.Workspace.Find(tab.ID) != nil || s.PendingClose != 0 {
		t.Errorf("after confirm: tab present=%v PendingClose=%d", s.Workspace.Find(tab.ID) != nil, s.PendingClose)
	}
}

func TestRequestCloseCleanTabCloses(t *testing.T) {
	s := newState()
	tab := open(t, &s.Workspace, "local-pg", "users")
	if !s.RequestClose(tab.ID) || s.PendingClose != 0 || len(s.Workspace.Tabs) != 0 {
		t.Errorf("clean tab: PendingClose=%d tabs=%d", s.PendingClose, len(s.Workspace.Tabs))
	}
	if s.RequestClose(12345) {
		t.Error("closing an unknown tab reported success")
	}
}

func TestRequestCloseOnlyAsksForThatTab(t *testing.T) {
	s := newState()
	a := open(t, &s.Workspace, "local-pg", "users")
	b := open(t, &s.Workspace, "local-pg", "orders")
	makeDirty(a)
	makeDirty(b)
	s.RequestClose(a.ID)
	if s.PendingClose != a.ID {
		t.Errorf("PendingClose = %d, want %d", s.PendingClose, a.ID)
	}
	s.ConfirmClose()
	if !b.Dirty() || s.Workspace.Find(b.ID) == nil {
		t.Error("confirming one tab affected the other")
	}
}

func TestRequestQuitWithDirtyTab(t *testing.T) {
	s := newState()
	tab := open(t, &s.Workspace, "local-pg", "users")
	if !s.RequestQuit() {
		t.Fatal("quit refused with no unsaved changes")
	}
	makeDirty(tab)
	if s.RequestQuit() || !s.PendingQuit {
		t.Errorf("dirty quit: PendingQuit=%v", s.PendingQuit)
	}
	s.CancelQuit()
	if s.PendingQuit {
		t.Error("CancelQuit left PendingQuit set")
	}
	tab.Drafts.Revert()
	if !s.RequestQuit() {
		t.Error("quit refused after revert")
	}
}

func TestSetSidebarWidthClampsAndSchedulesSave(t *testing.T) {
	s := newState()
	var saved []storage.Settings
	s.ScheduleSave = func(v storage.Settings) { saved = append(saved, v) }
	s.SetSidebarWidth(99999)
	if s.Settings.Window.SidebarWidth != storage.MaxSidebarWidth {
		t.Errorf("sidebar = %d, want %d", s.Settings.Window.SidebarWidth, storage.MaxSidebarWidth)
	}
	s.SetSidebarWidth(5)
	if s.Settings.Window.SidebarWidth != storage.MinSidebarWidth {
		t.Errorf("sidebar = %d, want %d", s.Settings.Window.SidebarWidth, storage.MinSidebarWidth)
	}
	if len(saved) != 2 || saved[1].Window.SidebarWidth != storage.MinSidebarWidth {
		t.Errorf("ScheduleSave calls = %+v", saved)
	}
}

func TestSetWindowSizeClamps(t *testing.T) {
	s := newState()
	calls := 0
	s.ScheduleSave = func(storage.Settings) { calls++ }
	s.SetWindowSize(10, 10)
	if w := s.Settings.Window; w.Width != storage.MinWindowWidth || w.Height != storage.MinWindowHeight {
		t.Errorf("window = %+v", w)
	}
	s.SetWindowSize(1500, 900)
	if w := s.Settings.Window; w.Width != 1500 || w.Height != 900 || calls != 2 {
		t.Errorf("window = %+v calls=%d", w, calls)
	}
}

func TestSetThemeCallsHookAndKeepsWorkspace(t *testing.T) {
	s := newState()
	tab := open(t, &s.Workspace, "local-pg", "users")
	makeDirty(tab)
	var got []storage.ThemePref
	s.SetThemeSource = func(p storage.ThemePref) { got = append(got, p) }
	s.SetTheme(storage.ThemeDark)
	if len(got) != 1 || got[0] != storage.ThemeDark || s.Settings.Theme != storage.ThemeDark {
		t.Errorf("hook calls %v, theme %q", got, s.Settings.Theme)
	}
	if s.Workspace.Active() != tab || !tab.Dirty() || len(s.Workspace.Tabs) != 1 {
		t.Error("changing the theme changed the workspace")
	}
}

func TestStateWithoutHooksDoesNotPanic(t *testing.T) {
	s := newState()
	s.SetTheme(storage.ThemeLight)
	s.SetSidebarWidth(300)
	s.SetWindowSize(1000, 700)
}
