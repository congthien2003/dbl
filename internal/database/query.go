package database

import (
	"fmt"

	"dbl/internal/grid"
)

// Page sizes (spec FR-004).
const (
	DefaultPageSize = 100
	MaxPageSize     = 1000
)

// Sort orders the rows by one column.
type Sort struct {
	Column string
	Desc   bool
}

// FilterOp is a filter operator (spec FR-006).
type FilterOp uint8

// Filter operators.
const (
	OpEquals FilterOp = iota + 1
	OpNotEquals
	OpContains
	OpGreater
	OpLess
	OpIsNull
	OpIsNotNull
)

// Label is the operator as the filter bar shows it.
func (op FilterOp) Label() string {
	switch op {
	case OpEquals:
		return "equals"
	case OpNotEquals:
		return "not equals"
	case OpContains:
		return "contains"
	case OpGreater:
		return "greater than"
	case OpLess:
		return "less than"
	case OpIsNull:
		return "is NULL"
	case OpIsNotNull:
		return "is not NULL"
	}
	return "?"
}

// NeedsValue reports whether the operator compares with a value.
func (op FilterOp) NeedsValue() bool { return op != OpIsNull && op != OpIsNotNull && op != 0 }

// OperatorsFor lists the operators that apply to a column class, the
// default first. Text columns default to "contains"; the others to
// equality.
func OperatorsFor(c ColumnClass) []FilterOp {
	switch c {
	case ClassText:
		return []FilterOp{OpContains, OpEquals, OpNotEquals, OpGreater, OpLess, OpIsNull, OpIsNotNull}
	case ClassNumber, ClassDateTime, ClassOrdered:
		return []FilterOp{OpEquals, OpNotEquals, OpGreater, OpLess, OpIsNull, OpIsNotNull}
	case ClassBool:
		return []FilterOp{OpEquals, OpNotEquals, OpIsNull, OpIsNotNull}
	}
	return []FilterOp{OpIsNull, OpIsNotNull}
}

// Allows reports whether op applies to the class.
func Allows(c ColumnClass, op FilterOp) bool {
	for _, o := range OperatorsFor(c) {
		if o == op {
			return true
		}
	}
	return false
}

// Filter is the single column filter (spec FR-006). Value is the text the
// user typed; the server reads it as the type of the column.
type Filter struct {
	Column string
	Op     FilterOp
	Value  string
}

// CursorDir says which page of a keyset walk a request asks for.
type CursorDir uint8

// Cursor directions.
const (
	CursorFirst  CursorDir = iota // the first page
	CursorAfter                   // the page after Key
	CursorBefore                  // the page before Key
	CursorLast                    // the last page
)

// Cursor is a position in a keyset walk. Key holds the text of the key
// columns of the row the page starts after (CursorAfter) or ends before
// (CursorBefore), in the order of QueryPlan.Key.
type Cursor struct {
	Dir CursorDir
	Key []string
}

// PageRequest asks for one page of a table. Meta must be the metadata the
// adapter returned for the table: column names, classes and the row
// identity come from it.
type PageRequest struct {
	Meta   *TableMetadata
	Sort   *Sort
	Filter *Filter
	Size   int
	// Offset is the first row of an offset page. Keyset pages use Cursor.
	Offset int
	Cursor Cursor
}

// PageResult is one page, in display order. More says whether rows exist
// beyond the page in the direction it was read: after it for an offset
// page, CursorFirst and CursorAfter; before it for CursorBefore and
// CursorLast.
type PageResult struct {
	Rows [][]grid.Value
	More bool
}

// Strategy is how pages are located.
type Strategy uint8

// Paging strategies.
const (
	StrategyOffset Strategy = iota
	StrategyKeyset
)

func (s Strategy) String() string {
	if s == StrategyKeyset {
		return "keyset"
	}
	return "offset"
}

// OrderColumn is one column of an ORDER BY.
type OrderColumn struct {
	Column string
	Desc   bool
}

// QueryPlan is how a table is ordered and paged. The same plan is used by
// the application (to know how to navigate) and by the adapter (to build
// the query), so they cannot disagree.
type QueryPlan struct {
	Strategy Strategy
	Order    []OrderColumn
	// Key holds the columns keyset paging walks, in order; empty for offset.
	Key []string
	// Stable reports whether the order is total: rows cannot repeat or
	// vanish between pages unless the data changes.
	Stable bool
	// Warning is shown to the user when the order is not stable.
	Warning string
}

// Describe says in a few words how the table is ordered and paged.
func (p QueryPlan) Describe() string {
	if len(p.Order) == 0 {
		return "No ORDER BY (" + p.Strategy.String() + " paging)"
	}
	s := "Order: "
	for i, o := range p.Order {
		if i > 0 {
			s += ", "
		}
		s += o.Column
		if o.Desc {
			s += " ↓"
		}
	}
	return s + " (" + p.Strategy.String() + " paging)"
}

