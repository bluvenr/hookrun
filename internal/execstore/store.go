// Package execstore keeps an in-memory ring buffer of asynchronous action
// execution records, queryable via the /api/executions endpoint.
package execstore

import (
	"sync"
	"time"
)

// Record statuses.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// DefaultCapacity is the fixed number of records kept in memory.
const DefaultCapacity = 100

// Record describes one asynchronous execution.
type Record struct {
	RequestID  string    `json:"request_id"`
	Config     string    `json:"config"`
	Rule       string    `json:"rule"`
	Status     string    `json:"status"` // running | succeeded | failed
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"` // zero while running
	Duration   string    `json:"duration,omitempty"`
	// ExitCode of the command/script; -1 for failures without an exit code
	// (webhook errors, timeouts, panics).
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// Store is a mutex-guarded ring buffer of execution records.
// The zero value is not usable; create one with NewStore.
type Store struct {
	mu       sync.Mutex
	records  []Record
	capacity int
	next     int  // next write position
	wrapped  bool // true once the buffer has wrapped at least once
	// index maps request_id -> slot for O(1) completion updates
	index map[string]int
}

// NewStore creates a Store holding at most capacity records.
// Non-positive capacities fall back to DefaultCapacity.
func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Store{
		records:  make([]Record, capacity),
		capacity: capacity,
		index:    make(map[string]int, capacity),
	}
}

// Add inserts a new record (status running) and returns its slot.
// When the buffer is full the oldest record is evicted.
func (s *Store) Add(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slot := s.next
	// Evict the record currently occupying this slot
	if old := s.records[slot]; old.RequestID != "" {
		delete(s.index, old.RequestID)
	}

	s.records[slot] = r
	s.index[r.RequestID] = slot
	s.next = (s.next + 1) % s.capacity
	if s.next == 0 {
		s.wrapped = true
	}
}

// Complete finalizes the record identified by requestID. When the record has
// already been evicted from the ring buffer the call is a silent no-op.
func (s *Store) Complete(requestID string, status string, exitCode int, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slot, ok := s.index[requestID]
	if !ok {
		return
	}
	r := &s.records[slot]
	r.Status = status
	r.FinishedAt = time.Now()
	r.Duration = r.FinishedAt.Sub(r.StartedAt).Round(time.Millisecond).String()
	r.ExitCode = exitCode
	r.Error = errMsg
}

// List returns up to limit records, newest first.
func (s *Store) List(limit int) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := s.capacity
	if !s.wrapped {
		count = s.next
	}
	if limit <= 0 || limit > count {
		limit = count
	}

	out := make([]Record, 0, limit)
	// Walk backwards from the most recently written slot
	for i := 0; i < limit; i++ {
		slot := (s.next - 1 - i + s.capacity*2) % s.capacity
		if s.records[slot].RequestID == "" {
			break
		}
		out = append(out, s.records[slot])
	}
	return out
}
