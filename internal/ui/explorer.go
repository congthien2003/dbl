package ui

import (
	"fmt"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/database"
)

// revealDuration is how long rows take to fade in after their parent opens.
const revealDuration = 160 * time.Millisecond

type nodeKind uint8

const (
	nodeFolder nodeKind = iota
	nodeConnection
	nodeDatabase
	nodeSchema
	nodeGroup // the "Tables" or "Views" of a schema
	nodeTable // a table or a view
	nodePlaceholder
)

// node is an item of the explorer tree: a folder, a saved connection, or
// something read from the database below a connection.
type node struct {
	kind  nodeKind
	id    string // the folder ID or the profile ID
	label string

	// Below a connection:
	connID string         // the connection the node belongs to
	schema string         // for schemas, groups and tables
	table  database.Table // for tables
	detail string         // muted text after the label
	failed bool           // a placeholder that stands for an error
	errMsg string
}

// sep separates the parts of the ID of a node read from a database. It
// cannot occur in the ID of a folder or a profile, so the IDs cannot clash.
const sep = "\x1f"

func folderItem(folderID string) string { return "folder:" + folderID }

func dbItem(conn string) string             { return "db" + sep + conn }
func schemaItem(conn, schema string) string { return "sc" + sep + conn + sep + schema }
func groupItem(conn, schema string, views bool) string {
	if views {
		return "gv" + sep + conn + sep + schema
	}
	return "gt" + sep + conn + sep + schema
}
func tableItem(conn, schema, name string) string {
	return "tb" + sep + conn + sep + schema + sep + name
}
func placeholderItem(parent, what string) string { return "ph" + sep + parent + sep + what }

// explorer is the tree of folders, saved connections and, below a connected
// connection, its database, schemas, tables and views. It is rebuilt
// whenever the catalog or what was read below the connections changes.
type explorer struct {
	state    mygoui.OutlineState[string]
	row      int // the row chosen, -1 for none
	roots    []string
	nodes    map[string]node
	children map[string][]string // nil for items without children
	parent   map[string]string   // "" for items at the top

	rev    int // Catalog.Rev the tree was built from
	exRev  int // ExplorerData.Rev the tree was built from
	built  bool
	opened map[string]bool // folders and groups already seen, which start open
	// autoOpened remembers the session (epoch) of each connection whose
	// nodes were opened when it connected.
	autoOpened map[string]uint64

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
	actRefresh   // read the node again
	actOpenTable // open the Table Inspector
)

type explorerAction struct {
	kind actionKind
	id   string // the connection, or for folder actions the folder, or the node
	arg  string // actMove: the destination folder ID, "" for the top level
}

func newExplorer() *explorer {
	return &explorer{
		row:        -1,
		nodes:      map[string]node{},
		children:   map[string][]string{},
		parent:     map[string]string{},
		opened:     map[string]bool{},
		autoOpened: map[string]uint64{},
		prevOpen:   map[string]bool{},
		born:       map[string]time.Time{},
	}
}

// rebuild reads the catalog and what was read below the connections into
// the tree when either changed since the last time. Folders start open the
// first time they appear. Only the maps are rebuilt: what was read lives in
// the app state, and which items are open lives in the outline state under
// IDs that do not change, so a rename or a refresh loses neither.
func (e *explorer) rebuild(st *app.State) {
	cat := &st.Catalog
	// Sessions come and go without the catalog changing; looking at each
	// connection's tree is what notices them.
	for _, p := range cat.Profiles {
		st.Tree(p.ID)
	}
	if e.built && e.rev == cat.Rev && e.exRev == st.Explorer.Rev {
		return
	}
	e.nodes = map[string]node{}
	e.children = map[string][]string{}
	e.parent = map[string]string{}
	e.roots = e.roots[:0]
	addConn := func(p connection.Profile, parent string) {
		e.nodes[p.ID] = node{kind: nodeConnection, id: p.ID, label: p.Name, connID: p.ID}
		if parent != "" {
			e.parent[p.ID] = parent
			e.children[parent] = append(e.children[parent], p.ID)
		}
		e.addDatabase(st, p)
	}
	for _, f := range cat.Folders {
		id := folderItem(f.ID)
		e.nodes[id] = node{kind: nodeFolder, id: f.ID, label: f.Name}
		e.roots = append(e.roots, id)
		if !e.opened[f.ID] {
			e.opened[f.ID] = true
			e.state.Open.Add(id)
		}
		for _, p := range cat.ProfilesIn(f.ID) {
			addConn(p, id)
		}
	}
	for _, p := range cat.ProfilesIn("") {
		e.roots = append(e.roots, p.ID)
		addConn(p, "")
	}
	e.rev, e.exRev, e.built = cat.Rev, st.Explorer.Rev, true
}

