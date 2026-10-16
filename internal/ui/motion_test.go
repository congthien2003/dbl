package ui

import (
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

// hover moves the pointer to the middle of the element showing text s and
// draws a frame.
func hover(t *testing.T, tt *mygoui.Tester, s string) {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no element shows %q", s)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
}

func TestButtonHoverFadesThenSettles(t *testing.T) {
	sh, tt := newTestShell(t)
	openTable(t, sh, tt, "users")
	hover(t, tt, "New SQL")
	if !tt.FramePending() {
		t.Error("hovering a button does not fade its color")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("the view keeps asking for frames after the hover fade")
	}
}

func TestTabHoverFadesThenSettles(t *testing.T) {
	sh, tt := newTestShell(t)
	openTable(t, sh, tt, "users")
	openTable(t, sh, tt, "active_users")
	hover(t, tt, "users")
	if !tt.FramePending() {
		t.Error("hovering a tab does not fade its color")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("the view keeps asking for frames after the tab fade")
	}
}

func TestHoverFadeSkippedWhileTheThemeFades(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.SetDark(false)
	tt.Frame()
	tt.SetDark(true)
	if !sh.fader.fading {
		t.Error("the fader does not report the fade")
	}
	tt.Advance(time.Second)
	tt.Frame()
	if sh.fader.fading {
		t.Error("the fader still reports a fade after it ended")
	}
	if tt.FramePending() {
		t.Error("frames pending after the theme fade")
	}
}

func TestConnectionStatusChangeSettles(t *testing.T) {
	e := newEnv(t)
	e.connect("Local PG")
	e.tt.Advance(time.Second)
	if e.tt.FramePending() {
		t.Error("the view keeps asking for frames after the status changed")
	}
}
