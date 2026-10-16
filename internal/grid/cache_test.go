package grid

import "testing"

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := NewCache[int](2)
	c.Put("a", 1)
	c.Put("b", 2)
	if _, ok := c.Get("a"); !ok { // a is now the most recent
		t.Fatal("a missing")
	}
	ev := c.Put("c", 3)
	if len(ev) != 1 || ev[0] != "b" {
		t.Errorf("evicted %v, want [b]", ev)
	}
	if _, ok := c.Get("b"); ok {
		t.Error("b should be gone")
	}
	if v, _ := c.Get("a"); v != 1 {
		t.Errorf("a = %d", v)
	}
	if c.Len() != 2 {
		t.Errorf("len %d", c.Len())
	}
}

func TestCachePutReplacesWithoutEviction(t *testing.T) {
	c := NewCache[string](2)
	c.Put("a", "x")
	c.Put("b", "y")
	if ev := c.Put("a", "z"); len(ev) != 0 {
		t.Errorf("evicted %v", ev)
	}
	if v, _ := c.Get("a"); v != "z" {
		t.Errorf("a = %q", v)
	}
}

func TestCacheClear(t *testing.T) {
	c := NewCache[int](3)
	c.Put("a", 1)
	c.Clear()
	if c.Len() != 0 {
		t.Errorf("len %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Error("a survived Clear")
	}
	if NewCache[int](0).cap != 1 {
		t.Error("capacity below 1 is not raised")
	}
}
