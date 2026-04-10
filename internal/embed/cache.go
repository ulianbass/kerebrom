package embed

import "sync"

// LRUCache caches embeddings by content hash to avoid recomputing
// cosine similarity from BLOB deserialization on every recall.
type LRUCache struct {
	mu       sync.RWMutex
	capacity int
	entries  map[string][]float32
	order    []string // oldest first
}

// NewLRUCache creates a cache with the given capacity.
func NewLRUCache(capacity int) *LRUCache {
	if capacity <= 0 {
		capacity = 2048
	}
	return &LRUCache{
		capacity: capacity,
		entries:  make(map[string][]float32, capacity),
	}
}

// Get retrieves a cached embedding by content hash.
func (c *LRUCache) Get(hash string) ([]float32, bool) {
	c.mu.RLock()
	v, ok := c.entries[hash]
	c.mu.RUnlock()
	return v, ok
}

// Put stores an embedding. Evicts oldest entry if at capacity.
func (c *LRUCache) Put(hash string, vec []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.entries[hash]; ok {
		return // already cached
	}
	if len(c.entries) >= c.capacity && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[hash] = vec
	c.order = append(c.order, hash)
}
