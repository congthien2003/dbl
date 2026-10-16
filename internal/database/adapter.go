// Package database holds the engine-independent models and errors shared by
// the application and UI, and the contract of a database adapter.
// Adapters live in sub-packages and never import a UI package.
package database

import "context"

// Adapter is an open session with a database. Later phases extend it with
// metadata, paging and execution; Phase 2.1 only connects and disconnects.
type Adapter interface {
	// Ping checks that the server still answers.
	Ping(ctx context.Context) error
	// Close releases every resource of the session. It is safe to call from
	// any goroutine, and more than once.
	Close() error
}
