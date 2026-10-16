package ui

import (
	"errors"
	"slices"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/app"
	"dbl/internal/database"
)

// filterDebounce is how long typing must pause before the filter runs on the
// server (spec FR-006: about 300 ms). Changing the column or the operator
// applies at once.
var filterDebounce = 300 * time.Millisecond

// filterBar is the view state of the filter bar above a grid: the column,
// operator and text being edited. The filter itself lives in the app state
// (app.TableData.Filter); the bar sends it when it differs.
type filterBar struct {
	col  string
	op   string // the label of the operator
	text string

	last      [3]string // col, op, text of the previous frame
	changedAt time.Time
	immediate bool

	refused *database.Filter // a filter the state refused, not sent again
	err     string

	// While the table has unsaved edits the filter is not applied as it is
	// typed: a question about them would interrupt the typing. apply is set by
	// the Apply button or Enter; asked is set once the state held the filter
	// back for the user's answer.
	apply, asked bool
}

func newFilterBar() *filterBar { return &filterBar{} }

// opLabels lists the labels of the operators that apply to a class.
func opLabels(class database.ColumnClass) []string {
	ops := database.OperatorsFor(class)
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.Label()
	}
	return out
}

func opByLabel(class database.ColumnClass, label string) database.FilterOp {
	for _, op := range database.OperatorsFor(class) {
		if op.Label() == label {
			return op
		}
	}
	return 0
}

// desired returns the filter the bar describes: nil when it describes none
// (a comparison with no value yet).
func (fb *filterBar) desired(m *database.TableMetadata) *database.Filter {
	col, ok := m.Column(fb.col)
	if !ok {
		return nil
	}
	op := opByLabel(col.Class, fb.op)
	if op == 0 || (op.NeedsValue() && fb.text == "") {
		return nil
	}
	f := &database.Filter{Column: fb.col, Op: op}
	if op.NeedsValue() {
		f.Value = fb.text
	}
	return f
}

// build draws the bar and sends the filter to the state when it should.
func (fb *filterBar) build(c *mygoui.Context, t *mygoui.Theme, sh *Shell, tab *app.Tab) {
	st := sh.st
	d := tab.Data
	m := d.Meta
	names := make([]string, len(m.Columns))
	for i, col := range m.Columns {
		names[i] = col.Name
	}
	if !slices.Contains(names, fb.col) {
		fb.col = names[0]
	}
	class := m.Columns[slices.Index(names, fb.col)].Class
	labels := opLabels(class)
	if fb.last[0] != fb.col || !slices.Contains(labels, fb.op) {
		// A new column starts with the default operator of its type.
		fb.op = labels[0]
	}
	needsValue := opByLabel(class, fb.op).NeedsValue()
	dirty := tab.Dirty()
	same := func(a, b *database.Filter) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
	if fb.asked && st.PendingEdit == nil && !tab.Saving() {
		// The question was answered. If the edits are still there and nothing
		// is being saved, the answer was Cancel (or the Save failed): the bar
		// goes back to the filter that is applied. While the Save runs the
		// answer is Save, and the filter is applied when it is committed.
		fb.asked = false
		if tab.Dirty() && !same(fb.desired(m), d.Filter) {
			fb.restore(d.Filter)
			return
		}
	}

	mygoui.Row(c).Padding(4, 12).Gap(8).AlignItems(mygoui.Center).Children(func() {
		mygoui.Text(c, "Filter").TextColor(t.TextMuted)
		mygoui.Select(c, &fb.col, names).Label("Filter column")
		mygoui.Select(c, &fb.op, labels).Label("Filter operator")
		if needsValue {
			if mygoui.TextInput(c, &fb.text).Placeholder("Value").Label("Filter value").Width(240).Submitted() && dirty {
				fb.apply = true
			}
		}
		if dirty {
			if sh.fade(mygoui.Button(c, "Apply")).Label("Apply filter").Clicked() {
				fb.apply = true
			}
		}
		if sh.fade(mygoui.Button(c, "Clear")).Disabled(fb.text == "" && d.Filter == nil).Clicked() {
			fb.text = ""
			fb.op = opLabels(class)[0]
		}
		if d.Filter != nil && d.PageState == app.OpLoading {
			mygoui.Spinner(c).Label("Filtering")
		}
		if fb.err != "" {
			mygoui.Text(c, fb.err).TextColor(t.Danger).SingleLine()
		}
	})

	// A change of column or operator is sent at once, typing after a pause.
	cur := [3]string{fb.col, fb.op, fb.text}
	if cur != fb.last {
		fb.immediate = fb.last[0] != cur[0] || fb.last[1] != cur[1] || (fb.last[2] != "" && cur[2] == "")
		fb.last, fb.changedAt = cur, c.Now()
		fb.err = ""
	}
	want := fb.desired(m)
	have := d.Filter
	if same(want, have) || same(want, fb.refused) {
		fb.apply = false
		return
	}
	if dirty {
		// With unsaved edits, only an explicit Apply asks the question.
		if !fb.apply {
			return
		}
		fb.apply = false
	} else if wait := filterDebounce - c.Now().Sub(fb.changedAt); !fb.immediate && wait > 0 {
		c.After(wait)
		return
	}
	switch err := st.SetFilter(tab, want); {
	case err == nil:
		fb.refused = nil
		c.Invalidate()
	case errors.Is(err, app.ErrEditsPending):
		fb.asked = true
		c.Invalidate()
	case errors.Is(err, app.ErrSaving):
		fb.err = "Wait for the save to finish."
	default:
		fb.refused = want
		fb.err = err.Error()
	}
}

// restore puts the bar back to show the filter that is applied.
func (fb *filterBar) restore(f *database.Filter) {
	fb.refused = nil
	if f == nil {
		fb.text = ""
		fb.last = [3]string{fb.col, fb.op, ""}
		return
	}
	fb.col, fb.op, fb.text = f.Column, f.Op.Label(), f.Value
	fb.last = [3]string{fb.col, fb.op, fb.text}
}
