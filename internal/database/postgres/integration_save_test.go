package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// These tests write to a real PostgreSQL server, in the schema of their own
// that newReadFixture creates and drops (see integration_read_test.go for the
// DBL_TEST_PG_* variables). Point them at a throw-away server only.

// loaded returns the rows of a table as FetchPage reads them, in plan order.
func (f *readFixture) loaded(t *testing.T, m *database.TableMetadata) [][]grid.Value {
	t.Helper()
	return f.page(t, database.PageRequest{Meta: m, Size: database.MaxPageSize}).Rows
}

// rowChange builds the change of the row at index i of rows: its identity is
// read from the loaded row, as the application does from its snapshot.
func rowChange(m *database.TableMetadata, rows [][]grid.Value, i int, cells ...database.CellChange) database.RowChange {
	rc := database.RowChange{RowID: int64(i + 1), Cells: cells}
	for _, k := range m.Identity.Columns {
		rc.Key = append(rc.Key, database.KeyValue{Column: k, Value: rows[i][m.ColumnIndex(k)]})
	}
	for j := range rc.Cells {
		if rc.Cells[j].Original == (grid.Value{}) {
			rc.Cells[j].Original = rows[i][m.ColumnIndex(rc.Cells[j].Column)]
		}
	}
	return rc
}

func cell(col string, cur grid.Value) database.CellChange {
	return database.CellChange{Column: col, Current: cur}
}

func (f *readFixture) save(m *database.TableMetadata, rcs ...database.RowChange) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return f.ad.SaveChanges(ctx, database.SaveRequest{Meta: m, Changes: database.ChangeSet{Rows: rcs}})
}

func wantSaveError(t *testing.T, err error, kind database.Kind, outcome database.SaveOutcome) *database.SaveError {
	t.Helper()
	var se *database.SaveError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *SaveError", err)
	}
	if se.Err.Kind != kind || se.Outcome != outcome {
		t.Fatalf("kind %v outcome %v (%v), want kind %v outcome %v", se.Err.Kind, se.Outcome, se.Err.Message, kind, outcome)
	}
	return se
}

func TestIntegrationSaveSeveralCellsAndRows(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	// Row 0 (Alice): a new name. Row 1 (empty name): NULL. Row 2 (NULL name):
	// the empty string, which is not NULL.
	err := f.save(m,
		rowChange(m, rows, 0, cell("name", txt("Alicia")), cell("note", txt("two cells"))),
		rowChange(m, rows, 1, cell("name", nul())),
		rowChange(m, rows, 2, cell("name", txt(""))),
	)
	if err != nil {
		t.Fatalf("SaveChanges: %v", err)
	}
	got := f.loaded(t, m)
	n, note := m.ColumnIndex("name"), m.ColumnIndex("note")
	if got[0][n].Text != "Alicia" || got[0][note].Text != "two cells" {
		t.Errorf("row 0: %+v", got[0])
	}
	if got[1][n].Kind != grid.KindNull {
		t.Errorf("row 1 name should be NULL: %+v", got[1][n])
	}
	if got[2][n].Kind == grid.KindNull || got[2][n].Text != "" {
		t.Errorf("row 2 name should be the empty string: %+v", got[2][n])
	}
	// Columns that were not edited, the binary one included, are untouched.
	if got[0][m.ColumnIndex("blob")] != rows[0][m.ColumnIndex("blob")] || got[0][m.ColumnIndex("doc")] != rows[0][m.ColumnIndex("doc")] {
		t.Errorf("an unedited column changed: %+v", got[0])
	}
}

