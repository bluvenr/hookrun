package config

import (
	"os"
	"path/filepath"
	"testing"
)

// --- GlobalConfig.Validate ---

func TestValidate_Defaults(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults should be valid: %v", err)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("expected default port 9000, got %d", cfg.Server.Port)
	}
	if *cfg.Server.AllowAll != false {
		t.Error("expected default AllowAll=false")
	}
	if *cfg.Server.MaxBodySizeMB != 10 {
		t.Errorf("expected default MaxBodySizeMB=10, got %d", *cfg.Server.MaxBodySizeMB)
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 70000} {
		cfg := Defaults()
		cfg.Server.Port = port
		if err := cfg.Validate(); err == nil {
			t.Errorf("port %d should be invalid", port)
		}
	}
}

func TestValidate_ValidPort(t *testing.T) {
	for _, port := range []int{1, 80, 8080, 65535} {
		cfg := Defaults()
		cfg.Server.Port = port
		if err := cfg.Validate(); err != nil {
			t.Errorf("port %d should be valid: %v", port, err)
		}
	}
}

func TestValidate_EmptyRouteGetsDefault(t *testing.T) {
	cfg := Defaults()
	cfg.Server.Route = ""
	_ = cfg.Validate()
	if cfg.Server.Route != "/webhook" {
		t.Errorf("expected route default '/webhook', got '%s'", cfg.Server.Route)
	}
}

func TestValidate_InvalidLogMode(t *testing.T) {
	cfg := Defaults()
	cfg.Log.Mode = "hourly"
	if err := cfg.Validate(); err == nil {
		t.Error("invalid log mode should fail")
	}
}

func TestValidate_NegativeMaxBodySizeMB(t *testing.T) {
	cfg := Defaults()
	v := -1
	cfg.Server.MaxBodySizeMB = &v
	if err := cfg.Validate(); err == nil {
		t.Error("negative MaxBodySizeMB should fail")
	}
}

func TestValidate_ZeroMaxBodySizeMB(t *testing.T) {
	cfg := Defaults()
	v := 0
	cfg.Server.MaxBodySizeMB = &v
	if err := cfg.Validate(); err != nil {
		t.Errorf("MaxBodySizeMB=0 (unlimited) should be valid: %v", err)
	}
}

// --- IsAllowAll ---

func TestIsAllowAll(t *testing.T) {
	s := ServerConfig{}
	if s.IsAllowAll() {
		t.Error("nil AllowAll should return false")
	}
	f := false
	s.AllowAll = &f
	if s.IsAllowAll() {
		t.Error("AllowAll=false should return false")
	}
	tr := true
	s.AllowAll = &tr
	if !s.IsAllowAll() {
		t.Error("AllowAll=true should return true")
	}
}

// --- Filter.Validate ---

func TestFilterValidate_Valid(t *testing.T) {
	f := Filter{Type: "header", Key: "x-event", Operator: "eq", Value: "push"}
	if err := f.Validate("test", 0); err != nil {
		t.Fatalf("valid filter should pass: %v", err)
	}
}

func TestFilterValidate_InvalidType(t *testing.T) {
	f := Filter{Type: "cookie", Key: "k", Operator: "eq", Value: "v"}
	if err := f.Validate("test", 0); err == nil {
		t.Error("invalid filter type should fail")
	}
}

func TestFilterValidate_MissingKey(t *testing.T) {
	f := Filter{Type: "body", Key: "", Operator: "eq", Value: "v"}
	if err := f.Validate("test", 0); err == nil {
		t.Error("missing key should fail")
	}
}

func TestFilterValidate_InvalidOperator(t *testing.T) {
	f := Filter{Type: "query", Key: "k", Operator: "gt", Value: "v"}
	if err := f.Validate("test", 0); err == nil {
		t.Error("invalid operator should fail")
	}
}

func TestFilterValidate_MissingValue(t *testing.T) {
	f := Filter{Type: "header", Key: "k", Operator: "contains", Value: ""}
	if err := f.Validate("test", 0); err == nil {
		t.Error("missing value should fail")
	}
}

// --- ExecutionConfig.Validate ---

func TestExecutionValidate_ValidPolicies(t *testing.T) {
	for _, p := range []string{"block", "always", "cooldown"} {
		e := ExecutionConfig{Policy: p}
		if p == "cooldown" {
			e.CooldownSeconds = 60
		}
		if err := e.Validate("test"); err != nil {
			t.Errorf("policy '%s' should be valid: %v", p, err)
		}
	}
}

func TestExecutionValidate_InvalidPolicy(t *testing.T) {
	e := ExecutionConfig{Policy: "retry"}
	if err := e.Validate("test"); err == nil {
		t.Error("invalid policy should fail")
	}
}

func TestExecutionValidate_CooldownWithoutSeconds(t *testing.T) {
	e := ExecutionConfig{Policy: "cooldown", CooldownSeconds: 0}
	if err := e.Validate("test"); err == nil {
		t.Error("cooldown with 0 seconds should fail")
	}
}

