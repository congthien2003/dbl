package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// These tests read metadata and pages from a real PostgreSQL server. They
// need the same DBL_TEST_PG_* variables as integration_test.go and a server
// that may be written to: each test run creates a schema of its own
// (dbl_it_<random>) with a few tables, and drops it when the test ends. They
// touch nothing outside that schema, with one opt-in exception:
// TestIntegrationReadWithoutPrivilegeIsPermissionError creates and drops a
// login ROLE, which is cluster-wide, and runs only when
// DBL_TEST_PG_ALLOW_ROLES=1. Point all of them at a throw-away server only.

type readFixture struct {
	ad     *Adapter
	schema string
}

func newReadFixture(t *testing.T) *readFixture {
	t.Helper()
	p, pw := integrationProfile(t)
	ad, err := Connect(context.Background(), p, pw)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	a := ad.(*Adapter)
	var b [6]byte
	_, _ = rand.Read(b[:])
	f := &readFixture{ad: a, schema: "dbl_it_" + hex.EncodeToString(b[:])}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = a.db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+quoteIdent(f.schema)+" CASCADE")
		_ = a.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, stmt := range []string{
		`CREATE SCHEMA ` + f.schema,
		`CREATE TABLE ` + f.schema + `.items (
			id serial PRIMARY KEY, name text, price numeric, active boolean, created timestamptz,
			doc jsonb, blob bytea, note text, CONSTRAINT price_positive CHECK (price IS NULL OR price >= 0))`,
		`INSERT INTO ` + f.schema + `.items (name, price, active, created, doc, blob, note) VALUES
			('Alice', 12345678901234.123456789, true,  '2026-10-16 10:11:12.123456+00', '{"a": 1}', '\xdeadbeef', 'x'),
			('',      0,                       false, NULL,                              NULL,       NULL,         NULL),
			(NULL,    NULL,                    NULL,  NULL,                              '[]',       '\x',         ''),
			('Việt Nam 100%_done', 7,           true,  NULL,                              NULL,       NULL,         repeat('long ', 20000))`,
		`CREATE INDEX items_name_idx ON ` + f.schema + `.items (lower(name))`,
		`CREATE UNIQUE INDEX items_price_uq ON ` + f.schema + `.items (price) WHERE price IS NOT NULL`,
		`CREATE TABLE ` + f.schema + `.child (id int PRIMARY KEY, item_id int REFERENCES ` + f.schema + `.items (id) ON DELETE CASCADE)`,
		`CREATE TABLE ` + f.schema + `.pairs (a int, b int, v text, PRIMARY KEY (a, b))`,
		`INSERT INTO ` + f.schema + `.pairs SELECT x / 10, x % 10, 'v' || x FROM generate_series(0, 249) x`,
		`CREATE TABLE ` + f.schema + `.nokey (a int, b text)`,
		`INSERT INTO ` + f.schema + `.nokey VALUES (1, 'x'), (1, 'x'), (2, NULL)`,
		`CREATE TABLE ` + f.schema + `."Select ""Table""" ("select" int PRIMARY KEY, "Name ""q""" text, "ünï" text)`,
		`INSERT INTO ` + f.schema + `."Select ""Table""" VALUES (1, 'a', 'b')`,
		`CREATE VIEW ` + f.schema + `.v_items AS SELECT id, name FROM ` + f.schema + `.items`,
		`CREATE FUNCTION ` + f.schema + `.noop() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`,
		`CREATE TRIGGER items_noop BEFORE INSERT OR UPDATE ON ` + f.schema + `.items FOR EACH ROW EXECUTE FUNCTION ` + f.schema + `.noop()`,
	} {
		if _, err := a.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt[:min(40, len(stmt))], err)
		}
	}
	return f
}

func (f *readFixture) describe(t *testing.T, name string) *database.TableMetadata {
	t.Helper()
	m, err := f.ad.DescribeTable(context.Background(), database.TableRef{Schema: f.schema, Name: name})
	if err != nil {
		t.Fatalf("DescribeTable(%s): %v", name, err)
	}
	return m
}

func (f *readFixture) page(t *testing.T, req database.PageRequest) *database.PageResult {
	t.Helper()
	res, err := f.ad.FetchPage(context.Background(), req)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	return res
}

func TestIntegrationListSchemasAndTables(t *testing.T) {
	f := newReadFixture(t)
	schemas, err := f.ad.ListSchemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range schemas {
		found = found || s.Name == f.schema
		if strings.HasPrefix(s.Name, "pg_") || s.Name == "information_schema" {
			t.Errorf("system schema %q listed", s.Name)
		}
	}
	if !found {
		t.Errorf("schema %s not listed", f.schema)
	}
	tables, err := f.ad.ListTables(context.Background(), f.schema)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]database.ObjectKind{}
	for _, tb := range tables {
		kinds[tb.Ref.Name] = tb.Kind
		if tb.Ref.Schema != f.schema || tb.Ref.Catalog == "" {
			t.Errorf("ref %+v", tb.Ref)
		}
	}
	if kinds["items"] != database.ObjectTable || kinds["v_items"] != database.ObjectView || len(kinds) != 6 {
		t.Errorf("tables %v", kinds)
	}
}