func TestIntegrationSaveTypesRoundTrip(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	err := f.save(m, rowChange(m, rows, 0,
		cell("price", numv("99.5000")),
		cell("active", grid.Value{Kind: grid.KindBool, Text: "no"}),
		cell("doc", grid.Value{Kind: grid.KindJSON, Text: `{"b":  [1, 2]}`}),
		cell("created", grid.Value{Kind: grid.KindDateTime, Text: "2026-01-02 03:04:05.5+00"}),
	))
	if err != nil {
		t.Fatalf("SaveChanges: %v", err)
	}
	got := f.loaded(t, m)[0]
	want := map[string]string{"price": "99.5000", "active": "false", "doc": `{"b": [1, 2]}`}
	for col, text := range want {
		if v := got[m.ColumnIndex(col)]; v.Text != text {
			t.Errorf("%s = %q, want %q", col, v.Text, text)
		}
	}
	if v := got[m.ColumnIndex("created")]; v.Kind == grid.KindNull || v.Text == "" {
		t.Errorf("created = %+v, want a timestamp", v)
	}
	// The value read back is the original for the next save: it must match.
	rows = f.loaded(t, m)
	if err := f.save(m, rowChange(m, rows, 0, cell("price", numv("1")), cell("doc", nul()), cell("created", nul()))); err != nil {
		t.Fatalf("second SaveChanges: %v", err)
	}
}

func TestIntegrationSaveCompositeKey(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "pairs")
	if len(m.Identity.Columns) != 2 {
		t.Fatalf("identity %v", m.Identity.Columns)
	}
	rows := f.loaded(t, m)
	if err := f.save(m, rowChange(m, rows, 17, cell("v", txt("changed")))); err != nil {
		t.Fatalf("SaveChanges: %v", err)
	}
	got := f.loaded(t, m)
	for i, r := range got {
		want := rows[i][2].Text
		if i == 17 {
			want = "changed"
		}
		if r[2].Text != want {
			t.Fatalf("row %d v = %q, want %q", i, r[2].Text, want)
		}
	}
}

func TestIntegrationSaveReservedAndUnicodeNames(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, `Select "Table"`)
	rows := f.loaded(t, m)
	if err := f.save(m, rowChange(m, rows, 0, cell(`Name "q"`, txt("Việt 100%_'\"; DROP")), cell("ünï", nul()))); err != nil {
		t.Fatalf("SaveChanges: %v", err)
	}
	got := f.loaded(t, m)[0]
	if got[m.ColumnIndex(`Name "q"`)].Text != "Việt 100%_'\"; DROP" || got[m.ColumnIndex("ünï")].Kind != grid.KindNull {
		t.Errorf("row %+v", got)
	}
}

func TestIntegrationSaveConflictRollsBackEverything(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	// Someone else changes the name of the second row after it was read.
	if _, err := f.ad.db.Exec(`UPDATE `+quoteTable(m.Ref)+` SET name = 'taken' WHERE id = $1`, rows[1][0].Text); err != nil {
		t.Fatal(err)
	}
	err := f.save(m,
		rowChange(m, rows, 0, cell("name", txt("first"))),
		rowChange(m, rows, 1, cell("name", txt("second"))),
	)
	se := wantSaveError(t, err, database.KindConflict, database.SaveNotApplied)
	if !se.HasRow || se.RowID != 2 {
		t.Errorf("the failing row is %d (%v), want 2", se.RowID, se.HasRow)
	}
	got := f.loaded(t, m)
	n := m.ColumnIndex("name")
	if got[0][n].Text != "Alice" || got[1][n].Text != "taken" {
		t.Errorf("a conflict left a partial write: %q, %q", got[0][n].Text, got[1][n].Text)
	}
}

func TestIntegrationSaveChangeToAnotherColumnIsNotAConflict(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	if _, err := f.ad.db.Exec(`UPDATE `+quoteTable(m.Ref)+` SET note = 'theirs' WHERE id = $1`, rows[0][0].Text); err != nil {
		t.Fatal(err)
	}
	if err := f.save(m, rowChange(m, rows, 0, cell("name", txt("mine")))); err != nil {
		t.Fatalf("SaveChanges: %v", err)
	}
	got := f.loaded(t, m)[0]
	if got[m.ColumnIndex("name")].Text != "mine" || got[m.ColumnIndex("note")].Text != "theirs" {
		t.Errorf("the other session's change was lost or the edit not applied: %+v", got)
	}
}

