package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/storage"
)

// ---- fakes: the manager is real, its edges are not ----

type queuePost struct{ ch chan func() }

func (p *queuePost) post(fn func()) { p.ch <- fn }
func (p *queuePost) run(t *testing.T) {
	t.Helper()
	select {
	case fn := <-p.ch:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was posted")
	}
}

type testAdapter struct {
	closed chan struct{}
	once   sync.Once
}

func newTestAdapter() *testAdapter                { return &testAdapter{closed: make(chan struct{})} }
func (a *testAdapter) Ping(context.Context) error { return nil }
func (a *testAdapter) Close() error               { a.once.Do(func() { close(a.closed) }); return nil }
func (a *testAdapter) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-a.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("adapter not closed")
	}
}

type connCall struct {
	profile  connection.Profile
	password string
	reply    chan connResult
}
type connResult struct {
	ad  database.Adapter
	err error
}

type testCreds struct {
	mu       sync.Mutex
	secrets  map[string]string
	sets     chan string
	deletes  chan string
	getCalls int
}

func (c *testCreds) Get(ref string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	if v, ok := c.secrets[ref]; ok {
		return v, nil
	}
	return "", connection.ErrSecretNotFound
}
func (c *testCreds) Set(ref, pw string) error {
	c.mu.Lock()
	c.secrets[ref] = pw
	c.mu.Unlock()
	c.sets <- ref
	return nil
}
func (c *testCreds) Delete(ref string) error {
	c.mu.Lock()
	delete(c.secrets, ref)
	c.mu.Unlock()
	c.deletes <- ref
	return nil
}

