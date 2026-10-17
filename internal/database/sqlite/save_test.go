package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

func num(s string) grid.Value { return grid.Value{Kind: grid.KindNumber, Text: s} }
func txt(s string) grid.Value { return grid.Value{Kind: grid.KindText, Text: s} }
func nullValue() grid.Value   { return grid.Value{Kind: grid.KindNull} }
func cell(col string, from, to grid.Value) database.CellChange {
	return database.CellChange{Column: col, Original: from, Current: to}
}

// rowOf is the change of the row whose first key column is id.
func rowOf(id int, cells ...database.CellChange) database.RowChange {
	return database.RowChange{
		RowID: int64(id),
		Key:   []database.KeyValue{{Column: "id", Value: num(strconv.Itoa(id))}},
		Cells: cells,
	}
}

func request(m *database.TableMetadata, rows ...database.RowChange) database.SaveRequest {
	return database.SaveRequest{Meta: m, Changes: database.ChangeSet{Rows: rows}}
}

func scalar(t *testing.T, a *Adapter, query string, args ...any) any {
	t.Helper()
	var v any
	if err := a.db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func saveError(t *testing.T, err error) *database.SaveError {
	t.Helper()
	var se *database.SaveError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a SaveError", err)
	}
	return se
}

func notesDB(t *testing.T) (*Adapter, *database.TableMetadata) {
	t.Helper()
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT, n INTEGER, r REAL, v)",
		"INSERT INTO notes VALUES (1, 'one', 10, 1.5, '1'), (2, 'two', 20, 2.5, 1), (3, NULL, NULL, NULL, NULL)",
	)))
	return a, describe(t, a, "notes")
}

func TestSaveUpdatesOneRow(t *testing.T) {
	a, m := notesDB(t)
	err := a.SaveChanges(context.Background(), request(m, rowOf(1,
		cell("title", txt("one"), txt("ONE")),
		cell("n", num("10"), num("11")),
		cell("r", num("1.5"), num("2.25")),
	)))
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 1"); got != "ONE" {
		t.Errorf("title %v", got)
	}
	if got := scalar(t, a, "SELECT typeof(n) || n FROM notes WHERE id = 1"); got != "integer11" {
		t.Errorf("n %v", got)
	}
	if got := scalar(t, a, "SELECT typeof(r) || r FROM notes WHERE id = 1"); got != "real2.25" {
		t.Errorf("r %v", got)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 2"); got != "two" {
		t.Errorf("another row changed: %v", got)
	}
}