// --- AuthConfig.Validate ---

func TestAuthValidate_TokenValid(t *testing.T) {
	a := AuthConfig{
		Token: &TokenConfig{Source: "header", Key: "Authorization", Value: "secret"},
	}
	if err := a.Validate("test"); err != nil {
		t.Fatalf("valid token auth should pass: %v", err)
	}
}

func TestAuthValidate_TokenInvalidSource(t *testing.T) {
	a := AuthConfig{
		Token: &TokenConfig{Source: "cookie", Key: "k", Value: "v"},
	}
	if err := a.Validate("test"); err == nil {
		t.Error("invalid token source should fail")
	}
}

func TestAuthValidate_TokenMissingKey(t *testing.T) {
	a := AuthConfig{
		Token: &TokenConfig{Source: "header", Key: "", Value: "v"},
	}
	if err := a.Validate("test"); err == nil {
		t.Error("missing token key should fail")
	}
}

func TestAuthValidate_TokenMissingValue(t *testing.T) {
	a := AuthConfig{
		Token: &TokenConfig{Source: "query", Key: "token", Value: ""},
	}
	if err := a.Validate("test"); err == nil {
		t.Error("missing token value should fail")
	}
}

func TestAuthValidate_HMACValid(t *testing.T) {
	a := AuthConfig{
		HMAC: &HMACConfig{Header: "X-Sig", Secret: "key", Algorithm: "sha256"},
	}
	if err := a.Validate("test"); err != nil {
		t.Fatalf("valid HMAC auth should pass: %v", err)
	}
}

func TestAuthValidate_HMACDefaultAlgorithm(t *testing.T) {
	a := AuthConfig{
		HMAC: &HMACConfig{Header: "X-Sig", Secret: "key"},
	}
	if err := a.Validate("test"); err != nil {
		t.Fatalf("HMAC with empty algorithm should pass (defaults applied): %v", err)
	}
	if a.HMAC.Algorithm != "sha256" {
		t.Errorf("expected default algorithm sha256, got %s", a.HMAC.Algorithm)
	}
	if a.HMAC.Prefix != "sha256=" {
		t.Errorf("expected default prefix 'sha256=', got '%s'", a.HMAC.Prefix)
	}
}

func TestAuthValidate_HMACInvalidAlgorithm(t *testing.T) {
	a := AuthConfig{
		HMAC: &HMACConfig{Header: "X-Sig", Secret: "key", Algorithm: "md5"},
	}
	if err := a.Validate("test"); err == nil {
		t.Error("invalid HMAC algorithm should fail")
	}
}

func TestAuthValidate_HMACMissingHeader(t *testing.T) {
	a := AuthConfig{
		HMAC: &HMACConfig{Header: "", Secret: "key"},
	}
	if err := a.Validate("test"); err == nil {
		t.Error("missing HMAC header should fail")
	}
}

// --- Action.Validate ---

func TestActionValidate_CommandValid(t *testing.T) {
	a := Action{Type: "command", Cmd: "echo hello"}
	if err := a.Validate("test", 0); err != nil {
		t.Fatalf("valid command action should pass: %v", err)
	}
}

func TestActionValidate_ScriptValid(t *testing.T) {
	a := Action{Type: "script", Path: "/tmp/deploy.sh"}
	if err := a.Validate("test", 0); err != nil {
		t.Fatalf("valid script action should pass: %v", err)
	}
}

func TestActionValidate_InvalidType(t *testing.T) {
	a := Action{Type: "webhook"}
	if err := a.Validate("test", 0); err == nil {
		t.Error("invalid action type should fail")
	}
}

func TestActionValidate_CommandMissingCmd(t *testing.T) {
	a := Action{Type: "command", Cmd: ""}
	if err := a.Validate("test", 0); err == nil {
		t.Error("command action without cmd should fail")
	}
}

func TestActionValidate_ScriptMissingPath(t *testing.T) {
	a := Action{Type: "script", Path: ""}
	if err := a.Validate("test", 0); err == nil {
		t.Error("script action without path should fail")
	}
}

func TestActionValidate_InvalidPassArg(t *testing.T) {
	a := Action{
		Type:     "command",
		Cmd:      "echo",
		PassArgs: []PassArg{{Source: "invalid", Key: "k"}},
	}
	if err := a.Validate("test", 0); err == nil {
		t.Error("action with invalid pass_arg should fail")
	}
}

// --- PassArg.Validate ---

func TestPassArgValidate_Valid(t *testing.T) {
	for _, src := range []string{"header", "query", "body"} {
		p := PassArg{Source: src, Key: "k"}
		if err := p.Validate("test", 0, 0); err != nil {
			t.Errorf("pass_arg source '%s' should be valid: %v", src, err)
		}
	}
}

func TestPassArgValidate_InvalidSource(t *testing.T) {
	p := PassArg{Source: "cookie", Key: "k"}
	if err := p.Validate("test", 0, 0); err == nil {
		t.Error("invalid pass_arg source should fail")
	}
}

