package sqlite

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"dbl/internal/connection"
	"dbl/internal/database"
	"dbl/internal/grid"
)

func describe(t *testing.T, a *Adapter, name string) *database.TableMetadata {
	t.Helper()
	m, err := a.DescribeTable(context.Background(), database.TableRef{Schema: "main", Name: name})
	if err != nil {
		t.Fatalf("DescribeTable(%s): %v", name, err)
	}
	return m
}

func TestListSchemasIsMainOnly(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t, "CREATE TABLE t (id INTEGER)")))
	got, err := a.ListSchemas(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "main" {
		t.Errorf("schemas %v, err %v", got, err)
	}
}

func TestListTablesSkipsInternalAndMarksViews(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE b (id INTEGER PRIMARY KEY AUTOINCREMENT)", // creates sqlite_sequence
		"CREATE TABLE a (id INTEGER)",
		"CREATE VIEW v AS SELECT id FROM a",
		"CREATE TABLE sqlitex (id INTEGER)",
	)))
	tables, err := a.ListTables(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	kinds := map[string]database.ObjectKind{}
	for _, tb := range tables {
		names = append(names, tb.Ref.Name)
		kinds[tb.Ref.Name] = tb.Kind
		if tb.Ref.Schema != "main" || tb.Ref.Catalog != "test.db" {
			t.Errorf("ref %+v", tb.Ref)
		}
	}
	if !slices.Equal(names, []string{"a", "b", "sqlitex", "v"}) {
		t.Errorf("names %v", names)
	}
	if kinds["v"] != database.ObjectView || kinds["a"] != database.ObjectTable {
		t.Errorf("kinds %v", kinds)
	}
}

func TestDescribeTableCompositePrimaryKey(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE line (order_id INTEGER, no INTEGER, qty INTEGER NOT NULL DEFAULT 1, note TEXT, PRIMARY KEY (no, order_id))")))
	m := describe(t, a, "line")
	if !slices.Equal(m.PrimaryKey, []string{"no", "order_id"}) {
		t.Errorf("primary key %v (key order, not column order)", m.PrimaryKey)
	}
	if !slices.Equal(m.Identity.Columns, []string{"no", "order_id"}) || m.Identity.Source != database.IdentityPrimaryKey {
		t.Errorf("identity %+v", m.Identity)
	}
	if ok, why := m.Editable(); !ok {
		t.Errorf("not editable: %s", why)
	}
	qty, _ := m.Column("qty")
	if qty.Nullable || qty.Default == nil || *qty.Default != "1" || qty.IntBits != 64 {
		t.Errorf("qty %+v", qty)
	}
	note, _ := m.Column("note")
	if !note.Nullable || note.Class != database.ClassText {
		t.Errorf("note %+v", note)
	}
}

func TestDescribeTableForeignKeysAndIncomingReferences(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE parent (id INTEGER PRIMARY KEY, code TEXT UNIQUE)",
		"CREATE TABLE child (id INTEGER PRIMARY KEY, p INTEGER REFERENCES parent ON DELETE CASCADE, c TEXT, FOREIGN KEY (c) REFERENCES parent(code))",
	)))
	child := describe(t, a, "child")
	if !child.ForeignKeys.OK() || len(child.ForeignKeys.Items) != 2 {
		t.Fatalf("foreign keys %+v", child.ForeignKeys)
	}
	var byCol = map[string]database.ForeignKeyMetadata{}
	for _, fk := range child.ForeignKeys.Items {
		byCol[fk.Columns[0]] = fk
	}
	if fk := byCol["p"]; fk.RefTable.Name != "parent" || !slices.Equal(fk.RefColumns, []string{"id"}) || fk.OnDelete != "CASCADE" {
		t.Errorf("fk p: %+v (an omitted target is the primary key)", fk)
	}
	if fk := byCol["c"]; !slices.Equal(fk.RefColumns, []string{"code"}) {
		t.Errorf("fk c: %+v", fk)
	}
	parent := describe(t, a, "parent")
	if !parent.Referenced.OK() || len(parent.Referenced.Items) != 2 {
		t.Fatalf("incoming %+v", parent.Referenced)
	}
	if parent.Referenced.Items[0].Table.Name != "child" {
		t.Errorf("incoming %+v", parent.Referenced.Items[0])
	}
	var unique int
	for _, c := range parent.Constraints.Items {
		if c.Type == database.ConstraintUnique && slices.Equal(c.Columns, []string{"code"}) {
			unique++
		}
	}
	if unique != 1 {
		t.Errorf("constraints %+v", parent.Constraints.Items)
	}
}

