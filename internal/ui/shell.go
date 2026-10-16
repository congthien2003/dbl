package ui

import (
	"strconv"
	"strings"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/mock"
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
}

// tabView is the view-only state of one tab. It lives as long as the tab.
type tabView struct {
	section int // 0 = Data, 1 = Properties
	grid    *gridView
	sql     *sqlView
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
	sh.dialogs(c)
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

func (sh *Shell) statusBar(c *mygoui.Context, t *mygoui.Theme) {
	mygoui.Row(c).Padding(4, 12).Gap(16).AlignItems(mygoui.Center).Children(func() {
		mygoui.Text(c, "Mock data — no database connected").FontSize(12).TextColor(t.TextMuted)
		if sh.st.Notice != "" {
			mygoui.Text(c, sh.st.Notice).FontSize(12).TextColor(t.Warning)
		}
		mygoui.Box(c).Grow(1)
		if tab := sh.st.Workspace.Active(); tab != nil && tab.Kind == app.TabTable {
			mygoui.Text(c, formatCount(tab.Dataset.Rows)+" rows").FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// dialogs shows the confirmation dialogs the state asks for.
func (sh *Shell) dialogs(c *mygoui.Context) {
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

// datasetFor is a small seam for the explorer: tables of the fake catalog.
func datasetFor(n *mock.Node) (*mock.Dataset, bool) {
	return mock.DatasetFor(n.ConnectionID, n.Table)
}
