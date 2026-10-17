package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dbl/internal/connection"
	"dbl/internal/database"
)

func profileFor(path string) connection.Profile {
	return connection.Profile{Name: "t", Driver: connection.DriverSQLite, SQLitePath: path, ConnectTimeoutSec: 5}
}

func connect(t *testing.T, p connection.Profile) *Adapter {
	t.Helper()
	ad, err := Connect(context.Background(), p, "")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { ad.Close() })
	return ad.(*Adapter)
}

func count(t *testing.T, a *Adapter, query string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConnectOpensExistingFile(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER PRIMARY KEY)", "INSERT INTO t VALUES (1), (2)")
	a := connect(t, profileFor(path))
	if n := count(t, a, "SELECT count(*) FROM t"); n != 2 {
		t.Errorf("rows %d", n)
	}
	if a.readOnly {
		t.Error("a writable file opened read-only")
	}
	if err := a.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestConnectRefusesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	_, err := Connect(context.Background(), profileFor(path), "")
	if k := database.KindOf(err); k != database.KindFileAccess && k != database.KindConnection {
		t.Errorf("error %v (kind %v)", err, k)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("Connect created the file")
	}
}

func TestConnectNotASQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("this is plain text, long enough to be a bad header ........................................"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Connect(context.Background(), profileFor(path), "")
	var de *database.Error
	if err == nil || database.KindOf(err) != database.KindConnection {
		t.Fatalf("error %v", err)
	}
	de = err.(*database.Error)
	if de.Message != "Not a SQLite database file" {
		t.Errorf("message %q", de.Message)
	}
}

func TestConnectEmptyFileOpensAsEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := connect(t, profileFor(path))
	if n := count(t, a, "SELECT count(*) FROM sqlite_master"); n != 0 {
		t.Errorf("%d objects", n)
	}
}

func TestConnectUnicodeAndSpecialPaths(t *testing.T) {
	name := "dữ liệu #1 100%41"
	if runtime.GOOS != "windows" {
		name += " ?" // Windows file names cannot hold '?'
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := newTestDBIn(t, dir, "tệp #2%.db", "CREATE TABLE special (id INTEGER)", "INSERT INTO special VALUES (7)")
	a := connect(t, profileFor(path))
	if n := count(t, a, "SELECT id FROM special"); n != 7 {
		t.Errorf("read %d from the wrong file", n)
	}
	// None of the escaped names became a new file next to it.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("folder holds %d entries", len(entries))
	}
}

func TestConnectReadOnlyBlocksWrites(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER)")
	p := profileFor(path)
	p.ReadOnly = true
	a := connect(t, p)
	if !a.readOnly {
		t.Error("adapter is not marked read-only")
	}
	if _, err := a.db.Exec("INSERT INTO t VALUES (1)"); err == nil {
		t.Error("a read-only connection wrote")
	}
}

func TestConnectLockedFileReturnsBusyError(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER)", "INSERT INTO t VALUES (1)")
	other, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}

	p := profileFor(path)
	p.ConnectTimeoutSec = 1
	start := time.Now()
	ad, err := Connect(context.Background(), p, "")
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("Connect waited %v on a locked file", elapsed)
	}
	if err == nil {
		ad.Close()
		t.Fatal("Connect succeeded on a locked file")
	}
	if k := database.KindOf(err); k != database.KindConnection && k != database.KindTimeout {
		t.Errorf("error %v (kind %v)", err, k)
	}
}

func TestCloseReleasesFile(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER)")
	ad, err := Connect(context.Background(), profileFor(path), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ad.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ad.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Errorf("the file is still held: %v", err)
	}
}

func TestConnectHonorsCanceledContext(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Connect(ctx, profileFor(path), "")
	if database.KindOf(err) != database.KindCanceled {
		t.Errorf("error %v (kind %v)", err, database.KindOf(err))
	}
}

func TestBuildDSNEscapesThePath(t *testing.T) {
	dsn := buildDSN("dir/a b#c%d?e.db", false)
	if !strings.HasPrefix(dsn, "file:dir/a b%23c%25d%3Fe.db?") {
		t.Errorf("DSN %q does not escape the path", dsn)
	}
	if !strings.Contains(dsn, "mode=rw") || strings.Contains(dsn, "rwc") {
		t.Errorf("DSN %q must open with mode=rw", dsn)
	}
	if ro := buildDSN("x.db", true); !strings.Contains(ro, "mode=ro") {
		t.Errorf("DSN %q must open with mode=ro", ro)
	}
}
