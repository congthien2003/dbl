package mock

import (
	"testing"
	"unicode/utf8"

	"dbl/internal/grid"
)

func TestValueIsDeterministicAndCoversKinds(t *testing.T) {
	d := NewDataset("orders", 1_000_000, 12, false)
	seen := map[grid.Kind]bool{}
	sawEmpty, sawLong := false, false
	for r := 0; r < 5000; r++ {
		if v := d.Value(r, 0); v.Kind != grid.KindNumber || v.Text == "" {
			t.Fatalf("id cell of row %d = %+v, want a number", r, v)
		}
		for c := 0; c < len(d.Columns); c++ {
			v := d.Value(r, c)
			if v != d.Value(r, c) {
				t.Fatalf("Value(%d,%d) is not deterministic", r, c)
			}
			seen[v.Kind] = true
			if v.Kind == grid.KindText && v.Text == "" {
				sawEmpty = true
			}
			if utf8.RuneCountInString(v.Text) > 500 {
				sawLong = true
			}
		}
	}
	for _, k := range []grid.Kind{grid.KindNull, grid.KindText, grid.KindNumber, grid.KindBool, grid.KindDateTime, grid.KindJSON, grid.KindBinary} {
		if !seen[k] {
			t.Errorf("kind %d never produced", k)
		}
	}
	if !sawEmpty {
		t.Error("no empty string in the first 5000 rows")
	}
	if !sawLong {
		t.Error("no value longer than 500 runes in the first 5000 rows")
	}
	pk := d.Columns[0]
	if !pk.PrimaryKey || pk.Editable || pk.Kind != grid.KindNumber {
		t.Errorf("column 0 = %+v, want a read-only numeric primary key", pk)
	}
}

func TestDatasetIsLazy(t *testing.T) {
	d := NewDataset("orders", 1_000_000, 12, false)
	allocs := testing.AllocsPerRun(100, func() { _ = d.Value(999_999, 5) })
	if allocs > 4 {
		t.Errorf("Value allocates %.0f times per call, want <= 4", allocs)
	}
}

func TestColumnsMatchRequestedCount(t *testing.T) {
	d := NewDataset("wide", 100, 200, false)
	if len(d.Columns) != 200 {
		t.Fatalf("%d columns, want 200", len(d.Columns))
	}
	names := map[string]bool{}
	for _, c := range d.Columns {
		if names[c.Name] {
			t.Fatalf("duplicate column name %q", c.Name)
		}
		names[c.Name] = true
	}
}

func TestReadOnlyDatasetHasNoEditableColumns(t *testing.T) {
	for _, c := range NewDataset("v", 10, 6, true).Columns {
		if c.Editable {
			t.Errorf("column %q is editable in a read-only dataset", c.Name)
		}
	}
	for _, c := range NewDataset("t", 10, 8, false).Columns {
		if c.Kind == grid.KindBinary && c.Editable {
			t.Errorf("binary column %q is editable", c.Name)
		}
	}
}

func TestOutOfRangeValueIsNull(t *testing.T) {
	d := NewDataset("t", 10, 3, false)
	for _, rc := range [][2]int{{-1, 0}, {10, 0}, {0, -1}, {0, 3}} {
		if v := d.Value(rc[0], rc[1]); v.Kind != grid.KindNull {
			t.Errorf("Value(%d,%d) = %+v, want NULL", rc[0], rc[1], v)
		}
	}
}
