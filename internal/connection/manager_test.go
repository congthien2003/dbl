package connection

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dbl/internal/database"
)

// fakePost stands in for Window.Update: functions queue up and the test runs
// them on its own goroutine, the "UI thread".
type fakePost struct{ ch chan func() }

func newFakePost() *fakePost { return &fakePost{ch: make(chan func(), 64)} }

func (p *fakePost) post(fn func()) { p.ch <- fn }

// run runs the next posted function.
func (p *fakePost) run(t *testing.T) {
	t.Helper()
	select {
	case fn := <-p.ch:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was posted to the UI thread")
	}
}

func (p *fakePost) expectNone(t *testing.T) {
	t.Helper()
	select {
	case <-p.ch:
		t.Fatal("a result was posted although it should have been dropped")
	case <-time.After(80 * time.Millisecond):
	}
}

type fakeAdapter struct {
	closed chan struct{}
	n      atomic.Int32
}

func newAdapter() *fakeAdapter { return &fakeAdapter{closed: make(chan struct{}, 8)} }

func (a *fakeAdapter) Ping(context.Context) error { return nil }
func (a *fakeAdapter) Close() error {
	a.n.Add(1)
	a.closed <- struct{}{}
	return nil
}

// waitClosed waits until the adapter was closed and returns how many times.
func (a *fakeAdapter) waitClosed(t *testing.T) int {
	t.Helper()
	select {
	case <-a.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the adapter was not closed")
	}
	time.Sleep(20 * time.Millisecond)
	return int(a.n.Load())
}

func (a *fakeAdapter) neverClosed(t *testing.T) {
	t.Helper()
	select {
	case <-a.closed:
		t.Fatal("the adapter was closed")
	case <-time.After(60 * time.Millisecond):
	}
}

type connectCall struct {
	profile  Profile
	password string
	ctx      context.Context
	reply    chan connectReply
}

type connectReply struct {
	ad  database.Adapter
	err error
}

// fakeConnector hands every call to the test, which decides the outcome.
type fakeConnector struct {
	calls     chan *connectCall
	ignoreCtx bool
}

func newConnector() *fakeConnector { return &fakeConnector{calls: make(chan *connectCall, 16)} }

func (f *fakeConnector) connect(ctx context.Context, p Profile, pw string) (database.Adapter, error) {
	c := &connectCall{profile: p, password: pw, ctx: ctx, reply: make(chan connectReply, 1)}
	f.calls <- c
	if f.ignoreCtx {
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

func (f *fakeConnector) next(t *testing.T) *connectCall {
	t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("the connector was not called")
		return nil
	}
}

func (f *fakeConnector) expectNoCall(t *testing.T) {
	t.Helper()
	select {
	case <-f.calls:
		t.Fatal("the connector was called")
	case <-time.After(60 * time.Millisecond):
	}
}

type fakeCreds struct {
	mu                  sync.Mutex
	secrets             map[string]string
	getErr, setErr      error
	gets, sets, deletes int
	setDone, deleteDone chan struct{}
	lastSet, lastDelete string
}

func newCreds() *fakeCreds {
	return &fakeCreds{secrets: map[string]string{}, setDone: make(chan struct{}, 8), deleteDone: make(chan struct{}, 8)}
}

func (c *fakeCreds) Get(ref string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	if c.getErr != nil {
		return "", c.getErr
	}
	pw, ok := c.secrets[ref]
	if !ok {
		return "", ErrSecretNotFound
	}
	return pw, nil
}

func (c *fakeCreds) Set(ref, pw string) error {
	c.mu.Lock()
	c.sets++
	c.lastSet = ref
	err := c.setErr
	if err == nil {
		c.secrets[ref] = pw
	}
	c.mu.Unlock()
	c.setDone <- struct{}{}
	return err
}

func (c *fakeCreds) Delete(ref string) error {
	c.mu.Lock()
	c.deletes++
	c.lastDelete = ref
	delete(c.secrets, ref)
	c.mu.Unlock()
	c.deleteDone <- struct{}{}
	return nil
}

func (c *fakeCreds) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets + c.sets + c.deletes
}

type rig struct {
	t     *testing.T
	m     *Manager
	cat   *Catalog
	post  *fakePost
	conn  *fakeConnector
	creds *fakeCreds
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, cat: &Catalog{}, post: newFakePost(), conn: newConnector(), creds: newCreds()}
	r.m = NewManager(r.cat, r.creds, r.conn.connect, r.post.post)
	return r
}

