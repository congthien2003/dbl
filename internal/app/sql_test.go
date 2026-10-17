package app

import (
	"context"
	"testing"
	"time"

	"dbl/internal/database"
	"dbl/internal/database/fakedb"
)

func TestSQLSelectionFailureKeepsTransaction(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, empty := range []bool{false, true} {
			e := newSQLEnv(t)
			e.s.SQLServices = NewSQLServices(nil, nil)
			e.s.Post = e.post.post
			t.Cleanup(func() { _ = e.s.CloseSQL(context.Background()) })
			transaction := database.SQLInTransaction
			if failed {
				transaction = database.SQLTransactionFailed
			}
			script := &fakedb.SQLSession{Run: func(context.Context, string) (*database.SQLExecution, error) {
				return &database.SQLExecution{Ready: true, Transaction: transaction}, nil
			}}
			script.SelectInput = func(in database.SQLInput) (database.SQLSelection, error) {
				if in.Text == "no query" {
					if empty {
						return database.SQLSelection{}, nil
					}
					return database.SQLSelection{}, &database.Error{Kind: database.KindValidation, Message: "Invalid selection"}
				}
				return database.SQLSelection{SQL: in.Text}, nil
			}
			e.db.QueueSQLSession(script)
			tab := e.s.Workspace.NewSQL(e.connID)
			e.s.ExecuteSQL(tab, database.SQLInput{Text: "BEGIN"}, false)
			e.post.run(t)
			e.post.run(t)
			e.s.ExecuteSQL(tab, database.SQLInput{Text: "no query"}, false)
			e.post.run(t)
			if len(script.Calls()) != 1 {
				t.Fatal("empty/rejected selection dispatched")
			}
			if e.s.RequestClose(tab.ID) || e.s.PendingSQL == nil || e.s.PendingSQL.Stage != SQLGuardTransaction {
				t.Fatalf("transaction guard disappeared after selection: failed=%v empty=%v", failed, empty)
			}
		}
	}
}

func TestSQLCloseBeforeDeliveryReleasesLease(t *testing.T) {
	e := newSQLEnv(t)
	e.s.SQLServices = NewSQLServices(nil, nil)
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	started := make(chan struct{})
	script := &fakedb.SQLSession{Run: func(context.Context, string) (*database.SQLExecution, error) {
		close(started)
		return &database.SQLExecution{Ready: true}, nil
	}}
	e.db.QueueSQLSession(script)
	tab := e.s.Workspace.NewSQL(e.connID)
	e.s.ExecuteSQL(tab, database.SQLInput{Text: "select 1"}, false)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query never started")
	}
	// Discard every UI callback. The tab must own the lease before delivery.
	e.s.Workspace.Close(tab.ID)
	select {
	case <-script.Done():
	case <-time.After(time.Second):
		t.Fatal("closed tab leaked its undelivered SQL session")
	}
	if script.CloseCount() != 1 {
		t.Fatal("session closed more than once")
	}
}

func TestSQLCancelKeepsTerminalStatus(t *testing.T) {
	e := newSQLEnv(t)
	e.s.SQLServices = NewSQLServices(nil, nil)
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	started := make(chan struct{})
	script := &fakedb.SQLSession{Run: func(ctx context.Context, _ string) (*database.SQLExecution, error) {
		close(started)
		<-ctx.Done()
		err := &database.Error{Kind: database.KindCanceled, Message: "Canceled"}
		return &database.SQLExecution{Ready: true, Transaction: database.SQLTransactionFailed, Stop: database.SQLCanceled, Err: err}, err
	}}
	e.db.QueueSQLSession(script)
	tab := e.s.Workspace.NewSQL(e.connID)
	e.s.ExecuteSQL(tab, database.SQLInput{Text: "BEGIN; SELECT pg_sleep(10)"}, false)
	e.post.run(t)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	e.s.CancelSQL(tab)
	e.post.run(t)
	if tab.SQL.State != SQLFinished || tab.SQL.Execution == nil || tab.SQL.Execution.Stop != database.SQLCanceled || tab.SQL.Transaction != database.SQLTransactionFailed || !tab.SQL.HasSession() {
		t.Fatal("canceled terminal state was discarded")
	}
	if len(script.Calls()) != 1 {
		t.Fatal("query retried")
	}
}

func TestSQLExecutionDropsStaleEpoch(t *testing.T) {
	e := newSQLEnv(t)
	e.s.SQLServices = NewSQLServices(nil, nil)
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	release := make(chan struct{})
	started := make(chan struct{})
	script := &fakedb.SQLSession{Run: func(context.Context, string) (*database.SQLExecution, error) {
		close(started)
		<-release
		return &database.SQLExecution{Ready: true}, nil
	}}
	e.db.QueueSQLSession(script)
	tab := e.s.Workspace.NewSQL(e.connID)
	e.s.ExecuteSQL(tab, database.SQLInput{Text: "select 1"}, false)
	e.post.run(t)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	e.s.Conns.Disconnect(e.connID)
	e.s.Conns.Connect(e.connID)
	e.post.run(t)
	close(release)
	e.post.run(t)
	if !tab.SQL.Lost || tab.SQL.Execution == nil || tab.SQL.Execution.Ready || !tab.SQL.Execution.OutcomeUncertain {
		t.Fatal("stale result overwrote new connection epoch")
	}
}

func TestSQLExecutionUsesIndependentTabSessions(t *testing.T) {
	e := newSQLEnv(t)
	e.s.SQLServices = NewSQLServices(nil, nil)
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	a, b := &fakedb.SQLSession{}, &fakedb.SQLSession{}
	e.db.QueueSQLSession(a)
	e.db.QueueSQLSession(b)
	tab := e.s.Workspace.NewSQL(e.connID)
	if !e.s.ExecuteSQL(tab, database.SQLInput{Text: "BEGIN"}, false) {
		t.Fatal("did not start")
	}
	e.post.run(t)
	e.post.run(t)
	if !e.s.ExecuteSQL(tab, database.SQLInput{Text: "SELECT 1"}, false) {
		t.Fatal("did not reuse")
	}
	e.post.run(t)
	other := e.s.Workspace.NewSQL(e.connID)
	e.s.ExecuteSQL(other, database.SQLInput{Text: "SELECT 2"}, false)
	e.post.run(t)
	e.post.run(t)
	if len(a.Calls()) != 2 || len(b.Calls()) != 1 {
		t.Fatal("sessions were shared or reopened")
	}
}
func TestSQLReconnectNeedsExplicitConsent(t *testing.T) {
	e := newSQLEnv(t)
	e.s.SQLServices = NewSQLServices(nil, nil)
	e.s.Post = e.post.post
	defer e.s.CloseSQL(context.Background())
	tab := e.s.Workspace.NewSQL(e.connID)
	tab.SQL.Lost = true
	e.db.QueueSQLSession(&fakedb.SQLSession{})
	if e.s.ExecuteSQL(tab, database.SQLInput{Text: "SELECT 1"}, false) || tab.SQL.PendingInput == nil {
		t.Fatal("lost session silently reopened")
	}
	if !e.s.ExecuteSQL(tab, *tab.SQL.PendingInput, true) {
		t.Fatal("explicit reopen failed")
	}
	e.post.run(t)
	e.post.run(t)
}
