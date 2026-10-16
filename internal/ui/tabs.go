package ui

import (
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
)

// tabSpan is the part of the line under the tab strip that the tab chosen
// leaves open, so that the tab runs into the panel below it.
type tabSpan struct{ x0, x1 float32 }

// tabStrip draws the workspace tabs with their close buttons. The tabs have a border and round corners at
// the top; a line runs under the strip, except under the tab chosen, which
// has no border below and the color of the panel.
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
	var gap tabSpan
	strip := mygoui.Row(c).Padding(4, 8, 0, 8).Gap(4).AlignItems(mygoui.End)
	// The gap is where the tab chosen is.
	strip.DrawOver(func(pt *mygoui.Painter, r mygoui.Rect) {
		y := r.Y + r.H - 1
		x0, x1 := r.X, r.X+r.W
		// The tab chosen may be scrolled partly or wholly out of the strip.
		g0, g1 := max(gap.x0, x0), min(gap.x1, x1)
		if g1 <= g0 {
			pt.Fill(mygoui.Rect{X: x0, Y: y, W: x1 - x0, H: 1}, t.Border, 0)
			return
		}
		pt.Fill(mygoui.Rect{X: x0, Y: y, W: g0 - x0, H: 1}, t.Border, 0)
		pt.Fill(mygoui.Rect{X: g1, Y: y, W: x1 - g1, H: 1}, t.Border, 0)
	})
	strip.Children(func() {
		// The tabs scroll sideways when there are more than the window shows:
		// with the touchpad, the wheel and the keys. The scroll bar would cover
		// the names, so it is moved far off to the right, out of the window: it
		// is drawn after its container's clip, so moving it down would not hide
		// it, and its track is out of reach of the pointer too.
		scroll := mygoui.ScrollHorizontal(c.Key("tab-scroll")).Grow(1).TrackScroll(&sh.tabScroll).
			ScrollbarInsets(0, 0, 0, 1e5)
		// A wheel that scrolls up and down scrolls the tabs sideways, as the
		// tabs of a browser do; a sideways wheel or touchpad, and Shift with
		// the wheel, scroll them already.
		scroll.HandleInput(func(ev mygoui.InputEvent) bool {
			st := &sh.tabScroll
			if ev.Kind != mygoui.InputScroll || ev.DX != 0 || ev.DY == 0 || st.MaxX <= 0 {
				return false
			}
			st.X = max(0, min(st.X+ev.DY, st.MaxX))
			return true
		})
		scroll.Children(func() {
			tabs := mygoui.TabsBase(c, &idx, len(ws.Tabs))
			tabs.List.Gap(2).Children(func() {
				for i, tab := range ws.Tabs {
					item := tabs.Tab(i).Padding(4, 6, 4, 10).Gap(6).AlignItems(mygoui.Center).
						Radius(6, 6, 0, 0).BorderWidth(1, 1, 0, 1).BorderColor(t.Border)
					switch {
					case i == idx:
						// The color of the panel, so that the two join.
						item.Background(t.Background)
						if r := item.LayoutBounds(); r.W > 0 {
							gap = tabSpan{r.X, r.X + r.W}
						}
						if sh.tabShown != tab.ID {
							// A new or newly chosen tab scrolls into view, in this
							// frame only.
							sh.tabShown = tab.ID
							item.ScrollIntoView()
						}
					case item.Hovered():
						item.Background(t.SurfaceHover)
					default:
						item.Background(t.Surface)
					}
					item.Children(func() {
						title := mygoui.Text(c, tab.Title).SingleLine()
						if tab.Kind == app.TabTable {
							// Tabs show the name of the table only; the tooltip
							// tells apart tables of the same name.
							title.Tooltip(tab.Table.Ref.Schema + "." + tab.Table.Ref.Name)
						}
						if tab.Dirty() {
							mygoui.Text(c, "●").Label("Unsaved changes").TextColor(t.Warning)
						}
						// A ghost button of an icon: no face until the pointer is over
						// it, then a pale red.
						closeBtn := mygoui.ButtonBase(c).Label("Close " + tab.Title).Padding(3).Radius(4).
							AlignItems(mygoui.Center).Justify(mygoui.Center)
						color := t.TextMuted
						if closeBtn.Hovered() {
							closeBtn.Background(t.Danger.Alpha(0.12))
							color = t.Danger
						}
						closeBtn.Children(func() { mygoui.Icon(c, icons.Close).FontSize(12).TextColor(color) })
						if closeBtn.Clicked() {
							closeID = tab.ID
						}
					})
				}
			})
		})
	})
	if gap != sh.tabGap {
		sh.tabGap = gap
		c.Invalidate()
	}
	switch {
	case closeID != 0:
		sh.st.RequestClose(closeID)
		c.Invalidate()
	case idx != active && idx >= 0 && idx < len(ws.Tabs):
		ws.Select(ws.Tabs[idx].ID)
		c.Invalidate()
	}
}
