package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"dbl/internal/database"
)

func testMeta() *database.TableMetadata {
	m := &database.TableMetadata{
		Ref: database.TableRef{Catalog: "app", Schema: `we"ird`, Name: "Users; DROP"},
		Columns: []database.ColumnMetadata{
			{Name: "id", Class: database.ClassNumber, NativeType: "integer"},
			{Name: `na"me`, Class: database.ClassText, Nullable: true, NativeType: "text"},
			{Name: "blob", Class: database.ClassBinary, Nullable: true, NativeType: "bytea"},
			{Name: "doc", Class: database.ClassJSON, Nullable: true, NativeType: "jsonb"},
		},
		Constraints: database.Section[database.ConstraintMetadata]{Items: []database.ConstraintMetadata{
			{Type: database.ConstraintPrimaryKey, Columns: []string{"id"}},
		}},
	}
	m.ResolveIdentity()
	return m
}

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"users":        `"users"`,
		`a"b`:          `"a""b"`,
		"x; DROP":      `"x; DROP"`,
		"Ünïcode name": `"Ünïcode name"`,
		"select":       `"select"`,
	}
	for in, want := range cases {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPageQueryKeysetFirst(t *testing.T) {
	pq, err := buildPageQuery(database.PageRequest{Meta: testMeta(), Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id"::text, "na""me"::text, octet_length("blob"), "doc"::text FROM "we""ird"."Users; DROP" ORDER BY "id" LIMIT $1`
	if pq.SQL != want {
		t.Errorf("SQL\n got %s\nwant %s", pq.SQL, want)
	}
	if len(pq.Args) != 1 || pq.Args[0] != int64(101) {
		t.Errorf("args %v", pq.Args)
	}
	if !pq.Binary[2] || pq.Binary[0] || pq.Reverse {
		t.Errorf("binary %v reverse %v", pq.Binary, pq.Reverse)
	}
}

func TestPageQueryKeysetAfterBeforeLast(t *testing.T) {
	m := testMeta()
	pq, _ := buildPageQuery(database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorAfter, Key: []string{"7"}}})
	if !strings.Contains(pq.SQL, `WHERE ("id") > ($1) ORDER BY "id" LIMIT $2`) || pq.Args[0] != "7" || pq.Reverse {
		t.Errorf("after: %s %v", pq.SQL, pq.Args)
	}
	pq, _ = buildPageQuery(database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorBefore, Key: []string{"7"}}})
	if !strings.Contains(pq.SQL, `WHERE ("id") < ($1) ORDER BY "id" DESC LIMIT $2`) || !pq.Reverse {
		t.Errorf("before: %s", pq.SQL)
	}
	pq, _ = buildPageQuery(database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorLast}})
	if strings.Contains(pq.SQL, "WHERE") || !strings.Contains(pq.SQL, `ORDER BY "id" DESC LIMIT $1`) || !pq.Reverse {
		t.Errorf("last: %s", pq.SQL)
	}
}

