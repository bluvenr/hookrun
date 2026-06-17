package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
)

// --- dedupCache basic behavior ---

func TestDedupCache_FirstRequest(t *testing.T) {
	c := newDedupCache()
	if c.IsDuplicate("req-1", 5*time.Minute) {
		t.Error("first request should not be duplicate")
	}
}

func TestDedupCache_DuplicateWithinWindow(t *testing.T) {
	c := newDedupCache()
	c.IsDuplicate("req-2", 5*time.Minute) // first
	if !c.IsDuplicate("req-2", 5*time.Minute) {
		t.Error("second request within window should be duplicate")
	}
}

func TestDedupCache_DifferentIDs(t *testing.T) {
	c := newDedupCache()
	c.IsDuplicate("req-a", 5*time.Minute)
	if c.IsDuplicate("req-b", 5*time.Minute) {
		t.Error("different request IDs should not be duplicates")
	}
}

func TestDedupCache_ExpiredWindow(t *testing.T) {
	c := newDedupCache()
	c.IsDuplicate("req-3", 50*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	if c.IsDuplicate("req-3", 50*time.Millisecond) {
		t.Error("request after window expiry should not be duplicate")
	}
}

func TestDedupCache_Cleanup(t *testing.T) {
	c := newDedupCache()
	c.IsDuplicate("old-1", 10*time.Millisecond)
	c.IsDuplicate("old-2", 10*time.Millisecond)
	c.IsDuplicate("fresh", 5*time.Minute)

	time.Sleep(20 * time.Millisecond)
	c.Cleanup()

	c.mu.Lock()
	count := len(c.entries)
	_, hasOld1 := c.entries["old-1"]
	_, hasOld2 := c.entries["old-2"]
	_, hasFresh := c.entries["fresh"]
	c.mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 entry after cleanup, got %d", count)
	}
	if hasOld1 || hasOld2 {
		t.Error("expired entries should be cleaned up")
	}
	if !hasFresh {
		t.Error("fresh entry should survive cleanup")
	}
}

func TestDedupCache_ConcurrentSafety(t *testing.T) {
	c := newDedupCache()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.IsDuplicate("concurrent-test", 5*time.Second)
		}(i)
	}
	wg.Wait()
	// No panic = success
}

// --- DeduplicateConfig.Validate ---

func TestDeduplicateConfigValidate_OK(t *testing.T) {
	d := config.DeduplicateConfig{Enabled: true, WindowSeconds: 300}
	if err := d.Validate("test"); err != nil {
		t.Fatalf("valid config should pass: %v", err)
	}
}

func TestDeduplicateConfigValidate_Disabled(t *testing.T) {
	d := config.DeduplicateConfig{Enabled: false, WindowSeconds: 0}
	if err := d.Validate("test"); err != nil {
		t.Fatalf("disabled config should pass regardless: %v", err)
	}
}

func TestDeduplicateConfigValidate_InvalidWindow(t *testing.T) {
	d := config.DeduplicateConfig{Enabled: true, WindowSeconds: 0}
	if err := d.Validate("test"); err == nil {
		t.Error("enabled with window=0 should fail")
	}
}

func TestDeduplicateConfigValidate_NegativeWindow(t *testing.T) {
	d := config.DeduplicateConfig{Enabled: true, WindowSeconds: -1}
	if err := d.Validate("test"); err == nil {
		t.Error("enabled with negative window should fail")
	}
}

// --- Integration: dedup in processConfig ---

func TestProcessConfig_DedupEnabled_SkipsDuplicate(t *testing.T) {
	e := newTestEngine(t)
	cfg := &config.RuleConfig{
		Name: "dedup-test",
		Deduplicate: &config.DeduplicateConfig{
			Enabled:       true,
			WindowSeconds: 300,
		},
		Rules: []config.Rule{
			{
				Name: "rule1",
				Actions: []config.Action{
					{Type: "command", Cmd: "echo hello"},
				},
			},
		},
	}
	e.UpdateConfigs([]*config.RuleConfig{cfg})

	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "dedup-req-1",
	}

	// First request: should execute
	resp1 := e.ProcessTargeted(cfg, req)
	if len(resp1) == 0 || resp1[0].Code != 200 {
		t.Fatalf("first request should succeed: %+v", resp1)
	}

	// Second request with same ID: should be deduplicated
	req2 := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "dedup-req-1",
	}
	resp2 := e.ProcessTargeted(cfg, req2)
	if len(resp2) == 0 {
		t.Fatal("second request should return a response")
	}
	if !containsString(resp2[0].Message, "Deduplicated") {
		t.Errorf("expected dedup message, got: %s", resp2[0].Message)
	}
}

func TestProcessConfig_DedupDisabled_AllowsDuplicate(t *testing.T) {
	e := newTestEngine(t)
	cfg := &config.RuleConfig{
		Name: "no-dedup-test",
		Rules: []config.Rule{
			{
				Name: "rule1",
				Actions: []config.Action{
					{Type: "command", Cmd: "echo hello"},
				},
			},
		},
	}
	e.UpdateConfigs([]*config.RuleConfig{cfg})

	req1 := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "no-dedup-req",
	}
	resp1 := e.ProcessTargeted(cfg, req1)
	if len(resp1) == 0 || resp1[0].Code != 200 {
		t.Fatalf("first request should succeed: %+v", resp1)
	}

	// Same request ID, but dedup is disabled
	req2 := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "no-dedup-req",
	}
	resp2 := e.ProcessTargeted(cfg, req2)
	if len(resp2) == 0 {
		t.Fatal("should get response")
	}
	if containsString(resp2[0].Message, "Deduplicated") {
		t.Error("without dedup config, should not be deduplicated")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
