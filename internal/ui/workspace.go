package ui

import (
	"fmt"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/database"
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
		// The strip may have closed or switched tabs; show the current one.
		if tab = ws.Active(); tab == nil {
			return
		}
		v := sh.viewFor(tab)
		mygoui.Column(c.Key(tab.ID)).Grow(1).Children(func() {
			if tab.Kind == app.TabSQL {
				v.sql.build(c, t, sh, tab)
				return
			}
			sh.tableTab(c, t, p, tab, v)
		})
	})
}

// tableTab is the Table Inspector: Data (the grid) and Properties.
func (sh *Shell) tableTab(c *mygoui.Context, t *mygoui.Theme, p Palette, tab *app.Tab, v *tabView) {
	d := tab.Data
	mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		// The two views of the table, as a small segmented control with icons.
		seg := mygoui.SegmentedBase(c.Key("section"), &v.section, 2)
		seg.Track.Padding(2).Radius(6).Background(t.Surface).Gap(2).Label("Section").Children(func() {
			for i, s := range []struct {
				name string
				svg  *mygoui.SVG
			}{{"Data", icons.Table}, {"Properties", icons.Properties}} {
				item := sh.fade(seg.Segment(i)).Padding(2, 8).Radius(4).Gap(4).AlignItems(mygoui.Center).Label(s.name)
				if i == v.section {
					item.Background(t.Background)
				}
				item.Children(func() {
					mygoui.Icon(c, s.svg).FontSize(12)
					mygoui.Text(c, s.name).FontSize(12)
				})
			}
		})
		mygoui.Box(c).Grow(1)
		if d != nil && d.Loading() {
			mygoui.Spinner(c).Label("Loading")
			mygoui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		}
		if sh.fade(mygoui.Button(c, "New SQL")).Padding(2, 8).FontSize(12).Clicked() {
			sh.newSQL()
			c.Invalidate()
		}
		if d != nil {
			if sh.fade(mygoui.Button(c, "Reload")).Label("Reload data").Padding(2, 8).FontSize(12).Disabled(d.MetaState == app.OpLoading).Clicked() {
				sh.st.ReloadTable(tab)
				c.Invalidate()
			}
			if sh.fade(mygoui.Button(c, "Refresh metadata")).Padding(2, 8).FontSize(12).Disabled(d.MetaState == app.OpLoading).Clicked() {
				sh.st.RefreshMetadata(tab)
				c.Invalidate()
			}
		}
		if tab.Source != nil && tab.Source.ReadOnly() {
			mygoui.Text(c, "Read-only").FontSize(12).TextColor(t.TextMuted)
		}
		if tab.Saving() {
			mygoui.Spinner(c).Label("Saving")
			mygoui.Text(c, "Saving…").FontSize(12).TextColor(t.TextMuted)
		}
		if n := tab.Drafts.Len(); n > 0 {
			mygoui.Text(c, fmt.Sprintf("%d unsaved edit(s)", n)).FontSize(12).TextColor(t.Warning)
		}
		if tab.Data == nil || !tab.Data.ReadOnly() || tab.Dirty() {
			idle := !tab.Dirty() || tab.Saving()
			if sh.fade(mygoui.Button(c, "Revert changes")).Padding(2, 8).FontSize(12).Disabled(idle).Clicked() {
				v.grid.revert(c, tab)
			}
			if tab.Data != nil {
				if sh.fade(mygoui.PrimaryButton(c, "Save")).Label("Save changes").Padding(2, 8).FontSize(12).Disabled(idle).Clicked() {
					sh.save(c, tab, v)
				}
			}
		}
	})
	if tab.Data != nil && tab.Dirty() && !tab.Saving() && c.Shortcut(mygoui.Cmd, mygoui.KeyS) {
		sh.save(c, tab, v)
	}
	if d == nil {
		// A source that is not a database table (tests): the grid alone.
		if v.section == 1 {
			mygoui.Column(c).Grow(1).Center().Children(func() {
				mygoui.Text(c, "Properties are not available for this table.").TextColor(t.TextMuted)
			})
			return
		}
		v.grid.build(c, sh.st, tab, p)
		return
	}
	switch {
	case d.Meta == nil && d.MetaState == app.OpLoading:
		centered(c, func() {
			mygoui.Spinner(c).Label("Reading the table")
			mygoui.Text(c, "Reading the structure of the table…").TextColor(t.TextMuted)
		})
		return
	case d.Meta == nil:
		centered(c, func() {
			errorBanner(c, t, d.MetaErr, "The table could not be read.")
			if sh.fade(mygoui.Button(c, "Retry")).Clicked() {
				sh.st.ReloadTable(tab)
				c.Invalidate()
			}
		})
		return
	}
	if d.MetaErr != nil {
		mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
			errorBanner(c, t, d.MetaErr, "The metadata could not be refreshed.")
		})
	}
	sh.editBanners(c, t, tab)
	if v.section == 1 {
		properties(c, t, d.Meta)
		return
	}
	if v.filter == nil {
		v.filter = newFilterBar()
	}
	v.filter.build(c, t, sh, tab)
	if d.ReadOnly() {
		_, why := d.Meta.Editable()
		if why == "" {
			why = "No column of this table can be edited."
		}
		mygoui.Row(c).Padding(2, 12).Children(func() {
			mygoui.Text(c, "Read-only: "+why).FontSize(12).TextColor(t.TextMuted)
		})
	} else {
		// Reserve the hint line before selection and while editing, so the
		// table stays in place when the hint changes or disappears.
		note := v.grid.note(tab)
		mygoui.Row(c).Height(22).Shrink(0).Padding(2, 12).AlignItems(mygoui.Center).Children(func() {
			mygoui.Text(c, note).SingleLine().FontSize(12).TextColor(t.TextMuted).Tooltip(note)
		})
	}
	if d.Plan.Warning != "" {
		mygoui.Row(c).Padding(2, 12).Children(func() {
			mygoui.Text(c, "⚠ "+d.Plan.Warning).FontSize(12).TextColor(t.Warning)
		})
	}
	if d.PageErr != nil {
		mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
			errorBanner(c, t, d.PageErr, "The page could not be read.")
			if d.Page != nil {
				mygoui.Text(c, "Showing the previous rows.").FontSize(12).TextColor(t.TextMuted)
			}
			if sh.fade(mygoui.Button(c, "Retry")).Clicked() {
				sh.st.ReloadTable(tab)
				c.Invalidate()
			}
		})
	}
	switch {
	case d.Page == nil && d.PageState == app.OpLoading:
		centered(c, func() {
			mygoui.Spinner(c).Label("Loading rows")
			mygoui.Text(c, "Loading rows…").TextColor(t.TextMuted)
		})
	case d.Page == nil:
		centered(c, func() { mygoui.Text(c, "No rows loaded.").TextColor(t.TextMuted) })
	case d.Len() == 0:
		centered(c, func() {
			if d.Filter != nil {
				mygoui.Text(c, "No rows match the filter.").TextColor(t.TextMuted)
			} else {
				mygoui.Text(c, "This table has no rows.").TextColor(t.TextMuted)
			}
		})
	default:
		v.grid.build(c, sh.st, tab, p)
	}
	sh.pagingBar(c, t, tab)
}

