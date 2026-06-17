package engine

import (
	"errors"
	"sync"
	"time"
)

// ErrRegistryFull is returned when the registry has reached its capacity.
var ErrRegistryFull = errors.New("relay registry is full")

// RegistryEntry represents a single registered relay target.
type RegistryEntry struct {
	URL      string
	Token    string
	Tags     []string
	TTL      int // negotiated TTL in seconds
	LastSeen time.Time
}

// TargetRegistry is an in-memory pool of dynamically registered relay targets.
type TargetRegistry struct {
	mu         sync.RWMutex
	entries    map[string]*RegistryEntry // key = URL
	maxEntries int
	maxTTL     int // max TTL cap in seconds, 0 = unlimited
}

// NewTargetRegistry creates a new registry with the given capacity and TTL limits.
func NewTargetRegistry(maxEntries, maxTTL int) *TargetRegistry {
	return &TargetRegistry{
		entries:    make(map[string]*RegistryEntry),
		maxEntries: maxEntries,
		maxTTL:     maxTTL,
	}
}

// Register adds or refreshes a target in the registry.
// Returns ErrRegistryFull if capacity is reached and the URL is not already registered.
func (r *TargetRegistry) Register(entry RegistryEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if URL already exists (refresh case)
	if existing, ok := r.entries[entry.URL]; ok {
		existing.Token = entry.Token
		existing.Tags = entry.Tags
		existing.TTL = r.negotiateTTL(entry.TTL)
		existing.LastSeen = time.Now()
		return nil
	}

	// New entry — check capacity
	if r.maxEntries > 0 && len(r.entries) >= r.maxEntries {
		return ErrRegistryFull
	}

	entry.TTL = r.negotiateTTL(entry.TTL)
	entry.LastSeen = time.Now()
	r.entries[entry.URL] = &entry
	return nil
}

// negotiateTTL applies the maxTTL cap. Returns the effective TTL.
func (r *TargetRegistry) negotiateTTL(ttl int) int {
	if r.maxTTL > 0 && (ttl == 0 || ttl > r.maxTTL) {
		return r.maxTTL
	}
	return ttl
}

// Unregister removes a target from the registry by URL.
func (r *TargetRegistry) Unregister(url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, url)
}

// FindByTag returns all entries whose tags contain the given tag.
func (r *TargetRegistry) FindByTag(tag string) []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []RegistryEntry
	for _, entry := range r.entries {
		for _, t := range entry.Tags {
			if t == tag {
				result = append(result, *entry)
				break
			}
		}
	}
	return result
}

// List returns a snapshot of all registered entries.
func (r *TargetRegistry) List() []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]RegistryEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		result = append(result, *entry)
	}
	return result
}

// Count returns the number of registered entries.
func (r *TargetRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

// Cleanup removes expired entries (ttl > 0 && now - lastSeen > ttl).
func (r *TargetRegistry) Cleanup() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	removed := 0
	for url, entry := range r.entries {
		if entry.TTL > 0 && now.Sub(entry.LastSeen) > time.Duration(entry.TTL)*time.Second {
			delete(r.entries, url)
			removed++
		}
	}
	return removed
}

// StartCleanupLoop runs a background goroutine that periodically cleans up expired entries.
func (r *TargetRegistry) StartCleanupLoop(interval time.Duration, stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if removed := r.Cleanup(); removed > 0 {
					// cleanup happened — caller can log if needed
				}
			case <-stop:
				return
			}
		}
	}()
}
