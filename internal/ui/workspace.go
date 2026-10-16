package ui

import (
	"fmt"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
)

// workspace draws the tab strip and the active tab, or the empty state.
func (sh *Shell) workspace(c *mygoui.Context, t *mygoui.Theme, p Palette) {
	ws := &sh.st.Workspace
	mygoui.Column(c).Fill().Children(func() {
		tab := ws.Active()
		if tab == nil {
			mygoui.Column(c).Fill().Center().Gap(6).Children(func() {
				mygoui.Text(c, "No tab open").FontSize(16).Bold()
				mygoui.Text(c, "Double-click a table in the explorer, or press Ctrl+N for a SQL tab.").TextColor(t.TextMuted)
			})
			return
		}
		sh.tabStrip(c, t, p)
		mygoui.Divider(c)
		// The strip may have closed or switched tabs; show the current one.
		if tab = ws.Active(); tab == nil {
			return
		}
		v := sh.viewFor(tab)
		mygoui.Column(c.Key(tab.ID)).Grow(1).Children(func() {
			if tab.Kind == app.TabSQL {
				v.sql.build(c, t, tab)
				return
			}
			sh.tableTab(c, t, p, tab, v)
		})
	})
}

// tableTab is the Table Inspector: Data (the grid) and Properties.
func (sh *Shell) tableTab(c *mygoui.Context, t *mygoui.Theme, p Palette, tab *app.Tab, v *tabView) {
	mygoui.Row(c).Padding(8, 12).Gap(12).AlignItems(mygoui.Center).Children(func() {
		mygoui.Text(c, fmt.Sprintf("%s.%s", tab.Table.Ref.Schema, tab.Table.Ref.Name)).Bold()
		mygoui.Segmented(c, &v.section, "Data", "Properties").Label("Section")
		mygoui.Box(c).Grow(1)
		if tab.Dataset.ReadOnly {
			mygoui.Text(c, "Read-only").TextColor(t.TextMuted)
		}
		if n := tab.Drafts.Len(); n > 0 {
			mygoui.Text(c, fmt.Sprintf("%d unsaved edit(s)", n)).TextColor(t.Warning)
		}
		if mygoui.Button(c, "Revert changes").Disabled(!tab.Dirty()).Clicked() {
			v.grid.revert(c, tab)
		}
	})
	if v.section == 1 {
		mygoui.Column(c).Grow(1).Center().Children(func() {
			mygoui.Text(c, "Properties are coming in a later phase.").TextColor(t.TextMuted)
		})
		return
	}
	v.grid.build(c, tab, p)
}