func TestDescribeTableGeneratedAndHiddenColumns(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE g (id INTEGER PRIMARY KEY, a INTEGER, dbl INTEGER GENERATED ALWAYS AS (a * 2) VIRTUAL, st INTEGER GENERATED ALWAYS AS (a + 1) STORED)",
	)))
	m := describe(t, a, "g")
	got := map[string]bool{}
	for _, c := range m.Columns {
		got[c.Name] = c.Generated
	}
	if len(got) != 4 || got["id"] || got["a"] || !got["dbl"] || !got["st"] {
		t.Errorf("generated flags %v", got)
	}
	if ok, why := m.CanEditColumn("dbl"); ok || why == "" {
		t.Errorf("a generated column is editable (%v, %q)", ok, why)
	}
	if ok, _ := m.CanEditColumn("a"); !ok {
		t.Error("a plain column is not editable")
	}
}

func TestDescribeTableWithoutRowidAndNoPrimaryKey(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE nokey (x TEXT, y TEXT)",
		"INSERT INTO nokey VALUES ('a', 'b')",
		"CREATE TABLE wr (k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID",
		"CREATE TABLE uq (u INTEGER NOT NULL, v TEXT)",
		"CREATE UNIQUE INDEX uq_u ON uq (u)",
		"CREATE TABLE uq_null (u INTEGER, v TEXT)",
		"CREATE UNIQUE INDEX uq_null_u ON uq_null (u)",
	)))
	nokey := describe(t, a, "nokey")
	if ok, why := nokey.Editable(); ok || why == "" {
		t.Errorf("a table without a key is editable (%v, %q); rowid must not be used", ok, why)
	}
	wr := describe(t, a, "wr")
	if ok, why := wr.Editable(); !ok {
		t.Errorf("WITHOUT ROWID table: %s", why)
	}
	uq := describe(t, a, "uq")
	if uq.Identity.Source != database.IdentityUniqueIndex || !slices.Equal(uq.Identity.Columns, []string{"u"}) {
		t.Errorf("unique index identity %+v", uq.Identity)
	}
	if ok, _ := describe(t, a, "uq_null").Editable(); ok {
		t.Error("a unique index on a nullable column was taken as identity")
	}
}

func TestDescribeViewIsReadOnly(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t, "CREATE TABLE a (id INTEGER PRIMARY KEY)", "CREATE VIEW v AS SELECT id FROM a")))
	m := describe(t, a, "v")
	if m.Kind != database.ObjectView {
		t.Errorf("kind %v", m.Kind)
	}
	if ok, _ := m.Editable(); ok {
		t.Error("a view is editable")
	}
	if m.Indexes.State != database.SectionNotApplicable {
		t.Errorf("indexes %+v", m.Indexes)
	}
}

func TestDescribeTableIndexesAndTriggers(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT, b TEXT)",
		"CREATE INDEX t_a ON t (a)",
		"CREATE INDEX t_expr ON t (lower(b))",
		"CREATE INDEX t_part ON t (b) WHERE a IS NOT NULL",
		"CREATE TABLE log (x)",
		"CREATE TRIGGER t_ai AFTER INSERT ON t BEGIN INSERT INTO log VALUES (new.id); END",
	)))
	m := describe(t, a, "t")
	byName := map[string]database.IndexMetadata{}
	for _, ix := range m.Indexes.Items {
		byName[ix.Name] = ix
	}
	if ix := byName["t_a"]; !slices.Equal(ix.KeyColumns, []string{"a"}) || ix.Unique {
		t.Errorf("t_a %+v", ix)
	}
	if ix := byName["t_expr"]; ix.KeyColumns != nil {
		t.Errorf("t_expr %+v", ix)
	}
	if ix := byName["t_part"]; !ix.Partial || ix.Predicate != "a IS NOT NULL" {
		t.Errorf("t_part %+v", ix)
	}
	if len(m.Triggers.Items) != 1 {
		t.Fatalf("triggers %+v", m.Triggers)
	}
	if tr := m.Triggers.Items[0]; tr.Name != "t_ai" || tr.Timing != "AFTER" || !slices.Equal(tr.Events, []string{"INSERT"}) {
		t.Errorf("trigger %+v", tr)
	}
}

func TestDescribeMissingTable(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t, "CREATE TABLE t (id INTEGER)")))
	_, err := a.DescribeTable(context.Background(), database.TableRef{Name: "nope"})
	if database.KindOf(err) != database.KindMetadataUnavailable {
		t.Errorf("error %v", err)
	}
}

