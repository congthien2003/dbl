// Package storage reads and writes the application's local files.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// SettingsVersion is the settings.json format this build reads and writes.
const SettingsVersion = 1

const (
	settingsFile = "settings.json"
	backupSuffix = ".bad"
)

// Bounds applied to values read from settings.json.
const (
	MinWindowWidth  = 800
	MinWindowHeight = 500
	MaxWindowDim    = 16384
	MinSidebarWidth = 160
	MaxSidebarWidth = 800
)

// Defaults used for missing settings.
const (
	defaultWindowWidth  = 1280
	defaultWindowHeight = 800
	defaultSidebarWidth = 260
)

// ThemePref is the theme the user chose.
type ThemePref string

// Theme preferences.
const (
	ThemeSystem ThemePref = "system"
	ThemeLight  ThemePref = "light"
	ThemeDark   ThemePref = "dark"
)

// ErrCorrupt means settings.json could not be parsed; the file was moved
// aside to settings.json.bad.
var ErrCorrupt = errors.New("settings file is corrupt")

// ErrUnsupportedVersion means settings.json was written by a newer
// version. It is never overwritten.
var ErrUnsupportedVersion = errors.New("settings file has an unsupported version")

// WindowSettings is the remembered window layout.
type WindowSettings struct {
	Width        int `json:"width"`
	Height       int `json:"height"`
	SidebarWidth int `json:"sidebarWidth"`
}

// Settings is the content of settings.json. Keys this version does not
// know are dropped when the file is read.
type Settings struct {
	Version int            `json:"version"`
	Theme   ThemePref      `json:"theme"`
	Window  WindowSettings `json:"window"`
}

// DefaultSettings returns the settings used when no file exists.
func DefaultSettings() Settings {
	return Settings{
		Version: SettingsVersion,
		Theme:   ThemeSystem,
		Window: WindowSettings{
			Width:        defaultWindowWidth,
			Height:       defaultWindowHeight,
			SidebarWidth: defaultSidebarWidth,
		},
	}
}

// Normalize fills settings that are missing (zero or negative) with their
// defaults and clamps the others into their valid range.
func (s Settings) Normalize() Settings {
	s.Version = SettingsVersion
	switch s.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		s.Theme = ThemeSystem
	}
	s.Window.Width = clampOrDefault(s.Window.Width, MinWindowWidth, MaxWindowDim, defaultWindowWidth)
	s.Window.Height = clampOrDefault(s.Window.Height, MinWindowHeight, MaxWindowDim, defaultWindowHeight)
	s.Window.SidebarWidth = clampOrDefault(s.Window.SidebarWidth, MinSidebarWidth, MaxSidebarWidth, defaultSidebarWidth)
	return s
}

func clampOrDefault(v, lo, hi, def int) int {
	switch {
	case v <= 0:
		return def
	case v < lo:
		return lo
	case v > hi:
		return hi
	}
	return v
}

// DefaultDir returns the directory holding the application's files,
// ~/.dbl.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".dbl"), nil
}

// SettingsStore reads and writes settings.json in one directory.
type SettingsStore struct {
	dir string

	mu       sync.Mutex
	readOnly bool // the file on disk came from a newer version
}

// NewSettingsStore returns a store for the settings file in dir.
func NewSettingsStore(dir string) *SettingsStore {
	return &SettingsStore{dir: dir}
}

func (st *SettingsStore) path() string { return filepath.Join(st.dir, settingsFile) }

// Load reads the settings. A missing file gives the defaults and no error.
// For a corrupt file it moves the file to settings.json.bad and returns
// the defaults with ErrCorrupt; for a newer version it returns the
// defaults with ErrUnsupportedVersion and later Save calls refuse to write.
func (st *SettingsStore) Load() (Settings, error) {
	data, err := os.ReadFile(st.path())
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return DefaultSettings(), fmt.Errorf("read settings: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return DefaultSettings(), st.quarantine(err)
	}
	if s.Version > SettingsVersion {
		st.mu.Lock()
		st.readOnly = true
		st.mu.Unlock()
		return DefaultSettings(), fmt.Errorf("%w: file is version %d, this build supports %d",
			ErrUnsupportedVersion, s.Version, SettingsVersion)
	}
	return s.Normalize(), nil
}

// quarantine moves the unreadable file aside so the next Save does not
// destroy it.
func (st *SettingsStore) quarantine(cause error) error {
	bad := st.path() + backupSuffix
	_ = os.Remove(bad)
	if err := os.Rename(st.path(), bad); err != nil {
		return fmt.Errorf("%w: %v (could not keep a backup: %v)", ErrCorrupt, cause, err)
	}
	return fmt.Errorf("%w: %v (kept as %s)", ErrCorrupt, cause, filepath.Base(bad))
}

// Save writes the settings atomically: a temporary file in the same
// directory is flushed and renamed over settings.json.
func (st *SettingsStore) Save(s Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.readOnly {
		return ErrUnsupportedVersion
	}
	data, err := json.MarshalIndent(s.Normalize(), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(st.dir, settingsFile, data)
}
