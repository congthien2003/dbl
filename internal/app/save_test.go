package app

import (
	"context"
	"errors"
	"testing"

	"dbl/internal/database"
	"dbl/internal/database/fakedb"
	"dbl/internal/grid"
)

const (
	colID    = 0
	colName  = 1
	colScore = 2
	colBlob  = 3
)

// edit makes a local draft of a text cell, the way the grid does.
func (e *sqlEnv) edit(tab *Tab, row, col int, text string) {
	e.t.Helper()
	d := tab.Data
	e.s.SetDraft(tab, row, col, grid.Value{Kind: d.Columns()[col].Kind, Text: text})
}

// dbName returns the name of user n in the fake database.
func (e *sqlEnv) dbName(table string, n int) string {
	return e.db.Table("public", table).Rows[n-1][colName].Text
}

func TestEditabilityFollowsTheMetadata(t *testing.T) {
	e := newSQLEnv(t)
	view := fakedb.UsersTable("v_users", 3)
	view.Meta.Kind = database.ObjectView
	e.db.AddTable("public", view)
	noKey := fakedb.UsersTable("nokey", 3)
	noKey.Meta.Constraints = database.Section[database.ConstraintMetadata]{}
	e.db.AddTable("public", noKey)

	for _, name := range []string{"v_users", "nokey"} {
		d := e.open(name).Data
		if !d.ReadOnly() || d.EditLock() == "" {
			t.Errorf("%s: ReadOnly=%v lock=%q", name, d.ReadOnly(), d.EditLock())
		}
		for _, c := range d.Columns() {
			if c.Editable || c.Reason == "" {
				t.Errorf("%s.%s: editable %v reason %q", name, c.Name, c.Editable, c.Reason)
			}
		}
	}
	d := e.open("small").Data
	if d.ReadOnly() || d.EditLock() != "" {
		t.Errorf("small: ReadOnly=%v lock=%q", d.ReadOnly(), d.EditLock())
	}
}

func TestDraftsKeepASnapshotAndGoAwayWhenEditedBack(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.edit(tab, 0, colName, "changed")
	if !tab.Dirty() || len(d.snapshots) != 1 {
		t.Fatalf("dirty %v snapshots %d", tab.Dirty(), len(d.snapshots))
	}
	if got := d.snapshots[d.RowID(0)].Values[colName].Text; got != "user 1" {
		t.Errorf("the snapshot holds %q", got)
	}
	// Editing back to the value that was loaded drops the draft and the snapshot.
	e.edit(tab, 0, colName, "user 1")
	if tab.Dirty() || len(d.snapshots) != 0 {
		t.Errorf("after editing back: dirty %v snapshots %d", tab.Dirty(), len(d.snapshots))
	}
	// NULL is not the empty string.
	e.s.SetDraft(tab, 0, colName, grid.Value{Kind: grid.KindNull})
	e.edit(tab, 1, colName, "")
	cs, issues := d.changeSet(&tab.Drafts)
	if len(issues) != 0 || len(cs.Rows) != 2 {
		t.Fatalf("changeset %+v issues %v", cs, issues)
	}
	if cs.Rows[0].Cells[0].Current.Kind != grid.KindNull || cs.Rows[1].Cells[0].Current.Kind == grid.KindNull {
		t.Errorf("NULL and empty were mixed up: %+v", cs.Rows)
	}
	e.s.RevertEdits(tab)
	if tab.Dirty() || len(d.snapshots) != 0 {
		t.Error("RevertEdits left edits behind")
	}
}

func TestRowIDsAreStableAcrossPagesAndReloads(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	first := d.RowID(0)
	seen := map[int64]bool{}
	for r := 0; r < d.Len(); r++ {
		if seen[d.RowID(r)] {
			t.Fatalf("row %d shares its number", r)
		}
		seen[d.RowID(r)] = true
	}
	e.s.ReloadTable(tab)
	e.post.run(t)
	if d.RowID(0) != first {
		t.Errorf("the same row got another number after a reload: %d, %d", first, d.RowID(0))
	}
	e.s.NextPage(tab)
	e.post.run(t)
	if seen[d.RowID(0)] {
		t.Error("a row of the next page shares a number with a row of the first")
	}
}