func TestSaveConflictWhenValueChanged(t *testing.T) {
	a, m := notesDB(t)
	if _, err := a.db.Exec("UPDATE notes SET title = 'elsewhere' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	err := a.SaveChanges(context.Background(), request(m, rowOf(1, cell("title", txt("one"), txt("mine")))))
	se := saveError(t, err)
	if se.Err.Kind != database.KindConflict || se.Outcome != database.SaveNotApplied || !se.HasRow || se.RowID != 1 {
		t.Errorf("error %+v", se)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 1"); got != "elsewhere" {
		t.Errorf("the conflicting save wrote: %v", got)
	}
	// A deleted row is a conflict too.
	if _, err := a.db.Exec("DELETE FROM notes WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	err = a.SaveChanges(context.Background(), request(m, rowOf(2, cell("title", txt("two"), txt("x")))))
	if saveError(t, err).Err.Kind != database.KindConflict {
		t.Errorf("deleted row: %v", err)
	}
}

func TestSaveDistinguishesIntegerOneFromTextOne(t *testing.T) {
	a, m := notesDB(t)
	ctx := context.Background()
	// Row 1 holds the text '1' in v, row 2 the integer 1.
	// The user saw text '1' in row 2: the database now holds an integer.
	err := a.SaveChanges(ctx, request(m, rowOf(2, cell("v", txt("1"), txt("changed")))))
	if saveError(t, err).Err.Kind != database.KindConflict {
		t.Errorf("text '1' against integer 1: %v", err)
	}
	// And the integer 1 seen in row 1, which holds text.
	err = a.SaveChanges(ctx, request(m, rowOf(1, cell("v", num("1"), txt("changed")))))
	if saveError(t, err).Err.Kind != database.KindConflict {
		t.Errorf("integer 1 against text '1': %v", err)
	}
	// The values as they are stored save.
	if err := a.SaveChanges(ctx, request(m,
		rowOf(1, cell("v", txt("1"), txt("a"))),
		rowOf(2, cell("v", num("1"), txt("b"))),
	)); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT v FROM notes WHERE id = 2"); got != "b" {
		t.Errorf("v = %v", got)
	}
}

func TestSaveRollsBackAllOnFailure(t *testing.T) {
	a, m := notesDB(t)
	err := a.SaveChanges(context.Background(), request(m,
		rowOf(1, cell("title", txt("one"), txt("first"))),
		rowOf(2, cell("title", txt("not what the user saw"), txt("second"))),
	))
	se := saveError(t, err)
	if se.Err.Kind != database.KindConflict || se.RowID != 2 {
		t.Errorf("error %+v", se)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 1"); got != "one" {
		t.Errorf("the first row stayed written: %v", got)
	}
}

func TestSaveCompositeKey(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE line (a INTEGER, b TEXT, qty INTEGER, PRIMARY KEY (a, b))",
		"INSERT INTO line VALUES (1, 'x', 5), (1, 'y', 6), (2, 'x', 7)",
	)))
	m := describe(t, a, "line")
	rc := database.RowChange{
		RowID: 1,
		Key:   []database.KeyValue{{Column: "a", Value: num("1")}, {Column: "b", Value: txt("y")}},
		Cells: []database.CellChange{cell("qty", num("6"), num("60"))},
	}
	if err := a.SaveChanges(context.Background(), request(m, rc)); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT group_concat(qty) FROM (SELECT qty FROM line ORDER BY a, b)"); got != "5,60,7" {
		t.Errorf("quantities %v", got)
	}
}

func TestSaveRejectsTextInIntegerColumn(t *testing.T) {
	a, m := notesDB(t)
	for _, bad := range []string{"abc", "1.5", "9223372036854775808", ""} {
		err := a.SaveChanges(context.Background(), request(m, rowOf(1, cell("n", num("10"), txt(bad)))))
		se := saveError(t, err)
		if se.Err.Kind != database.KindValidation {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if got := scalar(t, a, "SELECT n FROM notes WHERE id = 1"); got != int64(10) {
		t.Errorf("n = %v", got)
	}
	// The largest 64-bit integer is accepted and kept exactly.
	if err := a.SaveChanges(context.Background(), request(m, rowOf(1, cell("n", num("10"), num("9223372036854775807"))))); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT n FROM notes WHERE id = 1"); got != int64(9223372036854775807) {
		t.Errorf("n = %v", got)
	}
}

func TestSaveNullToValueAndBack(t *testing.T) {
	a, m := notesDB(t)
	ctx := context.Background()
	if err := a.SaveChanges(ctx, request(m, rowOf(3, cell("title", nullValue(), txt("now set")), cell("n", nullValue(), num("3"))))); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT title || n FROM notes WHERE id = 3"); got != "now set3" {
		t.Errorf("row 3: %v", got)
	}
	if err := a.SaveChanges(ctx, request(m, rowOf(3, cell("title", txt("now set"), nullValue())))); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 3"); got != nil {
		t.Errorf("title %v", got)
	}
	// Setting a value where the user saw NULL, but the cell holds one now.
	err := a.SaveChanges(ctx, request(m, rowOf(1, cell("title", nullValue(), txt("x")))))
	if saveError(t, err).Err.Kind != database.KindConflict {
		t.Errorf("NULL against a value: %v", err)
	}
}

