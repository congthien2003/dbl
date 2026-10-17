package query

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"dbl/internal/database"
)

type testFactory struct{ calls atomic.Int32 }

func (f *testFactory) OpenSQLSession(context.Context) (database.SQLSession, error) {
	f.calls.Add(1)
	return &testSession{}, nil
}

type testSession struct{ closed atomic.Int32 }

func (s *testSession) Select(in database.SQLInput) (database.SQLSelection, error) {
	return database.SQLSelection{SQL: in.Text}, nil
}
func (s *testSession) Execute(context.Context, string) (*database.SQLExecution, error) {
	return &database.SQLExecution{Ready: true}, nil
}
func (s *testSession) Close(context.Context) error { s.closed.Add(1); return nil }

func TestExecutorCapacityAndRelease(t *testing.T) {
	e, f := NewExecutor(), &testFactory{}
	defer e.Close(context.Background())
	var leases []*Lease
	for i := 0; i < 16; i++ {
		profile := "a"
		if i >= 8 {
			profile = "b"
		}
		l, err := e.Open(context.Background(), context.Background(), profile, f)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, l)
	}
	for _, profile := range []string{"a", "c"} {
		if _, err := e.Open(context.Background(), context.Background(), profile, f); err == nil {
			t.Fatal("capacity was not enforced")
		}
	}
	if f.calls.Load() != 16 {
		t.Fatal("dial called after capacity exceeded")
	}
	_ = leases[0].Close(context.Background())
	_ = leases[0].Close(context.Background())
	if _, err := e.Open(context.Background(), context.Background(), "a", f); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorOpenContextIsNotLeaseLifetime(t *testing.T) {
	e := NewExecutor()
	defer e.Close(context.Background())
	open, endOpen := context.WithCancel(context.Background())
	life, endLife := context.WithCancel(context.Background())
	l, err := e.Open(open, life, "a", &testFactory{})
	if err != nil {
		t.Fatal(err)
	}
	endOpen()
	if _, err := l.Execute(context.Background(), "select 1"); err != nil {
		t.Fatal(err)
	}
	endLife()
	select {
	case <-l.done:
	case <-time.After(time.Second):
		t.Fatal("lifetime did not close lease")
	}
}
