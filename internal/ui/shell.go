package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/storage"
)

// themeChoices lists the theme preferences in the order of the segmented
// control in the top bar.
var themeChoices = []storage.ThemePref{storage.ThemeSystem, storage.ThemeLight, storage.ThemeDark}

// Shell is the main window's view: top bar, explorer, workspace and status
// bar. It reads and changes app.State and keeps only view state of its own.
type Shell struct {
	// OnQuitConfirmed is called when the user confirms leaving the window
	// with unsaved changes.
	OnQuitConfirmed func()

	st       *app.State
	explorer *explorer
	views    map[app.TabID]*tabView
	fader    themeFader
	// tabGap is where the line under the tab strip is cut, under the tab
	// chosen, as laid out in the last frame.
	tabGap tabSpan
	// tabShown is the tab the strip last scrolled to show.
	tabShown app.TabID
	// tabScroll is how far the tab strip is scrolled; the wheel moves it.
	tabScroll mygoui.ScrollState

	// The connection dialogs: at most one of form and nameDlg is open; pw
	// is the password being typed in the prompt.
	form    *connForm
	nameDlg *nameDialog
	pw      pwPrompt

	win *mygo.Window
	// pickFile asks for a SQLite file; tests replace it, nil means the
	// native dialog.
	pickFile func() (string, error)
}

// tabView is the view-only state of one tab. It lives as long as the tab.
type tabView struct {
	section int // 0 = Data, 1 = Properties
	grid    *gridView
	sql     *sqlView
	filter  *filterBar
}

// NewShell returns the shell for st.
func NewShell(st *app.State) *Shell {
	sh := &Shell{st: st, explorer: newExplorer(), views: map[app.TabID]*tabView{}}
	st.OnSQLQuitReady = func() {
		if sh.OnQuitConfirmed != nil {
			sh.OnQuitConfirmed()
		}
	}
	return sh
}

func (sh *Shell) viewFor(tab *app.Tab) *tabView {
	v := sh.views[tab.ID]
	if v == nil {
		v = &tabView{}
		if tab.Kind == app.TabSQL {
			v.sql = newSQLView()
			v.sql.owner = sh.st
			v.sql.bind(tab)
		} else {
			v.grid = newGridView()
		}
		sh.views[tab.ID] = v
	}
	return v
}

// pruneViews drops the view state of tabs that no longer exist.
func (sh *Shell) pruneViews() {
	for id := range sh.views {
		if sh.st.Workspace.Find(id) == nil {
			delete(sh.views, id)
		}
	}
}

// View builds the whole window.
func (sh *Shell) SyncSQLDocuments() {
	for id, v := range sh.views {
		if v.sql != nil {
			if tab := sh.st.Workspace.Find(id); tab != nil {
				v.sql.syncDocument(tab)
			}
		}
	}
}
func (sh *Shell) RequestQuit() bool { sh.SyncSQLDocuments(); return sh.st.RequestQuit() }

// View builds the whole window.
func (sh *Shell) View(c *mygoui.Context) {
	sh.SyncSQLDocuments()
	sh.pruneViews()
	sh.fader.apply(c)
	t := c.Theme()
	p := NewPalette(t)
	if !sh.sqlModalOpen() && c.Shortcut(mygoui.Cmd, mygoui.KeyN) {
		sh.newSQL()
		c.Invalidate()
	}
	mygoui.Column(c).Fill().Children(func() {
		sh.topBar(c, t)
		mygoui.Divider(c)
		sh.body(c, t, p)
		mygoui.Divider(c)
		sh.statusBar(c, t)
	})
	sh.dialogs(c, t)
}

// newSQL opens a SQL tab on the connection chosen in the explorer.
func (sh *Shell) newSQL() {
	sh.st.Workspace.NewSQL(sh.explorer.selectedConnection())
}