// save writes the edits of a table tab. A cell being typed in is kept first,
// so what is on screen is what is saved.
func (sh *Shell) save(c *mygoui.Context, tab *app.Tab, v *tabView) {
	if v.grid.edit != nil {
		v.grid.commit(c, tab)
	}
	sh.st.Save(tab, nil)
	c.Invalidate()
}

// editBanners shows the outcome of the last Save, and the state of the rows
// after a Save that was committed.
func (sh *Shell) editBanners(c *mygoui.Context, t *mygoui.Theme, tab *app.Tab) {
	d := tab.Data
	if se := d.SaveErr; se != nil {
		text, color := "", t.Danger
		if se.Outcome == database.SaveUnknown {
			color = t.Warning
		} else {
			text = "Nothing was saved. "
		}
		text += se.Err.Kind.String() + ": " + se.Err.Message
		if se.Err.Hint != "" {
			text += " " + se.Err.Hint
		}
		mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
			mygoui.Text(c, text).TextColor(color)
			if se.Outcome == database.SaveNotApplied && se.HasRow {
				mygoui.Text(c, "The row is marked in the grid. Your edits are kept.").FontSize(12).TextColor(t.TextMuted)
			}
		})
	}
	if !d.Stale {
		return
	}
	mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		if d.PageState == app.OpLoading {
			mygoui.Spinner(c).Label("Reading the rows again")
			mygoui.Text(c, "Saved. Reading the rows again…").TextColor(t.TextMuted)
			return
		}
		mygoui.Text(c, "Saved. The table could not be read again, so the rows shown are from before the save and cannot be edited.").TextColor(t.Warning)
		if sh.fade(mygoui.Button(c, "Reload")).Label("Reload after save").Clicked() {
			sh.st.ReloadTable(tab)
			c.Invalidate()
		}
	})
}

