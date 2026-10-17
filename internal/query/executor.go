// Package query owns independent SQL session leases and result snapshots.
package query

import (
	"context"
	"sync"

	"dbl/internal/database"
)

const MaxSessionsPerProfile = 8
const MaxSessions = 16

type Executor struct {
	mu       sync.Mutex
	closed   bool
	total    int
	profiles map[string]int
	leases   map[*Lease]struct{}
	pending  map[*openRun]struct{}
}
type openRun struct{ cancel context.CancelFunc }
type Lease struct {
	owner    *Executor
	profile  string
	native   database.SQLSession
	once     sync.Once
	done     chan struct{}
	stopLife func() bool
	lifeMu   sync.Mutex
}

func NewExecutor() *Executor {
	return &Executor{profiles: map[string]int{}, leases: map[*Lease]struct{}{}, pending: map[*openRun]struct{}{}}
}
func unavailable(message string) error {
	return &database.Error{Kind: database.KindConnection, Message: message}
}
func (e *Executor) Open(openCtx, lifetime context.Context, profile string, f database.SQLSessionFactory) (*Lease, error) {
	e.mu.Lock()
	if e.closed || e.total >= MaxSessions || e.profiles[profile] >= MaxSessionsPerProfile {
		e.mu.Unlock()
		return nil, unavailable("SQL session capacity reached, or the workspace is closing. Close another SQL tab and try again.")
	}
	ctx, cancel := context.WithCancel(openCtx)
	run := &openRun{cancel: cancel}
	e.pending[run] = struct{}{}
	e.total++
	e.profiles[profile]++
	e.mu.Unlock()
	stop := context.AfterFunc(lifetime, cancel)
	native, err := f.OpenSQLSession(ctx)
	stop()
	canceled := ctx.Err()
	cancel()
	e.mu.Lock()
	delete(e.pending, run)
	if err != nil || canceled != nil || lifetime.Err() != nil || e.closed {
		e.total--
		e.profiles[profile]--
		e.mu.Unlock()
		if native != nil {
			closeSession(native)
		}
		if err != nil {
			return nil, err
		}
		return nil, unavailable("The SQL session was closed before it was ready.")
	}
	l := &Lease{owner: e, profile: profile, native: native, done: make(chan struct{})}
	e.leases[l] = struct{}{}
	l.lifeMu.Lock()
	l.stopLife = context.AfterFunc(lifetime, func() { closeSession(l) })
	l.lifeMu.Unlock()
	e.mu.Unlock()
	return l, nil
}
func closeSession(s database.SQLSession) {
	ctx, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
	defer cancel()
	_ = s.Close(ctx)
}
func (l *Lease) Select(in database.SQLInput) (database.SQLSelection, error) {
	select {
	case <-l.done:
		return database.SQLSelection{}, unavailable("The SQL session is closed.")
	default:
	}
	return l.native.Select(in)
}
func (l *Lease) Execute(ctx context.Context, sql string) (*database.SQLExecution, error) {
	select {
	case <-l.done:
		return nil, unavailable("The SQL session is closed.")
	default:
	}
	return l.native.Execute(ctx, sql)
}
func (l *Lease) Close(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		l.lifeMu.Lock()
		stop := l.stopLife
		l.stopLife = nil
		l.lifeMu.Unlock()
		if stop != nil {
			stop()
		}
		err = l.native.Close(ctx)
		l.owner.mu.Lock()
		delete(l.owner.leases, l)
		l.owner.total--
		l.owner.profiles[l.profile]--
		l.owner.mu.Unlock()
		close(l.done)
	})
	return err
}
func (e *Executor) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	for r := range e.pending {
		r.cancel()
	}
	var leases []*Lease
	for l := range e.leases {
		leases = append(leases, l)
	}
	e.mu.Unlock()
	var wg sync.WaitGroup
	for _, l := range leases {
		wg.Add(1)
		go func() { defer wg.Done(); _ = l.Close(ctx) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
