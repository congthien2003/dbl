package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

func TestQuoteIdentEscapesQuotes(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       `"plain"`,
		`we"ird`:      `"we""ird"`,
		"a b; DROP":   `"a b; DROP"`,
		"tên cột":     `"tên cột"`,
		`""`:          `""""""`,
		"select":      `"select"`,
		"x'; --":      `"x'; --"`,
		"line\nbreak": "\"line\nbreak\"",
	} {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
}

// pageDB is a table of n numbered rows with a nullable text column.
func pageDB(t *testing.T, n int) (*Adapter, *database.TableMetadata) {
	t.Helper()
	ddl := []string{"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, score REAL)"}
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("'item %03d'", i)
		if i%5 == 0 {
			name = "NULL"
		}
		ddl = append(ddl, fmt.Sprintf("INSERT INTO items VALUES (%d, %s, %d.5)", i, name, i))
	}
	a := connect(t, profileFor(newTestDB(t, ddl...)))
	return a, describe(t, a, "items")
}

func rowIDs(res *database.PageResult) []string {
	var ids []string
	for _, r := range res.Rows {
		ids = append(ids, r[0].Text)
	}
	return ids
}

func TestBuildPageQueryOffsetAndKeyset(t *testing.T) {
	_, m := pageDB(t, 3)

	// No sort and a usable key: keyset, ordered by the key.
	pq, err := buildPageQuery(database.PageRequest{Meta: m, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pq.SQL, `FROM "main"."items" ORDER BY "id" LIMIT ?`) || len(pq.Args) != 1 || pq.Args[0] != int64(11) {
		t.Errorf("first page: %s %v", pq.SQL, pq.Args)
	}
	if strings.Contains(pq.SQL, "OFFSET") {
		t.Errorf("a keyset page used OFFSET: %s", pq.SQL)
	}

	pq, err = buildPageQuery(database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorAfter, Key: []string{"7"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pq.SQL, `WHERE ("id") > (?) ORDER BY "id" LIMIT ?`) || len(pq.Args) != 2 || pq.Args[0] != "7" {
		t.Errorf("after: %s %v", pq.SQL, pq.Args)
	}

	pq, _ = buildPageQuery(database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorBefore, Key: []string{"7"}}})
	if !strings.Contains(pq.SQL, `("id") < (?) ORDER BY "id" DESC`) || !pq.Reverse {
		t.Errorf("before: %s", pq.SQL)
	}
	pq, _ = buildPageQuery(database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorLast}})
	if !strings.Contains(pq.SQL, `ORDER BY "id" DESC LIMIT ?`) || !pq.Reverse {
		t.Errorf("last: %s", pq.SQL)
	}

	// A sort: offset paging, the key breaks ties.
	pq, err = buildPageQuery(database.PageRequest{Meta: m, Size: 10, Offset: 20, Sort: &database.Sort{Column: "name", Desc: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pq.SQL, `ORDER BY "name" DESC, "id" LIMIT ? OFFSET ?`) || len(pq.Args) != 2 || pq.Args[1] != int64(20) {
		t.Errorf("offset: %s %v", pq.SQL, pq.Args)
	}

	if _, err := buildPageQuery(database.PageRequest{Meta: m, Size: 10, Sort: &database.Sort{Column: `x"; DROP TABLE items; --`}}); err == nil {
		t.Error("an unknown sort column reached the query")
	}
}

func TestFilterOperatorsBindParameters(t *testing.T) {
	_, m := pageDB(t, 3)
	const evil = "x' OR 1=1 --"
	for _, op := range []database.FilterOp{database.OpEquals, database.OpNotEquals, database.OpContains, database.OpGreater, database.OpLess} {
		pq, err := buildPageQuery(database.PageRequest{Meta: m, Size: 10, Filter: &database.Filter{Column: "name", Op: op, Value: evil}})
		if err != nil {
			t.Fatalf("%s: %v", op.Label(), err)
		}
		if strings.Contains(pq.SQL, "OR 1=1") || strings.Contains(pq.SQL, evil) {
			t.Errorf("%s: the value is in the SQL text: %s", op.Label(), pq.SQL)
		}
		found := false
		for _, a := range pq.Args {
			if s, ok := a.(string); ok && strings.Contains(s, evil) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the value is not a bound argument: %v", op.Label(), pq.Args)
		}
	}
	for _, op := range []database.FilterOp{database.OpIsNull, database.OpIsNotNull} {
		pq, err := buildPageQuery(database.PageRequest{Meta: m, Size: 10, Filter: &database.Filter{Column: "name", Op: op}})
		if err != nil || len(pq.Args) != 1 {
			t.Errorf("%s: %v %v", op.Label(), err, pq.Args)
		}
	}
}

func TestLikeEscapesWildcards(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE w (id INTEGER PRIMARY KEY, s TEXT)",
		`INSERT INTO w VALUES (1, '50% off'), (2, '50 percent'), (3, 'a_b'), (4, 'axb'), (5, 'back\slash'), (6, 'backslash')`,
	)))
	m := describe(t, a, "w")
	for filter, want := range map[string]string{"50%": "1", "a_b": "3", `\`: "5", "BACK": "5,6"} {
		res, err := a.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 10, Filter: &database.Filter{Column: "s", Op: database.OpContains, Value: filter}})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(rowIDs(res), ","); got != want {
			t.Errorf("contains %q: rows %s, want %s", filter, got, want)
		}
	}
}

func TestFetchPageMoreFlag(t *testing.T) {
	a, m := pageDB(t, 25)
	ctx := context.Background()
	res, err := a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10})
	if err != nil || len(res.Rows) != 10 || !res.More {
		t.Fatalf("first page: %d rows more=%v err=%v", len(res.Rows), res.More, err)
	}
	last := res.Rows[9][0].Text
	res, err = a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorAfter, Key: []string{last}}})
	if err != nil || len(res.Rows) != 10 || !res.More || res.Rows[0][0].Text != "11" {
		t.Fatalf("second page: %v err=%v", rowIDs(res), err)
	}
	res, err = a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorLast}})
	if err != nil || len(res.Rows) != 10 || !res.More || res.Rows[0][0].Text != "16" || res.Rows[9][0].Text != "25" {
		t.Fatalf("last page: %v more=%v err=%v", rowIDs(res), res.More, err)
	}
	res, err = a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10, Cursor: database.Cursor{Dir: database.CursorBefore, Key: []string{"11"}}})
	if err != nil || len(res.Rows) != 10 || res.More || res.Rows[0][0].Text != "1" || res.Rows[9][0].Text != "10" {
		t.Fatalf("page before 11: %v more=%v err=%v", rowIDs(res), res.More, err)
	}
	res, err = a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10, Offset: 20, Sort: &database.Sort{Column: "score"}})
	if err != nil || len(res.Rows) != 5 || res.More {
		t.Fatalf("offset page: %v more=%v err=%v", rowIDs(res), res.More, err)
	}
}

func TestFetchPageMixedTypeValues(t *testing.T) {
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE mixed (id INTEGER PRIMARY KEY, n INTEGER, t TEXT, b BLOB, d DATETIME, flag BOOLEAN, r REAL)",
		"INSERT INTO mixed VALUES (1, 'abc', 'x', x'00ff10', '2024-01-02 03:04:05', 1, 2)",
		"INSERT INTO mixed VALUES (2, 9007199254740993, NULL, NULL, 1700000000, 0, 0.5)",
		"INSERT INTO mixed VALUES (3, '7x', 'y', 'plain text in a blob column', NULL, 'true', NULL)",
		"INSERT INTO mixed VALUES (4, NULL, x'0102', NULL, NULL, NULL, NULL)",
	)))
	m := describe(t, a, "mixed")
	res, err := a.FetchPage(context.Background(), database.PageRequest{Meta: m, Size: 10})
	if err != nil || len(res.Rows) != 4 {
		t.Fatalf("rows %d, err %v", len(res.Rows), err)
	}
	text := func(s string) grid.Value { return grid.Value{Kind: grid.KindText, Text: s} }
	num := func(s string) grid.Value { return grid.Value{Kind: grid.KindNumber, Text: s} }
	null := grid.Value{Kind: grid.KindNull}
	want := [][]grid.Value{
		{num("1"), text("abc"), text("x"), {Kind: grid.KindBinary, Size: 3}, {Kind: grid.KindDateTime, Text: "2024-01-02 03:04:05"}, num("1"), num("2.0")},
		{num("2"), num("9007199254740993"), null, null, num("1700000000"), num("0"), num("0.5")},
		{num("3"), text("7x"), text("y"), text("plain text in a blob column"), null, text("true"), null},
		{num("4"), null, {Kind: grid.KindBinary, Size: 2}, null, null, null, null},
	}
	for i := range want {
		for j := range want[i] {
			if res.Rows[i][j] != want[i][j] {
				t.Errorf("row %d column %s: %+v, want %+v", i+1, m.Columns[j].Name, res.Rows[i][j], want[i][j])
			}
		}
	}
}

func TestFetchPageHonorsCancel(t *testing.T) {
	a, m := pageDB(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10})
	if database.KindOf(err) != database.KindCanceled {
		t.Errorf("error %v (kind %v)", err, database.KindOf(err))
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	_, err = a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10})
	if database.KindOf(err) != database.KindTimeout {
		t.Errorf("error %v (kind %v)", err, database.KindOf(err))
	}
}

func TestFetchPageSortOnNullableColumn(t *testing.T) {
	a, m := pageDB(t, 10)
	ctx := context.Background()
	asc, err := a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10, Sort: &database.Sort{Column: "name"}})
	if err != nil {
		t.Fatal(err)
	}
	// SQLite puts NULL first in ascending order; ties break by the key.
	if got := strings.Join(rowIDs(asc), ","); got != "5,10,1,2,3,4,6,7,8,9" {
		t.Errorf("ascending: %s", got)
	}
	desc, err := a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 10, Sort: &database.Sort{Column: "name", Desc: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rowIDs(desc), ","); got != "9,8,7,6,4,3,2,1,5,10" {
		t.Errorf("descending: %s", got)
	}
}

func TestFilterByNumberAndNull(t *testing.T) {
	a, m := pageDB(t, 10)
	ctx := context.Background()
	for _, c := range []struct {
		f    database.Filter
		want string
	}{
		{database.Filter{Column: "id", Op: database.OpEquals, Value: "3"}, "3"},
		{database.Filter{Column: "id", Op: database.OpGreater, Value: "8"}, "9,10"},
		{database.Filter{Column: "id", Op: database.OpLess, Value: "3"}, "1,2"},
		{database.Filter{Column: "score", Op: database.OpEquals, Value: "2.5"}, "2"},
		{database.Filter{Column: "name", Op: database.OpIsNull}, "5,10"},
	} {
		f := c.f
		res, err := a.FetchPage(ctx, database.PageRequest{Meta: m, Size: 20, Filter: &f})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(rowIDs(res), ","); got != c.want {
			t.Errorf("%+v: rows %s, want %s", c.f, got, c.want)
		}
	}
}
