package mysql

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"dbl/internal/database"
)

var _ database.SQLSessionFactory = (*Adapter)(nil)

// Server status flags, as the server sends them with every OK and EOF
// packet (SERVER_STATUS_* in the MySQL client/server protocol).
const (
	statusInTrans            = 1 << 0
	statusNoBackslashEscapes = 1 << 9
)

// killGrace is how long a statement may take to stop after KILL QUERY
// before its connection is dropped.
const killGrace = 2 * time.Second

// sqlSession is one SQL tab's connection to the server. It is a pool of its
// own with a single connection, so a transaction opened in the editor
// belongs to the tab and nothing else, and no setting of the tab reaches
// the table tabs.
type sqlSession struct {
	owner     *Adapter
	db        *sql.DB
	conn      *sql.Conn
	id        int64 // CONNECTION_ID(), for KILL QUERY
	op        sync.Mutex
	mu        sync.Mutex
	closing   bool
	active    context.CancelFunc
	closeOnce sync.Once
	closed    chan struct{}
}

// OpenSQLSession opens an independent session on the server.
func (a *Adapter) OpenSQLSession(ctx context.Context) (database.SQLSession, error) {
	a.sessMu.Lock()
	shut := a.sessClosed
	a.sessMu.Unlock()
	if shut {
		return nil, sqlValidation("The connection is not open.")
	}
	connector, err := gomysql.NewConnector(a.sessionCfg)
	if err != nil {
		return nil, classify(ctx, err, "open a SQL session")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, classify(ctx, err, "open a SQL session")
	}
	var id int64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, classify(ctx, err, "open a SQL session")
	}
	s := &sqlSession{owner: a, db: db, conn: conn, id: id, closed: make(chan struct{})}
	a.sessMu.Lock()
	if a.sessClosed {
		a.sessMu.Unlock()
		_ = conn.Close()
		_ = db.Close()
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

// killQuery asks the server to stop the statement a session runs, from a
// connection of the adapter's pool. It reports whether the server took the
// request.
func (a *Adapter) killQuery(id int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), killGrace)
	defer cancel()
	_, err := a.db.ExecContext(ctx, "KILL QUERY "+strconv.FormatInt(id, 10))
	return err == nil
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
	st, _ := s.status()
	return SelectMySQLSQL(input, st&statusNoBackslashEscapes != 0)
}

// Close stops what the session is doing, rolls back an open transaction and
// closes the connection. It waits at most until ctx ends or the cleanup
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
			cleanup, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
			defer cancel()
			s.op.Lock()
			defer s.op.Unlock()
			if st, err := s.status(); err == nil && st&statusInTrans != 0 {
				_, _ = s.conn.ExecContext(cleanup, "ROLLBACK")
			}
			_ = s.conn.Close()
			_ = s.db.Close()
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

var errNoStatus = errors.New("mysql: the driver does not expose the server status")

// status returns the server status flags of the session's connection as of
// the last packet the server sent. The driver keeps them but does not
// export them, so they are read by reflection; a test pins the driver
// version this works with.
func (s *sqlSession) status() (uint64, error) {
	var st uint64
	err := s.conn.Raw(func(dc any) error {
		v := reflect.ValueOf(dc)
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return errNoStatus
		}
		f := v.FieldByName("status")
		if !f.IsValid() || f.Kind() != reflect.Uint16 {
			return errNoStatus
		}
		st = f.Uint()
		return nil
	})
	return st, err
}

// lastAffected returns the rows the last statement changed, from the OK
// packet the driver keeps (unexported, like the status; read the same way).
func (s *sqlSession) lastAffected() (int64, bool) {
	var n int64
	ok := false
	_ = s.conn.Raw(func(dc any) error {
		v := reflect.ValueOf(dc)
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return errNoStatus
		}
		r := v.FieldByName("result")
		if !r.IsValid() || r.Kind() != reflect.Struct {
			return errNoStatus
		}
		rows := r.FieldByName("affectedRows")
		if !rows.IsValid() || rows.Kind() != reflect.Slice || rows.Len() == 0 || rows.Index(0).Kind() != reflect.Int64 {
			return errNoStatus
		}
		n, ok = rows.Index(rows.Len()-1).Int(), true
		return nil
	})
	return n, ok
}

// transaction maps the state of the connection to the editor's states. A
// failed statement does not end a MySQL transaction (a deadlock rolls it
// back, which the status shows), so the states are idle, in a transaction,
// and lost.
func (s *sqlSession) transaction() database.SQLTransaction {
	probe, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
	defer cancel()
	if err := s.conn.PingContext(probe); err != nil {
		return database.SQLSessionLost
	}
	st, err := s.status()
	switch {
	case err != nil:
		return database.SQLSessionLost
	case st&statusInTrans != 0:
		return database.SQLInTransaction
	}
	return database.SQLIdle
}
