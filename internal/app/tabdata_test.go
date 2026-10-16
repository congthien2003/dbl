package app

import (
	"context"
	"testing"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/database/fakedb"
	"dbl/internal/storage"
)

// sqlEnv is a state with one connected profile whose session is a fake
// in-memory database. Work finishing on workers reaches the state when the
// test runs the posted functions.
type sqlEnv struct {
	t      *testing.T
	s      *State
	post   *queuePost
	db     *fakedb.Adapter
	connID string
}

func newSQLEnv(t *testing.T) *sqlEnv {
	t.Helper()
	e := &sqlEnv{t: t, post: &queuePost{ch: make(chan func(), 64)}, db: fakedb.New()}
	e.db.AddTable("public", fakedb.UsersTable("users", 250))
	e.db.AddTable("public", fakedb.UsersTable("small", 3))
	e.db.AddTable("sales", fakedb.UsersTable("orders", 5))
	st := &State{Settings: storage.DefaultSettings()}
	connect := func(ctx context.Context, p connection.Profile, pw string) (database.Adapter, error) { return e.db, nil }
	st.Conns = connection.NewManager(&st.Catalog, nil, connect, e.post.post)
	p, err := st.Catalog.AddProfile(connection.Profile{
		Name: "pg", Driver: connection.DriverPostgres, Host: "h", Port: 5432, Database: "app", Username: "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.s, e.connID = st, p.ID
	st.Connect(p.ID)
	e.post.run(t) // the connection attempt finishes
	if st.Conns.Status(p.ID) != connection.Connected {
		t.Fatalf("status %v", st.Conns.Status(p.ID))
	}
	return e
}

func (e *sqlEnv) users() database.Table {
	return database.Table{Ref: database.TableRef{Catalog: "app", Schema: "public", Name: "users"}}
}

// open opens a table and runs the metadata and first page reads.
func (e *sqlEnv) open(name string) *Tab {
	e.t.Helper()
	tab := e.s.OpenTable(e.connID, database.Table{Ref: database.TableRef{Catalog: "app", Schema: "public", Name: name}})
	e.post.run(e.t) // metadata
	e.post.run(e.t) // first page
	return tab
}

func ids(d *TableData) []string {
	var out []string
	for r := 0; r < d.Len(); r++ {
		out = append(out, d.Value(r, 0).Text)
	}
	return out
}

func TestOpenTableReadsMetadataThenFirstPage(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	if d.MetaState != OpReady || d.PageState != OpReady || d.Meta == nil {
		t.Fatalf("states meta %v page %v", d.MetaState, d.PageState)
	}
	if d.Len() != 100 || d.Page.Number != 1 || d.Page.HasPrev || !d.Page.HasNext {
		t.Errorf("page len %d number %d prev %v next %v", d.Len(), d.Page.Number, d.Page.HasPrev, d.Page.HasNext)
	}
	if d.Plan.Strategy != database.StrategyKeyset || !d.Meta.Identity.Known() {
		t.Errorf("plan %+v identity %+v", d.Plan, d.Meta.Identity)
	}
	if d.Value(0, 0).Text != "1" || d.Columns()[3].Kind == 0 {
		t.Errorf("first value %v", d.Value(0, 0))
	}
	if !d.ReadOnly() {
		t.Error("PostgreSQL tables are read-only in this phase")
	}
	for _, c := range d.Columns() {
		if c.Editable {
			t.Errorf("column %s is editable", c.Name)
		}
	}
	// Opening the same table again focuses the tab and reads nothing.
	calls := len(e.db.Calls())
	again := e.s.OpenTable(e.connID, e.users())
	if again != tab || len(e.db.Calls()) != calls {
		t.Errorf("a second open created a tab or read again")
	}
}

func TestKeysetPagingForwardBackAndCache(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data

	e.s.NextPage(tab)
	if d.PageState != OpLoading {
		t.Fatalf("state %v", d.PageState)
	}
	e.post.run(t)
	if ids(d)[0] != "101" || d.Page.Number != 2 || !d.Page.HasPrev || !d.Page.HasNext {
		t.Fatalf("page 2: first %s number %d prev %v next %v", ids(d)[0], d.Page.Number, d.Page.HasPrev, d.Page.HasNext)
	}
	if r := e.db.Requests(); r[len(r)-1].Cursor.Dir != database.CursorAfter || r[len(r)-1].Cursor.Key[0] != "100" {
		t.Errorf("request %+v", r[len(r)-1])
	}
	e.s.NextPage(tab)
	e.post.run(t)
	if d.Len() != 50 || d.Page.HasNext {
		t.Errorf("last page len %d next %v", d.Len(), d.Page.HasNext)
	}
	e.s.NextPage(tab) // no next page: nothing happens
	if d.PageState != OpReady {
		t.Errorf("state %v", d.PageState)
	}

	reads := len(e.db.Requests())
	e.s.PrevPage(tab) // page 2 was cached when it was passed
	if ids(d)[0] != "101" || len(e.db.Requests()) != reads {
		t.Errorf("going back read again: first %s, reads %d -> %d", ids(d)[0], reads, len(e.db.Requests()))
	}
	e.s.FirstPage(tab)
	if ids(d)[0] != "1" || len(e.db.Requests()) != reads {
		t.Errorf("first page not from cache")
	}

	// Reload bypasses the cache.
	e.s.ReloadTable(tab)
	e.post.run(t)
	if len(e.db.Requests()) != reads+1 {
		t.Errorf("reload did not read")
	}
}

func TestPrevPageUsesBeforeCursor(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	e.s.NextPage(tab)
	e.post.run(t)
	tab.Data.cache.Clear() // force a read
	e.s.PrevPage(tab)
	e.post.run(t)
	r := e.db.Requests()
	if last := r[len(r)-1]; last.Cursor.Dir != database.CursorBefore || last.Cursor.Key[0] != "101" {
		t.Errorf("request %+v", last)
	}
	if got := ids(tab.Data); got[0] != "1" || got[99] != "100" || tab.Data.Page.HasPrev || !tab.Data.Page.HasNext {
		t.Errorf("page %s..%s prev %v", got[0], got[99], tab.Data.Page.HasPrev)
	}
}

func TestSortSwitchesToOffsetAndResetsPage(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.s.NextPage(tab)
	e.post.run(t)
	if err := e.s.SetSort(tab, &database.Sort{Column: "id", Desc: true}); err != nil {
		t.Fatal(err)
	}
	e.post.run(t)
	if d.Plan.Strategy != database.StrategyOffset || ids(d)[0] != "250" || d.Page.Number != 1 {
		t.Fatalf("plan %v first %s number %d", d.Plan.Strategy, ids(d)[0], d.Page.Number)
	}
	e.s.NextPage(tab)
	e.post.run(t)
	if ids(d)[0] != "150" || d.Page.Req.Offset != 100 {
		t.Errorf("offset page: first %s offset %d", ids(d)[0], d.Page.Req.Offset)
	}
	if err := e.s.SetSort(tab, nil); err != nil {
		t.Fatal(err)
	}
	e.post.run(t)
	if d.Plan.Strategy != database.StrategyKeyset || ids(d)[0] != "1" {
		t.Errorf("clearing the sort: plan %v first %s", d.Plan.Strategy, ids(d)[0])
	}
	if err := e.s.SetSort(tab, &database.Sort{Column: "blob"}); err == nil {
		t.Error("sorting by a binary column was accepted")
	}
}

func TestFilterResetsPageAndRunsOnServer(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.s.NextPage(tab)
	e.post.run(t)
	f := &database.Filter{Column: "name", Op: database.OpContains, Value: "USER 2"}
	if err := e.s.SetFilter(tab, f); err != nil {
		t.Fatal(err)
	}
	e.post.run(t)
	if d.Page.Number != 1 || d.Len() != 62 || d.Page.Req.Filter == nil || d.Page.Req.Filter.Value != "USER 2" {
		t.Errorf("filtered page: number %d len %d", d.Page.Number, d.Len())
	}
	if err := e.s.SetFilter(tab, &database.Filter{Column: "blob", Op: database.OpEquals, Value: "x"}); err == nil {
		t.Error("an equality filter on a binary column was accepted")
	}
	if d.Filter != f {
		t.Error("a refused filter changed the state")
	}
	if err := e.s.SetFilter(tab, &database.Filter{Column: "score", Op: database.OpIsNull}); err != nil {
		t.Fatal(err)
	}
	e.post.run(t)
	if d.Len() != 35 { // every seventh of 250
		t.Errorf("NULL filter gave %d rows", d.Len())
	}
	if err := e.s.SetFilter(tab, nil); err != nil {
		t.Fatal(err)
	}
	e.post.run(t)
	if d.Filter != nil || d.Len() != 100 {
		t.Errorf("cleared filter: %d rows", d.Len())
	}
}

// blockFetches makes FetchPage calls wait until released or canceled, and
// reports each call's end.
type blockFetches struct {
	release chan struct{}
	started chan int
	n       int
}

func (b *blockFetches) hook(ctx context.Context, call string) error {
	if call != "FetchPage" {
		return nil
	}
	b.n++
	n := b.n
	b.started <- n
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSupersededRequestNeverOverwritesNewerState(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	b := &blockFetches{release: make(chan struct{}), started: make(chan int, 8)}
	e.db.Hook = b.hook

	_ = e.s.SetFilter(tab, &database.Filter{Column: "name", Op: database.OpContains, Value: "user 1"}) // A
	<-b.started
	_ = e.s.SetFilter(tab, &database.Filter{Column: "id", Op: database.OpEquals, Value: "7"}) // B
	<-b.started
	close(b.release)

	// A was canceled; its result, whatever it is, arrives and is dropped.
	e.post.run(t)
	e.post.run(t)
	if d.PageState != OpReady || d.Len() != 1 || d.Value(0, 0).Text != "7" {
		t.Fatalf("state %v rows %d first %q: the older request won", d.PageState, d.Len(), d.Value(0, 0).Text)
	}
	if d.Filter == nil || d.Filter.Column != "id" {
		t.Errorf("filter %+v", d.Filter)
	}
}

func TestClosingTabCancelsAndDropsResult(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	b := &blockFetches{release: make(chan struct{}), started: make(chan int, 8)}
	e.db.Hook = b.hook
	e.s.NextPage(tab)
	<-b.started
	e.s.Workspace.Close(tab.ID)
	e.post.run(t) // the canceled read reports; nothing may panic or change a tab
	if e.s.Workspace.Find(tab.ID) != nil {
		t.Error("the tab came back")
	}
	if tab.Data.Page != nil {
		t.Error("the closed tab still holds its rows")
	}
}

func TestFailedPageKeepsTheOldRows(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.db.Hook = func(ctx context.Context, call string) error {
		if call == "FetchPage" {
			return &database.Error{Kind: database.KindTimeout, Message: "slow"}
		}
		return nil
	}
	e.s.NextPage(tab)
	e.post.run(t)
	if d.PageState != OpFailed || d.PageErr == nil || d.PageErr.Kind != database.KindTimeout {
		t.Fatalf("state %v err %v", d.PageState, d.PageErr)
	}
	if d.Len() != 100 || ids(d)[0] != "1" || d.Page.Number != 1 {
		t.Errorf("the rows in view were lost: %d rows", d.Len())
	}
	e.db.Hook = nil
	e.s.ReloadTable(tab) // retry
	e.post.run(t)
	if d.PageState != OpReady || d.PageErr != nil {
		t.Errorf("state %v err %v", d.PageState, d.PageErr)
	}
}

func TestDisconnectCancelsReadsAndDropsTree(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	b := &blockFetches{release: make(chan struct{}), started: make(chan int, 8)}
	e.db.Hook = b.hook
	e.s.EnsureSchemas(e.connID)
	e.post.run(t)
	if tr := e.s.Tree(e.connID); tr == nil || tr.State != OpReady || len(tr.Schemas) != 2 {
		t.Fatalf("tree %+v", tr)
	}
	e.s.NextPage(tab)
	<-b.started
	e.s.Conns.Disconnect(e.connID)
	e.post.run(t) // the canceled read reports and is dropped
	if tab.Data.Page.Number != 1 {
		t.Errorf("a read of a closed session changed the page")
	}
	if e.s.Tree(e.connID) != nil {
		t.Error("the tree of a closed session is still there")
	}
	// Reloading without a session says so instead of hanging.
	e.s.ReloadTable(tab)
	if tab.Data.PageState != OpFailed || tab.Data.PageErr.Kind != database.KindConnection {
		t.Errorf("state %v err %v", tab.Data.PageState, tab.Data.PageErr)
	}
}

func TestReconnectStartsAnEmptyTree(t *testing.T) {
	e := newSQLEnv(t)
	e.s.EnsureSchemas(e.connID)
	e.post.run(t)
	old := e.s.Tree(e.connID)
	e.s.Conns.Disconnect(e.connID)
	e.s.Connect(e.connID)
	e.post.run(t)
	tr := e.s.Tree(e.connID)
	if tr == nil || tr == old || tr.State != OpIdle || len(tr.Schemas) != 0 {
		t.Errorf("tree %+v", tr)
	}
}

func TestExplorerLazyTablesAndRefresh(t *testing.T) {
	e := newSQLEnv(t)
	e.s.EnsureSchemas(e.connID)
	e.post.run(t)
	tr := e.s.Tree(e.connID)
	if tr.Database != "app" {
		t.Errorf("database %q", tr.Database)
	}
	if calls := e.db.Calls(); len(calls) != 1 || calls[0] != "ListSchemas" {
		t.Fatalf("metadata read beyond the schemas: %v", calls)
	}
	e.s.EnsureTables(e.connID, "public")
	e.s.EnsureTables(e.connID, "public") // already loading: one read
	e.post.run(t)
	sc := tr.Schema("public")
	if sc.State != OpReady || len(sc.Tables) != 2 || tr.Schema("sales").State != OpIdle {
		t.Fatalf("public %+v sales %v", sc, tr.Schema("sales").State)
	}
	rev := e.s.Explorer.Rev
	e.db.AddTable("public", fakedb.UsersTable("extra", 1))
	e.s.RefreshTables(e.connID, "public")
	e.post.run(t)
	if len(sc.Tables) != 3 || e.s.Explorer.Rev == rev {
		t.Errorf("refresh: %d tables, rev %d -> %d", len(sc.Tables), rev, e.s.Explorer.Rev)
	}
	// A failure is a state of the node, with the error.
	e.db.Hook = func(ctx context.Context, call string) error {
		return &database.Error{Kind: database.KindPermission, Message: "no"}
	}
	e.s.RefreshTables(e.connID, "public")
	e.post.run(t)
	if sc.State != OpFailed || sc.Err.Kind != database.KindPermission || len(sc.Tables) != 3 {
		t.Errorf("state %v err %v tables %d", sc.State, sc.Err, len(sc.Tables))
	}
}

func TestMetadataRefreshKeepsPageUnlessLayoutChanges(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.s.NextPage(tab)
	e.post.run(t)
	reads := len(e.db.Requests())
	e.s.RefreshMetadata(tab)
	e.post.run(t)
	if d.Page.Number != 2 || len(e.db.Requests()) != reads {
		t.Errorf("an unchanged refresh reset the page (number %d)", d.Page.Number)
	}
	// A new column changes the layout: the page is read again from the start.
	tbl := e.db.Table("public", "users")
	tbl.Meta.Columns = append(tbl.Meta.Columns, database.ColumnMetadata{Name: "extra", Ordinal: 5, Class: database.ClassText, NativeType: "text", Nullable: true})
	for i := range tbl.Rows {
		tbl.Rows[i] = append(tbl.Rows[i], tbl.Rows[i][1])
	}
	e.s.RefreshMetadata(tab)
	e.post.run(t) // metadata
	e.post.run(t) // first page
	if len(d.Columns()) != 5 || d.Page.Number != 1 || d.Len() != 100 || d.Value(0, 4).Text != "user 1" {
		t.Errorf("layout change: %d columns, page %d", len(d.Columns()), d.Page.Number)
	}
}

func TestPrevPageWithNothingBeforeIsPageOne(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.s.NextPage(tab)
	e.post.run(t)
	// Rows ahead of the walk vanish: going back finds nothing before page 2.
	tbl := e.db.Table("public", "users")
	tbl.Rows = tbl.Rows[100:]
	d.cache.Clear()
	e.s.PrevPage(tab)
	e.post.run(t)
	if d.Page.Number != 1 || d.Page.HasPrev {
		t.Errorf("number %d, has previous %v", d.Page.Number, d.Page.HasPrev)
	}
	if d.Page.Start(d.Size) != 1 {
		t.Errorf("rows start at %d", d.Page.Start(d.Size))
	}
}

func TestBuildColumnsMarksPrimaryKeys(t *testing.T) {
	m := fakedb.UsersTable("orders", 1).Meta
	m.MarkKeys()
	cols := buildColumns(m)
	for i, want := range []bool{true, false, false, false} {
		if cols[i].PrimaryKey != want {
			t.Errorf("column %q: pk=%v, want %v", cols[i].Name, cols[i].PrimaryKey, want)
		}
	}
}
