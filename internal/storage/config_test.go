package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	got, err := NewSettingsStore(dir).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != DefaultSettings() {
		t.Errorf("got %+v, want defaults", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("Load created files: %v", entries)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewSettingsStore(dir)
	want := Settings{Version: 1, Theme: ThemeDark, Window: WindowSettings{Width: 1400, Height: 900, SidebarWidth: 300}}
	if err := st.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := NewSettingsStore(dir).Load()
	if err != nil || got != want {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	win, _ := m["window"].(map[string]any)
	for _, k := range []string{"version", "theme", "window"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	for _, k := range []string{"width", "height", "sidebarWidth"} {
		if _, ok := win[k]; !ok {
			t.Errorf("missing window key %q", k)
		}
	}
}

func TestSaveCreatesDirAndLeavesNoTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", ".dbl")
	if err := NewSettingsStore(dir).Save(DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Errorf("directory holds %v, want only settings.json", entries)
	}
}

func TestSaveReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	st := NewSettingsStore(dir)
	first := DefaultSettings()
	second := DefaultSettings()
	second.Theme = ThemeLight
	for _, s := range []Settings{first, second} {
		if err := st.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Load()
	if err != nil || got.Theme != ThemeLight {
		t.Fatalf("Load = %+v, %v; want light theme", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("directory holds %v, want only settings.json", entries)
	}
}

func TestLoadMissingKeysUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "settings.json", `{"version":1}`)
	got, err := NewSettingsStore(dir).Load()
	if err != nil || got != DefaultSettings() {
		t.Fatalf("Load = %+v, %v; want defaults", got, err)
	}
}

func TestLoadCorruptFileIsBackedUp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "settings.json", `{not json`)
	st := NewSettingsStore(dir)
	got, err := st.Load()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
	if got != DefaultSettings() {
		t.Errorf("got %+v, want defaults", got)
	}
	bad, readErr := os.ReadFile(filepath.Join(dir, "settings.json.bad"))
	if readErr != nil || string(bad) != `{not json` {
		t.Errorf("backup = %q, %v", bad, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "settings.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("settings.json still exists: %v", statErr)
	}
	if err := st.Save(DefaultSettings()); err != nil {
		t.Errorf("Save after corrupt file: %v", err)
	}
}

func TestLoadNewerVersionIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	const content = `{"version":99,"theme":"dark"}`
	writeFile(t, dir, "settings.json", content)
	st := NewSettingsStore(dir)
	got, err := st.Load()
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
	if got != DefaultSettings() {
		t.Errorf("got %+v, want defaults", got)
	}
	if err := st.Save(DefaultSettings()); !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("Save err = %v, want ErrUnsupportedVersion", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(raw) != content {
		t.Errorf("file changed to %q", raw)
	}
}

func TestNormalizeClamps(t *testing.T) {
	d := DefaultSettings()
	tests := []struct {
		name string
		mod  func(*Settings)
		get  func(Settings) any
		want any
	}{
		{"width unset", func(s *Settings) { s.Window.Width = 0 }, func(s Settings) any { return s.Window.Width }, d.Window.Width},
		{"width negative", func(s *Settings) { s.Window.Width = -5 }, func(s Settings) any { return s.Window.Width }, d.Window.Width},
		{"width too small", func(s *Settings) { s.Window.Width = 1 }, func(s Settings) any { return s.Window.Width }, MinWindowWidth},
		{"width too large", func(s *Settings) { s.Window.Width = 100000 }, func(s Settings) any { return s.Window.Width }, MaxWindowDim},
		{"height too small", func(s *Settings) { s.Window.Height = 10 }, func(s Settings) any { return s.Window.Height }, MinWindowHeight},
		{"sidebar too small", func(s *Settings) { s.Window.SidebarWidth = 5 }, func(s Settings) any { return s.Window.SidebarWidth }, MinSidebarWidth},
		{"sidebar too large", func(s *Settings) { s.Window.SidebarWidth = 99999 }, func(s Settings) any { return s.Window.SidebarWidth }, MaxSidebarWidth},
		{"theme unknown", func(s *Settings) { s.Theme = "neon" }, func(s Settings) any { return s.Theme }, ThemeSystem},
		{"theme empty", func(s *Settings) { s.Theme = "" }, func(s Settings) any { return s.Theme }, ThemeSystem},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := d
			tt.mod(&s)
			if got := tt.get(s.Normalize()); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultDirIsHomeDotDbl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".dbl"); got != want {
		t.Errorf("DefaultDir = %q, want %q", got, want)
	}
}
