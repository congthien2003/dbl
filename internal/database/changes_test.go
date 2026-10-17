package database

import (
	"errors"
	"strings"
	"testing"

	"dbl/internal/grid"
)

func editMeta() *TableMetadata {
	m := &TableMetadata{
		Ref: TableRef{Schema: "public", Name: "t"},
		Columns: []ColumnMetadata{
			{Name: "id", Class: ClassNumber, NativeType: "integer"},
			{Name: "name", Class: ClassText, NativeType: "text", Nullable: true},
			{Name: "age", Class: ClassNumber, NativeType: "smallint", Nullable: true},
			{Name: "price", Class: ClassNumber, NativeType: "numeric(10,2)", Nullable: true},
			{Name: "ok", Class: ClassBool, NativeType: "boolean", Nullable: true},
			{Name: "doc", Class: ClassJSON, NativeType: "jsonb", Nullable: true},
			{Name: "req", Class: ClassText, NativeType: "text"},
			{Name: "blob", Class: ClassBinary, NativeType: "bytea", Nullable: true},
			{Name: "gen", Class: ClassNumber, NativeType: "integer", Generated: true, Nullable: true},
			{Name: "ident", Class: ClassNumber, NativeType: "integer", Identity: IdentityAlways},
			{Name: "byd", Class: ClassNumber, NativeType: "integer", Identity: IdentityByDefault},
			{Name: "ser", Class: ClassNumber, NativeType: "integer", Identity: IdentitySerial},
		},
		Constraints: Section[ConstraintMetadata]{Items: []ConstraintMetadata{
			{Type: ConstraintPrimaryKey, Columns: []string{"id"}},
		}},
		Indexes: Section[IndexMetadata]{},
	}
	m.MarkKeys()
	m.ResolveIdentity()
	return m
}

func TestCanEditColumn(t *testing.T) {
	m := editMeta()
	cases := map[string]bool{
		"id": false, "name": true, "age": true, "blob": false, "gen": false,
		"ident": false, "byd": false, "ser": true, "doc": true, "nope": false,
	}
	for col, want := range cases {
		got, why := m.CanEditColumn(col)
		if got != want {
			t.Errorf("CanEditColumn(%q) = %v (%s), want %v", col, got, why, want)
		}
		if !got && why == "" {
			t.Errorf("CanEditColumn(%q) refused without a reason", col)
		}
	}
}

func TestCanEditColumnNeedsAnEditableTable(t *testing.T) {
	view := editMeta()
	view.Kind = ObjectView
	view.ResolveIdentity()
	if ok, why := view.CanEditColumn("name"); ok || !strings.Contains(why, "read-only") {
		t.Errorf("view: ok=%v why=%q", ok, why)
	}
	noKey := editMeta()
	noKey.Constraints.Items = nil
	noKey.ResolveIdentity()
	if ok, why := noKey.CanEditColumn("name"); ok || why == "" {
		t.Errorf("no key: ok=%v why=%q", ok, why)
	}
	// A key column that cannot be read back exactly makes the table read-only.
	odd := editMeta()
	odd.Columns[0].Class = ClassJSON
	if ok, _ := odd.Editable(); ok {
		t.Error("a table whose key cannot be compared exactly is editable")
	}
	if odd.HasEditableColumn() {
		t.Error("HasEditableColumn true for a read-only table")
	}
	if !editMeta().HasEditableColumn() {
		t.Error("HasEditableColumn false for an editable table")
	}
}

func change(rowID int64, col string, orig, cur grid.Value) RowChange {
	return RowChange{
		RowID: rowID,
		Key:   []KeyValue{{Column: "id", Value: grid.Value{Kind: grid.KindNumber, Text: "1"}}},
		Cells: []CellChange{{Column: col, Original: orig, Current: cur}},
	}
}

func text(s string) grid.Value { return grid.Value{Kind: grid.KindText, Text: s} }
func num(s string) grid.Value  { return grid.Value{Kind: grid.KindNumber, Text: s} }
func null() grid.Value         { return grid.Value{Kind: grid.KindNull} }

