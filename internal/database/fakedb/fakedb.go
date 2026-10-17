// Package fakedb is an in-memory database.SQLAdapter for tests. It plans
// and pages like a real adapter (it uses database.PlanQuery and Validate) but
// keeps its rows in memory. It is imported by tests only.
package fakedb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// Table is a table of the fake database.
type Table struct {
	Meta *database.TableMetadata
	Rows [][]grid.Value
}

// Adapter is the fake session.
type Adapter struct {
	// Hook, when set, runs at the start of every call with the call's name
	// ("ListSchemas", "ListTables", "DescribeTable", "FetchPage",
	// "SaveChanges"). A non-nil
	// result is returned as the call's error. It may block until ctx ends.
	Hook func(ctx context.Context, call string) error
	// UnknownCommit makes SaveChanges write the changes and then report that
	// the result of COMMIT is not known, as a lost connection would.
	UnknownCommit bool

	mu       sync.Mutex
	schemas  []database.Schema
	tables   map[string][]database.Table
	data     map[string]*Table
	calls    []string
	closed   bool
	reqs     []database.PageRequest
	saves    []database.SaveRequest
	sqlQueue []*SQLSession
}

// New returns an adapter with no schemas.
func New() *Adapter {
	return &Adapter{tables: map[string][]database.Table{}, data: map[string]*Table{}}
}

var _ database.SQLAdapter = (*Adapter)(nil)

// AddTable adds a table (or view) to a schema, creating the schema.
func (a *Adapter) AddTable(schema string, t *Table) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found := false
	for _, s := range a.schemas {
		found = found || s.Name == schema
	}
	if !found {
		a.schemas = append(a.schemas, database.Schema{Name: schema})
	}
	ref := t.Meta.Ref
	ref.Schema = schema
	t.Meta.Ref = ref
	a.tables[schema] = append(a.tables[schema], database.Table{Ref: ref, Kind: t.Meta.Kind, Subtype: t.Meta.Subtype})
	a.data[schema+"."+ref.Name] = t
}

// Table returns a table of the fake database, to change it between calls.
func (a *Adapter) Table(schema, name string) *Table {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.data[schema+"."+name]
}

// Calls returns the names of the calls made so far.
func (a *Adapter) Calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.calls...)
}

// Requests returns the page requests received so far.
func (a *Adapter) Requests() []database.PageRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]database.PageRequest(nil), a.reqs...)
}

// Saves returns the save requests received so far.
func (a *Adapter) Saves() []database.SaveRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]database.SaveRequest(nil), a.saves...)
}

// IsClosed reports whether Close was called.
func (a *Adapter) IsClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

func (a *Adapter) enter(ctx context.Context, call string) error {
	a.mu.Lock()
	a.calls = append(a.calls, call)
	hook := a.Hook
	a.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, call); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return &database.Error{Kind: database.KindCanceled, Message: "canceled", Err: err}
	}
	return nil
}

// Ping implements database.Adapter.
func (a *Adapter) Ping(context.Context) error { return nil }

// Close implements database.Adapter.
func (a *Adapter) Close() error {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return nil
}

// ListSchemas implements database.SQLAdapter.
func (a *Adapter) ListSchemas(ctx context.Context) ([]database.Schema, error) {
	if err := a.enter(ctx, "ListSchemas"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]database.Schema(nil), a.schemas...), nil
}

// ListTables implements database.SQLAdapter.
func (a *Adapter) ListTables(ctx context.Context, schema string) ([]database.Table, error) {
	if err := a.enter(ctx, "ListTables"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]database.Table(nil), a.tables[schema]...), nil
}

