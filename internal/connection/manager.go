package connection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"dbl/internal/database"
)

// Status is the state of one connection.
type Status uint8

// Connection states. A connection changes state only on the UI thread.
const (
	Disconnected  Status = iota
	Connecting           // an attempt is running
	Connected            // a session is open
	Failed               // the last attempt failed; Err says why
	NeedsPassword        // the last attempt found no password; the UI asks for it
)

func (s Status) String() string {
	switch s {
	case Connecting:
		return "Connecting"
	case Connected:
		return "Connected"
	case Failed:
		return "Failed"
	case NeedsPassword:
		return "Password needed"
	}
	return "Disconnected"
}

// Connector opens a session with the server of a profile. It must honor
// ctx, and it must not keep the password.
type Connector func(ctx context.Context, p Profile, password string) (database.Adapter, error)

// attempt is one connect run. Cancelling it is final: whatever it returns
// afterwards is discarded.
type attempt struct {
	stop     context.CancelFunc
	ctx      context.Context
	canceled atomic.Bool
}

func newAttempt() *attempt {
	ctx, stop := context.WithCancel(context.Background())
	return &attempt{ctx: ctx, stop: stop}
}

func (a *attempt) cancel() {
	a.canceled.Store(true)
	a.stop()
}

// entry is the runtime state of one profile.
type entry struct {
	status  Status
	err     *database.Error
	reason  string // why a password is needed
	adapter database.Adapter
	running *attempt // the attempt allowed to report, nil when none

	// epoch numbers the session: it changes with every new session, so work
	// started on an older one can tell that it is stale. sessionCtx ends
	// when the session does, which stops the queries running on it.
	epoch      uint64
	sessionCtx context.Context
	endSession context.CancelFunc

	// session is a password held in memory only: typed in the prompt, or
	// kept because the credential store failed. It wins over the store, as
	// it is the newest one known.
	session *string
}

// Manager opens, tracks and closes the sessions of the profiles of a
// Catalog.
//
// All methods run on the UI thread. Work that can block runs on its own
// goroutine; its result returns through post, and is applied only when it
// still belongs to the current attempt of a profile that still exists (the
// completion gate of the design). A result that fails the gate closes the
// session it carries and changes nothing.
type Manager struct {
	// OnCredentialIssue is called on the UI thread when a password could not
	// be saved or removed. profileID is empty when no profile is involved.
	OnCredentialIssue func(profileID string, err error)

	cat     *Catalog
	creds   CredentialStore
	connect Connector
	post    func(func())

	entries   map[string]*entry
	tests     map[int]*testRun
	nextRun   int
	nextEpoch uint64
	closed    bool

	// limbo holds the sessions of finished attempts whose result is still on
	// its way to the UI thread. If the app quits before the result arrives
	// (the event loop drops it), CloseAll closes them.
	limboMu sync.Mutex
	limbo   map[*parked]struct{}
}

// parked is a session in limbo.
type parked struct{ ad database.Adapter }

// park puts a session in limbo; a nil session needs none.
func (m *Manager) park(ad database.Adapter) *parked {
	if ad == nil {
		return nil
	}
	pk := &parked{ad: ad}
	m.limboMu.Lock()
	m.limbo[pk] = struct{}{}
	m.limboMu.Unlock()
	return pk
}

// unpark takes a session out of limbo and reports whether the caller now
// owns it; false means CloseAll took it and closed it.
func (m *Manager) unpark(pk *parked) bool {
	if pk == nil {
		return true
	}
	m.limboMu.Lock()
	defer m.limboMu.Unlock()
	_, ok := m.limbo[pk]
	delete(m.limbo, pk)
	return ok
}

// NewManager returns a manager for the profiles of cat. post runs a
// function on the UI thread; it may be set later with SetPost.
func NewManager(cat *Catalog, creds CredentialStore, connect Connector, post func(func())) *Manager {
	return &Manager{
		cat: cat, creds: creds, connect: connect, post: post,
		entries: map[string]*entry{}, tests: map[int]*testRun{},
		limbo: map[*parked]struct{}{},
	}
}

// SetPost sets the function that runs code on the UI thread.
func (m *Manager) SetPost(post func(func())) {
	if m != nil {
		m.post = post
	}
}

func (m *Manager) poster() func(func()) {
	post := m.post
	return func(fn func()) {
		if post != nil {
			post(fn)
		}
	}
}

// Post runs fn on the UI thread; it does nothing before SetPost, or after
// the app quit and the event loop dropped it.
func (m *Manager) Post(fn func()) {
	if m != nil {
		m.poster()(fn)
	}
}

