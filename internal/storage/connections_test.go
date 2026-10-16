package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbl/internal/connection"
)

func sampleCatalog(t *testing.T) connection.Catalog {
	t.Helper()
	var c connection.Catalog
	f, err := c.AddFolder("Work")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.AddProfile(connection.Profile{
		Name: "Local PG", FolderID: f.ID, Driver: connection.DriverPostgres,
		Host: "localhost", Port: 5432, Database: "app", Username: "postgres",
		SecretRef: "dbl/x", TLS: connection.TLSOptions{Mode: connection.TLSVerifyFull, RootCert: "ca.pem"},
		ConnectTimeoutSec: 20, Pool: connection.PoolOptions{MaxOpen: 4, MaxIdle: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConnectionsMissingFileIsEmptyAndWritable(t *testing.T) {
	st := NewConnectionsStore(t.TempDir())
	c, warnings, err := st.Load()
	if err != nil || len(c.Profiles) != 0 || len(c.Folders) != 0 || len(warnings) != 0 {
		t.Fatalf("Load: %+v %q %v", c, warnings, err)
	}
	if _, ro := st.ReadOnly(); ro {
		t.Error("a missing file made the store read-only")
	}
}

func TestConnectionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := sampleCatalog(t)
	if err := NewConnectionsStore(dir).Save(want); err != nil {
		t.Fatal(err)
	}
	got, warnings, err := NewConnectionsStore(dir).Load()
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Load: %v %q", err, warnings)
	}
	if len(got.Folders) != 1 || got.Folders[0] != want.Folders[0] {
		t.Errorf("folders: %+v, want %+v", got.Folders, want.Folders)
	}
	if len(got.Profiles) != 1 || got.Profiles[0] != want.Profiles[0] {
		t.Errorf("profiles: %+v, want %+v", got.Profiles, want.Profiles)
	}
}

func TestConnectionsFileShape(t *testing.T) {
	dir := t.TempDir()
	if err := NewConnectionsStore(dir).Save(sampleCatalog(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"] != float64(ConnectionsVersion) {
		t.Errorf("version = %v", raw["version"])
	}
	conn := raw["connections"].([]any)[0].(map[string]any)
	for _, key := range []string{"id", "name", "folderId", "driver", "host", "port", "database", "username", "secretRef", "tls", "connectTimeoutSec", "pool"} {
		if _, ok := conn[key]; !ok {
			t.Errorf("connections.json has no %q key: %v", key, conn)
		}
	}
	for key := range conn {
		if strings.Contains(strings.ToLower(key), "password") {
			t.Errorf("the file has a %q key", key)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestConnectionsEmptyCatalogWritesEmptyLists(t *testing.T) {
	dir := t.TempDir()
	if err := NewConnectionsStore(dir).Save(connection.Catalog{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "connections.json"))
	if !strings.Contains(string(data), `"folders": []`) || !strings.Contains(string(data), `"connections": []`) {
		t.Errorf("empty lists are not written as []:\n%s", data)
	}
}

func TestConnectionsSaveCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", ".dbl")
	if err := NewConnectionsStore(dir).Save(sampleCatalog(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "connections.json")); err != nil {
		t.Error(err)
	}
}

func TestConnectionsCorruptFileIsKeptAndStoreStaysWritable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connections.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewConnectionsStore(dir)
	c, _, err := st.Load()
	if !errors.Is(err, ErrConnectionsCorrupt) || !strings.Contains(err.Error(), "connections.json.bad") {
		t.Fatalf("Load error: %v", err)
	}
	if len(c.Profiles) != 0 {
		t.Error("a corrupt file gave profiles")
	}
	if kept, err := os.ReadFile(path + ".bad"); err != nil || string(kept) != "{broken" {
		t.Errorf("backup: %q %v", kept, err)
	}
	if err := st.Save(sampleCatalog(t)); err != nil {
		t.Errorf("Save after quarantine: %v", err)
	}
	if kept, _ := os.ReadFile(path + ".bad"); string(kept) != "{broken" {
		t.Error("saving destroyed the backup")
	}
}

func TestConnectionsEmptyFileIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewConnectionsStore(dir).Load(); !errors.Is(err, ErrConnectionsCorrupt) {
		t.Errorf("empty file: %v", err)
	}
}

func TestConnectionsNewerVersionIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connections.json")
	original := `{"version":99,"connections":[{"id":"x","future":true}]}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewConnectionsStore(dir)
	_, _, err := st.Load()
	if !errors.Is(err, ErrConnectionsVersion) {
		t.Fatalf("Load error: %v", err)
	}
	if reason, ro := st.ReadOnly(); !ro || reason == "" {
		t.Errorf("ReadOnly = %q, %v", reason, ro)
	}
	if err := st.Save(sampleCatalog(t)); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Save error: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != original {
		t.Errorf("the newer file was changed: %q", data)
	}
}

// A file that exists but cannot be read must not look like "no profiles":
// the first profile saved would replace all the others.
func TestConnectionsUnreadableFileMakesTheStoreReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connections.json")
	if err := os.Mkdir(path, 0o700); err != nil { // reading a directory fails
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewConnectionsStore(dir)
	c, _, err := st.Load()
	if !errors.Is(err, ErrConnectionsUnreadable) {
		t.Fatalf("Load error: %v", err)
	}
	if len(c.Profiles) != 0 {
		t.Error("an unreadable file gave profiles")
	}
	if _, ro := st.ReadOnly(); !ro {
		t.Fatal("the store is not read-only after a read failure")
	}
	if err := st.Save(sampleCatalog(t)); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Save error: %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Errorf("the unreadable path was replaced: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(path, "keep")); err != nil {
		t.Errorf("content of the unreadable path was lost: %v", err)
	}
}

func TestConnectionsLoadRepairsAndReportsWarnings(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":1,"folders":[{"id":"f","name":"Work"}],"connections":[
	  {"id":"a","name":"A","folderId":"gone","driver":"postgres","host":"h","port":5432,"database":"d","username":"u"},
	  {"id":"b","name":"Old","driver":"mysql","host":"h","port":3306,"database":"d","username":"u"}]}`
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, warnings, err := NewConnectionsStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "moved to the root") {
		t.Errorf("warnings: %q", warnings)
	}
	a, _ := c.Profile("a")
	if a.FolderID != "" || a.TLS.Mode != connection.TLSRequire || a.ConnectTimeoutSec != 10 || a.Pool.MaxOpen != 2 {
		t.Errorf("profile a was not normalized: %+v", a)
	}
	if b, ok := c.Profile("b"); !ok || b.Driver != connection.DriverMySQL {
		t.Errorf("the profile of an unsupported driver was lost: %+v", b)
	}
}

func TestConnectionsSaveFailureLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	// A file where the directory should be makes MkdirAll fail.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewConnectionsStore(filepath.Join(blocker, ".dbl"))
	if err := st.Save(sampleCatalog(t)); err == nil {
		t.Fatal("Save succeeded under a file")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*", "*")); len(left) != 0 {
		t.Errorf("files left behind: %v", left)
	}
}
