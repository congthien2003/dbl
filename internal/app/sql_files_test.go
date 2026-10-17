package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dbl/internal/storage"
)

type blockedSQLFiles struct {
	storage.SQLFiles
	started, release chan struct{}
	once             sync.Once
	writes           atomic.Int32
}

func (b *blockedSQLFiles) Write(req storage.SQLWriteRequest) (*storage.SQLFile, error) {
	b.writes.Add(1)
	b.once.Do(func() { close(b.started) })
	<-b.release
	return b.SQLFiles.Write(req)
}
func newBlockedSQLFiles(t *testing.T, root string) (*blockedSQLFiles, func()) {
	b := &blockedSQLFiles{SQLFiles: storage.NewSQLStore(root), started: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(b.release) }) }
	t.Cleanup(release)
	return b, release
}
func TestSQLSaveUsesCapturedRevision(t *testing.T) {
	e := newSQLEnv(t)
	root := t.TempDir()
	path := filepath.Join(root, "x.sql")
	files, release := newBlockedSQLFiles(t, root)
	e.s.SQLServices = NewSQLServices(files, &testSQLDialogs{path: path})
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	tab := e.s.Workspace.NewSQL(e.connID)
	e.s.SetSQLText(tab, "select 1")
	continued := false
	e.s.SaveSQLFile(tab, false, func() { continued = true })
	e.post.run(t)
	select {
	case <-files.started:
	case <-time.After(time.Second):
		t.Fatal("save writer did not start")
	}
	e.s.SetSQLText(tab, "select 2")
	release()
	e.post.run(t)
	if !tab.Dirty() || tab.SQL.Document.SavedText != "select 1" || tab.SQL.Document.Text != "select 2" || continued {
		t.Fatal("concurrent edit was marked saved or closed")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "select 1" {
		t.Fatal(string(data), err)
	}
}
func TestSQLSaveAsReservations(t *testing.T) {
	e := newSQLEnv(t)
	root := t.TempDir()
	path := filepath.Join(root, "shared.sql")
	files, release := newBlockedSQLFiles(t, root)
	e.s.SQLServices = NewSQLServices(files, &testSQLDialogs{path: path})
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	a, b := e.s.Workspace.NewSQL(e.connID), e.s.Workspace.NewSQL(e.connID)
	e.s.SetSQLText(a, "select 1")
	e.s.SetSQLText(b, "select 2")
	e.s.SaveSQLFile(a, true, nil)
	e.post.run(t)
	select {
	case <-files.started:
	case <-time.After(time.Second):
		t.Fatal("save writer did not start")
	}
	e.s.SaveSQLFile(b, true, nil)
	e.post.run(t)
	if b.SQL.Document.FileErr == nil || !b.Dirty() || files.writes.Load() != 1 {
		t.Fatal("competing Save As reached the writer")
	}
	release()
	e.post.run(t)
	if a.Dirty() || !b.Dirty() {
		t.Fatal("wrong document baseline after concurrent Save As")
	}
}

type testSQLDialogs struct{ path string }

func (d *testSQLDialogs) OpenSQL() (string, error)       { return d.path, nil }
func (d *testSQLDialogs) SaveSQL(string) (string, error) { return d.path, nil }

func TestSQLDocumentDirtyUndoAndNewEmpty(t *testing.T) {
	s := &State{}
	tab := s.Workspace.NewSQL("")
	if tab.Dirty() {
		t.Fatal("empty new document is dirty")
	}
	s.SetSQLText(tab, "select 1")
	if !tab.Dirty() {
		t.Fatal("edit was not tracked")
	}
	s.SetSQLText(tab, "")
	if tab.Dirty() {
		t.Fatal("undo to empty baseline is dirty")
	}
}
func TestSQLSaveFailureKeepsAssociation(t *testing.T) {
	e := newSQLEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "query.sql")
	e.s.Post = e.post.post
	e.s.SQLServices = NewSQLServices(storage.NewSQLStore(dir), &testSQLDialogs{path: path})
	defer e.s.CloseSQL(context.Background())
	tab := e.s.Workspace.NewSQL(e.connID)
	e.s.SetSQLText(tab, "select 1;\n")
	if !e.s.SaveSQLFile(tab, false, nil) {
		t.Fatal("save did not start")
	}
	e.post.run(t)
	e.post.run(t)
	if tab.Dirty() || tab.SQL.Document.File == nil {
		t.Fatal("successful save did not update baseline")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "select 1;\n" {
		t.Fatal(string(data), err)
	}
	e.s.SetSQLText(tab, "select 2;")
	e.s.SQLServices.Dialogs = &testSQLDialogs{path: ""}
	e.s.SaveSQLFile(tab, true, nil)
	e.post.run(t)
	if !tab.Dirty() || tab.SQL.Document.File.Path != path {
		t.Fatal("canceled Save As lost buffer/association")
	}
}