const noKeyWarning = "This table has no unique key, so the order of rows between pages is not guaranteed."

// PlanQuery decides the order and the paging of a table for the sort asked.
//
// Without a sort, a table with a verified row identity whose key columns can
// be walked is paged by keyset on that identity: it stays fast deep into a
// large table. Otherwise pages are located by offset, ordered by the sort
// and then by the identity, which makes the order total. A table without
// identity gets no tie-breaker and a warning: DBL does not invent one.
func PlanQuery(m *TableMetadata, sort *Sort) QueryPlan {
	var key []string
	if m.Identity.Known() {
		key = m.Identity.Columns
	}
	keySafe := len(key) > 0
	for _, k := range key {
		if c, ok := m.Column(k); !ok || !c.Class.KeysetSafe() {
			keySafe = false
		}
	}
	plan := QueryPlan{Stable: len(key) > 0}
	if !plan.Stable {
		plan.Warning = noKeyWarning
	}
	if sort == nil && keySafe {
		plan.Strategy = StrategyKeyset
		plan.Key = append([]string(nil), key...)
		for _, k := range key {
			plan.Order = append(plan.Order, OrderColumn{Column: k})
		}
		return plan
	}
	if sort != nil {
		plan.Order = append(plan.Order, OrderColumn{Column: sort.Column, Desc: sort.Desc})
	}
	for _, k := range key {
		if sort != nil && k == sort.Column {
			continue
		}
		c, ok := m.Column(k)
		if !ok || !c.Class.Sortable() {
			// A key that cannot be ordered cannot break ties; the order is
			// not total.
			plan.Stable, plan.Warning = false, noKeyWarning
			continue
		}
		plan.Order = append(plan.Order, OrderColumn{Column: k})
	}
	return plan
}

// Validate checks a request against the metadata it carries. Column names
// are checked here because they are quoted but never trusted; a column
// that does not exist, a sort on a type that cannot be ordered or a filter
// operator that does not apply are validation errors, not SQL errors.
func (r PageRequest) Validate() error {
	if r.Meta == nil {
		return &Error{Kind: KindValidation, Message: "The request has no table metadata."}
	}
	if r.Size < 1 || r.Size > MaxPageSize {
		return &Error{Kind: KindValidation, Message: fmt.Sprintf("The page size must be between 1 and %d.", MaxPageSize)}
	}
	if r.Offset < 0 {
		return &Error{Kind: KindValidation, Message: "The page offset cannot be negative."}
	}
	if len(r.Meta.Columns) == 0 {
		return &Error{Kind: KindMetadataUnavailable, Message: "The table has no readable columns."}
	}
	if s := r.Sort; s != nil {
		c, ok := r.Meta.Column(s.Column)
		if !ok {
			return &Error{Kind: KindValidation, Message: fmt.Sprintf("Cannot sort by %q: no such column.", s.Column)}
		}
		if !c.Class.Sortable() {
			return &Error{Kind: KindUnsupported, Message: fmt.Sprintf("Column %q (%s) cannot be sorted.", s.Column, c.NativeType)}
		}
	}
	if f := r.Filter; f != nil {
		c, ok := r.Meta.Column(f.Column)
		if !ok {
			return &Error{Kind: KindValidation, Message: fmt.Sprintf("Cannot filter by %q: no such column.", f.Column)}
		}
		if !Allows(c.Class, f.Op) {
			return &Error{Kind: KindUnsupported, Message: fmt.Sprintf("Column %q (%s) cannot be filtered with %q.", f.Column, c.NativeType, f.Op.Label())}
		}
	}
	plan := PlanQuery(r.Meta, r.Sort)
	switch plan.Strategy {
	case StrategyOffset:
		if r.Cursor.Dir != CursorFirst || len(r.Cursor.Key) != 0 {
			return &Error{Kind: KindValidation, Message: "This table is paged by offset; a cursor does not apply."}
		}
	case StrategyKeyset:
		if r.Offset != 0 {
			return &Error{Kind: KindValidation, Message: "This table is paged by keyset; an offset does not apply."}
		}
		switch r.Cursor.Dir {
		case CursorFirst, CursorLast:
			if len(r.Cursor.Key) != 0 {
				return &Error{Kind: KindValidation, Message: "The cursor has a key it should not have."}
			}
		case CursorAfter, CursorBefore:
			if len(r.Cursor.Key) != len(plan.Key) {
				return &Error{Kind: KindValidation, Message: "The cursor does not match the key of the table."}
			}
		default:
			return &Error{Kind: KindValidation, Message: "Unknown cursor direction."}
		}
	}
	return nil
}