type env struct {
	t      *testing.T
	s      *State
	post   *queuePost
	calls  chan *connCall
	creds  *testCreds
	saves  []connection.Catalog
	issues []string

	// ignoreCtx makes the connector a driver that finishes whatever the
	// context says.
	ignoreCtx bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		t:     t,
		s:     &State{Settings: storage.DefaultSettings()},
		post:  &queuePost{ch: make(chan func(), 32)},
		calls: make(chan *connCall, 8),
		creds: &testCreds{secrets: map[string]string{}, sets: make(chan string, 8), deletes: make(chan string, 8)},
	}
	connect := func(ctx context.Context, p connection.Profile, pw string) (database.Adapter, error) {
		c := &connCall{profile: p, password: pw, reply: make(chan connResult, 1)}
		e.calls <- c
		if e.ignoreCtx {
			r := <-c.reply
			return r.ad, r.err
		}
		select {
		case r := <-c.reply:
			return r.ad, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	e.s.Conns = connection.NewManager(&e.s.Catalog, e.creds, connect, e.post.post)
	e.s.ScheduleCatalogSave = func(c connection.Catalog) { e.saves = append(e.saves, c) }
	return e
}

func (e *env) profile(name string, password bool) connection.Profile {
	e.t.Helper()
	p := connection.Profile{Name: name, Driver: connection.DriverPostgres, Host: "h", Port: 5432, Database: "d", Username: "u"}
	change := connection.PasswordChange{}
	if password {
		change = connection.PasswordChange{Mode: connection.SetPassword, Value: "pw-" + name}
	}
	if _, err := e.s.SaveProfile(p, change); err != nil {
		e.t.Fatal(err)
	}
	got := e.s.Catalog.Profiles[len(e.s.Catalog.Profiles)-1]
	if password {
		<-e.creds.sets // the background write
		e.post.run(e.t)
	}
	return got
}

// connected connects the profile with a fresh adapter and returns it.
func (e *env) connected(id string) *testAdapter {
	e.t.Helper()
	ad := newTestAdapter()
	e.s.Connect(id)
	select {
	case c := <-e.calls:
		c.reply <- connResult{ad: ad}
	case <-time.After(2 * time.Second):
		e.t.Fatal("connector not called")
	}
	e.post.run(e.t)
	if e.s.Conns.Status(id) != connection.Connected {
		e.t.Fatalf("status %v", e.s.Conns.Status(id))
	}
	return ad
}

func (e *env) lastSave() connection.Catalog { return e.saves[len(e.saves)-1] }

// ---- tests ----

func TestZeroStateIsSafe(t *testing.T) {
	s := &State{}
	s.Connect("x")
	s.CancelConnect("x")
	s.SubmitPassword("x", "y")
	s.CancelPasswordPrompt("x")
	s.TestSaved("x")
	if !s.RequestDisconnect("x") {
		t.Error("disconnect of nothing should be immediate")
	}
	s.RequestDelete("x")
	s.ConfirmConn()
	s.CancelConn()
	s.ConfirmDeleteFolder()
	if _, err := s.AddFolder("F"); err != nil {
		t.Error(err)
	}
	p := connection.Profile{Name: "A", Driver: connection.DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u"}
	if ok, err := s.SaveProfile(p, connection.PasswordChange{Mode: connection.SetPassword, Value: "x"}); !ok || err != nil {
		t.Errorf("SaveProfile on a zero state: %v %v", ok, err)
	}
	s.ReportCredentialIssue("", errors.New("x"))
	s.SetCatalogSaveError(errors.New("disk"))
	s.ClearCatalogSaveError()
}

func TestFolderAndMoveOperationsPersist(t *testing.T) {
	e := newEnv(t)
	f, err := e.s.AddFolder("Work")
	if err != nil || len(e.saves) != 1 || len(e.lastSave().Folders) != 1 {
		t.Fatalf("AddFolder: %v saves=%d", err, len(e.saves))
	}
	p := e.profile("A", false)
	if err := e.s.MoveConnection(p.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.savedProfile(p.ID); got.FolderID != f.ID {
		t.Errorf("saved folder %q", got.FolderID)
	}
	if err := e.s.RenameFolder(f.ID, "Job"); err != nil || e.lastSave().Folders[0].Name != "Job" {
		t.Errorf("RenameFolder: %v", err)
	}
	if err := e.s.RenameConnection(p.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.savedProfile(p.ID); got.Name != "Renamed" {
		t.Errorf("saved name %q", got.Name)
	}
	n := len(e.saves)
	if err := e.s.RenameFolder("nope", "x"); err == nil || len(e.saves) != n {
		t.Error("a failed rename was persisted")
	}
	// Snapshots handed to the writer do not follow later edits.
	first := e.saves[0]
	if len(first.Folders) != 1 || len(first.Profiles) != 0 {
		t.Errorf("the first snapshot changed: %+v", first)
	}
}

func TestReadOnlyRefusesEveryChange(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	f, _ := e.s.AddFolder("F")
	saves := len(e.saves)
	e.s.ConnectionsReadOnly = "connections.json was written by a newer version"
	before := e.s.Catalog.Snapshot()

	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrConnectionsReadOnly) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	_, err := e.s.AddFolder("G")
	check("AddFolder", err)
	check("RenameFolder", e.s.RenameFolder(f.ID, "x"))
	check("MoveConnection", e.s.MoveConnection(p.ID, f.ID))
	check("RenameConnection", e.s.RenameConnection(p.ID, "x"))
	_, err = e.s.SaveProfile(p, connection.PasswordChange{})
	check("SaveProfile", err)
	e.s.RequestDelete(p.ID)
	if e.s.PendingConn != nil {
		t.Error("RequestDelete queued a delete while read-only")
	}
	if e.s.RequestDeleteFolder(f.ID) || e.s.PendingFolderDelete != "" {
		t.Error("RequestDeleteFolder worked while read-only")
	}
	if !strings.Contains(e.s.Notice, "read-only") || !strings.Contains(e.s.Notice, "newer version") {
		t.Errorf("notice %q", e.s.Notice)
	}
	if len(e.saves) != saves || e.s.Catalog.Rev != before.Rev {
		t.Error("the catalog changed or was persisted while read-only")
	}
	// Connecting is still allowed: it changes no file.
	e.s.Connect(p.ID)
	c := <-e.calls
	c.reply <- connResult{ad: newTestAdapter()}
	e.post.run(t)
	if e.s.Conns.Status(p.ID) != connection.Connected {
		t.Error("connecting was blocked by read-only mode")
	}
}

func TestDeleteFolderBranches(t *testing.T) {
	e := newEnv(t)
	empty, _ := e.s.AddFolder("Empty")
	full, _ := e.s.AddFolder("Full")
	p := e.profile("A", false)
	_ = e.s.MoveConnection(p.ID, full.ID)

	if !e.s.RequestDeleteFolder(empty.ID) {
		t.Error("an empty folder should be deleted at once")
	}
	if _, ok := e.s.Catalog.Folder(empty.ID); ok {
		t.Error("empty folder still there")
	}
	if e.s.RequestDeleteFolder(full.ID) || e.s.PendingFolderDelete != full.ID {
		t.Fatalf("a folder with connections must ask: pending %q", e.s.PendingFolderDelete)
	}
	saves := len(e.saves)
	e.s.CancelDeleteFolder()
	if _, ok := e.s.Catalog.Folder(full.ID); !ok || e.s.PendingFolderDelete != "" || len(e.saves) != saves {
		t.Error("Cancel changed something")
	}
	e.s.RequestDeleteFolder(full.ID)
	e.s.ConfirmDeleteFolder()
	if _, ok := e.s.Catalog.Folder(full.ID); ok {
		t.Error("the folder was not deleted")
	}
	if got, ok := e.s.Catalog.Profile(p.ID); !ok || got.FolderID != "" {
		t.Errorf("the connection was not moved to the root: %+v", got)
	}
	if len(e.lastSave().Folders) != 0 || e.lastSave().Profiles[0].FolderID != "" {
		t.Error("the deletion was not persisted")
	}
	e.s.ConfirmDeleteFolder() // nothing pending: harmless
}

func TestCreateProfile(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", true)
	if p.ID == "" || p.SecretRef != connection.SecretRefFor(p.ID) || p.TLS.Mode != connection.TLSRequire {
		t.Errorf("created %+v", p)
	}
	if e.creds.secrets[p.SecretRef] != "pw-A" {
		t.Errorf("secrets %v", e.creds.secrets)
	}
	// The password is nowhere in what is persisted.
	for _, c := range e.saves {
		for _, q := range c.Profiles {
			if strings.Contains(q.SecretRef+q.Name+q.Host, "pw-A") {
				t.Error("the password reached the catalog")
			}
		}
	}
	nopw := e.profile("B", false)
	if nopw.SecretRef != "" {
		t.Errorf("a profile without password has SecretRef %q", nopw.SecretRef)
	}
}

func TestCreateProfileValidation(t *testing.T) {
	e := newEnv(t)
	bad := connection.Profile{Name: "", Driver: connection.DriverPostgres, Host: "a,b", Port: 0}
	ok, err := e.s.SaveProfile(bad, connection.PasswordChange{Mode: connection.SetPassword, Value: strings.Repeat("x", 3000)})
	var ve *connection.ValidationError
	if ok || !errors.As(err, &ve) {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	fields := map[string]bool{}
	for _, f := range ve.Fields {
		fields[f.Field] = true
	}
	for _, want := range []string{connection.FieldName, connection.FieldHost, connection.FieldPort, connection.FieldDatabase, connection.FieldUsername, connection.FieldPassword} {
		if !fields[want] {
			t.Errorf("no error on %q: %v", want, ve.Fields)
		}
	}
	if len(e.s.Catalog.Profiles) != 0 || len(e.saves) != 0 {
		t.Error("an invalid profile was saved")
	}
	select {
	case ref := <-e.creds.sets:
		t.Errorf("the credential store was written for an invalid profile (%s)", ref)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestEditKeepSetRemovePassword(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", true)

	// Keep: the form left the password empty.
	edit := p
	edit.SecretRef = "" // the form does not carry the reference
	edit.Host = "other"
	ok, err := e.s.SaveProfile(edit, connection.PasswordChange{})
	if !ok || err != nil {
		t.Fatalf("keep: %v %v", ok, err)
	}
	got, _ := e.s.Catalog.Profile(p.ID)
	if got.Host != "other" || got.SecretRef != p.SecretRef {
		t.Errorf("after keep: %+v", got)
	}
	if e.creds.secrets[p.SecretRef] != "pw-A" {
		t.Error("keeping the password changed the secret")
	}

	// Set: a new password replaces it.
	ok, err = e.s.SaveProfile(got, connection.PasswordChange{Mode: connection.SetPassword, Value: "new"})
	if !ok || err != nil {
		t.Fatal(err)
	}
	<-e.creds.sets
	e.post.run(t)
	if e.creds.secrets[p.SecretRef] != "new" {
		t.Errorf("secrets %v", e.creds.secrets)
	}

	// Remove: the secret is deleted and the profile no longer uses one.
	got, _ = e.s.Catalog.Profile(p.ID)
	ok, err = e.s.SaveProfile(got, connection.PasswordChange{Mode: connection.RemovePassword})
	if !ok || err != nil {
		t.Fatal(err)
	}
	if ref := <-e.creds.deletes; ref != p.SecretRef {
		t.Errorf("deleted %q, want %q", ref, p.SecretRef)
	}
	if got, _ = e.s.Catalog.Profile(p.ID); got.SecretRef != "" {
		t.Errorf("SecretRef %q after removing the password", got.SecretRef)
	}
}

func TestEditUnsupportedDriverIsBlocked(t *testing.T) {
	e := newEnv(t)
	e.s.Catalog.Profiles = append(e.s.Catalog.Profiles, connection.Profile{ID: "old", Name: "Old", Driver: connection.DriverSQLServer, Host: "h", Port: 1433, Database: "d", Username: "u"})
	edit := e.s.Catalog.Profiles[0]
	edit.Driver = connection.DriverPostgres // what the form would send
	_, err := e.s.SaveProfile(edit, connection.PasswordChange{})
	if err == nil || e.s.Catalog.Profiles[0].Driver != connection.DriverSQLServer {
		t.Errorf("the driver of an unsupported profile was rewritten: %v", err)
	}
	// Rename and delete still work.
	if err := e.s.RenameConnection("old", "Legacy"); err != nil {
		t.Error(err)
	}
	e.s.RequestDelete("old")
	if e.s.PendingConn == nil {
		t.Error("delete not offered")
	}
}

func TestEditingAConnectedProfileAsksFirst(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	ad := e.connected(p.ID)
	tab := e.s.Workspace.NewSQL(p.ID)
	other := e.s.Workspace.NewSQL("other")
	saves := len(e.saves)

	edit := p
	edit.Host = "new-host"
	applied, err := e.s.SaveProfile(edit, connection.PasswordChange{})
	if applied || err != nil {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	pc := e.s.PendingConn
	if pc == nil || pc.Kind != ConnApplyEdit || pc.Tabs != 1 || pc.Name != "A" {
		t.Fatalf("pending %+v", pc)
	}
	if got, _ := e.s.Catalog.Profile(p.ID); got.Host != "h" || len(e.saves) != saves {
		t.Error("the edit was applied before the user answered")
	}
	if e.s.Workspace.Find(tab.ID) == nil || e.s.Conns.Status(p.ID) != connection.Connected {
		t.Error("the session or tab ended before the user answered")
	}

	e.s.CancelConn()
	if e.s.PendingConn != nil || e.s.Conns.Status(p.ID) != connection.Connected {
		t.Error("Cancel ended the session")
	}

	e.s.SaveProfile(edit, connection.PasswordChange{})
	e.s.ConfirmConn()
	ad.waitClosed(t)
	if e.s.Conns.Status(p.ID) != connection.Disconnected {
		t.Errorf("status %v after applying the edit", e.s.Conns.Status(p.ID))
	}
	if got, _ := e.s.Catalog.Profile(p.ID); got.Host != "new-host" {
		t.Errorf("host %q", got.Host)
	}
	if e.s.Workspace.Find(tab.ID) != nil {
		t.Error("the tab of the connection stayed open")
	}
	if e.s.Workspace.Find(other.ID) == nil {
		t.Error("a tab of another connection was closed")
	}
	if len(e.saves) != saves+1 || e.lastSave().Profiles[0].Host != "new-host" {
		t.Error("the edit was not persisted")
	}
}

func TestRenameOrMoveWhileConnectedKeepsTheSession(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	e.connected(p.ID)
	tab := e.s.Workspace.NewSQL(p.ID)
	f, _ := e.s.AddFolder("F")

	edit := p
	edit.Name = "Renamed"
	edit.FolderID = f.ID
	applied, err := e.s.SaveProfile(edit, connection.PasswordChange{})
	if !applied || err != nil || e.s.PendingConn != nil {
		t.Fatalf("applied=%v err=%v pending=%v", applied, err, e.s.PendingConn)
	}
	if e.s.Conns.Status(p.ID) != connection.Connected || e.s.Workspace.Find(tab.ID) == nil {
		t.Error("a rename ended the session or closed a tab")
	}
	if got, _ := e.s.Catalog.Profile(p.ID); got.Name != "Renamed" || got.FolderID != f.ID {
		t.Errorf("saved %+v", got)
	}
}

func TestEditingAConnectedProfileWithoutTabsStillAsks(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	e.connected(p.ID)
	edit := p
	edit.Database = "other"
	if applied, _ := e.s.SaveProfile(edit, connection.PasswordChange{}); applied || e.s.PendingConn == nil || e.s.PendingConn.Tabs != 0 {
		t.Errorf("applied=%v pending=%+v", applied, e.s.PendingConn)
	}
}

func TestEditingAFailedProfileResetsItsState(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	e.s.Connect(p.ID)
	c := <-e.calls
	c.reply <- connResult{err: errors.New("boom")}
	e.post.run(t)
	if e.s.Conns.Status(p.ID) != connection.Failed {
		t.Fatalf("status %v", e.s.Conns.Status(p.ID))
	}
	edit := p
	edit.Host = "fixed"
	if applied, err := e.s.SaveProfile(edit, connection.PasswordChange{}); !applied || err != nil {
		t.Fatal(err)
	}
	if e.s.Conns.Status(p.ID) != connection.Disconnected {
		t.Errorf("status %v: the old failure is still shown", e.s.Conns.Status(p.ID))
	}
}

func TestDisconnectAsksOnlyWhenTabsAreOpen(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	ad := e.connected(p.ID)
	if !e.s.RequestDisconnect(p.ID) || e.s.PendingConn != nil {
		t.Fatal("a disconnect without tabs should be immediate")
	}
	ad.waitClosed(t)

	ad = e.connected(p.ID)
	tab := e.s.Workspace.NewSQL(p.ID)
	keep := e.s.Workspace.NewSQL("other")
	if e.s.RequestDisconnect(p.ID) || e.s.PendingConn == nil || e.s.PendingConn.Kind != ConnDisconnect || e.s.PendingConn.Tabs != 1 {
		t.Fatalf("pending %+v", e.s.PendingConn)
	}
	if e.s.Conns.Status(p.ID) != connection.Connected {
		t.Error("disconnected before the user answered")
	}
	e.s.ConfirmConn()
	ad.waitClosed(t)
	if e.s.Conns.Status(p.ID) != connection.Disconnected || e.s.Workspace.Find(tab.ID) != nil || e.s.Workspace.Find(keep.ID) == nil {
		t.Error("confirm did not close the tab and the session, or closed too much")
	}
	if got, ok := e.s.Catalog.Profile(p.ID); !ok || got.Name != "A" {
		t.Error("disconnecting removed the profile")
	}
}

func TestClosingTabsDropsAPendingCloseOfThoseTabs(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	tab := e.s.Workspace.NewSQL(p.ID)
	e.s.PendingClose = tab.ID
	e.s.RequestDisconnect(p.ID)
	e.s.ConfirmConn()
	if e.s.PendingClose != 0 {
		t.Errorf("PendingClose = %d for a closed tab", e.s.PendingClose)
	}
}

func TestDeleteAlwaysAsksAndCleansUp(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", true)
	ad := e.connected(p.ID)
	tab := e.s.Workspace.NewSQL(p.ID)
	e.s.RequestDelete(p.ID)
	pc := e.s.PendingConn
	if pc == nil || pc.Kind != ConnDelete || pc.Tabs != 1 {
		t.Fatalf("pending %+v", pc)
	}
	if _, ok := e.s.Catalog.Profile(p.ID); !ok {
		t.Fatal("deleted before the user answered")
	}
	e.s.CancelConn()
	if _, ok := e.s.Catalog.Profile(p.ID); !ok {
		t.Fatal("Cancel deleted the profile")
	}

	e.s.RequestDelete(p.ID)
	e.s.ConfirmConn()
	ad.waitClosed(t)
	if _, ok := e.s.Catalog.Profile(p.ID); ok {
		t.Error("profile still in the catalog")
	}
	if e.s.Workspace.Find(tab.ID) != nil {
		t.Error("its tab is still open")
	}
	if ref := <-e.creds.deletes; ref != p.SecretRef {
		t.Errorf("deleted secret %q, want %q", ref, p.SecretRef)
	}
	if len(e.lastSave().Profiles) != 0 {
		t.Error("the deletion was not persisted")
	}
	if e.s.Conns.Status(p.ID) != connection.Disconnected || e.s.Conns.HasSessionPassword(p.ID) {
		t.Error("the manager still knows the deleted profile")
	}
}

func TestDeleteWhileConnectingDropsALateSession(t *testing.T) {
	e := newEnv(t)
	e.ignoreCtx = true
	p := e.profile("A", false)
	e.s.Connect(p.ID)
	c := <-e.calls
	e.s.RequestDelete(p.ID)
	e.s.ConfirmConn()
	ad := newTestAdapter()
	c.reply <- connResult{ad: ad}
	ad.waitClosed(t)
	if len(e.s.Conns.Active()) != 0 {
		t.Error("a deleted profile became active")
	}
}

func TestConfirmWithNothingPendingOrAVanishedProfile(t *testing.T) {
	e := newEnv(t)
	e.s.ConfirmConn()
	e.s.PendingConn = &PendingConn{Kind: ConnDelete, ConnectionID: "gone"}
	e.s.ConfirmConn()
	e.s.PendingConn = &PendingConn{Kind: ConnApplyEdit, ConnectionID: "gone"}
	e.s.ConfirmConn()
	if len(e.saves) != 0 {
		t.Error("something was persisted")
	}
}

func TestCatalogSaveErrorNotice(t *testing.T) {
	s := &State{}
	s.Notice = "other message"
	s.SetCatalogSaveError(errors.New("disk full"))
	if !strings.Contains(s.Notice, "disk full") {
		t.Errorf("notice %q", s.Notice)
	}
	s.ClearCatalogSaveError()
	if s.Notice != "" {
		t.Errorf("notice %q after a successful write", s.Notice)
	}
	s.SetCatalogSaveError(errors.New("x"))
	s.Notice = "something newer"
	s.ClearCatalogSaveError()
	if s.Notice != "something newer" {
		t.Errorf("a newer notice was cleared: %q", s.Notice)
	}
}

func TestReportCredentialIssue(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	e.s.ReportCredentialIssue(p.ID, errors.New("locked"))
	if !strings.Contains(e.s.Notice, `"A"`) || !strings.Contains(e.s.Notice, "this session only") {
		t.Errorf("notice %q", e.s.Notice)
	}
	e.s.ReportCredentialIssue("", errors.New("locked"))
	if !strings.Contains(e.s.Notice, "could not be removed") {
		t.Errorf("notice %q", e.s.Notice)
	}
}

func TestTestSavedReportsWithoutThePassword(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", true)
	e.s.TestSaved(p.ID)
	if !strings.Contains(e.s.TestNotice, "Testing") {
		t.Errorf("notice %q", e.s.TestNotice)
	}
	c := <-e.calls
	if c.password != "pw-A" {
		t.Errorf("the test used %q", c.password)
	}
	c.reply <- connResult{err: errors.New("login with pw-A refused")}
	e.post.run(t)
	if !strings.Contains(e.s.TestNotice, "failed") || strings.Contains(e.s.TestNotice, "pw-A") {
		t.Errorf("notice %q", e.s.TestNotice)
	}

	e.s.TestSaved(p.ID)
	c = <-e.calls
	c.reply <- connResult{ad: newTestAdapter()}
	e.post.run(t)
	if !strings.Contains(e.s.TestNotice, "succeeded") {
		t.Errorf("notice %q", e.s.TestNotice)
	}
	if len(e.s.Conns.Active()) != 0 {
		t.Error("a test connected")
	}
	// A new action clears the message.
	e.s.Connect(p.ID)
	if e.s.TestNotice != "" {
		t.Errorf("notice %q after Connect", e.s.TestNotice)
	}
	c = <-e.calls
	c.reply <- connResult{err: errors.New("x")}
	e.post.run(t)
}

func TestPasswordPromptFlowThroughState(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", true)
	delete(e.creds.secrets, p.SecretRef) // the stored password is gone
	e.s.Connect(p.ID)
	e.post.run(t)
	if e.s.Conns.Status(p.ID) != connection.NeedsPassword {
		t.Fatalf("status %v", e.s.Conns.Status(p.ID))
	}
	e.s.CancelPasswordPrompt(p.ID)
	if e.s.Conns.Status(p.ID) != connection.Disconnected {
		t.Error("cancel did not dismiss the prompt")
	}
	e.s.Connect(p.ID)
	e.post.run(t)
	e.s.SubmitPassword(p.ID, "typed")
	c := <-e.calls
	if c.password != "typed" {
		t.Errorf("connected with %q", c.password)
	}
	c.reply <- connResult{ad: newTestAdapter()}
	e.post.run(t)
	if e.s.Conns.Status(p.ID) != connection.Connected {
		t.Error("not connected after the password")
	}
}

func TestStartupMakesNoCredentialCalls(t *testing.T) {
	e := newEnv(t)
	_, _ = e.s.Catalog.AddProfile(connection.Profile{
		Name: "A", Driver: connection.DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u", SecretRef: "dbl/x",
	})
	_ = e.s.Conns.Status("x")
	_ = e.s.Conns.Active()
	if e.creds.getCalls != 0 || len(e.creds.sets)+len(e.creds.deletes) != 0 || len(e.calls) != 0 {
		t.Error("loading the catalog touched the credential store or connected")
	}
}

func (e *env) savedProfile(id string) (connection.Profile, bool) {
	c := e.lastSave()
	return c.Profile(id)
}

// Saving a form for a connection that was deleted meanwhile must not bring
// it back.
func TestEditOfADeletedProfileIsRefused(t *testing.T) {
	e := newEnv(t)
	p := e.profile("A", false)
	e.s.RequestDelete(p.ID)
	e.s.ConfirmConn()
	saves := len(e.saves)
	edit := p
	edit.Host = "other"
	applied, err := e.s.SaveProfile(edit, connection.PasswordChange{})
	if applied || !errors.Is(err, connection.ErrNotFound) {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if len(e.s.Catalog.Profiles) != 0 || len(e.saves) != saves {
		t.Error("the deleted connection was recreated")
	}
	// A password typed for it is not stored either.
	_, _ = e.s.SaveProfile(edit, connection.PasswordChange{Mode: connection.SetPassword, Value: "x"})
	select {
	case ref := <-e.creds.sets:
		t.Errorf("a password was stored for a deleted connection (%s)", ref)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestEditingSQLitePathEndsSession(t *testing.T) {
	base := connection.Profile{Name: "n", Driver: connection.DriverSQLite, SQLitePath: "a.db"}
	if affectsSession(base, base, connection.PasswordChange{}) {
		t.Error("an unchanged profile ends the session")
	}
	moved := base
	moved.SQLitePath = "b.db"
	if !affectsSession(base, moved, connection.PasswordChange{}) {
		t.Error("a new path keeps the session")
	}
	ro := base
	ro.ReadOnly = true
	if !affectsSession(base, ro, connection.PasswordChange{}) {
		t.Error("read-only keeps the session")
	}
	renamed := base
	renamed.Name = "other"
	if affectsSession(base, renamed, connection.PasswordChange{}) {
		t.Error("a rename ends the session")
	}
}
