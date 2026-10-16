// Package ui builds SBDb's interface with MyGo's native UI. Importers alias
// it (for example dblui) because MyGo's own package is also named ui.
package ui

import (
	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/storage"
)

// Palette holds the semantic colors SBDb adds to MyGo's theme. Every color
// is opaque (composited over the theme background) and derived from the
// theme in use, so the same names work in Light and Dark.
type Palette struct {
	Dirty      mygoui.Color // fill of a cell with an unsaved edit
	Error      mygoui.Color // fill of something invalid
	Disabled   mygoui.Color // fill of a disabled surface
	ActiveCell mygoui.Color // fill of the selected cell
	Null       mygoui.Color // text color of NULL and empty values
}

// NewPalette derives the palette from t.
func NewPalette(t *mygoui.Theme) Palette {
	return Palette{
		Dirty:      t.Warning.Alpha(0.40).Over(t.Background),
		Error:      t.Danger.Alpha(0.40).Over(t.Background),
		Disabled:   t.TextMuted.Alpha(0.20).Over(t.Background),
		ActiveCell: t.Accent.Alpha(0.30).Over(t.Background),
		Null:       t.TextMuted,
	}
}

// ThemeSource maps the saved preference to MyGo's theme source. Unknown
// values follow the system.
func ThemeSource(p storage.ThemePref) mygo.ThemeSource {
	switch p {
	case storage.ThemeLight:
		return mygo.ThemeLight
	case storage.ThemeDark:
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}
