package ui

import (
	"fmt"
	"time"

	"dbl/internal/app"
	"dbl/internal/query"
	mygoui "github.com/egoist/mygo/ui"
)

func (v *sqlView) buildResults(c *mygoui.Context, t *mygoui.Theme, sh *Shell, tab *app.Tab) {
	d := tab.SQL
	mygoui.Row(c).Padding(5, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		status := "Session not opened"
		if d.Execution != nil {
			status = d.Execution.Transaction.String()
		} else if d.Lost {
			status = "Session lost"
		} else if d.State == app.SQLOpening {
			status = "Opening session…"
		} else if d.State == app.SQLRunning {
			status = "Running…"
		} else if d.State == app.SQLCanceling {
			status = "Canceling…"
		} else if d.HasSession() {
			status = d.Transaction.String()
		}
		if d.Busy() {
			mygoui.Spinner(c).Label("SQL query running")
		}
		mygoui.Text(c, status).TextColor(t.TextMuted).SingleLine()
		if d.Execution != nil {
			mygoui.Text(c, fmt.Sprintf("%s · %s", d.Execution.Stop.String(), d.Execution.Elapsed.Round(time.Millisecond))).TextColor(t.TextMuted).SingleLine()
		}
		if tab.Saving() {
			mygoui.Text(c, "Saving SQL file…").TextColor(t.TextMuted)
		}
	})
	if d.Document.FileErr != nil {
		mygoui.Text(c, "File: "+d.Document.FileErr.Error()).Padding(4, 12).TextColor(t.Danger)
	}
	if d.Err != nil {
		mygoui.Column(c).Padding(4, 12).Children(func() { errorBanner(c, t, d.Err, "Query failed") })
	}
	if d.Execution != nil && d.Execution.OutcomeUncertain {
		mygoui.Text(c, "The query outcome is unknown. Some changes may have completed. Check the database before executing again.").Padding(4, 12).TextColor(t.Warning)
	}
	if d.Execution == nil || len(d.Execution.Results) == 0 {
		centered(c, func() {
			label := "Execute a selection or the statement at the cursor."
			if d.Busy() {
				label = "Waiting for the database…"
			}
			mygoui.Text(c, label).TextColor(t.TextMuted)
		})
		return
	}
	run := d.Execution
	mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		labels := make([]string, len(run.Results))
		selected := ""
		for i, r := range run.Results {
			labels[i] = fmt.Sprintf("Result %d", i+1)
			if r.CommandTag != "" {
				labels[i] += " · " + r.CommandTag
			}
			if i == d.Result {
				selected = labels[i]
			}
		}
		before := selected
		mygoui.Select(c, &selected, labels).Width(260).Label("SQL result")
		if selected != before {
			for i, label := range labels {
				if label == selected {
					sh.st.SetSQLResultPage(tab, i, 1)
					c.Invalidate()
					break
				}
			}
		}
		r := &run.Results[d.Result]
		if r.HasRowsAffected {
			mygoui.Text(c, fmt.Sprintf("%d affected / returned rows", r.RowsAffected)).TextColor(t.TextMuted)
		}
		mygoui.Text(c, "Read-only").TextColor(t.TextMuted)
		if r.Truncated {
			mygoui.Text(c, "Snapshot limited to 1,000 rows / 16 MiB per execution").TextColor(t.Warning)
		}
	})
	for _, notice := range run.Notices {
		mygoui.Text(c, notice).Padding(2, 12).FontSize(12).TextColor(t.TextMuted)
	}
	if run.DroppedNotices > 0 {
		mygoui.Text(c, fmt.Sprintf("%d additional notices omitted", run.DroppedNotices)).Padding(2, 12).TextColor(t.TextMuted)
	}
	r := &run.Results[d.Result]
	if len(r.Columns) == 0 {
		centered(c, func() {
			label := "No completed row result. The command was interrupted."
			if r.CommandComplete {
				label = "Command completed without a row result."
			}
			mygoui.Text(c, label).TextColor(t.TextMuted)
		})
		return
	}
	if v.sequence != d.Sequence {
		v.results.newPage()
		v.sequence = d.Sequence
	}
	if tab.Source != nil {
		v.results.build(c, sh.st, tab, NewPalette(t))
	}
	mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		pages := query.PageCount(r)
		if mygoui.Button(c, "Previous result page").Disabled(d.Page <= 1).Clicked() {
			sh.st.SetSQLResultPage(tab, d.Result, d.Page-1)
			c.Invalidate()
		}
		mygoui.Text(c, fmt.Sprintf("Page %d / %d · %d retained rows", d.Page, pages, len(r.Rows))).TextColor(t.TextMuted)
		if mygoui.Button(c, "Next result page").Disabled(d.Page >= pages).Clicked() {
			sh.st.SetSQLResultPage(tab, d.Result, d.Page+1)
			c.Invalidate()
		}
	})
}
