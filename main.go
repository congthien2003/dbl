// Command dbl is SBDb, a desktop database manager built with MyGo's native
// UI. Phase 1 shows the application shell on mock data.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/storage"
	dblui "dbl/internal/ui"
)

// processStart is when main's package initialized, for SBDB_TRACE_STARTUP.
var processStart = time.Now()

const settingsSaveDelay = 400 * time.Millisecond

// discardWriter stands in for the settings file when its folder is unknown.
type discardWriter struct{}

func (discardWriter) Save(storage.Settings) error { return nil }

// noticeFor turns a settings error into the status-bar message.
func noticeFor(err error) string {
	switch {
	case errors.Is(err, storage.ErrCorrupt):
		return "Settings file was unreadable; using defaults (backup kept as settings.json.bad)"
	case errors.Is(err, storage.ErrUnsupportedVersion):
		return "Settings were written by a newer version; using defaults and not saving"
	}
	return "Settings could not be read: " + err.Error()
}

// loadState reads the settings and returns the starting state. A settings
// problem never stops the app: it starts with defaults and a notice.
func loadState(store *storage.SettingsStore) *app.State {
	st := &app.State{}
	settings, err := store.Load()
	st.Settings = settings
	if err != nil {
		st.Notice = noticeFor(err)
		log.Printf("settings: %v", err)
	}
	return st
}

func main() {
	var (
		st     *app.State
		writer app.SettingsWriter = discardWriter{}
	)
	if dir, err := storage.DefaultDir(); err != nil {
		st = &app.State{Settings: storage.DefaultSettings(), Notice: "Home folder not found; settings will not be saved"}
	} else {
		store := storage.NewSettingsStore(dir)
		st = loadState(store)
		writer = store
	}
	saver := app.NewSettingsSaver(writer, settingsSaveDelay, func(err error) { log.Printf("save settings: %v", err) })
	st.SetThemeSource = func(p storage.ThemePref) { mygo.Theme.SetSource(dblui.ThemeSource(p)) }
	st.ScheduleSave = saver.Schedule
	mygo.Theme.SetSource(dblui.ThemeSource(st.Settings.Theme))

	shell := dblui.NewShell(st)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { saver.Flush() })
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "SBDb",
			Width:     st.Settings.Window.Width,
			Height:    st.Settings.Window.Height,
			MinWidth:  storage.MinWindowWidth,
			MinHeight: storage.MinWindowHeight,
			Content:   mygoui.View(shell.View),
		})
		shell.OnQuitConfirmed = func() {
			saver.Flush()
			win.Destroy()
		}
		win.OnResize(func() {
			w, h := win.Size()
			st.SetWindowSize(w, h)
		})
		win.OnClose(func(e *mygo.CloseEvent) {
			if !st.RequestQuit() {
				e.PreventDefault()
				win.Invalidate()
			}
		})
		win.OnClosed(saver.Flush)
		if os.Getenv("SBDB_TRACE_STARTUP") != "" {
			win.OnReadyToShow(func() {
				fmt.Fprintf(os.Stderr, "startup: first frame ready %s after main started (Go runtime init not included)\n", time.Since(processStart))
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
