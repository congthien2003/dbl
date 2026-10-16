package database

// TableRef identifies a table or view (spec 6.2).
type TableRef struct {
	Catalog string
	Schema  string
	Name    string
}