// DescribeTable implements database.SQLAdapter.
func (a *Adapter) DescribeTable(ctx context.Context, ref database.TableRef) (*database.TableMetadata, error) {
	if err := a.enter(ctx, "DescribeTable"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.data[ref.Schema+"."+ref.Name]
	if t == nil {
		return nil, &database.Error{Kind: database.KindMetadataUnavailable, Message: "no such table"}
	}
	m := *t.Meta
	m.Columns = append([]database.ColumnMetadata(nil), t.Meta.Columns...)
	m.MarkKeys()
	m.ResolveIdentity()
	return &m, nil
}

// FetchPage implements database.SQLAdapter.
func (a *Adapter) FetchPage(ctx context.Context, req database.PageRequest) (*database.PageResult, error) {
	if err := a.enter(ctx, "FetchPage"); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	if err := req.Validate(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	t := a.data[req.Meta.Ref.Schema+"."+req.Meta.Ref.Name]
	a.mu.Unlock()
	if t == nil {
		return nil, &database.Error{Kind: database.KindMetadataUnavailable, Message: "no such table"}
	}
	m := req.Meta
	plan := database.PlanQuery(m, req.Sort)

	var rows [][]grid.Value
	for _, r := range t.Rows {
		if req.Filter == nil || matches(m, r, req.Filter) {
			rows = append(rows, r)
		}
	}
	if len(plan.Order) > 0 {
		sort.SliceStable(rows, func(i, j int) bool { return compareRows(m, plan.Order, rows[i], rows[j]) < 0 })
	}

	size := req.Size
	start, end := 0, 0
	switch plan.Strategy {
	case database.StrategyOffset:
		start = min(req.Offset, len(rows))
		end = min(start+size, len(rows))
	default:
		keyOrder := make([]database.OrderColumn, len(plan.Key))
		for i, k := range plan.Key {
			keyOrder[i] = database.OrderColumn{Column: k}
		}
		switch req.Cursor.Dir {
		case database.CursorFirst:
			end = min(size, len(rows))
		case database.CursorAfter:
			start = sort.Search(len(rows), func(i int) bool { return compareKey(m, plan.Key, rows[i], req.Cursor.Key) > 0 })
			end = min(start+size, len(rows))
		case database.CursorBefore:
			end = sort.Search(len(rows), func(i int) bool { return compareKey(m, plan.Key, rows[i], req.Cursor.Key) >= 0 })
			start = max(0, end-size)
		case database.CursorLast:
			end = len(rows)
			start = max(0, end-size)
		}
	}
	res := &database.PageResult{Rows: append([][]grid.Value(nil), rows[start:end]...)}
	if plan.Strategy == database.StrategyKeyset && (req.Cursor.Dir == database.CursorBefore || req.Cursor.Dir == database.CursorLast) {
		res.More = start > 0
	} else {
		res.More = end < len(rows)
	}
	return res, nil
}

func compareValues(class database.ColumnClass, x, y grid.Value) int {
	switch {
	case x.Kind == grid.KindNull && y.Kind == grid.KindNull:
		return 0
	case x.Kind == grid.KindNull:
		return 1 // NULLs sort last
	case y.Kind == grid.KindNull:
		return -1
	}
	if class == database.ClassNumber {
		fx, _ := strconv.ParseFloat(x.Text, 64)
		fy, _ := strconv.ParseFloat(y.Text, 64)
		switch {
		case fx < fy:
			return -1
		case fx > fy:
			return 1
		}
		return 0
	}
	return strings.Compare(x.Text, y.Text)
}

func compareRows(m *database.TableMetadata, order []database.OrderColumn, a, b []grid.Value) int {
	for _, o := range order {
		i := m.ColumnIndex(o.Column)
		c := compareValues(m.Columns[i].Class, a[i], b[i])
		if o.Desc {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// compareKey compares a row's key columns with a cursor's key texts.
func compareKey(m *database.TableMetadata, key []string, row []grid.Value, cur []string) int {
	for n, k := range key {
		i := m.ColumnIndex(k)
		c := compareValues(m.Columns[i].Class, row[i], grid.Value{Kind: m.Columns[i].Class.GridKind(), Text: cur[n]})
		if c != 0 {
			return c
		}
	}
	return 0
}

func matches(m *database.TableMetadata, r []grid.Value, f *database.Filter) bool {
	i := m.ColumnIndex(f.Column)
	v := r[i]
	class := m.Columns[i].Class
	switch f.Op {
	case database.OpIsNull:
		return v.Kind == grid.KindNull
	case database.OpIsNotNull:
		return v.Kind != grid.KindNull
	}
	if v.Kind == grid.KindNull {
		return false
	}
	want := grid.Value{Kind: class.GridKind(), Text: f.Value}
	switch f.Op {
	case database.OpEquals:
		return compareValues(class, v, want) == 0
	case database.OpNotEquals:
		return compareValues(class, v, want) != 0
	case database.OpGreater:
		return compareValues(class, v, want) > 0
	case database.OpLess:
		return compareValues(class, v, want) < 0
	case database.OpContains:
		return strings.Contains(strings.ToLower(v.Text), strings.ToLower(f.Value))
	}
	return false
}

// UsersTable returns a table "users" with n rows: id (number, primary key),
// name (text), score (number, nullable, NULL on every seventh row) and blob
// (binary).
func UsersTable(name string, n int) *Table {
	meta := &database.TableMetadata{
		Ref:  database.TableRef{Catalog: "app", Schema: "public", Name: name},
		Kind: database.ObjectTable,
		Columns: []database.ColumnMetadata{
			{Name: "id", Ordinal: 1, Class: database.ClassNumber, NativeType: "integer"},
			{Name: "name", Ordinal: 2, Class: database.ClassText, NativeType: "text", Nullable: true},
			{Name: "score", Ordinal: 3, Class: database.ClassNumber, NativeType: "numeric(10,2)", Nullable: true},
			{Name: "blob", Ordinal: 4, Class: database.ClassBinary, NativeType: "bytea", Nullable: true},
		},
		Constraints: database.Section[database.ConstraintMetadata]{Items: []database.ConstraintMetadata{
			{Name: name + "_pkey", Type: database.ConstraintPrimaryKey, Columns: []string{"id"}, Definition: "PRIMARY KEY (id)"},
		}},
		EstimatedRows: int64(n),
	}
	t := &Table{Meta: meta}
	for i := 1; i <= n; i++ {
		score := grid.Value{Kind: grid.KindNull}
		if i%7 != 0 {
			score = grid.Value{Kind: grid.KindNumber, Text: fmt.Sprintf("%d.50", i)}
		}
		t.Rows = append(t.Rows, []grid.Value{
			{Kind: grid.KindNumber, Text: strconv.Itoa(i)},
			{Kind: grid.KindText, Text: fmt.Sprintf("user %d", i)},
			score,
			{Kind: grid.KindBinary, Size: i},
		})
	}
	return t
}

// SaveChanges implements database.SQLAdapter. It works on a copy of the rows
// and keeps the copy only when every row is written, which is the atomicity of
// the transaction of a real adapter.
func (a *Adapter) SaveChanges(ctx context.Context, req database.SaveRequest) error {
	notApplied := func(err error) error {
		var se *database.SaveError
		if errors.As(err, &se) {
			return se
		}
		var de *database.Error
		if !errors.As(err, &de) {
			de = &database.Error{Kind: database.KindSave, Message: err.Error(), Err: err}
		}
		return &database.SaveError{Err: de}
	}
	if err := a.enter(ctx, "SaveChanges"); err != nil {
		return notApplied(err)
	}
	a.mu.Lock()
	a.saves = append(a.saves, req)
	t := a.data[req.Meta.Ref.Schema+"."+req.Meta.Ref.Name]
	a.mu.Unlock()
	if t == nil {
		return notApplied(&database.Error{Kind: database.KindMetadataUnavailable, Message: "no such table"})
	}
	if issues := database.ValidateChanges(req.Meta, req.Changes); len(issues) > 0 {
		return notApplied(&database.Error{Kind: database.KindValidation, Message: issues[0].Message})
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	rows := make([][]grid.Value, len(t.Rows))
	for i, r := range t.Rows {
		rows[i] = append([]grid.Value(nil), r...)
	}
	m := req.Meta
	for _, rc := range req.Changes.Rows {
		var hits []int
		for i, r := range rows {
			if rowMatches(m, r, rc) {
				hits = append(hits, i)
			}
		}
		fail := func(kind database.Kind, msg string) error {
			return &database.SaveError{Err: &database.Error{Kind: kind, Message: msg}, RowID: rc.RowID, HasRow: true}
		}
		switch {
		case len(hits) == 0:
			return fail(database.KindConflict, "The row was changed or deleted by someone else.")
		case len(hits) > 1:
			return fail(database.KindSave, fmt.Sprintf("The write would change %d rows; it was not applied.", len(hits)))
		}
		for _, ch := range rc.Cells {
			rows[hits[0]][m.ColumnIndex(ch.Column)] = ch.Current
		}
	}
	if err := ctx.Err(); err != nil {
		return notApplied(&database.Error{Kind: database.KindCanceled, Message: "canceled", Err: err})
	}
	t.Rows = rows
	if a.UnknownCommit {
		return &database.SaveError{
			Err:     &database.Error{Kind: database.KindConnection, Message: "The connection was lost while committing."},
			Outcome: database.SaveUnknown,
		}
	}
	return nil
}

// rowMatches reports whether a row is the one rc describes: it has the key and
// still holds the original values of the columns being changed.
func rowMatches(m *database.TableMetadata, r []grid.Value, rc database.RowChange) bool {
	for _, k := range rc.Key {
		if !r[m.ColumnIndex(k.Column)].Equal(k.Value) {
			return false
		}
	}
	for _, ch := range rc.Cells {
		if !r[m.ColumnIndex(ch.Column)].Equal(ch.Original) {
			return false
		}
	}
	return true
}
