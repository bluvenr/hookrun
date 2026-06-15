package engine

import (
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
)

// --- RetryConfig Validate ---

func TestRetryConfig_Validate_Valid(t *testing.T) {
	a := config.Action{
		Type: "command",
		Cmd:  "echo ok",
		Retry: &config.RetryConfig{
			MaxAttempts:     3,
			IntervalSeconds: 5,
		},
	}
	if err := a.Validate("test", 0); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestRetryConfig_Validate_MaxAttemptsZero(t *testing.T) {
	a := config.Action{
		Type: "command",
		Cmd:  "echo ok",
		Retry: &config.RetryConfig{
			MaxAttempts:     0,
			IntervalSeconds: 5,
		},
	}
	if err := a.Validate("test", 0); err == nil {
		t.Error("expected error for max_attempts=0")
	}
}

func TestRetryConfig_Validate_MaxAttemptsNegative(t *testing.T) {
	a := config.Action{
		Type: "command",
		Cmd:  "echo ok",
		Retry: &config.RetryConfig{
			MaxAttempts:     -1,
			IntervalSeconds: 5,
		},
	}
	if err := a.Validate("test", 0); err == nil {
		t.Error("expected error for max_attempts=-1")
	}
}

func TestRetryConfig_Validate_NegativeInterval(t *testing.T) {
	a := config.Action{
		Type: "command",
		Cmd:  "echo ok",
		Retry: &config.RetryConfig{
			MaxAttempts:     3,
			IntervalSeconds: -1,
		},
	}
	if err := a.Validate("test", 0); err == nil {
		t.Error("expected error for interval_seconds=-1")
	}
}

func TestRetryConfig_Validate_ZeroInterval(t *testing.T) {
	a := config.Action{
		Type: "command",
		Cmd:  "echo ok",
		Retry: &config.RetryConfig{
			MaxAttempts:     2,
			IntervalSeconds: 0,
		},
	}
	if err := a.Validate("test", 0); err != nil {
		t.Errorf("expected no error for interval_seconds=0, got: %v", err)
	}
}

func TestRetryConfig_Validate_NilRetry(t *testing.T) {
	a := config.Action{
		Type:  "command",
		Cmd:   "echo ok",
		Retry: nil,
	}
	if err := a.Validate("test", 0); err != nil {
		t.Errorf("expected no error for nil retry, got: %v", err)
	}
}

// --- calcRetryInterval ---

func TestCalcRetryInterval_ZeroBase(t *testing.T) {
	d := calcRetryInterval(1, 0)
	if d != 0 {
		t.Errorf("expected 0 for base=0, got %v", d)
	}
}

func TestCalcRetryInterval_NegativeBase(t *testing.T) {
	d := calcRetryInterval(1, -5)
	if d != 0 {
		t.Errorf("expected 0 for negative base, got %v", d)
	}
}

func TestCalcRetryInterval_ExponentialGrowth(t *testing.T) {
	base := 5
	// Run multiple times to account for jitter
	for attempt := 1; attempt <= 4; attempt++ {
		expectedBase := base * (1 << (attempt - 1))
		// Run 20 samples per attempt
		for s := 0; s < 20; s++ {
			d := calcRetryInterval(attempt, base)
			secs := d.Seconds()
			// Verify within ±30% of expected base (generous margin for jitter)
			low := float64(expectedBase) * 0.5
			high := float64(expectedBase) * 1.5
			if secs < low || secs > high {
				t.Errorf("attempt %d sample %d: expected [%.1f, %.1f], got %.3f", attempt, s, low, high, secs)
			}
		}
	}
}

func TestCalcRetryInterval_CapAt300(t *testing.T) {
	// attempt=10, base=5 → 5 * 2^9 = 2560, should be capped at 300
	for i := 0; i < 20; i++ {
		d := calcRetryInterval(10, 5)
		secs := d.Seconds()
		// With jitter: [300*0.75, 300*1.25] = [225, 375]
		if secs < 225 || secs > 375 {
			t.Errorf("expected capped to ~300s (±25%%), got %f", secs)
		}
	}
}

func TestCalcRetryInterval_JitterRange(t *testing.T) {
	// Run many times and verify results vary (jitter is applied)
	base := 10
	min, max := 999.0, 0.0
	for i := 0; i < 100; i++ {
		d := calcRetryInterval(1, base)
		secs := d.Seconds()
		if secs < min {
			min = secs
		}
		if secs > max {
			max = secs
		}
	}
	// For attempt=1, base=10: all values should be in [7, 13] (generous bounds)
	if min < 7.0 || max > 13.0 {
		t.Errorf("values out of range: min=%f max=%f (expected [7, 13])", min, max)
	}
	// Verify jitter is actually applied (not always the same value)
	if max-min < 0.1 {
		t.Errorf("jitter seems absent: min=%f max=%f", min, max)
	}
}

// --- executeActions with retry ---

func TestExecuteActions_RetrySuccess(t *testing.T) {
	e := newTestEngine(t)
	log := e.logger
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{},
	}

	// "echo ok" always succeeds — with retry configured, should complete on first attempt
	actions := []config.Action{
		{
			Type: "command",
			Cmd:  "echo ok",
			Retry: &config.RetryConfig{
				MaxAttempts:     3,
				IntervalSeconds: 0,
			},
		},
	}
	completed := e.executeActions("test/retry-success", actions, req, log)
	if completed != 1 {
		t.Errorf("expected 1 completed, got %d", completed)
	}
}