func TestSaveClearsEditsOnlyAfterCommitThenReloads(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.edit(tab, 0, colName, "first")
	e.edit(tab, 0, colScore, "9.25")
	e.edit(tab, 2, colName, "third")
	then := 0
	if !e.s.Save(tab, func() { then++ }) {
		t.Fatal("Save did not start")
	}
	if d.SaveState != OpLoading || !tab.Dirty() || !tab.Saving() || d.EditLock() == "" {
		t.Fatalf("while saving: state %v dirty %v lock %q", d.SaveState, tab.Dirty(), d.EditLock())
	}
	e.post.run(t) // the save finishes
	if tab.Dirty() || d.SaveState != OpReady || d.SaveErr != nil || then != 1 {
		t.Fatalf("after commit: dirty %v state %v err %v then %d", tab.Dirty(), d.SaveState, d.SaveErr, then)
	}
	if e.dbName("small", 1) != "first" || e.dbName("small", 3) != "third" ||
		e.db.Table("public", "small").Rows[0][colScore].Text != "9.25" || e.dbName("small", 2) != "user 2" {
		t.Errorf("the database holds the wrong values: %v", e.db.Table("public", "small").Rows)
	}
	if len(e.db.Saves()) != 1 || len(e.db.Saves()[0].Changes.Rows) != 2 {
		t.Errorf("saves %+v", e.db.Saves())
	}
	// The rows in view are older than the save: they cannot be edited until read again.
	if !d.Stale || d.PageState != OpLoading || d.EditLock() == "" {
		t.Errorf("after commit: stale %v page %v lock %q", d.Stale, d.PageState, d.EditLock())
	}
	e.post.run(t) // the reload finishes
	if d.Stale || d.EditLock() != "" || d.Value(0, colName).Text != "first" {
		t.Errorf("after reload: stale %v lock %q name %q", d.Stale, d.EditLock(), d.Value(0, colName).Text)
	}
}

func TestSaveWhoseReloadFailsReportsSavedAndKeepsEditingOff(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.edit(tab, 0, colName, "kept")
	e.s.Save(tab, nil)
	e.db.Hook = func(ctx context.Context, call string) error {
		if call == "FetchPage" {
			return &database.Error{Kind: database.KindConnection, Message: "gone"}
		}
		return nil
	}
	e.post.run(t) // committed
	e.post.run(t) // the reload fails
	if tab.Dirty() {
		t.Error("committed edits are still pending: they could be saved twice")
	}
	if !d.Stale || d.PageState != OpFailed || d.EditLock() == "" {
		t.Errorf("stale %v page %v lock %q", d.Stale, d.PageState, d.EditLock())
	}
	if d.Value(0, colName).Text != "user 1" {
		t.Errorf("the old rows should stay in view, got %q", d.Value(0, colName).Text)
	}
	// A later successful read ends it.
	e.db.Hook = nil
	e.s.ReloadTable(tab)
	e.post.run(t)
	if d.Stale || d.Value(0, colName).Text != "kept" {
		t.Errorf("after a good reload: stale %v name %q", d.Stale, d.Value(0, colName).Text)
	}
}

func TestSaveConflictRollsBackAndKeepsEdits(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.edit(tab, 0, colName, "mine 1")
	e.edit(tab, 1, colName, "mine 2")
	// Someone else changed the second row after it was read.
	e.db.Table("public", "small").Rows[1][colName] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	then := 0
	e.s.Save(tab, func() { then++ })
	e.post.run(t)
	var se *database.SaveError
	if d.SaveState != OpFailed || !errors.As(d.SaveErr, &se) || se.Err.Kind != database.KindConflict || se.Outcome != database.SaveNotApplied {
		t.Fatalf("state %v err %+v", d.SaveState, d.SaveErr)
	}
	if !tab.Dirty() || tab.Drafts.Len() != 2 || then != 0 {
		t.Errorf("edits lost or continuation ran: dirty %v drafts %d then %d", tab.Dirty(), tab.Drafts.Len(), then)
	}
	if e.dbName("small", 1) != "user 1" || e.dbName("small", 2) != "theirs" {
		t.Errorf("a partial write: %q, %q", e.dbName("small", 1), e.dbName("small", 2))
	}
	if d.RowErrors[d.RowID(1)] == "" || d.RowErrors[d.RowID(0)] != "" {
		t.Errorf("row errors %v", d.RowErrors)
	}
	if d.Stale {
		t.Error("a failed save marked the rows stale")
	}
	// Editing the row again clears its error.
	e.edit(tab, 1, colName, "mine 2b")
	if d.RowErrors[d.RowID(1)] != "" {
		t.Error("the error of an edited row stayed")
	}
}

