package engine

import (
	"runtime"
	"strings"
	"testing"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/executor"
)

// --- envVarName: HOOKRUN_ prefix logic ---

func TestEnvVarName_AutoAddPrefix(t *testing.T) {
	result := envVarName("GITHUB_EVENT")
	if result != "HOOKRUN_GITHUB_EVENT" {
		t.Errorf("expected HOOKRUN_GITHUB_EVENT, got %s", result)
	}
}

func TestEnvVarName_AlreadyHasPrefix(t *testing.T) {
	result := envVarName("HOOKRUN_GITHUB_EVENT")
	if result != "HOOKRUN_GITHUB_EVENT" {
		t.Errorf("expected HOOKRUN_GITHUB_EVENT (no double prefix), got %s", result)
	}
}

func TestEnvVarName_LowercaseInput(t *testing.T) {
	result := envVarName("git_ref")
	if result != "HOOKRUN_GIT_REF" {
		t.Errorf("expected HOOKRUN_GIT_REF (uppercased), got %s", result)
	}
}

func TestEnvVarName_LowercaseWithPrefix(t *testing.T) {
	result := envVarName("hookrun_custom")
	if result != "HOOKRUN_CUSTOM" {
		t.Errorf("expected HOOKRUN_CUSTOM, got %s", result)
	}
}

// --- buildEnvVars: default + env_from ---

func TestBuildEnvVars_DefaultVars(t *testing.T) {
	e := newTestEngine(t)
	action := &config.Action{Type: "command", Cmd: "echo"}
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		BodyRaw: `{"ref":"main"}`,
		IP:      "192.168.1.100",
	}

	envVars := e.buildEnvVars(action, req)

	found := map[string]bool{}
	for _, ev := range envVars {
		parts := strings.SplitN(ev, "=", 2)
		if len(parts) == 2 {
			found[parts[0]] = true
		}
	}

	if !found["HOOKRUN_RAW_BODY"] {
		t.Error("missing HOOKRUN_RAW_BODY default env var")
	}
	if !found["HOOKRUN_TRIGGER_IP"] {
		t.Error("missing HOOKRUN_TRIGGER_IP default env var")
	}
}

