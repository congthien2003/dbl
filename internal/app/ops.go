package app

import (
	"context"
	"errors"
	"time"

	"dbl/internal/database"
)

// Timeouts of database work. A profile has no query timeout yet, so these
// are fixed; the user can always cancel by closing the tab or reloading.
const (
	MetadataTimeout = 30 * time.Second
	PageTimeout     = 30 * time.Second
	// SaveTimeout bounds a Save. It can be canceled until COMMIT is sent.
	SaveTimeout = 60 * time.Second
)

// OpState is the state of a piece of data that is read from a database.
type OpState uint8

// Operation states. Loaded data stays visible while a new read runs or
// fails: the state describes the read, not whether there is data.
const (
	OpIdle OpState = iota
	OpLoading
	OpReady
	OpFailed
)

// op is one run of database work. Its context ends when the op is
// superseded, canceled, times out or the session of its connection ends.
// An op reports its result only while it is still the current op of its
// slot (the completion gate of the design): a result of an older op, of a
// closed tab, of another session or of a canceled run is dropped.
type op struct {
	ctx    context.Context
	cancel context.CancelFunc
	epoch  uint64 // the session the op started on
}

// opSlot holds the current op of one kind of work. Starting a new op
// cancels the previous one, which is how a request that is superseded by
// a newer one (a filter typed again, a page turned) stops.
type opSlot struct{ cur *op }

// begin cancels the current op and starts a new one on the session of the
// connection. It returns nil when there is no session.
func (sl *opSlot) begin(s *State, connectionID string, timeout time.Duration) *op {
	sl.stop()
	epoch := s.Conns.Epoch(connectionID)
	if epoch == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.Conns.SessionContext(connectionID), timeout)
	sl.cur = &op{ctx: ctx, cancel: cancel, epoch: epoch}
	return sl.cur
}

// stop cancels the current op.
func (sl *opSlot) stop() {
	if sl.cur != nil {
		sl.cur.cancel()
		sl.cur = nil
	}
}

// release ends o without asking whether its result may be applied. A Save
// uses it: the result of a write is applied even after the op's context ended,
// because the write may have been committed.
func (sl *opSlot) release(o *op) {
	if sl.cur == o {
		sl.cur = nil
	}
	o.cancel()
}

// finish reports whether o may apply its result: it is the current op of the
// slot, its context is alive, and the session it started on is still the
// session of the connection. It always releases o.
func (sl *opSlot) finish(s *State, connectionID string, o *op) bool {
	ok := sl.cur == o && o.ctx.Err() == nil && s.Conns.Epoch(connectionID) == o.epoch
	if sl.cur == o {
		sl.cur = nil
	}
	o.cancel()
	return ok
}

// post runs fn on the UI thread, through the manager's poster.
func (s *State) post(fn func()) { s.Conns.Post(fn) }

// noSession is the error of work that needs a connection that is not
// open.
func noSession() *database.Error {
	return &database.Error{
		Kind:    database.KindConnection,
		Message: "The connection is not open.",
		Hint:    "Connect from the explorer, then reload.",
	}
}

// asDBError turns any error into a *database.Error.
func asDBError(err error) *database.Error {
	var de *database.Error
	if errors.As(err, &de) {
		return de
	}
	return &database.Error{Kind: database.KindConnection, Message: err.Error(), Err: err}
}
