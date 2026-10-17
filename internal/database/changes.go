package database

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"dbl/internal/grid"
)

// KeyValue is the value of one column, used for the original identity of a
// row.
type KeyValue struct {
	Column string
	Value  grid.Value
}

// CellChange is one edited cell: the value the user saw when the edit began
// and the value to write. Original is also what the write checks the
// database against (optimistic concurrency, spec FR-005), so it is the value
// as it was read, never a display string.
type CellChange struct {
	Column   string
	Original grid.Value
	Current  grid.Value
}

// RowChange is the pending change of one row. Key holds the identity of the
// row as it was loaded, in the order of Meta.Identity.Columns; the write finds
// the row by it. RowID is the grid's stable number for the row, used only to
// report which row a problem belongs to.
type RowChange struct {
	RowID int64
	Key   []KeyValue
	Cells []CellChange
}

// ChangeSet is everything a Save writes: one RowChange per edited row, in
// the order the rows were loaded.
type ChangeSet struct{ Rows []RowChange }

// Empty reports whether there is nothing to write.
func (cs ChangeSet) Empty() bool { return len(cs.Rows) == 0 }

// SaveRequest asks an adapter to write a ChangeSet of one table. Meta must
// be the metadata the adapter returned for the table.
type SaveRequest struct {
	Meta    *TableMetadata
	Changes ChangeSet
}

// SaveOutcome says what is known about the database after a failed Save.
type SaveOutcome uint8

// Save outcomes.
const (
	// SaveNotApplied: nothing was written; the transaction was rolled back
	// or never began.
	SaveNotApplied SaveOutcome = iota
	// SaveUnknown: COMMIT was sent and its result was not seen. The changes
	// may or may not have been written; they must not be sent again blindly.
	SaveUnknown
)

// SaveError is the error of a failed Save. Err has the category and the
// message to show.
type SaveError struct {
	Err     *Error
	Outcome SaveOutcome
	// RowID is the row the failure belongs to, when HasRow is set.
	RowID  int64
	HasRow bool
}

func (e *SaveError) Error() string { return e.Err.Error() }

// Unwrap lets errors.As and KindOf reach the *Error.
func (e *SaveError) Unwrap() error { return e.Err }

// Issue is a problem found in a pending edit before anything is written.
type Issue struct {
	RowID   int64
	Column  string
	Message string
}

// ValidateChanges checks pending changes against the metadata, without
// touching a database. A column that may not be edited, NULL in a column that
// does not take it, a value that cannot be the column's type are issues;
// whatever else the server rejects is found when it runs the write inside
// the transaction. A Save with issues writes nothing.
func ValidateChanges(m *TableMetadata, cs ChangeSet) []Issue {
	var out []Issue
	add := func(r RowChange, col, msg string) {
		out = append(out, Issue{RowID: r.RowID, Column: col, Message: msg})
	}
	if ok, why := m.Editable(); !ok {
		for _, r := range cs.Rows {
			add(r, "", why)
		}
		return out
	}
	for _, r := range cs.Rows {
		if !sameKeyColumns(m.Identity.Columns, r.Key) {
			add(r, "", "The row has no complete identity, so it cannot be written safely.")
			continue
		}
		for _, k := range r.Key {
			if k.Value.Kind == grid.KindNull {
				add(r, k.Column, "The identity of the row holds NULL.")
			}
		}
		if len(r.Cells) == 0 {
			add(r, "", "The row has no edited cell.")
		}
		seen := map[string]bool{}
		for _, ch := range r.Cells {
			if seen[ch.Column] {
				add(r, ch.Column, "The column is edited twice in the same row.")
				continue
			}
			seen[ch.Column] = true
			if ok, why := m.CanEditColumn(ch.Column); !ok {
				add(r, ch.Column, why)
				continue
			}
			c, _ := m.Column(ch.Column)
			if msg := checkValue(c, ch.Current); msg != "" {
				add(r, ch.Column, msg)
			}
		}
	}
	return out
}

func sameKeyColumns(want []string, key []KeyValue) bool {
	if len(want) != len(key) {
		return false
	}
	for i, k := range key {
		if k.Column != want[i] {
			return false
		}
	}
	return true
}

// checkValue returns why v cannot be written to c, or "".
func checkValue(c ColumnMetadata, v grid.Value) string {
	if v.Kind == grid.KindNull {
		if !c.Nullable {
			return "This column does not accept NULL."
		}
		return ""
	}
	if v.Kind == grid.KindBinary {
		return "Binary values cannot be edited."
	}
	if strings.IndexByte(v.Text, 0) >= 0 {
		return "A text value cannot hold the NUL character."
	}
	text := strings.TrimSpace(v.Text)
	switch c.Class {
	case ClassNumber:
		return checkNumber(c, text)
	case ClassBool:
		if !isBoolLiteral(text) {
			return "Not a boolean. Use true, false, t, f, yes, no, on, off, 1 or 0."
		}
	case ClassJSON:
		if !json.Valid([]byte(v.Text)) {
			return "Not valid JSON."
		}
	}
	return ""
}

var decimalRE = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// baseType is the native type without its length or precision, in lower case.
func baseType(native string) string {
	t := strings.ToLower(strings.TrimSpace(native))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = strings.TrimSpace(t[:i] + t[strings.IndexByte(t, ')')+1:])
	}
	return t
}

func checkNumber(c ColumnMetadata, text string) string {
	if c.IntBits > 0 {
		if c.Unsigned {
			return checkUint(text, c.IntBits)
		}
		return checkInt(text, c.IntBits)
	}
	// MySQL spells its attributes after the type: "double unsigned".
	native := strings.TrimSuffix(strings.TrimSuffix(baseType(c.NativeType), " zerofill"), " unsigned")
	switch native {
	case "smallint", "int2":
		return checkInt(text, 16)
	case "integer", "int", "int4":
		return checkInt(text, 32)
	case "bigint", "int8":
		return checkInt(text, 64)
	case "numeric", "decimal", "real", "float4", "double precision", "float8", "float", "double":
		switch strings.ToLower(strings.TrimLeft(text, "+-")) {
		case "nan", "infinity", "inf":
			return ""
		}
		if !decimalRE.MatchString(text) {
			return "Not a number."
		}
	}
	return ""
}

func checkUint(text string, bits int) string {
	if _, err := strconv.ParseUint(text, 10, bits); err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return fmt.Sprintf("Out of range for an unsigned %d-bit integer.", bits)
		}
		if strings.HasPrefix(text, "-") {
			return "This column does not accept negative numbers."
		}
		return "Not a whole number."
	}
	return ""
}

func checkInt(text string, bits int) string {
	if _, err := strconv.ParseInt(text, 10, bits); err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return fmt.Sprintf("Out of range for a %d-bit integer.", bits)
		}
		return "Not a whole number."
	}
	return ""
}

// isBoolLiteral accepts what PostgreSQL accepts for a boolean: the words and
// their unambiguous prefixes, in any case, and 1 and 0.
func isBoolLiteral(s string) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	if s == "1" || s == "0" || s == "on" || s == "of" || s == "off" {
		return true
	}
	return slices.ContainsFunc([]string{"true", "false", "yes", "no"}, func(w string) bool {
		return strings.HasPrefix(w, s)
	})
}
