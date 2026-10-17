package app

import (
	"context"
	"strings"
	"time"

	"dbl/internal/database"
	"dbl/internal/query"
)

func (s *State) ExecuteSQL(tab *Tab, input database.SQLInput, allowNewSession bool) bool {
	if tab == nil || tab.SQL == nil || s.SQLServices == nil || tab.SQL.Busy() {
		return false
	}
	d := tab.SQL
	if len(input.Text) > database.SQLMaxInputBytes {
		d.Err = &database.Error{Kind: database.KindValidation, Message: "SQL input exceeds 8 MiB."}
		return false
	}
	if strings.TrimSpace(input.Text) == "" {
		return false
	}
	connectionID := tab.Table.ConnectionID
	factory, ok := s.Conns.SQLSessions(connectionID)
	if !ok {
		d.Err = noSession()
		return false
	}
	epoch := s.Conns.Epoch(connectionID)
	if d.Lost || d.lease != nil && d.epoch != epoch {
		if !allowNewSession {
			copy := input
			d.PendingInput = &copy
			return false
		}
		d.release()
		d.Lost = false
	}
	d.PendingInput = nil
	life := s.Conns.SessionContext(connectionID)
	if d.sessionLife == nil || d.sessionLife.Err() != nil || d.epoch != epoch {
		if d.endSession != nil {
			d.endSession()
		}
		d.sessionLife, d.endSession = context.WithCancel(life)
		d.epoch = epoch
	}
	sessionLife := d.sessionLife
	ctx, cancel := context.WithCancel(life)
	stopService := context.AfterFunc(s.SQLServices.life, cancel)
	o := &sqlRun{cancel: func() { stopService(); cancel() }, epoch: epoch}
	d.run = o
	d.Err = nil
	d.Execution = nil
	tab.Source = nil
	d.Sequence++
	d.Result, d.Page = 0, 1
	lease := d.lease
	d.State = SQLRunning
	if lease == nil {
		d.State = SQLOpening
	}
	id := tab.ID
	timeout := 10 * time.Second
	if p, ok := s.Catalog.Profile(connectionID); ok {
		timeout = time.Duration(p.Normalized().ConnectTimeoutSec) * time.Second
	}
	go func() {
		var err error
		if lease == nil {
			open, end := context.WithTimeout(ctx, timeout)
			lease, err = s.SQLServices.Executor.Open(open, sessionLife, connectionID, factory)
			end()
			if err == nil {
				opened := lease
				s.post(func() {
					if !s.liveSQLRun(id, d, o, connectionID) {
						go closeSQLLease(opened)
						return
					}
					d.lease, d.epoch = opened, epoch
					d.Transaction = database.SQLIdle
					if d.State != SQLCanceling {
						d.State = SQLRunning
					}
				})
			}
		}
		var result *database.SQLExecution
		if err == nil {
			var selection database.SQLSelection
			selection, err = lease.Select(input)
			if err == nil && selection.SQL != "" {
				result, err = lease.Execute(ctx, selection.SQL)
			}
		}
		if err != nil && result == nil && ctx.Err() != nil {
			err = &database.Error{Kind: database.KindCanceled, Message: "The query was canceled.", Err: ctx.Err()}
		}
		s.post(func() {
			if !s.liveSQLRun(id, d, o, connectionID) {
				if lease != nil {
					go closeSQLLease(lease)
				}
				return
			}
			d.run = nil
			o.cancel()
			d.State = SQLFinished
			d.Execution = result
			if result != nil {
				d.Transaction = result.Transaction
			}
			if err != nil {
				d.Err = asDBError(err)
			}
			if result != nil && result.Transaction == database.SQLSessionLost || result == nil && d.Err != nil && d.Err.Kind == database.KindConnection && lease != nil {
				d.Lost = true
				d.Transaction = database.SQLSessionLost
				d.lease = nil
				if lease != nil {
					go closeSQLLease(lease)
				}
			}
			d.Sequence++
			s.SetSQLResultPage(tab, 0, 1)
			s.advanceSQLAction()
		})
	}()
	return true
}
func (s *State) liveSQLRun(id TabID, d *SQLData, o *sqlRun, connectionID string) bool {
	tab := s.Workspace.Find(id)
	if tab == nil || tab.SQL != d || d.run != o {
		return false
	}
	if s.Conns.Epoch(connectionID) != o.epoch {
		d.run = nil
		o.cancel()
		d.State = SQLFinished
		d.Lost = true
		d.Transaction = database.SQLSessionLost
		d.lease = nil
		d.Err = noSession()
		d.Execution = &database.SQLExecution{Stop: database.SQLFailed, Transaction: database.SQLSessionLost, OutcomeUncertain: true, Err: d.Err}
		s.advanceSQLAction()
		return false
	}
	return true
}
func closeSQLLease(lease *query.Lease) {
	ctx, cancel := context.WithTimeout(context.Background(), database.SQLCleanupTimeout)
	defer cancel()
	_ = lease.Close(ctx)
}
func (s *State) CancelSQL(tab *Tab) {
	if tab != nil && tab.SQL != nil && tab.SQL.run != nil {
		tab.SQL.State = SQLCanceling
		tab.SQL.run.cancel()
	}
}
func (s *State) SetSQLResultPage(tab *Tab, result, page int) {
	if tab == nil || tab.SQL == nil {
		return
	}
	d := tab.SQL
	if d.Execution == nil || len(d.Execution.Results) == 0 {
		tab.Source = nil
		return
	}
	result = max(0, min(result, len(d.Execution.Results)-1))
	r := &d.Execution.Results[result]
	d.Result, d.Page = result, max(1, min(page, query.PageCount(r)))
	tab.Source = query.ResultSource(r, d.Page)
	d.Sequence++
}
