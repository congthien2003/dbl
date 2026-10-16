package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/database/fakedb"
	"dbl/internal/storage"
)

// newTablesEnv returns a shell with one PostgreSQL connection whose session
// is the fake database db, already connected, and its explorer open down to
// the schema "public".
func newTablesEnv(t *testing.T) (*uiEnv, *fakedb.Adapter) {
	t.Helper()
	db := fakedb.New()
	db.AddTable("public", fakedb.UsersTable("users", 250))
	db.AddTable("public", fakedb.UsersTable("small", 3))
	v := fakedb.UsersTable("active_users", 4)
	v.Meta.Kind = database.ObjectView
	v.Meta.Constraints = database.Section[database.ConstraintMetadata]{State: database.SectionNotApplicable, Reason: "Views have no constraints."}
	db.AddTable("public", v)
	db.AddTable("audit", fakedb.UsersTable("log", 2))

	e := &uiEnv{
		t:     t,
		post:  make(chan func(), 64),
		calls: make(chan *fakeCall, 8),
		creds: &fakeStore{secrets: map[string]string{}, sets: make(chan string, 8)},
	}
	st := &app.State{Settings: storage.DefaultSettings()}
	connect := func(ctx context.Context, p connection.Profile, pw string) (database.Adapter, error) { return db, nil }
	st.Conns = connection.NewManager(&st.Catalog, e.creds, connect, func(fn func()) { e.post <- fn })
	p, err := st.Catalog.AddProfile(connection.Profile{
		Name: "Local PG", Driver: connection.DriverPostgres, Host: "localhost", Port: 5432, Database: "app", Username: "postgres",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.sh = NewShell(st)
	e.tt = mygoui.NewTester(e.sh.View, 1280, 800)
	e.tt.Frame()
	if e.tt.HasText("app") {
		t.Fatal("the database shows before the connection is open")
	}
	st.Connect(p.ID)
	e.tt.Frame()
	e.runPost() // connected: the connection and its database open, the schemas are asked for
	e.runPost() // the schemas arrive
	return e, db
}

func TestExplorerShowsDatabaseSchemasAndLazyTables(t *testing.T) {
	e, db := newTablesEnv(t)
	for _, want := range []string{"Local PG", "app", "public", "audit"} {
		if !e.tt.HasText(want) {
			t.Fatalf("the explorer does not show %q: %q", want, e.tt.Texts())
		}
	}
	if calls := db.Calls(); len(calls) != 1 || calls[0] != "ListSchemas" {
		t.Fatalf("calls %v: only the schemas may be read before a schema is opened", calls)
	}
	if e.tt.HasText("users") {
		t.Error("tables show before their schema is opened")
	}
	doubleClickText(t, e.tt, "public")
	e.runPost()
	for _, want := range []string{"Tables (2)", "Views (1)", "users", "small", "active_users"} {
		if !e.tt.HasText(want) {
			t.Fatalf("missing %q after opening public: %q", want, e.tt.Texts())
		}
	}
	if e.tt.HasText("log") {
		t.Error("the tables of another schema were read")
	}
}

// openUsers opens the table "users" from the explorer and runs its reads.
func openUsers(t *testing.T, e *uiEnv) *app.Tab {
	t.Helper()
	doubleClickText(t, e.tt, "public")
	e.runPost()
	doubleClickText(t, e.tt, "users")
	e.runPost() // metadata
	e.runPost() // first page
	tab := e.sh.st.Workspace.Active()
	if tab == nil || tab.Data == nil {
		t.Fatal("no table tab opened")
	}
	return tab
}

func TestOpeningATableShowsTheFirstPage(t *testing.T) {
	e, _ := newTablesEnv(t)
	tab := openUsers(t, e)
	for _, want := range []string{"user 1", "id", "name", "score", "Page 1 · rows 1–100", "Read-only"} {
		if !e.tt.HasText(want) {
			t.Errorf("missing %q: %q", want, e.tt.Texts())
		}
	}
	if !e.tt.HasText("NULL") && !e.tt.HasText("<binary 1 bytes>") {
		t.Errorf("neither NULL nor the binary placeholder is shown: %q", e.tt.Texts())
	}
	if e.tt.HasText("user 150") {
		t.Error("rows beyond the page are shown")
	}
	// The same table again focuses the tab.
	doubleClickText(t, e.tt, "users")
	if len(e.sh.st.Workspace.Tabs) != 1 || e.sh.st.Workspace.Active() != tab {
		t.Errorf("tabs %d", len(e.sh.st.Workspace.Tabs))
	}
}

func TestPagingButtonsMoveThroughPages(t *testing.T) {
	e, _ := newTablesEnv(t)
	openUsers(t, e)
	if err := e.tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	if !e.tt.HasText("Page 2 · rows 101–200") || !e.tt.HasText("user 101") {
		t.Fatalf("page 2: %q", e.tt.Texts())
	}
	if err := e.tt.Click("Previous"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText("Page 1 · rows 1–100") {
		t.Fatalf("back to page 1: %q", e.tt.Texts())
	}
}

func TestFilterBarDebouncesAndFiltersOnTheServer(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	reads := len(db.Requests())
	e.sh.views[tab.ID].filter.col = "name" // the first column, id, only compares numbers
	e.tt.Frame()
	if err := e.tt.Click("Filter value"); err != nil {
		t.Fatal(err)
	}
	e.tt.Type("user 24")
	e.tt.Advance(100 * time.Millisecond)
	if len(db.Requests()) != reads || tab.Data.Filter != nil {
		t.Fatal("the filter ran before the typing paused")
	}
	e.tt.Advance(300 * time.Millisecond)
	e.runPost()
	if tab.Data.Filter == nil || tab.Data.Filter.Op != database.OpContains || tab.Data.Filter.Value != "user 24" {
		t.Fatalf("filter %+v", tab.Data.Filter)
	}
	if tab.Data.Len() == 0 || !e.tt.HasText("user 24") || e.tt.HasText("user 3") {
		t.Errorf("rows %d: %q", tab.Data.Len(), e.tt.Texts())
	}
	if len(db.Requests()) != reads+1 {
		t.Errorf("%d reads for one filter", len(db.Requests())-reads)
	}
	// Changing the operator applies at once, with no pause to wait out.
	e.sh.views[tab.ID].filter.op = "is NULL"
	e.tt.Frame()
	e.runPost()
	if tab.Data.Filter == nil || tab.Data.Filter.Op != database.OpIsNull {
		t.Errorf("filter after the operator changed: %+v", tab.Data.Filter)
	}
}

func TestPropertiesShowsSectionsAndMissingMetadata(t *testing.T) {
	e, _ := newTablesEnv(t)
	tab := openUsers(t, e)
	if err := e.tt.Click("Properties"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	for _, want := range []string{"Columns (4)", "numeric(10,2)", "bytea", "Constraints", "users_pkey", "Triggers", "Indexes", "Primary key"} {
		if !e.tt.HasText(want) {
			t.Errorf("Properties lacks %q: %q", want, e.tt.Texts())
		}
	}
	_ = tab
	// A view: constraints are not applicable, said so rather than empty.
	doubleClickText(t, e.tt, "active_users")
	e.runPost()
	e.runPost()
	if err := e.tt.Click("Properties"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if !e.tt.HasText("Not applicable. Views have no constraints.") {
		t.Errorf("a view's constraints: %q", e.tt.Texts())
	}
	if ok, _ := e.sh.st.Workspace.Active().Data.Meta.Editable(); ok {
		t.Error("a view is editable")
	}
}

func TestMetadataFailureShowsErrorAndRetry(t *testing.T) {
	e, db := newTablesEnv(t)
	db.Hook = func(ctx context.Context, call string) error {
		if call == "DescribeTable" {
			return &database.Error{Kind: database.KindPermission, Message: "permission denied for table users"}
		}
		return nil
	}
	doubleClickText(t, e.tt, "public")
	e.runPost()
	doubleClickText(t, e.tt, "users")
	e.runPost()
	if !e.tt.HasText("Retry") || !strings.Contains(strings.Join(e.tt.Texts(), "|"), "permission denied for table users") {
		t.Fatalf("no error shown: %q", e.tt.Texts())
	}
	db.Hook = nil
	if err := e.tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	e.runPost()
	if !e.tt.HasText("user 1") {
		t.Errorf("retry did not load the table: %q", e.tt.Texts())
	}
}

func TestDisconnectClosesTabsAndTheTreeGoesAway(t *testing.T) {
	e, _ := newTablesEnv(t)
	openUsers(t, e)
	st := e.sh.st
	id := st.Catalog.Profiles[0].ID
	st.RequestDisconnect(id)
	st.ConfirmConn()
	e.tt.Frame()
	if len(st.Workspace.Tabs) != 0 {
		t.Errorf("tabs %d", len(st.Workspace.Tabs))
	}
	if e.tt.HasText("public") {
		t.Errorf("the database tree outlived the session: %q", e.tt.Texts())
	}
}

func TestHeaderClickSortsOnTheServer(t *testing.T) {
	e, db := newTablesEnv(t)
	tab := openUsers(t, e)
	if err := e.tt.Click("score"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	d := tab.Data
	if d.Sort == nil || d.Sort.Column != "score" || d.Sort.Desc || d.Plan.Strategy != database.StrategyOffset {
		t.Fatalf("sort %+v plan %v", d.Sort, d.Plan.Strategy)
	}
	reqs := db.Requests()
	if last := reqs[len(reqs)-1]; last.Sort == nil || last.Sort.Column != "score" {
		t.Errorf("the sort did not reach the adapter: %+v", last)
	}
	if err := e.tt.Click("score"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	if d.Sort == nil || !d.Sort.Desc {
		t.Errorf("second click: %+v", d.Sort)
	}
	if err := e.tt.Click("Clear sort"); err != nil {
		t.Fatal(err)
	}
	e.runPost()
	if d.Sort != nil || d.Plan.Strategy != database.StrategyKeyset {
		t.Errorf("clear: %+v %v", d.Sort, d.Plan.Strategy)
	}
}

func countText(tt *mygoui.Tester, s string) int {
	n := 0
	for _, x := range tt.Texts() {
		if x == s {
			n++
		}
	}
	return n
}

func TestKeyIconIsShownForPrimaryKeyColumns(t *testing.T) {
	e, _ := newTablesEnv(t)
	doubleClickText(t, e.tt, "public")
	e.runPost()
	doubleClickText(t, e.tt, "users")
	e.runPost() // metadata
	e.runPost() // first page
	e.tt.Frame()
	e.tt.Frame()

	// The grid header keeps room for an icon only on the primary key column,
	// and the icon is drawn at the left edges the cells reported.
	g := gridOf(t, e.sh)
	titles := map[string]string{}
	for _, c := range g.cols {
		titles[c.ID] = c.Title
	}
	for id, want := range map[string]string{"id": " id", "name": "name", "score": "score", "blob": "blob"} {
		if titles[id] != want {
			t.Errorf("header of %q is %q, want %q", id, titles[id], want)
		}
	}
	if len(g.left) != len(g.cols) {
		t.Errorf("left edges for %d of %d columns", len(g.left), len(g.cols))
	}

	if err := e.tt.Click("Properties"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if n := countText(e.tt, "Primary key column"); n != 1 {
		t.Errorf("%d primary key icons in Properties, want 1: %q", n, e.tt.Texts())
	}
}

func TestKeyIconIsShownOnceForTheIdColumn(t *testing.T) {
	e, _ := newTablesEnv(t)
	doubleClickText(t, e.tt, "public")
	e.runPost()
	doubleClickText(t, e.tt, "small")
	e.runPost()
	e.runPost()
	if err := e.tt.Click("Properties"); err != nil {
		t.Fatal(err)
	}
	e.tt.Frame()
	if n := countText(e.tt, "Primary key column"); n != 1 {
		t.Errorf("%d primary key icons, want 1 (the id column)", n)
	}
}

func TestIconsLoad(t *testing.T) {
	for name, svg := range map[string]*mygoui.SVG{
		"folder": icons.Folder, "folder open": icons.FolderOpen, "connection": icons.Connection,
		"database": icons.Database, "schema": icons.Schema,
		"table": icons.Table, "view": icons.View, "primary key": icons.PrimaryKey,
	} {
		if svg == nil {
			t.Errorf("icon %s did not load", name)
		}
	}
}

// The explorer rows carry icons now: every kind of node must still build
// and show its label.
func TestExplorerRowsWithIconsShowTheirLabels(t *testing.T) {
	e, _ := newTablesEnv(t)
	doubleClickText(t, e.tt, "public")
	e.runPost()
	for _, want := range []string{"Local PG", "app", "public", "users", "active_users"} {
		if !e.tt.HasText(want) {
			t.Errorf("missing %q: %q", want, e.tt.Texts())
		}
	}
}

func TestTabStripLineIsCutUnderTheChosenTab(t *testing.T) {
	sh, tt := newTestShell(t)
	users := openTable(t, sh, tt, "users")
	orders := openTable(t, sh, tt, "orders")
	tt.Frame()
	tt.Frame()
	gapOrders := sh.tabGap
	if gapOrders.x1 <= gapOrders.x0 {
		t.Fatalf("no gap under the chosen tab: %+v", gapOrders)
	}
	sh.st.Workspace.Select(users.ID)
	tt.Frame()
	tt.Frame()
	gapUsers := sh.tabGap
	if gapUsers.x1 <= gapUsers.x0 || gapUsers.x0 >= gapOrders.x0 {
		t.Errorf("the gap did not move to the first tab: users %+v, orders %+v", gapUsers, gapOrders)
	}
	sh.st.Workspace.Select(orders.ID)
	tt.Frame()
	tt.Frame()
	if sh.tabGap != gapOrders {
		t.Errorf("the gap did not come back: %+v, want %+v", sh.tabGap, gapOrders)
	}
}

func TestTabStripScrollsWhenTabsOverflow(t *testing.T) {
	sh, tt := newTestShell(t)
	ws := &sh.st.Workspace
	var first, last *app.Tab
	for i := range 30 {
		tab := ws.NewSQL("local-pg")
		if i == 0 {
			first = tab
		}
		last = tab
	}
	frames := func() {
		for range 4 {
			tt.Frame()
		}
	}
	frames()
	// A tab scrolled out of the strip is laid out with no size.
	const stripLeft, width = 260, 1280 // the strip lies right of the sidebar
	inView := func(title string) bool {
		r, ok := tt.Find(title)
		return ok && r.W > 0 && r.X >= stripLeft && r.X+r.W <= width
	}
	if !inView(last.Title) {
		r, _ := tt.Find(last.Title)
		t.Errorf("the new tab %q is not scrolled into view: %+v", last.Title, r)
	}
	if inView(first.Title) {
		t.Errorf("the first of 30 tabs is still in view with the last: the strip does not overflow")
	}
	ws.Select(first.ID)
	frames()
	if !inView(first.Title) {
		r, _ := tt.Find(first.Title)
		t.Errorf("the tab chosen %q is not scrolled into view: %+v", first.Title, r)
	}
}

func TestWheelScrollsTheTabStripSideways(t *testing.T) {
	sh, tt := newTestShell(t)
	ws := &sh.st.Workspace
	for range 30 {
		ws.NewSQL("local-pg")
	}
	for range 4 {
		tt.Frame()
	}
	at := func() float32 { r, _ := tt.Find("Query 29"); return r.X }
	r, _ := tt.Find("Query 29") // over the strip
	x0 := at()
	// Up the wheel: toward the first tab, the tabs move right.
	tt.Scroll(r.X+10, r.Y+5, 0, -200)
	tt.Frame()
	tt.Frame()
	x1 := at()
	if x1 <= x0 {
		t.Fatalf("the wheel up moved the tabs from x=%v to x=%v, want to the right", x0, x1)
	}
	// Down the wheel: back toward the last.
	tt.Scroll(r.X+10, r.Y+5, 0, 200)
	tt.Frame()
	tt.Frame()
	if x2 := at(); x2 >= x1 {
		t.Errorf("the wheel down moved the tabs from x=%v to x=%v, want to the left", x1, x2)
	}
	if sh.tabScroll.X <= 0 {
		t.Errorf("scrolled %v after going down, want a positive offset", sh.tabScroll.X)
	}
}

func TestLastRowKeepsRoomForTheHorizontalScrollBar(t *testing.T) {
	e, _ := newTablesEnv(t)
	doubleClickText(t, e.tt, "public")
	e.runPost()
	doubleClickText(t, e.tt, "users")
	e.runPost()
	e.runPost()
	e.tt.SetSize(700, 500) // narrower than the columns: the grid has a horizontal bar
	for range 3 {
		e.tt.Frame()
	}
	e.tt.Scroll(500, 300, 0, 100000) // to the last row of the page
	e.tt.Frame()
	e.tt.Frame()
	last, ok := e.tt.Find("user 100")
	if !ok {
		t.Fatalf("the last row of the page is not shown: %q", e.tt.Texts()[:20])
	}
	pager, ok := e.tt.Find("First")
	if !ok {
		t.Fatal("no pager")
	}
	// The pager starts below the grid; the bar (6 DIPs, 3 above the edge)
	// must lie under the text of the last row.
	if room := pager.Y - (last.Y + last.H); room < 9+4 {
		t.Errorf("%.1f DIPs between the text of the last row and the bottom of the grid, want room for the scroll bar", room)
	}
}
