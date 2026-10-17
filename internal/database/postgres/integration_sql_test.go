package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dbl/internal/database"
)

func TestIntegrationSQLTransactionAcrossExecutes(t *testing.T) {
	p, pw := integrationProfile(t)
	p.Pool.MaxOpen, p.Pool.MaxIdle = 1, 1
	ad, err := Connect(context.Background(), p, pw)
	if err != nil {
		t.Fatal(err)
	}
	a := ad.(*Adapter)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := a.OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	name := fmt.Sprintf("dbl_sql_it_%d", time.Now().UnixNano())
	q := `"` + name + `"`
	if _, err := s.Execute(ctx, "CREATE SCHEMA "+q+"; CREATE TABLE "+q+".t(id integer);"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, end := context.WithTimeout(context.Background(), 3*time.Second)
		defer end()
		_, _ = a.db.ExecContext(cleanup, "DROP SCHEMA "+q+" CASCADE")
	}()
	r, err := s.Execute(ctx, "BEGIN; INSERT INTO "+q+".t VALUES (1);")
	if err != nil || r.Transaction != database.SQLInTransaction {
		t.Fatal(r, err)
	}
	var count int
	if err := a.db.QueryRowContext(ctx, "SELECT count(*) FROM "+q+".t").Scan(&count); err != nil || count != 0 {
		t.Fatal("pool blocked or saw uncommitted data", count, err)
	}
	r, err = s.Execute(ctx, "COMMIT;")
	if err != nil || r.Transaction != database.SQLIdle {
		t.Fatal(r, err)
	}
	if err := a.db.QueryRowContext(ctx, "SELECT count(*) FROM "+q+".t").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := s.Execute(ctx, "BEGIN; SELECT definitely_missing_column;"); err == nil {
		t.Fatal("expected SQL error")
	}
	r, err = s.Execute(ctx, "ROLLBACK;")
	if err != nil || r.Transaction != database.SQLIdle {
		t.Fatal(r, err)
	}
}

func TestIntegrationSQLResultsAndCancel(t *testing.T) {
	p, pw := integrationProfile(t)
	ad, err := Connect(context.Background(), p, pw)
	if err != nil {
		t.Fatal(err)
	}
	defer ad.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := ad.(database.SQLSessionFactory).OpenSQLSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	r, err := s.Execute(ctx, "SELECT generate_series(1,1500); SELECT 9007199254740993.12345::numeric AS x, NULL::text AS x;")
	if err != nil || !r.Ready || len(r.Results) != 2 || len(r.Results[0].Rows) != 1000 || !r.Results[0].Truncated {
		t.Fatal(r, err)
	}
	if r.Results[1].Rows[0][0].Text != "9007199254740993.12345" {
		t.Fatal("numeric precision changed")
	}
	short, end := context.WithTimeout(ctx, 100*time.Millisecond)
	defer end()
	r, err = s.Execute(short, "SELECT pg_sleep(10);")
	if err == nil || r == nil || r.Stop != database.SQLTimedOut {
		t.Fatal("timeout not reported", r, err)
	}
}
