package ui

import (
	"github.com/egoist/mygo/ui/editor"
	"testing"

	"dbl/internal/app"
	"dbl/internal/database"
	"dbl/internal/grid"
)

func TestSQLDuplicateColumnIDs(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := sh.st.Workspace.NewSQL("")
	tab.SQL.Execution = &database.SQLExecution{Ready: true, Results: []database.SQLResult{{Columns: []database.SQLColumn{{Name: "x", Kind: grid.KindText}, {Name: "x", Kind: grid.KindText}}, Rows: [][]grid.Value{{{Kind: grid.KindText, Text: "a"}, {Kind: grid.KindText, Text: "b"}}}}}}
	tab.SQL.State = app.SQLFinished
	sh.st.SetSQLResultPage(tab, 0, 1)
	tt.Frame()
	cols := sh.views[tab.ID].sql.results.columns(tab.Source)
	if len(cols) != 2 || cols[0].ID == cols[1].ID {
		t.Fatal("duplicate SQL labels share grid identity")
	}
	if !tab.Source.ReadOnly() {
		t.Fatal("SQL result is editable")
	}
}
func TestSQLBufferInactiveDirtyGuard(t *testing.T) {
	sh, tt := newTestShell(t)
	a := sh.st.Workspace.NewSQL("")
	tt.Frame()
	buf := sh.views[a.ID].sql.state.Buffer
	buf.Edit(editor.Edit{Start: 0, End: 0, Text: "select 'tiếng Việt';"})
	sh.st.Workspace.NewSQL("")
	if sh.RequestQuit() || !a.Dirty() || sh.st.PendingSQL == nil {
		t.Fatal("inactive editor bypassed quit guard")
	}
}