func TestValidateChanges(t *testing.T) {
	m := editMeta()
	cases := []struct {
		name string
		col  string
		cur  grid.Value
		ok   bool
	}{
		{"text", "name", text("héllo"), true},
		{"empty text is not NULL", "name", text(""), true},
		{"null into nullable", "name", null(), true},
		{"null into NOT NULL", "req", null(), false},
		{"NUL character", "name", text("a\x00b"), false},
		{"smallint ok", "age", num("42"), true},
		{"smallint padded", "age", num(" -7 "), true},
		{"smallint out of range", "age", num("40000"), false},
		{"smallint fraction", "age", num("1.5"), false},
		{"smallint text", "age", num("abc"), false},
		{"numeric ok", "price", num("12.50"), true},
		{"numeric exponent", "price", num("1e3"), true},
		{"numeric NaN", "price", num("NaN"), true},
		{"numeric -Infinity", "price", num("-Infinity"), true},
		{"numeric junk", "price", num("12,5"), false},
		{"bool word", "ok", grid.Value{Kind: grid.KindBool, Text: "TRUE"}, true},
		{"bool prefix", "ok", grid.Value{Kind: grid.KindBool, Text: "y"}, true},
		{"bool one", "ok", grid.Value{Kind: grid.KindBool, Text: "1"}, true},
		{"bool ambiguous o", "ok", grid.Value{Kind: grid.KindBool, Text: "o"}, false},
		{"bool junk", "ok", grid.Value{Kind: grid.KindBool, Text: "maybe"}, false},
		{"json ok", "doc", grid.Value{Kind: grid.KindJSON, Text: `{"a":[1,2]}`}, true},
		{"json bad", "doc", grid.Value{Kind: grid.KindJSON, Text: `{a:1}`}, false},
		{"pk", "id", num("2"), false},
		{"binary column", "blob", grid.Value{Kind: grid.KindBinary, Size: 3}, false},
		{"generated", "gen", num("1"), false},
		{"identity", "ident", num("1"), false},
	}
	for _, tc := range cases {
		issues := ValidateChanges(m, ChangeSet{Rows: []RowChange{change(7, tc.col, text("x"), tc.cur)}})
		if tc.ok != (len(issues) == 0) {
			t.Errorf("%s: issues %v, want ok=%v", tc.name, issues, tc.ok)
		}
		for _, is := range issues {
			if is.RowID != 7 || is.Message == "" {
				t.Errorf("%s: issue %+v lacks the row or a message", tc.name, is)
			}
		}
	}
}

func TestValidateChangesRowProblems(t *testing.T) {
	m := editMeta()
	bad := change(1, "name", text("a"), text("b"))
	bad.Key = nil
	if got := ValidateChanges(m, ChangeSet{Rows: []RowChange{bad}}); len(got) == 0 {
		t.Error("a row without its identity was accepted")
	}
	nullKey := change(1, "name", text("a"), text("b"))
	nullKey.Key[0].Value = null()
	if got := ValidateChanges(m, ChangeSet{Rows: []RowChange{nullKey}}); len(got) == 0 {
		t.Error("a NULL identity was accepted")
	}
	twice := change(1, "name", text("a"), text("b"))
	twice.Cells = append(twice.Cells, twice.Cells[0])
	if got := ValidateChanges(m, ChangeSet{Rows: []RowChange{twice}}); len(got) == 0 {
		t.Error("the same column twice was accepted")
	}
	empty := change(1, "name", text("a"), text("b"))
	empty.Cells = nil
	if got := ValidateChanges(m, ChangeSet{Rows: []RowChange{empty}}); len(got) == 0 {
		t.Error("a row without cells was accepted")
	}
	ro := editMeta()
	ro.Kind = ObjectView
	ro.ResolveIdentity()
	if got := ValidateChanges(ro, ChangeSet{Rows: []RowChange{change(1, "name", text("a"), text("b"))}}); len(got) == 0 {
		t.Error("changes to a view were accepted")
	}
	if got := ValidateChanges(m, ChangeSet{Rows: []RowChange{change(1, "name", text("a"), text("b"))}}); len(got) != 0 {
		t.Errorf("a valid row has issues: %v", got)
	}
	if !(ChangeSet{}).Empty() {
		t.Error("the zero ChangeSet is not empty")
	}
}

