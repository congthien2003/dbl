package mock

import (
	"testing"

	"dbl/internal/database"
)

func walk(nodes []*Node, fn func(*Node)) {
	for _, n := range nodes {
		fn(n)
		walk(n.Children, fn)
	}
}

func TestCatalogShape(t *testing.T) {
	roots := Catalog()
	if len(roots) != 3 {
		t.Fatalf("%d roots, want 3", len(roots))
	}
	byLabel := map[string]*Node{}
	for _, r := range roots {
		byLabel[r.Label] = r
	}
	if w := byLabel["Work"]; w == nil || w.Kind != NodeFolder || len(w.Children) != 2 {
		t.Errorf("Work folder = %+v", w)
	}
	if p := byLabel["Personal"]; p == nil || p.Kind != NodeFolder || len(p.Children) != 1 {
		t.Errorf("Personal folder = %+v", p)
	}
	if s := byLabel["Sandbox"]; s == nil || s.Kind != NodeConnection {
		t.Errorf("root connection = %+v", s)
	}

	ids := map[string]bool{}
	connections := 0
	walk(roots, func(n *Node) {
		if ids[n.ID] {
			t.Errorf("duplicate node ID %q", n.ID)
		}
		ids[n.ID] = true
		if n.Kind == NodeConnection {
			connections++
		}
	})
	if connections != 4 {
		t.Errorf("%d connections, want 4", connections)
	}

	want := map[string][2]int{"users": {500, 8}, "orders": {1_000_000, 12}, "wide_events": {100_000, 200}, "active_users": {500, 6}}
	found := map[string]bool{}
	walk(roots, func(n *Node) {
		if n.Kind != NodeTable && n.Kind != NodeView {
			return
		}
		ds, ok := DatasetFor(n.ConnectionID, n.Table)
		if !ok {
			t.Errorf("no dataset for %s", n.ID)
			return
		}
		if n.ConnectionID != "local-pg" {
			return
		}
		found[n.Label] = true
		w := want[n.Label]
		if ds.Rows != w[0] || len(ds.Columns) != w[1] {
			t.Errorf("%s: %d rows x %d cols, want %d x %d", n.Label, ds.Rows, len(ds.Columns), w[0], w[1])
		}
		if n.Label == "active_users" && (n.Kind != NodeView || !ds.ReadOnly) {
			t.Errorf("active_users must be a read-only view, got kind %d readOnly %v", n.Kind, ds.ReadOnly)
		}
	})
	for name := range want {
		if !found[name] {
			t.Errorf("table %s missing under local-pg", name)
		}
	}
}

func TestDatasetForUnknownTable(t *testing.T) {
	if _, ok := DatasetFor("local-pg", database.TableRef{Catalog: "app", Schema: "public", Name: "nope"}); ok {
		t.Error("unknown table returned a dataset")
	}
	if _, ok := DatasetFor("nope", database.TableRef{Catalog: "app", Schema: "public", Name: "users"}); ok {
		t.Error("unknown connection returned a dataset")
	}
}
