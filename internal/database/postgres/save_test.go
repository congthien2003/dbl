package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbl/internal/database"
	"dbl/internal/grid"
)

func txt(s string) grid.Value { return grid.Value{Kind: grid.KindText, Text: s} }
func nul() grid.Value         { return grid.Value{Kind: grid.KindNull} }
func numv(s string) grid.Value {
	return grid.Value{Kind: grid.KindNumber, Text: s}
}

func TestBuildUpdate(t *testing.T) {
	m := testMeta()
	rc := database.RowChange{
		RowID: 1,
		Key:   []database.KeyValue{{Column: "id", Value: numv("5")}},
		Cells: []database.CellChange{
			{Column: `na"me`, Original: txt("old'; DROP TABLE x;--"), Current: txt("new")},
			{Column: "doc", Original: nul(), Current: grid.Value{Kind: grid.KindJSON, Text: `{"a":1}`}},
		},
	}
	stmt, args := buildUpdate(m, rc)
	want := `UPDATE "we""ird"."Users; DROP" SET "na""me" = $1, "doc" = $2 WHERE "id" = $3 AND "na""me"::text IS NOT DISTINCT FROM $4 AND "doc"::text IS NOT DISTINCT FROM $5`
	if stmt != want {
		t.Errorf("SQL\n got %s\nwant %s", stmt, want)
	}
	wantArgs := []any{"new", `{"a":1}`, "5", "old'; DROP TABLE x;--", nil}
	if len(args) != len(wantArgs) {
		t.Fatalf("args %v", args)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("arg %d = %#v, want %#v", i+1, args[i], wantArgs[i])
		}
	}
	// What the user typed travels only as a parameter.
	if strings.Contains(stmt, "DROP TABLE x") || strings.Contains(stmt, "new") {
		t.Errorf("a value reached the SQL text: %s", stmt)
	}
}

func TestBuildUpdateNullAndCompositeKey(t *testing.T) {
	m := testMeta()
	rc := database.RowChange{
		Key: []database.KeyValue{
			{Column: "id", Value: numv("1")},
			{Column: "doc", Value: txt("k")},
		},
		Cells: []database.CellChange{{Column: `na"me`, Original: txt("a"), Current: nul()}},
	}
	stmt, args := buildUpdate(m, rc)
	want := `UPDATE "we""ird"."Users; DROP" SET "na""me" = $1 WHERE "id" = $2 AND "doc" = $3 AND "na""me"::text IS NOT DISTINCT FROM $4`
	if stmt != want {
		t.Errorf("SQL\n got %s\nwant %s", stmt, want)
	}
	if args[0] != nil || args[1] != "1" || args[2] != "k" || args[3] != "a" {
		t.Errorf("args %#v", args)
	}
}

func TestClassifySave(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		code string
		want database.Kind
	}{
		{"23505", database.KindValidation}, // unique_violation
		{"23502", database.KindValidation}, // not_null_violation
		{"23503", database.KindValidation}, // foreign_key_violation
		{"23514", database.KindValidation}, // check_violation
		{"40001", database.KindConflict},   // serialization_failure
		{"40P01", database.KindConflict},   // deadlock_detected
		{"55P03", database.KindConflict},   // lock_not_available
		{"25006", database.KindPermission}, // read_only_sql_transaction
		{"42501", database.KindPermission}, // insufficient_privilege
		{"22P02", database.KindValidation}, // invalid_text_representation (classifyQuery)
		{"22003", database.KindValidation}, // numeric_value_out_of_range
		{"42P01", database.KindMetadataUnavailable},
	}
	for _, tc := range cases {
		got := classifySave(ctx, &pgconn.PgError{Code: tc.code, Message: "boom"}, "Saving the row")
		if got.Kind != tc.want {
			t.Errorf("%s: kind %v, want %v", tc.code, got.Kind, tc.want)
		}
		if tc.code[:2] == "23" && !strings.Contains(got.Message, "boom") {
			t.Errorf("%s: the server's message is lost: %q", tc.code, got.Message)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got := classifySave(canceled, &pgconn.PgError{Code: "23505"}, "Saving"); got.Kind != database.KindCanceled {
		t.Errorf("canceled context: kind %v", got.Kind)
	}
}

func TestCommitFailedOutcome(t *testing.T) {
	var se *database.SaveError
	for _, err := range []error{
		&pgconn.PgError{Code: "23505", Message: "deferred"},
		pgx.ErrTxCommitRollback,
		sql.ErrTxDone,
	} {
		if !errors.As(commitFailed(err), &se) || se.Outcome != database.SaveNotApplied {
			t.Errorf("%v: outcome %v, want not applied", err, se)
		}
	}
	err := commitFailed(errors.New("write tcp: connection reset by peer"))
	if !errors.As(err, &se) || se.Outcome != database.SaveUnknown || se.Err.Kind != database.KindConnection {
		t.Fatalf("lost connection: %+v", se)
	}
	if !strings.Contains(se.Err.Message, "not known") {
		t.Errorf("the message does not say the outcome is unknown: %q", se.Err.Message)
	}
}

func TestCommitGateFollowsCancelUntilCommit(t *testing.T) {
	// Canceled before COMMIT: the transaction's context ends, and the commit
	// is refused.
	ctx, cancel := context.WithCancel(context.Background())
	txCtx, gate := newCommitGate(ctx)
	cancel()
	select {
	case <-txCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("canceling the caller did not cancel the transaction before the commit")
	}
	if gate.beginCommit() {
		t.Error("beginCommit succeeded after the caller canceled")
	}
	gate.release()

	// Canceled after the commit began: the transaction's context lives on.
	ctx2, cancel2 := context.WithCancel(context.Background())
	txCtx2, gate2 := newCommitGate(ctx2)
	if !gate2.beginCommit() {
		t.Fatal("beginCommit refused a live context")
	}
	cancel2()
	time.Sleep(50 * time.Millisecond)
	if txCtx2.Err() != nil {
		t.Error("canceling the caller during COMMIT canceled the transaction")
	}
	gate2.release()
	if txCtx2.Err() == nil {
		t.Error("release left the transaction's context alive")
	}
}
