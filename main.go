// Command dbl is SBDb, a desktop database manager built with MyGo's native
// UI. It keeps connection profiles and opens PostgreSQL and SQLite sessions.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/database/postgres"
	"dbl/internal/database/sqlite"
	"dbl/internal/storage"
	dblui "dbl/internal/ui"
)

// processStart is when main's package initialized, for SBDB_TRACE_STARTUP.
var processStart = time.Now()

const (
	settingsSaveDelay = 400 * time.Millisecond
	// shutdownTimeout bounds how long quitting waits for sessions to close.
	shutdownTimeout = 2 * time.Second
)

// discardWriter stands in for the settings file when its folder is unknown.
type discardWriter struct{}

func (discardWriter) Save(storage.Settings) error { return nil }

// discardCatalog stands in for connections.json when its folder is unknown;
// the state is read-only then, so nothing is ever scheduled.
type discardCatalog struct{}

func (discardCatalog) Save(connection.Catalog) error { return nil }

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

// addNotice adds a message to the status-bar notice.
func addNotice(st *app.State, msg string) {
	if st.Notice != "" {
		st.Notice += " · "
	}
	st.Notice += msg
}

// connectionsNotice turns a connections.json problem into a status-bar
// message.
func connectionsNotice(err error, readOnly bool) string {
	switch {
	case errors.Is(err, storage.ErrConnectionsCorrupt) && readOnly:
		return "Connections file was unreadable and could not be set aside; connections cannot be changed"
	case errors.Is(err, storage.ErrConnectionsCorrupt):
		return "Connections file was unreadable; starting with no connections (backup kept as connections.json.bad)"
	case errors.Is(err, storage.ErrConnectionsVersion):
		return "Connections were written by a newer version; they are not shown and cannot be changed"
	case errors.Is(err, storage.ErrConnectionsUnreadable):
		return "Connections file could not be read; connections cannot be changed (" + err.Error() + ")"
	}
	return "Connections could not be loaded: " + err.Error()
}

// loadConnections reads connections.json into the state. A problem never
// stops the app. A file that cannot be understood makes the connections
// read-only so it is never overwritten. It reads no credential and makes no
// connection.
func loadConnections(st *app.State, store *storage.ConnectionsStore) {
	cat, warnings, err := store.Load()
	st.Catalog = cat
	reason, ro := store.ReadOnly()
	if ro {
		st.ConnectionsReadOnly = reason
	}
	if err != nil {
		addNotice(st, connectionsNotice(err, ro))
		log.Printf("connections: %v", err)
	}
	for _, w := range warnings {
		log.Printf("connections: %s", w)
	}
	if len(warnings) > 0 {
		addNotice(st, "Connections file was repaired; see the log")
	}
}

// connectDriver opens a session with the engine of the profile.
func connectDriver(ctx context.Context, p connection.Profile, password string) (database.Adapter, error) {
	switch p.Driver {
	case connection.DriverPostgres:
		return postgres.Connect(ctx, p, password)
	case connection.DriverSQLite:
		return sqlite.Connect(ctx, p, password)
	}
	return nil, &database.Error{Kind: database.KindUnsupported, Message: p.Driver.Label() + " connections are not supported yet"}
}

func main() {
	// DBL takes every connection setting from its profiles, never from the
	// libpq environment; this must run before any other goroutine starts.
	postgres.IgnoreLibpqEnvironment()

	var (
		st        *app.State
		writer    app.SettingsWriter             = discardWriter{}
		catWriter app.Writer[connection.Catalog] = discardCatalog{}
	)
	if dir, err := storage.DefaultDir(); err != nil {
		st = &app.State{Settings: storage.DefaultSettings(), Notice: "Home folder not found; settings and connections will not be saved"}
		st.ConnectionsReadOnly = "the home folder was not found"
	} else {
		store := storage.NewSettingsStore(dir)
		st = loadState(store)
		writer = store
		connStore := storage.NewConnectionsStore(dir)
		loadConnections(st, connStore)
		catWriter = connStore
	}

	// win is set once the window exists; work finishing on other goroutines
	// reaches the UI thread through it.
	var win atomic.Pointer[mygo.Window]
	post := func(fn func()) {
		if w := win.Load(); w != nil {
			w.Update(fn)
		}
	}

	saver := app.NewSettingsSaver(writer, settingsSaveDelay, func(err error) { log.Printf("save settings: %v", err) })
	catSaver := app.NewSaver(catWriter, 0, func(err error) {
		log.Printf("save connections: %v", err)
		post(func() { st.SetCatalogSaveError(err) })
	})
	catSaver.OnSaved = func() { post(st.ClearCatalogSaveError) }
	conns := connection.NewManager(&st.Catalog, storage.SystemCredentials{}, connectDriver, post)
	conns.OnCredentialIssue = st.ReportCredentialIssue
	st.Conns = conns
	st.Post = post
	queriesDir, queriesErr := storage.DefaultQueriesDir()
	if queriesErr != nil {
		addNotice(st, "The default queries folder is unavailable; choose an absolute SQL file location.")
	}
	st.SQLServices = app.NewSQLServices(storage.NewSQLStore(queriesDir), dblui.NewSQLDialogs(queriesDir))
	st.ScheduleCatalogSave = catSaver.Schedule
	st.SetThemeSource = func(p storage.ThemePref) { mygo.Theme.SetSource(dblui.ThemeSource(p)) }
	st.ScheduleSave = saver.Schedule
	mygo.Theme.SetSource(dblui.ThemeSource(st.Settings.Theme))

	// shutdown ends every session and writes what is waiting. Which of the
	// hooks below runs last when the window closes is not settled, so each
	// calls it; it is safe to call again.
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); _ = st.CloseSQL(ctx) }()
			// Manager state remains on the UI thread; SQL cleanup runs beside it.
			deadline, _ := ctx.Deadline()
			conns.CloseAll(max(time.Until(deadline), 0))
			select {
			case <-done:
			case <-ctx.Done():
			}
			catSaver.Flush()
			saver.Flush()
		})
	}

	shell := dblui.NewShell(st)
	quitApproved := false
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if quitApproved {
			return
		}
		if shell.RequestQuit() {
			quitApproved = true
			return
		}
		e.PreventDefault()
		if w := win.Load(); w != nil {
			w.Invalidate()
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { shutdown() })
	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:     "SBDb",
			Width:     st.Settings.Window.Width,
			Height:    st.Settings.Window.Height,
			MinWidth:  storage.MinWindowWidth,
			MinHeight: storage.MinWindowHeight,
			Content:   mygoui.View(shell.View),
		})
		shell.OnQuitConfirmed = func() {
			quitApproved = true
			mygo.App.Quit()
		}
		shell.SetWindow(w)
		w.OnResize(func() {
			width, height := w.Size()
			st.SetWindowSize(width, height)
		})
		w.OnClose(func(e *mygo.CloseEvent) {
			if !quitApproved && !shell.RequestQuit() {
				e.PreventDefault()
				w.Invalidate()
			}
		})
		w.OnClosed(shutdown)
		win.Store(w)
		if os.Getenv("SBDB_TRACE_STARTUP") != "" {
			w.OnReadyToShow(func() {
				fmt.Fprintf(os.Stderr, "startup: first frame ready %s after main started (Go runtime init not included)\n", time.Since(processStart))
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
