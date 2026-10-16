package ui

import (
	"math"
	"testing"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/storage"
)

func distance(a, b mygoui.Color) float64 {
	dr, dg, db := float64(a.R)-float64(b.R), float64(a.G)-float64(b.G), float64(a.B)-float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

func TestPaletteDistinguishableInBothThemes(t *testing.T) {
	const minDistance = 24
	for name, theme := range map[string]*mygoui.Theme{"light": mygoui.LightTheme(), "dark": mygoui.DarkTheme()} {
		p := NewPalette(theme)
		fills := map[string]mygoui.Color{"Dirty": p.Dirty, "Error": p.Error, "Disabled": p.Disabled, "ActiveCell": p.ActiveCell}
		for fname, c := range fills {
			if c.A != 255 {
				t.Errorf("%s %s is not opaque (alpha %d)", name, fname, c.A)
			}
			if d := distance(c, theme.Background); d < minDistance {
				t.Errorf("%s %s is only %.1f from the background", name, fname, d)
			}
			for oname, o := range fills {
				if fname < oname {
					if d := distance(c, o); d < minDistance {
						t.Errorf("%s %s and %s are only %.1f apart", name, fname, oname, d)
					}
				}
			}
		}
	}
}

func TestPaletteDiffersBetweenThemes(t *testing.T) {
	light, dark := NewPalette(mygoui.LightTheme()), NewPalette(mygoui.DarkTheme())
	if light.Dirty == dark.Dirty {
		t.Error("Dirty is the same color in Light and Dark")
	}
}

func TestExplorerThemeIsPalerAndLeavesTheOriginal(t *testing.T) {
	for name, theme := range map[string]*mygoui.Theme{"light": mygoui.LightTheme(), "dark": mygoui.DarkTheme()} {
		orig := *theme
		et := explorerTheme(theme)
		if *theme != orig {
			t.Errorf("%s: the theme given was changed", name)
		}
		if et.Accent.A != 255 || et.SurfaceHover.A != 255 {
			t.Errorf("%s: chosen or hover color is not opaque", name)
		}
		// Paler: nearer the surface than the accent is, by about 60%.
		full, pale := distance(theme.Accent, theme.Surface), distance(et.Accent, theme.Surface)
		if ratio := pale / full; ratio < 0.3 || ratio > 0.5 {
			t.Errorf("%s: the chosen color is %.0f%% of the accent's distance from the surface, want about 40%%", name, ratio*100)
		}
		if distance(et.SurfaceHover, theme.Surface) >= pale {
			t.Errorf("%s: hover is not paler than the chosen row", name)
		}
		if et.AccentText != theme.Text {
			t.Errorf("%s: text on the chosen row is not the ordinary text", name)
		}
	}
}

func TestGridThemeIsPalerAndLeavesTheOriginal(t *testing.T) {
	for name, theme := range map[string]*mygoui.Theme{"light": mygoui.LightTheme(), "dark": mygoui.DarkTheme()} {
		orig := *theme
		gt := gridTheme(theme)
		if *theme != orig {
			t.Errorf("%s: the theme given was changed", name)
		}
		if gt.Accent.A != 255 {
			t.Errorf("%s: chosen row color is not opaque", name)
		}
		full, pale := distance(theme.Accent, theme.Background), distance(gt.Accent, theme.Background)
		if ratio := pale / full; ratio < 0.1 || ratio > 0.3 {
			t.Errorf("%s: the chosen row is %.0f%% of the accent's distance from the background, want about 18%%", name, ratio*100)
		}
		if gt.AccentText != theme.Text {
			t.Errorf("%s: text on the chosen row is not the ordinary text", name)
		}
	}
}

func TestThemeSourceMapping(t *testing.T) {
	tests := []struct {
		in   storage.ThemePref
		want mygo.ThemeSource
	}{
		{storage.ThemeSystem, mygo.ThemeSystem},
		{storage.ThemeLight, mygo.ThemeLight},
		{storage.ThemeDark, mygo.ThemeDark},
		{"neon", mygo.ThemeSystem},
		{"", mygo.ThemeSystem},
	}
	for _, tt := range tests {
		if got := ThemeSource(tt.in); got != tt.want {
			t.Errorf("ThemeSource(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
