// Package database holds the engine-independent models and errors shared by
// the application and UI, and the contract of a database adapter.
// Adapters live in sub-packages and never import a UI package.
package database

import "context"

// Adapter is an open session with a database: it is what a connection
// attempt returns.
type Adapter interface {
	// Ping checks that the server still answers.
	Ping(ctx context.Context) error
	// Close releases every resource of the session. It is safe to call from
	// any goroutine, and more than once.
	Close() error
}

// SQLAdapter is the part of a relational session that reads metadata and
// data (spec 6.3). Every method honors ctx, runs on the caller's goroutine
// and keeps nothing of the result: rows are read fully and released before
// it returns. Errors are *Error values; a canceled ctx gives KindCanceled
// and a deadline KindTimeout.
//
// Free-form SQL uses the separate SQLSessionFactory capability.
type SQLAdapter interface {
	Adapter
	// ListSchemas lists the schemas of the connected database that the
	// explorer shows.
	ListSchemas(ctx context.Context) ([]Schema, error)
	// ListTables lists the tables and views of a schema.
	ListTables(ctx context.Context, schema string) ([]Table, error)
	// DescribeTable reads the metadata of a table or view. The columns are
	// required: when they cannot be read the call fails. Every other
	// section reports its own failure in its Section state.
	DescribeTable(ctx context.Context, ref TableRef) (*TableMetadata, error)
	// FetchPage reads one page of a table (see PageRequest).
	FetchPage(ctx context.Context, req PageRequest) (*PageResult, error)
	// SaveChanges writes a ChangeSet in one transaction (spec FR-005). It
	// validates the changes first and writes nothing when they are not
	// valid. Each row is found by its original identity and written only if
	// the columns being changed still hold the values the user saw; a row
	// that matches nothing is a conflict and one that matches more than one
	// row is unsafe, and either rolls everything back. Canceling ctx stops
	// the Save only until COMMIT is sent. A nil result means the transaction
	// is committed; any error is a *SaveError saying whether the outcome is
	// known. A write is never retried.
	SaveChanges(ctx context.Context, req SaveRequest) error
}
