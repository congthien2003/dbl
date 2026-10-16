package ui

import (
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/connection"
)

// revealDuration is how long rows take to fade in after their parent opens.
const revealDuration = 160 * time.Millisecond

type nodeKind uint8

const (
	nodeFolder nodeKind = iota
	nodeConnection
)

// node is an item of the explorer tree: a folder or a saved connection.
type node struct {
	kind  nodeKind
	id    string // the folder ID or the profile ID
	label string
}

func folderItem(folderID string) string { return "folder:" + folderID }

// explorer is the tree of folders and saved connections in the sidebar. It
// is rebuilt from the catalog whenever the catalog changes. Databases,
// schemas and tables below a connection arrive with Phase 2.2.
type explorer struct {
	state    mygoui.OutlineState[string]
	row      int // the row chosen, -1 for none
	roots    []string
	nodes    map[string]node
	children map[string][]string // nil for items without children
	parent   map[string]string   // "" for items at the top

	rev    int // Catalog.Rev the tree was built from
	built  bool
	opened map[string]bool // folders already seen, which start open

	// act is the choice made in a context menu during the frame, run once
	// the outline is done (a choice that changes the tree must not run while
	// the rows are being built).
	act explorerAction

	// The outline builds only the rows in view and shows opened rows at
	// once, so the explorer fades in the rows an opening reveals itself:
	// prevOpen is the open state of the last pass, born when a revealed row
	// started to fade.
	prevOpen map[string]bool
	primed   bool
	born     map[string]time.Time
}

type actionKind uint8

const (
	actNone actionKind = iota
	actConnect
	actCancel
	actDisconnect
	actTest
	actNewSQL
	actEdit
	actRename
	actMove
	actDelete
	actNewConnectionIn
	actRenameFolder
	actDeleteFolder
)

type explorerAction struct {
	kind actionKind
	id   string // the connection, or for folder actions the folder
	arg  string // actMove: the destination folder ID, "" for the top level
}

func newExplorer() *explorer {
	return &explorer{
		row:      -1,
		nodes:    map[string]node{},
		children: map[string][]string{},
		parent:   map[string]string{},
		opened:   map[string]bool{},
		prevOpen: map[string]bool{},
		born:     map[string]time.Time{},
	}
}

// rebuild reads the catalog into the tree when it changed since the last
// time. Folders start open the first time they appear.
func (e *explorer) rebuild(cat *connection.Catalog) {
	if e.built && e.rev == cat.Rev {
		return
	}
	e.nodes = map[string]node{}
	e.children = map[string][]string{}
	e.parent = map[string]string{}
	e.roots = e.roots[:0]
	for _, f := range cat.Folders {
		id := folderItem(f.ID)
		e.nodes[id] = node{kind: nodeFolder, id: f.ID, label: f.Name}
		e.roots = append(e.roots, id)
		if !e.opened[f.ID] {
			e.opened[f.ID] = true
			e.state.Open.Add(id)
		}
		for _, p := range cat.ProfilesIn(f.ID) {
			e.nodes[p.ID] = node{kind: nodeConnection, id: p.ID, label: p.Name}
			e.parent[p.ID] = id
			e.children[id] = append(e.children[id], p.ID)
		}
	}
	for _, p := range cat.ProfilesIn("") {
		e.nodes[p.ID] = node{kind: nodeConnection, id: p.ID, label: p.Name}
		e.roots = append(e.roots, p.ID)
	}
	e.rev, e.built = cat.Rev, true
}

func (e *explorer) childrenOf(id string) []string { return e.children[id] }

// reveal returns how visible the row of id is, from 0 to 1. A row whose
// ancestor opened since the last pass starts to fade in; ReduceMotion shows
// every row at once.
func (e *explorer) reveal(c *mygoui.Context, id string) float32 {
	if c.Preferences().ReduceMotion {
		return 1
	}
	now := c.Now()
	if _, ok := e.born[id]; !ok && e.primed {
		for a := e.parent[id]; a != ""; a = e.parent[a] {
			if e.state.Open.Has(a) && !e.prevOpen[a] {
				e.born[id] = now
				break
			}
		}
	}
	born, ok := e.born[id]
	if !ok {
		return 1
	}
	p := float32(now.Sub(born)) / float32(revealDuration)
	if p >= 1 {
		delete(e.born, id)
		return 1
	}
	p = max(0, p)
	return 1 - (1-p)*(1-p)*(1-p)
}

