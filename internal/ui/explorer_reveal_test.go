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
	e := newEnv(t)
	personal := e.folderItem("Personal")
	e.sh.explorer.state.Open.Remove(personal)
	e.tt.Frame()
	if e.tt.HasText("Scratch") {
		t.Fatal("a closed folder still shows its connection")
	}
	e.sh.explorer.state.Open.Add(personal)
	e.tt.Frame()
	if !e.tt.FramePending() {
		t.Fatal("opening a folder did not start a fade")
	}
	if len(e.sh.explorer.born) == 0 {
		t.Error("no row was stamped as revealed")
	}
	e.tt.Advance(time.Second)
	if e.tt.FramePending() {
		t.Error("the explorer keeps asking for frames after the fade")
	}
	if len(e.sh.explorer.born) != 0 {
		t.Errorf("%d fade records left after the fade", len(e.sh.explorer.born))
	}
	if !e.tt.HasText("Scratch") {
		t.Error("revealed rows are missing")
	}
}

func TestExplorerClosingDoesNotAnimate(t *testing.T) {
	e := newEnv(t)
	e.sh.explorer.state.Open.Remove(e.folderItem("Work"))
	e.tt.Frame()
	// The outline's own arrow turns, which may ask for frames; no row fades.
	if len(e.sh.explorer.born) != 0 {
		t.Errorf("closing an item stamped %d rows to fade in", len(e.sh.explorer.born))
	}
}

func TestExplorerRevealSkippedWhenMotionIsReduced(t *testing.T) {
	e := newEnv(t)
	e.tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
	personal := e.folderItem("Personal")
	e.sh.explorer.state.Open.Remove(personal)
	e.tt.Frame()
	e.sh.explorer.state.Open.Add(personal)
	e.tt.Frame()
	if e.tt.FramePending() || len(e.sh.explorer.born) != 0 {
		t.Error("rows faded in although motion is reduced")
	}
}
