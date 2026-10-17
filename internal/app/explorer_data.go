package app

import (
	"path/filepath"

	"dbl/internal/connection"
	"dbl/internal/database"
)

// SchemaNode is a schema of a connected database and, once read, its tables
// and views.
type SchemaNode struct {
	Name   string
	State  OpState
	Err    *database.Error
	Tables []database.Table

	op opSlot
}

// ConnTree is what the explorer knows below one connection: the schemas of
// its database and their tables, read lazily as nodes are opened. It
// belongs to one session; a new session starts an empty tree.
type ConnTree struct {
	Epoch    uint64
	Database string
	State    OpState
	Err      *database.Error
	Schemas  []*SchemaNode

	op opSlot
}

// Schema returns the node of a schema.
func (t *ConnTree) Schema(name string) *SchemaNode {
	for _, sc := range t.Schemas {
		if sc.Name == name {
			return sc
		}
	}
	return nil
}

// ExplorerData holds the trees of the connected connections. Rev changes
// whenever anything in it does, so the view rebuilds only then.
type ExplorerData struct {
	Rev   int
	trees map[string]*ConnTree
}

// Tree returns the tree of a connection, or nil when it has no open
// session. A tree of an older session is dropped and replaced by an empty
// one, so nothing read on a closed session is shown on a new one.
func (s *State) Tree(connectionID string) *ConnTree {
	ex := &s.Explorer
	epoch := s.Conns.Epoch(connectionID)
	t := ex.trees[connectionID]
	if epoch == 0 {
		if t != nil {
			t.stop()
			delete(ex.trees, connectionID)
			ex.Rev++
		}
		return nil
	}
	if t != nil && t.Epoch == epoch {
		return t
	}
	if t != nil {
		t.stop()
	}
	if ex.trees == nil {
		ex.trees = map[string]*ConnTree{}
	}
	t = &ConnTree{Epoch: epoch}
	if p, ok := s.Catalog.Profile(connectionID); ok {
		t.Database = p.Database
		if p.Driver == connection.DriverSQLite {
			t.Database = filepath.Base(p.SQLitePath)
		}
	}
	ex.trees[connectionID] = t
	ex.Rev++
	return t
}

func (t *ConnTree) stop() {
	t.op.stop()
	for _, sc := range t.Schemas {
		sc.op.stop()
	}
}

// EnsureSchemas reads the schemas of a connection's database unless they
// were read or are being read. The explorer calls it when the database node
// is open.
func (s *State) EnsureSchemas(connectionID string) {
	if t := s.Tree(connectionID); t != nil && t.State == OpIdle {
		s.loadSchemas(connectionID, t)
	}
}

// RefreshSchemas reads the schemas again, and the tables of the schemas
// that were open.
func (s *State) RefreshSchemas(connectionID string) {
	if t := s.Tree(connectionID); t != nil {
		s.loadSchemas(connectionID, t)
	}
}

func (s *State) loadSchemas(connectionID string, t *ConnTree) {
	ad, ok := s.Conns.SQL(connectionID)
	o := t.op.begin(s, connectionID, MetadataTimeout)
	t.State, t.Err = OpLoading, nil
	s.Explorer.Rev++
	if !ok || o == nil {
		t.State, t.Err = OpFailed, noSession()
		return
	}
	go func() {
		schemas, err := ad.ListSchemas(o.ctx)
		s.post(func() {
			if !t.op.finish(s, connectionID, o) || s.Explorer.trees[connectionID] != t {
				return
			}
			s.Explorer.Rev++
			if err != nil {
				t.State, t.Err = OpFailed, asDBError(err)
				return
			}
			// Keep the tables already read of schemas that are still there.
			old := t.Schemas
			t.Schemas = t.Schemas[:0:0]
			for _, sc := range schemas {
				node := &SchemaNode{Name: sc.Name}
				for _, prev := range old {
					if prev.Name == sc.Name {
						node = prev
					}
				}
				t.Schemas = append(t.Schemas, node)
			}
			t.State = OpReady
		})
	}()
}

// EnsureTables reads the tables and views of a schema unless they were
// read or are being read. The explorer calls it when the schema node is
// open.
func (s *State) EnsureTables(connectionID, schema string) {
	t := s.Tree(connectionID)
	if t == nil {
		return
	}
	if sc := t.Schema(schema); sc != nil && sc.State == OpIdle {
		s.loadTables(connectionID, t, sc)
	}
}

// RefreshTables reads the tables of a schema again.
func (s *State) RefreshTables(connectionID, schema string) {
	t := s.Tree(connectionID)
	if t == nil {
		return
	}
	if sc := t.Schema(schema); sc != nil {
		s.loadTables(connectionID, t, sc)
	}
}

func (s *State) loadTables(connectionID string, t *ConnTree, sc *SchemaNode) {
	ad, ok := s.Conns.SQL(connectionID)
	o := sc.op.begin(s, connectionID, MetadataTimeout)
	sc.State, sc.Err = OpLoading, nil
	s.Explorer.Rev++
	if !ok || o == nil {
		sc.State, sc.Err = OpFailed, noSession()
		return
	}
	name := sc.Name
	go func() {
		tables, err := ad.ListTables(o.ctx, name)
		s.post(func() {
			if !sc.op.finish(s, connectionID, o) || s.Explorer.trees[connectionID] != t {
				return
			}
			s.Explorer.Rev++
			if err != nil {
				sc.State, sc.Err = OpFailed, asDBError(err)
				return
			}
			sc.Tables, sc.State = tables, OpReady
		})
	}()
}