func TestExecuteActions_RetryExhausted(t *testing.T) {
	e := newTestEngine(t)
	log := e.logger
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{},
	}

	// "exit 1" always fails — retry should exhaust all attempts
	actions := []config.Action{
		{
			Type: "command",
			Cmd:  "exit 1",
			Retry: &config.RetryConfig{
				MaxAttempts:     3,
				IntervalSeconds: 0,
			},
		},
	}
	completed := e.executeActions("test/retry-exhausted", actions, req, log)
	if completed != 0 {
		t.Errorf("expected 0 completed, got %d", completed)
	}
}

func TestExecuteActions_RetryExhausted_ContinueOnError(t *testing.T) {
	e := newTestEngine(t)
	log := e.logger
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{},
	}

	// First action fails with retry, second succeeds (continue_on_error=true)
	actions := []config.Action{
		{
			Type:            "command",
			Cmd:             "exit 1",
			ContinueOnError: true,
			Retry: &config.RetryConfig{
				MaxAttempts:     2,
				IntervalSeconds: 0,
			},
		},
		{
			Type: "command",
			Cmd:  "echo ok",
		},
	}
	completed := e.executeActions("test/retry-continue", actions, req, log)
	// First action fails (0), second succeeds (1)
	if completed != 1 {
		t.Errorf("expected 1 completed, got %d", completed)
	}
}

func TestExecuteActions_NoRetry_Unchanged(t *testing.T) {
	e := newTestEngine(t)
	log := e.logger
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{},
	}

	// No retry config — should behave exactly as before
	actions := []config.Action{
		{
			Type: "command",
			Cmd:  "exit 1",
		},
	}
	completed := e.executeActions("test/no-retry", actions, req, log)
	if completed != 0 {
		t.Errorf("expected 0 completed, got %d", completed)
	}
}

func TestExecuteActions_RetryZeroInterval(t *testing.T) {
	e := newTestEngine(t)
	log := e.logger
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{},
	}

	// interval_seconds=0 — immediate retry, no wait
	start := time.Now()
	actions := []config.Action{
		{
			Type: "command",
			Cmd:  "exit 1",
			Retry: &config.RetryConfig{
				MaxAttempts:     3,
				IntervalSeconds: 0,
			},
		},
	}
	e.executeActions("test/retry-zero-interval", actions, req, log)
	elapsed := time.Since(start)

	// With 3 attempts and 0 interval, should complete in under 5 seconds
	if elapsed > 5*time.Second {
		t.Errorf("expected fast retry with 0 interval, took %v", elapsed)
	}
}

func TestExecuteActions_RetryMultipleActions(t *testing.T) {
	e := newTestEngine(t)
	log := e.logger
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{},
	}

	// Two actions: first succeeds, second fails with retry
	actions := []config.Action{
		{
			Type: "command",
			Cmd:  "echo first",
			Retry: &config.RetryConfig{
				MaxAttempts:     2,
				IntervalSeconds: 0,
			},
		},
		{
			Type:            "command",
			Cmd:             "exit 1",
			ContinueOnError: true,
			Retry: &config.RetryConfig{
				MaxAttempts:     2,
				IntervalSeconds: 0,
			},
		},
	}
	completed := e.executeActions("test/retry-multi", actions, req, log)
	// First succeeds (1), second fails after retries (0)
	if completed != 1 {
		t.Errorf("expected 1 completed, got %d", completed)
	}
}
