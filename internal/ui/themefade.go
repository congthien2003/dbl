package ui

import (
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

// themeFadeDuration is how long switching between Light and Dark takes.
const themeFadeDuration = 220 * time.Millisecond

// themeFader fades between themes. MyGo resets the frame's theme to the
// system's every pass, so the fader remembers the theme it showed last,
// mixes it toward the new target over themeFadeDuration and gives the mix
// to the frame with SetTheme. Every widget reads its colors from the
// frame's theme, so backgrounds, borders and text all change together.
type themeFader struct {
	started        bool
	fading         bool // the frame being built shows a mix of two themes
	from, to, show mygoui.Theme
	start          time.Time
}

// apply must run first in the view, before anything reads c.Theme().
func (f *themeFader) apply(c *mygoui.Context) {
	f.fading = false
	target := *c.Theme()
	now := c.Now()
	if !f.started {
		f.started = true
		f.from, f.to, f.show, f.start = target, target, target, now
		return
	}
	if target != f.to {
		f.from, f.to, f.start = f.show, target, now
		// Even when the theme changes at once, the colors of this frame
		// are not a hover: they must not fade.
		f.fading = true
		if c.Preferences().ReduceMotion {
			f.from = target
		}
	}
	p := float32(now.Sub(f.start)) / float32(themeFadeDuration)
	if p >= 1 || f.from == f.to {
		f.show = f.to
		return
	}
	f.show = lerpTheme(f.from, f.to, smoothstep(p))
	c.SetTheme(&f.show)
	f.fading = true
	c.AnimationFrame()
}

func smoothstep(p float32) float32 {
	p = max(0, min(p, 1))
	return p * p * (3 - 2*p)
}

// lerpTheme mixes the colors of a toward b by t (0 gives a, 1 gives b).
// Sizes, fonts and the Dark flag come from b.
func lerpTheme(a, b mygoui.Theme, t float32) mygoui.Theme {
	out := b
	out.Background = a.Background.Mix(b.Background, t)
	out.Surface = a.Surface.Mix(b.Surface, t)
	out.SurfaceHover = a.SurfaceHover.Mix(b.SurfaceHover, t)
	out.SurfacePressed = a.SurfacePressed.Mix(b.SurfacePressed, t)
	out.Border = a.Border.Mix(b.Border, t)
	out.Text = a.Text.Mix(b.Text, t)
	out.TextMuted = a.TextMuted.Mix(b.TextMuted, t)
	out.Accent = a.Accent.Mix(b.Accent, t)
	out.AccentHover = a.AccentHover.Mix(b.AccentHover, t)
	out.AccentPressed = a.AccentPressed.Mix(b.AccentPressed, t)
	out.AccentText = a.AccentText.Mix(b.AccentText, t)
	out.Danger = a.Danger.Mix(b.Danger, t)
	out.Warning = a.Warning.Mix(b.Warning, t)
	out.Success = a.Success.Mix(b.Success, t)
	out.Selection = a.Selection.Mix(b.Selection, t)
	out.Focus = a.Focus.Mix(b.Focus, t)
	out.Inverse = a.Inverse.Mix(b.Inverse, t)
	out.InverseText = a.InverseText.Mix(b.InverseText, t)
	out.Scrollbar = a.Scrollbar.Mix(b.Scrollbar, t)
	return out
}