// add adds a valid profile; withPassword gives it a SecretRef.
func (r *rig) add(name string, withPassword bool) Profile {
	r.t.Helper()
	p := Profile{Name: name, Driver: DriverPostgres, Host: "localhost", Port: 5432, Database: "app", Username: "u"}
	p.ID = NewID()
	if withPassword {
		p.SecretRef = SecretRefFor(p.ID)
	}
	p, err := r.cat.AddProfile(p)
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func TestCreatingAManagerMakesNoCalls(t *testing.T) {
	r := newRig(t)
	r.add("A", true)
	_ = r.m.Status("x")
	_ = r.m.Active()
	r.conn.expectNoCall(t)
	if n := r.creds.calls(); n != 0 {
		t.Errorf("%d credential calls before anything was asked", n)
	}
}

func TestConnectSucceeds(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	ad := newAdapter()
	r.m.Connect(p.ID)
	if r.m.Status(p.ID) != Connecting {
		t.Fatalf("status %v right after Connect", r.m.Status(p.ID))
	}
	c := r.conn.next(t)
	if c.profile.ID != p.ID || c.password != "" {
		t.Errorf("connector got %+v / %q", c.profile.ID, c.password)
	}
	if dl, ok := c.ctx.Deadline(); !ok || time.Until(dl) > 11*time.Second || time.Until(dl) < 5*time.Second {
		t.Errorf("deadline %v not about the profile timeout", time.Until(dl))
	}
	c.reply <- connectReply{ad: ad}
	r.post.run(t)
	if r.m.Status(p.ID) != Connected {
		t.Fatalf("status %v, want Connected", r.m.Status(p.ID))
	}
	if got := r.m.Active(); len(got) != 1 || got[0] != p.ID {
		t.Errorf("Active = %v", got)
	}
	ad.neverClosed(t)
	r.m.Disconnect(p.ID)
	if r.m.Status(p.ID) != Disconnected || ad.waitClosed(t) != 1 {
		t.Errorf("after Disconnect: %v", r.m.Status(p.ID))
	}
}

func TestConnectTwiceIsOneAttempt(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	r.m.Connect(p.ID)
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	r.conn.expectNoCall(t)
	ad := newAdapter()
	c.reply <- connectReply{ad: ad}
	r.post.run(t)
	r.m.Connect(p.ID) // already connected
	r.conn.expectNoCall(t)
}

func TestConnectFailureShowsARedactedError(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.creds.secrets[p.SecretRef] = "hunter2"
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	if c.password != "hunter2" {
		t.Fatalf("the saved password was not used: %q", c.password)
	}
	c.reply <- connectReply{err: errors.New("auth failed for password hunter2 (wrong hunter2)")}
	r.post.run(t)
	if r.m.Status(p.ID) != Failed {
		t.Fatalf("status %v", r.m.Status(p.ID))
	}
	err := r.m.Err(p.ID)
	if err == nil || strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Message, "hunter2") {
		t.Fatalf("error %v leaks the password", err)
	}
	if err.Kind != database.KindConnection {
		t.Errorf("kind %v", err.Kind)
	}
	// A failed connection can be tried again.
	r.m.Connect(p.ID)
	c = r.conn.next(t)
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
	if r.m.Status(p.ID) != Connected || r.m.Err(p.ID) != nil {
		t.Errorf("retry: %v %v", r.m.Status(p.ID), r.m.Err(p.ID))
	}
}

func TestTypedErrorsKeepTheirKindAndGetRedacted(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.m.Connect(p.ID) // no secret saved: a prompt first
	r.post.run(t)
	r.m.SubmitPassword(p.ID, "s3cret")
	c := r.conn.next(t)
	c.reply <- connectReply{err: &database.Error{Kind: database.KindAuthentication, Message: "bad s3cret", Hint: "try s3cret"}}
	r.post.run(t)
	err := r.m.Err(p.ID)
	if err == nil || err.Kind != database.KindAuthentication || strings.Contains(err.Message+err.Hint, "s3cret") {
		t.Errorf("error %+v", err)
	}
}

func TestDisconnectWhileConnectingDropsALateSuccess(t *testing.T) {
	r := newRig(t)
	r.conn.ignoreCtx = true // a driver that finishes anyway
	p := r.add("A", false)
	ad := newAdapter()
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	r.m.Disconnect(p.ID)
	if r.m.Status(p.ID) != Disconnected {
		t.Fatalf("status %v after Disconnect", r.m.Status(p.ID))
	}
	c.reply <- connectReply{ad: ad}
	if ad.waitClosed(t) != 1 {
		t.Error("the late session was not closed exactly once")
	}
	r.post.expectNone(t)
	if r.m.Status(p.ID) != Disconnected {
		t.Errorf("the late success changed the state to %v", r.m.Status(p.ID))
	}
}

