package engine

import (
	"sync"
	"time"
)

// dedupCache tracks recently seen request IDs to prevent duplicate webhook execution.
type dedupCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // requestID -> expiry time
}

// newDedupCache creates a new empty dedup cache.
func newDedupCache() *dedupCache {
	return &dedupCache{
		entries: make(map[string]time.Time),
	}
}

// IsDuplicate checks whether the given request ID has been seen within the window.
// If not seen, it records the ID and returns false.
// If seen within the window, it returns true (duplicate).
func (c *dedupCache) IsDuplicate(requestID string, window time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if expiry, exists := c.entries[requestID]; exists {
		if time.Now().Before(expiry) {
			return true
		}
	}

	c.entries[requestID] = time.Now().Add(window)
	return false
}

// Cleanup removes expired entries from the cache.
func (c *dedupCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for id, expiry := range c.entries {
		if now.After(expiry) {
			delete(c.entries, id)
		}
	}
}

// startCleanupLoop runs a background goroutine that periodically cleans expired entries.
func (c *dedupCache) startCleanupLoop(interval time.Duration, stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.Cleanup()
			case <-stop:
				return
			}
		}
	}()
}
