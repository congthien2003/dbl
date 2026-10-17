package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/database/sqlite"
	"dbl/internal/grid"
	"dbl/internal/storage"
)

// newSQLiteEnv connects a State to a real SQLite file with two tables and a
// view.
func newSQLiteEnv(t *testing.T) (*State, *queuePost, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)",
		"INSERT INTO notes VALUES (1, 'first'), (2, 'second'), (3, NULL)",
		"CREATE TABLE tags (name TEXT)",
		"CREATE VIEW note_bodies AS SELECT body FROM notes",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	post := &queuePost{ch: make(chan func(), 64)}
	st := &State{Settings: storage.DefaultSettings()}
	st.Conns = connection.NewManager(&st.Catalog, nil, sqlite.Connect, post.post)
	p, err := st.Catalog.AddProfile(connection.Profile{Name: "Notes", Driver: connection.DriverSQLite, SQLitePath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Conns.CloseAll(time.Second) }) // Windows cannot delete an open file
	st.Connect(p.ID)
	post.run(t)
	if st.Conns.Status(p.ID) != connection.Connected {
		t.Fatalf("status %v", st.Conns.Status(p.ID))
	}
	return st, post, p.ID
}

func TestSQLiteTreeShowsMainWithTables(t *testing.T) {
	st, post, id := newSQLiteEnv(t)
	st.EnsureSchemas(id)
	post.run(t)
	tr := st.Tree(id)
	if tr.Database != "notes.db" {
		t.Errorf("database label %q", tr.Database)
	}
	if tr.State != OpReady || len(tr.Schemas) != 1 || tr.Schemas[0].Name != "main" {
		t.Fatalf("tree %+v", tr)
	}
	st.EnsureTables(id, "main")
	post.run(t)
	sc := tr.Schema("main")
	if sc.State != OpReady || len(sc.Tables) != 3 {
		t.Fatalf("main %+v", sc)
	}
	views := 0
	for _, tb := range sc.Tables {
		if tb.Kind == database.ObjectView {
			views++
		}
	}
	if views != 1 {
		t.Errorf("%d views", views)
	}
}

func TestSQLiteOpenTableReadsRows(t *testing.T) {
	st, post, id := newSQLiteEnv(t)
	tab := st.OpenTable(id, database.Table{Ref: database.TableRef{Catalog: "notes.db", Schema: "main", Name: "notes"}})
	post.run(t) // metadata
	post.run(t) // first page
	d := tab.Data
	if d == nil || d.Len() != 3 {
		t.Fatalf("data %+v", d)
	}
	if got := ids(d); len(got) != 3 || got[0] != "1" || got[2] != "3" {
		t.Errorf("ids %v", got)
	}
	if v := d.Value(2, 1); v.Kind != 0 { // NULL
		t.Errorf("row 3 body %+v", v)
	}
}

func TestSQLiteEditSaveReloadsFromTheFile(t *testing.T) {
	st, post, id := newSQLiteEnv(t)
	tab := st.OpenTable(id, database.Table{Ref: database.TableRef{Catalog: "notes.db", Schema: "main", Name: "notes"}})
	post.run(t) // metadata
	post.run(t) // first page
	d := tab.Data
	st.SetDraft(tab, 0, 1, grid.Value{Kind: grid.KindText, Text: "edited"})
	if !tab.Dirty() {
		t.Fatal("the edit made no draft")
	}
	if !st.Save(tab, nil) {
		t.Fatal("Save did not start")
	}
	post.run(t) // the save finishes
	if tab.Dirty() || d.SaveState != OpReady || d.SaveErr != nil {
		t.Fatalf("after save: dirty %v state %v err %v", tab.Dirty(), d.SaveState, d.SaveErr)
	}
	post.run(t) // the reload finishes
	if got := d.Value(0, 1); got.Text != "edited" {
		t.Errorf("after reload: %+v", got)
	}
}

func TestSQLiteSQLTabRunsStatementsOnTheFile(t *testing.T) {
	st, post, id := newSQLiteEnv(t)
	st.SQLServices = NewSQLServices(nil, nil)
	st.Post = post.post
	t.Cleanup(func() { _ = st.CloseSQL(context.Background()) })
	tab := st.Workspace.NewSQL(id)
	text := "INSERT INTO notes VALUES (4, 'fourth'); SELECT count(*) AS n FROM notes"
	// Selecting the whole text runs both statements; the cursor alone runs one.
	if !st.ExecuteSQL(tab, database.SQLInput{Text: text, Selections: []database.SQLSpan{{Start: 0, End: len(text)}}}, false) {
		t.Fatal("did not start")
	}
	post.run(t) // the session opens
	post.run(t) // the run ends
	d := tab.SQL
	if d.Err != nil || d.Execution == nil || d.Execution.Stop != database.SQLCompleted || len(d.Execution.Results) != 2 {
		t.Fatalf("err %v execution %+v", d.Err, d.Execution)
	}
	if got := d.Execution.Results[1].Rows[0][0].Text; got != "4" {
		t.Errorf("count %q", got)
	}
	if tag := d.Execution.Results[0].CommandTag; tag != "INSERT 1" {
		t.Errorf("tag %q", tag)
	}
}
