package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// newTestDB creates a SQLite file in a temporary folder, runs the DDL in it
// and returns its path.
func newTestDB(t *testing.T, ddl ...string) string {
	t.Helper()
	return newTestDBIn(t, t.TempDir(), "test.db", ddl...)
}

// newTestDBIn is newTestDB with the folder and the file name chosen.
func newTestDBIn(t *testing.T, dir, name string, ddl ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return path
}
