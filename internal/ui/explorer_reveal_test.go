package ui

import (
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

func TestExplorerDoesNotAnimateAtStartup(t *testing.T) {
	_, tt := newTestShell(t)
	tt.Frame()
	if tt.FramePending() {
		t.Error("the explorer asks for frames before anything was opened")
	}
}

func TestExplorerFadesInNewlyOpenedChildren(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.Frame()
	sh.explorer.state.Open.Add("scratch") // under the open Personal folder
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("opening an item did not start a fade")
	}
	if len(sh.explorer.born) == 0 {
		t.Error("no row was stamped as revealed")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("the explorer keeps asking for frames after the fade")
	}
	if len(sh.explorer.born) != 0 {
		t.Errorf("%d fade records left after the fade", len(sh.explorer.born))
	}
	if !tt.HasText("public") {
		t.Error("revealed rows are missing")
	}
}

func TestExplorerClosingDoesNotAnimate(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.Frame()
	sh.explorer.state.Open.Remove("local-pg")
	tt.Frame()
	// The outline's own arrow turns, which may ask for frames; no row fades.
	if len(sh.explorer.born) != 0 {
		t.Errorf("closing an item stamped %d rows to fade in", len(sh.explorer.born))
	}
}

func TestExplorerRevealSkippedWhenMotionIsReduced(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
	tt.Frame()
	sh.explorer.state.Open.Add("scratch")
	tt.Frame()
	if tt.FramePending() || len(sh.explorer.born) != 0 {
		t.Error("rows faded in although motion is reduced")
	}
}