// centered draws content in the middle of the room left.
func centered(c *mygoui.Context, content func()) {
	mygoui.Column(c).Grow(1).Center().Gap(8).Children(content)
}

// errorBanner shows an error with its category, its message and its hint.
func errorBanner(c *mygoui.Context, t *mygoui.Theme, err *database.Error, fallback string) {
	text := fallback
	if err != nil {
		text = err.Kind.String() + ": " + err.Message
		if err.Hint != "" {
			text += " " + err.Hint
		}
	}
	mygoui.Text(c, text).TextColor(t.Danger)
}

// pagingBar draws the page controls under the grid and says how the table
// is ordered and paged.
func (sh *Shell) pagingBar(c *mygoui.Context, t *mygoui.Theme, tab *app.Tab) {
	d := tab.Data
	busy := d.PageState == app.OpLoading
	mygoui.Divider(c)
	mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		hasPrev, hasNext := d.Page != nil && d.Page.HasPrev, d.Page != nil && d.Page.HasNext
		if sh.fade(mygoui.Button(c, "First")).Disabled(!hasPrev || busy).Clicked() {
			sh.st.FirstPage(tab)
			c.Invalidate()
		}
		if sh.fade(mygoui.Button(c, "Previous")).Disabled(!hasPrev || busy).Clicked() {
			sh.st.PrevPage(tab)
			c.Invalidate()
		}
		if sh.fade(mygoui.Button(c, "Next")).Disabled(!hasNext || busy).Clicked() {
			sh.st.NextPage(tab)
			c.Invalidate()
		}
		if d.Page != nil && len(d.Page.Rows) > 0 {
			first := d.Page.Start(d.Size)
			mygoui.Text(c, fmt.Sprintf("Page %d · rows %s–%s", d.Page.Number,
				formatCount(first), formatCount(first+len(d.Page.Rows)-1))).FontSize(12).SingleLine()
		}
		mygoui.Box(c).Grow(1)
		mygoui.Text(c, d.Plan.Describe()).FontSize(12).TextColor(t.TextMuted).SingleLine()
		if d.Sort != nil {
			dir := "ascending"
			if d.Sort.Desc {
				dir = "descending"
			}
			mygoui.Text(c, fmt.Sprintf("Sorted by %s, %s", d.Sort.Column, dir)).FontSize(12).TextColor(t.TextMuted).SingleLine()
			if sh.fade(mygoui.Button(c, "Clear sort")).Clicked() {
				_ = sh.st.SetSort(tab, nil)
				c.Invalidate()
			}
		}
	})
}
