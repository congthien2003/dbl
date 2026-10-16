package ui

import (
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/mock"
)

// revealDuration is how long rows take to fade in after their parent opens.
const revealDuration = 160 * time.Millisecond

// explorer is the connection tree in the sidebar, over the fake catalog.
type explorer struct {
	state    mygoui.OutlineState[string]
	row      int // the row chosen, -1 for none
	roots    []string
	nodes    map[string]*mock.Node
	children map[string][]string // nil for items without children
	parent   map[string]string   // "" for items at the top

	// The outline builds only the rows in view and shows opened rows at
	// once, so the explorer fades in the rows an opening reveals itself:
	// prevOpen is the open state of the last pass, born when a revealed row
	// started to fade.
	prevOpen map[string]bool
	primed   bool
	born     map[string]time.Time
}

func newExplorer() *explorer {
	e := &explorer{
		row:      -1,
		nodes:    map[string]*mock.Node{},
		children: map[string][]string{},
		parent:   map[string]string{},
		prevOpen: map[string]bool{},
		born:     map[string]time.Time{},
	}
	var walk func(list []*mock.Node, parent string) []string
	walk = func(list []*mock.Node, parent string) []string {
		ids := make([]string, 0, len(list))
		for _, n := range list {
			e.nodes[n.ID] = n
			e.parent[n.ID] = parent
			ids = append(ids, n.ID)
			if len(n.Children) > 0 {
				e.children[n.ID] = walk(n.Children, n.ID)
			}
		}
		return ids
	}
	e.roots = walk(mock.Catalog(), "")
	e.state.Open.Add("folder:work")
	e.state.Open.Add("folder:personal")
	return e
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

// selectedConnection returns the connection of the row chosen, or "".
func (e *explorer) selectedConnection() string {
	if e.row < 0 || e.row >= e.state.Rows() {
		return ""
	}
	return e.nodes[e.state.Item(e.row)].ConnectionID
}

func (sh *Shell) sidebar(c *mygoui.Context, t *mygoui.Theme) {
	e := sh.explorer
	mygoui.Column(c).Fill().Padding(8).Gap(6).Background(t.Surface).Children(func() {
		mygoui.Text(c, "CONNECTIONS").FontSize(11).Bold().TextColor(t.TextMuted)
		e.state.List.Selected = &e.row
		out := mygoui.Outline(c, &e.state, e.roots, e.childrenOf, func(id string) {
			n := e.nodes[id]
			text := mygoui.Text(c, n.Label).SingleLine()
			if n.Kind == mock.NodeSchema {
				text.TextColor(t.TextMuted)
			}
			if a := e.reveal(c, id); a < 1 {
				text.Opacity(a).Translate(0, (a-1)*6)
				c.AnimationFrame()
			}
		}).Grow(1).Label("Connections")
		e.endPass()
		if out.Submitted() {
			sh.activateRow(e.row)
			c.Invalidate()
		}
	})
}

// activateRow handles Enter or a double click on an explorer row: tables and
// views open in a tab; folders, connections and schemas open or close.
func (sh *Shell) activateRow(row int) {
	e := sh.explorer
	if row < 0 || row >= e.state.Rows() {
		return
	}
	id := e.state.Item(row)
	n := e.nodes[id]
	switch n.Kind {
	case mock.NodeTable, mock.NodeView:
		if ds, ok := datasetFor(n); ok {
			sh.st.Workspace.OpenTable(app.TableKey{ConnectionID: n.ConnectionID, Ref: n.Table}, n.Label, ds)
		}
	default:
		if e.state.Open.Has(id) {
			e.state.Open.Remove(id)
		} else {
			e.state.Open.Add(id)
		}
	}
}