func TestPassArgValidate_MissingKey(t *testing.T) {
	p := PassArg{Source: "header", Key: ""}
	if err := p.Validate("test", 0, 0); err == nil {
		t.Error("missing pass_arg key should fail")
	}
}

// --- RuleConfig.Validate ---

func TestRuleConfigValidate_MinimalValid(t *testing.T) {
	rc := RuleConfig{
		Name: "test",
		Rules: []Rule{{
			Name:    "deploy",
			Actions: []Action{{Type: "command", Cmd: "echo ok"}},
		}},
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("minimal valid config should pass: %v", err)
	}
}

func TestRuleConfigValidate_MissingName(t *testing.T) {
	rc := RuleConfig{
		Rules: []Rule{{Name: "r", Actions: []Action{{Type: "command", Cmd: "x"}}}},
	}
	if err := rc.Validate(); err == nil {
		t.Error("missing name should fail")
	}
}

func TestRuleConfigValidate_EmptyRules(t *testing.T) {
	rc := RuleConfig{Name: "test", Rules: []Rule{}}
	if err := rc.Validate(); err == nil {
		t.Error("empty rules should fail")
	}
}

func TestRuleConfigValidate_RuleWithoutName(t *testing.T) {
	rc := RuleConfig{
		Name:  "test",
		Rules: []Rule{{Actions: []Action{{Type: "command", Cmd: "x"}}}},
	}
	if err := rc.Validate(); err == nil {
		t.Error("rule without name should fail")
	}
}

func TestRuleConfigValidate_RuleWithoutActions(t *testing.T) {
	rc := RuleConfig{
		Name:  "test",
		Rules: []Rule{{Name: "r1"}},
	}
	if err := rc.Validate(); err == nil {
		t.Error("rule without actions should fail")
	}
}

func TestRuleConfigValidate_LogWithoutPath(t *testing.T) {
	rc := RuleConfig{
		Name: "test",
		Log:  &RuleLogConfig{Path: ""},
		Rules: []Rule{{
			Name:    "r1",
			Actions: []Action{{Type: "command", Cmd: "x"}},
		}},
	}
	if err := rc.Validate(); err == nil {
		t.Error("log config without path should fail")
	}
}

// --- loadGlobalConfig with temp files ---

func TestLoadGlobalConfig_FileNotFound_UsesDefaults(t *testing.T) {
	cfg, err := loadGlobalConfig("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("missing file should use defaults, got error: %v", err)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("expected default port, got %d", cfg.Server.Port)
	}
}

func TestLoadGlobalConfig_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: 8080
  route: /hook
  allow_all: true
  max_body_size_mb: 5
log:
  mode: single
  path: /tmp/logs
config_dir: /tmp/hooks
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadGlobalConfig(path)
	if err != nil {
		t.Fatalf("valid YAML should load: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.Route != "/hook" {
		t.Errorf("expected route '/hook', got '%s'", cfg.Server.Route)
	}
	if !*cfg.Server.AllowAll {
		t.Error("expected AllowAll=true")
	}
	if *cfg.Server.MaxBodySizeMB != 5 {
		t.Errorf("expected MaxBodySizeMB=5, got %d", *cfg.Server.MaxBodySizeMB)
	}
}

func TestLoadGlobalConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGlobalConfig(path); err == nil {
		t.Error("invalid YAML should fail")
	}
}

func TestLoadGlobalConfig_InvalidPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: 99999
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGlobalConfig(path); err == nil {
		t.Error("invalid port should fail validation")
	}
}

func TestLoadGlobalConfig_WithEnvInterpolation(t *testing.T) {
	os.Setenv("HOOKRUN_TEST_PORT", "7777")
	defer os.Unsetenv("HOOKRUN_TEST_PORT")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: ${env:HOOKRUN_TEST_PORT}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Note: port is int, so YAML parser needs the interpolated string "7777" to parse as int
	cfg, err := loadGlobalConfig(path)
	if err != nil {
		t.Fatalf("env interpolation should work: %v", err)
	}
	if cfg.Server.Port != 7777 {
		t.Errorf("expected port 7777 from env, got %d", cfg.Server.Port)
	}
}

// --- loadRuleFile ---

func TestLoadRuleFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yaml")
	content := `
name: deploy-app
rules:
  - name: on-push
    actions:
      - type: command
        cmd: echo deployed
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := loadRuleFile(path)
	if err != nil {
		t.Fatalf("valid rule file should load: %v", err)
	}
	if rc.Name != "deploy-app" {
		t.Errorf("expected name 'deploy-app', got '%s'", rc.Name)
	}
	if rc.FileName != "deploy" {
		t.Errorf("expected filename 'deploy', got '%s'", rc.FileName)
	}
	if len(rc.Rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rc.Rules))
	}
}

func TestLoadRuleFile_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	content := `
rules:
  - name: r1
    actions:
      - type: command
        cmd: echo x
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := loadRuleFile(path); err == nil {
		t.Error("rule file without name should fail")
	}
}
