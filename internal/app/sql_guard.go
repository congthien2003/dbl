package app

import (
	"context"

	"dbl/internal/database"
)

type SQLGuardStage uint8

const (
	SQLGuardWaiting SQLGuardStage = iota
	SQLGuardQuery
	SQLGuardDocument
	SQLGuardTransaction
	SQLGuardUncertain
)

type SQLActionChoice uint8

const (
	SQLActionCancel SQLActionChoice = iota
	SQLActionStopQuery
	SQLActionSaveDocument
	SQLActionDiscardDocument
	SQLActionRollback
	SQLActionContinue
)

type PendingSQLAction struct {
	Tab            TabID
	Reason         string
	Stage          SQLGuardStage
	targets        []TabID
	done           func()
	discarded      map[TabID]uint64
	acknowledged   map[TabID]*database.SQLExecution
	autoSave       bool
	closing        bool
	promptRevision uint64
	promptSequence int
	promptRun      *sqlRun
}

func (s *State) startSQLAction(tabs []*Tab, reason string, autoSave bool, done func()) bool {
	if s.PendingSQL != nil {
		return false
	}
	p := &PendingSQLAction{Reason: reason, done: done, discarded: map[TabID]uint64{}, acknowledged: map[TabID]*database.SQLExecution{}, autoSave: autoSave}
	for _, tab := range tabs {
		p.targets = append(p.targets, tab.ID)
	}
	s.PendingSQL = p
	completed := false
	p.done = func() {
		completed = true
		if done != nil {
			done()
		}
	}
	s.advanceSQLAction()
	return completed
}
func (s *State) cancelSQLActionFor(id TabID) {
	if p := s.PendingSQL; p != nil {
		for _, target := range p.targets {
			if target == id {
				s.PendingSQL = nil
				return
			}
		}
	}
}
func (s *State) advanceSQLAction() {
	p := s.PendingSQL
	if p == nil || p.closing {
		return
	}
	for _, id := range p.targets {
		tab := s.Workspace.Find(id)
		if tab == nil {
			continue
		}
		p.Tab = id
		if tab.Saving() {
			p.Stage = SQLGuardWaiting
			return
		}
		if d := tab.SQL; d != nil {
			p.promptRevision, p.promptSequence, p.promptRun = d.Document.Revision, d.Sequence, d.run
			if d.Busy() {
				if d.State == SQLCanceling {
					p.Stage = SQLGuardWaiting
				} else {
					p.Stage = SQLGuardQuery
				}
				return
			}
			if tab.Dirty() {
				if revision, ok := p.discarded[id]; !ok || revision != d.Document.Revision {
					p.Stage = SQLGuardDocument
					if p.autoSave {
						s.ResolveSQLAction(SQLActionSaveDocument)
					}
					return
				}
			}
			if d.Execution != nil {
				if d.Execution.OutcomeUncertain && p.acknowledged[id] != d.Execution {
					p.Stage = SQLGuardUncertain
					return
				}
			}
			if d.Transaction == database.SQLInTransaction || d.Transaction == database.SQLTransactionFailed {
				p.Stage = SQLGuardTransaction
				return
			}
		} else if tab.Dirty() {
			p.Stage = SQLGuardDocument
			if p.autoSave {
				s.ResolveSQLAction(SQLActionSaveDocument)
			}
			return
		}
	}
	s.PendingSQL = nil
	if p.done != nil {
		p.done()
	}
}
func (s *State) ResolveSQLAction(choice SQLActionChoice) {
	p := s.PendingSQL
	if p == nil {
		return
	}
	if choice == SQLActionCancel {
		s.PendingSQL = nil
		return
	}
	tab := s.Workspace.Find(p.Tab)
	if tab == nil {
		s.advanceSQLAction()
		return
	}
	if d := tab.SQL; d != nil && (d.Document.Revision != p.promptRevision || d.Sequence != p.promptSequence || d.run != p.promptRun) {
		s.Notice = "The SQL tab changed while the prompt was open. Review the pending action again."
		s.advanceSQLAction()
		return
	}
	switch choice {
	case SQLActionStopQuery:
		if p.Stage != SQLGuardQuery {
			return
		}
		s.CancelSQL(tab)
		p.Stage = SQLGuardWaiting
	case SQLActionSaveDocument:
		if p.Stage != SQLGuardDocument {
			return
		}
		p.Stage = SQLGuardWaiting
		if tab.SQL != nil {
			if !s.SaveSQLFile(tab, false, s.advanceSQLAction) {
				s.PendingSQL = nil
			}
		} else if !s.Save(tab, s.advanceSQLAction) {
			s.PendingSQL = nil
		}
	case SQLActionDiscardDocument:
		if p.Stage != SQLGuardDocument {
			return
		}
		if tab.SQL != nil {
			p.discarded[tab.ID] = tab.SQL.Document.Revision
		} else {
			s.RevertEdits(tab)
		}
		s.advanceSQLAction()
	case SQLActionContinue:
		if p.Stage != SQLGuardUncertain || tab.SQL == nil {
			return
		}
		p.acknowledged[tab.ID] = tab.SQL.Execution
		s.advanceSQLAction()
	case SQLActionRollback:
		if p.Stage != SQLGuardTransaction || tab.SQL == nil {
			return
		}
		d := tab.SQL
		lease := d.lease
		if lease == nil {
			s.Notice = "The transaction session is unavailable. Its state cannot be confirmed."
			s.PendingSQL = nil
			return
		}
		p.Stage = SQLGuardWaiting
		p.closing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
			defer cancel()
			err := lease.Close(ctx)
			s.post(func() {
				if current := s.Workspace.Find(tab.ID); current != nil && current.SQL == d && d.lease == lease {
					d.lease = nil
					d.Lost = true
					d.Transaction = database.SQLSessionLost
					d.State = SQLFinished
					if d.Execution != nil {
						d.Execution.Transaction = database.SQLSessionLost
					}
					if err != nil {
						d.Err = &database.Error{Kind: database.KindConnection, Message: "Session cleanup could not be confirmed. The session has been stopped.", Err: err}
					}
				}
				if s.PendingSQL != p {
					return
				}
				p.closing = false
				if err != nil {
					s.PendingSQL = nil
					return
				}
				s.advanceSQLAction()
			})
		}()
	}
}
func (s *State) RequestSQLConnection(tab *Tab, id string) bool {
	if tab == nil || tab.SQL == nil {
		return false
	}
	if tab.Table.ConnectionID == id {
		return true
	}
	return s.startSQLAction([]*Tab{tab}, "change this tab's connection", false, func() {
		tab.SQL.release()
		tab.SQL.State = SQLNotOpened
		tab.SQL.Lost = false
		tab.SQL.Transaction = database.SQLIdle
		tab.SQL.Err = nil
		tab.SQL.PendingInput = nil
		tab.Table.ConnectionID = id
		tab.Source = nil
		tab.SQL.Sequence++
	})
}
func (s *State) hasSQLTabs(tabs []*Tab) bool {
	for _, tab := range tabs {
		if tab.SQL != nil {
			return true
		}
	}
	return false
}