func TestBuildEnvVars_WithEnvFrom(t *testing.T) {
	e := newTestEngine(t)
	action := &config.Action{
		Type: "command",
		Cmd:  "echo",
		EnvFrom: []config.EnvSource{
			{Source: "header", Key: "X-GitHub-Event", Env: "GITHUB_EVENT"},
			{Source: "body", Key: "ref", Env: "GIT_REF"},
			{Source: "query", Key: "token", Env: "TOKEN"},
		},
	}
	req := &RequestData{
		Headers: map[string]string{"x-github-event": "push"},
		Query:   map[string]string{"token": "abc123"},
		Body:    map[string]interface{}{"ref": "refs/heads/main"},
		BodyRaw: `{"ref":"refs/heads/main"}`,
		IP:      "10.0.0.1",
	}

	envVars := e.buildEnvVars(action, req)

	expected := map[string]string{
		"HOOKRUN_GITHUB_EVENT": "push",
		"HOOKRUN_GIT_REF":      "refs/heads/main",
		"HOOKRUN_TOKEN":        "abc123",
	}

	envMap := make(map[string]string)
	for _, ev := range envVars {
		parts := strings.SplitN(ev, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	for key, wantVal := range expected {
		gotVal, ok := envMap[key]
		if !ok {
			t.Errorf("missing env var %s", key)
			continue
		}
		if gotVal != wantVal {
			t.Errorf("env var %s: expected '%s', got '%s'", key, wantVal, gotVal)
		}
	}
}

func TestBuildEnvVars_NoDoublePrefix(t *testing.T) {
	e := newTestEngine(t)
	action := &config.Action{
		Type: "command",
		Cmd:  "echo",
		EnvFrom: []config.EnvSource{
			{Source: "body", Key: "ref", Env: "HOOKRUN_GIT_REF"},
		},
	}
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		Body:    map[string]interface{}{"ref": "main"},
		BodyRaw: `{"ref":"main"}`,
		IP:      "10.0.0.1",
	}

	envVars := e.buildEnvVars(action, req)

	envMap := make(map[string]string)
	for _, ev := range envVars {
		parts := strings.SplitN(ev, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Should be HOOKRUN_GIT_REF, not HOOKRUN_HOOKRUN_GIT_REF
	if _, ok := envMap["HOOKRUN_HOOKRUN_GIT_REF"]; ok {
		t.Error("double prefix detected: HOOKRUN_HOOKRUN_GIT_REF should not exist")
	}
	if v, ok := envMap["HOOKRUN_GIT_REF"]; !ok || v != "main" {
		t.Errorf("HOOKRUN_GIT_REF should be 'main', got '%s' (ok=%v)", v, ok)
	}
}

// --- resolveActionTemplates: {{.raw_body}} ---

func TestResolveActionTemplates_RawBody(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		BodyRaw: `{"action":"push","ref":"main"}`,
	}
	result := e.resolveActionTemplates(`echo '{{.raw_body}}'`, req, nil)
	expected := `echo '{"action":"push","ref":"main"}'`
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestResolveActionTemplates_RawBodyWithDollarSign(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		BodyRaw: `{"price":"$100"}`,
	}
	result := e.resolveActionTemplates(`echo '{{.raw_body}}'`, req, nil)
	expected := `echo '{"price":"$100"}'`
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

func TestResolveActionTemplates_RawBodyWithSpaces(t *testing.T) {
	e := newTestEngine(t)
	req := &RequestData{
		Headers: map[string]string{},
		Query:   map[string]string{},
		BodyRaw: `{"msg":"hello world"}`,
	}
	result := e.resolveActionTemplates(`process {{ .raw_body }}`, req, nil)
	expected := `process {"msg":"hello world"}`
	if result != expected {
		t.Errorf("expected '%s', got '%s'", expected, result)
	}
}

// --- EnvSource.Validate ---

func TestEnvSourceValidate_Valid(t *testing.T) {
	es := config.EnvSource{Source: "header", Key: "X-Token", Env: "TOKEN"}
	if err := es.Validate("test", 0, 0); err != nil {
		t.Errorf("should be valid, got error: %v", err)
	}
}

func TestEnvSourceValidate_InvalidSource(t *testing.T) {
	es := config.EnvSource{Source: "invalid", Key: "k", Env: "E"}
	if err := es.Validate("test", 0, 0); err == nil {
		t.Error("should reject invalid source")
	}
}

func TestEnvSourceValidate_MissingKey(t *testing.T) {
	es := config.EnvSource{Source: "body", Key: "", Env: "E"}
	if err := es.Validate("test", 0, 0); err == nil {
		t.Error("should reject missing key")
	}
}

func TestEnvSourceValidate_MissingEnv(t *testing.T) {
	es := config.EnvSource{Source: "query", Key: "k", Env: ""}
	if err := es.Validate("test", 0, 0); err == nil {
		t.Error("should reject missing env")
	}
}

// --- Executor: env vars injected into subprocess ---

func TestExecuteCommand_CustomEnvVars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping env var test on Windows")
	}
	result := executor.ExecuteCommand("echo $HOOKRUN_TEST_VAR", 10, false, []string{"HOOKRUN_TEST_VAR=hello_env"})
	if !result.Success() {
		t.Fatalf("should succeed: %v", result.Error)
	}
	out := strings.TrimSpace(result.Stdout)
	if out != "hello_env" {
		t.Errorf("expected 'hello_env', got '%s'", out)
	}
}

func TestExecuteCommand_DefaultEnvVarsOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping env var test on Windows")
	}
	// Custom env var should not override PATH (forced HOOKRUN_ prefix)
	result := executor.ExecuteCommand("echo $HOOKRUN_PATH", 10, false, []string{"HOOKRUN_PATH=custom"})
	if !result.Success() {
		t.Fatalf("should succeed: %v", result.Error)
	}
	out := strings.TrimSpace(result.Stdout)
	if out != "custom" {
		t.Errorf("expected 'custom', got '%s'", out)
	}
}
