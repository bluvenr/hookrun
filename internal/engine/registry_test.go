package engine

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// --- Register ---

func TestTargetRegistry_Register_FirstEntry(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	entry := RegistryEntry{URL: "http://10.0.0.1:9000/webhook", Token: "tok1", Tags: []string{"web"}}

	if err := reg.Register(entry); err != nil {
		t.Fatalf("first register should succeed: %v", err)
	}
	if reg.Count() != 1 {
		t.Errorf("expected count 1, got %d", reg.Count())
	}
}

func TestTargetRegistry_Register_RefreshExisting(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://10.0.0.1:9000/webhook", Token: "tok1", Tags: []string{"web"}, TTL: 60})

	// Register same URL with different token/tags
	time.Sleep(10 * time.Millisecond) // ensure lastSeen changes
	reg.Register(RegistryEntry{URL: "http://10.0.0.1:9000/webhook", Token: "tok2", Tags: []string{"api"}, TTL: 120})

	if reg.Count() != 1 {
		t.Errorf("refresh should not add new entry, got count %d", reg.Count())
	}

	entries := reg.List()
	if entries[0].Token != "tok2" {
		t.Errorf("token should be updated to tok2, got %s", entries[0].Token)
	}
	if len(entries[0].Tags) != 1 || entries[0].Tags[0] != "api" {
		t.Errorf("tags should be updated to [api], got %v", entries[0].Tags)
	}
}

func TestTargetRegistry_Register_Full(t *testing.T) {
	reg := NewTargetRegistry(2, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"t"}})

	err := reg.Register(RegistryEntry{URL: "http://c:9000/w", Tags: []string{"t"}})
	if !errors.Is(err, ErrRegistryFull) {
		t.Errorf("expected ErrRegistryFull, got %v", err)
	}
}

func TestTargetRegistry_Register_FullButRefresh(t *testing.T) {
	reg := NewTargetRegistry(2, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"t"}})

	// Refresh existing URL when full — should succeed
	err := reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t2"}})
	if err != nil {
		t.Errorf("refreshing existing entry should succeed even when full: %v", err)
	}
}

// --- Unregister ---

func TestTargetRegistry_Unregister(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"t"}})

	reg.Unregister("http://a:9000/w")
	if reg.Count() != 1 {
		t.Errorf("expected 1 entry after unregister, got %d", reg.Count())
	}

	// Unregister non-existent URL — should not panic
	reg.Unregister("http://nonexistent:9000/w")
	if reg.Count() != 1 {
		t.Errorf("unregister non-existent should not change count, got %d", reg.Count())
	}
}

// --- FindByTag ---

func TestTargetRegistry_FindByTag_Match(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"web", "prod"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"api", "prod"}})
	reg.Register(RegistryEntry{URL: "http://c:9000/w", Tags: []string{"web", "staging"}})

	results := reg.FindByTag("prod")
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'prod', got %d", len(results))
	}

	results = reg.FindByTag("web")
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'web', got %d", len(results))
	}

	results = reg.FindByTag("staging")
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'staging', got %d", len(results))
	}
}

func TestTargetRegistry_FindByTag_NoMatch(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"web"}})

	results := reg.FindByTag("nonexistent")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --- List ---

func TestTargetRegistry_List(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"t"}})

	list := reg.List()
	if len(list) != 2 {
		t.Errorf("expected 2 entries, got %d", len(list))
	}
}

func TestTargetRegistry_List_Empty(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	list := reg.List()
	if len(list) != 0 {
		t.Errorf("expected 0 entries, got %d", len(list))
	}
}

// --- TTL Negotiation ---

func TestTargetRegistry_TTL_Negotiation_Cap(t *testing.T) {
	reg := NewTargetRegistry(10, 300) // max TTL = 300s

	// TTL 600 > max 300 → capped to 300
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 600})
	entries := reg.List()
	if entries[0].TTL != 300 {
		t.Errorf("expected TTL capped at 300, got %d", entries[0].TTL)
	}
}

func TestTargetRegistry_TTL_Negotiation_ZeroToCap(t *testing.T) {
	reg := NewTargetRegistry(10, 300) // max TTL = 300s

	// TTL 0 (unlimited) → capped to 300
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 0})
	entries := reg.List()
	if entries[0].TTL != 300 {
		t.Errorf("expected TTL capped at 300 for ttl=0, got %d", entries[0].TTL)
	}
}

func TestTargetRegistry_TTL_Negotiation_NoCap(t *testing.T) {
	reg := NewTargetRegistry(10, 0) // no cap

	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 120})
	entries := reg.List()
	if entries[0].TTL != 120 {
		t.Errorf("expected TTL 120 with no cap, got %d", entries[0].TTL)
	}
}

func TestTargetRegistry_TTL_Negotiation_UnderCap(t *testing.T) {
	reg := NewTargetRegistry(10, 300)

	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 120})
	entries := reg.List()
	if entries[0].TTL != 120 {
		t.Errorf("expected TTL 120 (under cap), got %d", entries[0].TTL)
	}
}

// --- Cleanup ---

func TestTargetRegistry_Cleanup_Expired(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 1}) // 1 second TTL

	// Not expired yet
	removed := reg.Cleanup()
	if removed != 0 {
		t.Errorf("expected 0 removed (not expired), got %d", removed)
	}

	// Wait for expiry
	time.Sleep(1100 * time.Millisecond)
	removed = reg.Cleanup()
	if removed != 1 {
		t.Errorf("expected 1 removed (expired), got %d", removed)
	}
	if reg.Count() != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", reg.Count())
	}
}

func TestTargetRegistry_Cleanup_NoTTL(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 0}) // never expires

	time.Sleep(100 * time.Millisecond)
	removed := reg.Cleanup()
	if removed != 0 {
		t.Errorf("entries with TTL=0 should never expire, got %d removed", removed)
	}
}

// --- Concurrent Safety ---

func TestTargetRegistry_Concurrent(t *testing.T) {
	reg := NewTargetRegistry(1000, 0)
	var wg sync.WaitGroup

	// 50 goroutines register
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			url := "http://host" + string(rune('A'+n%26)) + ":9000/w"
			reg.Register(RegistryEntry{URL: url, Tags: []string{"t"}})
		}(i)
	}

	// 20 goroutines read
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.List()
			reg.FindByTag("t")
			reg.Count()
		}()
	}

	wg.Wait()

	// Should not panic and count should be reasonable
	if reg.Count() == 0 {
		t.Error("expected some entries after concurrent operations")
	}
}

// --- Count ---

func TestTargetRegistry_Count(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	if reg.Count() != 0 {
		t.Errorf("expected 0, got %d", reg.Count())
	}

	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"t"}})
	if reg.Count() != 2 {
		t.Errorf("expected 2, got %d", reg.Count())
	}
}

// --- StartCleanupLoop ---

func TestTargetRegistry_StartCleanupLoop(t *testing.T) {
	reg := NewTargetRegistry(10, 0)
	stop := make(chan struct{})

	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"t"}, TTL: 1})

	reg.StartCleanupLoop(500*time.Millisecond, stop)

	// Wait for expiry + cleanup
	time.Sleep(1600 * time.Millisecond)
	close(stop)

	if reg.Count() != 0 {
		t.Errorf("expected 0 entries after cleanup loop, got %d", reg.Count())
	}
}
