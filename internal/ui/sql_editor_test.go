package ui

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestCtrlNOpensNewSQLTab(t *testing.T) {
	sh, tt := newTestShell(t)
	tt.Key(mygoui.Cmd, mygoui.KeyN)
	if tab := sh.st.Workspace.Active(); tab == nil || tab.Title != "Query 1" {
		t.Fatalf("after the first Ctrl+N the active tab is %+v", sh.st.Workspace.Active())
	}
	tt.Key(mygoui.Cmd, mygoui.KeyN)
	if tab := sh.st.Workspace.Active(); tab == nil || tab.Title != "Query 2" {
		t.Fatalf("after the second Ctrl+N the active tab is %+v", sh.st.Workspace.Active())
	}
}

func TestNewSQLButtonOpensSQLTab(t *testing.T) {
	sh, tt := newTestShell(t)
	openTable(t, sh, tt, "users")
	if tt.HasText("New SQL tab") {
		t.Error("the tab strip still has the + button")
	}
	if err := tt.Click("New SQL"); err != nil {
		t.Fatal(err)
	}
	if tab := sh.st.Workspace.Active(); tab == nil || tab.Title != "Query 1" {
		t.Errorf("active tab after New SQL is %+v", tab)
	}
}

func TestTableInspectorBarHasNoTableName(t *testing.T) {
	e, _ := newTablesEnv(t)
	openUsers(t, e)
	if e.tt.HasText("public.users") {
		t.Errorf("the bar under the tabs still shows the schema and table: %q", e.tt.Texts())
	}
	for _, want := range []string{"Data", "Properties", "Reload", "Refresh metadata", "New SQL"} {
		if !e.tt.HasText(want) {
			t.Errorf("missing %q: %q", want, e.tt.Texts())
		}
	}
}

func TestSQLTabsKeepSeparateBuffers(t *testing.T) {
	sh, tt := newTestShell(t)
	ws := &sh.st.Workspace
	a := ws.NewSQL("local-pg")
	tt.Frame()
	bufA := sh.views[a.ID].sql.state.Buffer
	bufA.Edit(editor.Edit{Start: 0, End: 0, Text: "select 1"})

	b := ws.NewSQL("local-pg")
	tt.Frame()
	bufB := sh.views[b.ID].sql.state.Buffer
	if bufB.String() != "" {
		t.Errorf("a new SQL tab starts with %q", bufB.String())
	}
	ws.Select(a.ID)
	tt.Frame()
	if got := sh.views[a.ID].sql.state.Buffer.String(); got != "select 1" {
		t.Errorf("the first SQL tab holds %q after switching back, want %q", got, "select 1")
	}
}

func TestSQLTabUsesSQLLanguageAndLineNumbers(t *testing.T) {
	v := newSQLView()
	if lang := v.state.Buffer.Language(); lang == nil || lang.Name != "SQL" {
		t.Errorf("language = %+v, want SQL", lang)
	}
	if v.state.LineNumbers != editor.LineNumbersOn {
		t.Errorf("LineNumbers = %v, want on", v.state.LineNumbers)
	}
}

func TestClosingSQLTabReleasesItsEditorState(t *testing.T) {
	sh, tt := newTestShell(t)
	tab := sh.st.Workspace.NewSQL("local-pg")
	tt.Frame()
	if sh.views[tab.ID] == nil {
		t.Fatal("no view state for the SQL tab")
	}
	sh.st.Workspace.Close(tab.ID)
	tt.Frame()
	if _, ok := sh.views[tab.ID]; ok {
		t.Error("the editor state outlived its tab")
	}
}