func TestSaveWithUnknownCommitKeepsEditsAndNeverRetries(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.db.UnknownCommit = true
	e.edit(tab, 0, colName, "maybe")
	then := 0
	e.s.Save(tab, func() { then++ })
	e.post.run(t)
	if d.SaveState != OpFailed || d.SaveErr == nil || d.SaveErr.Outcome != database.SaveUnknown {
		t.Fatalf("state %v err %+v", d.SaveState, d.SaveErr)
	}
	if !tab.Dirty() || then != 0 || d.Stale {
		t.Errorf("dirty %v then %d stale %v", tab.Dirty(), then, d.Stale)
	}
	if len(e.db.Saves()) != 1 {
		t.Errorf("the save was sent %d times", len(e.db.Saves()))
	}
	// Saving again is the user's decision. Because the first write may have
	// landed, the optimistic check turns it into a conflict, not a double write.
	e.db.UnknownCommit = false
	e.s.Save(tab, nil)
	e.post.run(t)
	if d.SaveErr == nil || d.SaveErr.Err.Kind != database.KindConflict {
		t.Errorf("second save: %+v", d.SaveErr)
	}
}

func TestInvalidEditsWriteNothing(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.edit(tab, 0, colName, "fine")
	e.edit(tab, 1, colScore, "not a number")
	if e.s.Save(tab, nil) {
		t.Fatal("Save started with an invalid edit")
	}
	if len(e.db.Saves()) != 0 || d.SaveState != OpFailed || !tab.Dirty() {
		t.Errorf("saves %d state %v dirty %v", len(e.db.Saves()), d.SaveState, tab.Dirty())
	}
	if d.RowErrors[d.RowID(1)] == "" || d.RowErrors[d.RowID(0)] != "" {
		t.Errorf("row errors %v", d.RowErrors)
	}
	if d.SaveErr.Err.Kind != database.KindValidation {
		t.Errorf("kind %v", d.SaveErr.Err.Kind)
	}
}