// link adds a node below parent.
func (e *explorer) link(parent, id string, n node) {
	e.nodes[id] = n
	e.parent[id] = parent
	e.children[parent] = append(e.children[parent], id)
}

// placeholder adds a row that stands for what is not there yet, or for the
// error that kept it from being read. connID and schema say what a retry
// reads again.
func (e *explorer) placeholder(parent, what, label, connID, schema string, err *database.Error) {
	n := node{kind: nodePlaceholder, label: label, connID: connID, schema: schema}
	if err != nil {
		n.failed, n.errMsg = true, err.Kind.String()+": "+err.Message
		if err.Hint != "" {
			n.errMsg += " " + err.Hint
		}
	}
	e.link(parent, placeholderItem(parent, what), n)
}

// addDatabase adds what is read below a connected connection: its database,
// the schemas, and below each schema its tables and views. Nothing is
// added for a connection with no session, so it shows no arrow.
func (e *explorer) addDatabase(st *app.State, p connection.Profile) {
	tree := st.Tree(p.ID)
	if tree == nil {
		return
	}
	if e.autoOpened[p.ID] != tree.Epoch {
		// A session just began: show the database, which is all there is
		// to choose from for a PostgreSQL connection.
		e.autoOpened[p.ID] = tree.Epoch
		e.state.Open.Add(p.ID)
		e.state.Open.Add(dbItem(p.ID))
	}
	db := dbItem(p.ID)
	label := tree.Database
	if label == "" {
		label = p.Database
	}
	e.link(p.ID, db, node{kind: nodeDatabase, id: db, label: label, connID: p.ID})
	switch {
	case tree.State == app.OpFailed:
		e.placeholder(db, "err", "Could not read the schemas", p.ID, "", tree.Err)
		return
	case tree.State != app.OpReady:
		e.placeholder(db, "load", "Loading…", p.ID, "", nil)
		return
	case len(tree.Schemas) == 0:
		e.placeholder(db, "none", "No schemas", p.ID, "", nil)
		return
	}
	for _, sc := range tree.Schemas {
		sid := schemaItem(p.ID, sc.Name)
		e.link(db, sid, node{kind: nodeSchema, id: sid, label: sc.Name, connID: p.ID, schema: sc.Name})
		switch {
		case sc.State == app.OpFailed:
			e.placeholder(sid, "err", "Could not read the tables", p.ID, sc.Name, sc.Err)
			continue
		case sc.State != app.OpReady:
			e.placeholder(sid, "load", "Loading…", p.ID, sc.Name, nil)
			continue
		case len(sc.Tables) == 0:
			e.placeholder(sid, "none", "No tables or views", p.ID, sc.Name, nil)
			continue
		}
		var tables, views []database.Table
		for _, t := range sc.Tables {
			if t.Kind == database.ObjectView {
				views = append(views, t)
			} else {
				tables = append(tables, t)
			}
		}
		for _, g := range []struct {
			views bool
			label string
			list  []database.Table
		}{{false, "Tables", tables}, {true, "Views", views}} {
			if len(g.list) == 0 {
				continue
			}
			gid := groupItem(p.ID, sc.Name, g.views)
			e.link(sid, gid, node{
				kind: nodeGroup, id: gid, label: fmt.Sprintf("%s (%d)", g.label, len(g.list)),
				connID: p.ID, schema: sc.Name,
			})
			if !e.opened[gid] {
				e.opened[gid] = true
				e.state.Open.Add(gid)
			}
			for _, t := range g.list {
				tid := tableItem(p.ID, sc.Name, t.Ref.Name)
				e.link(gid, tid, node{
					kind: nodeTable, id: tid, label: t.Ref.Name,
					connID: p.ID, schema: sc.Name, table: t, detail: t.Subtype,
				})
			}
		}
	}
}

