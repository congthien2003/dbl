package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/storage"
	dblui "dbl/internal/ui"
)

func TestLoadStateMissingFileHasNoNotice(t *testing.T) {
	st := loadState(storage.NewSettingsStore(t.TempDir()))
	if st.Notice != "" || st.Settings != storage.DefaultSettings() {
		t.Errorf("notice %q, settings %+v", st.Notice, st.Settings)
	}
}

func TestLoadStateCorruptSettingsStartsWithNotice(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := loadState(storage.NewSettingsStore(dir))
	if st.Settings != storage.DefaultSettings() {
		t.Errorf("settings %+v, want defaults", st.Settings)
	}
	if !strings.Contains(st.Notice, "settings.json.bad") {
		t.Errorf("notice %q does not mention the backup", st.Notice)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.bad")); err != nil {
		t.Errorf("backup missing: %v", err)
	}
}

func TestLoadStateNewerVersionStartsWithNotice(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := loadState(storage.NewSettingsStore(dir))
	if !strings.Contains(st.Notice, "newer version") {
		t.Errorf("notice %q", st.Notice)
	}
}

func TestShellShowsApplicationName(t *testing.T) {
	st := loadState(storage.NewSettingsStore(t.TempDir()))
	tt := mygoui.NewTester(dblui.NewShell(st).View, 1280, 800)
	if !tt.HasText("SBDb") {
		t.Errorf("window shows %q", tt.Texts())
	}
}

func TestLoadConnectionsMissingFileIsEmptyAndWritable(t *testing.T) {
	st := &app.State{}
	loadConnections(st, storage.NewConnectionsStore(t.TempDir()))
	if len(st.Catalog.Profiles) != 0 || st.ConnectionsReadOnly != "" || st.Notice != "" {
		t.Errorf("catalog %+v readOnly %q notice %q", st.Catalog, st.ConnectionsReadOnly, st.Notice)
	}
}

func TestLoadConnectionsReadsSavedProfiles(t *testing.T) {
	dir := t.TempDir()
	var cat connection.Catalog
	if _, err := cat.AddProfile(connection.Profile{Name: "A", Driver: connection.DriverPostgres, Host: "h", Port: 5432, Database: "d", Username: "u"}); err != nil {
		t.Fatal(err)
	}
	if err := storage.NewConnectionsStore(dir).Save(cat); err != nil {
		t.Fatal(err)
	}
	st := &app.State{}
	loadConnections(st, storage.NewConnectionsStore(dir))
	if len(st.Catalog.Profiles) != 1 || st.Catalog.Profiles[0].Name != "A" || st.Notice != "" {
		t.Errorf("catalog %+v notice %q", st.Catalog, st.Notice)
	}
}

func TestLoadConnectionsCorruptFileStartsEmptyWithNotice(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &app.State{}
	loadConnections(st, storage.NewConnectionsStore(dir))
	if !strings.Contains(st.Notice, "connections.json.bad") || st.ConnectionsReadOnly != "" {
		t.Errorf("notice %q readOnly %q", st.Notice, st.ConnectionsReadOnly)
	}
	if _, err := os.Stat(filepath.Join(dir, "connections.json.bad")); err != nil {
		t.Errorf("backup missing: %v", err)
	}
}

func TestLoadConnectionsNewerVersionIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":99,"connections":[{"id":"x"}]}`
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &app.State{}
	loadConnections(st, storage.NewConnectionsStore(dir))
	if st.ConnectionsReadOnly == "" || !strings.Contains(st.Notice, "newer version") {
		t.Errorf("readOnly %q notice %q", st.ConnectionsReadOnly, st.Notice)
	}
	if _, err := st.AddFolder("F"); err == nil {
		t.Error("a folder could be added although the file must not be overwritten")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "connections.json")); string(got) != body {
		t.Errorf("the newer file was changed: %q", got)
	}
}

func TestLoadConnectionsUnreadableFileIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "connections.json"), 0o700); err != nil { // cannot be read as a file
		t.Fatal(err)
	}
	st := &app.State{}
	loadConnections(st, storage.NewConnectionsStore(dir))
	if st.ConnectionsReadOnly == "" || !strings.Contains(st.Notice, "could not be read") {
		t.Errorf("readOnly %q notice %q", st.ConnectionsReadOnly, st.Notice)
	}
}

func TestLoadConnectionsRepairsAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	body := `{"version":1,"connections":[{"id":"a","name":"A","folderId":"gone","driver":"postgres","host":"h","port":1,"database":"d","username":"u"}]}`
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &app.State{Notice: "earlier notice"}
	loadConnections(st, storage.NewConnectionsStore(dir))
	if !strings.Contains(st.Notice, "earlier notice") || !strings.Contains(st.Notice, "repaired") {
		t.Errorf("notice %q", st.Notice)
	}
	if len(st.Catalog.Profiles) != 1 || st.Catalog.Profiles[0].FolderID != "" {
		t.Errorf("catalog %+v", st.Catalog)
	}
}

// Starting the app reads local files only: no credential, no connection.
func TestStartupWithSavedProfilesTouchesNoCredentialAndNoNetwork(t *testing.T) {
	dir := t.TempDir()
	var cat connection.Catalog
	cat.AddProfile(connection.Profile{Name: "A", Driver: connection.DriverPostgres, Host: "h", Port: 5432, Database: "d", Username: "u", SecretRef: "dbl/x"})
	if err := storage.NewConnectionsStore(dir).Save(cat); err != nil {
		t.Fatal(err)
	}
	creds, connects := 0, 0
	st := &app.State{}
	loadConnections(st, storage.NewConnectionsStore(dir))
	st.Conns = connection.NewManager(&st.Catalog, countingCreds{&creds}, func(context.Context, connection.Profile, string) (database.Adapter, error) {
		connects++
		return nil, errors.New("must not be called")
	}, func(fn func()) { fn() })
	tt := mygoui.NewTester(dblui.NewShell(st).View, 1280, 800)
	tt.Frame()
	if !tt.HasText("A") {
		t.Errorf("window shows %q", tt.Texts())
	}
	if creds != 0 || connects != 0 {
		t.Errorf("startup made %d credential calls and %d connections", creds, connects)
	}
}

type countingCreds struct{ n *int }

func (c countingCreds) Get(string) (string, error) { *c.n++; return "", connection.ErrSecretNotFound }
func (c countingCreds) Set(string, string) error   { *c.n++; return nil }
func (c countingCreds) Delete(string) error        { *c.n++; return nil }

func TestConnectionsNoticeSaysWhetherABackupExists(t *testing.T) {
	kept := connectionsNotice(storage.ErrConnectionsCorrupt, false)
	if !strings.Contains(kept, "connections.json.bad") {
		t.Errorf("notice %q", kept)
	}
	notKept := connectionsNotice(storage.ErrConnectionsCorrupt, true)
	if strings.Contains(notKept, ".bad") || !strings.Contains(notKept, "could not be set aside") {
		t.Errorf("notice %q: no backup exists when the file could not be moved", notKept)
	}
}
