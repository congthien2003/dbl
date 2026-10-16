package ui

import (
	"strconv"
	"strings"

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

	// The connection dialogs: at most one of form and nameDlg is open; pw
	// is the password being typed in the prompt.
	form    *connForm
	nameDlg *nameDialog
	pw      pwPrompt
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
	return &Shell{st: st, explorer: newExplorer(), views: map[app.TabID]*tabView{}}
}

func (sh *Shell) viewFor(tab *app.Tab) *tabView {
	v := sh.views[tab.ID]
	if v == nil {
		v = &tabView{}
		if tab.Kind == app.TabSQL {
			v.sql = newSQLView()
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
func (sh *Shell) View(c *mygoui.Context) {
	sh.pruneViews()
	sh.fader.apply(c)
	t := c.Theme()
	p := NewPalette(t)
	if c.Shortcut(mygoui.Cmd, mygoui.KeyN) {
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
	mygoui.Row(c).Padding(8, 12).Gap(12).AlignItems(mygoui.Center).Children(func() {
		mygoui.Text(c, "SBDb").Bold()
		mygoui.Box(c).Grow(1)
		mygoui.Text(c, "Theme").TextColor(t.TextMuted)
		if mygoui.Segmented(c, &idx, "System", "Light", "Dark").Label("Theme").Changed() {
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
			mygoui.Text(c, text).FontSize(12).TextColor(color).SingleLine()
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
	sh.connFormView(c, t)
	sh.nameDialogView(c, t)
	sh.connectionDialogs(c)
	sh.passwordPromptView(c, t)
	if sh.st.PendingClose != 0 {
		open := true
		switch mygoui.AlertDialog(c, &open, "Discard changes?",
			"This tab has unsaved edits. Closing it discards them.", "Cancel", "Discard") {
		case 1:
			sh.st.ConfirmClose()
			c.Invalidate()
		case 0, mygoui.AlertDismissed:
			sh.st.CancelClose()
			c.Invalidate()
		}
	}
	if sh.st.PendingQuit {
		open := true
		switch mygoui.AlertDialog(c, &open, "Discard changes and exit?",
			"Some tabs have unsaved edits. Exiting discards them.", "Cancel", "Discard and exit") {
		case 1:
			sh.st.CancelQuit()
			c.Invalidate()
			if sh.OnQuitConfirmed != nil {
				sh.OnQuitConfirmed()
			}
		case 0, mygoui.AlertDismissed:
			sh.st.CancelQuit()
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