func TestIntegrationSaveDeletedRowIsAConflict(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	if _, err := f.ad.db.Exec(`DELETE FROM `+quoteTable(m.Ref)+` WHERE id = $1`, rows[0][0].Text); err != nil {
		t.Fatal(err)
	}
	wantSaveError(t, f.save(m, rowChange(m, rows, 0, cell("name", txt("x")))), database.KindConflict, database.SaveNotApplied)
}

func TestIntegrationSaveConstraintViolationRollsBack(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	err := f.save(m,
		rowChange(m, rows, 0, cell("name", txt("first"))),
		rowChange(m, rows, 3, cell("price", numv("-1"))), // price_positive
	)
	se := wantSaveError(t, err, database.KindValidation, database.SaveNotApplied)
	if !se.HasRow || se.RowID != 4 {
		t.Errorf("the failing row is %d (%v), want 4", se.RowID, se.HasRow)
	}
	if got := f.loaded(t, m); got[0][m.ColumnIndex("name")].Text != "Alice" {
		t.Errorf("the first row was written although the second failed: %q", got[0][m.ColumnIndex("name")].Text)
	}
}

func TestIntegrationSaveServerRejectsTheValue(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	// Local validation cannot know a date: the server says no, inside the
	// transaction, and nothing is written.
	err := f.save(m,
		rowChange(m, rows, 0, cell("name", txt("first"))),
		rowChange(m, rows, 1, cell("created", grid.Value{Kind: grid.KindDateTime, Text: "not a date"})),
	)
	wantSaveError(t, err, database.KindValidation, database.SaveNotApplied)
	if got := f.loaded(t, m); got[0][m.ColumnIndex("name")].Text != "Alice" {
		t.Error("a rejected save left a partial write")
	}
}

func TestIntegrationSaveUpdateOfMoreThanOneRowIsUnsafe(t *testing.T) {
	f := newReadFixture(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE ` + f.schema + `.inh_parent (id int PRIMARY KEY, v text)`,
		`CREATE TABLE ` + f.schema + `.inh_child () INHERITS (` + f.schema + `.inh_parent)`,
		`INSERT INTO ` + f.schema + `.inh_parent VALUES (1, 'a')`,
		`INSERT INTO ` + f.schema + `.inh_child VALUES (1, 'a')`, // the child does not inherit the key
	} {
		if _, err := f.ad.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	m := f.describe(t, "inh_parent")
	rows := f.loaded(t, m)
	if len(rows) != 2 {
		t.Fatalf("rows %d", len(rows))
	}
	wantSaveError(t, f.save(m, rowChange(m, rows, 0, cell("v", txt("b")))), database.KindSave, database.SaveNotApplied)
	for _, r := range f.loaded(t, m) {
		if r[1].Text != "a" {
			t.Errorf("an unsafe update was left in place: %+v", r)
		}
	}
}

func TestIntegrationSaveCanceledBeforeItStartsWritesNothing(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.ad.SaveChanges(ctx, database.SaveRequest{Meta: m, Changes: database.ChangeSet{
		Rows: []database.RowChange{rowChange(m, rows, 0, cell("name", txt("x")))},
	}})
	wantSaveError(t, err, database.KindCanceled, database.SaveNotApplied)
	if got := f.loaded(t, m); got[0][m.ColumnIndex("name")].Text != "Alice" {
		t.Error("a canceled save wrote")
	}
}

func TestIntegrationSaveRefusesWhatValidationRefuses(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	rows := f.loaded(t, m)
	// Binary, key and invalid values never reach the server.
	for _, cells := range [][]database.CellChange{
		{cell("blob", grid.Value{Kind: grid.KindBinary, Size: 1})},
		{cell("id", numv("99"))},
		{cell("active", grid.Value{Kind: grid.KindBool, Text: "maybe"})},
	} {
		wantSaveError(t, f.save(m, rowChange(m, rows, 0, cells...)), database.KindValidation, database.SaveNotApplied)
	}
	view := f.describe(t, "v_items")
	vrows := f.loaded(t, view)
	wantSaveError(t, f.save(view, database.RowChange{RowID: 1, Cells: []database.CellChange{{Column: "name", Original: vrows[0][1], Current: txt("x")}}}),
		database.KindValidation, database.SaveNotApplied)
}
