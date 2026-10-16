package ui

import (
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

// hoverFade fades the background and border of an element when hover or
// press changes them. Nothing else about the element moves, so the frames
// stay cheap.
var hoverFade = mygoui.ElementTransition{Colors: true, Duration: 110 * time.Millisecond}

// statusFade cross-fades text that changes with a connection's status.
var statusFade = mygoui.ContentTransition{
	Animation: &mygoui.Animation{Duration: 150 * time.Millisecond},
}

// fade gives e hoverFade. While the theme fades, e is returned as it is:
// the theme changes every frame, and a color transition would restart
// toward it each time and trail the rest of the window.
func (sh *Shell) fade(e mygoui.Element) mygoui.Element {
	if sh.fader.fading {
		return e
	}
	return e.Transition(hoverFade)
}