func (m *Manager) entry(id string) *entry {
	e := m.entries[id]
	if e == nil {
		e = &entry{}
		m.entries[id] = e
	}
	return e
}

// Status returns the state of a connection; unknown IDs are Disconnected.
func (m *Manager) Status(id string) Status {
	if m == nil {
		return Disconnected
	}
	if e := m.entries[id]; e != nil {
		return e.status
	}
	return Disconnected
}

// Err returns why the last attempt of the connection failed.
func (m *Manager) Err(id string) *database.Error {
	if m == nil {
		return nil
	}
	if e := m.entries[id]; e != nil && e.status == Failed {
		return e.err
	}
	return nil
}

// PromptReason says why the password of a connection is asked for.
func (m *Manager) PromptReason(id string) string {
	if m == nil {
		return ""
	}
	if e := m.entries[id]; e != nil && e.status == NeedsPassword {
		return e.reason
	}
	return ""
}

// Active returns the IDs of the connections that are connecting or
// connected, in catalog order.
func (m *Manager) Active() []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, p := range m.cat.Profiles {
		if s := m.Status(p.ID); s == Connecting || s == Connected {
			out = append(out, p.ID)
		}
	}
	return out
}

// attemptResult is what a connect run found.
type attemptResult struct {
	adapter       database.Adapter
	err           error
	needsPassword bool
	reason        string
	password      string // for redaction only
}

// asError turns err into a database.Error whose text has no password.
func asError(err error, password string) *database.Error {
	var de *database.Error
	if errors.As(err, &de) {
		c := *de
		c.Message = database.Redact(c.Message, password)
		c.Hint = database.Redact(c.Hint, password)
		return &c
	}
	return &database.Error{
		Kind:    database.KindConnection,
		Message: database.Redact(err.Error(), password),
		Err:     err,
	}
}

func closeAsync(a database.Adapter) {
	if a != nil {
		go func() { _ = a.Close() }()
	}
}

// resolvePassword finds the password to connect with, on a worker. A
// profile without a SecretRef uses none. The session password wins over the
// store; with neither, the user must be asked.
func resolvePassword(creds CredentialStore, ref string, session *string) (pw string, needs bool, reason string) {
	if ref == "" {
		return "", false, ""
	}
	if session != nil {
		return *session, false, ""
	}
	if creds == nil {
		return "", true, "The system has no secure password storage."
	}
	pw, err := creds.Get(ref)
	switch {
	case err == nil:
		return pw, false, ""
	case errors.Is(err, ErrSecretNotFound):
		return "", true, "No saved password was found for this connection."
	}
	return "", true, "The saved password could not be read: " + err.Error()
}

