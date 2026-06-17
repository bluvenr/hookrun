package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
)

// --- RelayTarget.Validate ---

func TestRelayTargetValidate_OK(t *testing.T) {
	rt := config.RelayTarget{URL: "http://10.0.0.2:9000/webhook", Token: "secret"}
	if err := rt.Validate("test", 0); err != nil {
		t.Fatalf("valid target should pass: %v", err)
	}
}

func TestRelayTargetValidate_EmptyURL(t *testing.T) {
	rt := config.RelayTarget{Token: "secret"}
	if err := rt.Validate("test", 0); err == nil {
		t.Error("empty URL should fail")
	}
}

func TestRelayTargetValidate_NoToken(t *testing.T) {
	rt := config.RelayTarget{URL: "http://10.0.0.2:9000/webhook"}
	if err := rt.Validate("test", 0); err != nil {
		t.Fatalf("token is optional, should pass: %v", err)
	}
}

// --- RelayConfig.Validate ---

func TestRelayConfigValidate_OK(t *testing.T) {
	rc := config.RelayConfig{
		Targets: []config.RelayTarget{{URL: "http://a", Token: "t"}},
	}
	if err := rc.Validate("test"); err != nil {
		t.Fatalf("valid config should pass: %v", err)
	}
}

func TestRelayConfigValidate_EmptyTargets(t *testing.T) {
	rc := config.RelayConfig{}
	if err := rc.Validate("test"); err == nil {
		t.Error("empty targets should fail")
	}
}

func TestRelayConfigValidate_NegativeHops(t *testing.T) {
	rc := config.RelayConfig{
		Targets:      []config.RelayTarget{{URL: "http://a"}},
		MaxRelayHops: -1,
	}
	if err := rc.Validate("test"); err == nil {
		t.Error("negative max_relay_hops should fail")
	}
}

func TestRelayConfigValidate_TargetMissingURL(t *testing.T) {
	rc := config.RelayConfig{
		Targets: []config.RelayTarget{{Token: "t"}},
	}
	if err := rc.Validate("test"); err == nil {
		t.Error("target without URL should fail")
	}
}

// --- Action.Validate with relay type ---

func TestActionValidate_RelayOK(t *testing.T) {
	a := config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{{URL: "http://a", Token: "t"}},
		},
	}
	if err := a.Validate("test", 0); err != nil {
		t.Fatalf("valid relay action should pass: %v", err)
	}
}

func TestActionValidate_RelayNoConfig(t *testing.T) {
	a := config.Action{Type: "relay"}
	if err := a.Validate("test", 0); err == nil {
		t.Error("relay without config should fail")
	}
}

// --- Request-ID generation ---

func TestParseRequest_GeneratesRequestID(t *testing.T) {
	r, _ := http.NewRequest("POST", "/webhook", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.RequestID == "" {
		t.Error("RequestID should be auto-generated")
	}
	if !strings.Contains(data.RequestID, "-") {
		t.Errorf("RequestID should contain dash separator, got: %s", data.RequestID)
	}
}

func TestParseRequest_InheritsRequestID(t *testing.T) {
	r, _ := http.NewRequest("POST", "/webhook", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Hookrun-Request-Id", "inherited-id-123")
	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.RequestID != "inherited-id-123" {
		t.Errorf("expected inherited RequestID, got: %s", data.RequestID)
	}
}

func TestParseRequest_RelayHops(t *testing.T) {
	r, _ := http.NewRequest("POST", "/webhook", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Hookrun-Relay-Hops", "2")
	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.RelayHops != 2 {
		t.Errorf("expected RelayHops=2, got: %d", data.RelayHops)
	}
}

func TestParseRequest_RelayHopsDefault(t *testing.T) {
	r, _ := http.NewRequest("POST", "/webhook", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	data, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.RelayHops != 0 {
		t.Errorf("expected RelayHops=0, got: %d", data.RelayHops)
	}
}

// --- generateRequestID ---

func TestGenerateRequestID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateRequestID()
		if ids[id] {
			t.Errorf("duplicate request ID generated: %s", id)
		}
		ids[id] = true
	}
}

// --- executeRelay ---

func TestExecuteRelay_MultiTarget(t *testing.T) {
	var hitCount int32

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		w.WriteHeader(200)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		w.WriteHeader(200)
	}))
	defer server2.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{
				{URL: server1.URL, Token: "tok1"},
				{URL: server2.URL, Token: "tok2"},
			},
		},
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte(`{"event":"push"}`),
		BodyRaw:   `{"event":"push"}`,
		RequestID: "test-req-1",
		IP:        "1.2.3.4",
	}

	result := e.executeRelay(action, req, "cfg", "rule", e.logger)

	if result.ExitCode != 0 {
		t.Fatalf("relay should succeed, got exit code %d: %v", result.ExitCode, result.Error)
	}
	if atomic.LoadInt32(&hitCount) != 2 {
		t.Errorf("expected 2 targets hit, got %d", hitCount)
	}
}