func TestClassOfAffinityTable(t *testing.T) {
	for _, c := range []struct {
		declared string
		class    database.ColumnClass
		bits     int
	}{
		{"INTEGER", database.ClassNumber, 64},
		{"BIGINT", database.ClassNumber, 64},
		{"UNSIGNED BIG INT", database.ClassNumber, 64},
		{"BOOLEAN", database.ClassNumber, 0},
		{"VARCHAR(20)", database.ClassText, 0},
		{"TEXT", database.ClassText, 0},
		{"DATETIME", database.ClassDateTime, 0},
		{"TIMESTAMP", database.ClassDateTime, 0},
		{"BLOB", database.ClassBinary, 0},
		{"", database.ClassOther, 0},
		{"DECIMAL(10,2)", database.ClassNumber, 0},
		{"REAL", database.ClassNumber, 0},
		{"JSON", database.ClassJSON, 0},
		{"UUID", database.ClassOther, 0},
	} {
		class, bits := classOf(c.declared)
		if class != c.class || bits != c.bits {
			t.Errorf("classOf(%q) = %v, %d; want %v, %d", c.declared, class, bits, c.class, c.bits)
		}
	}
}

func TestReservedAndQuotedIdentifiers(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		`CREATE TABLE "my ""odd"" table" ("select" INTEGER PRIMARY KEY, "tên cột" TEXT)`,
		`CREATE TABLE "bảng" (id INTEGER PRIMARY KEY)`,
	)))
	tables, err := a.ListTables(context.Background(), "main")
	if err != nil || len(tables) != 2 {
		t.Fatalf("tables %v, err %v", tables, err)
	}
	m := describe(t, a, `my "odd" table`)
	if len(m.Columns) != 2 || m.Columns[0].Name != "select" || m.Columns[1].Name != "tên cột" {
		t.Errorf("columns %+v", m.Columns)
	}
	if ok, why := m.Editable(); !ok {
		t.Error(why)
	}
	if got := quoteTable(database.TableRef{Schema: "main", Name: `my "odd" table`}); got != `"main"."my ""odd"" table"` {
		t.Errorf("quoteTable %s", got)
	}
}

func TestToValueKeepsStorageClass(t *testing.T) {
	for _, c := range []struct {
		class database.ColumnClass
		in    any
		want  grid.Value
	}{
		{database.ClassNumber, nil, grid.Value{Kind: grid.KindNull}},
		{database.ClassNumber, int64(9223372036854775807), grid.Value{Kind: grid.KindNumber, Text: "9223372036854775807"}},
		{database.ClassNumber, 2.0, grid.Value{Kind: grid.KindNumber, Text: "2.0"}},
		{database.ClassNumber, 1.5, grid.Value{Kind: grid.KindNumber, Text: "1.5"}},
		{database.ClassNumber, "1", grid.Value{Kind: grid.KindText, Text: "1"}}, // '1' is not 1
		{database.ClassNumber, "abc", grid.Value{Kind: grid.KindText, Text: "abc"}},
		{database.ClassNumber, true, grid.Value{Kind: grid.KindNumber, Text: "1"}},
		{database.ClassText, []byte{1, 2, 3}, grid.Value{Kind: grid.KindBinary, Size: 3}},
		{database.ClassDateTime, "2024-01-02 03:04:05", grid.Value{Kind: grid.KindDateTime, Text: "2024-01-02 03:04:05"}},
		{database.ClassJSON, `{"a":1}`, grid.Value{Kind: grid.KindJSON, Text: `{"a":1}`}},
		{database.ClassText, "", grid.Value{Kind: grid.KindText, Text: ""}},
	} {
		if got := toValue(c.class, c.in); got != c.want {
			t.Errorf("toValue(%v, %#v) = %+v, want %+v", c.class, c.in, got, c.want)
		}
	}
}

func TestReadOnlyAdapterMarksTablesReadOnly(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	p := profileFor(path)
	p.ReadOnly = true
	a := connect(t, p)
	m := describe(t, a, "t")
	if ok, why := m.Editable(); ok || why != readOnlyReason {
		t.Errorf("Editable = %v, %q", ok, why)
	}
	if ok, _ := m.CanEditColumn("v"); ok {
		t.Error("a column of a read-only connection is editable")
	}
	// The same file opened for writing is editable.
	w := connect(t, connection.Profile{Name: "w", Driver: connection.DriverSQLite, SQLitePath: filepath.Clean(path), ConnectTimeoutSec: 5})
	if ok, why := describe(t, w, "t").Editable(); !ok {
		t.Error(why)
	}
}
