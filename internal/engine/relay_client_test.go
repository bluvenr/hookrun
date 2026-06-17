package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
)

// --- resolveURL ---

func TestRelayClient_ResolveURL_Explicit(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://upstream:9000",
		URL:      "http://10.0.0.5:9000/webhook/deploy",
		Tags:     []string{"web"},
	}
	rc := NewRelayClient(cfg, 9000, nil)
	url := rc.resolveURL()
	if url != "http://10.0.0.5:9000/webhook/deploy" {
		t.Errorf("expected explicit URL, got %s", url)
	}
}

func TestRelayClient_ResolveURL_AutoDetect(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream:    "http://upstream:9000",
		Tags:        []string{"web"},
		WebhookPath: "/webhook/deploy",
	}
	rc := NewRelayClient(cfg, 9000, nil)
	url := rc.resolveURL()

	// Should use auto-detected IP + port + path
	if url == "" {
		t.Error("auto-detected URL should not be empty")
	}
	if len(url) < 10 {
		t.Errorf("auto-detected URL seems too short: %s", url)
	}
}

func TestRelayClient_ResolveURL_DefaultPath(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://upstream:9000",
		Tags:     []string{"web"},
	}
	rc := NewRelayClient(cfg, 8080, nil)
	url := rc.resolveURL()

	// Should use default /webhook path
	if url == "" {
		t.Error("URL should not be empty")
	}
}

func TestRelayClient_ResolveURL_TrailingSlash(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://upstream:9000",
		URL:      "http://10.0.0.5:9000/webhook/",
		Tags:     []string{"web"},
	}
	rc := NewRelayClient(cfg, 9000, nil)
	url := rc.resolveURL()
	if url != "http://10.0.0.5:9000/webhook" {
		t.Errorf("trailing slash should be trimmed, got %s", url)
	}
}

// --- detectLocalIP ---

func TestDetectLocalIP(t *testing.T) {
	ip := detectLocalIP()
	// On CI or some environments, this might be empty, so just check it doesn't panic
	if ip != "" {
		// Basic format check
		if len(ip) < 7 {
			t.Errorf("IP seems too short: %s", ip)
		}
	}
}

// --- register/unregister HTTP interaction ---

func TestRelayClient_Register_Success(t *testing.T) {
	var receivedBody map[string]interface{}
	var receivedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/relay/register" {
			t.Errorf("expected /api/relay/register, got %s", r.URL.Path)
		}
		receivedAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
	}))
	defer server.Close()

	cfg := &config.RelayClientConfig{
		Upstream:      server.URL,
		URL:           "http://10.0.0.1:9000/webhook",
		Token:         "my-token",
		RegistryToken: "reg-token",
		Tags:          []string{"web", "prod"},
		TTL:           120,
	}
	rc := NewRelayClient(cfg, 9000, &testLogWriter{})

	if err := rc.register(); err != nil {
		t.Fatalf("register should succeed: %v", err)
	}

	if receivedAuth != "Bearer reg-token" {
		t.Errorf("expected Bearer reg-token, got %s", receivedAuth)
	}
	if receivedBody["url"] != "http://10.0.0.1:9000/webhook" {
		t.Errorf("expected URL in body, got %v", receivedBody["url"])
	}
	if receivedBody["token"] != "my-token" {
		t.Errorf("expected token in body, got %v", receivedBody["token"])
	}
}

func TestRelayClient_Register_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
	}))
	defer server.Close()

	cfg := &config.RelayClientConfig{
		Upstream: server.URL,
		URL:      "http://10.0.0.1:9000/webhook",
		Tags:     []string{"web"},
	}
	rc := NewRelayClient(cfg, 9000, &testLogWriter{})

	if err := rc.register(); err == nil {
		t.Error("register should fail on 500")
	}
}

func TestRelayClient_Register_ConnectionRefused(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://127.0.0.1:1", // unlikely to be listening
		URL:      "http://10.0.0.1:9000/webhook",
		Tags:     []string{"web"},
	}
	rc := NewRelayClient(cfg, 9000, &testLogWriter{})

	if err := rc.register(); err == nil {
		t.Error("register should fail on connection refused")
	}
}

func TestRelayClient_Unregister_Success(t *testing.T) {
	var receivedMethod string
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "unregistered"})
	}))
	defer server.Close()

	cfg := &config.RelayClientConfig{
		Upstream: server.URL,
		URL:      "http://10.0.0.1:9000/webhook",
		Tags:     []string{"web"},
	}
	rc := NewRelayClient(cfg, 9000, &testLogWriter{})

	if err := rc.unregister(); err != nil {
		t.Fatalf("unregister should succeed: %v", err)
	}
	if receivedMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", receivedMethod)
	}
	if receivedBody["url"] != "http://10.0.0.1:9000/webhook" {
		t.Errorf("expected URL in body, got %v", receivedBody["url"])
	}
}

// --- Start/Stop lifecycle ---

func TestRelayClient_StartStop(t *testing.T) {
	var registerCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&registerCount, 1)
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	cfg := &config.RelayClientConfig{
		Upstream: server.URL,
		URL:      "http://10.0.0.1:9000/webhook",
		Tags:     []string{"web"},
		TTL:      20, // heartbeat every 10s
	}
	rc := NewRelayClient(cfg, 9000, &testLogWriter{})
	rc.Start()

	// Wait for initial registration
	time.Sleep(100 * time.Millisecond)

	count := atomic.LoadInt32(&registerCount)
	if count < 1 {
		t.Errorf("expected at least 1 register call, got %d", count)
	}

	// Stop should send unregister
	rc.Stop()

	// Give time for unregister to complete
	time.Sleep(100 * time.Millisecond)

	finalCount := atomic.LoadInt32(&registerCount)
	// Stop sends DELETE which also hits the handler
	if finalCount < 2 {
		t.Errorf("expected at least 2 calls (register + unregister), got %d", finalCount)
	}
}

func TestRelayClient_Start_FailsNonBlocking(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://127.0.0.1:1", // will fail
		URL:      "http://10.0.0.1:9000/webhook",
		Tags:     []string{"web"},
		TTL:      60,
	}
	rc := NewRelayClient(cfg, 9000, &testLogWriter{})

	// Start should NOT block or panic even when upstream is unreachable
	done := make(chan struct{})
	go func() {
		rc.Start()
		close(done)
	}()

	select {
	case <-done:
		// OK, start completed
	case <-time.After(5 * time.Second):
		t.Fatal("Start() blocked for too long")
	}

	// Clean up
	rc.Stop()
}

// --- RelayClientConfig Validate ---

func TestRelayClientConfig_Validate_Valid(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://upstream:9000",
		Tags:     []string{"web"},
		TTL:      120,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}
}

func TestRelayClientConfig_Validate_NoUpstream(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Tags: []string{"web"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("missing upstream should fail")
	}
}

func TestRelayClientConfig_Validate_NoTags(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://upstream:9000",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("missing tags should fail")
	}
}

func TestRelayClientConfig_Validate_NegativeTTL(t *testing.T) {
	cfg := &config.RelayClientConfig{
		Upstream: "http://upstream:9000",
		Tags:     []string{"web"},
		TTL:      -1,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("negative TTL should fail")
	}
}

// testLogWriter is a minimal logger.LogWriter implementation for tests.
type testLogWriter struct{}

func (l *testLogWriter) Info(format string, args ...interface{})  {}
func (l *testLogWriter) Warn(format string, args ...interface{})  {}
func (l *testLogWriter) Error(format string, args ...interface{}) {}
func (l *testLogWriter) Debug(format string, args ...interface{}) {}
func (l *testLogWriter) Close()                                   {}
