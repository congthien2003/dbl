package ui

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/connection"
)

func TestPickerShowsAllDriverNames(t *testing.T) {
	e := newEmptyEnv(t)
	openNewPicker(t, e)
	for _, want := range []string{"PostgreSQL", "SQLite", "MySQL", "SQL Server", "Redis"} {
		if !e.tt.HasText(want) {
			t.Errorf("the picker lacks %q; texts %q", want, e.tt.Texts())
		}
	}
}

func TestPickerDisablesUnsupportedDrivers(t *testing.T) {
	e := newEmptyEnv(t)
	openNewPicker(t, e)
	if !e.tt.HasText("Soon") {
		t.Error("an unsupported driver is not marked Soon")
	}
	for _, name := range []string{"SQL Server", "Redis"} {
		click(t, e.tt, name)
		if e.sh.form.step != stepPickDriver || e.sh.form.driver != "" {
			t.Fatalf("clicking %s chose it: step %v driver %q", name, e.sh.form.step, e.sh.form.driver)
		}
	}
}

func TestPickerCancelClosesDialog(t *testing.T) {
	e := newEmptyEnv(t)
	openNewPicker(t, e)
	click(t, e.tt, "Cancel")
	if e.sh.form != nil || e.tt.HasText("Choose a database type") {
		t.Errorf("the picker stayed open; texts %q", e.tt.Texts())
	}
}

// The picker has no scrolling area; at the smallest window its tiles and
// Cancel must still be on screen.
func TestPickerFitsTheSmallestWindow(t *testing.T) {
	e := newEmptyEnv(t)
	e.tt.SetSize(800, 500)
	e.sh.st.Settings.Window.Width, e.sh.st.Settings.Window.Height = 800, 500
	e.tt.Frame()
	openNewPicker(t, e)
	for _, label := range []string{"PostgreSQL", "SQLite", "MySQL", "SQL Server", "Redis", "Cancel"} {
		r, ok := e.tt.Find(label)
		if !ok {
			t.Fatalf("no %q on screen", label)
		}
		if r.W == 0 || r.H == 0 || r.Y < 0 || r.Y+r.H > 500 || r.X < 0 || r.X+r.W > 800 {
			t.Errorf("%q at %+v is outside the 800x500 window", label, r)
		}
	}
}

func TestPickerEscapeClosesDialog(t *testing.T) {
	e := newEmptyEnv(t)
	openNewPicker(t, e)
	e.tt.Key(0, mygoui.KeyEscape)
	if e.sh.form != nil {
		t.Errorf("Escape left the picker open; texts %q", e.tt.Texts())
	}
}

func TestDriversListsEveryDriverOnce(t *testing.T) {
	// The picker draws connection.Drivers; this keeps its icon switch total.
	seen := map[connection.DriverType]bool{}
	for _, d := range connection.Drivers {
		if seen[d] {
			t.Errorf("%s is listed twice", d)
		}
		seen[d] = true
		if driverIcon(d) == nil {
			t.Errorf("no icon for %s", d)
		}
	}
}
