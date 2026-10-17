package database

import (
	"context"
	"time"

	"dbl/internal/grid"
)

const (
	SQLMaxInputBytes  = 8 << 20
	SQLPageSize       = 100
	SQLSnapshotRows   = 1000
	SQLSnapshotBytes  = 16 << 20
	SQLReadRows       = 100000
	SQLReadBytes      = 64 << 20
	SQLMaxResults     = 32
	SQLMaxColumns     = 4096
	SQLMetadataBytes  = 1 << 20
	SQLMaxNotices     = 32
	SQLNoticeBytes    = 4 << 10
	SQLMessageBytes   = 8 << 20
	SQLTimeout        = 60 * time.Second
	SQLCleanupTimeout = 2 * time.Second
)

// SQLSpan uses half-open UTF-8 byte offsets, like the native editor.
type SQLSpan struct{ Start, End int }
type SQLInput struct {
	Text       string
	Cursor     int
	Selections []SQLSpan
}
type SQLSelection struct {
	SQL  string
	Span SQLSpan
}

// SQLSessionFactory opens independent sessions, without reserving table-pool slots.
type SQLSessionFactory interface {
	OpenSQLSession(context.Context) (SQLSession, error)
}

// SQLSession serializes database work. Close may race Execute and must be bounded.
type SQLSession interface {
	Select(SQLInput) (SQLSelection, error)
	Execute(context.Context, string) (*SQLExecution, error)
	Close(context.Context) error
}

type SQLTransaction uint8

const (
	SQLIdle SQLTransaction = iota
	SQLInTransaction
	SQLTransactionFailed
	SQLSessionLost
)

func (s SQLTransaction) String() string {
	switch s {
	case SQLInTransaction:
		return "In transaction"
	case SQLTransactionFailed:
		return "Transaction failed"
	case SQLSessionLost:
		return "Session lost"
	default:
		return "Idle"
	}
}

type SQLStop uint8

const (
	SQLCompleted SQLStop = iota
	SQLFailed
	SQLCanceled
	SQLTimedOut
	SQLLimitReached
)

func (s SQLStop) String() string {
	switch s {
	case SQLFailed:
		return "Failed"
	case SQLCanceled:
		return "Canceled"
	case SQLTimedOut:
		return "Timed out"
	case SQLLimitReached:
		return "Stopped at result limit"
	default:
		return "Completed"
	}
}

type SQLColumn struct {
	Name     string
	TypeOID  uint32
	TypeName string
	Kind     grid.Kind
}
type SQLResult struct {
	Columns                                     []SQLColumn
	Rows                                        [][]grid.Value
	CommandTag                                  string
	RowsAffected                                int64
	HasRowsAffected, CommandComplete, Truncated bool
}

// SQLExecution retains evidence even when Execute also returns an error.
// Ready means ReadyForQuery was received; it does not mean a transaction committed.
type SQLExecution struct {
	Results                 []SQLResult
	Notices                 []string
	DroppedNotices          int
	Stop                    SQLStop
	Transaction             SQLTransaction
	Ready, OutcomeUncertain bool
	Elapsed                 time.Duration
	Err                     *Error
}