func TestSaveWritesTheEditedRowEvenIfThePageWasReplaced(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.edit(tab, 0, colName, "edited") // user 1
	// The page is replaced under the edit by some path nobody guarded.
	req, number, ok := d.requestFor(navNext)
	if !ok {
		t.Fatal("no next page")
	}
	res, err := e.db.FetchPage(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	d.Page = d.buildPage(req, number, res)
	if d.Value(0, colID).Text != "101" {
		t.Fatalf("first row is %s", d.Value(0, colID).Text)
	}
	e.s.Save(tab, nil)
	e.post.run(t)
	if e.dbName("users", 1) != "edited" || e.dbName("users", 101) != "user 101" {
		t.Errorf("the edit went to the wrong row: user 1 = %q, user 101 = %q", e.dbName("users", 1), e.dbName("users", 101))
	}
}

func TestLateReadDoesNotReplaceRowsWithEdits(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	before := d.Page
	e.s.ReloadTable(tab) // a read is on its way
	e.edit(tab, 0, colName, "typed meanwhile")
	e.post.run(t)
	if d.Page != before || !tab.Dirty() || d.PageState != OpReady {
		t.Errorf("page replaced %v dirty %v state %v", d.Page != before, tab.Dirty(), d.PageState)
	}
}

func TestMetadataRefreshNeverOverwritesEdits(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	d := tab.Data
	e.edit(tab, 0, colName, "kept")
	// Unchanged structure: applied, edits stay.
	e.s.RefreshMetadata(tab)
	e.post.run(t)
	if d.MetaErr != nil || !tab.Dirty() || d.Page == nil {
		t.Errorf("same structure: err %v dirty %v", d.MetaErr, tab.Dirty())
	}
	// A column was added: not applied while edits exist.
	meta := e.db.Table("public", "small").Meta
	meta.Columns = append(meta.Columns, database.ColumnMetadata{Name: "extra", Class: database.ClassText, NativeType: "text", Nullable: true})
	oldMeta := d.Meta
	e.s.RefreshMetadata(tab)
	e.post.run(t)
	if d.Meta != oldMeta || d.MetaErr == nil || d.MetaErr.Kind != database.KindMetadataUnavailable {
		t.Fatalf("changed structure: meta replaced %v err %v", d.Meta != oldMeta, d.MetaErr)
	}
	if !tab.Dirty() || d.Page == nil || d.Len() != 3 {
		t.Errorf("edits or rows were disturbed: dirty %v len %d", tab.Dirty(), d.Len())
	}
	// Once the edits are gone, the refresh goes through.
	e.s.RevertEdits(tab)
	e.s.RefreshMetadata(tab)
	e.post.run(t) // metadata
	e.post.run(t) // the page, read again for the new layout
	if len(d.Columns()) != 5 || d.MetaErr != nil {
		t.Errorf("columns %d err %v", len(d.Columns()), d.MetaErr)
	}
}

// held reports ErrEditsPending when an action was held back for the user.
func held(e *sqlEnv) error {
	if e.s.PendingEdit != nil {
		return ErrEditsPending
	}
	return nil
}

// reads counts the page requests the fake database received.
func reads(e *sqlEnv) int { return len(e.db.Requests()) }

func TestActionsThatReplaceRowsAskFirst(t *testing.T) {
	actions := map[string]func(e *sqlEnv, tab *Tab) error{
		"filter": func(e *sqlEnv, tab *Tab) error {
			return e.s.SetFilter(tab, &database.Filter{Column: "name", Op: database.OpContains, Value: "user 1"})
		},
		"sort": func(e *sqlEnv, tab *Tab) error {
			return e.s.SetSort(tab, &database.Sort{Column: "name"})
		},
		"next":   func(e *sqlEnv, tab *Tab) error { e.s.NextPage(tab); return held(e) },
		"first":  func(e *sqlEnv, tab *Tab) error { e.s.FirstPage(tab); return held(e) },
		"reload": func(e *sqlEnv, tab *Tab) error { e.s.ReloadTable(tab); return held(e) },
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			e := newSQLEnv(t)
			tab := e.open("users")
			d := tab.Data
			e.edit(tab, 0, colName, "dirty")
			n := reads(e)
			err := act(e, tab)
			if !errors.Is(err, ErrEditsPending) || e.s.PendingEdit == nil || e.s.PendingEdit.Tab != tab.ID {
				t.Fatalf("err %v pending %+v", err, e.s.PendingEdit)
			}
			if reads(e) != n || d.PageState == OpLoading || !tab.Dirty() {
				t.Fatalf("the action ran before the user answered: reads %d->%d", n, reads(e))
			}
			// Cancel: nothing happens.
			e.s.ResolvePendingEdit(EditCancel)
			if e.s.PendingEdit != nil || !tab.Dirty() || reads(e) != n {
				t.Fatalf("Cancel: pending %v dirty %v reads %d", e.s.PendingEdit, tab.Dirty(), reads(e))
			}
			// Discard: the edits go and the action runs.
			_ = act(e, tab)
			e.s.ResolvePendingEdit(EditDiscard)
			if tab.Dirty() || len(d.snapshots) != 0 || e.s.PendingEdit != nil {
				t.Fatalf("Discard: dirty %v pending %v", tab.Dirty(), e.s.PendingEdit)
			}
			if d.PageState == OpLoading { // the first page may come from the cache
				e.post.run(t)
			}
			if d.Value(0, colName).Text == "dirty" {
				t.Error("the discarded edit is still shown")
			}
		})
	}
}

func TestPendingEditSaveRunsTheActionOnlyAfterCommit(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.edit(tab, 0, colName, "saved first")
	e.s.NextPage(tab)
	e.s.ResolvePendingEdit(EditSave)
	if d.SaveState != OpLoading || d.Page.Number != 1 {
		t.Fatalf("save %v page %d: the page changed before the commit", d.SaveState, d.Page.Number)
	}
	e.post.run(t) // committed; the reload and the next page are both started
	e.post.run(t) // the page read that is still current
	if e.dbName("users", 1) != "saved first" || d.Page.Number != 2 || tab.Dirty() {
		t.Errorf("name %q page %d dirty %v", e.dbName("users", 1), d.Page.Number, tab.Dirty())
	}
}

func TestPendingEditSaveFailureDoesNotRunTheAction(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("users")
	d := tab.Data
	e.edit(tab, 0, colName, "mine")
	e.db.Table("public", "users").Rows[0][colName] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	e.s.NextPage(tab)
	e.s.ResolvePendingEdit(EditSave)
	e.post.run(t)
	if d.Page.Number != 1 || !tab.Dirty() || d.SaveState != OpFailed || d.PageState == OpLoading {
		t.Errorf("page %d dirty %v save %v page state %v", d.Page.Number, tab.Dirty(), d.SaveState, d.PageState)
	}
}

