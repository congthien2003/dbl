package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLFileReplacementFailureKeepsOriginal(t *testing.T) {
	st := NewSQLStore(t.TempDir())
	path := filepath.Join(st.dir, "query.sql")
	if err := os.WriteFile(path, []byte("select 1"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := st.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	st.replace = func(string, string, bool) (bool, error) { return false, errors.New("simulated replacement failure") }
	if _, err := st.Write(SQLWriteRequest{Path: path, Data: []byte("select 2"), Expected: f.Revision}); err == nil {
		t.Fatal("expected replacement error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "select 1" {
		t.Fatal("original file was changed", string(data), err)
	}
	temps, err := filepath.Glob(filepath.Join(st.dir, ".sbdb-query-*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatal("temporary file leaked", temps, err)
	}
}
