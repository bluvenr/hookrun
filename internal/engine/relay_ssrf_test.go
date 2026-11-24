package engine

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bluvenr/hookrun/internal/config"
)

// D-08 dial-side enforcement: registration-time checks only cover IP-literal
// hosts. A hostname can resolve to a blocked address later (DNS rebinding),
// so sendToTarget must re-check every resolved IP in the dialer's Control
// hook, reading the policy live from the provider (hot-reload safe).

func relaySSRFAction(targetURL string) *config.Action {
	return &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{{URL: targetURL}},
			Timeout: 5,
		},
	}
}

func relaySSRFRequest() *RequestData {
	return &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte(`{"event":"push"}`),
		BodyRaw:   `{"event":"push"}`,
		RequestID: "ssrf-test-1",
		IP:        "203.0.113.7",
	}
}

func TestExecuteRelay_StrictPolicyBlocksLoopbackTarget(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := newTestEngine(t)
	e.SetRelayPolicyProvider(func() bool { return true })

	res := e.executeRelay(relaySSRFAction(srv.URL), relaySSRFRequest(), "cfg", "rule", e.logger)
	if res.Error == nil {
		t.Fatalf("strict policy: relay to loopback target must fail, got success (stdout=%s)", res.Stdout)
	}
	if hits != 0 {
		t.Errorf("strict policy: target must never be contacted, got %d hits", hits)
	}
	if !strings.Contains(res.Stderr, "SSRF") {
		t.Errorf("failure detail should mention the SSRF policy, got: %s", res.Stderr)
	}
}

func TestExecuteRelay_StrictPolicyBlocksHostnameResolvingLoopback(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Swap the IP literal for the hostname "localhost": registration would
	// accept this (no DNS at register time), so the dial-side Control hook
	// is the only line of defense.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	hostnameURL := "http://localhost:" + u.Port() + "/"

	e := newTestEngine(t)
	e.SetRelayPolicyProvider(func() bool { return true })

	res := e.executeRelay(relaySSRFAction(hostnameURL), relaySSRFRequest(), "cfg", "rule", e.logger)
	if res.Error == nil {
		t.Fatalf("strict policy: relay to localhost must fail after resolution, got success (stdout=%s)", res.Stdout)
	}
	if hits != 0 {
		t.Errorf("strict policy: target must never be contacted, got %d hits", hits)
	}
}

func TestExecuteRelay_DefaultPolicyAllowsLoopbackTarget(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := newTestEngine(t)
	e.SetRelayPolicyProvider(func() bool { return false })

	res := e.executeRelay(relaySSRFAction(srv.URL), relaySSRFRequest(), "cfg", "rule", e.logger)
	if res.Error != nil {
		t.Fatalf("default policy: local relay cluster must keep working, got %v (stderr=%s)", res.Error, res.Stderr)
	}
	if hits != 1 {
		t.Errorf("default policy: expected exactly 1 hit, got %d", hits)
	}
}

func TestExecuteRelay_NoProviderDefaultsToAllowLocal(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Engines without a wired provider (tests, embedders) keep the default
	// policy: loopback allowed, metadata still blocked by the Control hook.
	e := newTestEngine(t)
	res := e.executeRelay(relaySSRFAction(srv.URL), relaySSRFRequest(), "cfg", "rule", e.logger)
	if res.Error != nil {
		t.Fatalf("no provider: loopback target must be allowed, got %v", res.Error)
	}
	if hits != 1 {
		t.Errorf("no provider: expected exactly 1 hit, got %d", hits)
	}
}