// loadOpen reads what the nodes that are open need and have not got: the
// schemas of an open database node, the tables of an open schema. Nothing
// is read for a node that is closed, so connecting reads no metadata but
// the schema list of the database.
func (e *explorer) loadOpen(st *app.State) {
	for id, n := range e.nodes {
		switch n.kind {
		case nodeDatabase:
			if e.state.Open.Has(id) {
				st.EnsureSchemas(n.connID)
			}
		case nodeSchema:
			if e.state.Open.Has(id) {
				st.EnsureTables(n.connID, n.schema)
			}
		}
	}
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

// selectedConnection returns the connection of the row chosen (the
// connection itself, or the one the node belongs to), or "" when a folder
// or nothing is chosen.
func (e *explorer) selectedConnection() string {
	if n, ok := e.selected(); ok && n.kind != nodeFolder {
		return n.connID
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
	if n.connID != "" {
		n = e.nodes[n.connID]
	}
	if parent := e.parent[n.id]; parent != "" {
		return e.nodes[parent].id
	}
	return ""
}

func (sh *Shell) sidebar(c *mygoui.Context, t *mygoui.Theme) {
	e := sh.explorer
	e.rebuild(sh.st)
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
			// The row chosen and the row under the pointer are paler here
			// than elsewhere: the tree takes their colors from the theme
			// it is made with, which is put back right after.
			et := explorerTheme(t)
			c.SetTheme(&et)
			out := mygoui.Outline(c, &e.state, e.roots, e.childrenOf, func(id string) {
				sh.explorerRow(c, t, id)
			}).Grow(1).Label("Connections")
			c.SetTheme(t)
			e.endPass()
			e.loadOpen(sh.st)
			if out.Submitted() {
				sh.activateRow(e.row)
				c.Invalidate()
			}
		}
		mygoui.Row(c).Gap(6).Children(func() {
			if sh.fade(mygoui.Button(c, "+ Connection")).Disabled(readOnly).Clicked() {
				sh.openConnForm("", e.selectedFolder())
				c.Invalidate()
			}
			if sh.fade(mygoui.Button(c, "+ Folder")).Disabled(readOnly).Clicked() {
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
	switch n.kind {
	case nodeFolder:
		row.ContextMenu(func(m *mygoui.Menu) { sh.folderMenu(m, n) }).Children(func() {
			svg := icons.Folder
			if e.state.Open.Has(id) {
				svg = icons.FolderOpen
			}
			mygoui.Icon(c, svg)
			mygoui.Text(c, n.label).SingleLine()
		})
	case nodeConnection:
		status := sh.st.Conns.Status(id)
		glyph, color := statusGlyph(status, t)
		row.ContextMenu(func(m *mygoui.Menu) { sh.connectionMenu(m, n) }).Children(func() {
			mygoui.Text(c, glyph).TextColor(color).Label(status.String()).ContentTransition(statusFade)
			mygoui.Icon(c, icons.Connection)
			text := mygoui.Text(c, n.label).SingleLine()
			if status == connection.Failed {
				if err := sh.st.Conns.Err(id); err != nil {
					text.Tooltip(err.Message)
				}
			}
		})
	case nodePlaceholder:
		row.ContextMenu(func(m *mygoui.Menu) {
			if n.failed && m.Item("Retry").Chosen() {
				e.act = explorerAction{kind: actRefresh, id: id}
			}
		}).Children(func() {
			text := mygoui.Text(c, n.label).SingleLine().Italic().TextColor(t.TextMuted)
			if n.failed {
				text.TextColor(t.Danger).Tooltip(n.errMsg)
			}
		})
	default:
		row.ContextMenu(func(m *mygoui.Menu) { sh.objectMenu(m, id, n) }).Children(func() {
			isView := n.kind == nodeTable && n.table.Kind == database.ObjectView
			switch {
			case n.kind == nodeDatabase:
				mygoui.Icon(c, icons.Database)
			case n.kind == nodeSchema:
				mygoui.Icon(c, icons.Schema)
			case isView:
				mygoui.Icon(c, icons.View)
			case n.kind == nodeTable:
				mygoui.Icon(c, icons.Table)
			}
			text := mygoui.Text(c, n.label).SingleLine()
			if isView {
				text.Italic()
			}
			if n.detail != "" {
				mygoui.Text(c, n.detail).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
		})
	}
}

// objectMenu is the context menu of a node read from the database.
func (sh *Shell) objectMenu(m *mygoui.Menu, id string, n node) {
	e := sh.explorer
	if n.kind == nodeTable && m.Item("Open").Chosen() {
		e.act = explorerAction{kind: actOpenTable, id: id}
	}
	if m.Item("Refresh").Chosen() {
		e.act = explorerAction{kind: actRefresh, id: id}
	}
	if m.Item("New SQL tab").Chosen() {
		e.act = explorerAction{kind: actNewSQL, id: n.connID}
	}
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
		if m.Item("Refresh").Chosen() {
			e.act = explorerAction{kind: actRefresh, id: n.id}
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
	case actRefresh:
		sh.refreshNode(a.id)
	case actOpenTable:
		sh.openTableNode(a.id)
	}
	c.Invalidate()
}

// refreshNode reads a node of the explorer again: a connection or database
// its schemas, a schema (or its tables group) its tables, a table the
// metadata of its open tab. A retry on a placeholder refreshes its parent.
func (sh *Shell) refreshNode(id string) {
	e := sh.explorer
	n, ok := e.nodes[id]
	if !ok {
		return
	}
	switch n.kind {
	case nodeConnection, nodeDatabase:
		sh.st.RefreshSchemas(n.connID)
	case nodePlaceholder:
		if n.schema != "" {
			sh.st.RefreshTables(n.connID, n.schema)
		} else {
			sh.st.RefreshSchemas(n.connID)
		}
	case nodeSchema, nodeGroup:
		sh.st.RefreshTables(n.connID, n.schema)
	case nodeTable:
		key := app.TableKey{ConnectionID: n.connID, Ref: n.table.Ref}
		for _, tab := range sh.st.Workspace.TabsOf(n.connID) {
			if tab.Kind == app.TabTable && tab.Table == key && tab.Data != nil {
				sh.st.RefreshMetadata(tab)
			}
		}
		sh.st.RefreshTables(n.connID, n.schema)
	}
}

// openTableNode opens the Table Inspector of a table node.
func (sh *Shell) openTableNode(id string) {
	if n, ok := sh.explorer.nodes[id]; ok && n.kind == nodeTable {
		sh.st.OpenTable(n.connID, n.table)
	}
}

// activateRow handles Enter or a double click on an explorer row: a table
// opens in the inspector, a connection that is not connected connects, and
// anything else with children opens or closes.
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
	switch {
	case n.kind == nodeTable:
		sh.st.OpenTable(n.connID, n.table)
		return
	case n.kind == nodePlaceholder:
		if n.failed {
			sh.refreshNode(id)
		}
		return
	case n.kind == nodeConnection && sh.st.Conns.Status(n.id) != connection.Connected:
		sh.st.Connect(n.id)
		return
	}
	if e.state.Open.Has(id) {
		e.state.Open.Remove(id)
	} else {
		e.state.Open.Add(id)
	}
}
