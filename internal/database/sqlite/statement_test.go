package sqlite

import (
	"strings"
	"testing"

	"dbl/internal/database"
)

// at selects the statement at the cursor, which is the position of marker
// in text.
func at(t *testing.T, text, marker string) string {
	t.Helper()
	got, err := SelectSQLiteSQL(database.SQLInput{Text: text, Cursor: strings.Index(text, marker)})
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return got.SQL
}

func TestSelectionPrefersSelectedText(t *testing.T) {
	text := "select 1; select 2; select 3;"
	got, err := SelectSQLiteSQL(database.SQLInput{Text: text, Cursor: 0, Selections: []database.SQLSpan{{Start: 10, End: 29}}})
	if err != nil || got.SQL != "select 2; select 3;" || got.Span != (database.SQLSpan{Start: 10, End: 29}) {
		t.Errorf("selection: %+v, %v", got, err)
	}
	// An empty selection is no selection.
	got, err = SelectSQLiteSQL(database.SQLInput{Text: text, Cursor: 12, Selections: []database.SQLSpan{{Start: 5, End: 5}}})
	if err != nil || got.SQL != "select 2;" {
		t.Errorf("empty selection: %+v, %v", got, err)
	}
	if _, err := SelectSQLiteSQL(database.SQLInput{Text: text, Selections: []database.SQLSpan{{Start: 0, End: 8}, {Start: 10, End: 18}}}); err == nil {
		t.Error("two selections were accepted")
	}
	if _, err := SelectSQLiteSQL(database.SQLInput{Text: text, Cursor: 99}); err == nil {
		t.Error("a cursor outside the text was accepted")
	}
}

func TestStatementAtCursorWithStringsAndComments(t *testing.T) {
	for _, text := range []string{
		"SELECT 'a;b'; SELECT 2;",
		"SELECT 'it''s; ok'; SELECT 2;",
		"SELECT 1 -- ; not an end\n; SELECT 2;",
		"/* one; two */ SELECT 1; SELECT 2;",
		"/* never closes; SELECT 9 */ SELECT 1; SELECT 2;",
		"SELECT 'tiếng Việt 😀;'; SELECT 2;",
	} {
		if got := at(t, text, "2"); got != "SELECT 2;" {
			t.Errorf("%q -> %q", text, got)
		}
	}
	// A backslash is not an escape in SQLite: the string ends at its quote.
	text := "SELECT 'a" + string(rune(92)) + "'; SELECT 2;"
	if got := at(t, text, "2"); got != "SELECT 2;" {
		t.Errorf("%q -> %q", text, got)
	}
}

func TestStatementAtCursorQuotedIdentifiersWithSemicolons(t *testing.T) {
	for _, text := range []string{
		`SELECT "a;b" FROM t; SELECT 2;`,
		"SELECT `a;b` FROM t; SELECT 2;",
		"SELECT [a;b] FROM t; SELECT 2;",
		`SELECT "say ""hi;""" ; SELECT 2;`,
	} {
		if got := at(t, text, "2"); got != "SELECT 2;" {
			t.Errorf("%q -> %q", text, got)
		}
		if got := at(t, text, "SELECT"); !strings.HasPrefix(got, "SELECT ") || strings.Contains(got, "SELECT 2") {
			t.Errorf("%q first -> %q", text, got)
		}
	}
	for _, text := range []string{`SELECT "open; SELECT 2;`, "SELECT [open; SELECT 2;", "SELECT 'open; SELECT 2;"} {
		if _, err := SelectSQLiteSQL(database.SQLInput{Text: text}); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestCreateTriggerBlockIsOneStatement(t *testing.T) {
	trigger := `CREATE TRIGGER t_ai AFTER INSERT ON t
WHEN NEW.x > 0
BEGIN
  INSERT INTO log VALUES (NEW.id, 'a;b'); -- note; here
  UPDATE t SET y = CASE WHEN NEW.x > 1 THEN 'big' ELSE 'small' END WHERE id = NEW.id;
  /* block; comment */
  DELETE FROM "weird;name" WHERE id = 1;
END;`
	text := "SELECT 1;\n" + trigger + "\nSELECT 2;"
	if got := at(t, text, "SELECT 2"); got != "SELECT 2;" {
		t.Errorf("after the trigger: %q", got)
	}
	for _, marker := range []string{"CREATE TRIGGER", "BEGIN", "UPDATE t SET", "DELETE FROM", "END;"} {
		if got := at(t, text, marker); got != trigger {
			t.Errorf("cursor at %q selected %q", marker, got)
		}
	}
	if got := at(t, text, "SELECT 1"); got != "SELECT 1;" {
		t.Errorf("before the trigger: %q", got)
	}

	// TEMP triggers and lower case.
	temp := "create temp trigger x before delete on t begin delete from u; end;"
	if got := at(t, temp+" select 5;", "delete from u"); got != temp {
		t.Errorf("temp trigger: %q", got)
	}
	// BEGIN as a transaction statement is not a trigger body.
	if got := at(t, "begin; select 2; commit;", "select 2"); got != "select 2;" {
		t.Errorf("BEGIN: %q", got)
	}
	// A trigger that never ends takes the rest of the text.
	open := "create trigger x after insert on t begin select 1; select 2;"
	if got := at(t, open, "select 2"); got != open {
		t.Errorf("open trigger: %q", got)
	}
	// A column named like a keyword, quoted, is not a keyword.
	quoted := `create trigger x after insert on t begin update t set "end" = 1; end; select 3;`
	if got := at(t, quoted, "select 3"); got != "select 3;" {
		t.Errorf("quoted END: %q", got)
	}
}

func TestCursorAtBoundaryBetweenStatements(t *testing.T) {
	text := "select 1; select 2;"
	// Like the PostgreSQL editor: a cursor right after the semicolon belongs
	// to the next statement, and the last statement takes the end.
	for cursor, want := range map[int]string{
		0:  "select 1;",
		8:  "select 1;",
		9:  "select 2;",
		10: "select 2;",
		19: "select 2;",
	} {
		got, err := SelectSQLiteSQL(database.SQLInput{Text: text, Cursor: cursor})
		if err != nil || got.SQL != want {
			t.Errorf("cursor %d -> %q (%v), want %q", cursor, got.SQL, err, want)
		}
	}
}

func TestEmptyOrCommentOnlyInput(t *testing.T) {
	for _, text := range []string{"", "   \n", "-- just a note;", "/* only; a comment */", ";;;", " ; ; "} {
		got, err := SelectSQLiteSQL(database.SQLInput{Text: text, Cursor: len(text) / 2})
		if err != nil || got.SQL != "" {
			t.Errorf("%q -> %+v, %v", text, got, err)
		}
	}
	if _, err := SelectSQLiteSQL(database.SQLInput{Text: "select 1\x00"}); err == nil {
		t.Error("NUL was accepted")
	}
	if _, err := SelectSQLiteSQL(database.SQLInput{Text: "select '\xff'"}); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
}

func TestScanKeepsStatementWordsAndSpans(t *testing.T) {
	text := "  create unique index i on t(a);\n insert into t values ('x');"
	st, err := scanSQLStatements(text)
	if err != nil || len(st) != 2 {
		t.Fatalf("%+v, %v", st, err)
	}
	if text[st[0].span.Start:st[0].span.End] != "create unique index i on t(a);" || st[0].words[0] != "CREATE" {
		t.Errorf("first: %+v", st[0])
	}
	if text[st[1].span.Start:st[1].span.End] != "insert into t values ('x');" || st[1].words[0] != "INSERT" {
		t.Errorf("second: %+v", st[1])
	}
}
