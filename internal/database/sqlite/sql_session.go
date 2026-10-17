package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	sqlite3driver "github.com/ncruces/go-sqlite3/driver"

	"dbl/internal/database"
)

var _ database.SQLSessionFactory = (*Adapter)(nil)

// sqlSession is one SQL tab's connection to the file. It has its own
// connection from the pool, so a transaction opened in the editor belongs to
// the tab and to nothing else.
type sqlSession struct {
	owner     *Adapter
	conn      *sql.Conn
	op        sync.Mutex // held while the session works
	mu        sync.Mutex
	closing   bool
	active    context.CancelFunc
	closeOnce sync.Once
	closed    chan struct{}
}

// OpenSQLSession opens an independent session on the file.
func (a *Adapter) OpenSQLSession(ctx context.Context) (database.SQLSession, error) {
	a.sessMu.Lock()
	shut := a.sessClosed
	a.sessMu.Unlock()
	if shut {
		return nil, sqlValidation("The connection is not open.")
	}
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return nil, classify(ctx, err, "open a SQL session")
	}
	s := &sqlSession{owner: a, conn: conn, closed: make(chan struct{})}
	a.sessMu.Lock()
	if a.sessClosed {
		a.sessMu.Unlock()
		_ = conn.Close()
		return nil, sqlValidation("The connection closed while the SQL session was opening.")
	}
	if a.sessions == nil {
		a.sessions = map[*sqlSession]struct{}{}
	}
	a.sessions[s] = struct{}{}
	a.sessMu.Unlock()
	return s, nil
}

// closeSQLSessions ends every session of the adapter, within the cleanup
// time, and refuses new ones.
func (a *Adapter) closeSQLSessions() {
	a.sessMu.Lock()
	a.sessClosed = true
	var sessions []*sqlSession
	for s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.sessMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Close(ctx) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Select picks the statement to run.
func (s *sqlSession) Select(input database.SQLInput) (database.SQLSelection, error) {
	if !s.op.TryLock() {
		return database.SQLSelection{}, sqlValidation("A query is already running in this SQL tab.")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return database.SQLSelection{}, &database.Error{Kind: database.KindConnection, Message: "The SQL session is closed. Open a new session to continue."}
	}
	return SelectSQLiteSQL(input)
}

// Close stops what the session is doing, rolls back an open transaction and
// gives the connection back. It waits at most until ctx ends or the cleanup
// time has passed.
func (s *sqlSession) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		if s.active != nil {
			s.active()
		}
		s.mu.Unlock()
		go func() {
			defer close(s.closed)
			cleanup, cancel := context.WithTimeout(ctx, database.SQLCleanupTimeout)
			defer cancel()
			s.op.Lock()
			defer s.op.Unlock()
			if autocommit, err := s.autocommit(); err == nil && !autocommit {
				_, _ = s.conn.ExecContext(cleanup, "ROLLBACK")
			}
			_ = s.conn.Close()
			s.owner.sessMu.Lock()
			delete(s.owner.sessions, s)
			s.owner.sessMu.Unlock()
		}()
	})
	select {
	case <-s.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// autocommit reports whether the connection is outside a transaction.
func (s *sqlSession) autocommit() (bool, error) {
	var auto bool
	err := s.conn.Raw(func(dc any) error {
		c, ok := dc.(sqlite3driver.Conn)
		if !ok {
			return errors.New("sqlite: unexpected driver connection")
		}
		auto = c.Raw().GetAutocommit()
		return nil
	})
	return auto, err
}

// counters reads the number of rows changed so far by the connection and by
// the last statement that changed any.
func (s *sqlSession) counters() (total, last int64, err error) {
	err = s.conn.Raw(func(dc any) error {
		c, ok := dc.(sqlite3driver.Conn)
		if !ok {
			return errors.New("sqlite: unexpected driver connection")
		}
		total, last = c.Raw().TotalChanges(), c.Raw().Changes()
		return nil
	})
	return total, last, err
}

// transaction maps the state of the connection to the editor's states. A
// failed statement does not break a SQLite transaction, so the only states
// are idle, in a transaction, and lost.
func (s *sqlSession) transaction() database.SQLTransaction {
	probe, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
	defer cancel()
	if err := s.conn.PingContext(probe); err != nil {
		return database.SQLSessionLost
	}
	auto, err := s.autocommit()
	switch {
	case err != nil:
		return database.SQLSessionLost
	case auto:
		return database.SQLIdle
	}
	return database.SQLInTransaction
}
