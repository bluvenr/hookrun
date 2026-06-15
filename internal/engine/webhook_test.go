package engine

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/version"
)

// --- Action.Validate: webhook type ---

func TestActionValidate_WebhookValid(t *testing.T) {
	a := config.Action{Type: "webhook", URL: "https://example.com/hook"}
	if err := a.Validate("test", 0); err != nil {
		t.Fatalf("valid webhook action should pass: %v", err)
	}
	if a.Method != "POST" {
		t.Errorf("expected default method POST, got %s", a.Method)
	}
}

func TestActionValidate_WebhookCustomMethod(t *testing.T) {
	a := config.Action{Type: "webhook", URL: "https://example.com/hook", Method: "put"}
	if err := a.Validate("test", 0); err != nil {
		t.Fatalf("webhook with PUT should pass: %v", err)
	}
	if a.Method != "PUT" {
		t.Errorf("expected method normalized to PUT, got %s", a.Method)
	}
}

func TestActionValidate_WebhookMissingURL(t *testing.T) {
	a := config.Action{Type: "webhook"}
	if err := a.Validate("test", 0); err == nil {
		t.Error("webhook without URL should fail")
	}
}

func TestActionValidate_WebhookInvalidMethod(t *testing.T) {
	a := config.Action{Type: "webhook", URL: "https://example.com", Method: "DELETE"}
	if err := a.Validate("test", 0); err == nil {
		t.Error("webhook with DELETE method should fail")
	}
}

// --- webhookResult ---

func TestWebhookResult_Success(t *testing.T) {
	r := &webhookResult{StatusCode: 200}
	if !r.Success() {
		t.Error("200 should be success")
	}
	r2 := &webhookResult{StatusCode: 299}
	if !r2.Success() {
		t.Error("299 should be success")
	}
	r3 := &webhookResult{StatusCode: 400}
	if r3.Success() {
		t.Error("400 should not be success")
	}
	r4 := &webhookResult{StatusCode: 200, Error: io.EOF}
	if r4.Success() {
		t.Error("error present should not be success")
	}
}

func TestWebhookResult_ToActionResult(t *testing.T) {
	r := &webhookResult{StatusCode: 200, Body: `{"ok":true}`, Duration: 100}
	ar := r.toActionResult()
	if ar.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", ar.ExitCode)
	}
	if ar.Stdout != "HTTP 200" {
		t.Errorf("expected stdout 'HTTP 200', got '%s'", ar.Stdout)
	}

	r2 := &webhookResult{StatusCode: 500, Body: "error"}
	ar2 := r2.toActionResult()
	if ar2.ExitCode != 1 {
		t.Errorf("expected exit code 1 for HTTP 500, got %d", ar2.ExitCode)
	}
}

// --- resolveWebhookTemplates ---

func TestResolveWebhookTemplates_BodyVar(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{"x-event": "push"},
		Query:   map[string]string{"env": "prod"},
		Body:    map[string]interface{}{"ref": "main"},
	}
	result := e.resolveWebhookTemplates("ref={{.body.ref}} event={{.header.x-event}} env={{.query.env}}", req)
	expected := "ref=main event=push env=prod"
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

// --- executeWebhook (with httptest server) ---