// endPass remembers which items are open, for the next pass.
func (e *explorer) endPass() {
	for id := range e.children {
		e.prevOpen[id] = e.state.Open.Has(id)
	}
	e.primed = true
}

// selected returns the node of the row chosen.
func (e *explorer) selected() (node, bool) {
	if e.row < 0 || e.row >= e.state.Rows() {
		return node{}, false
	}
	n, ok := e.nodes[e.state.Item(e.row)]
	return n, ok
}

// selectedConnection returns the connection of the row chosen, or "" when a
// folder or nothing is chosen.
func (e *explorer) selectedConnection() string {
	if n, ok := e.selected(); ok && n.kind == nodeConnection {
		return n.id
	}
	return ""
}

// selectedFolder returns the folder of the row chosen, or the folder that
// holds the connection chosen, or "".
func (e *explorer) selectedFolder() string {
	n, ok := e.selected()
	switch {
	case !ok:
		return ""
	case n.kind == nodeFolder:
		return n.id
	}
	if parent := e.parent[n.id]; parent != "" {
		return e.nodes[parent].id
	}
	return ""
}

func (sh *Shell) sidebar(c *mygoui.Context, t *mygoui.Theme) {
	e := sh.explorer
	e.rebuild(&sh.st.Catalog)
	readOnly := sh.st.ConnectionsReadOnly != ""
	mygoui.Column(c).Fill().Padding(8).Gap(6).Background(t.Surface).Children(func() {
		mygoui.Text(c, "CONNECTIONS").FontSize(11).Bold().TextColor(t.TextMuted)
		if len(e.roots) == 0 {
			mygoui.Column(c).Grow(1).Gap(4).Padding(8, 4).Children(func() {
				mygoui.Text(c, "No connections yet").TextColor(t.TextMuted)
				mygoui.Text(c, "Add one with + Connection.").FontSize(12).TextColor(t.TextMuted)
			})
		} else {
			e.state.List.Selected = &e.row
			out := mygoui.Outline(c, &e.state, e.roots, e.childrenOf, func(id string) {
				sh.explorerRow(c, t, id)
			}).Grow(1).Label("Connections")
			e.endPass()
			if out.Submitted() {
				sh.activateRow(e.row)
				c.Invalidate()
			}
		}
		mygoui.Row(c).Gap(6).Children(func() {
			if mygoui.Button(c, "+ Connection").Disabled(readOnly).Clicked() {
				sh.openConnForm("", e.selectedFolder())
				c.Invalidate()
			}
			if mygoui.Button(c, "+ Folder").Disabled(readOnly).Clicked() {
				sh.openNameDialog(nameNewFolder, "", "")
				c.Invalidate()
			}
		})
	})
	sh.runExplorerAction(c)
}

// statusGlyph is the dot in front of a connection and the color it takes.
func statusGlyph(s connection.Status, t *mygoui.Theme) (string, mygoui.Color) {
	switch s {
	case connection.Connected:
		return "●", t.Success
	case connection.Connecting:
		return "◌", t.Warning
	case connection.Failed:
		return "●", t.Danger
	case connection.NeedsPassword:
		return "◐", t.Warning
	}
	return "○", t.TextMuted
}

func (sh *Shell) explorerRow(c *mygoui.Context, t *mygoui.Theme, id string) {
	e := sh.explorer
	n, ok := e.nodes[id]
	if !ok {
		return
	}
	fade := e.reveal(c, id)
	row := mygoui.Row(c).Gap(6).AlignItems(mygoui.Center)
	if fade < 1 {
		row.Opacity(fade).Translate(0, (fade-1)*6)
		c.AnimationFrame()
	}
	if n.kind == nodeFolder {
		row.ContextMenu(func(m *mygoui.Menu) { sh.folderMenu(m, n) }).Children(func() {
			mygoui.Text(c, n.label).SingleLine()
		})
		return
	}
	status := sh.st.Conns.Status(id)
	glyph, color := statusGlyph(status, t)
	row.ContextMenu(func(m *mygoui.Menu) { sh.connectionMenu(m, n) }).Children(func() {
		mygoui.Text(c, glyph).TextColor(color).Label(status.String())
		text := mygoui.Text(c, n.label).SingleLine()
		if status == connection.Failed {
			if err := sh.st.Conns.Err(id); err != nil {
				text.Tooltip(err.Message)
			}
		}
	})
}

