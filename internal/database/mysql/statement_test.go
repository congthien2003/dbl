package mysql

import (
	"strings"
	"testing"

	"dbl/internal/database"
)

func statementTexts(t *testing.T, s string, noBackslash bool) []string {
	t.Helper()
	stmts, err := scanSQLStatements(s, noBackslash)
	if err != nil {
		t.Fatalf("scan %q: %v", s, err)
	}
	out := make([]string, len(stmts))
	for i, st := range stmts {
		out[i] = s[st.span.Start:st.span.End]
	}
	return out
}

func TestScanSplitsOnSemicolons(t *testing.T) {
	got := statementTexts(t, "SELECT 1; SELECT 2 ;\nSELECT 3", false)
	want := []string{"SELECT 1;", "SELECT 2 ;", "SELECT 3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
}

func TestScanStringsAndQuotedNames(t *testing.T) {
	s := "SELECT 'a;b', \"c;d\", `e;f`, 'it''s;', 'back\\';slash' FROM t; SELECT 2"
	got := statementTexts(t, s, false)
	if len(got) != 2 || !strings.HasSuffix(got[0], "FROM t;") {
		t.Errorf("got %q", got)
	}
	// Under NO_BACKSLASH_ESCAPES the backslash is plain, so 'x\' ends there.
	got = statementTexts(t, `SELECT 'x\'; SELECT 2`, true)
	if len(got) != 2 {
		t.Errorf("no backslash escapes: %q", got)
	}
	if _, err := scanSQLStatements(`SELECT 'x\'; SELECT 2`, false); err == nil {
		t.Error("with backslash escapes the string is unterminated")
	}
}

func TestScanComments(t *testing.T) {
	s := "# a; comment\nSELECT 1 -- b; comment\n; /* c; */ SELECT 2--3;\nSELECT 4"
	got := statementTexts(t, s, false)
	want := []string{"SELECT 1 -- b; comment\n;", "SELECT 2--3;", "SELECT 4"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q\nwant %q", got, want)
	}
	got = statementTexts(t, "/*!40101 SET NAMES utf8mb4 */; SELECT 1", false)
	if len(got) != 2 || got[0] != "/*!40101 SET NAMES utf8mb4 */;" {
		t.Errorf("executable comment: %q", got)
	}
	if _, err := scanSQLStatements("SELECT 1 /* open", false); err == nil {
		t.Error("an unterminated comment must be refused")
	}
}

func TestScanRoutineBodies(t *testing.T) {
	proc := "CREATE DEFINER=`root`@`%` PROCEDURE p(IN n INT)\nBEGIN\n  DECLARE i INT DEFAULT 0;\n  lbl: WHILE i < n DO\n    IF i = 2 THEN SET i = i + 1; ELSE SET i = i + 1; END IF;\n    CASE WHEN i > 5 THEN LEAVE lbl; ELSE BEGIN END; END CASE;\n  END WHILE lbl;\n  SELECT CASE WHEN n > 0 THEN 'p' ELSE 'n' END;\n  REPEAT SET i = i - 1; UNTIL i = 0 END REPEAT;\n  l2: LOOP LEAVE l2; END LOOP;\nEND;"
	got := statementTexts(t, proc+"\nSELECT 9;", false)
	if len(got) != 2 || got[0] != proc || got[1] != "SELECT 9;" {
		t.Errorf("procedure:\n%q", got)
	}
	trig := "CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW BEGIN SET NEW.a = 1; SET NEW.b = IF(NEW.c, 1, 2); END;"
	got = statementTexts(t, trig+" SELECT 1;", false)
	if len(got) != 2 || got[0] != trig {
		t.Errorf("trigger: %q", got)
	}
	event := "CREATE OR REPLACE EVENT e ON SCHEDULE EVERY 1 DAY DO BEGIN DELETE FROM t; END;"
	got = statementTexts(t, event+" SELECT 1;", false)
	if len(got) != 2 || got[0] != event {
		t.Errorf("event: %q", got)
	}
	// BEGIN outside a stored program starts a transaction.
	got = statementTexts(t, "BEGIN; UPDATE t SET a = 1; COMMIT;", false)
	if len(got) != 3 {
		t.Errorf("transaction: %q", got)
	}
}

func TestDelimiterIsRefused(t *testing.T) {
	_, err := scanSQLStatements("DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; END$$\nDELIMITER ;", false)
	if database.KindOf(err) != database.KindValidation || !strings.Contains(err.Error(), "DELIMITER") {
		t.Errorf("got %v", err)
	}
	// A column named delimiter is fine.
	if got := statementTexts(t, "SELECT delimiter FROM t", false); len(got) != 1 {
		t.Errorf("got %q", got)
	}
}

func TestSelectMySQLSQL(t *testing.T) {
	text := "SELECT 1;\nSELECT 'a;b';\nSELECT 3"
	sel, err := SelectMySQLSQL(database.SQLInput{Text: text, Cursor: strings.Index(text, "'a")}, false)
	if err != nil || sel.SQL != "SELECT 'a;b';" {
		t.Errorf("cursor: %q %v", sel.SQL, err)
	}
	sel, err = SelectMySQLSQL(database.SQLInput{Text: text, Cursor: len(text)}, false)
	if err != nil || sel.SQL != "SELECT 3" {
		t.Errorf("end: %q %v", sel.SQL, err)
	}
	sel, err = SelectMySQLSQL(database.SQLInput{Text: text, Cursor: 0, Selections: []database.SQLSpan{{Start: 0, End: 9}}}, false)
	if err != nil || sel.SQL != "SELECT 1;" {
		t.Errorf("selection: %q %v", sel.SQL, err)
	}
	sel, err = SelectMySQLSQL(database.SQLInput{Text: "  -- nothing\n", Cursor: 0}, false)
	if err != nil || sel.SQL != "" {
		t.Errorf("empty: %q %v", sel.SQL, err)
	}
	if _, err := SelectMySQLSQL(database.SQLInput{Text: "SELECT 1", Cursor: 99}, false); err == nil {
		t.Error("cursor outside must fail")
	}
}
