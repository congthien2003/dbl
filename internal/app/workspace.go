package app

import (
	"fmt"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// TabID identifies a tab for its whole life. IDs start at 1 and are never
// reused; 0 means "no tab".
type TabID int

// TabKind is the type of a workspace tab.
type TabKind uint8

// Tab kinds.
const (
	TabTable TabKind = iota
	TabSQL
)

// TableKey is the identity of an open table: the same connection and the
// same full table reference.
type TableKey struct {
	ConnectionID string
	Ref          database.TableRef
}

// Tab is one workspace tab. Each tab owns its state; the UI keeps its
// view-only state elsewhere, keyed by ID.
type Tab struct {
	ID    TabID
	Kind  TabKind
	Title string
	Table TableKey // for TabSQL only ConnectionID is meaningful

	// Source is what the grid of a table tab shows. For a PostgreSQL table
	// it is Data; tests may give any source.
	Source grid.Source
	// Data is the state of a table read from a database, nil otherwise.
	Data   *TableData
	Drafts grid.Drafts
	SQL    *SQLData
}

// Dirty reports whether the tab holds unsaved data. SQL tabs are never
// dirty in Phase 1 (their protection arrives with local files).
func (t *Tab) Dirty() bool {
	if t.SQL != nil {
		return t.SQL.Document.Dirty()
	}
	return t.Kind == TabTable && t.Drafts.Len() > 0
}

// Saving reports whether a Save of the tab is running.
func (t *Tab) Saving() bool {
	return t.Data != nil && t.Data.SaveState == OpLoading || t.SQL != nil && t.SQL.Document.FileState == OpLoading
}

// Workspace is the ordered list of open tabs and the active one.
type Workspace struct {
	Tabs     []*Tab
	ActiveID TabID

	nextID   TabID
	sqlCount int
}

func (w *Workspace) newID() TabID {
	w.nextID++
	return w.nextID
}

// OpenTable opens a tab for key, or focuses the existing tab for the same
// connection and table. created reports whether a tab was added.
func (w *Workspace) OpenTable(key TableKey, title string, src grid.Source) (tab *Tab, created bool) {
	for _, t := range w.Tabs {
		if t.Kind == TabTable && t.Table == key {
			w.ActiveID = t.ID
			return t, false
		}
	}
	tab = &Tab{ID: w.newID(), Kind: TabTable, Title: title, Table: key, Source: src}
	w.Tabs = append(w.Tabs, tab)
	w.ActiveID = tab.ID
	return tab, true
}

// NewSQL opens a new SQL tab titled "Query N"; N is never reused.
func (w *Workspace) NewSQL(connectionID string) *Tab {
	w.sqlCount++
	tab := &Tab{
		ID:    w.newID(),
		Kind:  TabSQL,
		Title: fmt.Sprintf("Query %d", w.sqlCount),
		Table: TableKey{ConnectionID: connectionID},
		SQL:   &SQLData{Page: 1},
	}
	w.Tabs = append(w.Tabs, tab)
	w.ActiveID = tab.ID
	return tab
}

func (w *Workspace) indexOf(id TabID) int {
	for i, t := range w.Tabs {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// Find returns the tab with the given ID, or nil.
func (w *Workspace) Find(id TabID) *Tab {
	if i := w.indexOf(id); i >= 0 {
		return w.Tabs[i]
	}
	return nil
}

// Close removes a tab without asking. When it was active, the tab to its
// right becomes active, else the one to its left, else none.
func (w *Workspace) Close(id TabID) {
	i := w.indexOf(id)
	if i < 0 {
		return
	}
	w.Tabs[i].Data.release()
	w.Tabs[i].SQL.release()
	w.Tabs = append(w.Tabs[:i:i], w.Tabs[i+1:]...)
	if w.ActiveID != id {
		return
	}
	switch {
	case i < len(w.Tabs):
		w.ActiveID = w.Tabs[i].ID
	case i > 0:
		w.ActiveID = w.Tabs[i-1].ID
	default:
		w.ActiveID = 0
	}
}

// TabsOf returns the tabs that belong to a connection.
func (w *Workspace) TabsOf(connectionID string) []*Tab {
	var out []*Tab
	for _, t := range w.Tabs {
		if t.Table.ConnectionID == connectionID {
			out = append(out, t)
		}
	}
	return out
}

// CloseConnection closes every tab of a connection without asking, and
// reports how many it closed and how many of them held unsaved data.
func (w *Workspace) CloseConnection(connectionID string) (closed, dirty int) {
	for _, t := range w.TabsOf(connectionID) {
		if t.Dirty() {
			dirty++
		}
		w.Close(t.ID)
		closed++
	}
	return closed, dirty
}

// Select makes a tab active; unknown IDs are ignored.
func (w *Workspace) Select(id TabID) {
	if w.indexOf(id) >= 0 {
		w.ActiveID = id
	}
}

// Active returns the active tab, or nil when the workspace is empty.
func (w *Workspace) Active() *Tab { return w.Find(w.ActiveID) }

// DirtyTabs returns the tabs holding unsaved data.
func (w *Workspace) DirtyTabs() []*Tab {
	var out []*Tab
	for _, t := range w.Tabs {
		if t.Dirty() {
			out = append(out, t)
		}
	}
	return out
}