func TestExecuteRelay_HeadersInjected(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets:        []config.RelayTarget{{URL: server.URL, Token: "my-secret"}},
			ForwardHeaders: []string{"X-GitHub-Event"},
		},
	}
	req := &RequestData{
		Headers:   map[string]string{"x-github-event": "push"},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "req-abc",
		IP:        "10.0.0.1",
		RelayHops: 0,
	}

	e.executeRelay(action, req, "cfg", "rule", e.logger)

	if receivedHeaders.Get("X-Hookrun-Relay") != "true" {
		t.Error("missing X-HookRun-Relay header")
	}
	if receivedHeaders.Get("X-Hookrun-Request-Id") != "req-abc" {
		t.Errorf("expected X-HookRun-Request-Id=req-abc, got %s", receivedHeaders.Get("X-Hookrun-Request-Id"))
	}
	if receivedHeaders.Get("X-Hookrun-Relay-Hops") != "1" {
		t.Errorf("expected X-HookRun-Relay-Hops=1, got %s", receivedHeaders.Get("X-Hookrun-Relay-Hops"))
	}
	if receivedHeaders.Get("X-Hookrun-Relay-Token") != "my-secret" {
		t.Errorf("expected token=my-secret, got %s", receivedHeaders.Get("X-Hookrun-Relay-Token"))
	}
	if receivedHeaders.Get("X-Hookrun-Relay-From") != "10.0.0.1" {
		t.Errorf("expected relay-from=10.0.0.1, got %s", receivedHeaders.Get("X-Hookrun-Relay-From"))
	}
	if receivedHeaders.Get("X-Github-Event") != "push" {
		t.Errorf("expected forwarded X-GitHub-Event=push, got %s", receivedHeaders.Get("X-Github-Event"))
	}
}

func TestExecuteRelay_AntiLoop(t *testing.T) {
	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets:      []config.RelayTarget{{URL: "http://unused", Token: "t"}},
			MaxRelayHops: 3,
		},
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "req-loop",
		RelayHops: 3, // already at max
	}

	result := e.executeRelay(action, req, "cfg", "rule", e.logger)
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1 for hop limit, got %d", result.ExitCode)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "hop limit") {
		t.Errorf("expected hop limit error, got: %v", result.Error)
	}
}

func TestExecuteRelay_PartialFailure(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer good.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{
				{URL: good.URL, Token: "t1"},
				{URL: bad.URL, Token: "t2"},
			},
		},
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "req-partial",
		IP:        "1.2.3.4",
	}

	result := e.executeRelay(action, req, "cfg", "rule", e.logger)
	if result.ExitCode != 0 {
		t.Errorf("partial success should still be exit 0, got %d", result.ExitCode)
	}
}

func TestExecuteRelay_AllFail(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{
				{URL: bad.URL, Token: "t1"},
			},
		},
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "req-fail",
		IP:        "1.2.3.4",
	}

	result := e.executeRelay(action, req, "cfg", "rule", e.logger)
	if result.ExitCode != 1 {
		t.Errorf("all targets failing should be exit 1, got %d", result.ExitCode)
	}
}

func TestExecuteRelay_BodyForwarded(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{{URL: server.URL}},
		},
	}
	originalBody := `{"ref":"refs/heads/main","action":"push"}`
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte(originalBody),
		RequestID: "req-body",
		IP:        "1.2.3.4",
	}

	e.executeRelay(action, req, "cfg", "rule", e.logger)

	if receivedBody != originalBody {
		t.Errorf("body mismatch: expected %s, got %s", originalBody, receivedBody)
	}
}

func TestExecuteRelay_Timeout(t *testing.T) {
	// Server that sleeps longer than the relay timeout
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type: "relay",
		Relay: &config.RelayConfig{
			Targets: []config.RelayTarget{{URL: server.URL}},
			Timeout: 1, // 1 second
		},
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
		RequestID: "req-timeout",
		IP:        "1.2.3.4",
	}

	result := e.executeRelay(action, req, "cfg", "rule", e.logger)
	if result.ExitCode != 1 {
		t.Errorf("timeout should result in exit 1, got %d", result.ExitCode)
	}
}