// The result was already posted when its target went away: the gate in the
// posted function must drop it (PR5).
func TestPostedResultAfterDisconnectIsDropped(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	ad := newAdapter()
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	c.reply <- connectReply{ad: ad}
	time.Sleep(50 * time.Millisecond) // the worker has posted by now
	r.m.Disconnect(p.ID)
	r.post.run(t)
	if r.m.Status(p.ID) != Disconnected {
		t.Errorf("status %v, want Disconnected", r.m.Status(p.ID))
	}
	if ad.waitClosed(t) != 1 {
		t.Error("the dropped session was not closed")
	}
}

func TestPostedResultAfterDeleteIsDropped(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	ad := newAdapter()
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	c.reply <- connectReply{ad: ad}
	time.Sleep(50 * time.Millisecond)
	r.m.Forget(p.ID)
	if _, err := r.cat.DeleteProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	r.post.run(t)
	if r.m.Status(p.ID) != Disconnected || len(r.m.Active()) != 0 {
		t.Errorf("a deleted profile came back: %v %v", r.m.Status(p.ID), r.m.Active())
	}
	if ad.waitClosed(t) != 1 {
		t.Error("the dropped session was not closed")
	}
}

func TestOldAttemptCannotOverwriteANewerOne(t *testing.T) {
	r := newRig(t)
	r.conn.ignoreCtx = true
	p := r.add("A", false)
	first, second := newAdapter(), newAdapter()
	r.m.Connect(p.ID)
	c1 := r.conn.next(t)
	r.m.Disconnect(p.ID)
	r.m.Connect(p.ID)
	c2 := r.conn.next(t)
	c2.reply <- connectReply{ad: second}
	r.post.run(t)
	if r.m.Status(p.ID) != Connected {
		t.Fatalf("status %v", r.m.Status(p.ID))
	}
	c1.reply <- connectReply{ad: first} // the old attempt finishes last
	if first.waitClosed(t) != 1 {
		t.Error("the old session was not closed")
	}
	r.post.expectNone(t)
	second.neverClosed(t)
	if r.m.Status(p.ID) != Connected {
		t.Errorf("the old attempt changed the state to %v", r.m.Status(p.ID))
	}
}

func TestCancelledConnectorWithoutResultLeavesNoTrace(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	r.m.Disconnect(p.ID)
	select {
	case <-c.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not cancel the attempt's context")
	}
	r.post.expectNone(t)
}

func TestTimeoutIsAFailureNotASilence(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	c.reply <- connectReply{err: &database.Error{Kind: database.KindTimeout, Message: "timed out"}}
	r.post.run(t)
	if r.m.Status(p.ID) != Failed || r.m.Err(p.ID).Kind != database.KindTimeout {
		t.Errorf("status %v err %v", r.m.Status(p.ID), r.m.Err(p.ID))
	}
}

func TestMissingPasswordAsksThenConnects(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.m.Connect(p.ID)
	r.post.run(t)
	if r.m.Status(p.ID) != NeedsPassword || r.m.PromptReason(p.ID) == "" {
		t.Fatalf("status %v reason %q", r.m.Status(p.ID), r.m.PromptReason(p.ID))
	}
	r.conn.expectNoCall(t) // no attempt without a password
	r.m.SubmitPassword(p.ID, "typed pw")
	c := r.conn.next(t)
	if c.password != "typed pw" {
		t.Errorf("connector got %q", c.password)
	}
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
	if r.m.Status(p.ID) != Connected || !r.m.HasSessionPassword(p.ID) {
		t.Errorf("status %v session %v", r.m.Status(p.ID), r.m.HasSessionPassword(p.ID))
	}
	// Reconnecting does not ask again in this session.
	r.m.Disconnect(p.ID)
	r.m.Connect(p.ID)
	c = r.conn.next(t)
	if c.password != "typed pw" {
		t.Errorf("second connect used %q", c.password)
	}
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
}

func TestUnavailableStoreFallsBackToThePrompt(t *testing.T) {
	r := newRig(t)
	r.creds.getErr = ErrCredentialsUnavailable
	p := r.add("A", true)
	r.m.Connect(p.ID)
	r.post.run(t)
	if r.m.Status(p.ID) != NeedsPassword || !strings.Contains(r.m.PromptReason(p.ID), "could not be read") {
		t.Errorf("status %v reason %q", r.m.Status(p.ID), r.m.PromptReason(p.ID))
	}
}

