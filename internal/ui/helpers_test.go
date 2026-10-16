package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/grid"
	"dbl/internal/mock"
	"dbl/internal/storage"
)

// uiEnv is a shell on a sample catalog with a manager whose connector and
// credential store are fakes, drawn in a 1280x800 window.
type uiEnv struct {
	t     *testing.T
	sh    *Shell
	tt    *mygoui.Tester
	post  chan func()
	calls chan *fakeCall
	creds *fakeStore
	saves []connection.Catalog
}

type fakeCall struct {
	profile  connection.Profile
	password string
	reply    chan fakeReply
}

type fakeReply struct {
	ad  database.Adapter
	err error
}

type fakeAdapter struct{ closed chan struct{} }

func (a *fakeAdapter) Ping(context.Context) error { return nil }
func (a *fakeAdapter) Close() error {
	select {
	case a.closed <- struct{}{}:
	default:
	}
	return nil
}

type fakeStore struct {
	mu      sync.Mutex
	secrets map[string]string
	sets    chan string
}

func (s *fakeStore) Get(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.secrets[ref]; ok {
		return v, nil
	}
	return "", connection.ErrSecretNotFound
}
func (s *fakeStore) Set(ref, pw string) error {
	s.mu.Lock()
	s.secrets[ref] = pw
	s.mu.Unlock()
	s.sets <- ref
	return nil
}
func (s *fakeStore) Delete(ref string) error {
	s.mu.Lock()
	delete(s.secrets, ref)
	s.mu.Unlock()
	return nil
}

// newEnv returns a shell with the sample catalog: folder Work (Local PG,
// Staging PG), folder Personal (Scratch) and Sandbox at the root.
func newEnv(t *testing.T) *uiEnv {
	t.Helper()
	e := newEmptyEnv(t)
	cat := &e.sh.st.Catalog
	work, _ := cat.AddFolder("Work")
	personal, _ := cat.AddFolder("Personal")
	for _, spec := range []struct{ name, folder string }{
		{"Local PG", work.ID}, {"Staging PG", work.ID}, {"Scratch", personal.ID}, {"Sandbox", ""},
	} {
		if _, err := cat.AddProfile(connection.Profile{
			Name: spec.name, FolderID: spec.folder, Driver: connection.DriverPostgres,
			Host: "localhost", Port: 5432, Database: "app", Username: "postgres",
		}); err != nil {
			t.Fatal(err)
		}
	}
	e.saves = nil
	e.tt.Frame()
	return e
}