func TestSaveErrorUnwrapsToError(t *testing.T) {
	inner := &Error{Kind: KindConflict, Message: "gone"}
	var err error = &SaveError{Err: inner, Outcome: SaveUnknown, RowID: 3, HasRow: true}
	var de *Error
	if !errors.As(err, &de) || de != inner {
		t.Errorf("errors.As did not reach the *Error: %v", de)
	}
	if KindOf(err) != KindConflict || err.Error() != "gone" {
		t.Errorf("KindOf=%v text=%q", KindOf(err), err.Error())
	}
	var se *SaveError
	if !errors.As(err, &se) || se.Outcome != SaveUnknown || !se.HasRow || se.RowID != 3 {
		t.Errorf("SaveError fields lost: %+v", se)
	}
}

func TestCheckNumberUsesIntBits(t *testing.T) {
	c := ColumnMetadata{Name: "n", Class: ClassNumber, NativeType: "INTEGER", IntBits: 64}
	if msg := checkNumber(c, "9223372036854775807"); msg != "" {
		t.Errorf("the largest 64-bit integer: %s", msg)
	}
	if msg := checkNumber(c, "9223372036854775808"); msg == "" {
		t.Error("an integer past 64 bits was accepted")
	}
	if msg := checkNumber(c, "abc"); msg == "" {
		t.Error("text was accepted in an integer column")
	}
	// Without IntBits the width comes from the native type, as before.
	pg := ColumnMetadata{Name: "n", Class: ClassNumber, NativeType: "integer"}
	if msg := checkNumber(pg, "2147483648"); msg == "" {
		t.Error("a 32-bit column accepted a 64-bit value")
	}
}

func TestReadOnlyTableIsNotEditable(t *testing.T) {
	m := &TableMetadata{
		Columns:  []ColumnMetadata{{Name: "id", Class: ClassNumber}},
		Identity: RowIdentity{Source: IdentityPrimaryKey, Columns: []string{"id"}},
		ReadOnly: "The connection is read-only.",
	}
	if ok, why := m.Editable(); ok || why != m.ReadOnly {
		t.Errorf("Editable = %v, %q", ok, why)
	}
	m.ReadOnly = ""
	if ok, why := m.Editable(); !ok {
		t.Error(why)
	}
}

func TestCheckNumberUnsignedAndMySQLWidths(t *testing.T) {
	cases := []struct {
		c    ColumnMetadata
		text string
		ok   bool
	}{
		{ColumnMetadata{IntBits: 64, Unsigned: true}, "18446744073709551615", true},
		{ColumnMetadata{IntBits: 64, Unsigned: true}, "18446744073709551616", false},
		{ColumnMetadata{IntBits: 8, Unsigned: true}, "-1", false},
		{ColumnMetadata{IntBits: 8}, "-128", true},
		{ColumnMetadata{IntBits: 8}, "128", false},
		{ColumnMetadata{IntBits: 24}, "8388607", true},
		{ColumnMetadata{IntBits: 24}, "8388608", false},
		{ColumnMetadata{NativeType: "double unsigned"}, "abc", false},
		{ColumnMetadata{NativeType: "decimal(10,2) unsigned zerofill"}, "1.5", true},
		{ColumnMetadata{NativeType: "float"}, "1e3", true},
	}
	for _, c := range cases {
		c.c.Class = ClassNumber
		if got := checkNumber(c.c, c.text) == ""; got != c.ok {
			t.Errorf("%+v %q: ok %v, want %v", c.c, c.text, got, c.ok)
		}
	}
}