// --- relayResult ---

func TestRelayResult_Success(t *testing.T) {
	r := &relayResult{Targets: 3, Succeeded: 2, Failed: 1}
	if !r.Success() {
		t.Error("partial success should be Success()=true")
	}
	r2 := &relayResult{Targets: 1, Succeeded: 0, Failed: 1}
	if r2.Success() {
		t.Error("all fail should be Success()=false")
	}
}

// --- Tag Resolution ---

func TestResolveRelayTargets_StaticOnly(t *testing.T) {
	e := newTestEngine(t)
	targets := []config.RelayTarget{
		{URL: "http://a:9000/w", Token: "t1"},
		{URL: "http://b:9000/w", Token: "t2"},
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 2 {
		t.Errorf("expected 2 static targets, got %d", len(resolved))
	}
}

func TestResolveRelayTargets_TagOnly(t *testing.T) {
	e := newTestEngine(t)

	// Set up registry with some entries
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://x:9000/w", Token: "tx", Tags: []string{"prod"}})
	reg.Register(RegistryEntry{URL: "http://y:9000/w", Token: "ty", Tags: []string{"prod"}})
	e.registry = reg

	targets := []config.RelayTarget{
		{Tag: "prod"},
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 2 {
		t.Errorf("expected 2 targets from tag 'prod', got %d", len(resolved))
	}
}

func TestResolveRelayTargets_Mixed(t *testing.T) {
	e := newTestEngine(t)

	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://x:9000/w", Token: "tx", Tags: []string{"prod"}})
	reg.Register(RegistryEntry{URL: "http://y:9000/w", Token: "ty", Tags: []string{"prod"}})
	e.registry = reg

	targets := []config.RelayTarget{
		{URL: "http://static:9000/w", Token: "ts"},
		{Tag: "prod"},
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 3 {
		t.Errorf("expected 3 targets (1 static + 2 dynamic), got %d", len(resolved))
	}
	// First should be static
	if resolved[0].URL != "http://static:9000/w" {
		t.Errorf("first target should be static, got %s", resolved[0].URL)
	}
}

func TestResolveRelayTargets_DedupStaticPriority(t *testing.T) {
	e := newTestEngine(t)

	// Registry has same URL as static
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Token: "dynamic-tok", Tags: []string{"prod"}})
	e.registry = reg

	targets := []config.RelayTarget{
		{URL: "http://a:9000/w", Token: "static-tok"}, // static takes priority
		{Tag: "prod"}, // same URL, should be deduped
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 1 {
		t.Errorf("expected 1 target after dedup, got %d", len(resolved))
	}
	if resolved[0].Token != "static-tok" {
		t.Errorf("static token should take priority, got %s", resolved[0].Token)
	}
}

func TestResolveRelayTargets_NoRegistry(t *testing.T) {
	e := newTestEngine(t)
	// registry is nil

	targets := []config.RelayTarget{
		{URL: "http://a:9000/w"},
		{Tag: "prod"}, // should be skipped with warning
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 1 {
		t.Errorf("expected 1 target (tag skipped), got %d", len(resolved))
	}
	if resolved[0].URL != "http://a:9000/w" {
		t.Errorf("expected static target, got %s", resolved[0].URL)
	}
}

func TestResolveRelayTargets_EmptyTagMatch(t *testing.T) {
	e := newTestEngine(t)
	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"staging"}})
	e.registry = reg

	targets := []config.RelayTarget{
		{Tag: "prod"}, // no match
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 0 {
		t.Errorf("expected 0 targets (no match), got %d", len(resolved))
	}
}

func TestResolveRelayTargets_MultipleTags(t *testing.T) {
	e := newTestEngine(t)

	reg := NewTargetRegistry(10, 0)
	reg.Register(RegistryEntry{URL: "http://a:9000/w", Tags: []string{"web", "prod"}})
	reg.Register(RegistryEntry{URL: "http://b:9000/w", Tags: []string{"api", "prod"}})
	e.registry = reg

	targets := []config.RelayTarget{
		{Tag: "web"},  // matches only 'a'
		{Tag: "prod"}, // matches both, but 'a' is already seen
	}

	resolved := e.resolveRelayTargets(targets, "cfg", "rule", e.logger)
	if len(resolved) != 2 {
		t.Errorf("expected 2 targets (deduped), got %d", len(resolved))
	}
}