func (sh *Shell) folderMenu(m *mygoui.Menu, n node) {
	e := sh.explorer
	readOnly := sh.st.ConnectionsReadOnly != ""
	if m.Item("New connection here…").Disabled(readOnly).Chosen() {
		e.act = explorerAction{kind: actNewConnectionIn, id: n.id}
	}
	m.Separator()
	if m.Item("Rename…").Disabled(readOnly).Chosen() {
		e.act = explorerAction{kind: actRenameFolder, id: n.id}
	}
	if m.Item("Delete…").Disabled(readOnly).Chosen() {
		e.act = explorerAction{kind: actDeleteFolder, id: n.id}
	}
}

func (sh *Shell) connectionMenu(m *mygoui.Menu, n node) {
	e := sh.explorer
	readOnly := sh.st.ConnectionsReadOnly != ""
	p, _ := sh.st.Catalog.Profile(n.id)
	supported := p.Driver.Supported()
	switch sh.st.Conns.Status(n.id) {
	case connection.Connecting:
		if m.Item("Cancel connecting").Chosen() {
			e.act = explorerAction{kind: actCancel, id: n.id}
		}
	case connection.Connected:
		if m.Item("Disconnect").Chosen() {
			e.act = explorerAction{kind: actDisconnect, id: n.id}
		}
	default:
		if m.Item("Connect").Disabled(!supported).Chosen() {
			e.act = explorerAction{kind: actConnect, id: n.id}
		}
	}
	if m.Item("Test connection").Disabled(!supported).Chosen() {
		e.act = explorerAction{kind: actTest, id: n.id}
	}
	if m.Item("New SQL tab").Chosen() {
		e.act = explorerAction{kind: actNewSQL, id: n.id}
	}
	m.Separator()
	if m.Item("Edit…").Disabled(readOnly || !supported).Chosen() {
		e.act = explorerAction{kind: actEdit, id: n.id}
	}
	if m.Item("Rename…").Disabled(readOnly).Chosen() {
		e.act = explorerAction{kind: actRename, id: n.id}
	}
	m.Submenu("Move to", func(sub *mygoui.Menu) {
		if sub.Item("Top level").Checked(p.FolderID == "").Disabled(readOnly).Chosen() {
			e.act = explorerAction{kind: actMove, id: n.id}
		}
		for _, f := range sh.st.Catalog.Folders {
			if sub.Item(f.Name).Checked(p.FolderID == f.ID).Disabled(readOnly).Chosen() {
				e.act = explorerAction{kind: actMove, id: n.id, arg: f.ID}
			}
		}
	})
	m.Separator()
	if m.Item("Delete…").Disabled(readOnly).Chosen() {
		e.act = explorerAction{kind: actDelete, id: n.id}
	}
}

// runExplorerAction carries out the choice made in a context menu.
func (sh *Shell) runExplorerAction(c *mygoui.Context) {
	e := sh.explorer
	a := e.act
	e.act = explorerAction{}
	if a.kind == actNone {
		return
	}
	st := sh.st
	switch a.kind {
	case actConnect:
		st.Connect(a.id)
	case actCancel:
		st.CancelConnect(a.id)
	case actDisconnect:
		st.RequestDisconnect(a.id)
	case actTest:
		st.TestSaved(a.id)
	case actNewSQL:
		st.Workspace.NewSQL(a.id)
	case actEdit:
		sh.openConnForm(a.id, "")
	case actRename:
		if p, ok := st.Catalog.Profile(a.id); ok {
			sh.openNameDialog(nameRenameConnection, a.id, p.Name)
		}
	case actMove:
		_ = st.MoveConnection(a.id, a.arg)
	case actDelete:
		st.RequestDelete(a.id)
	case actNewConnectionIn:
		sh.openConnForm("", a.id)
	case actRenameFolder:
		if f, ok := st.Catalog.Folder(a.id); ok {
			sh.openNameDialog(nameRenameFolder, a.id, f.Name)
		}
	case actDeleteFolder:
		st.RequestDeleteFolder(a.id)
	}
	c.Invalidate()
}

// activateRow handles Enter or a double click on an explorer row:
// connections connect; folders open or close.
func (sh *Shell) activateRow(row int) {
	e := sh.explorer
	if row < 0 || row >= e.state.Rows() {
		return
	}
	id := e.state.Item(row)
	n, ok := e.nodes[id]
	if !ok {
		return
	}
	if n.kind == nodeConnection {
		sh.st.Connect(n.id)
		return
	}
	if e.state.Open.Has(id) {
		e.state.Open.Remove(id)
	} else {
		e.state.Open.Add(id)
	}
}
