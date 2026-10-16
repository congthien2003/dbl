// Package database holds the engine-independent models shared by the
// application and UI. Phase 1 defines only TableRef; adapters come later.
package database

// TableRef identifies a table or view (spec 6.2).
type TableRef struct {
	Catalog string
	Schema  string
	Name    string
}