func TestPasswordPromptCancelAndDeletedProfile(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.m.Connect(p.ID)
	r.post.run(t)
	r.m.CancelPrompt(p.ID)
	if r.m.Status(p.ID) != Disconnected {
		t.Errorf("status %v after cancelling the prompt", r.m.Status(p.ID))
	}
	// A prompt for a profile that is deleted meanwhile does nothing.
	r.m.Connect(p.ID)
	r.post.run(t)
	r.m.Forget(p.ID)
	r.cat.DeleteProfile(p.ID)
	r.m.SubmitPassword(p.ID, "x")
	r.conn.expectNoCall(t)
	if r.m.Status(p.ID) != Disconnected {
		t.Errorf("status %v", r.m.Status(p.ID))
	}
}

func TestPasswordlessProfileNeverTouchesTheStore(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
	if n := r.creds.calls(); n != 0 {
		t.Errorf("%d credential calls for a profile without a password", n)
	}
}

func TestStorePasswordSuccessClearsTheSessionCopy(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.m.StorePassword(p.ID, "pw1")
	if !r.m.HasSessionPassword(p.ID) {
		t.Error("no interim session copy")
	}
	<-r.creds.setDone
	r.post.run(t)
	if r.m.HasSessionPassword(p.ID) {
		t.Error("the session copy stayed although the store has the password")
	}
	if r.creds.secrets[SecretRefFor(p.ID)] != "pw1" || r.creds.lastSet != SecretRefFor(p.ID) {
		t.Errorf("stored %v under %q", r.creds.secrets, r.creds.lastSet)
	}
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	if c.password != "pw1" {
		t.Errorf("connect used %q", c.password)
	}
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
}

func TestStorePasswordFailureKeepsItForTheSession(t *testing.T) {
	r := newRig(t)
	r.creds.setErr = ErrCredentialsUnavailable
	p := r.add("A", true)
	var got []error
	r.m.OnCredentialIssue = func(id string, err error) { got = append(got, err) }
	r.m.StorePassword(p.ID, "pw1")
	<-r.creds.setDone
	r.post.run(t)
	if len(got) != 1 || !errors.Is(got[0], ErrCredentialsUnavailable) {
		t.Errorf("issues: %v", got)
	}
	if !r.m.HasSessionPassword(p.ID) {
		t.Error("the password was lost with the failed save")
	}
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	if c.password != "pw1" {
		t.Errorf("connect used %q", c.password)
	}
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
}

func TestDeleteStoredPassword(t *testing.T) {
	r := newRig(t)
	r.creds.secrets["dbl/x"] = "pw"
	r.m.DeleteStoredPassword("dbl/x")
	<-r.creds.deleteDone
	if _, ok := r.creds.secrets["dbl/x"]; ok || r.creds.lastDelete != "dbl/x" {
		t.Errorf("secrets %v", r.creds.secrets)
	}
	r.m.DeleteStoredPassword("") // nothing to delete
	if r.creds.deletes != 1 {
		t.Errorf("%d deletes", r.creds.deletes)
	}
}

func TestConnectionsAreIndependent(t *testing.T) {
	r := newRig(t)
	a, b := r.add("A", false), r.add("B", false)
	adA, adB := newAdapter(), newAdapter()
	r.m.Connect(a.ID)
	cA := r.conn.next(t)
	r.m.Connect(b.ID)
	cB := r.conn.next(t)
	cB.reply <- connectReply{ad: adB}
	cA.reply <- connectReply{ad: adA}
	r.post.run(t)
	r.post.run(t)
	if len(r.m.Active()) != 2 {
		t.Fatalf("Active = %v", r.m.Active())
	}
	r.m.Disconnect(a.ID)
	if r.m.Status(a.ID) != Disconnected || r.m.Status(b.ID) != Connected {
		t.Errorf("states %v %v", r.m.Status(a.ID), r.m.Status(b.ID))
	}
	adB.neverClosed(t)
	if adA.waitClosed(t) != 1 {
		t.Error("A was not closed")
	}
}

