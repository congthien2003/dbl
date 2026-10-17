package app

import (
	"context"

	"dbl/internal/database"
	"dbl/internal/query"
)

type SQLExecState uint8

const (
	SQLNotOpened SQLExecState = iota
	SQLOpening
	SQLRunning
	SQLCanceling
	SQLFinished
)

type sqlRun struct {
	cancel context.CancelFunc
	epoch  uint64
}
type SQLData struct {
	Document  SQLDocument
	Execution *database.SQLExecution
	// Transaction belongs to the session, independently of result snapshots.
	Transaction            database.SQLTransaction
	State                  SQLExecState
	Err                    *database.Error
	Result, Page, Sequence int
	lease                  *query.Lease
	epoch                  uint64
	run                    *sqlRun
	sessionLife            context.Context
	endSession             context.CancelFunc
	Lost                   bool
	PendingInput           *database.SQLInput
}

func (d *SQLData) Busy() bool {
	return d != nil && (d.State == SQLOpening || d.State == SQLRunning || d.State == SQLCanceling)
}
func (d *SQLData) HasSession() bool { return d != nil && d.lease != nil && !d.Lost }
func (d *SQLData) release() {
	if d == nil {
		return
	}
	if d.endSession != nil {
		d.endSession()
		d.endSession = nil
		d.sessionLife = nil
	}
	d.Transaction = database.SQLSessionLost
	if d.run != nil {
		d.run.cancel()
		d.run = nil
	}
	if d.lease != nil {
		lease := d.lease
		d.lease = nil
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
			defer cancel()
			_ = lease.Close(ctx)
		}()
	}
	d.Execution = nil
}
