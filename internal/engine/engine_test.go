package engine

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/logger"
)

// newTestEngine creates an engine with a silent logger for testing.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	log := logger.New(logger.Options{
		Mode:    "single",
		Path:    dir,
		Console: false,
	})
	t.Cleanup(func() { log.Close() })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	return &Engine{
		logger:      log,
		ruleLoggers: make(map[string]*logger.Logger),
		running:     make(map[string]bool),
		lastRun:     make(map[string]time.Time),
		dedup:       newDedupCache(),
		dedupStop:   stop,
	}
}

// --- matchFilter ---

func TestMatchFilter_HeaderEq(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{"x-event": "push"}, Query: map[string]string{}}
	f := config.Filter{Type: "header", Key: "x-event", Operator: "eq", Value: "push"}
	if !e.matchFilter(f, req) {
		t.Error("header eq should match")
	}
}

func TestMatchFilter_HeaderEqCaseInsensitive(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{"x-event": "push"}, Query: map[string]string{}}
	f := config.Filter{Type: "header", Key: "X-Event", Operator: "eq", Value: "push"}
	if !e.matchFilter(f, req) {
		t.Error("header key should be case-insensitive (lowercased)")
	}
}

func TestMatchFilter_QueryNe(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{"action": "deploy"}}
	f := config.Filter{Type: "query", Key: "action", Operator: "ne", Value: "rollback"}
	if !e.matchFilter(f, req) {
		t.Error("query ne should match when values differ")
	}
}

func TestMatchFilter_BodyContains(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"ref": "refs/heads/main"},
	}
	f := config.Filter{Type: "body", Key: "ref", Operator: "contains", Value: "main"}
	if !e.matchFilter(f, req) {
		t.Error("body contains should match")
	}
}

func TestMatchFilter_BodyRegex(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"ref": "refs/heads/feature-123"},
	}
	f := config.Filter{Type: "body", Key: "ref", Operator: "regex", Value: `feature-\d+`}
	if !e.matchFilter(f, req) {
		t.Error("body regex should match")
	}
}

func TestMatchFilter_NoMatch(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{"x-event": "pull"}, Query: map[string]string{}}
	f := config.Filter{Type: "header", Key: "x-event", Operator: "eq", Value: "push"}
	if e.matchFilter(f, req) {
		t.Error("should not match")
	}
}

func TestMatchFilter_InvalidType(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}
	f := config.Filter{Type: "cookie", Key: "k", Operator: "eq", Value: "v"}
	if e.matchFilter(f, req) {
		t.Error("invalid type should not match")
	}
}

// --- matchFilters (AND logic) ---

func TestMatchFilters_EmptyIsCatchAll(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}
	if !e.matchFilters(nil, req) {
		t.Error("nil filters should match (catch-all)")
	}
	if !e.matchFilters([]config.Filter{}, req) {
		t.Error("empty filters should match (catch-all)")
	}
}

func TestMatchFilters_AllMustMatch(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{"x-event": "push"},
		Query:   map[string]string{"env": "prod"},
	}
	filters := []config.Filter{
		{Type: "header", Key: "x-event", Operator: "eq", Value: "push"},
		{Type: "query", Key: "env", Operator: "eq", Value: "staging"}, // won't match
	}
	if e.matchFilters(filters, req) {
		t.Error("AND logic: all filters must match")
	}
}

// --- checkToken ---

func TestCheckToken_HeaderMatch(t *testing.T) {
	e := newTestEngine(t)
	tok := &config.TokenConfig{Source: "header", Key: "Authorization", Value: "secret123"}
	req := &RequestData{Headers: map[string]string{"authorization": "secret123"}, Query: map[string]string{}}
	if !e.checkToken(tok, req) {
		t.Error("header token should match")
	}
}

func TestCheckToken_HeaderMismatch(t *testing.T) {
	e := newTestEngine(t)
	tok := &config.TokenConfig{Source: "header", Key: "Authorization", Value: "secret123"}
	req := &RequestData{Headers: map[string]string{"authorization": "wrong"}, Query: map[string]string{}}
	if e.checkToken(tok, req) {
		t.Error("header token should not match")
	}
}

func TestCheckToken_QueryMatch(t *testing.T) {
	e := newTestEngine(t)
	tok := &config.TokenConfig{Source: "query", Key: "token", Value: "abc"}
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{"token": "abc"}}
	if !e.checkToken(tok, req) {
		t.Error("query token should match")
	}
}

func TestCheckToken_InvalidSource(t *testing.T) {
	e := newTestEngine(t)
	tok := &config.TokenConfig{Source: "cookie", Key: "k", Value: "v"}
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}
	if e.checkToken(tok, req) {
		t.Error("invalid source should fail")
	}
}

// --- checkHMAC ---

