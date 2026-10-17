package app

import (
	"errors"
	"fmt"

	"dbl/internal/database"
	"dbl/internal/grid"
)

// SetDraft records the edit of a cell as a local draft. Nothing is written to
// any database (spec FR-005: Enter confirms a local edit only). The first
// edit of a row keeps a snapshot of the row as it was loaded, which is what a
// later Save finds the row by and checks against. A value equal to that
// original removes the draft; a row with no draft left forgets its snapshot.
//
// For a source that is not a database table (Phase 1 tests) the draft is
// simply stored.
func (s *State) SetDraft(tab *Tab, row, col int, v grid.Value) {
	d := tab.Data
	if d == nil {
		src := tab.Source
		tab.Drafts.Set(grid.CellRef{RowID: src.RowID(row), Col: col}, src.Value(row, col), v)
		return
	}
	if d.Meta == nil || d.Page == nil || row < 0 || row >= len(d.Page.Rows) || col < 0 || col >= len(d.Meta.Columns) ||
		d.Page.IDs == nil || d.SaveState == OpLoading {
		return
	}
	id := d.RowID(row)
	snap := d.snapshots[id]
	if snap == nil {
		snap = &RowSnapshot{Values: append([]grid.Value(nil), d.Page.Rows[row]...)}
		for _, name := range d.Meta.Identity.Columns {
			snap.Key = append(snap.Key, database.KeyValue{Column: name, Value: snap.Values[d.Meta.ColumnIndex(name)]})
		}
		if d.snapshots == nil {
			d.snapshots = map[int64]*RowSnapshot{}
		}
		d.snapshots[id] = snap
	}
	tab.Drafts.Set(grid.CellRef{RowID: id, Col: col}, snap.Values[col], v)
	delete(d.RowErrors, id)
	if !tab.Drafts.HasRow(id) {
		delete(d.snapshots, id)
	}
	if !tab.Dirty() {
		d.SaveErr = nil
	}
}

// RevertEdits drops every unsaved edit of a table tab. A tab that is being
// saved keeps them: the Save decides.
func (s *State) RevertEdits(tab *Tab) {
	if tab.Saving() {
		return
	}
	tab.Drafts.Revert()
	if d := tab.Data; d != nil {
		d.snapshots = nil
		d.RowErrors = nil
		d.SaveErr = nil
		if d.SaveState == OpFailed {
			d.SaveState = OpIdle
		}
	}
}

// changeSet builds what a Save writes from the drafts and the snapshots of
// their rows. It never reads the page in view: rows are found by the identity
// they had when they were edited. Rows that cannot be described are returned
// as issues.
func (d *TableData) changeSet(dr *grid.Drafts) (database.ChangeSet, []database.Issue) {
	var cs database.ChangeSet
	var issues []database.Issue
	refs := dr.Refs()
	for i := 0; i < len(refs); {
		id := refs[i].RowID
		j := i
		for j < len(refs) && refs[j].RowID == id {
			j++
		}
		group := refs[i:j]
		i = j
		snap := d.snapshots[id]
		if snap == nil || len(snap.Values) != len(d.Meta.Columns) {
			issues = append(issues, database.Issue{RowID: id, Message: "The row was not recorded when it was edited. Discard the edits and edit it again."})
			continue
		}
		rc := database.RowChange{RowID: id, Key: snap.Key}
		for _, ref := range group {
			v, _ := dr.Get(ref)
			rc.Cells = append(rc.Cells, database.CellChange{
				Column: d.Meta.Columns[ref.Col].Name, Original: snap.Values[ref.Col], Current: v,
			})
		}
		cs.Rows = append(cs.Rows, rc)
	}
	return cs, issues
}

func asSaveError(err error) *database.SaveError {
	var se *database.SaveError
	if errors.As(err, &se) {
		return se
	}
	return &database.SaveError{Err: asDBError(err)}
}

