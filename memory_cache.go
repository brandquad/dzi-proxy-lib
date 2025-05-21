package dziproxylib

import (
	"container/list"
	"sync"
)

// cacheEntry stores the key, value, and size of a cached item.
type cacheEntry struct {
	key       string
	value     []byte
	sizeBytes int64
}

// InMemoryCache is an in-memory LRU cache for file contents.
// It has capacity limits based on the number of items and total size in bytes.
type InMemoryCache struct {
	mu            sync.RWMutex
	items         map[string]*list.Element // Maps key to *list.Element containing a cacheEntry
	lru           *list.List               // Doubly linked list to track LRU order; elements store cacheEntry
	capacityItems int                      // Max number of items
	currentItems  int                      // Current number of items
	capacityBytes int64                    // Max total size of all items in bytes
	currentBytes  int64                    // Current total size of all items in bytes
}

// NewInMemoryCache creates a new InMemoryCache.
// capacityItems and capacityBytes must be positive.
func NewInMemoryCache(capacityItems int, capacityBytes int64) *InMemoryCache {
	if capacityItems <= 0 {
		capacityItems = 1 // Default to at least 1 item if invalid
	}
	if capacityBytes <= 0 {
		capacityBytes = 1 // Default to at least 1 byte if invalid
	}
	return &InMemoryCache{
		items:         make(map[string]*list.Element),
		lru:           list.New(),
		capacityItems: capacityItems,
		capacityBytes: capacityBytes,
	}
}

// Get retrieves a value from the cache.
// It returns the value and true if the key was found, otherwise nil and false.
// Accessing an item moves it to the front of the LRU list.
func (c *InMemoryCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if elem, found := c.items[key]; found {
		c.lru.MoveToFront(elem)
		// The element's value is of type interface{}, so we need to type assert it.
		// Since we store *cacheEntry in the list, we assert to that.
		entry := elem.Value.(*cacheEntry)
		return entry.value, true
	}
	return nil, false
}

// Put adds a value to the cache.
// If the key already exists, its value is updated, and it's moved to the front.
// If adding the item exceeds capacity, LRU items are evicted.
// If the item itself is larger than capacityBytes, it's not cached.
func (c *InMemoryCache) Put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entrySizeBytes := int64(len(value))

	// If the single item is larger than the total cache capacity, don't cache it.
	if entrySizeBytes > c.capacityBytes {
		// Optionally log this decision:
		// log.Printf("[W] Item with key %s and size %d bytes is larger than total cache capacity %d bytes. Not caching.", key, entrySizeBytes, c.capacityBytes)
		return
	}

	if elem, found := c.items[key]; found {
		// Update existing entry
		entry := elem.Value.(*cacheEntry)
		c.currentBytes -= entry.sizeBytes // Subtract old size
		c.currentBytes += entrySizeBytes  // Add new size
		entry.value = value
		entry.sizeBytes = entrySizeBytes
		c.lru.MoveToFront(elem)
	} else {
		// New entry
		entry := &cacheEntry{
			key:       key,
			value:     value,
			sizeBytes: entrySizeBytes,
		}
		elem := c.lru.PushFront(entry)
		c.items[key] = elem
		c.currentItems++
		c.currentBytes += entrySizeBytes
	}

	// Eviction loop
	for c.currentItems > c.capacityItems || c.currentBytes > c.capacityBytes {
		if elem := c.lru.Back(); elem != nil {
			entry := elem.Value.(*cacheEntry)
			delete(c.items, entry.key)
			c.lru.Remove(elem)
			c.currentItems--
			c.currentBytes -= entry.sizeBytes
		} else {
			// Should not happen if currentItems > 0 or currentBytes > 0
			break
		}
	}
}
