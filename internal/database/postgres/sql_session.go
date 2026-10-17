package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgproto3"

	"dbl/internal/database"
)

type sqlSession struct {
	owner      *Adapter
	conn       *pgx.Conn
	transport  net.Conn
	reader     *sqlProtocolReader
	secret     string
	op         sync.Mutex
	mu         sync.Mutex
	closing    bool
	active     context.CancelFunc
	closeOnce  sync.Once
	closed     chan struct{}
	stopParent func() bool
	current    *database.SQLExecution // only accessed by the protocol owner
}

func (a *Adapter) OpenSQLSession(ctx context.Context) (database.SQLSession, error) {
	a.sqlMu.Lock()
	if a.sqlClosed || a.sqlConfig == nil {
		a.sqlMu.Unlock()
		return nil, sqlValidation("The connection is not open.")
	}
	cfg := a.sqlConfig.Copy()
	parent := a.sqlCtx
	a.sqlMu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	open, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(parent, cancel)
	defer func() { stop(); cancel() }()
	s := &sqlSession{owner: a, secret: cfg.Password, closed: make(chan struct{})}
	cfg.MaxProtocolMessageBodyLen = database.SQLMessageBytes
	cfg.BuildFrontend = func(r io.Reader, w io.Writer) *pgproto3.Frontend {
		s.reader = &sqlProtocolReader{source: r}
		return pgproto3.NewFrontend(s.reader, w)
	}
	cfg.AfterNetConnect = func(_ context.Context, _ *pgconn.Config, conn net.Conn) (net.Conn, error) {
		s.transport = conn
		return conn, nil
	}
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: database.SQLCleanupTimeout}
	}
	// Override pgx's default notification queue: LISTEN is legal, but this
	// editor does not retain an unbounded asynchronous notification history.
	cfg.OnNotification = func(*pgconn.PgConn, *pgconn.Notification) {}
	cfg.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) {
		if s.current == nil {
			return
		}
		if len(s.current.Notices) >= database.SQLMaxNotices {
			s.current.DroppedNotices++
			return
		}
		message := database.Redact(notice.Message, s.secret)
		s.current.Notices = append(s.current.Notices, boundedSQLText(message, database.SQLNoticeBytes))
	}
	conn, err := pgx.ConnectConfig(open, cfg)
	if err != nil {
		return nil, classifySQLExecution(open, err, s.secret)
	}
	s.conn = conn
	a.sqlMu.Lock()
	if a.sqlClosed || open.Err() != nil || parent.Err() != nil {
		a.sqlMu.Unlock()
		cleanup, done := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
		defer done()
		_ = conn.Close(cleanup)
		return nil, sqlValidation("The connection closed while the SQL session was opening.")
	}
	if a.sqlSessions == nil {
		a.sqlSessions = map[*sqlSession]struct{}{}
	}
	a.sqlSessions[s] = struct{}{}
	s.mu.Lock()
	s.stopParent = context.AfterFunc(parent, func() {
		cleanup, done := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
		defer done()
		_ = s.Close(cleanup)
	})
	s.mu.Unlock()
	a.sqlMu.Unlock()
	return s, nil
}

