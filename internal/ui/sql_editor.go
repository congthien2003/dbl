package ui

import (
	mygoui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"dbl/internal/app"
)

// sqlView is the view state of one SQL tab: its own text buffer and
// editor. Phase 2.1 only edits; running queries and saving files come later.
type sqlView struct {
	state *editor.State
}

func newSQLView() *sqlView {
	buf := editor.NewBuffer("")
	buf.SetLanguage(editor.LanguageNamed("SQL"))
	return &sqlView{state: &editor.State{
		Buffer:      buf,
		LineNumbers: editor.LineNumbersOn,
		Placeholder: "Write SQL here",
	}}
}

func (v *sqlView) build(c *mygoui.Context, t *mygoui.Theme, sh *Shell, tab *app.Tab) {
	label, color := "Connection: none", t.TextMuted
	if p, ok := sh.st.Catalog.Profile(tab.Table.ConnectionID); ok {
		label, color = sh.connectionStatus(t, p)
		label = "Connection: " + label
	}
	mygoui.Row(c).Padding(6, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		mygoui.Text(c, label).TextColor(color).SingleLine()
	})
	mygoui.Divider(c)
	editor.View(c, v.state).Grow(1).FillWidth().Label("SQL editor")
}
