package ui

import (
	mygoui "github.com/egoist/mygo/ui"

	"dbl/internal/connection"
)

// pickerColumns is how many database types a row of the picker holds.
const pickerColumns = 3

// driverPickerView draws the first step of a new connection: a tile for
// each database type, with its icon and name. A type this version cannot
// connect to is dimmed, marked "Soon" and cannot be chosen. It reports the
// type the user clicked, if any.
func (sh *Shell) driverPickerView(c *mygoui.Context, t *mygoui.Theme, f *connForm) (picked connection.DriverType, ok bool) {
	mygoui.Text(c, "Choose a database type").TextColor(t.TextMuted)
	mygoui.Grid(c).Columns(pickerColumns).Gap(12).Children(func() {
		for _, d := range connection.Drivers {
			supported := d.Supported()
			tile := sh.fade(mygoui.ButtonBase(c)).Label(d.Label()).Padding(16, 12).Radius(8).
				Background(t.Surface).Disabled(!supported)
			if supported && tile.Hovered() {
				tile.Background(t.SurfaceHover)
			}
			tile.Children(func() {
				mygoui.Column(c).Gap(8).AlignItems(mygoui.Center).Children(func() {
					mygoui.Icon(c, driverIcon(d)).FontSize(32).TextColor(t.Text)
					mygoui.Text(c, d.Label()).Bold()
					if !supported {
						mygoui.Text(c, "Soon").FontSize(12).TextColor(t.TextMuted)
					}
				})
			})
			if !supported {
				tile.Opacity(0.5)
			}
			if supported && tile.Clicked() {
				picked, ok = d, true
			}
		}
	})
	return picked, ok
}