func (a *Adapter) closeSQLSessions() {
	a.sqlMu.Lock()
	a.sqlClosed = true
	if a.endSQL != nil {
		a.endSQL()
	}
	a.sqlConfig = nil
	var sessions []*sqlSession
	for s := range a.sqlSessions {
		sessions = append(sessions, s)
	}
	a.sqlMu.Unlock()
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

func (s *sqlSession) Select(input database.SQLInput) (database.SQLSelection, error) {
	if !s.op.TryLock() {
		return database.SQLSelection{}, sqlValidation("A query is already running in this SQL tab.")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing || s.conn.IsClosed() {
		return database.SQLSelection{}, &database.Error{Kind: database.KindConnection, Message: "The SQL session is closed. Open a new session to continue."}
	}
	setting := s.conn.PgConn().ParameterStatus("standard_conforming_strings")
	if setting != "on" && setting != "off" {
		for _, span := range input.Selections {
			if span.Start != span.End {
				return SelectPostgresSQL(input, true)
			}
		}
		return database.SQLSelection{}, sqlUnsupported("The session string-escaping mode is unknown. Select a complete SQL block.")
	}
	return SelectPostgresSQL(input, setting == "on")
}

func (s *sqlSession) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		if s.active != nil {
			s.active()
		}
		stop := s.stopParent
		s.stopParent = nil
		s.mu.Unlock()
		if stop != nil {
			stop()
		}
		go func() {
			defer close(s.closed)
			cleanup, cancel := context.WithTimeout(ctx, database.SQLCleanupTimeout)
			defer cancel()
			force := context.AfterFunc(cleanup, func() {
				if s.transport != nil {
					_ = s.transport.Close()
				}
			})
			defer force()
			s.op.Lock()
			defer s.op.Unlock()
			if !s.conn.IsClosed() {
				state := s.conn.PgConn().TxStatus()
				if state == 'T' || state == 'E' {
					_ = s.conn.PgConn().Exec(cleanup, "ROLLBACK").Close()
				}
				_ = s.conn.Close(cleanup)
			}
			s.owner.sqlMu.Lock()
			delete(s.owner.sqlSessions, s)
			s.owner.sqlMu.Unlock()
			s.secret = ""
		}()
	})
	select {
	case <-s.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *sqlSession) Execute(ctx context.Context, text string) (*database.SQLExecution, error) {
	if !s.op.TryLock() {
		return nil, sqlValidation("A query is already running in this SQL tab.")
	}
	defer s.op.Unlock()
	s.mu.Lock()
	if s.closing || s.conn.IsClosed() {
		s.mu.Unlock()
		return nil, &database.Error{Kind: database.KindConnection, Message: "The SQL session is closed. Open a new session to continue."}
	}
	run, cancel := context.WithTimeout(ctx, database.SQLTimeout)
	s.active = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.active = nil; s.mu.Unlock() }()
	if len(text) > database.SQLMaxInputBytes {
		return nil, sqlValidation("SQL input exceeds 8 MiB.")
	}
	// Preflight the complete block without splitting its execution.
	_, err := SelectPostgresSQL(database.SQLInput{Text: text, Selections: []database.SQLSpan{{Start: 0, End: len(text)}}}, s.conn.PgConn().ParameterStatus("standard_conforming_strings") != "off")
	if err != nil {
		return nil, err
	}
	if run.Err() != nil {
		return nil, classifySQLExecution(run, run.Err(), s.secret)
	}
	start := time.Now()
	r := &database.SQLExecution{}
	s.current = r
	s.reader.begin()
	defer func() { s.reader.end(); s.current = nil }()
	err = s.readExecution(run, text, r)
	r.Elapsed = time.Since(start)
	if s.conn.IsClosed() {
		r.Transaction = database.SQLSessionLost
	} else {
		switch s.conn.PgConn().TxStatus() {
		case 'T':
			r.Transaction = database.SQLInTransaction
		case 'E':
			r.Transaction = database.SQLTransactionFailed
		default:
			r.Transaction = database.SQLIdle
		}
	}
	if err != nil {
		r.Err = classifySQLExecution(run, err, s.secret)
		r.Stop = database.SQLFailed
		switch {
		case errors.Is(err, errSQLResultLimit) || s.reader.limited.Load():
			r.Stop = database.SQLLimitReached
			r.Err = &database.Error{Kind: database.KindValidation, Message: "The query was stopped at the result limit. Results are partial; check the transaction state."}
		case errors.Is(run.Err(), context.DeadlineExceeded):
			r.Stop = database.SQLTimedOut
		case errors.Is(run.Err(), context.Canceled):
			r.Stop = database.SQLCanceled
		case r.Err.Kind == database.KindTimeout:
			r.Stop = database.SQLTimedOut
		case r.Err.Kind == database.KindCanceled:
			r.Stop = database.SQLCanceled
		}
		r.OutcomeUncertain = !r.Ready
		return r, r.Err
	}
	r.Stop = database.SQLCompleted
	return r, nil
}