func TestIntegrationDescribeTable(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	byName := map[string]database.ColumnMetadata{}
	for _, c := range m.Columns {
		byName[c.Name] = c
	}
	wantClass := map[string]database.ColumnClass{
		"id": database.ClassNumber, "name": database.ClassText, "price": database.ClassNumber, "active": database.ClassBool,
		"created": database.ClassDateTime, "doc": database.ClassJSON, "blob": database.ClassBinary,
	}
	for name, class := range wantClass {
		if byName[name].Class != class {
			t.Errorf("%s class %v, want %v", name, byName[name].Class, class)
		}
	}
	if !byName["id"].PrimaryKey || byName["id"].Nullable || byName["id"].Identity != database.IdentitySerial || byName["id"].Default == nil {
		t.Errorf("id %+v", byName["id"])
	}
	if !m.Identity.Known() || m.Identity.Source != database.IdentityPrimaryKey {
		t.Errorf("identity %+v", m.Identity)
	}
	if !m.Constraints.OK() || len(m.Constraints.Items) < 2 {
		t.Errorf("constraints %+v", m.Constraints)
	}
	if !m.Indexes.OK() || len(m.Indexes.Items) != 3 {
		t.Fatalf("indexes %+v", m.Indexes)
	}
	for _, ix := range m.Indexes.Items {
		switch ix.Name {
		case "items_name_idx":
			if ix.KeyColumns != nil || ix.Unique || len(ix.Columns) != 1 {
				t.Errorf("expression index %+v", ix)
			}
		case "items_price_uq":
			if !ix.Unique || !ix.Partial || ix.Predicate == "" {
				t.Errorf("partial unique index %+v", ix)
			}
		}
	}
	if !m.Triggers.OK() || len(m.Triggers.Items) != 1 || m.Triggers.Items[0].Timing != "BEFORE" || len(m.Triggers.Items[0].Events) != 2 {
		t.Errorf("triggers %+v", m.Triggers)
	}
	if !m.Referenced.OK() || len(m.Referenced.Items) != 1 || m.Referenced.Items[0].Table.Name != "child" ||
		m.Referenced.Items[0].OnDelete != "CASCADE" {
		t.Errorf("incoming foreign keys %+v", m.Referenced)
	}

	child := f.describe(t, "child")
	if len(child.ForeignKeys.Items) != 1 || child.ForeignKeys.Items[0].RefTable.Name != "items" ||
		child.ForeignKeys.Items[0].Columns[0] != "item_id" || child.ForeignKeys.Items[0].RefColumns[0] != "id" {
		t.Errorf("foreign keys %+v", child.ForeignKeys)
	}

	v := f.describe(t, "v_items")
	if v.Kind != database.ObjectView || v.Constraints.State != database.SectionNotApplicable || v.Indexes.State != database.SectionNotApplicable {
		t.Errorf("view %+v", v)
	}
	if ok, _ := v.Editable(); ok {
		t.Error("a view is editable")
	}

	nk := f.describe(t, "nokey")
	if nk.Identity.Known() {
		t.Errorf("a table without a key got identity %+v", nk.Identity)
	}
	if plan := database.PlanQuery(nk, nil); plan.Stable || plan.Warning == "" {
		t.Errorf("plan %+v", plan)
	}

	pairs := f.describe(t, "pairs")
	if len(pairs.Identity.Columns) != 2 {
		t.Errorf("composite identity %+v", pairs.Identity)
	}

	if _, err := f.ad.DescribeTable(context.Background(), database.TableRef{Schema: f.schema, Name: "nope"}); database.KindOf(err) != database.KindMetadataUnavailable {
		t.Errorf("missing table: %v", err)
	}
	if m.EstimatedRows < -1 {
		t.Errorf("estimate %d", m.EstimatedRows)
	}
}

