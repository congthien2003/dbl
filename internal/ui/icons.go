package ui

import (
	mygoui "github.com/egoist/mygo/ui"

	"dbl/resources"
)

// icon is an SVG of resources/ parsed once. The files draw in currentColor,
// so where one is shown sets its color with TextColor.
func icon(name string) *mygoui.SVG {
	data, err := resources.FS.ReadFile(name)
	if err != nil {
		panic("icon " + name + ": " + err.Error())
	}
	return mygoui.MustParseSVG(data)
}

// icons are the icons of the explorer and of the key columns.
var icons = struct {
	Folder, FolderOpen *mygoui.SVG
	Connection         *mygoui.SVG // the list of the databases of a connection
	Database           *mygoui.SVG
	Schema             *mygoui.SVG
	Table, View        *mygoui.SVG
	PrimaryKey         *mygoui.SVG
}{
	Folder:     icon("folder.svg"),
	FolderOpen: icon("folder-minus.svg"),
	Connection: icon("dropdown.svg"),
	Database:   icon("database-alt.svg"),
	Schema:     icon("folder-cog.svg"),
	Table:      icon("table.svg"),
	View:       icon("grid-circle-diagonal-right.svg"),
	PrimaryKey: icon("car-key.svg"),
}

// keyIcon is an icon a column shows, with its color and its name.
type keyIcon struct {
	svg   *mygoui.SVG
	color mygoui.Color
	label string
}

// primaryKeyIcon is the icon of a primary key column.
func primaryKeyIcon(t *mygoui.Theme) keyIcon {
	return keyIcon{icons.PrimaryKey, t.Warning, "Primary key column"}
}
