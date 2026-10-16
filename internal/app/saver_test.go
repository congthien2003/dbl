package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"dbl/internal/storage"
)

type fakeWriter struct {
	mu    sync.Mutex
	saved []storage.Settings
	err   error
	ch    chan storage.Settings
}

func newFakeWriter() *fakeWriter { return &fakeWriter{ch: make(chan storage.Settings, 16)} }

func (f *fakeWriter) Save(s storage.Settings) error {
	f.mu.Lock()
	f.saved = append(f.saved, s)
	err := f.err
	f.mu.Unlock()
	f.ch <- s
	return err
}

func (f *fakeWriter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func withSidebar(w int) storage.Settings {
	s := storage.DefaultSettings()
	s.Window.SidebarWidth = w
	return s
}

func TestScheduleCoalescesToLastValue(t *testing.T) {
	w := newFakeWriter()
	sv := NewSettingsSaver(w, 20*time.Millisecond, nil)
	sv.Schedule(withSidebar(200))
	sv.Schedule(withSidebar(250))
	sv.Schedule(withSidebar(300))
	select {
	case got := <-w.ch:
		if got.Window.SidebarWidth != 300 {
			t.Errorf("saved sidebar %d, want 300", got.Window.SidebarWidth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no write after the debounce delay")
	}
	time.Sleep(100 * time.Millisecond)
	if n := w.count(); n != 1 {
		t.Errorf("%d writes, want 1", n)
	}
}

func TestFlushWritesPendingImmediately(t *testing.T) {
	w := newFakeWriter()
	sv := NewSettingsSaver(w, 50*time.Millisecond, nil)
	sv.Schedule(withSidebar(333))
	sv.Flush()
	if n := w.count(); n != 1 {
		t.Fatalf("%d writes right after Flush, want 1", n)
	}
	time.Sleep(150 * time.Millisecond)
	if n := w.count(); n != 1 {
		t.Errorf("%d writes after the delay, want still 1", n)
	}
}

func TestFlushWithNothingPendingDoesNothing(t *testing.T) {
	w := newFakeWriter()
	sv := NewSettingsSaver(w, 10*time.Millisecond, nil)
	sv.Flush()
	if n := w.count(); n != 0 {
		t.Errorf("%d writes, want 0", n)
	}
}

func TestWriteErrorGoesToCallback(t *testing.T) {
	w := newFakeWriter()
	boom := errors.New("disk full")
	w.err = boom
	errs := make(chan error, 4)
	sv := NewSettingsSaver(w, 10*time.Millisecond, func(err error) { errs <- err })
	sv.Schedule(withSidebar(210))
	select {
	case err := <-errs:
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want %v", err, boom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onError not called")
	}
	sv.Schedule(withSidebar(220))
	select {
	case <-errs:
	case <-time.After(2 * time.Second):
		t.Fatal("saver stopped accepting values after an error")
	}
	if n := w.count(); n != 2 {
		t.Errorf("%d writes, want 2", n)
	}
}
