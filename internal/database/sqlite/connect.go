// Package sqlite is the SQLite adapter. It opens a database file that
// already exists, through database/sql and the pure-Go (no CGO) driver
// github.com/ncruces/go-sqlite3 v0.35.6 (module version pinned in go.mod).
// The driver bundles its SQLite build, so importing its embed package is
// not needed.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/query"
)

// busyTimeoutMs is how long one statement waits for a lock held by another
// process before it fails as busy.
var busyTimeoutMs = 5000

// Adapter is an open SQLite database: a small pool of connections to one
// file.
type Adapter struct {
	db       *sql.DB
	path     string
	catalog  string // the Catalog of every table reference: the file's name
	readOnly bool

	sessMu     sync.Mutex
	sessions   map[*sqlSession]struct{}
	sessClosed bool
}

// Ping checks that the file still answers.
func (a *Adapter) Ping(ctx context.Context) error {
	if err := a.db.PingContext(ctx); err != nil {
		return classify(ctx, err, "reach the database")
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

// uriPathEscaper escapes the three characters a SQLite URI reads as
// something other than part of the file name.
var uriPathEscaper = strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F")

// buildDSN returns the URI that opens the file at path. The path is
// escaped, so a name with '#', '?' or '%' is the file it names. The mode is
// rw or ro, never rwc: a missing file is an error, not a new database.
//
// The path follows "file:" as it is: SQLite's Windows VFS fails on the
// /C:/dir/x.db of a URL and wants the plain C:\dir\x.db.
func buildDSN(path string, readOnly bool) string {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	q := url.Values{}
	q.Set("mode", mode)
	// The busy timeout must be the first pragma.
	q.Add("_pragma", "busy_timeout("+strconv.Itoa(busyTimeoutMs)+")")
	q.Add("_pragma", "foreign_keys(1)")
	return "file:" + uriPathEscaper.Replace(path) + "?" + q.Encode()
}

// writable reports whether the file can be opened for writing. A file the
// user may only read is opened read-only instead of failing on the first
// Save.
func writable(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return !errors.Is(err, fs.ErrPermission)
	}
	_ = f.Close()
	return true
}

// Connect opens the SQLite file of the profile and checks it, within the
// profile's timeout. It never creates the file. On any failure nothing is
// left open.
func Connect(ctx context.Context, p connection.Profile, password string) (database.Adapter, error) {
	p = p.Normalized()
	path, err := filepath.Abs(p.SQLitePath)
	if err != nil {
		return nil, classify(ctx, err, "open the database")
	}
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &database.Error{Kind: database.KindFileAccess, Message: "File not found", Hint: "Check the path of the database file.", Err: err}
	case err != nil:
		return nil, &database.Error{Kind: database.KindFileAccess, Message: "The file cannot be read", Err: err}
	case fi.IsDir():
		return nil, &database.Error{Kind: database.KindFileAccess, Message: "This is a folder, not a database file"}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.ConnectTimeoutSec)*time.Second)
	defer cancel()

	readOnly := p.ReadOnly || !writable(path)
	db, err := openPool(ctx, path, readOnly)
	if err != nil && !readOnly && database.KindOf(err) == database.KindPermission {
		readOnly = true
		db, err = openPool(ctx, path, true)
	}
	if err != nil {
		return nil, err
	}
	return &Adapter{db: db, path: path, catalog: filepath.Base(path), readOnly: readOnly}, nil
}

// openPool opens the pool and reads the file header, which is when SQLite
// finds out that the file is not a database.
func openPool(ctx context.Context, path string, readOnly bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", buildDSN(path, readOnly))
	if err != nil {
		return nil, classify(ctx, err, "open the database")
	}
	db.SetMaxOpenConns(query.MaxSessionsPerProfile + 2)
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		_ = db.Close()
		return nil, classify(ctx, err, "open the database")
	}
	return db, nil
}