func (sh *Shell) topBar(c *mygoui.Context, t *mygoui.Theme) {
	idx := 0
	for i, p := range themeChoices {
		if p == sh.st.Settings.Theme {
			idx = i
		}
	}
	mygoui.Row(c).Padding(6, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		mygoui.Text(c, "SBDb").Bold()
		mygoui.Box(c).Grow(1)
		mygoui.Text(c, "Theme").FontSize(11).TextColor(t.TextMuted)
		// Three icons: the system's theme, Light and Dark.
		shown := idx
		seg := mygoui.SegmentedBase(c.Key("theme"), &idx, len(themeChoices))
		seg.Track.Padding(2).Radius(6).Background(t.Surface).Gap(2).Label("Theme").Children(func() {
			for i, s := range []struct {
				name string
				svg  *mygoui.SVG
			}{{"System", icons.ThemeSystem}, {"Light", icons.ThemeLight}, {"Dark", icons.ThemeDark}} {
				item := sh.fade(seg.Segment(i)).Padding(4, 8).Radius(5).AlignItems(mygoui.Center).Label(s.name).Tooltip(s.name)
				if i == idx {
					item.Background(t.Background)
				}
				item.Children(func() { mygoui.Icon(c, s.svg).FontSize(16) })
			}
		})
		if idx != shown {
			sh.st.SetTheme(themeChoices[idx])
			c.Invalidate()
		}
	})
}

func (sh *Shell) body(c *mygoui.Context, t *mygoui.Theme, p Palette) {
	width := float32(sh.st.Settings.Window.SidebarWidth)
	mygoui.Split(c, &width,
		func() { sh.sidebar(c, t) },
		func() { sh.workspace(c, t, p) },
	).Grow(1).FillWidth()
	if w := int(width + 0.5); w != sh.st.Settings.Window.SidebarWidth {
		sh.st.SetSidebarWidth(w)
	}
}

// statusConnection is the connection the status bar describes: the one
// chosen in the explorer, else the one of the active tab.
func (sh *Shell) statusConnection() (connection.Profile, bool) {
	if id := sh.explorer.selectedConnection(); id != "" {
		if p, ok := sh.st.Catalog.Profile(id); ok {
			return p, true
		}
	}
	if tab := sh.st.Workspace.Active(); tab != nil {
		return sh.st.Catalog.Profile(tab.Table.ConnectionID)
	}
	return connection.Profile{}, false
}

// connectionStatus describes the state of a connection in words, and the
// color of the text.
func (sh *Shell) connectionStatus(t *mygoui.Theme, p connection.Profile) (string, mygoui.Color) {
	switch sh.st.Conns.Status(p.ID) {
	case connection.Connected:
		return "Connected: " + p.Name, t.Success
	case connection.Connecting:
		return "Connecting to " + p.Name + "…", t.TextMuted
	case connection.Failed:
		msg := "Connection to " + p.Name + " failed"
		if err := sh.st.Conns.Err(p.ID); err != nil {
			msg += ": " + err.Message
			if err.Hint != "" {
				msg += " " + err.Hint
			}
		}
		return msg, t.Danger
	case connection.NeedsPassword:
		return "Password needed for " + p.Name, t.Warning
	}
	return p.Name + ": not connected", t.TextMuted
}

