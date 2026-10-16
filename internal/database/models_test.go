package database

import "testing"

func col(name string, class ColumnClass, nullable bool) ColumnMetadata {
	return ColumnMetadata{Name: name, Class: class, Nullable: nullable, NativeType: "t"}
}

func okSection[T any](items ...T) Section[T] { return Section[T]{Items: items} }

func tableWith(cols ...ColumnMetadata) *TableMetadata {
	return &TableMetadata{
		Columns:     cols,
		Constraints: okSection[ConstraintMetadata](),
		Indexes:     okSection[IndexMetadata](),
	}
}

func TestIdentityFromPrimaryKey(t *testing.T) {
	m := tableWith(col("a", ClassNumber, false), col("b", ClassText, false), col("c", ClassText, true))
	m.Constraints = okSection(ConstraintMetadata{Name: "pk", Type: ConstraintPrimaryKey, Columns: []string{"a", "b"}})
	m.ResolveIdentity()
	if m.Identity.Source != IdentityPrimaryKey || len(m.Identity.Columns) != 2 {
		t.Fatalf("identity %+v", m.Identity)
	}
	if ok, _ := m.Editable(); !ok {
		t.Error("a table with a primary key should be editable")
	}
}

func TestIdentityFromUniqueIndex(t *testing.T) {
	m := tableWith(col("a", ClassNumber, false), col("b", ClassText, false), col("n", ClassText, true))
	m.Indexes = okSection(
		IndexMetadata{Name: "nullable", Unique: true, Valid: true, KeyColumns: []string{"n"}},
		IndexMetadata{Name: "partial", Unique: true, Valid: true, Partial: true, KeyColumns: []string{"a"}},
		IndexMetadata{Name: "expr", Unique: true, Valid: true, KeyColumns: nil, Columns: []string{"lower(b)"}},
		IndexMetadata{Name: "invalid", Unique: true, Valid: false, KeyColumns: []string{"a"}},
		IndexMetadata{Name: "two", Unique: true, Valid: true, KeyColumns: []string{"a", "b"}},
		IndexMetadata{Name: "one", Unique: true, Valid: true, KeyColumns: []string{"b"}},
	)
	m.ResolveIdentity()
	if m.Identity.Source != IdentityUniqueIndex || len(m.Identity.Columns) != 1 || m.Identity.Columns[0] != "b" {
		t.Fatalf("identity %+v: only the unique index on non-null columns with the fewest columns qualifies", m.Identity)
	}
}

func TestIdentityNeverGuessed(t *testing.T) {
	cases := map[string]func(*TableMetadata){
		"no key": func(m *TableMetadata) {},
		"constraints unreadable": func(m *TableMetadata) {
			m.Constraints = Section[ConstraintMetadata]{State: SectionDenied, Reason: "permission denied"}
		},
		"indexes unreadable": func(m *TableMetadata) {
			m.Indexes = Section[IndexMetadata]{State: SectionFailed, Reason: "boom"}
		},
		"view": func(m *TableMetadata) {
			m.Kind = ObjectView
			m.Constraints = okSection(ConstraintMetadata{Type: ConstraintPrimaryKey, Columns: []string{"a"}})
		},
		"foreign table": func(m *TableMetadata) { m.Subtype = "foreign table" },
		"key column missing": func(m *TableMetadata) {
			m.Constraints = okSection(ConstraintMetadata{Type: ConstraintPrimaryKey, Columns: []string{"ghost"}})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := tableWith(col("a", ClassNumber, false))
			mutate(m)
			m.ResolveIdentity()
			if m.Identity.Known() {
				t.Fatalf("identity %+v should be unavailable", m.Identity)
			}
			if ok, why := m.Editable(); ok || why == "" {
				t.Errorf("Editable = %v, %q", ok, why)
			}
		})
	}
}

func TestMarkKeys(t *testing.T) {
	m := tableWith(col("a", ClassNumber, false), col("b", ClassText, false), col("c", ClassText, true))
	m.Constraints = okSection(
		ConstraintMetadata{Type: ConstraintPrimaryKey, Columns: []string{"a"}},
		ConstraintMetadata{Type: ConstraintUnique, Columns: []string{"b"}},
	)
	m.MarkKeys()
	if !m.Columns[0].PrimaryKey || m.Columns[1].PrimaryKey || !m.Columns[1].Unique || m.Columns[2].Unique {
		t.Errorf("columns %+v", m.Columns)
	}
	if len(m.PrimaryKey) != 1 || m.PrimaryKey[0] != "a" {
		t.Errorf("PrimaryKey %v", m.PrimaryKey)
	}
	m.Constraints = Section[ConstraintMetadata]{State: SectionFailed}
	m.Columns[0].PrimaryKey = false
	m.MarkKeys()
	if m.Columns[0].PrimaryKey {
		t.Error("keys were marked from constraints that were not read")
	}
}

func TestColumnClassBehavior(t *testing.T) {
	for _, c := range []ColumnClass{ClassJSON, ClassBinary, ClassOther} {
		if c.Sortable() || c.KeysetSafe() {
			t.Errorf("%v must not sort", c)
		}
		if ops := OperatorsFor(c); len(ops) != 2 || ops[0] != OpIsNull {
			t.Errorf("%v operators %v", c, ops)
		}
	}
	if OperatorsFor(ClassText)[0] != OpContains {
		t.Error("text defaults to contains")
	}
	if OperatorsFor(ClassNumber)[0] != OpEquals {
		t.Error("numbers default to equality")
	}
	if Allows(ClassNumber, OpContains) || !Allows(ClassText, OpContains) {
		t.Error("contains is for text only")
	}
	if OpIsNull.NeedsValue() || !OpEquals.NeedsValue() {
		t.Error("NeedsValue")
	}
}
