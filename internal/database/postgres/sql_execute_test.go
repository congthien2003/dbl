package postgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"

	"dbl/internal/database"
)

func TestSQLResultGroupAndMetadataBudgets(t *testing.T) {
	for _, mode := range []string{"groups", "columns", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool {
				if mode == "groups" {
					for i := 0; i < database.SQLMaxResults+1; i++ {
						b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SET")})
					}
				} else {
					count := database.SQLMaxColumns
					name := "x"
					if mode == "columns" {
						count++
					} else {
						name = strings.Repeat("x", 300)
					}
					fields := make([]pgproto3.FieldDescription, count)
					for i := range fields {
						fields[i] = pgproto3.FieldDescription{Name: []byte(name), DataTypeOID: 25}
					}
					b.Send(&pgproto3.RowDescription{Fields: fields})
					b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 0")})
				}
				b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				return true
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s, err := a.OpenSQLSession(ctx)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Execute(ctx, "SELECT 1")
			if err == nil || r == nil || r.Stop != database.SQLLimitReached {
				t.Fatal("budget was not applied", r, err)
			}
		})
	}
}

func TestSQLCancelVersusConfirmedCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool {
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("COMMIT")})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		_ = b.Flush()
		cancel()
		return true
	})
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "COMMIT")
	if err != nil || r == nil || !r.Ready || r.Stop != database.SQLCompleted || !r.Results[0].CommandComplete {
		t.Fatal("late cancellation discarded completion", r, err)
	}
}

func TestSQLExecuteValuesAndEmptyMetadata(t *testing.T) {
	a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool {
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{
			{Name: []byte("n"), DataTypeOID: 1700}, {Name: []byte("b"), DataTypeOID: 17}, {Name: []byte("x"), DataTypeOID: 25}, {Name: []byte("x"), DataTypeOID: 25},
		}})
		b.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("9007199254740993.12345"), []byte(`\x00ff`), {}, nil}})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("empty"), DataTypeOID: 25}}})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 0")})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("CREATE TABLE")})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "SELECT typed; SELECT empty; CREATE TABLE t(x int)")
	if err != nil || len(r.Results) != 3 {
		t.Fatal(r, err)
	}
	row := r.Results[0].Rows[0]
	if row[0].Text != "9007199254740993.12345" || row[1].Size != 2 || row[2].Kind == row[3].Kind {
		t.Fatal("type/NULL/precision mismatch", row)
	}
	if len(r.Results[1].Rows) != 0 || len(r.Results[1].Columns) != 1 || r.Results[2].HasRowsAffected {
		t.Fatal("empty metadata or affected count invented", r.Results)
	}
}

func TestSQLDrainStopsAtBudget(t *testing.T) {
	var queries atomic.Int32
	a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool {
		queries.Add(1)
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("n"), DataTypeOID: 23}}})
		for i := 0; i < database.SQLReadRows+1; i++ {
			b.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("1")}})
		}
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 100001")})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "SELECT many_rows")
	if err == nil || r == nil || r.Stop != database.SQLLimitReached || r.Ready || len(r.Results) != 1 || len(r.Results[0].Rows) != database.SQLSnapshotRows || !r.Results[0].Truncated {
		t.Fatal("unbounded/incorrect partial result", r, err)
	}
	if queries.Load() != 1 {
		t.Fatal("query retried")
	}
}

func TestSQLNoticeAndNotificationFlood(t *testing.T) {
	a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool {
		for i := 0; i < 40; i++ {
			b.Send(&pgproto3.NoticeResponse{Severity: "NOTICE", Code: "00000", Message: strings.Repeat("n", 5000)})
			b.Send(&pgproto3.NotificationResponse{Channel: "c", Payload: "ignored"})
		}
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("DO")})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "DO $$ BEGIN NULL; END $$;")
	if err != nil || len(r.Notices) != 32 || r.DroppedNotices != 8 {
		t.Fatal(r, err)
	}
	for _, notice := range r.Notices {
		if len(notice) > database.SQLNoticeBytes {
			t.Fatal("unbounded notice")
		}
	}
}

func TestSQLCopyModeStops(t *testing.T) {
	a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool { b.Send(&pgproto3.CopyOutResponse{}); return true })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "SELECT opaque_function()")
	if err == nil || r == nil || r.Transaction != database.SQLSessionLost {
		t.Fatal("unexpected COPY mode did not terminate", r, err)
	}
}

func TestSQLCommandEvidenceSurvivesLostReady(t *testing.T) {
	a := sqlPeer(t, func(b *pgproto3.Backend, _ string) bool {
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("COMMIT")})
		_ = b.Flush()
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "COMMIT")
	if err == nil || r == nil || len(r.Results) != 1 || !r.Results[0].CommandComplete || r.Results[0].CommandTag != "COMMIT" || !r.OutcomeUncertain {
		t.Fatal("confirmed command evidence was lost", r, err)
	}
}

// sqlPeer supplies a real PostgreSQL protocol connection without a database.
func sqlPeer(t *testing.T, respond func(*pgproto3.Backend, string) bool) *Adapter {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		defer server.Close()
		b := pgproto3.NewBackend(server, server)
		if _, err := b.ReceiveStartupMessage(); err != nil {
			return
		}
		b.Send(&pgproto3.AuthenticationOk{})
		b.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if b.Flush() != nil {
			return
		}
		for {
			msg, err := b.Receive()
			if err != nil {
				return
			}
			q, ok := msg.(*pgproto3.Query)
			if !ok {
				return
			}
			if !respond(b, q.String) {
				return
			}
			if b.Flush() != nil {
				return
			}
		}
	}()
	cfg, err := pgx.ParseConfig("host=localhost user=test dbname=test sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	var dialed atomic.Bool
	cfg.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		if dialed.Swap(true) {
			return nil, errors.New("protocol peer has no cancel connection")
		}
		return client, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Adapter{sqlConfig: cfg, sqlCtx: ctx, endSQL: cancel, sqlSessions: map[*sqlSession]struct{}{}}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestSQLExecuteMultipleResultsAndTags(t *testing.T) {
	a := sqlPeer(t, func(b *pgproto3.Backend, sql string) bool {
		if sql != "SELECT 1; UPDATE t SET x=1;" {
			return false
		}
		b.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("x"), DataTypeOID: 23}}})
		b.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("1")}})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		b.Send(&pgproto3.CommandComplete{CommandTag: []byte("UPDATE 3")})
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "SELECT 1; UPDATE t SET x=1;")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Ready || len(r.Results) != 2 || r.Results[0].Rows[0][0].Text != "1" || r.Results[1].RowsAffected != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestSQLCommitResponseLost(t *testing.T) {
	a := sqlPeer(t, func(*pgproto3.Backend, string) bool { return false })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(ctx, "COMMIT;")
	if err == nil || r == nil || !r.OutcomeUncertain || r.Transaction != database.SQLSessionLost {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSQLSessionRegistryClosesUndeliveredSessions(t *testing.T) {
	a := sqlPeer(t, func(b *pgproto3.Backend, sql string) bool {
		b.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, "SELECT 1"); err == nil {
		t.Fatal("undelivered session survived adapter Close")
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
