// Package app holds the application state and workspace logic. It does not
// import any UI package.
package app

import (
	"sync"
	"time"

	"dbl/internal/storage"
)

// SettingsWriter persists settings.
type SettingsWriter interface {
	Save(storage.Settings) error
}

// SettingsSaver debounces settings writes: only the latest value scheduled
// within delay is written, on a background goroutine.
type SettingsSaver struct {
	w       SettingsWriter
	delay   time.Duration
	onError func(error)

	writeMu sync.Mutex // serializes writes so an older value never lands last
	mu      sync.Mutex // guards pending and timer
	pending *storage.Settings
	timer   *time.Timer
}

// NewSettingsSaver returns a saver writing through w after delay. Write
// errors go to onError, which may be nil.
func NewSettingsSaver(w SettingsWriter, delay time.Duration, onError func(error)) *SettingsSaver {
	return &SettingsSaver{w: w, delay: delay, onError: onError}
}

// Schedule queues s to be written after the debounce delay, replacing any
// value still waiting.
func (sv *SettingsSaver) Schedule(s storage.Settings) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.pending = &s
	if sv.timer == nil {
		sv.timer = time.AfterFunc(sv.delay, sv.flush)
		return
	}
	sv.timer.Reset(sv.delay)
}

// Flush writes the waiting value now, if any, and returns once it is
// written.
func (sv *SettingsSaver) Flush() {
	sv.mu.Lock()
	if sv.timer != nil {
		sv.timer.Stop()
	}
	sv.mu.Unlock()
	sv.flush()
}

func (sv *SettingsSaver) flush() {
	sv.writeMu.Lock()
	defer sv.writeMu.Unlock()
	sv.mu.Lock()
	p := sv.pending
	sv.pending = nil
	sv.mu.Unlock()
	if p == nil {
		return
	}
	if err := sv.w.Save(*p); err != nil && sv.onError != nil {
		sv.onError(err)
	}
}
