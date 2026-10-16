// Package resources embeds the SVG icons kept next to this file. They draw
// in currentColor, so the code that shows one chooses its color.
package resources

import "embed"

// FS holds the *.svg files of this folder.
//
//go:embed *.svg
var FS embed.FS
