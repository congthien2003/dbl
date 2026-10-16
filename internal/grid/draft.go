package grid

import (
	"cmp"
	"slices"
)

// CellRef identifies a cell by its row's identity and its column index.
// RowID is a stable identity, never derived from display strings.
type CellRef struct {
	RowID int64
	Col   int
}

// Drafts holds local, unsaved edits keyed by cell. A draft that equals the
// original value is not kept, so editing a cell back clears its dirty
// state. The zero value is ready to use.
type Drafts struct {
	m map[CellRef]Value
}

// Set records v as the draft of ref, or removes the draft when v equals
// the original value.
func (d *Drafts) Set(ref CellRef, original, v Value) {
	if v.Equal(original) {
		delete(d.m, ref)
		return
	}
	if d.m == nil {
		d.m = make(map[CellRef]Value)
	}
	d.m[ref] = v
}

// Get returns the draft of ref.
func (d *Drafts) Get(ref CellRef) (Value, bool) {
	v, ok := d.m[ref]
	return v, ok
}

// IsDirty reports whether ref has a draft.
func (d *Drafts) IsDirty(ref CellRef) bool {
	_, ok := d.m[ref]
	return ok
}

// Len returns the number of drafts.
func (d *Drafts) Len() int { return len(d.m) }

// Revert drops every draft.
func (d *Drafts) Revert() { clear(d.m) }

// Refs returns the cells that have a draft, ordered by row and then column.
func (d *Drafts) Refs() []CellRef {
	out := make([]CellRef, 0, len(d.m))
	for ref := range d.m {
		out = append(out, ref)
	}
	slices.SortFunc(out, func(a, b CellRef) int {
		if c := cmp.Compare(a.RowID, b.RowID); c != 0 {
			return c
		}
		return cmp.Compare(a.Col, b.Col)
	})
	return out
}

// HasRow reports whether any cell of the row has a draft.
func (d *Drafts) HasRow(rowID int64) bool {
	for ref := range d.m {
		if ref.RowID == rowID {
			return true
		}
	}
	return false
}
