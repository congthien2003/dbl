package app

import (
	"dbl/internal/connection"
	"dbl/internal/storage"
)

// State is the application state the UI reads and the actions change. All
// methods run on the UI thread. The zero value is usable: it has no
// connections and nothing to connect with.
type State struct {
	Settings  storage.Settings
	Workspace Workspace
	// Explorer holds what the explorer read below the connections.
	Explorer ExplorerData

	// Catalog is the saved folders and connections; Conns opens and tracks
	// their sessions (nil in tests that do not connect).
	Catalog connection.Catalog
	Conns   *connection.Manager
	// ConnectionsReadOnly, when not empty, says why connections.json must
	// not be written; every change to the catalog is refused meanwhile.
	ConnectionsReadOnly string
	// PendingConn is a connection change waiting for the user's answer.
	// PendingFolderDelete is the folder the user is deleting, which holds
	// connections that would move to the root.
	PendingConn         *PendingConn
	PendingFolderDelete string
	// TestNotice is the outcome of the last "Test connection" from the
	// explorer's menu, for the status bar.
	TestNotice string

	// Notice is a short message for the status bar, such as a settings
	// file that could not be read.
	Notice string

	// PendingClose is the tab waiting for the user to confirm closing it
	// (0 for none); PendingQuit is set while the window waits for the user
	// to confirm leaving with unsaved changes.
	PendingClose TabID
	PendingQuit  bool
	// PendingEdit is an action on a table tab held back because the tab has
	// unsaved edits (see guard.go).
	PendingEdit *PendingEdit

	// Hooks set by main. All may be nil.
	SetThemeSource      func(storage.ThemePref)
	ScheduleSave        func(storage.Settings)
	ScheduleCatalogSave func(connection.Catalog)

	// catalogNotice is the Notice set by a failed catalog write, so a later
	// successful write clears only that message.
	catalogNotice string
	cancelTest    func()
}

func (s *State) save() {
	if s.ScheduleSave != nil {
		s.ScheduleSave(s.Settings)
	}
}

// SetTheme changes the theme preference. The workspace is untouched.
func (s *State) SetTheme(p storage.ThemePref) {
	s.Settings.Theme = p
	s.Settings = s.Settings.Normalize()
	if s.SetThemeSource != nil {
		s.SetThemeSource(s.Settings.Theme)
	}
	s.save()
}

// SetSidebarWidth records the sidebar width, clamped to the valid range.
func (s *State) SetSidebarWidth(w int) {
	s.Settings.Window.SidebarWidth = w
	s.Settings = s.Settings.Normalize()
	s.save()
}

// SetWindowSize records the window size, clamped to the valid range.
func (s *State) SetWindowSize(w, h int) {
	s.Settings.Window.Width, s.Settings.Window.Height = w, h
	s.Settings = s.Settings.Normalize()
	s.save()
}

// RequestClose closes a tab, or, when it holds unsaved changes, asks first:
// it sets PendingClose and returns false.
func (s *State) RequestClose(id TabID) (closed bool) {
	tab := s.Workspace.Find(id)
	if tab == nil {
		return false
	}
	if tab.Dirty() {
		s.PendingClose = id
		return false
	}
	s.Workspace.Close(id)
	return true
}

// SaveAndClose saves the tab waiting in PendingClose and closes it once the
// changes are committed. If the Save fails the tab stays open, with its edits
// and the reason.
func (s *State) SaveAndClose() {
	id := s.PendingClose
	s.PendingClose = 0
	if tab := s.Workspace.Find(id); tab != nil {
		s.SaveAll([]*Tab{tab}, func() { s.Workspace.Close(id) })
	}
}

// ConfirmClose closes the tab waiting in PendingClose, discarding its drafts.
func (s *State) ConfirmClose() {
	if s.PendingClose != 0 {
		s.Workspace.Close(s.PendingClose)
		s.PendingClose = 0
	}
}

// CancelClose keeps the tab waiting in PendingClose.
func (s *State) CancelClose() { s.PendingClose = 0 }

// RequestQuit reports whether the window may close. With unsaved changes it
// sets PendingQuit and returns false.
func (s *State) RequestQuit() (allowed bool) {
	if len(s.Workspace.DirtyTabs()) > 0 {
		s.PendingQuit = true
		return false
	}
	return true
}

// SaveAndQuit saves every tab with unsaved edits, one after another, and calls
// quit once they are all committed. If one fails the window stays open on
// that tab.
func (s *State) SaveAndQuit(quit func()) {
	s.PendingQuit = false
	s.SaveAll(s.Workspace.DirtyTabs(), quit)
}

// CancelQuit keeps the window open.
func (s *State) CancelQuit() { s.PendingQuit = false }
