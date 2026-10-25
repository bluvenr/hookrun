package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/engine"
	"github.com/bluvenr/hookrun/internal/execstore"
	"github.com/bluvenr/hookrun/internal/logger"
)

// newTestServer creates a server backed by a real engine with a temp logger.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	log := logger.New(logger.Options{Mode: "single", Path: dir, Console: false})
	eng := engine.New(nil, log, "single", 30, 0, 4)
	t.Cleanup(func() {
		eng.Stop()
		log.Close()
	})
	return New(nil, eng, log)
}

func TestHandleExecutions_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/executions", nil)
	rec := httptest.NewRecorder()
	s.handleExecutions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Executions []execstore.Record `json:"executions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Executions) != 0 {
		t.Errorf("expected empty list, got %d records", len(body.Executions))
	}
}

func TestHandleExecutions_ListAndLimit(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 5; i++ {
		s.engine.ExecStore().Add(execstore.Record{
			RequestID: "req-" + string(rune('a'+i)),
			Config:    "cfg",
			Rule:      "r1",
			Status:    execstore.StatusSucceeded,
			StartedAt: time.Now(),
		})
	}

	// Default limit covers all 5
	rec := httptest.NewRecorder()
	s.handleExecutions(rec, httptest.NewRequest(http.MethodGet, "/api/executions", nil))
	var body struct {
		Executions []execstore.Record `json:"executions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Executions) != 5 {
		t.Errorf("expected 5 records, got %d", len(body.Executions))
	}
	// Newest first
	if body.Executions[0].RequestID != "req-e" {
		t.Errorf("expected newest record first, got %s", body.Executions[0].RequestID)
	}

	// Explicit limit
	rec = httptest.NewRecorder()
	s.handleExecutions(rec, httptest.NewRequest(http.MethodGet, "/api/executions?limit=2", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Executions) != 2 {
		t.Errorf("limit=2: expected 2 records, got %d", len(body.Executions))
	}

	// Invalid limit falls back to default
	rec = httptest.NewRecorder()
	s.handleExecutions(rec, httptest.NewRequest(http.MethodGet, "/api/executions?limit=abc", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Executions) != 5 {
		t.Errorf("invalid limit: expected all 5 records, got %d", len(body.Executions))
	}
}

func TestHandleExecutions_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleExecutions(rec, httptest.NewRequest(http.MethodPost, "/api/executions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// newWebhookTestServer builds a full server backed by real config files on
// disk so handleWebhook can run end-to-end (manager + engine + routes).
func newWebhookTestServer(t *testing.T, ruleYAML string) (*Server, *engine.Engine) {
	t.Helper()
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalYAML := "server:\n  port: 9000\n  route: \"/webhook\"\nlog:\n  mode: \"single\"\n  path: " +
		filepath.Join(dir, "logs") + "\nconfig_dir: " + hooksDir + "\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "deploy.yaml"), []byte(ruleYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := config.NewManager(cfgPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	log := logger.New(logger.Options{Mode: "single", Path: dir, Console: false})
	eng := engine.New(mgr.Rules(), log, "single", 30, 0, 4)
	t.Cleanup(func() {
		eng.WaitForAsync(5 * time.Second)
		eng.Stop()
		log.Close()
	})
	return New(mgr, eng, log), eng
}

func TestHandleWebhook_AsyncReturnsHTTP202(t *testing.T) {
	ruleYAML := `name: "deploy"
async: true
rules:
  - name: "r1"
    actions:
      - type: "command"
        cmd: "echo async-http-test"
`
	s, eng := newWebhookTestServer(t, ruleYAML)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/deploy", strings.NewReader(`{}`))
	s.handleWebhook(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("expected HTTP 202 for async config, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body engine.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Code != 202 {
		t.Errorf("expected body code 202, got %d", body.Code)
	}
	if body.RequestID == "" {
		t.Error("202 response must carry request_id")
	}
	eng.WaitForAsync(5 * time.Second)
}

func TestHandleWebhook_SyncStillReturnsHTTP200(t *testing.T) {
	ruleYAML := `name: "deploy"
rules:
  - name: "r1"
    actions:
      - type: "command"
        cmd: "echo sync-http-test"
`
	s, _ := newWebhookTestServer(t, ruleYAML)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/deploy", strings.NewReader(`{}`))
	s.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected HTTP 200 for sync config, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