func TestIntegrationValuesKeepTheirDistinctions(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	res := f.page(t, database.PageRequest{Meta: m, Size: 10})
	if len(res.Rows) != 4 || res.More {
		t.Fatalf("rows %d more %v", len(res.Rows), res.More)
	}
	col := func(name string) int { return m.ColumnIndex(name) }
	alice, empty, nulls, big := res.Rows[0], res.Rows[1], res.Rows[2], res.Rows[3]
	if got := alice[col("price")]; got.Kind != grid.KindNumber || got.Text != "12345678901234.123456789" {
		t.Errorf("numeric lost digits: %+v", got)
	}
	if got := alice[col("created")]; got.Kind != grid.KindDateTime || !strings.Contains(got.Text, ".123456") {
		t.Errorf("timestamp %+v", got)
	}
	if got := alice[col("active")]; got.Kind != grid.KindBool || got.Text != "true" {
		t.Errorf("bool %+v", got)
	}
	if got := alice[col("blob")]; got.Kind != grid.KindBinary || got.Size != 4 {
		t.Errorf("binary %+v", got)
	}
	if got := alice[col("doc")]; got.Kind != grid.KindJSON || !strings.Contains(got.Text, `"a"`) {
		t.Errorf("json %+v", got)
	}
	if got := empty[col("name")]; got.Kind != grid.KindText || got.Text != "" {
		t.Errorf("empty string came back as %+v", got)
	}
	if got := nulls[col("name")]; got.Kind != grid.KindNull {
		t.Errorf("NULL came back as %+v", got)
	}
	if got := nulls[col("blob")]; got.Kind != grid.KindBinary || got.Size != 0 {
		t.Errorf("empty bytea %+v", got)
	}
	if got := empty[col("blob")]; got.Kind != grid.KindNull {
		t.Errorf("NULL bytea %+v", got)
	}
	if got := big[col("name")]; got.Text != "Việt Nam 100%_done" {
		t.Errorf("unicode %+v", got)
	}
	if got := big[col("note")]; len(got.Text) != 100000 {
		t.Errorf("a long value was cut: %d bytes", len(got.Text))
	}
}

func TestIntegrationKeysetWalkOverCompositeKey(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "pairs")
	plan := database.PlanQuery(m, nil)
	if plan.Strategy != database.StrategyKeyset || len(plan.Key) != 2 {
		t.Fatalf("plan %+v", plan)
	}
	keyOf := func(r []grid.Value) []string { return []string{r[0].Text, r[1].Text} }
	var seen []string
	req := database.PageRequest{Meta: m, Size: 100}
	pages := 0
	for {
		res := f.page(t, req)
		pages++
		for _, r := range res.Rows {
			seen = append(seen, strings.Join(keyOf(r), ","))
		}
		if !res.More {
			break
		}
		req.Cursor = database.Cursor{Dir: database.CursorAfter, Key: keyOf(res.Rows[len(res.Rows)-1])}
	}
	if len(seen) != 250 || pages != 3 {
		t.Fatalf("saw %d rows in %d pages", len(seen), pages)
	}
	uniq := map[string]bool{}
	for _, s := range seen {
		uniq[s] = true
	}
	if len(uniq) != 250 {
		t.Errorf("%d distinct rows: pages overlapped or skipped", len(uniq))
	}
	// Backwards from the last page, and the last page directly.
	last := f.page(t, database.PageRequest{Meta: m, Size: 100, Cursor: database.Cursor{Dir: database.CursorLast}})
	if len(last.Rows) != 100 || !last.More || last.Rows[99][2].Text != "v249" {
		t.Fatalf("last page: %d rows, more %v", len(last.Rows), last.More)
	}
	prev := f.page(t, database.PageRequest{Meta: m, Size: 100, Cursor: database.Cursor{Dir: database.CursorBefore, Key: keyOf(last.Rows[0])}})
	if len(prev.Rows) != 100 || prev.Rows[99][2].Text != "v149" || prev.Rows[0][2].Text != "v50" || !prev.More {
		t.Errorf("previous page: %d rows, first %s, last %s", len(prev.Rows), prev.Rows[0][2].Text, prev.Rows[99][2].Text)
	}
}

