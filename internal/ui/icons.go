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
	Properties         *mygoui.SVG
	ThemeSystem        *mygoui.SVG
	ThemeLight         *mygoui.SVG
	ThemeDark          *mygoui.SVG
	Close              *mygoui.SVG
}{
	Folder:     icon("folder.svg"),
	FolderOpen: icon("folder-minus.svg"),
	Connection: icon("dropdown.svg"),
	Database:   icon("database-alt.svg"),
	Schema:     icon("folder-cog.svg"),
	Table:      icon("table.svg"),
	View:       icon("grid-circle-diagonal-right.svg"),
	PrimaryKey: icon("car-key.svg"),
	Properties: icon("hashtag.svg"),
	ThemeSystem: icon("desktop.svg"),
	ThemeLight:  icon("sun.svg"),
	ThemeDark:   icon("moon-stars.svg"),
	// Close is two strokes; resources/ has no file for it.
	Close: mygoui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>`)),
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
