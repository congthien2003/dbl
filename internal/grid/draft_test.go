package grid

import "testing"

func TestDraftsRevertToOriginalClearsDirty(t *testing.T) {
	var d Drafts
	ref := CellRef{RowID: 7, Col: 2}
	orig := Value{Kind: KindText, Text: "old"}
	d.Set(ref, orig, Value{Kind: KindText, Text: "new"})
	if !d.IsDirty(ref) || d.Len() != 1 {
		t.Fatalf("after edit: dirty=%v len=%d", d.IsDirty(ref), d.Len())
	}
	if got, ok := d.Get(ref); !ok || got.Text != "new" {
		t.Errorf("Get = %+v, %v", got, ok)
	}
	d.Set(ref, orig, orig)
	if d.IsDirty(ref) || d.Len() != 0 {
		t.Errorf("after editing back: dirty=%v len=%d", d.IsDirty(ref), d.Len())
	}
}

func TestDraftsIdentityIsRowIDNotDisplay(t *testing.T) {
	var d Drafts
	a := CellRef{RowID: 1, Col: 3}
	b := CellRef{RowID: 2, Col: 3}
	same := Value{Kind: KindText, Text: "same"}
	d.Set(a, same, Value{Kind: KindText, Text: "x"})
	if d.IsDirty(b) {
		t.Error("a draft on row 1 marked row 2 dirty")
	}
	d.Set(b, same, Value{Kind: KindText, Text: "y"})
	if d.Len() != 2 {
		t.Errorf("len = %d, want 2", d.Len())
	}
	d.Revert()
	if d.Len() != 0 || d.IsDirty(a) || d.IsDirty(b) {
		t.Errorf("Revert left %d drafts", d.Len())
	}
}

func TestZeroDraftsIsUsable(t *testing.T) {
	var d Drafts
	if d.Len() != 0 || d.IsDirty(CellRef{}) {
		t.Error("zero Drafts is not empty")
	}
	d.Revert() // must not panic
}