func TestIntegrationOffsetSortAndFilter(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	res := f.page(t, database.PageRequest{Meta: m, Size: 2, Sort: &database.Sort{Column: "id", Desc: true}})
	if res.Rows[0][0].Text != "4" || !res.More {
		t.Errorf("sorted page %v more %v", res.Rows[0][0], res.More)
	}
	res = f.page(t, database.PageRequest{Meta: m, Size: 2, Offset: 2, Sort: &database.Sort{Column: "id", Desc: true}})
	if res.Rows[0][0].Text != "2" || res.More {
		t.Errorf("second page %v more %v", res.Rows[0][0], res.More)
	}

	contains := func(v string) int {
		return len(f.page(t, database.PageRequest{Meta: m, Size: 10,
			Filter: &database.Filter{Column: "name", Op: database.OpContains, Value: v}}).Rows)
	}
	if n := contains("100%_"); n != 1 {
		t.Errorf("a literal %%_ matched %d rows", n)
	}
	if n := contains("%"); n != 1 { // only the row that really holds a percent sign
		t.Errorf("'%%' matched %d rows", n)
	}
	if n := contains("alice"); n != 1 {
		t.Errorf("contains is not case-insensitive: %d", n)
	}

	count := func(f2 database.Filter) int {
		return len(f.page(t, database.PageRequest{Meta: m, Size: 10, Filter: &f2}).Rows)
	}
	if n := count(database.Filter{Column: "price", Op: database.OpEquals, Value: "12345678901234.123456789"}); n != 1 {
		t.Errorf("numeric equality: %d", n)
	}
	if n := count(database.Filter{Column: "price", Op: database.OpGreater, Value: "5"}); n != 2 {
		t.Errorf("greater: %d", n)
	}
	if n := count(database.Filter{Column: "name", Op: database.OpIsNull}); n != 1 {
		t.Errorf("is null: %d", n)
	}
	if n := count(database.Filter{Column: "name", Op: database.OpNotEquals, Value: "Alice"}); n != 2 { // NULL is excluded, as in SQL
		t.Errorf("not equals: %d", n)
	}
	if n := count(database.Filter{Column: "active", Op: database.OpEquals, Value: "true"}); n != 2 {
		t.Errorf("bool equality: %d", n)
	}
	// A value the column's type cannot read is a validation error, and a
	// quote in a value is just text.
	_, err := f.ad.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 10,
		Filter: &database.Filter{Column: "id", Op: database.OpEquals, Value: "1'; DROP TABLE items;--"}})
	if database.KindOf(err) != database.KindValidation {
		t.Errorf("invalid value: kind %v (%v)", database.KindOf(err), err)
	}
	if got := f.describe(t, "items"); len(got.Columns) == 0 {
		t.Error("the table is gone")
	}
}

func TestIntegrationReservedAndUnicodeNames(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, `Select "Table"`)
	if len(m.Columns) != 3 || m.Columns[1].Name != `Name "q"` || m.Columns[2].Name != "ünï" {
		t.Fatalf("columns %+v", m.Columns)
	}
	res := f.page(t, database.PageRequest{Meta: m, Size: 10,
		Sort:   &database.Sort{Column: "ünï"},
		Filter: &database.Filter{Column: `Name "q"`, Op: database.OpEquals, Value: "a"}})
	if len(res.Rows) != 1 || res.Rows[0][2].Text != "b" {
		t.Errorf("rows %v", res.Rows)
	}
}

func TestIntegrationCanceledAndTimedOutReads(t *testing.T) {
	f := newReadFixture(t)
	m := f.describe(t, "items")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.ad.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10}); database.KindOf(err) != database.KindCanceled {
		t.Errorf("canceled: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := f.ad.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10}); database.KindOf(err) != database.KindTimeout {
		t.Errorf("deadline: %v", err)
	}
}

func TestIntegrationReadWithoutPrivilegeIsPermissionError(t *testing.T) {
	if os.Getenv("DBL_TEST_PG_ALLOW_ROLES") != "1" {
		t.Skip("creates a login role (cluster-wide); set DBL_TEST_PG_ALLOW_ROLES=1 on a throw-away server to run it")
	}
	f := newReadFixture(t)
	m := f.describe(t, "items")
	// A user that exists only for this test, with no privilege on the table.
	role := "dbl_it_ro_" + strings.TrimPrefix(f.schema, "dbl_it_")
	ctx := context.Background()
	if _, err := f.ad.db.ExecContext(ctx, "CREATE ROLE "+quoteIdent(role)+" LOGIN PASSWORD 'x'"); err != nil {
		t.Skipf("cannot create a role (%v); skipping the privilege test", err)
	}
	t.Cleanup(func() {
		_, _ = f.ad.db.ExecContext(context.Background(), "DROP OWNED BY "+quoteIdent(role))
		_, _ = f.ad.db.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+quoteIdent(role))
	})
	_, _ = f.ad.db.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+quoteIdent(f.schema)+" TO "+quoteIdent(role))
	p, _ := integrationProfile(t)
	p.Username = role
	ad, err := Connect(ctx, p, "x")
	if err != nil {
		t.Skipf("cannot connect as the test role: %v", err)
	}
	defer ad.Close()
	// Metadata is readable from pg_catalog without privileges on the table...
	if _, err := ad.(*Adapter).DescribeTable(ctx, database.TableRef{Schema: f.schema, Name: "items"}); err != nil {
		t.Errorf("describe without privilege: %v", err)
	}
	// ...but the rows are not.
	if _, err := ad.(*Adapter).FetchPage(ctx, database.PageRequest{Meta: m, Size: 10}); database.KindOf(err) != database.KindPermission {
		t.Errorf("read without privilege: kind %v (%v)", database.KindOf(err), err)
	}
}
