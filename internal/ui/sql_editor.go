package ui

import (
	"fmt"
	mygoui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"dbl/internal/app"
	"dbl/internal/database"
)

// sqlView keeps one SQL tab's editor buffer, splitter and result grid state.
type sqlView struct {
	state             *editor.State
	owner             *app.State
	bound             bool
	version, revision uint64
	split             float32
	results           *gridView
	sequence          int
}

func newSQLView() *sqlView {
	buf := editor.NewBuffer("")
	buf.SetLanguage(editor.LanguageNamed("SQL"))
	return &sqlView{split: 55, results: newGridView(), state: &editor.State{
		Buffer:      buf,
		LineNumbers: editor.LineNumbersOn,
		Placeholder: "Write SQL here",
	}}
}

func (v *sqlView) build(c *mygoui.Context, t *mygoui.Theme, sh *Shell, tab *app.Tab) {
	v.owner = sh.st
	v.bind(tab)
	v.syncDocument(tab)
	d := tab.SQL
	modal := sh.sqlModalOpen()
	if !modal && c.Shortcut(mygoui.Cmd, mygoui.KeyS) {
		sh.st.SaveSQLFile(tab, false, nil)
		c.Invalidate()
	}
	if !modal && c.Shortcut(mygoui.Cmd, mygoui.KeyEnter) {
		v.execute(sh, tab)
		c.Invalidate()
	}
	mygoui.Row(c).Padding(6, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		labels := []string{"Connection: none"}
		ids := []string{""}
		selected := labels[0]
		seen := map[string]int{}
		for _, p := range sh.st.Catalog.Profiles {
			if !p.Driver.Supported() {
				continue
			}
			label := fmt.Sprintf("%s · %s (%s@%s)", p.Name, p.Database, p.Username, p.Host)
			seen[label]++
			if n := seen[label]; n > 1 {
				label = fmt.Sprintf("%s (%d)", label, n)
			}
			labels = append(labels, label)
			ids = append(ids, p.ID)
			if p.ID == tab.Table.ConnectionID {
				selected = label
			}
		}
		before := selected
		mygoui.Select(c, &selected, labels).Width(280).Label("SQL connection").Disabled(modal)
		if selected != before {
			for i, label := range labels {
				if label == selected {
					sh.st.RequestSQLConnection(tab, ids[i])
					c.Invalidate()
					break
				}
			}
		}
		if sh.fade(mygoui.PrimaryButton(c, "Execute")).Disabled(modal || d.Busy()).Clicked() {
			v.execute(sh, tab)
			c.Invalidate()
		}
		if sh.fade(mygoui.Button(c, "Cancel query")).Disabled(modal || !d.Busy() || d.State == app.SQLCanceling).Clicked() {
			sh.st.CancelSQL(tab)
			c.Invalidate()
		}
		mygoui.Box(c).Grow(1)
		if sh.fade(mygoui.Button(c, "Open SQL")).Disabled(modal).Clicked() {
			sh.st.OpenSQLFile()
			c.Invalidate()
		}
		if sh.fade(mygoui.Button(c, "Save SQL")).Disabled(modal || tab.Saving()).Clicked() {
			sh.st.SaveSQLFile(tab, false, nil)
			c.Invalidate()
		}
		if sh.fade(mygoui.Button(c, "Save As")).Disabled(modal || tab.Saving()).Clicked() {
			sh.st.SaveSQLFile(tab, true, nil)
			c.Invalidate()
		}
	})
	mygoui.Divider(c)
	mygoui.SplitVerticalPercent(c, &v.split, func() {
		mygoui.Column(c).Fill().Children(func() { editor.View(c, v.state).Grow(1).FillWidth().Label("SQL editor"); v.syncDocument(tab) })
	}, func() { mygoui.Column(c).Fill().Children(func() { v.buildResults(c, t, sh, tab) }) }).Grow(1).FillWidth()
}

func (v *sqlView) bind(tab *app.Tab) {
	if v.bound || tab.SQL == nil {
		return
	}
	v.state.Buffer = editor.NewBuffer(tab.SQL.Document.Text)
	v.state.Buffer.SetLanguage(editor.LanguageNamed("SQL"))
	v.version = v.state.Buffer.Version()
	v.revision = tab.SQL.Document.Revision
	v.bound = true
}
func (v *sqlView) syncDocument(tab *app.Tab) {
	if tab.SQL == nil || v.owner == nil {
		return
	}
	v.bind(tab)
	version := v.state.Buffer.Version()
	if version != v.version {
		v.owner.SetSQLText(tab, v.state.Buffer.String())
		v.version = version
		v.revision = tab.SQL.Document.Revision
	}
}
func (v *sqlView) execute(sh *Shell, tab *app.Tab) {
	v.syncDocument(tab)
	in := database.SQLInput{Text: tab.SQL.Document.Text, Cursor: v.state.Cursor()}
	for _, sel := range v.state.Selections() {
		in.Selections = append(in.Selections, database.SQLSpan{Start: sel.Start(), End: sel.End()})
	}
	sh.st.ExecuteSQL(tab, in, false)
}
