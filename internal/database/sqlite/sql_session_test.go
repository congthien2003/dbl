package sqlite

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"dbl/internal/database"
	"dbl/internal/grid"
)

func openSession(t *testing.T, a *Adapter) database.SQLSession {
	t.Helper()
	s, err := a.OpenSQLSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
	return s
}

func run(t *testing.T, s database.SQLSession, text string) *database.SQLExecution {
	t.Helper()
	r, err := s.Execute(context.Background(), text)
	if err != nil {
		t.Fatalf("Execute(%q): %v", text, err)
	}
	return r
}

func sessionDB(t *testing.T) (*Adapter, database.SQLSession) {
	t.Helper()
	a := connect(t, profileFor(newTestDB(t,
		"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
		"INSERT INTO t VALUES (1, 'a'), (2, 'b'), (3, 'c')",
	)))
	return a, openSession(t, a)
}

func TestExecuteSelectIsBoundedAndReadOnly(t *testing.T) {
	a, s := sessionDB(t)
	r := run(t, s, "SELECT id, name, id * 1.5 AS scaled, NULL AS nada, x'0102' AS bytes FROM t ORDER BY id")
	if r.Stop != database.SQLCompleted || r.Transaction != database.SQLIdle || !r.Ready || r.OutcomeUncertain || r.Err != nil {
		t.Fatalf("execution %+v", r)
	}
	if len(r.Results) != 1 {
		t.Fatalf("%d results", len(r.Results))
	}
	res := r.Results[0]
	var names []string
	for _, c := range res.Columns {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "id,name,scaled,nada,bytes" {
		t.Errorf("columns %v", names)
	}
	if len(res.Rows) != 3 || !res.HasRowsAffected || res.RowsAffected != 3 || res.CommandTag != "SELECT 3" || !res.CommandComplete {
		t.Errorf("result %+v", res)
	}
	want := []grid.Value{{Kind: grid.KindNumber, Text: "1"}, {Kind: grid.KindText, Text: "a"}, {Kind: grid.KindNumber, Text: "1.5"}, {Kind: grid.KindNull}, {Kind: grid.KindBinary, Size: 2}}
	for i, v := range want {
		if res.Rows[0][i] != v {
			t.Errorf("cell %d: %+v, want %+v", i, res.Rows[0][i], v)
		}
	}

	// Past the snapshot the rows are counted but not kept.
	big := run(t, s, "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2500) SELECT i FROM n")
	res = big.Results[0]
	if len(res.Rows) != database.SQLSnapshotRows || !res.Truncated || res.RowsAffected != 2500 {
		t.Errorf("snapshot: %d rows, truncated %v, count %d", len(res.Rows), res.Truncated, res.RowsAffected)
	}
	if got := scalar(t, a, "SELECT count(*) FROM t"); got != int64(3) {
		t.Errorf("a SELECT changed the table: %v", got)
	}
}

func TestExecuteWriteReportsAffectedRows(t *testing.T) {
	a, s := sessionDB(t)
	r := run(t, s, "UPDATE t SET name = 'z' WHERE id > 1")
	res := r.Results[0]
	if len(res.Columns) != 0 || !res.HasRowsAffected || res.RowsAffected != 2 || res.CommandTag != "UPDATE 2" || !res.CommandComplete {
		t.Errorf("update %+v", res)
	}
	r = run(t, s, "INSERT INTO t VALUES (4, 'd'); DELETE FROM t WHERE id = 99; CREATE TABLE u (x); DROP TABLE u")
	if len(r.Results) != 4 {
		t.Fatalf("%d results", len(r.Results))
	}
	tags := []string{}
	for _, res := range r.Results {
		tags = append(tags, res.CommandTag)
	}
	if strings.Join(tags, "|") != "INSERT 1|DELETE 0|CREATE TABLE|DROP TABLE" {
		t.Errorf("tags %v", tags)
	}
	if r.Results[2].HasRowsAffected {
		t.Error("DDL reports affected rows")
	}
	// A trigger's own changes are not the statement's.
	run(t, s, "CREATE TABLE log (x); CREATE TRIGGER t_ai AFTER INSERT ON t BEGIN INSERT INTO log VALUES (1); INSERT INTO log VALUES (2); END;")
	r = run(t, s, "INSERT INTO t VALUES (5, 'e')")
	if r.Results[0].RowsAffected != 1 {
		t.Errorf("insert with a trigger: %+v", r.Results[0])
	}
	if got := scalar(t, a, "SELECT count(*) FROM log"); got != int64(2) {
		t.Errorf("trigger did not run: %v", got)
	}
}

func TestBeginInOneRunCommitInNext(t *testing.T) {
	a, s := sessionDB(t)
	r := run(t, s, "BEGIN; INSERT INTO t VALUES (10, 'x')")
	if r.Transaction != database.SQLInTransaction {
		t.Fatalf("after BEGIN: %v", r.Transaction)
	}
	// Another connection does not see the uncommitted row.
	if got := scalar(t, a, "SELECT count(*) FROM t WHERE id = 10"); got != int64(0) {
		t.Errorf("uncommitted row is visible: %v", got)
	}
	r = run(t, s, "COMMIT")
	if r.Transaction != database.SQLIdle {
		t.Errorf("after COMMIT: %v", r.Transaction)
	}
	if got := scalar(t, a, "SELECT count(*) FROM t WHERE id = 10"); got != int64(1) {
		t.Errorf("committed row is missing: %v", got)
	}
}

func TestFailedStatementInTransactionState(t *testing.T) {
	a, s := sessionDB(t)
	run(t, s, "BEGIN; INSERT INTO t VALUES (20, 'kept in txn')")
	r, err := s.Execute(context.Background(), "INSERT INTO t VALUES (1, 'duplicate'); SELECT 99")
	if err == nil || r == nil {
		t.Fatalf("a failing statement returned %v, %v", r, err)
	}
	// The failure stops the run; a SQLite transaction survives a failed
	// statement and the editor says it is still open.
	if r.Stop != database.SQLFailed || r.Transaction != database.SQLInTransaction || r.Err == nil || !r.Ready || r.OutcomeUncertain {
		t.Errorf("execution %+v", r)
	}
	if len(r.Results) != 0 {
		t.Errorf("results of a statement that failed: %+v", r.Results)
	}
	if r.Err.Kind != database.KindValidation {
		t.Errorf("kind %v: %v", r.Err.Kind, r.Err)
	}
	r = run(t, s, "ROLLBACK")
	if r.Transaction != database.SQLIdle {
		t.Errorf("after ROLLBACK: %v", r.Transaction)
	}
	if got := scalar(t, a, "SELECT count(*) FROM t"); got != int64(3) {
		t.Errorf("rows %v", got)
	}

	// Earlier statements of the run keep their results.
	r, err = s.Execute(context.Background(), "SELECT 1; SELEC 2; SELECT 3")
	if err == nil || len(r.Results) != 1 || database.KindOf(err) != database.KindSyntax {
		t.Errorf("syntax error run: %v %+v", err, r)
	}
}

func TestOpenTransactionBlocksSaveWithClearBusyError(t *testing.T) {
	old := busyTimeoutMs
	busyTimeoutMs = 200
	defer func() { busyTimeoutMs = old }()
	a, _ := sessionDB(t)
	s := openSession(t, a)
	run(t, s, "BEGIN IMMEDIATE; UPDATE t SET name = 'held' WHERE id = 1")

	m := describe(t, a, "t")
	start := time.Now()
	err := a.SaveChanges(context.Background(), request(m, rowOf(2, cell("name", txt("b"), txt("saved")))))
	if time.Since(start) > 3*time.Second {
		t.Errorf("Save waited %v", time.Since(start))
	}
	se := saveError(t, err)
	if se.Err.Message != "Database is locked by another process" || se.Outcome != database.SaveNotApplied {
		t.Errorf("error %+v", se.Err)
	}
	run(t, s, "ROLLBACK")
	if err := a.SaveChanges(context.Background(), request(m, rowOf(2, cell("name", txt("b"), txt("saved"))))); err != nil {
		t.Errorf("after the rollback: %v", err)
	}
}

func TestCancelStopsLongQuery(t *testing.T) {
	_, s := sessionDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	r, err := s.Execute(ctx, "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c")
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the query ran %v after cancel", time.Since(start))
	}
	if err == nil || r == nil || r.Stop != database.SQLCanceled || database.KindOf(err) != database.KindCanceled {
		t.Fatalf("result %+v, error %v", r, err)
	}
	if !r.Ready || r.Transaction != database.SQLIdle {
		t.Errorf("the session is not usable after cancel: %+v", r)
	}
	// The session works again.
	if got := run(t, s, "SELECT 1").Results[0].Rows[0][0].Text; got != "1" {
		t.Errorf("after cancel: %q", got)
	}
}