// Save writes the pending edits of a table tab in one transaction (spec
// FR-005). It validates first and starts no write when anything is invalid.
// then, when not nil, runs on the UI thread after the changes are committed
// and never after a failure. Save reports whether the write started; the
// result arrives later in the tab's SaveState and SaveErr.
func (s *State) Save(tab *Tab, then func()) bool {
	d := tab.Data
	if d == nil || d.Meta == nil || !tab.Dirty() || d.SaveState == OpLoading {
		return false
	}
	cs, issues := d.changeSet(&tab.Drafts)
	issues = append(issues, database.ValidateChanges(d.Meta, cs)...)
	d.RowErrors = nil
	if len(issues) > 0 {
		s.saveRejected(d, issues)
		return false
	}
	ad, ok := s.Conns.SQL(d.Key.ConnectionID)
	o := d.saveOp.begin(s, d.Key.ConnectionID, SaveTimeout)
	if !ok || o == nil {
		d.SaveState, d.SaveErr = OpFailed, &database.SaveError{Err: noSession()}
		return false
	}
	d.SaveState, d.SaveErr = OpLoading, nil
	id, req := tab.ID, database.SaveRequest{Meta: d.Meta, Changes: cs}
	go func() {
		err := ad.SaveChanges(o.ctx, req)
		s.post(func() { s.finishSave(id, d, o, err, then) })
	}()
	return true
}

// saveRejected records that edits are not valid, without having written.
func (s *State) saveRejected(d *TableData, issues []database.Issue) {
	d.RowErrors = map[int64]string{}
	for _, is := range issues {
		if _, seen := d.RowErrors[is.RowID]; seen {
			continue
		}
		msg := is.Message
		if is.Column != "" {
			msg = is.Column + ": " + msg
		}
		d.RowErrors[is.RowID] = msg
	}
	d.SaveState = OpFailed
	d.SaveErr = &database.SaveError{Err: &database.Error{
		Kind:    database.KindValidation,
		Message: fmt.Sprintf("%s. Nothing was saved.", issues[0].Message),
		Hint:    "Fix the cells marked in red, or discard the edits.",
	}}
}

// finishSave applies the result of a Save. Unlike a read, the result of a
// write is applied even when its context has ended: the write may have been
// committed, and the edits must not stay as if it had not. It is dropped only
// when the tab is gone.
func (s *State) finishSave(id TabID, d *TableData, o *op, err error, then func()) {
	defer func() {
		if err != nil {
			s.cancelSQLActionFor(id)
		} else {
			s.advanceSQLAction()
		}
	}()
	d.saveOp.release(o)
	tab := s.liveTab(id, d)
	if tab == nil {
		return
	}
	if err != nil {
		se := asSaveError(err)
		d.SaveState, d.SaveErr = OpFailed, se
		if se.HasRow {
			d.RowErrors = map[int64]string{se.RowID: se.Err.Message}
		}
		if then != nil {
			// A chain of Saves (closing, quitting) stopped here: show why.
			s.Workspace.Select(tab.ID)
		}
		return
	}
	// Committed. Only now do the edits go. The rows in view are older than
	// the save, so they are read again; until that works they cannot be
	// edited.
	tab.Drafts.Revert()
	d.snapshots, d.RowErrors, d.SaveErr = nil, nil, nil
	d.SaveState = OpReady
	d.Stale = true
	d.cache.Clear()
	s.fetchPage(tab, navReload)
	if then != nil {
		then()
	}
}

// SavingTabs counts the tabs that are being saved.
func (s *State) SavingTabs() int {
	n := 0
	for _, t := range s.Workspace.Tabs {
		if t.Saving() {
			n++
		}
	}
	return n
}

// SaveAll saves tabs one after another and runs then after the last one is
// committed. It stops at the first tab that fails (that tab is selected so the
// user sees why) and then never runs.
func (s *State) SaveAll(tabs []*Tab, then func()) {
	var queue []*Tab
	for _, t := range tabs {
		if t.Dirty() {
			queue = append(queue, t)
		}
	}
	var step func()
	step = func() {
		for len(queue) > 0 {
			t := queue[0]
			queue = queue[1:]
			if s.Workspace.Find(t.ID) != t || !t.Dirty() {
				continue
			}
			if t.SQL != nil {
				if !s.SaveSQLFile(t, false, step) {
					return
				}
				return
			}
			if !s.Save(t, step) {
				s.Workspace.Select(t.ID)
			}
			return
		}
		if then != nil {
			then()
		}
	}
	step()
}
