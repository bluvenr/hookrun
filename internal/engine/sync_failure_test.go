package engine

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
)

// Regression for D-03a: the synchronous path used to discard the failed
// action result and always answer {code:200, message:"ok"}, hiding command
// failures and timeouts from the caller. A failed sync action must surface
// a non-200 code naming the failure, mirroring the async path's honesty.

func syncTestConfig(cmd string, timeoutSec int) *config.RuleConfig {
	return &config.RuleConfig{
		Name: "sync-fail-test",
		Rules: []config.Rule{{
			Name: "r1",
			Actions: []config.Action{{
				Type:    "command",
				Cmd:     cmd,
				Timeout: timeoutSec,
			}},
		}},
	}
}

func TestProcessConfig_SyncFailureSurfaced(t *testing.T) {
	e := newTestEngine(t)
	cfg := syncTestConfig("exit 7", 0)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}

	responses := e.processConfig(cfg, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %+v", responses)
	}
	resp := responses[0]
	if resp.Code == 200 {
		t.Fatalf("failed sync action must not answer 200 ok, got %+v", resp)
	}
	if resp.Code < 400 {
		t.Errorf("expected an error code (>=400), got %d", resp.Code)
	}
	if !strings.Contains(resp.Message, "7") {
		t.Errorf("message should name the failing exit code, got %q", resp.Message)
	}
	if resp.Config != "sync-fail-test" || resp.Rule != "r1" {
		t.Errorf("response should identify config/rule, got %+v", resp)
	}
	// Task must be released so future requests are not permanently blocked
	if e.running["sync-fail-test/r1"] {
		t.Error("failed request must not leave the task marked running")
	}
}

func TestProcessConfig_SyncTimeoutSurfaced(t *testing.T) {
	e := newTestEngine(t)
	cmd := "sleep 5"
	if runtime.GOOS == "windows" {
		cmd = "ping -n 6 127.0.0.1"
	}
	cfg := syncTestConfig(cmd, 1)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}

	start := time.Now()
	responses := e.processConfig(cfg, req)
	elapsed := time.Since(start)

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %+v", responses)
	}
	resp := responses[0]
	if resp.Code == 200 {
		t.Fatalf("timed-out sync action must not answer 200 ok, got %+v", resp)
	}
	if !strings.Contains(strings.ToLower(resp.Message), "tim") {
		t.Errorf("message should mention the timeout, got %q", resp.Message)
	}
	// D-03b: the response must be bounded by the action timeout, not by the
	// command's own lifetime (orphaned grandchildren holding the stdout pipe
	// used to stall cmd.Wait on fork-type shells).
	if elapsed > 4*time.Second {
		t.Errorf("timeout=1s but response took %v — timeout does not bound the response", elapsed)
	}
}

func TestProcessConfig_SyncSuccessUnchanged(t *testing.T) {
	e := newTestEngine(t)
	cfg := syncTestConfig("echo sync-ok", 0)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}

	responses := e.processConfig(cfg, req)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %+v", responses)
	}
	resp := responses[0]
	if resp.Code != 200 || resp.Message != "ok" {
		t.Errorf("successful sync action must keep 200 ok, got %+v", resp)
	}
	if resp.Actions != 1 {
		t.Errorf("expected actions=1, got %d", resp.Actions)
	}
}
