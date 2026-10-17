package postgres

import (
	"strings"
	"testing"

	"dbl/internal/database"
)

func TestSelectPostgresSQL(t *testing.T) {
	for _, text := range []string{
		"SELECT 'a;b'; SELECT 2;",
		"SELECT \"a;b\"; SELECT 2;",
		"/* outer /* ; */ */ SELECT 1; SELECT 2;",
		"DO $body$ BEGIN PERFORM 1; END; $body$; SELECT 2;",
		"SELECT 'tiếng Việt 😀;'; SELECT 2;",
		`SELECT E'a\';b'; SELECT 2;`,
	} {
		got, err := SelectPostgresSQL(database.SQLInput{Text: text, Cursor: strings.LastIndex(text, "2")}, true)
		if err != nil || got.SQL != "SELECT 2;" {
			t.Errorf("%q -> %+v, %v", text, got, err)
		}
	}
	text := "select 1; select 2;"
	got, err := SelectPostgresSQL(database.SQLInput{Text: text, Selections: []database.SQLSpan{{Start: 0, End: len(text)}}}, true)
	if err != nil || got.SQL != text {
		t.Fatalf("selection %+v %v", got, err)
	}
	got, err = SelectPostgresSQL(database.SQLInput{Text: "-- no SQL;", Cursor: 3}, true)
	if err != nil || got.SQL != "" {
		t.Fatal("comments should not execute")
	}
	_, err = SelectPostgresSQL(database.SQLInput{Text: text, Selections: []database.SQLSpan{{0, 8}, {10, 18}}}, true)
	if err == nil {
		t.Fatal("multiple selections accepted")
	}
}
func TestUnsupportedScriptNeedsSelection(t *testing.T) {
	text := "CREATE FUNCTION f() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT 1; END;"
	if _, err := SelectPostgresSQL(database.SQLInput{Text: text}, true); err == nil {
		t.Fatal("guessed SQL-standard body boundary")
	}
	if got, err := SelectPostgresSQL(database.SQLInput{Text: text, Selections: []database.SQLSpan{{0, len(text)}}}, true); err != nil || got.SQL != text {
		t.Fatal(got, err)
	}
	for _, text := range []string{"\\dt", "COPY t FROM STDIN;", "select 1; COPY t TO STDOUT;"} {
		if _, err := SelectPostgresSQL(database.SQLInput{Text: text, Selections: []database.SQLSpan{{0, len(text)}}}, true); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}
func TestSelectPostgresSQLSessionEscaping(t *testing.T) {
	text := "SELECT 'a\\'; SELECT 2;"
	if _, err := SelectPostgresSQL(database.SQLInput{Text: text, Cursor: len(text)}, false); err == nil {
		t.Fatal("unterminated legacy string was guessed")
	}
	got, err := SelectPostgresSQL(database.SQLInput{Text: text, Cursor: len(text)}, true)
	if err != nil || got.SQL != "SELECT 2;" {
		t.Fatal(got, err)
	}
}
