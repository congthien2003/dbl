// Package app holds the application state and workspace logic. It does not
// import any UI package.
package app

import (
	"sync"
	"time"

	"dbl/internal/storage"
)

// Writer persists one kind of value.
type Writer[T any] interface {
	Save(T) error
}

// SettingsWriter persists settings.
type SettingsWriter = Writer[storage.Settings]

// Saver coalesces writes: only the latest value scheduled within delay is
// written, on a background goroutine. A delay of zero writes as soon as
// the goroutine runs, which still coalesces values scheduled meanwhile.
type Saver[T any] struct {
	// OnSaved, when set before the first Schedule, is called on the writing
	// goroutine after each successful write.
	OnSaved func()

	w       Writer[T]
	delay   time.Duration
	onError func(error)

	writeMu sync.Mutex // serializes writes so an older value never lands last
	mu      sync.Mutex // guards pending and timer
	pending *T
	timer   *time.Timer
}

// SettingsSaver debounces settings writes.
type SettingsSaver = Saver[storage.Settings]

// NewSaver returns a saver writing through w after delay. Write errors go
// to onError, which may be nil.
func NewSaver[T any](w Writer[T], delay time.Duration, onError func(error)) *Saver[T] {
	return &Saver[T]{w: w, delay: delay, onError: onError}
}

// NewSettingsSaver returns a saver of settings.
func NewSettingsSaver(w SettingsWriter, delay time.Duration, onError func(error)) *SettingsSaver {
	return NewSaver(w, delay, onError)
}

// Schedule queues v to be written after the debounce delay, replacing any
// value still waiting.
func (sv *Saver[T]) Schedule(v T) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.pending = &v
	if sv.timer == nil {
		sv.timer = time.AfterFunc(sv.delay, sv.flush)
		return
	}
	sv.timer.Reset(sv.delay)
}

// Flush writes the waiting value now, if any, and returns once it is
// written.
func (sv *Saver[T]) Flush() {
	sv.mu.Lock()
	if sv.timer != nil {
		sv.timer.Stop()
	}
	sv.mu.Unlock()
	sv.flush()
}

func (sv *Saver[T]) flush() {
	sv.writeMu.Lock()
	defer sv.writeMu.Unlock()
	sv.mu.Lock()
	p := sv.pending
	sv.pending = nil
	sv.mu.Unlock()
	if p == nil {
		return
	}
	if err := sv.w.Save(*p); err != nil {
		if sv.onError != nil {
			sv.onError(err)
		}
		return
	}
	if sv.OnSaved != nil {
		sv.OnSaved()
	}
}