func copySession(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

// Connect starts connecting the profile. Nothing happens for an unknown
// profile or one that is already connecting or connected.
func (m *Manager) Connect(id string) {
	if m == nil || m.closed {
		return
	}
	p, ok := m.cat.Profile(id)
	if !ok {
		return
	}
	e := m.entry(id)
	if e.status == Connecting || e.status == Connected {
		return
	}
	if !p.Driver.Supported() || m.connect == nil {
		e.status, e.err = Failed, &database.Error{
			Kind:    database.KindUnsupported,
			Message: fmt.Sprintf("%s connections are not supported yet.", p.Driver.Label()),
		}
		return
	}
	a := newAttempt()
	e.status, e.err, e.reason, e.running = Connecting, nil, "", a
	session := copySession(e.session)
	creds, connect, post := m.creds, m.connect, m.poster()
	go func() {
		defer a.stop()
		res := runAttempt(a, creds, connect, p, session)
		if a.canceled.Load() {
			// Whoever cancelled has already updated the state.
			closeAsync(res.adapter)
			return
		}
		pk := m.park(res.adapter)
		post(func() { m.finish(id, a, res, pk) })
	}()
}

func runAttempt(a *attempt, creds CredentialStore, connect Connector, p Profile, session *string) attemptResult {
	pw, needs, reason := resolvePassword(creds, p.SecretRef, session)
	if needs {
		return attemptResult{needsPassword: true, reason: reason}
	}
	timeout := time.Duration(p.Normalized().ConnectTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	defer cancel()
	ad, err := connect(ctx, p, pw)
	if err != nil {
		if ad != nil {
			_ = ad.Close()
		}
		return attemptResult{err: err, password: pw}
	}
	return attemptResult{adapter: ad, password: pw}
}

// finish is the completion gate: it applies the result of attempt a only
// when the app is running, a is still the attempt of the profile, and the
// profile still exists.
func (m *Manager) finish(id string, a *attempt, res attemptResult, pk *parked) {
	if !m.unpark(pk) {
		return // CloseAll already closed the session
	}
	e := m.entries[id]
	if m.closed || a.canceled.Load() || e == nil || e.running != a {
		closeAsync(res.adapter)
		return
	}
	if _, ok := m.cat.Profile(id); !ok {
		closeAsync(res.adapter)
		delete(m.entries, id)
		return
	}
	e.running = nil
	switch {
	case res.needsPassword:
		e.status, e.reason = NeedsPassword, res.reason
	case res.err != nil:
		e.status, e.err = Failed, asError(res.err, res.password)
	default:
		e.status, e.adapter = Connected, res.adapter
		m.nextEpoch++
		e.epoch = m.nextEpoch
		e.sessionCtx, e.endSession = context.WithCancel(context.Background())
	}
}

// stopSession stops the work running on the session of e.
func (e *entry) stopSession() {
	if e.endSession != nil {
		e.endSession()
		e.endSession = nil
	}
}

// SQL returns the relational session of a connected profile. The second
// result is false when the connection is not connected or its driver has no
// relational session.
func (m *Manager) SQL(id string) (database.SQLAdapter, bool) {
	if m == nil {
		return nil, false
	}
	e := m.entries[id]
	if e == nil || e.status != Connected || e.adapter == nil {
		return nil, false
	}
	ad, ok := e.adapter.(database.SQLAdapter)
	return ad, ok
}

// SQLSessions returns the independent SQL session capability of an open profile.
func (m *Manager) SQLSessions(id string) (database.SQLSessionFactory, bool) {
	if m == nil {
		return nil, false
	}
	e := m.entries[id]
	if e == nil || e.status != Connected || e.adapter == nil {
		return nil, false
	}
	f, ok := e.adapter.(database.SQLSessionFactory)
	return f, ok
}

// Epoch identifies the current session of a connection; it is 0 when there
// is none. A new session, even after a reconnect, has a new number.
func (m *Manager) Epoch(id string) uint64 {
	if m == nil {
		return 0
	}
	if e := m.entries[id]; e != nil && e.status == Connected {
		return e.epoch
	}
	return 0
}

// SessionContext returns a context that ends when the session of the
// connection does (disconnect, delete, quit). Work started on the session
// derives its context from it. With no session it returns a context that
// is already canceled.
func (m *Manager) SessionContext(id string) context.Context {
	if m != nil {
		if e := m.entries[id]; e != nil && e.status == Connected && e.sessionCtx != nil {
			return e.sessionCtx
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// Disconnect cancels a running attempt and closes the session. The profile
// stays; the state becomes Disconnected.
func (m *Manager) Disconnect(id string) {
	if m == nil {
		return
	}
	e := m.entries[id]
	if e == nil {
		return
	}
	if e.running != nil {
		e.running.cancel()
		e.running = nil
	}
	e.stopSession()
	closeAsync(e.adapter)
	e.adapter = nil
	e.status, e.err, e.reason = Disconnected, nil, ""
}

// Forget disconnects and drops everything the manager knows about the
// profile, including its session password. Use it when the profile is
// deleted.
func (m *Manager) Forget(id string) {
	if m == nil {
		return
	}
	m.Disconnect(id)
	delete(m.entries, id)
}

// SubmitPassword answers the prompt of a connection: the password is kept
// in memory only, and a new attempt starts. It does nothing unless the
// connection is waiting for a password.
func (m *Manager) SubmitPassword(id, password string) {
	if m == nil || m.Status(id) != NeedsPassword {
		return
	}
	m.entry(id).session = &password
	m.Connect(id)
}

// CancelPrompt dismisses the password prompt: the connection stays
// disconnected.
func (m *Manager) CancelPrompt(id string) {
	if m != nil && m.Status(id) == NeedsPassword {
		m.Disconnect(id)
	}
}

// ClearSessionPassword forgets the in-memory password of a profile.
func (m *Manager) ClearSessionPassword(id string) {
	if m != nil {
		if e := m.entries[id]; e != nil {
			e.session = nil
		}
	}
}

// HasSessionPassword reports whether a password is held in memory for the
// profile because it could not be saved.
func (m *Manager) HasSessionPassword(id string) bool {
	if m == nil {
		return false
	}
	e := m.entries[id]
	return e != nil && e.session != nil
}

func (m *Manager) issue(id string, err error) {
	if m.OnCredentialIssue != nil {
		m.OnCredentialIssue(id, err)
	}
}

// StorePassword saves the password of a profile in the credential store.
// Until the store has it, and whenever the store fails, the password stays
// in memory for this session so the profile keeps working; a failure is
// reported through OnCredentialIssue.
func (m *Manager) StorePassword(id, password string) {
	if m == nil {
		return
	}
	e := m.entry(id)
	pw := password
	e.session = &pw
	ref := SecretRefFor(id)
	creds, post := m.creds, m.poster()
	go func() {
		var err error
		if creds == nil {
			err = ErrCredentialsUnavailable
		} else {
			err = creds.Set(ref, pw)
		}
		post(func() {
			if m.closed {
				return
			}
			if err != nil {
				m.issue(id, err)
				return
			}
			// The store has it now. Drop the in-memory copy unless a newer
			// password replaced it meanwhile.
			if cur := m.entries[id]; cur != nil && cur.session == &pw {
				cur.session = nil
			}
		})
	}()
}

// DeleteStoredPassword removes a password from the credential store. A
// failure is reported through OnCredentialIssue.
func (m *Manager) DeleteStoredPassword(ref string) {
	if m == nil || ref == "" || m.creds == nil {
		return
	}
	creds, post := m.creds, m.poster()
	go func() {
		if err := creds.Delete(ref); err != nil {
			post(func() {
				if !m.closed {
					m.issue("", err)
				}
			})
		}
	}()
}

// TestRequest describes a connection test from the form.
type TestRequest struct {
	Profile Profile
	// ExistingID is the saved profile being edited, "" for a new one.
	ExistingID string
	// Password says which password to use: a typed one (SetPassword), none
	// (RemovePassword), or, with KeepPassword, the one saved for
	// ExistingID.
	Password PasswordChange
}

// TestResult is the outcome of a test.
type TestResult struct {
	OK            bool
	Err           *database.Error
	NeedsPassword bool // no saved password could be found for the profile
	Reason        string
	Duration      time.Duration
}

type testRun struct {
	a *attempt
}

// Test opens a separate session with the profile, checks it and closes it
// at once; nothing is registered with the manager. done is called on the UI
// thread, once, unless the test was cancelled or the manager closed. The
// returned function cancels the test.
func (m *Manager) Test(req TestRequest, done func(TestResult)) (cancel func()) {
	if m == nil || m.closed {
		return func() {}
	}
	m.nextRun++
	runID := m.nextRun
	run := &testRun{a: newAttempt()}
	m.tests[runID] = run
	p := req.Profile.Normalized()
	var ref string
	var session *string
	switch req.Password.Mode {
	case KeepPassword:
		if old, ok := m.cat.Profile(req.ExistingID); ok {
			ref = old.SecretRef
			if e := m.entries[req.ExistingID]; e != nil {
				session = copySession(e.session)
			}
		}
	case SetPassword:
		v := req.Password.Value
		session, ref = &v, "typed"
	}
	creds, connect, post := m.creds, m.connect, m.poster()
	go func() {
		defer run.a.stop()
		start := time.Now()
		var res TestResult
		switch {
		case connect == nil:
			res.Err = &database.Error{Kind: database.KindUnsupported, Message: "Connections are not available."}
		default:
			ar := runAttempt(run.a, creds, connect, withRef(p, ref), session)
			res.Duration = time.Since(start)
			switch {
			case ar.needsPassword:
				res.NeedsPassword, res.Reason = true, ar.reason
			case ar.err != nil:
				res.Err = asError(ar.err, ar.password)
			default:
				res.OK = true
			}
			// A test never keeps its session.
			if ar.adapter != nil {
				_ = ar.adapter.Close()
			}
		}
		if run.a.canceled.Load() {
			return
		}
		post(func() {
			if m.closed || run.a.canceled.Load() || m.tests[runID] != run {
				return
			}
			delete(m.tests, runID)
			done(res)
		})
	}()
	return func() {
		run.a.cancel()
		delete(m.tests, runID)
	}
}

// withRef returns p with the secret reference the test should resolve.
func withRef(p Profile, ref string) Profile {
	p.SecretRef = ref
	return p
}

// CloseAll ends every attempt and test and closes every session, waiting at
// most timeout. It does not use post, so it works while the app quits, and
// it is safe to call more than once.
func (m *Manager) CloseAll(timeout time.Duration) {
	if m == nil {
		return
	}
	m.closed = true
	var adapters []database.Adapter
	for id, e := range m.entries {
		if e.running != nil {
			e.running.cancel()
		}
		e.stopSession()
		if e.adapter != nil {
			adapters = append(adapters, e.adapter)
		}
		delete(m.entries, id)
	}
	for id, t := range m.tests {
		t.a.cancel()
		delete(m.tests, id)
	}
	m.limboMu.Lock()
	for pk := range m.limbo {
		adapters = append(adapters, pk.ad)
		delete(m.limbo, pk)
	}
	m.limboMu.Unlock()
	if len(adapters) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, a := range adapters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.Close()
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
