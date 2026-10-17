package app

import (
	"context"
	"testing"

	"dbl/internal/connection"
	"dbl/internal/database"
)

func TestSQLGuardEntryPointMatrix(t *testing.T) {
	for _, action := range []string{"close", "quit", "switch", "disconnect", "delete", "edit profile"} {
		for _, condition := range []string{"dirty", "transaction", "running", "saving"} {
			t.Run(action+"/"+condition, func(t *testing.T) {
				e := newSQLEnv(t)
				tab := e.s.Workspace.NewSQL(e.connID)
				stage := SQLGuardDocument
				switch condition {
				case "dirty":
					e.s.SetSQLText(tab, "select 1")
				case "transaction":
					tab.SQL.Transaction = database.SQLInTransaction
					stage = SQLGuardTransaction
				case "running":
					tab.SQL.State = SQLRunning
					tab.SQL.run = &sqlRun{cancel: func() {}}
					stage = SQLGuardQuery
				case "saving":
					tab.SQL.Document.FileState = OpLoading
					stage = SQLGuardWaiting
				}
				switch action {
				case "close":
					e.s.RequestClose(tab.ID)
				case "quit":
					e.s.RequestQuit()
				case "switch":
					e.s.RequestSQLConnection(tab, "other")
				case "disconnect":
					e.s.RequestDisconnect(e.connID)
					e.s.ConfirmConn()
				case "delete":
					e.s.RequestDelete(e.connID)
					e.s.ConfirmConn()
				case "edit profile":
					p, _ := e.s.Catalog.Profile(e.connID)
					p.Host = "new-host"
					if _, err := e.s.SaveProfile(p, connection.PasswordChange{Mode: connection.KeepPassword}); err != nil {
						t.Fatal(err)
					}
					e.s.ConfirmConn()
				}
				if e.s.PendingSQL == nil || e.s.PendingSQL.Stage != stage {
					t.Fatalf("missing %v guard: %+v", stage, e.s.PendingSQL)
				}
				e.s.ResolveSQLAction(SQLActionCancel)
				if e.s.Workspace.Find(tab.ID) == nil || tab.Table.ConnectionID != e.connID || e.s.Conns.Status(e.connID) != connection.Connected {
					t.Fatal("cancel applied destructive action")
				}
			})
		}
	}
}

func TestSQLGuardMixedTabsStopOnFirstFailure(t *testing.T) {
	e := newSQLEnv(t)
	table := e.open("small")
	e.edit(table, 0, colName, "changed")
	sql := e.s.Workspace.NewSQL(e.connID)
	e.s.SetSQLText(sql, "select 1")
	e.db.Hook = func(_ context.Context, call string) error {
		if call == "SaveChanges" {
			return &database.Error{Kind: database.KindPermission, Message: "Denied"}
		}
		return nil
	}
	e.s.RequestQuit()
	if e.s.PendingSQL == nil || e.s.PendingSQL.Tab != table.ID {
		t.Fatal("mixed save order changed")
	}
	e.s.ResolveSQLAction(SQLActionSaveDocument)
	e.post.run(t)
	if e.s.PendingSQL != nil || !table.Dirty() || !sql.Dirty() || len(e.s.Workspace.Tabs) != 2 {
		t.Fatal("failed table save continued into SQL file/quit")
	}
}

func TestSQLGuardRevalidatesDuringPrompt(t *testing.T) {
	s := &State{}
	tab := s.Workspace.NewSQL("a")
	s.SetSQLText(tab, "select 1")
	s.RequestClose(tab.ID)
	s.SetSQLText(tab, "select 2")
	s.ResolveSQLAction(SQLActionDiscardDocument)
	if s.Workspace.Find(tab.ID) == nil || s.PendingSQL == nil {
		t.Fatal("an old discard prompt approved newly changed text")
	}
	s.ResolveSQLAction(SQLActionDiscardDocument)
	if s.Workspace.Find(tab.ID) != nil {
		t.Fatal("current prompt did not authorize close")
	}
}

func TestSQLGuardActionMatrix(t *testing.T) {
	s := &State{}
	tab := s.Workspace.NewSQL("a")
	s.SetSQLText(tab, "select 1")
	if s.RequestClose(tab.ID) || s.PendingSQL == nil {
		t.Fatal("dirty SQL tab closed")
	}
	s.ResolveSQLAction(SQLActionCancel)
	if s.Workspace.Find(tab.ID) == nil || !tab.Dirty() {
		t.Fatal("cancel changed document")
	}
	s.RequestClose(tab.ID)
	s.ResolveSQLAction(SQLActionDiscardDocument)
	if s.Workspace.Find(tab.ID) != nil {
		t.Fatal("discard did not close")
	}
}
func TestSQLGuardKeepsTransactionSeparateFromDocument(t *testing.T) {
	s := &State{}
	tab := s.Workspace.NewSQL("a")
	tab.SQL.Execution = &database.SQLExecution{Ready: true, Transaction: database.SQLInTransaction}
	tab.SQL.Transaction = database.SQLInTransaction
	// A live lease is required before rollback can be attempted; the
	// coordinator must still ask even when the document is clean.
	if s.RequestClose(tab.ID) || s.PendingSQL == nil || s.PendingSQL.Stage != SQLGuardTransaction {
		t.Fatal("clean document bypassed transaction prompt")
	}
	s.ResolveSQLAction(SQLActionCancel)
	if s.Workspace.Find(tab.ID) == nil {
		t.Fatal("transaction cancel closed tab")
	}
}
