package mock

import "dbl/internal/database"

// NodeKind is the type of a node in the fake connection tree.
type NodeKind uint8

// Node kinds.
const (
	NodeFolder NodeKind = iota
	NodeConnection
	NodeSchema
	NodeTable
	NodeView
)

// Node is an entry of the fake connection tree.
type Node struct {
	ID       string
	Label    string
	Kind     NodeKind
	Children []*Node

	// ConnectionID is set on a connection and everything below it.
	ConnectionID string
	// Table identifies the table or view of a NodeTable or NodeView.
	Table database.TableRef
}

const (
	mockCatalog = "app"
	mockSchema  = "public"
)

type tableSpec struct {
	name     string
	rows     int
	cols     int
	readOnly bool
	view     bool
}

var tableSpecs = []tableSpec{
	{name: "users", rows: 500, cols: 8},
	{name: "orders", rows: 1_000_000, cols: 12},
	{name: "wide_events", rows: 100_000, cols: 200},
	{name: "active_users", rows: 500, cols: 6, readOnly: true, view: true},
}

type connSpec struct {
	id, label string
}

func connection(c connSpec) *Node {
	schema := &Node{ID: c.id + "/" + mockSchema, Label: mockSchema, Kind: NodeSchema, ConnectionID: c.id}
	for _, t := range tableSpecs {
		kind := NodeTable
		if t.view {
			kind = NodeView
		}
		schema.Children = append(schema.Children, &Node{
			ID:           c.id + "/" + mockSchema + "/" + t.name,
			Label:        t.name,
			Kind:         kind,
			ConnectionID: c.id,
			Table:        database.TableRef{Catalog: mockCatalog, Schema: mockSchema, Name: t.name},
		})
	}
	return &Node{ID: c.id, Label: c.label, Kind: NodeConnection, ConnectionID: c.id, Children: []*Node{schema}}
}

// Catalog returns a fresh copy of the fake connection tree: folder "Work"
// (two connections), folder "Personal" (one) and one connection at the root.
func Catalog() []*Node {
	work := &Node{ID: "folder:work", Label: "Work", Kind: NodeFolder, Children: []*Node{
		connection(connSpec{"local-pg", "Local PG"}),
		connection(connSpec{"staging-pg", "Staging PG"}),
	}}
	personal := &Node{ID: "folder:personal", Label: "Personal", Kind: NodeFolder, Children: []*Node{
		connection(connSpec{"scratch", "Scratch"}),
	}}
	return []*Node{work, personal, connection(connSpec{"sandbox", "Sandbox"})}
}

// DatasetFor returns a new dataset for a table of the fake catalog.
func DatasetFor(connectionID string, ref database.TableRef) (*Dataset, bool) {
	if !knownConnection(connectionID) || ref.Catalog != mockCatalog || ref.Schema != mockSchema {
		return nil, false
	}
	for _, t := range tableSpecs {
		if t.name == ref.Name {
			return NewDataset(t.name, t.rows, t.cols, t.readOnly), true
		}
	}
	return nil, false
}

func knownConnection(id string) bool {
	switch id {
	case "local-pg", "staging-pg", "scratch", "sandbox":
		return true
	}
	return false
}