func TestActionsAreRefusedWhileSaving(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	e.s.Save(tab, nil)
	if err := e.s.SetSort(tab, &database.Sort{Column: "name"}); !errors.Is(err, ErrSaving) {
		t.Errorf("SetSort while saving: %v", err)
	}
	if err := e.s.SetFilter(tab, &database.Filter{Column: "name", Op: database.OpEquals, Value: "x"}); !errors.Is(err, ErrSaving) {
		t.Errorf("SetFilter while saving: %v", err)
	}
	e.s.NextPage(tab)
	e.s.ReloadTable(tab)
	e.s.RevertEdits(tab)
	if e.s.PendingEdit != nil || !tab.Dirty() || len(e.db.Requests()) != 1 {
		t.Errorf("pending %v dirty %v reads %d", e.s.PendingEdit, tab.Dirty(), len(e.db.Requests()))
	}
	if e.s.Save(tab, nil) {
		t.Error("a second Save started while one was running")
	}
	e.post.run(t)
}

func TestClosingTheTabWhileSavingDropsTheLateResult(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	started := make(chan struct{})
	e.db.Hook = func(ctx context.Context, call string) error {
		if call == "SaveChanges" {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	e.s.Save(tab, func() { t.Error("the continuation ran") })
	<-started
	if e.s.SavingTabs() != 1 {
		t.Errorf("SavingTabs = %d", e.s.SavingTabs())
	}
	e.s.Workspace.Close(tab.ID) // stops the Save's context
	e.post.run(t)               // the late result reaches a tab that is gone
	if e.s.Workspace.Find(tab.ID) != nil {
		t.Error("the tab is back")
	}
}

func TestSaveWithoutASessionFails(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	e.s.Conns.Disconnect(e.connID)
	if e.s.Save(tab, nil) || tab.Data.SaveState != OpFailed || !tab.Dirty() {
		t.Errorf("state %v dirty %v", tab.Data.SaveState, tab.Dirty())
	}
	if tab.Data.SaveErr.Err.Kind != database.KindConnection {
		t.Errorf("kind %v", tab.Data.SaveErr.Err.Kind)
	}
}

// drain runs posted functions until done reports true.
func (e *sqlEnv) drain(done func() bool) {
	e.t.Helper()
	for i := 0; i < 8 && !done(); i++ {
		e.post.run(e.t)
	}
	if !done() {
		e.t.Fatal("the condition never became true")
	}
}

func TestSaveAllStopsAtTheFirstFailureAndSelectsThatTab(t *testing.T) {
	e := newSQLEnv(t)
	a := e.open("small")
	b := e.open("users")
	e.edit(a, 0, colName, "a edited")
	e.edit(b, 0, colName, "b edited")
	e.db.Table("public", "users").Rows[0][colName] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	e.s.Workspace.Select(a.ID)
	then := 0
	e.s.SaveAll([]*Tab{a, b}, func() { then++ })
	e.drain(func() bool { return b.Data.SaveState == OpFailed })
	if a.Dirty() || e.dbName("small", 1) != "a edited" {
		t.Errorf("the first tab was not saved: dirty %v name %q", a.Dirty(), e.dbName("small", 1))
	}
	if !b.Dirty() || then != 0 || e.s.Workspace.ActiveID != b.ID {
		t.Errorf("second tab dirty %v then %d active %d (want %d)", b.Dirty(), then, e.s.Workspace.ActiveID, b.ID)
	}
}

func TestSaveAllRunsTheContinuationAfterTheLastCommit(t *testing.T) {
	e := newSQLEnv(t)
	a := e.open("small")
	b := e.open("users")
	e.edit(a, 0, colName, "a edited")
	e.edit(b, 0, colName, "b edited")
	done := false
	e.s.SaveAll(e.s.Workspace.DirtyTabs(), func() { done = true })
	e.drain(func() bool { return done })
	if a.Dirty() || b.Dirty() || e.dbName("small", 1) != "a edited" || e.dbName("users", 1) != "b edited" {
		t.Errorf("a dirty %v b dirty %v", a.Dirty(), b.Dirty())
	}
	// Nothing to save: the continuation runs at once.
	ran := false
	e.s.SaveAll(nil, func() { ran = true })
	if !ran {
		t.Error("SaveAll of nothing did not continue")
	}
}

func TestSaveAndCloseClosesOnlyAfterCommit(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	if e.s.RequestClose(tab.ID) || e.s.PendingClose != tab.ID {
		t.Fatal("a dirty tab closed without asking")
	}
	e.s.SaveAndClose()
	if e.s.PendingClose != 0 || e.s.Workspace.Find(tab.ID) == nil {
		t.Fatal("the tab closed before the commit")
	}
	e.drain(func() bool { return e.s.Workspace.Find(tab.ID) == nil })
	if e.dbName("small", 1) != "x" {
		t.Errorf("name %q", e.dbName("small", 1))
	}
}

func TestSaveAndCloseKeepsTheTabWhenTheSaveFails(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	e.db.Table("public", "small").Rows[0][colName] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	e.s.RequestClose(tab.ID)
	e.s.SaveAndClose()
	e.post.run(t)
	if e.s.Workspace.Find(tab.ID) == nil || !tab.Dirty() || tab.Data.SaveState != OpFailed {
		t.Errorf("tab kept %v dirty %v state %v", e.s.Workspace.Find(tab.ID) != nil, tab.Dirty(), tab.Data.SaveState)
	}
}

func TestSaveAndQuitQuitsAfterEveryCommit(t *testing.T) {
	e := newSQLEnv(t)
	a := e.open("small")
	b := e.open("users")
	e.edit(a, 1, colName, "qa")
	e.edit(b, 1, colName, "qb")
	if e.s.RequestQuit() || !e.s.PendingQuit {
		t.Fatal("quit was allowed with unsaved edits")
	}
	quit := 0
	e.s.SaveAndQuit(func() { quit++ })
	if e.s.PendingQuit || quit != 0 {
		t.Fatalf("PendingQuit %v quit %d before the commits", e.s.PendingQuit, quit)
	}
	e.drain(func() bool { return quit > 0 })
	if quit != 1 || e.dbName("small", 2) != "qa" || e.dbName("users", 2) != "qb" {
		t.Errorf("quit %d, names %q %q", quit, e.dbName("small", 2), e.dbName("users", 2))
	}
}

func TestSaveAndQuitStaysOpenWhenASaveFails(t *testing.T) {
	e := newSQLEnv(t)
	a := e.open("small")
	e.edit(a, 0, colName, "x")
	e.db.Table("public", "small").Rows[0][colName] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	e.s.RequestQuit()
	quit := 0
	e.s.SaveAndQuit(func() { quit++ })
	e.post.run(t)
	if quit != 0 || !a.Dirty() {
		t.Errorf("quit %d dirty %v", quit, a.Dirty())
	}
}

func TestSaveAndConfirmDisconnectSavesThenEndsTheSession(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "before disconnect")
	if e.s.RequestDisconnect(e.connID) || e.s.PendingConn == nil || e.s.PendingConn.DirtyTabs != 1 {
		t.Fatalf("pending %+v", e.s.PendingConn)
	}
	e.s.SaveAndConfirmConn()
	if e.s.PendingConn != nil || e.s.Workspace.Find(tab.ID) == nil || e.s.Conns.Epoch(e.connID) == 0 {
		t.Fatal("the connection ended before the commit")
	}
	e.drain(func() bool { return e.s.Workspace.Find(tab.ID) == nil })
	if e.dbName("small", 1) != "before disconnect" {
		t.Errorf("name %q", e.dbName("small", 1))
	}
	if e.s.Conns.Epoch(e.connID) != 0 {
		t.Error("the session is still open")
	}
}

func TestSaveAndConfirmDisconnectKeepsEverythingWhenTheSaveFails(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	e.db.Table("public", "small").Rows[0][colName] = grid.Value{Kind: grid.KindText, Text: "theirs"}
	e.s.RequestDisconnect(e.connID)
	e.s.SaveAndConfirmConn()
	e.post.run(t)
	if e.s.Workspace.Find(tab.ID) == nil || !tab.Dirty() || e.s.Conns.Epoch(e.connID) == 0 {
		t.Errorf("tab kept %v dirty %v epoch %d", e.s.Workspace.Find(tab.ID) != nil, tab.Dirty(), e.s.Conns.Epoch(e.connID))
	}
}

func TestPendingConnCountsTabsBeingSaved(t *testing.T) {
	e := newSQLEnv(t)
	tab := e.open("small")
	e.edit(tab, 0, colName, "x")
	e.s.Save(tab, nil)
	e.s.RequestDisconnect(e.connID)
	if pc := e.s.PendingConn; pc == nil || pc.SavingTabs != 1 || pc.DirtyTabs != 1 {
		t.Errorf("pending %+v", pc)
	}
	e.post.run(t)
}
