package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/engine"
	"github.com/bluvenr/hookrun/internal/logger"
)

// Regression for D-09: starting with a non-empty relay_registry_token mounts
// the registry routes; a hot-reload that clears the token must NOT turn those
// already-mounted endpoints unauthenticated. Empty token means "registry
// disabled", so every request must be denied after the reload.
func TestRegistryAuth_ReloadClearingTokenDeniesAccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGlobal := func(token string) string {
		yaml := "server:\n  port: 9000\n  route: \"/webhook\"\n  relay_registry_token: \"" + token + "\"\n" +
			"log:\n  mode: \"single\"\n  path: " + filepath.Join(dir, "logs") + "\n" +
			"config_dir: " + filepath.Join(dir, "hooks") + "\n"
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cfgPath := writeGlobal("tok-secret")
	mgr := config.NewManager(cfgPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	log := logger.New(logger.Options{Mode: "single", Path: dir, Console: false})
	eng := engine.New(mgr.Rules(), log, "single", 30, 0, 4)
	eng.SetRegistry(engine.NewTargetRegistry(20, 30))
	t.Cleanup(func() {
		eng.Stop()
		log.Close()
	})
	s := New(mgr, eng, log)

	registerBody := `{"url":"http://127.0.0.1:9/x","tags":["t"]}`

	// Control: with the token configured, correct Bearer auth succeeds.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/relay/register", strings.NewReader(registerBody))
	req.Header.Set("Authorization", "Bearer tok-secret")
	s.handleRelayRegister(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("control: expected 200 with valid token, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Simulate the hot-reload clearing the token (same path as /_reload:
	// config file rewritten, manager.Reload swaps the global config).
	writeGlobal("")
	if err := mgr.Reload(); err != nil {
		t.Fatalf("reload config: %v", err)
	}

	// Unauthenticated register must be denied (401), not silently accepted.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/relay/register", strings.NewReader(registerBody))
	s.handleRelayRegister(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("after token cleared: unauthenticated register expected 401, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Even a request carrying some Bearer value must be denied: there is no
	// valid token anymore, so nothing can authenticate.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/relay/register", strings.NewReader(registerBody))
	req.Header.Set("Authorization", "Bearer tok-secret")
	s.handleRelayRegister(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("after token cleared: register with old token expected 401, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// The targets listing endpoint must be denied as well.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/relay/targets", nil)
	s.handleRelayTargets(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("after token cleared: targets expected 401, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Give the registry cleanup loop a moment to observe the stop channel.
	time.Sleep(10 * time.Millisecond)
}
