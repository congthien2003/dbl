package app

import (
	"context"
	"testing"
	"time"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/database/fakedb"
	"dbl/internal/storage"
)

// labeledServer is a fake MySQL session that names its server.
type labeledServer struct{ *fakedb.Adapter }

func (labeledServer) ServerLabel() string { return "db.internal:3306 · MySQL 8.4.11" }

func newMySQLEnv(t *testing.T, label bool) (*State, *queuePost, string) {
	t.Helper()
	post := &queuePost{ch: make(chan func(), 64)}
	st := &State{Settings: storage.DefaultSettings()}
	connect := func(context.Context, connection.Profile, string) (database.Adapter, error) {
		ad := fakedb.New()
		ad.AddTable("shop", fakedb.UsersTable("users", 3))
		if label {
			return labeledServer{ad}, nil
		}
		return ad, nil
	}
	st.Conns = connection.NewManager(&st.Catalog, nil, connect, post.post)
	p, err := st.Catalog.AddProfile(connection.Profile{
		Name: "Shop", Driver: connection.DriverMySQL, Host: "db.internal", Port: 3306, Username: "root",
		TLS: connection.TLSOptions{Mode: connection.TLSDisable},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Conns.CloseAll(time.Second) })
	st.Connect(p.ID)
	post.run(t)
	if st.Conns.Status(p.ID) != connection.Connected {
		t.Fatalf("status %v", st.Conns.Status(p.ID))
	}
	return st, post, p.ID
}

func TestMySQLTreeIsLabeledByServer(t *testing.T) {
	st, _, id := newMySQLEnv(t, true)
	if got := st.Tree(id).Database; got != "db.internal:3306 · MySQL 8.4.11" {
		t.Errorf("label %q", got)
	}
}

func TestMySQLTreeFallsBackToAddress(t *testing.T) {
	st, post, id := newMySQLEnv(t, false)
	tr := st.Tree(id)
	if tr.Database != "db.internal:3306" {
		t.Errorf("label %q", tr.Database)
	}
	st.EnsureSchemas(id)
	post.run(t)
	if tr.State != OpReady || tr.Schema("shop") == nil {
		t.Errorf("tree %+v", tr)
	}
}