// newEmptyEnv returns a shell with no connections.
func newEmptyEnv(t *testing.T) *uiEnv {
	t.Helper()
	e := &uiEnv{
		t:     t,
		post:  make(chan func(), 32),
		calls: make(chan *fakeCall, 8),
		creds: &fakeStore{secrets: map[string]string{}, sets: make(chan string, 8)},
	}
	st := &app.State{Settings: storage.DefaultSettings()}
	connect := func(ctx context.Context, p connection.Profile, pw string) (database.Adapter, error) {
		c := &fakeCall{profile: p, password: pw, reply: make(chan fakeReply, 1)}
		e.calls <- c
		select {
		case r := <-c.reply:
			return r.ad, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	st.Conns = connection.NewManager(&st.Catalog, e.creds, connect, func(fn func()) { e.post <- fn })
	st.ScheduleCatalogSave = func(c connection.Catalog) { e.saves = append(e.saves, c) }
	e.sh = NewShell(st)
	e.tt = mygoui.NewTester(e.sh.View, 1280, 800)
	return e
}

// runPost runs the next function a worker posted to the UI thread and draws
// a frame.
func (e *uiEnv) runPost() {
	e.t.Helper()
	select {
	case fn := <-e.post:
		fn()
	case <-time.After(2 * time.Second):
		e.t.Fatal("nothing was posted to the UI thread")
	}
	e.tt.Frame()
}

func (e *uiEnv) nextCall() *fakeCall {
	e.t.Helper()
	select {
	case c := <-e.calls:
		return c
	case <-time.After(2 * time.Second):
		e.t.Fatal("the connector was not called")
		return nil
	}
}

// profile returns the saved connection with the given name.
func (e *uiEnv) profile(name string) connection.Profile {
	e.t.Helper()
	for _, p := range e.sh.st.Catalog.Profiles {
		if p.Name == name {
			return p
		}
	}
	e.t.Fatalf("no connection named %q", name)
	return connection.Profile{}
}

// folderItem returns the explorer item of the folder with the given name.
func (e *uiEnv) folderItem(name string) string {
	e.t.Helper()
	for _, f := range e.sh.st.Catalog.Folders {
		if f.Name == name {
			return folderItem(f.ID)
		}
	}
	e.t.Fatalf("no folder named %q", name)
	return ""
}

// connect connects the named connection and draws the result.
func (e *uiEnv) connect(name string) *fakeAdapter {
	e.t.Helper()
	p := e.profile(name)
	ad := &fakeAdapter{closed: make(chan struct{}, 4)}
	e.sh.st.Connect(p.ID)
	c := e.nextCall()
	c.reply <- fakeReply{ad: ad}
	e.runPost()
	if got := e.sh.st.Conns.Status(p.ID); got != connection.Connected {
		e.t.Fatalf("status of %q is %v", name, got)
	}
	return ad
}

// newTestShell returns a shell on the sample catalog, drawn in a 1280x800
// window.
func newTestShell(t *testing.T) (*Shell, *mygoui.Tester) {
	t.Helper()
	e := newEnv(t)
	return e.sh, e.tt
}

// openTable opens a table of local-pg in the workspace, as the explorer
// does, and draws a frame.
func openTable(t *testing.T, sh *Shell, tt *mygoui.Tester, name string) *app.Tab {
	t.Helper()
	key := app.TableKey{ConnectionID: "local-pg", Ref: database.TableRef{Catalog: "app", Schema: "public", Name: name}}
	ds, ok := mock.DatasetFor(key.ConnectionID, key.Ref)
	if !ok {
		t.Fatalf("no mock table %q", name)
	}
	tab, _ := sh.st.Workspace.OpenTable(key, name, ds)
	tt.Frame()
	return tab
}

// gridOf returns the grid view of the active tab.
func gridOf(t *testing.T, sh *Shell) *gridView {
	t.Helper()
	tab := sh.st.Workspace.Active()
	if tab == nil {
		t.Fatal("no active tab")
	}
	v := sh.views[tab.ID]
	if v == nil || v.grid == nil {
		t.Fatalf("tab %q has no grid view", tab.Title)
	}
	return v.grid
}

func center(r mygoui.Rect) (x, y float32) { return r.X + r.W/2, r.Y + r.H/2 }

// plainNameRow returns the first row whose name column (1) shows an
// ordinary "Name N" text, so the cell can be found by its text alone.
func plainNameRow(t *testing.T, ds *mock.Dataset) int {
	t.Helper()
	for r := 0; r < 40; r++ {
		if v := ds.Value(r, 1); v.Kind == grid.KindText && strings.HasPrefix(v.Text, "Name ") {
			return r
		}
	}
	t.Fatal("no plain name cell in the first rows")
	return -1
}

// clickCell clicks the cell showing the display text of ds.Value(row, col)
// and returns its box.
func clickCell(t *testing.T, tt *mygoui.Tester, ds *mock.Dataset, row, col int) mygoui.Rect {
	t.Helper()
	text := ds.Value(row, col).Display(maxCellRunes)
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("cell (%d,%d) showing %q is not on screen", row, col, text)
	}
	x, y := center(r)
	tt.ClickAt(x, y)
	return r
}

func doubleClickText(t *testing.T, tt *mygoui.Tester, text string) {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no element shows %q", text)
	}
	x, y := center(r)
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
}

func draftAt(tab *app.Tab, row, col int) (grid.Value, bool) {
	return tab.Drafts.Get(grid.CellRef{RowID: tab.Dataset.RowID(row), Col: col})
}
