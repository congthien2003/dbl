package ui

import (
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

func TestLerpThemeEndpointsAndMiddle(t *testing.T) {
	light, dark := *mygoui.LightTheme(), *mygoui.DarkTheme()
	if got := lerpTheme(light, dark, 0); got.Background != light.Background || got.Text != light.Text {
		t.Errorf("t=0 gave %+v", got.Background)
	}
	if got := lerpTheme(light, dark, 1); got.Background != dark.Background || got.Text != dark.Text || !got.Dark {
		t.Errorf("t=1 gave %+v", got.Background)
	}
	mid := lerpTheme(light, dark, 0.5)
	if !(mid.Background.R < light.Background.R && mid.Background.R > dark.Background.R) {
		t.Errorf("middle background R=%d, want between %d and %d", mid.Background.R, dark.Background.R, light.Background.R)
	}
	if !(mid.Text.R > light.Text.R && mid.Text.R < dark.Text.R) {
		t.Errorf("middle text R=%d, want between %d and %d", mid.Text.R, light.Text.R, dark.Text.R)
	}
}

func TestSmoothstep(t *testing.T) {
	if smoothstep(-1) != 0 || smoothstep(0) != 0 || smoothstep(1) != 1 || smoothstep(2) != 1 {
		t.Error("smoothstep does not clamp to 0..1")
	}
	if got := smoothstep(0.5); got != 0.5 {
		t.Errorf("smoothstep(0.5) = %v, want 0.5", got)
	}
}

func TestThemeFadesWhenTheSystemThemeChanges(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.SetDark(false)
	tt.Frame()
	light, dark := mygoui.LightTheme(), mygoui.DarkTheme()
	if sh.fader.show.Background != light.Background {
		t.Fatalf("starts with background %+v, want the light one", sh.fader.show.Background)
	}
	tt.SetDark(true)
	if !tt.FramePending() {
		t.Error("no frames requested while the theme fades")
	}
	if got := sh.fader.show.Background; got != light.Background {
		t.Errorf("at the start of the fade the background is %+v, want still light", got)
	}
	tt.Advance(themeFadeDuration / 2)
	mid := sh.fader.show.Background
	if !(mid.R < light.Background.R && mid.R > dark.Background.R) {
		t.Errorf("half way the background R=%d, want between %d and %d", mid.R, dark.Background.R, light.Background.R)
	}
	tt.Advance(time.Second)
	if got := sh.fader.show.Background; got != dark.Background {
		t.Errorf("after the fade the background is %+v, want dark", got)
	}
	if tt.FramePending() {
		t.Error("the view keeps asking for frames after the fade")
	}
}

func TestThemeDoesNotFadeWhenMotionIsReduced(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.SetDark(false)
	tt.Frame()
	tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
	tt.SetDark(true)
	if got := sh.fader.show.Background; got != mygoui.DarkTheme().Background {
		t.Errorf("with reduced motion the background is %+v, want dark at once", got)
	}
	if tt.FramePending() {
		t.Error("frames requested although motion is reduced")
	}
}

func TestThemeSwitchDuringFadeContinuesFromWhatIsShown(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.SetDark(false)
	tt.Frame()
	tt.SetDark(true)
	tt.Advance(themeFadeDuration / 2)
	shown := sh.fader.show.Background
	tt.SetDark(false)
	if got := sh.fader.show.Background; got != shown {
		t.Errorf("reversing jumped from %+v to %+v", shown, got)
	}
	tt.Advance(time.Second)
	if got := sh.fader.show.Background; got != mygoui.LightTheme().Background {
		t.Errorf("after reversing the background is %+v, want light", got)
	}
}
