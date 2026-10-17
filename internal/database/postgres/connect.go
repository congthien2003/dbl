package postgres

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"dbl/internal/connection"
	"dbl/internal/database"
)

// Adapter is an open PostgreSQL session: a small pool of connections.
type Adapter struct {
	db *sql.DB
	// catalog is the database the pool is bound to, the Catalog of every
	// table reference this adapter returns.
	catalog     string
	sqlMu       sync.Mutex
	sqlConfig   *pgx.ConnConfig
	sqlCtx      context.Context
	endSQL      context.CancelFunc
	sqlClosed   bool
	sqlSessions map[*sqlSession]struct{}
}

// Ping checks that the server answers.
func (a *Adapter) Ping(ctx context.Context) error {
	if err := a.db.PingContext(ctx); err != nil {
		return classify(err, connection.Profile{}, "")
	}
	return nil
}

// Close closes every connection of the pool; it is safe to call twice.
func (a *Adapter) Close() error {
	a.closeSQLSessions()
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// Connect opens a pool for the profile and checks it with a ping, within
// the profile's timeout. On any failure nothing is left open, and the error
// is a *database.Error whose text has no password.
func Connect(ctx context.Context, p connection.Profile, password string) (database.Adapter, error) {
	p = p.Normalized()
	cfg, err := buildConfig(p, password)
	if err != nil {
		return nil, classify(err, p, password)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.ConnectTimeoutSec)*time.Second)
	defer cancel()
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(p.Pool.MaxOpen)
	db.SetMaxIdleConns(p.Pool.MaxIdle)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, classify(err, p, password)
	}
	life, endSQL := context.WithCancel(context.Background())
	return &Adapter{db: db, catalog: p.Database, sqlConfig: cfg.Copy(), sqlCtx: life, endSQL: endSQL, sqlSessions: map[*sqlSession]struct{}{}}, nil
}
