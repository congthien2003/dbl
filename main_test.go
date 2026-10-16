package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mygoui "github.com/egoist/mygo/ui"

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