func TestExecuteWebhook_BasicPOST(t *testing.T) {
	// Set up test server
	var receivedBody string
	var receivedHeaders http.Header
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedHeaders = r.Header
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type:    "webhook",
		URL:     server.URL,
		Method:  "POST",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"text":"hello"}`,
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte(`{"text":"hello"}`),
	}

	result := e.executeWebhook(action, req, "my-config", "on-push", e.logger)

	if !result.Success() {
		t.Fatalf("webhook should succeed: %v", result.Error)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected 200, got %d", result.StatusCode)
	}
	if receivedMethod != "POST" {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedBody != `{"text":"hello"}` {
		t.Errorf("unexpected body: %s", receivedBody)
	}
	// Check auto headers
	if receivedHeaders.Get("X-Hookrun-Source") != "HookRun/v"+version.Version {
		t.Error("missing X-HookRun-Source header")
	}
	if receivedHeaders.Get("X-Hookrun-Config") != "my-config" {
		t.Error("missing X-HookRun-Config header")
	}
	if receivedHeaders.Get("X-Hookrun-Rule") != "on-push" {
		t.Error("missing X-HookRun-Rule header")
	}
}

func TestExecuteWebhook_ForwardHeaders(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type:           "webhook",
		URL:            server.URL,
		Method:         "POST",
		ForwardHeaders: []string{"X-GitHub-Event", "X-Request-Id"},
	}
	req := &RequestData{
		Headers: map[string]string{
			"x-github-event": "push",
			"x-request-id":   "abc-123",
			"cookie":         "should-not-forward",
		},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
	}

	e.executeWebhook(action, req, "cfg", "rule", e.logger)

	if receivedHeaders.Get("X-Github-Event") != "push" {
		t.Errorf("expected forwarded X-GitHub-Event=push, got '%s'", receivedHeaders.Get("X-Github-Event"))
	}
	if receivedHeaders.Get("X-Request-Id") != "abc-123" {
		t.Errorf("expected forwarded X-Request-Id=abc-123, got '%s'", receivedHeaders.Get("X-Request-Id"))
	}
	if receivedHeaders.Get("Cookie") != "" {
		t.Error("Cookie should NOT be forwarded")
	}
}

func TestExecuteWebhook_RawBodyTemplate(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	originalBody := `{"ref":"main","pusher":{"name":"alice"}}`
	action := &config.Action{
		Type:   "webhook",
		URL:    server.URL,
		Method: "POST",
		Body:   `{"event":"{{.header.x-event}}","payload":{{.raw_body}}}`,
	}
	req := &RequestData{
		Headers:   map[string]string{"x-event": "push"},
		Query:     map[string]string{},
		Body:      map[string]interface{}{"ref": "main"},
		BodyBytes: []byte(originalBody),
	}

	e.executeWebhook(action, req, "cfg", "rule", e.logger)

	// Verify the result is valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(receivedBody), &parsed); err != nil {
		t.Fatalf("result should be valid JSON: %v\nBody: %s", err, receivedBody)
	}
	if parsed["event"] != "push" {
		t.Errorf("expected event=push, got %v", parsed["event"])
	}
	payload, ok := parsed["payload"].(map[string]interface{})
	if !ok {
		t.Fatalf("payload should be an object, got %T", parsed["payload"])
	}
	if payload["ref"] != "main" {
		t.Errorf("expected payload.ref=main, got %v", payload["ref"])
	}
}

func TestExecuteWebhook_PureRawBody(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	originalBody := `{"ref":"main","count":42}`
	action := &config.Action{
		Type:   "webhook",
		URL:    server.URL,
		Method: "POST",
		Body:   "{{.raw_body}}",
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte(originalBody),
	}

	e.executeWebhook(action, req, "cfg", "rule", e.logger)

	if receivedBody != originalBody {
		t.Errorf("raw body should be forwarded as-is.\nExpected: %s\nGot: %s", originalBody, receivedBody)
	}
}

func TestExecuteWebhook_GETNoBody(t *testing.T) {
	var receivedBody string
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type:   "webhook",
		URL:    server.URL + "?notify=true",
		Method: "GET",
		Body:   "should be ignored",
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("should be ignored"),
	}

	e.executeWebhook(action, req, "cfg", "rule", e.logger)

	if receivedMethod != "GET" {
		t.Errorf("expected GET, got %s", receivedMethod)
	}
	if receivedBody != "" {
		t.Errorf("GET should have no body, got: %s", receivedBody)
	}
}

func TestExecuteWebhook_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type:   "webhook",
		URL:    server.URL,
		Method: "POST",
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
	}

	result := e.executeWebhook(action, req, "cfg", "rule", e.logger)
	if result.Success() {
		t.Error("HTTP 500 should not be success")
	}
	if result.StatusCode != 500 {
		t.Errorf("expected status 500, got %d", result.StatusCode)
	}
	ar := result.toActionResult()
	if ar.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", ar.ExitCode)
	}
}

func TestExecuteWebhook_InvalidURL(t *testing.T) {
	e := newTestEngine(t)
	action := &config.Action{
		Type:   "webhook",
		URL:    "http://localhost:1/nonexistent",
		Method: "POST",
	}
	req := &RequestData{
		Headers:   map[string]string{},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
	}

	result := e.executeWebhook(action, req, "cfg", "rule", e.logger)
	if result.Success() {
		t.Error("invalid URL should fail")
	}
}

func TestExecuteWebhook_HeaderPriority(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(200)
	}))
	defer server.Close()

	e := newTestEngine(t)
	action := &config.Action{
		Type:           "webhook",
		URL:            server.URL,
		Method:         "POST",
		ForwardHeaders: []string{"X-Event"},
		Headers:        map[string]string{"X-Event": "custom-override"}, // should override forward
	}
	req := &RequestData{
		Headers:   map[string]string{"x-event": "original"},
		Query:     map[string]string{},
		BodyBytes: []byte("{}"),
	}

	e.executeWebhook(action, req, "cfg", "rule", e.logger)

	if receivedHeaders.Get("X-Event") != "custom-override" {
		t.Errorf("custom header should override forwarded header, got '%s'", receivedHeaders.Get("X-Event"))
	}
}
