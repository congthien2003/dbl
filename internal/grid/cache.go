package grid

// Cache is a small least-recently-used cache. It holds at most Cap
// entries; adding one more drops the one used longest ago. It is not safe
// for concurrent use: the application keeps it on the UI thread.
type Cache[V any] struct {
	cap   int
	keys  []string // least recently used first
	items map[string]V
}

// NewCache returns a cache of at most capacity entries (at least one).
func NewCache[V any](capacity int) *Cache[V] {
	return &Cache[V]{cap: max(1, capacity), items: map[string]V{}}
}

// Get returns the entry for key and marks it as the most recently used.
func (c *Cache[V]) Get(key string) (V, bool) {
	v, ok := c.items[key]
	if ok {
		c.touch(key)
	}
	return v, ok
}

// Put stores v under key and returns the keys it evicted.
func (c *Cache[V]) Put(key string, v V) (evicted []string) {
	if _, ok := c.items[key]; ok {
		c.items[key] = v
		c.touch(key)
		return nil
	}
	c.items[key] = v
	c.keys = append(c.keys, key)
	for len(c.keys) > c.cap {
		old := c.keys[0]
		c.keys = c.keys[1:]
		delete(c.items, old)
		evicted = append(evicted, old)
	}
	return evicted
}

// Clear drops every entry.
func (c *Cache[V]) Clear() {
	clear(c.items)
	c.keys = nil
}

// Len returns the number of entries.
func (c *Cache[V]) Len() int { return len(c.keys) }

func (c *Cache[V]) touch(key string) {
	for i, k := range c.keys {
		if k == key {
			c.keys = append(append(c.keys[:i:i], c.keys[i+1:]...), key)
			return
		}
	}
}
