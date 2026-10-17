package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLFileRoundTrip(t *testing.T) {
	st := NewSQLStore(t.TempDir())
	for _, data := range [][]byte{[]byte("select 'tiếng Việt';\r\n"), append([]byte{0xef, 0xbb, 0xbf}, []byte("select 1;\r\nselect 2;\n")...)} {
		path := filepath.Join(st.dir, "query.sql")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		f, err := st.Read(path)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := EncodeSQL(f, f.Text)
		if err != nil || !bytes.Equal(encoded, data) {
			t.Fatal("line endings/BOM changed", err)
		}
		out, err := st.Write(SQLWriteRequest{Path: path, Data: encoded, Expected: f.Revision})
		if err != nil || !bytes.Equal(out.Raw, data) {
			t.Fatal(out, err)
		}
	}
}
func TestSQLFileExternalChangeConflict(t *testing.T) {
	st := NewSQLStore(t.TempDir())
	path := filepath.Join(st.dir, "x.sql")
	_ = os.WriteFile(path, []byte("select 1"), 0600)
	f, err := st.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte("select 2"), 0600)
	_, err = st.Write(SQLWriteRequest{Path: path, Data: []byte("select 3"), Expected: f.Revision})
	var conflict *FileConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "select 2" {
		t.Fatal("external edit overwritten")
	}
}
func TestSQLFileBounds(t *testing.T) {
	st := NewSQLStore(t.TempDir())
	for _, data := range [][]byte{[]byte{0xff}, []byte("x\x00"), bytes.Repeat([]byte("x"), SQLFileMaxBytes+1)} {
		path := filepath.Join(st.dir, "bad.sql")
		_ = os.WriteFile(path, data, 0600)
		if _, err := st.Read(path); err == nil {
			t.Fatal("invalid/oversize file accepted")
		}
	}
	if _, err := st.Inspect("../escape.sql"); err == nil {
		t.Fatal("relative traversal accepted")
	}
}
func TestSQLFileIdentityAliases(t *testing.T) {
	st := NewSQLStore(t.TempDir())
	a := filepath.Join(st.dir, "a.sql")
	b := filepath.Join(st.dir, "b.sql")
	_ = os.WriteFile(a, []byte("select 1"), 0600)
	if err := os.Link(a, b); err != nil {
		t.Skip(err)
	}
	x, err := st.Inspect(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := st.Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if !x.Identity.Same(y.Identity) {
		t.Fatal("hardlink aliases not recognized")
	}
}