func TestExecuteTimeoutIsReported(t *testing.T) {
	_, s := sessionDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r, err := s.Execute(ctx, "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c")
	if err == nil || r.Stop != database.SQLTimedOut {
		t.Errorf("result %+v, error %v", r, err)
	}
}

func TestCloseRollsBackOpenTransaction(t *testing.T) {
	a, _ := sessionDB(t)
	s, err := a.OpenSQLSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run(t, s, "BEGIN; INSERT INTO t VALUES (30, 'gone')")
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, a, "SELECT count(*) FROM t WHERE id = 30"); got != int64(0) {
		t.Errorf("an open transaction was committed: %v", got)
	}
	// The file is not left locked.
	if _, err := a.db.Exec("INSERT INTO t VALUES (31, 'ok')"); err != nil {
		t.Errorf("write after Close: %v", err)
	}
	if _, err := s.Execute(context.Background(), "SELECT 1"); err == nil {
		t.Error("a closed session executed")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestAdapterCloseClosesSessions(t *testing.T) {
	path := newTestDB(t, "CREATE TABLE t (id INTEGER)")
	ad, err := Connect(context.Background(), profileFor(path), "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ad.(*Adapter).OpenSQLSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run(t, s, "BEGIN; INSERT INTO t VALUES (1)")
	if err := ad.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), "SELECT 1"); err == nil {
		t.Error("a session outlived its adapter")
	}
	if _, err := ad.(*Adapter).OpenSQLSession(context.Background()); err == nil {
		t.Error("a closed adapter opened a session")
	}
	if err := os.Remove(path); err != nil {
		t.Errorf("the file is still held: %v", err)
	}
}