func (sh *Shell) statusBar(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Row(c).Padding(4, 12).Gap(16).AlignItems(mygoui.Center).Children(func() {
		if p, ok := sh.statusConnection(); ok {
			text, color := sh.connectionStatus(t, p)
			mygoui.Text(c, text).FontSize(12).TextColor(color).SingleLine().ContentTransition(statusFade)
		} else {
			mygoui.Text(c, "No connection selected").FontSize(12).TextColor(t.TextMuted)
		}
		if sh.st.TestNotice != "" {
			mygoui.Text(c, sh.st.TestNotice).FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
		if sh.st.Notice != "" {
			mygoui.Text(c, sh.st.Notice).FontSize(12).TextColor(t.Warning).SingleLine()
		}
		mygoui.Box(c).Grow(1)
		if tab := sh.st.Workspace.Active(); tab != nil && tab.Kind == app.TabTable {
			mygoui.Text(c, rowsSummary(tab)).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// dialogs shows the dialogs the state asks for.
func (sh *Shell) dialogs(c *mygoui.Context, t *mygoui.Theme) {
	if sh.sqlDialogs(c) {
		return
	}
	sh.connFormView(c, t)
	sh.nameDialogView(c, t)
	sh.connectionDialogs(c)
	sh.passwordPromptView(c, t)
	sh.editDialogs(c)
}

// canSave reports whether any of the tabs holds edits that can be saved now:
// a tab that is being saved already has its Save under way.
func canSave(tabs []*app.Tab) bool {
	for _, t := range tabs {
		if t.Dirty() && (t.Data != nil || t.SQL != nil) && !t.Saving() {
			return true
		}
	}
	return false
}

// savingNote warns that a Save is still running.
func savingNote(n int) string {
	if n == 0 {
		return ""
	}
	return " A save is still running; stopping it now may leave its result unknown."
}

// editDialogs asks what to do with unsaved edits: before an action that would
// replace the rows of a table, before closing a tab and before leaving the
// window. Each offers Cancel, Discard and (for a table that can save) Save.
func (sh *Shell) editDialogs(c *mygoui.Context) {
	st := sh.st
	if pe := st.PendingEdit; pe != nil {
		tab := st.Workspace.Find(pe.Tab)
		if tab == nil {
			st.PendingEdit = nil
			return
		}
		msg := fmt.Sprintf("%s has unsaved edits. Save them or discard them before you %s.", plural(tab.Drafts.Len(), "cell"), pe.Reason)
		open := true
		switch mygoui.AlertDialog(c, &open, "Unsaved edits", msg, "Cancel", "Discard", "Save and continue") {
		case 1:
			st.ResolvePendingEdit(app.EditDiscard)
			c.Invalidate()
		case 2:
			st.ResolvePendingEdit(app.EditSave)
			c.Invalidate()
		case 0, mygoui.AlertDismissed:
			st.ResolvePendingEdit(app.EditCancel)
			c.Invalidate()
		}
		return
	}
	if id := st.PendingClose; id != 0 {
		tab := st.Workspace.Find(id)
		if tab == nil {
			st.CancelClose()
			return
		}
		msg := "This tab has unsaved edits. Closing it discards them." + savingNote(st.SavingTabs())
		buttons := []string{"Cancel", "Discard"}
		if canSave([]*app.Tab{tab}) {
			buttons = append(buttons, "Save and close")
		}
		open := true
		switch mygoui.AlertDialog(c, &open, "Close tab with unsaved edits?", msg, buttons...) {
		case 1:
			st.ConfirmClose()
			c.Invalidate()
		case 2:
			st.SaveAndClose()
			c.Invalidate()
		case 0, mygoui.AlertDismissed:
			st.CancelClose()
			c.Invalidate()
		}
		return
	}
	if st.PendingQuit {
		msg := "Some tabs have unsaved edits. Exiting discards them." + savingNote(st.SavingTabs())
		buttons := []string{"Cancel", "Discard and exit"}
		if canSave(st.Workspace.DirtyTabs()) {
			buttons = append(buttons, "Save and exit")
		}
		open := true
		switch mygoui.AlertDialog(c, &open, "Exit with unsaved edits?", msg, buttons...) {
		case 1:
			st.CancelQuit()
			c.Invalidate()
			if sh.OnQuitConfirmed != nil {
				sh.OnQuitConfirmed()
			}
		case 2:
			st.SaveAndQuit(sh.OnQuitConfirmed)
			c.Invalidate()
		case 0, mygoui.AlertDismissed:
			st.CancelQuit()
			c.Invalidate()
		}
	}
}

// rowsSummary describes the rows of a table tab for the status bar: the
// rows in view, and the server's estimate of the table when it has one.
func rowsSummary(tab *app.Tab) string {
	if tab.Data == nil {
		return formatCount(tab.Source.Len()) + " rows"
	}
	d := tab.Data
	s := formatCount(d.Len()) + " rows in view"
	if d.Meta != nil && d.Filter == nil && d.Meta.EstimatedRows >= 0 {
		s += " · about " + formatCount(int(d.Meta.EstimatedRows)) + " in the table"
	}
	return s
}

// formatCount writes n with thousands separators.
func formatCount(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