func TestPageQueryOffsetSortAndFilter(t *testing.T) {
	m := testMeta()
	pq, err := buildPageQuery(database.PageRequest{
		Meta: m, Size: 50, Offset: 100,
		Sort:   &database.Sort{Column: `na"me`, Desc: true},
		Filter: &database.Filter{Column: "id", Op: database.OpGreater, Value: "5'; DROP TABLE x;--"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pq.SQL, `WHERE "id" > $1 ORDER BY "na""me" DESC, "id" LIMIT $2 OFFSET $3`) {
		t.Errorf("SQL %s", pq.SQL)
	}
	if strings.Contains(pq.SQL, "DROP TABLE") {
		t.Error("a typed value reached the SQL text")
	}
	if pq.Args[0] != "5'; DROP TABLE x;--" || pq.Args[1] != int64(51) || pq.Args[2] != int64(100) {
		t.Errorf("args %v", pq.Args)
	}
}

func TestFilterClauses(t *testing.T) {
	m := testMeta()
	cases := []struct {
		f    database.Filter
		sql  string
		args int
	}{
		{database.Filter{Column: "id", Op: database.OpEquals, Value: "1"}, `WHERE "id" = $1 `, 3},
		{database.Filter{Column: "id", Op: database.OpNotEquals, Value: "1"}, `WHERE "id" <> $1 `, 3},
		{database.Filter{Column: "id", Op: database.OpLess, Value: "1"}, `WHERE "id" < $1 `, 3},
		{database.Filter{Column: `na"me`, Op: database.OpContains, Value: "a_b"}, `WHERE "na""me"::text ILIKE $1 `, 3},
		{database.Filter{Column: `na"me`, Op: database.OpIsNull}, `WHERE "na""me" IS NULL `, 2},
		{database.Filter{Column: "blob", Op: database.OpIsNotNull}, `WHERE "blob" IS NOT NULL `, 2},
	}
	for _, tc := range cases {
		f := tc.f
		pq, err := buildPageQuery(database.PageRequest{Meta: m, Size: 10, Sort: &database.Sort{Column: "id"}, Filter: &f})
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		if !strings.Contains(pq.SQL, tc.sql) {
			t.Errorf("%+v: SQL %s", f, pq.SQL)
		}
		// Sort is set, so the strategy is offset; with Offset 0 there is no OFFSET.
		if len(pq.Args) != tc.args-1 && len(pq.Args) != tc.args {
			t.Errorf("%+v: args %v", f, pq.Args)
		}
	}
}

func TestContainsPatternEscapes(t *testing.T) {
	if got := containsPattern(`50%_off\`); got != `%50\%\_off\\%` {
		t.Errorf("pattern %q", got)
	}
}

func TestPageQueryRejectsBadRequests(t *testing.T) {
	m := testMeta()
	bad := []database.PageRequest{
		{Meta: m, Size: 10, Sort: &database.Sort{Column: `x" FROM y; --`}},
		{Meta: m, Size: 10, Sort: &database.Sort{Column: "doc"}},
		{Meta: m, Size: 10, Filter: &database.Filter{Column: "doc", Op: database.OpEquals, Value: "1"}},
		{Meta: m, Size: 10, Filter: &database.Filter{Column: "id", Op: database.OpContains, Value: "1"}},
	}
	for i, r := range bad {
		if _, err := buildPageQuery(r); err == nil {
			t.Errorf("request %d accepted", i)
		}
	}
}

func TestClassOf(t *testing.T) {
	cases := []struct {
		name, cat string
		want      database.ColumnClass
	}{
		{"int4", "N", database.ClassNumber},
		{"numeric", "N", database.ClassNumber},
		{"text", "S", database.ClassText},
		{"mood", "E", database.ClassText},
		{"bool", "B", database.ClassBool},
		{"timestamptz", "D", database.ClassDateTime},
		{"interval", "T", database.ClassDateTime},
		{"bytea", "U", database.ClassBinary},
		{"jsonb", "U", database.ClassJSON},
		{"uuid", "U", database.ClassOrdered},
		{"inet", "I", database.ClassOrdered},
		{"money", "N", database.ClassOther},
		{"xml", "U", database.ClassOther},
		{"_int4", "A", database.ClassOther},
		{"point", "G", database.ClassOther},
	}
	for _, tc := range cases {
		if got := classOf(tc.name, tc.cat); got != tc.want {
			t.Errorf("classOf(%s,%s) = %v, want %v", tc.name, tc.cat, got, tc.want)
		}
	}
}

func TestTriggerDecoding(t *testing.T) {
	timing, events, row := triggerKind(1 | 2 | 4 | 16) // row, before, insert, update
	if timing != "BEFORE" || !row || len(events) != 2 || events[0] != "INSERT" || events[1] != "UPDATE" {
		t.Errorf("%s %v %v", timing, events, row)
	}
	if timing, _, _ := triggerKind(64 | 1 | 4); timing != "INSTEAD OF" {
		t.Errorf("timing %s", timing)
	}
	if timing, _, row := triggerKind(8); timing != "AFTER" || row {
		t.Errorf("timing %s row %v", timing, row)
	}
	if triggerEnabled("D") != "disabled" || triggerEnabled("O") != "enabled" {
		t.Error("triggerEnabled")
	}
}

func TestRelationKind(t *testing.T) {
	if k, s := relationKind("m", false); k != database.ObjectView || s != "materialized view" {
		t.Errorf("%v %q", k, s)
	}
	if k, s := relationKind("r", true); k != database.ObjectTable || s != "partition" {
		t.Errorf("%v %q", k, s)
	}
	if k, _ := relationKind("v", false); k != database.ObjectView {
		t.Error("view")
	}
}

func TestClassifyQuery(t *testing.T) {
	pg := func(code, msg string) error { return &pgconn.PgError{Code: code, Message: msg} }
	cases := []struct {
		err  error
		want database.Kind
	}{
		{pg("42501", "permission denied for table t"), database.KindPermission},
		{pg("42P01", "relation does not exist"), database.KindMetadataUnavailable},
		{pg("22P02", "invalid input syntax for type integer"), database.KindValidation},
		{pg("42883", "operator does not exist"), database.KindUnsupported},
		{pg("42601", "syntax error"), database.KindSyntax},
		{pg("57014", "canceling statement"), database.KindTimeout},
		{pg("08006", "connection failure"), database.KindConnection},
		{errors.New("sql: database is closed"), database.KindConnection},
	}
	for _, tc := range cases {
		if got := classifyQuery(context.Background(), tc.err, "Reading"); got.Kind != tc.want {
			t.Errorf("%v: kind %v, want %v", tc.err, got.Kind, tc.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := classifyQuery(ctx, errors.New("whatever"), "Reading"); got.Kind != database.KindCanceled {
		t.Errorf("canceled ctx: %v", got.Kind)
	}
	if classifyQuery(context.Background(), nil, "x") != nil {
		t.Error("nil error")
	}
}