func TestSaveComparesTextByteForByte(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE ci (id INTEGER PRIMARY KEY, s TEXT COLLATE NOCASE)",
		"INSERT INTO ci VALUES (1, 'ABC')",
	)))
	m := describe(t, a, "ci")
	// The user saw 'abc'; the database holds 'ABC'. The collation of the
	// column says they are equal; they are not the same value.
	err := a.SaveChanges(context.Background(), request(m, rowOf(1, cell("s", txt("abc"), txt("new")))))
	if saveError(t, err).Err.Kind != database.KindConflict {
		t.Errorf("a case difference was missed: %v", err)
	}
}

func TestSaveConstraintFailureRollsBack(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE u (id INTEGER PRIMARY KEY, code TEXT UNIQUE, name TEXT NOT NULL)",
		"INSERT INTO u VALUES (1, 'a', 'x'), (2, 'b', 'y')",
	)))
	m := describe(t, a, "u")
	err := a.SaveChanges(context.Background(), request(m,
		rowOf(1, cell("name", txt("x"), txt("changed"))),
		rowOf(2, cell("code", txt("b"), txt("a"))),
	))
	se := saveError(t, err)
	if se.Err.Kind != database.KindValidation || se.RowID != 2 || se.Outcome != database.SaveNotApplied {
		t.Errorf("error %+v", se)
	}
	if got := scalar(t, a, "SELECT name FROM u WHERE id = 1"); got != "x" {
		t.Errorf("row 1 = %v", got)
	}
	// NULL into a NOT NULL column is stopped before anything runs.
	err = a.SaveChanges(context.Background(), request(m, rowOf(1, cell("name", txt("x"), nullValue()))))
	if saveError(t, err).Err.Kind != database.KindValidation {
		t.Errorf("NULL in NOT NULL: %v", err)
	}
}

func TestSaveCancelBeforeCommit(t *testing.T) {
	a, m := notesDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.SaveChanges(ctx, request(m, rowOf(1, cell("title", txt("one"), txt("x")))))
	se := saveError(t, err)
	if se.Err.Kind != database.KindCanceled || se.Outcome != database.SaveNotApplied {
		t.Errorf("error %+v", se)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 1"); got != "one" {
		t.Errorf("a canceled save wrote: %v", got)
	}
}

func TestSaveLockedDatabaseReturnsBusyAndKeepsEdits(t *testing.T) {
	old := busyTimeoutMs
	busyTimeoutMs = 200
	defer func() { busyTimeoutMs = old }()

	a, m := notesDB(t)
	other, err := sql.Open("sqlite3", a.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}

	req := request(m, rowOf(1, cell("title", txt("one"), txt("mine"))))
	start := time.Now()
	err = a.SaveChanges(context.Background(), req)
	if time.Since(start) > 3*time.Second {
		t.Errorf("Save waited %v on a locked file", time.Since(start))
	}
	se := saveError(t, err)
	if se.Err.Kind != database.KindConnection || se.Err.Message != "Database is locked by another process" || se.Outcome != database.SaveNotApplied {
		t.Errorf("error %+v", se.Err)
	}

	// The request is untouched: once the lock is gone, the same edits save.
	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveChanges(context.Background(), req); err != nil {
		t.Fatalf("after the lock was released: %v", err)
	}
	if got := scalar(t, a, "SELECT title FROM notes WHERE id = 1"); got != "mine" {
		t.Errorf("title %v", got)
	}
}

func TestSaveReadOnlyAdapterRefuses(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)", "INSERT INTO t VALUES (1, 'a')")
	p := profileFor(path)
	p.ReadOnly = true
	a := connect(t, p)
	m := describe(t, a, "t")
	err := a.SaveChanges(context.Background(), request(m, rowOf(1, cell("v", txt("a"), txt("b")))))
	if saveError(t, err).Err.Kind != database.KindPermission {
		t.Errorf("error %v", err)
	}
	if got := scalar(t, a, "SELECT v FROM t"); got != "a" {
		t.Errorf("v = %v", got)
	}
}

func TestSaveNothingToSave(t *testing.T) {
	a, m := notesDB(t)
	if saveError(t, a.SaveChanges(context.Background(), request(m))).Err.Kind != database.KindValidation {
		t.Error("an empty save was accepted")
	}
}
