package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/engine"
	"github.com/bluvenr/hookrun/internal/logger"
)

// D-08: relay targets are registered by remote parties, so IP-literal hosts
// must be policy-checked at registration time. Default policy blocks
// link-local/cloud metadata (169.254/16, fe80::/10) but keeps loopback and
// RFC1918 allowed for local relay clusters. relay_block_private=true tightens
// the policy to also block loopback/RFC1918/ULA. Hostname hosts are deferred
// to the dial-side check (DNS rebinding defense).

func newSSRFTestServer(t *testing.T, blockPrivate bool) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "server:\n  port: 9000\n  route: \"/webhook\"\n  relay_registry_token: \"tok-secret\"\n" +
		"  relay_block_private: " + map[bool]string{true: "true", false: "false"}[blockPrivate] + "\n" +
		"log:\n  mode: \"single\"\n  path: " + filepath.Join(dir, "logs") + "\n" +
		"config_dir: " + filepath.Join(dir, "hooks") + "\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
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
	return New(mgr, eng, log)
}

func postRegister(s *Server, url string) int {
	body := `{"url":"` + url + `","tags":["t"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/relay/register", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok-secret")
	s.handleRelayRegister(rec, req)
	return rec.Code
}

func TestRelayRegister_DefaultPolicyBlocksMetadata(t *testing.T) {
	s := newSSRFTestServer(t, false)
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.1.1/x",
		"http://[fe80::1]/x",
		"http://[::ffff:169.254.169.254]/x",
	} {
		if code := postRegister(s, u); code != http.StatusBadRequest {
			t.Errorf("default policy: register %s expected 400, got %d", u, code)
		}
	}
}

func TestRelayRegister_DefaultPolicyAllowsLocalCluster(t *testing.T) {
	s := newSSRFTestServer(t, false)
	for _, u := range []string{
		"http://127.0.0.1:9001/webhook",
		"http://10.0.0.5:9000/webhook",
		"http://192.168.1.5:9000/webhook",
		"http://[::1]:9001/webhook",
	} {
		if code := postRegister(s, u); code != http.StatusOK {
			t.Errorf("default policy: register %s expected 200, got %d", u, code)
		}
	}
}

func TestRelayRegister_StrictPolicyBlocksPrivate(t *testing.T) {
	s := newSSRFTestServer(t, true)
	for _, u := range []string{
		"http://169.254.169.254/x",
		"http://127.0.0.1:9001/webhook",
		"http://10.0.0.5:9000/webhook",
		"http://172.16.0.1:9000/webhook",
		"http://192.168.1.5:9000/webhook",
		"http://[::1]:9001/webhook",
		"http://[fc00::1]:9000/webhook",
	} {
		if code := postRegister(s, u); code != http.StatusBadRequest {
			t.Errorf("strict policy: register %s expected 400, got %d", u, code)
		}
	}
	// Public addresses stay allowed even in strict mode.
	if code := postRegister(s, "http://8.8.8.8:9000/webhook"); code != http.StatusOK {
		t.Errorf("strict policy: public register expected 200, got %d", code)
	}
}

func TestRelayRegister_HostnameDeferredToDialSide(t *testing.T) {
	s := newSSRFTestServer(t, true)
	// Hostnames cannot be classified without DNS; registration accepts them
	// and the dial-side Control hook enforces the policy after resolution.
	if code := postRegister(s, "http://localhost:9001/webhook"); code != http.StatusOK {
		t.Errorf("hostname register expected 200 (dial-side enforcement), got %d", code)
	}
}
