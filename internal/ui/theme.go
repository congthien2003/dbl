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
	GridLine   mygoui.Color // faint line between cells
}

// NewPalette derives the palette from t.
func NewPalette(t *mygoui.Theme) Palette {
	return Palette{
		Dirty:      t.Warning.Alpha(0.40).Over(t.Background),
		Error:      t.Danger.Alpha(0.40).Over(t.Background),
		Disabled:   t.TextMuted.Alpha(0.20).Over(t.Background),
		ActiveCell: t.Accent.Alpha(0.30).Over(t.Background),
		Null:       t.TextMuted,
		GridLine:   t.Border.Alpha(0.45).Over(t.Background),
	}
}

// explorerTheme is t with the colors of the explorer's chosen row and hovered
// row paler: the accent at 40% (60% less) over the surface for the chosen
// row, with the ordinary text on it, and the accent at 15% for the row under
// the pointer.
func explorerTheme(t *mygoui.Theme) mygoui.Theme {
	et := *t
	et.Accent = t.Accent.Alpha(0.40).Over(t.Surface)
	et.AccentText = t.Text
	et.SurfaceHover = t.Accent.Alpha(0.15).Over(t.Surface)
	return et
}

// gridTheme is t with the color of the grid's chosen row paler: the accent
// at 18% over the background, with the ordinary text on it. The chosen cell
// is drawn on top of it with Palette.ActiveCell and the full accent border.
func gridTheme(t *mygoui.Theme) mygoui.Theme {
	gt := *t
	gt.Accent = t.Accent.Alpha(0.18).Over(t.Background)
	gt.AccentText = t.Text
	return gt
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