func TestUnsupportedDriverFailsWithoutConnecting(t *testing.T) {
	r := newRig(t)
	r.cat.Profiles = append(r.cat.Profiles, Profile{ID: "old", Name: "Old", Driver: DriverMySQL})
	r.m.Connect("old")
	r.conn.expectNoCall(t)
	if r.m.Status("old") != Failed || r.m.Err("old").Kind != database.KindUnsupported {
		t.Errorf("status %v err %v", r.m.Status("old"), r.m.Err("old"))
	}
}

func TestUnknownProfileIsIgnored(t *testing.T) {
	r := newRig(t)
	r.m.Connect("nope")
	r.m.Disconnect("nope")
	r.m.Forget("nope")
	r.m.CancelPrompt("nope")
	r.conn.expectNoCall(t)
	if r.m.Status("nope") != Disconnected {
		t.Error("unknown ID is not Disconnected")
	}
}

func TestCloseAllClosesSessionsAndDropsLateResults(t *testing.T) {
	r := newRig(t)
	r.conn.ignoreCtx = true
	a, b := r.add("A", false), r.add("B", false)
	adA, late := newAdapter(), newAdapter()
	r.m.Connect(a.ID)
	cA := r.conn.next(t)
	cA.reply <- connectReply{ad: adA}
	r.post.run(t)
	r.m.Connect(b.ID)
	cB := r.conn.next(t) // still connecting when the app quits
	r.m.CloseAll(2 * time.Second)
	if adA.waitClosed(t) != 1 {
		t.Error("the open session was not closed by CloseAll")
	}
	cB.reply <- connectReply{ad: late}
	if late.waitClosed(t) != 1 {
		t.Error("a session that finished after quitting was not closed")
	}
	r.post.expectNone(t)
	// After CloseAll nothing new starts, and a second call is harmless.
	r.m.Connect(a.ID)
	r.conn.expectNoCall(t)
	r.m.CloseAll(time.Second)
	if adA.n.Load() != 1 {
		t.Errorf("session closed %d times", adA.n.Load())
	}
}

func TestCloseAllWorksWhenPostIsDropped(t *testing.T) {
	// After the event loop stops, posted functions are discarded (E28).
	r := newRig(t)
	r.m.SetPost(func(func()) {})
	p := r.add("A", false)
	ad := newAdapter()
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	c.reply <- connectReply{ad: ad}
	time.Sleep(50 * time.Millisecond)
	r.m.CloseAll(time.Second) // the result is stuck in the dropped post
	if ad.waitClosed(t) != 1 {
		t.Error("a session whose result never arrived was not closed by CloseAll")
	}
	r.m.CloseAll(time.Second)
	if ad.n.Load() != 1 {
		t.Errorf("session closed %d times", ad.n.Load())
	}
}

// If the result does arrive after CloseAll took the session, the gate must
// not close it a second time.
func TestResultArrivingAfterCloseAllDoesNotCloseTwice(t *testing.T) {
	r := newRig(t)
	p := r.add("A", false)
	ad := newAdapter()
	r.m.Connect(p.ID)
	c := r.conn.next(t)
	c.reply <- connectReply{ad: ad}
	time.Sleep(50 * time.Millisecond)
	r.m.CloseAll(time.Second)
	ad.waitClosed(t)
	r.post.run(t)
	time.Sleep(30 * time.Millisecond)
	if ad.n.Load() != 1 {
		t.Errorf("session closed %d times", ad.n.Load())
	}
}

func TestNilManagerIsSafe(t *testing.T) {
	var m *Manager
	m.Connect("x")
	m.Disconnect("x")
	m.Forget("x")
	m.SubmitPassword("x", "y")
	m.CancelPrompt("x")
	m.StorePassword("x", "y")
	m.DeleteStoredPassword("x")
	m.ClearSessionPassword("x")
	m.CloseAll(time.Millisecond)
	m.SetPost(nil)
	if m.Status("x") != Disconnected || m.Err("x") != nil || m.Active() != nil || m.HasSessionPassword("x") || m.PromptReason("x") != "" {
		t.Error("nil manager reported state")
	}
	m.Test(TestRequest{}, func(TestResult) { t.Error("done called") })()
}

func TestTestConnectionClosesItsSessionAndReports(t *testing.T) {
	r := newRig(t)
	ad := newAdapter()
	var got []TestResult
	prof := Profile{Name: "T", Driver: DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u"}
	r.m.Test(TestRequest{Profile: prof, Password: PasswordChange{Mode: SetPassword, Value: "typed"}}, func(res TestResult) { got = append(got, res) })
	c := r.conn.next(t)
	if c.password != "typed" {
		t.Errorf("test used %q", c.password)
	}
	c.reply <- connectReply{ad: ad}
	r.post.run(t)
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("results %+v", got)
	}
	if ad.waitClosed(t) != 1 {
		t.Error("the test session was not closed")
	}
	if len(r.m.Active()) != 0 || r.m.Status("") != Disconnected {
		t.Error("a test registered a connection")
	}
	if len(r.m.tests) != 0 {
		t.Errorf("%d test runs left registered", len(r.m.tests))
	}
}