func TestCheckHMAC_ValidSHA256(t *testing.T) {
	e := newTestEngine(t)
	secret := "mysecret"
	body := []byte(`{"ref":"refs/heads/main"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	cfg := &config.HMACConfig{
		Header:    "X-Hub-Signature-256",
		Secret:    secret,
		Algorithm: "sha256",
		Prefix:    "sha256=",
	}
	req := &RequestData{
		Headers:   map[string]string{"x-hub-signature-256": "sha256=" + sig},
		Query:     map[string]string{},
		BodyBytes: body,
	}
	if !e.checkHMAC(cfg, req) {
		t.Error("valid HMAC should pass")
	}
}

func TestCheckHMAC_InvalidSignature(t *testing.T) {
	e := newTestEngine(t)
	cfg := &config.HMACConfig{
		Header:    "X-Hub-Signature-256",
		Secret:    "secret",
		Algorithm: "sha256",
		Prefix:    "sha256=",
	}
	req := &RequestData{
		Headers:   map[string]string{"x-hub-signature-256": "sha256=0000000000000000000000000000000000000000000000000000000000000000"},
		Query:     map[string]string{},
		BodyBytes: []byte(`{"ref":"main"}`),
	}
	if e.checkHMAC(cfg, req) {
		t.Error("invalid HMAC signature should fail")
	}
}

func TestCheckHMAC_MissingHeader(t *testing.T) {
	e := newTestEngine(t)
	cfg := &config.HMACConfig{Header: "X-Sig", Secret: "key", Algorithm: "sha256", Prefix: "sha256="}
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}, BodyBytes: []byte("body")}
	if e.checkHMAC(cfg, req) {
		t.Error("missing HMAC header should fail")
	}
}

func TestCheckHMAC_InvalidHex(t *testing.T) {
	e := newTestEngine(t)
	cfg := &config.HMACConfig{Header: "X-Sig", Secret: "key", Algorithm: "sha256", Prefix: ""}
	req := &RequestData{
		Headers:   map[string]string{"x-sig": "not-valid-hex!!!"},
		Query:     map[string]string{},
		BodyBytes: []byte("body"),
	}
	if e.checkHMAC(cfg, req) {
		t.Error("invalid hex in signature should fail")
	}
}

// --- checkIPWhitelist ---

func TestCheckIPWhitelist_ExactMatch(t *testing.T) {
	e := newTestEngine(t)
	if !e.checkIPWhitelist([]string{"192.168.1.1"}, "192.168.1.1") {
		t.Error("exact IP should match")
	}
}

func TestCheckIPWhitelist_WithPort(t *testing.T) {
	e := newTestEngine(t)
	if !e.checkIPWhitelist([]string{"10.0.0.1"}, "10.0.0.1:12345") {
		t.Error("IP with port should match after stripping port")
	}
}

func TestCheckIPWhitelist_CIDRMatch(t *testing.T) {
	e := newTestEngine(t)
	if !e.checkIPWhitelist([]string{"192.168.1.0/24"}, "192.168.1.42") {
		t.Error("IP in CIDR range should match")
	}
}

func TestCheckIPWhitelist_NoMatch(t *testing.T) {
	e := newTestEngine(t)
	if e.checkIPWhitelist([]string{"10.0.0.0/8"}, "192.168.1.1") {
		t.Error("IP outside CIDR range should not match")
	}
}

func TestCheckIPWhitelist_InvalidIP(t *testing.T) {
	e := newTestEngine(t)
	if e.checkIPWhitelist([]string{"10.0.0.1"}, "not-an-ip") {
		t.Error("invalid IP should not match")
	}
}

// --- resolvePolicy ---

func TestResolvePolicy_RuleOverridesFile(t *testing.T) {
	e := newTestEngine(t)
	fileLevel := &config.ExecutionConfig{Policy: "block"}
	ruleLevel := &config.ExecutionConfig{Policy: "always"}
	got := e.resolvePolicy(fileLevel, ruleLevel)
	if got.Policy != "always" {
		t.Errorf("rule-level should override file-level, got '%s'", got.Policy)
	}
}

func TestResolvePolicy_FileLevelDefault(t *testing.T) {
	e := newTestEngine(t)
	fileLevel := &config.ExecutionConfig{Policy: "cooldown", CooldownSeconds: 30}
	got := e.resolvePolicy(fileLevel, nil)
	if got.Policy != "cooldown" {
		t.Errorf("file-level should apply when no rule-level, got '%s'", got.Policy)
	}
}

func TestResolvePolicy_DefaultBlock(t *testing.T) {
	e := newTestEngine(t)
	got := e.resolvePolicy(nil, nil)
	if got.Policy != "block" {
		t.Errorf("default policy should be 'block', got '%s'", got.Policy)
	}
}

// --- checkPolicy ---

func TestCheckPolicy_Always(t *testing.T) {
	e := newTestEngine(t)
	policy := config.ExecutionConfig{Policy: "always"}
	if blocked := e.checkPolicy("test/task", policy); blocked != nil {
		t.Error("always policy should never block")
	}
}

func TestCheckPolicy_BlockWhenRunning(t *testing.T) {
	e := newTestEngine(t)
	e.running["test/task"] = true
	policy := config.ExecutionConfig{Policy: "block"}
	if blocked := e.checkPolicy("test/task", policy); blocked == nil {
		t.Error("block policy should block when running")
	}
}

func TestCheckPolicy_BlockWhenNotRunning(t *testing.T) {
	e := newTestEngine(t)
	policy := config.ExecutionConfig{Policy: "block"}
	if blocked := e.checkPolicy("test/task", policy); blocked != nil {
		t.Error("block policy should not block when not running")
	}
}

// --- extractBodyValue ---

func TestExtractBodyValue_SimplePath(t *testing.T) {
	e := newTestEngine(t)
	body := map[string]interface{}{"ref": "refs/heads/main"}
	if got := e.extractBodyValue(body, "ref"); got != "refs/heads/main" {
		t.Errorf("expected 'refs/heads/main', got '%s'", got)
	}
}

func TestExtractBodyValue_NestedPath(t *testing.T) {
	e := newTestEngine(t)
	body := map[string]interface{}{
		"repository": map[string]interface{}{
			"owner": map[string]interface{}{
				"name": "alice",
			},
		},
	}
	if got := e.extractBodyValue(body, "repository.owner.name"); got != "alice" {
		t.Errorf("expected 'alice', got '%s'", got)
	}
}

func TestExtractBodyValue_ArrayIndex(t *testing.T) {
	e := newTestEngine(t)
	body := map[string]interface{}{
		"commits": []interface{}{
			map[string]interface{}{"message": "first"},
			map[string]interface{}{"message": "second"},
		},
	}
	if got := e.extractBodyValue(body, "commits[1].message"); got != "second" {
		t.Errorf("expected 'second', got '%s'", got)
	}
}

func TestExtractBodyValue_MissingKey(t *testing.T) {
	e := newTestEngine(t)
	body := map[string]interface{}{"a": "b"}
	if got := e.extractBodyValue(body, "nonexistent"); got != "" {
		t.Errorf("missing key should return empty, got '%s'", got)
	}
}

func TestExtractBodyValue_NilBody(t *testing.T) {
	e := newTestEngine(t)
	if got := e.extractBodyValue(nil, "any.path"); got != "" {
		t.Errorf("nil body should return empty, got '%s'", got)
	}
}

func TestExtractBodyValue_FloatValue(t *testing.T) {
	e := newTestEngine(t)
	body := map[string]interface{}{"count": float64(42)}
	if got := e.extractBodyValue(body, "count"); got != "42" {
		t.Errorf("expected '42', got '%s'", got)
	}
}

func TestExtractBodyValue_BoolValue(t *testing.T) {
	e := newTestEngine(t)
	body := map[string]interface{}{"force": true}
	if got := e.extractBodyValue(body, "force"); got != "true" {
		t.Errorf("expected 'true', got '%s'", got)
	}
}

// --- parseJSONPath ---

func TestParseJSONPath_Simple(t *testing.T) {
	parts := parseJSONPath("ref")
	if len(parts) != 1 || parts[0].key != "ref" || parts[0].index != -1 {
		t.Errorf("unexpected parts: %+v", parts)
	}
}

func TestParseJSONPath_Nested(t *testing.T) {
	parts := parseJSONPath("repo.owner.name")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	if parts[2].key != "name" {
		t.Errorf("expected key 'name', got '%s'", parts[2].key)
	}
}

func TestParseJSONPath_WithArrayIndex(t *testing.T) {
	parts := parseJSONPath("commits[0].message")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].key != "commits" || parts[0].index != 0 {
		t.Errorf("expected commits[0], got %+v", parts[0])
	}
	if parts[1].key != "message" || parts[1].index != -1 {
		t.Errorf("expected message, got %+v", parts[1])
	}
}

// --- resolveActionTemplates ---

func TestResolveActionTemplates_BodyVar(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"branch": "main"},
	}
	result := e.resolveActionTemplates("deploy {{.body.branch}}", req, nil)
	if result != "deploy main" {
		t.Errorf("expected 'deploy main', got '%s'", result)
	}
}

func TestResolveActionTemplates_HeaderVar(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{"x-repo": "myapp"},
		Query:   map[string]string{},
	}
	result := e.resolveActionTemplates("deploy {{.header.x-repo}}", req, nil)
	if result != "deploy myapp" {
		t.Errorf("expected 'deploy myapp', got '%s'", result)
	}
}

func TestResolveActionTemplates_QueryVar(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{"env": "prod"},
	}
	result := e.resolveActionTemplates("deploy {{.query.env}}", req, nil)
	if result != "deploy prod" {
		t.Errorf("expected 'deploy prod', got '%s'", result)
	}
}

func TestResolveActionTemplates_PassArgs(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{"x-token": "abc123"},
		Query:   map[string]string{},
	}
	passArgs := []config.PassArg{{Source: "header", Key: "x-token"}}
	result := e.resolveActionTemplates("echo", req, passArgs)
	if result != "echo abc123" {
		t.Errorf("expected 'echo abc123', got '%s'", result)
	}
}

func TestResolveActionTemplates_NoTemplates(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{Headers: map[string]string{}, Query: map[string]string{}}
	result := e.resolveActionTemplates("plain command", req, nil)
	if result != "plain command" {
		t.Errorf("expected unchanged, got '%s'", result)
	}
}

func init() {
	// Ensure HOOKRUN env var is not set during tests
	os.Unsetenv("HOOKRUN")
}