func TestSessionsAreIndependent(t *testing.T) {
	a, s1 := sessionDB(t)
	s2 := openSession(t, a)
	run(t, s1, "BEGIN; INSERT INTO t VALUES (40, 'one')")
	if got := run(t, s2, "SELECT count(*) FROM t WHERE id = 40").Results[0].Rows[0][0].Text; got != "0" {
		t.Errorf("session two sees session one's transaction: %s", got)
	}
	run(t, s1, "COMMIT")
	if got := run(t, s2, "SELECT count(*) FROM t WHERE id = 40").Results[0].Rows[0][0].Text; got != "1" {
		t.Errorf("session two misses the commit: %s", got)
	}
}

func TestSelectUsesTheStatementScanner(t *testing.T) {
	_, s := sessionDB(t)
	text := "select 'a;b'; select 2;"
	got, err := s.Select(database.SQLInput{Text: text, Cursor: strings.Index(text, "2")})
	if err != nil || got.SQL != "select 2;" {
		t.Errorf("Select: %+v, %v", got, err)
	}
}

func TestExecuteRejectsUnterminatedInputBeforeRunning(t *testing.T) {
	a, s := sessionDB(t)
	if _, err := s.Execute(context.Background(), "DELETE FROM t; SELECT 'open"); err == nil {
		t.Fatal("unterminated text was accepted")
	}
	if got := scalar(t, a, "SELECT count(*) FROM t"); got != int64(3) {
		t.Errorf("part of a rejected block ran: %v", got)
	}
}

func TestResultLimitStopsAtMaxResults(t *testing.T) {
	_, s := sessionDB(t)
	var b strings.Builder
	for i := 0; i < database.SQLMaxResults+2; i++ {
		fmt.Fprintf(&b, "SELECT %d;", i)
	}
	r, err := s.Execute(context.Background(), b.String())
	if err == nil || r.Stop != database.SQLLimitReached || len(r.Results) != database.SQLMaxResults {
		t.Errorf("stop %v, %d results, error %v", r.Stop, len(r.Results), err)
	}
}

func TestSessionLostWhenFileIsGone(t *testing.T) {
	a, s := sessionDB(t)
	run(t, s, "SELECT 1")
	// Windows will not delete an open file; where it can be done, the next
	// run reports a lost session or still works on the open handle.
	if err := os.Remove(a.path); err != nil {
		t.Skipf("cannot remove an open file here: %v", err)
	}
	r, err := s.Execute(context.Background(), "SELECT count(*) FROM t")
	if err == nil && r.Transaction == database.SQLSessionLost {
		t.Error("a lost session reported success")
	}
}
