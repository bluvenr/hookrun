package execstore

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewStore_DefaultCapacity(t *testing.T) {
	s := NewStore(0)
	if s.capacity != DefaultCapacity {
		t.Errorf("expected default capacity %d, got %d", DefaultCapacity, s.capacity)
	}
}

func TestAddAndList(t *testing.T) {
	s := NewStore(10)
	for i := 0; i < 3; i++ {
		s.Add(Record{RequestID: fmt.Sprintf("req-%d", i), Config: "cfg", Rule: "rule", Status: StatusRunning, StartedAt: time.Now()})
	}
	list := s.List(10)
	if len(list) != 3 {
		t.Fatalf("expected 3 records, got %d", len(list))
	}
	// Newest first
	if list[0].RequestID != "req-2" || list[2].RequestID != "req-0" {
		t.Errorf("unexpected order: %v / %v", list[0].RequestID, list[2].RequestID)
	}
}

func TestList_LimitClamping(t *testing.T) {
	s := NewStore(10)
	for i := 0; i < 5; i++ {
		s.Add(Record{RequestID: fmt.Sprintf("req-%d", i), Status: StatusRunning, StartedAt: time.Now()})
	}
	if got := len(s.List(2)); got != 2 {
		t.Errorf("limit=2: expected 2, got %d", got)
	}
	if got := len(s.List(0)); got != 5 {
		t.Errorf("limit=0: expected all 5, got %d", got)
	}
	if got := len(s.List(100)); got != 5 {
		t.Errorf("limit=100: expected all 5, got %d", got)
	}
}

func TestRingEviction(t *testing.T) {
	s := NewStore(3)
	for i := 0; i < 5; i++ {
		s.Add(Record{RequestID: fmt.Sprintf("req-%d", i), Status: StatusRunning, StartedAt: time.Now()})
	}
	list := s.List(10)
	if len(list) != 3 {
		t.Fatalf("expected 3 records after eviction, got %d", len(list))
	}
	// Only the 3 newest survive, newest first
	want := []string{"req-4", "req-3", "req-2"}
	for i, w := range want {
		if list[i].RequestID != w {
			t.Errorf("slot %d: expected %s, got %s", i, w, list[i].RequestID)
		}
	}
}

func TestComplete(t *testing.T) {
	s := NewStore(10)
	s.Add(Record{RequestID: "req-x", Config: "cfg", Rule: "r", Status: StatusRunning, StartedAt: time.Now()})
	s.Complete("req-x", StatusFailed, 42, "boom")

	list := s.List(10)
	if len(list) != 1 {
		t.Fatalf("expected 1 record, got %d", len(list))
	}
	r := list[0]
	if r.Status != StatusFailed {
		t.Errorf("expected status failed, got %s", r.Status)
	}
	if r.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", r.ExitCode)
	}
	if r.Error != "boom" {
		t.Errorf("expected error 'boom', got %q", r.Error)
	}
	if r.FinishedAt.IsZero() {
		t.Error("finished_at should be set")
	}
	if r.Duration == "" {
		t.Error("duration should be set")
	}
}

func TestComplete_EvictedIsNoop(t *testing.T) {
	s := NewStore(2)
	s.Add(Record{RequestID: "req-0", Status: StatusRunning, StartedAt: time.Now()})
	s.Add(Record{RequestID: "req-1", Status: StatusRunning, StartedAt: time.Now()})
	s.Add(Record{RequestID: "req-2", Status: StatusRunning, StartedAt: time.Now()}) // evicts req-0

	// Must not panic and must not touch the wrong record
	s.Complete("req-0", StatusFailed, 1, "should not apply")

	list := s.List(10)
	for _, r := range list {
		if r.RequestID == "req-0" {
			t.Error("evicted record should not be present")
		}
		if r.Status == StatusFailed {
			t.Errorf("no record should be failed, got %s (%s)", r.RequestID, r.Status)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore(50)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				id := fmt.Sprintf("req-%d-%d", base, j)
				s.Add(Record{RequestID: id, Status: StatusRunning, StartedAt: time.Now()})
				s.Complete(id, StatusSucceeded, 0, "")
				_ = s.List(10)
			}
		}(i)
	}
	wg.Wait()

	list := s.List(100)
	if len(list) != 50 {
		t.Errorf("expected 50 records in ring, got %d", len(list))
	}
}
