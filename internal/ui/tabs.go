package ui

import (
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
)

// tabStrip draws the workspace tabs with their close buttons and the "+"
// button that opens a SQL tab.
func (sh *Shell) tabStrip(c *mygoui.Context, t *mygoui.Theme, p Palette) {
	ws := &sh.st.Workspace
	active := 0
	for i, tab := range ws.Tabs {
		if tab.ID == ws.ActiveID {
			active = i
		}
	}
	idx := active
	var closeID app.TabID
	mygoui.Row(c).Padding(4, 8, 0, 8).Gap(4).AlignItems(mygoui.End).Children(func() {
		tabs := mygoui.TabsBase(c, &idx, len(ws.Tabs))
		tabs.List.Gap(2).Children(func() {
			for i, tab := range ws.Tabs {
				item := tabs.Tab(i).Padding(4, 6, 4, 10).Gap(6).AlignItems(mygoui.Center).Radius(6, 6, 0, 0)
				if i == idx {
					item.Background(t.Surface)
				}
				item.Children(func() {
					mygoui.Text(c, tab.Title).SingleLine().MaxWidth(180)
					if tab.Dirty() {
						mygoui.Text(c, "●").Label("Unsaved changes").TextColor(t.Warning)
					}
					if mygoui.Button(c, "×").Label("Close "+tab.Title).Padding(0, 6).Radius(4).Clicked() {
						closeID = tab.ID
					}
				})
			}
		})
		if mygoui.Button(c, "+").Label("New SQL tab").Padding(2, 10).Clicked() {
			sh.newSQL()
			c.Invalidate()
		}
	})
	switch {
	case closeID != 0:
		sh.st.RequestClose(closeID)
		c.Invalidate()
	case idx != active && idx >= 0 && idx < len(ws.Tabs):
		ws.Select(ws.Tabs[idx].ID)
		c.Invalidate()
	}
}
