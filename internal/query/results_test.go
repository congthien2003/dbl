package query

import (
	"dbl/internal/database"
	"dbl/internal/grid"
	"testing"
)

func TestResultSourcePagesNeverExecute(t *testing.T) {
	r := &database.SQLResult{Columns: []database.SQLColumn{{Name: "x", Kind: grid.KindText}}}
	for i := 0; i < 250; i++ {
		r.Rows = append(r.Rows, []grid.Value{{Kind: grid.KindText, Text: "x"}})
	}
	s := ResultSource(r, 2)
	if s.Len() != 100 || s.RowID(0) != 100 || !s.ReadOnly() {
		t.Fatalf("page: len=%d row=%d readonly=%v", s.Len(), s.RowID(0), s.ReadOnly())
	}
	if ResultSource(r, 3).Len() != 50 || PageCount(r) != 3 {
		t.Fatal("last page")
	}
	if s.Columns()[0].Editable || s.Columns()[0].Sortable {
		t.Fatal("SQL source permits writes or sorting")
	}
	r.Rows = nil
	if PageCount(r) != 1 || len(ResultSource(r, 1).Columns()) != 1 {
		t.Fatal("empty result lost its columns")
	}
}