func TestTestConnectionErrorIsRedacted(t *testing.T) {
	r := newRig(t)
	var got []TestResult
	prof := Profile{Name: "T", Driver: DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u"}
	r.m.Test(TestRequest{Profile: prof, Password: PasswordChange{Mode: SetPassword, Value: "p@ss"}}, func(res TestResult) { got = append(got, res) })
	c := r.conn.next(t)
	c.reply <- connectReply{err: errors.New("login with p@ss refused")}
	r.post.run(t)
	if len(got) != 1 || got[0].OK || got[0].Err == nil || strings.Contains(got[0].Err.Message, "p@ss") {
		t.Errorf("results %+v", got)
	}
}

func TestCancelledTestStaysSilentAndClosesALateSession(t *testing.T) {
	r := newRig(t)
	r.conn.ignoreCtx = true
	ad := newAdapter()
	prof := Profile{Name: "T", Driver: DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u"}
	cancel := r.m.Test(TestRequest{Profile: prof}, func(TestResult) { t.Error("done called after cancel") })
	c := r.conn.next(t)
	cancel()
	c.reply <- connectReply{ad: ad}
	if ad.waitClosed(t) != 1 {
		t.Error("the late test session was not closed")
	}
	r.post.expectNone(t)
}

func TestTestResultAfterCancelInPostIsDropped(t *testing.T) {
	r := newRig(t)
	ad := newAdapter()
	prof := Profile{Name: "T", Driver: DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u"}
	cancel := r.m.Test(TestRequest{Profile: prof}, func(TestResult) { t.Error("done called after cancel") })
	c := r.conn.next(t)
	c.reply <- connectReply{ad: ad}
	time.Sleep(50 * time.Millisecond) // posted already
	cancel()
	r.post.run(t)
	if ad.waitClosed(t) != 1 {
		t.Error("session not closed")
	}
}

func TestTestOnEditFormUsesTheSavedPassword(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.creds.secrets[p.SecretRef] = "stored"
	edited := p
	edited.Host = "other"
	var got []TestResult
	r.m.Test(TestRequest{Profile: edited, ExistingID: p.ID}, func(res TestResult) { got = append(got, res) })
	c := r.conn.next(t)
	if c.password != "stored" || c.profile.Host != "other" {
		t.Errorf("test used password %q host %q", c.password, c.profile.Host)
	}
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("%+v", got)
	}
}

func TestTestWithRemovedPasswordUsesNone(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true)
	r.creds.secrets[p.SecretRef] = "stored"
	r.m.Test(TestRequest{Profile: p, ExistingID: p.ID, Password: PasswordChange{Mode: RemovePassword}}, func(TestResult) {})
	c := r.conn.next(t)
	if c.password != "" {
		t.Errorf("a removed password was still used: %q", c.password)
	}
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
	if r.creds.gets != 0 {
		t.Errorf("%d store reads", r.creds.gets)
	}
}

func TestTestWithoutAnyPasswordReportsIt(t *testing.T) {
	r := newRig(t)
	p := r.add("A", true) // has a SecretRef but nothing is stored
	var got []TestResult
	r.m.Test(TestRequest{Profile: p, ExistingID: p.ID}, func(res TestResult) { got = append(got, res) })
	r.post.run(t)
	if len(got) != 1 || !got[0].NeedsPassword || got[0].Reason == "" {
		t.Errorf("%+v", got)
	}
	r.conn.expectNoCall(t)
}

func TestTestNeverWritesTheStore(t *testing.T) {
	r := newRig(t)
	prof := Profile{Name: "T", Driver: DriverPostgres, Host: "h", Port: 1, Database: "d", Username: "u"}
	r.m.Test(TestRequest{Profile: prof, Password: PasswordChange{Mode: SetPassword, Value: "typed"}}, func(TestResult) {})
	c := r.conn.next(t)
	c.reply <- connectReply{ad: newAdapter()}
	r.post.run(t)
	if r.creds.sets != 0 || r.creds.gets != 0 {
		t.Errorf("sets %d gets %d", r.creds.sets, r.creds.gets)
	}
}
