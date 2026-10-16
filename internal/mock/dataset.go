// Package mock generates fake data for Phase 1: datasets computed from the
// row and column index, and a fake connection tree. Nothing here talks to a
// database.
package mock

import (
	"strconv"

	"dbl/internal/grid"
)

// Column describes a mock column.
type Column struct {
	Name       string
	Kind       grid.Kind
	Width      float32
	Editable   bool
	PrimaryKey bool
}

// Dataset is a table of generated rows. It stores no rows: Value computes
// a cell from its position, so a million rows cost nothing to hold.
type Dataset struct {
	Name     string
	Rows     int
	Columns  []Column
	ReadOnly bool
}

// colTemplate is repeated across the columns after the id column.
var colTemplate = []struct {
	name  string
	kind  grid.Kind
	width float32
}{
	{"name", grid.KindText, 160},
	{"amount", grid.KindNumber, 100},
	{"active", grid.KindBool, 80},
	{"created_at", grid.KindDateTime, 170},
	{"payload", grid.KindJSON, 200},
	{"blob", grid.KindBinary, 120},
	{"notes", grid.KindText, 260},
}

const notesTemplateIndex = 6

// longText is a value well past any display limit.
var longText = func() string {
	b := make([]byte, 0, 720)
	for len(b) < 720 {
		b = append(b, "lorem ipsum dolor sit amet "...)
	}
	return string(b)
}()

// NewDataset returns a dataset of rows x cols. Column 0 is a read-only
// numeric primary key; the others cycle through every value kind.
func NewDataset(name string, rows, cols int, readOnly bool) *Dataset {
	if cols < 1 {
		cols = 1
	}
	d := &Dataset{Name: name, Rows: rows, ReadOnly: readOnly, Columns: make([]Column, cols)}
	d.Columns[0] = Column{Name: "id", Kind: grid.KindNumber, Width: 80, PrimaryKey: true}
	for j := 1; j < cols; j++ {
		t := colTemplate[(j-1)%len(colTemplate)]
		colName := t.name
		if j > len(colTemplate) {
			colName += "_" + strconv.Itoa(j)
		}
		d.Columns[j] = Column{
			Name:     colName,
			Kind:     t.kind,
			Width:    t.width,
			Editable: !readOnly && t.kind != grid.KindBinary,
		}
	}
	return d
}

// RowID returns the stable identity of a row.
func (d *Dataset) RowID(row int) int64 { return int64(row) + 1 }

// Value returns the value at (row, col), computed from the position alone.
func (d *Dataset) Value(row, col int) grid.Value {
	if row < 0 || row >= d.Rows || col < 0 || col >= len(d.Columns) {
		return grid.Value{Kind: grid.KindNull}
	}
	id := d.RowID(row)
	if col == 0 {
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatInt(id, 10)}
	}
	h := hash(row, col)
	if h%7 == 0 {
		return grid.Value{Kind: grid.KindNull}
	}
	t := (col - 1) % len(colTemplate)
	switch colTemplate[t].kind {
	case grid.KindNumber:
		return grid.Value{Kind: grid.KindNumber, Text: strconv.FormatUint(h%1_000_000, 10)}
	case grid.KindBool:
		if h&1 == 0 {
			return grid.Value{Kind: grid.KindBool, Text: "true"}
		}
		return grid.Value{Kind: grid.KindBool, Text: "false"}
	case grid.KindDateTime:
		return grid.Value{Kind: grid.KindDateTime, Text: "2026-" + pad2(int(h%12)+1) + "-" + pad2(int(h/12%28)+1) + " 12:00:00"}
	case grid.KindJSON:
		return grid.Value{Kind: grid.KindJSON, Text: `{"id":` + strconv.FormatInt(id, 10) + `,"ok":true}`}
	case grid.KindBinary:
		return grid.Value{Kind: grid.KindBinary, Size: int(h%8192) + 1}
	}
	// Text columns.
	if h%11 == 0 {
		return grid.Value{Kind: grid.KindText}
	}
	if t == notesTemplateIndex {
		if h%13 == 0 {
			return grid.Value{Kind: grid.KindText, Text: longText}
		}
		return grid.Value{Kind: grid.KindText, Text: "Note " + strconv.FormatInt(id, 10)}
	}
	if h%5 == 0 {
		return grid.Value{Kind: grid.KindText, Text: "Việt Nam " + strconv.FormatInt(id, 10)}
	}
	return grid.Value{Kind: grid.KindText, Text: "Name " + strconv.FormatInt(id, 10)}
}

func hash(row, col int) uint64 {
	x := uint64(row)*0x9E3779B97F4A7C15 ^ uint64(col)*0xC2B2AE3D27D4EB4F
	x ^= x >> 33
	x *= 0xFF51AFD7ED558CCD
	x ^= x >> 33
	return x
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
