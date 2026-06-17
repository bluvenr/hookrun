package config

import (
	"fmt"
	"strings"
)

// GlobalConfig represents the top-level config.yaml structure.
type GlobalConfig struct {
	Server    ServerConfig `yaml:"server"`
	Log       LogConfig    `yaml:"log"`
	ConfigDir string       `yaml:"config_dir"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port          int    `yaml:"port"`
	Route         string `yaml:"route"`
	AllowAll      *bool  `yaml:"allow_all,omitempty"`        // allow /webhook to iterate all configs (default: false)
	MaxBodySizeMB *int   `yaml:"max_body_size_mb,omitempty"` // max request body in MB, 0 = unlimited (default: 10)
}

// LogConfig holds logging settings.
type LogConfig struct {
	Mode          string `yaml:"mode"` // "daily" (default) | "single"
	Path          string `yaml:"path"`
	RetentionDays int    `yaml:"retention_days"` // only for daily mode
	MaxSizeMB     int    `yaml:"max_size_mb"`    // only for single mode, 0 = unlimited (default)
}

// RuleLogConfig holds per-rule-file logging settings.
type RuleLogConfig struct {
	Path string `yaml:"path"` // independent log file path
}

// RuleConfig represents a single hooks/*.yaml file structure.
type RuleConfig struct {
	Name        string             `yaml:"name"`
	Auth        *AuthConfig        `yaml:"auth,omitempty"`
	Execution   *ExecutionConfig   `yaml:"execution,omitempty"`
	Filters     []Filter           `yaml:"filters,omitempty"`     // file-level global filters (AND with rule-level)
	Log         *RuleLogConfig     `yaml:"log,omitempty"`         // rule-level independent log file
	Deduplicate *DeduplicateConfig `yaml:"deduplicate,omitempty"` // file-level deduplication settings
	Rules       []Rule             `yaml:"rules"`
	FilePath    string             `yaml:"-"` // source file path (not from YAML)
	FileName    string             `yaml:"-"` // file name without extension (used for routing)
}

// AuthConfig holds authentication settings (AND relationship).
type AuthConfig struct {
	Token       *TokenConfig `yaml:"token,omitempty"`
	HMAC        *HMACConfig  `yaml:"hmac,omitempty"`
	IPWhitelist []string     `yaml:"ip_whitelist,omitempty"`
}

// TokenConfig holds token-based authentication settings.
type TokenConfig struct {
	Source string `yaml:"source"` // "header" or "query"
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
}

// HMACConfig holds HMAC signature verification settings.
// Supports GitHub (X-Hub-Signature-256, sha256), GitLab, and other platforms.
type HMACConfig struct {
	Header    string `yaml:"header"`    // signature header name, e.g. "X-Hub-Signature-256"
	Secret    string `yaml:"secret"`    // HMAC secret key
	Algorithm string `yaml:"algorithm"` // "sha256" (default) | "sha1" | "sha512"
	Prefix    string `yaml:"prefix"`    // signature prefix, e.g. "sha256=" (auto-derived from algorithm if empty)
}

// ExecutionConfig holds execution policy settings.
type ExecutionConfig struct {
	Policy          string `yaml:"policy"`           // "block" | "always" | "cooldown"
	CooldownSeconds int    `yaml:"cooldown_seconds"` // only for "cooldown" policy
}

// Rule represents a single rule with filters and actions.
type Rule struct {
	Name      string           `yaml:"name"`
	Execution *ExecutionConfig `yaml:"execution,omitempty"`
	Filters   []Filter         `yaml:"filters,omitempty"`
	Actions   []Action         `yaml:"actions"`
}

// Filter represents a single matching condition.
type Filter struct {
	Type     string `yaml:"type"` // "header" | "query" | "body"
	Key      string `yaml:"key"`
	Operator string `yaml:"operator"` // "eq" | "ne" | "contains" | "regex"
	Value    string `yaml:"value"`
}

// PassArg defines a parameter to extract from the request and pass to the action.
type PassArg struct {
	Source string `yaml:"source"` // "header" | "query" | "body"
	Key    string `yaml:"key"`    // field name or JSON path for body
}

// EnvSource defines a parameter to extract from the request and inject as an environment variable.
type EnvSource struct {
	Source string `yaml:"source"` // "header" | "query" | "body"
	Key    string `yaml:"key"`    // field name or JSON path for body
	Env    string `yaml:"env"`    // env var suffix (HOOKRUN_ prefix auto-applied)
}

// RetryConfig holds retry settings for an action.
type RetryConfig struct {
	MaxAttempts     int `yaml:"max_attempts"`     // total attempts including first, must >= 1
	IntervalSeconds int `yaml:"interval_seconds"` // base interval in seconds, must >= 0
}

// RelayTarget represents a single relay destination.
type RelayTarget struct {
	URL   string `yaml:"url"`   // target URL (e.g. http://10.0.0.2:9000/webhook/deploy-app)
	Token string `yaml:"token"` // auth token injected as X-HookRun-Relay-Token header
}

// RelayConfig holds relay action settings for forwarding webhooks to other HookRun instances.
type RelayConfig struct {
	Targets        []RelayTarget `yaml:"targets"`                   // destination list
	ForwardHeaders []string      `yaml:"forward_headers,omitempty"` // whitelist of original headers to forward
	MaxRelayHops   int           `yaml:"max_relay_hops,omitempty"`  // anti-loop limit (default 3)
	Timeout        int           `yaml:"timeout,omitempty"`         // per-target timeout in seconds
}

// DeduplicateConfig holds deduplication settings for preventing duplicate webhook execution.
type DeduplicateConfig struct {
	Enabled       bool `yaml:"enabled"`        // enable deduplication
	WindowSeconds int  `yaml:"window_seconds"` // dedup window in seconds (default 300)
}

// Action represents a command, script, or webhook to execute.
type Action struct {
	Type            string       `yaml:"type"`                // "command" | "script" | "webhook" | "relay"
	Cmd             string       `yaml:"cmd,omitempty"`       // for type "command"
	Path            string       `yaml:"path,omitempty"`      // for type "script"
	Args            []string     `yaml:"args,omitempty"`      // for type "script"
	PassArgs        []PassArg    `yaml:"pass_args,omitempty"` // extract from request and append as arguments
	EnvFrom         []EnvSource  `yaml:"env_from,omitempty"`  // extract from request and inject as env vars
	Timeout         int          `yaml:"timeout,omitempty"`   // seconds, 0 = no limit
	Isolate         bool         `yaml:"isolate,omitempty"`
	ContinueOnError bool         `yaml:"continue_on_error,omitempty"`
	Retry           *RetryConfig `yaml:"retry,omitempty"` // retry settings (nil = no retry)
	Relay           *RelayConfig `yaml:"relay,omitempty"` // relay action config
	// Webhook-specific fields
	URL            string            `yaml:"url,omitempty"`             // target URL (supports templates)
	Method         string            `yaml:"method,omitempty"`          // HTTP method, default "POST"
	Headers        map[string]string `yaml:"headers,omitempty"`         // custom headers (supports templates)
	ForwardHeaders []string          `yaml:"forward_headers,omitempty"` // whitelist of original headers to forward
	Body           string            `yaml:"body,omitempty"`            // request body template (supports {{.raw_body}})
}

// Defaults returns a GlobalConfig with default values applied.
func Defaults() GlobalConfig {
	return GlobalConfig{
		Server: ServerConfig{
			Port:  9000,
			Route: "/webhook",
		},
		Log: LogConfig{
			Mode:          "daily",
			Path:          "./logs",
			RetentionDays: 30,
			MaxSizeMB:     0,
		},
		ConfigDir: "./hooks",
	}
}

// Validate checks the GlobalConfig for errors.
func (g *GlobalConfig) Validate() error {
	if g.Server.Port < 1 || g.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", g.Server.Port)
	}
	if g.Server.Route == "" {
		g.Server.Route = "/webhook"
	}
	// Default AllowAll to false
	if g.Server.AllowAll == nil {
		f := false
		g.Server.AllowAll = &f
	}
	// Default MaxBodySizeMB to 10
	if g.Server.MaxBodySizeMB == nil {
		v := 10
		g.Server.MaxBodySizeMB = &v
	}
	if *g.Server.MaxBodySizeMB < 0 {
		return fmt.Errorf("server.max_body_size_mb must be >= 0, got %d", *g.Server.MaxBodySizeMB)
	}
	if g.Log.Path == "" {
		g.Log.Path = "./logs"
	}
	if g.Log.Mode == "" {
		g.Log.Mode = "daily"
	}
	if g.Log.Mode != "daily" && g.Log.Mode != "single" {
		return fmt.Errorf("log.mode must be 'daily' or 'single', got '%s'", g.Log.Mode)
	}
	if g.Log.RetentionDays <= 0 {
		g.Log.RetentionDays = 30
	}
	if g.ConfigDir == "" {
		g.ConfigDir = "./hooks"
	}
	return nil
}

// IsAllowAll returns whether the base route allows iterating all configs.
func (s *ServerConfig) IsAllowAll() bool {
	if s.AllowAll == nil {
		return false
	}
	return *s.AllowAll
}

// Validate checks the RuleConfig for errors.
func (r *RuleConfig) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("rule config file '%s': 'name' is required", r.FilePath)
	}

	// Validate auth
	if r.Auth != nil {
		if err := r.Auth.Validate(r.Name); err != nil {
			return err
		}
	}

	// Validate file-level execution
	if r.Execution != nil {
		if err := r.Execution.Validate(r.Name); err != nil {
			return err
		}
	}

	// Validate rule-level log
	if r.Log != nil {
		if r.Log.Path == "" {
			return fmt.Errorf("config '%s': log.path is required when log is configured", r.Name)
		}
	}

	// Validate file-level deduplicate
	if r.Deduplicate != nil {
		if err := r.Deduplicate.Validate(r.Name); err != nil {
			return err
		}
	}

	// Validate file-level filters
	for i, f := range r.Filters {
		if err := f.Validate(r.Name+" (file-level)", i); err != nil {
			return err
		}
	}

	if len(r.Rules) == 0 {
		return fmt.Errorf("rule config '%s': at least one rule is required", r.Name)
	}

	for i, rule := range r.Rules {
		if err := rule.Validate(r.Name, i); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks AuthConfig.
func (a *AuthConfig) Validate(configName string) error {
	if a.Token != nil {
		if a.Token.Source != "header" && a.Token.Source != "query" {
			return fmt.Errorf("config '%s': auth.token.source must be 'header' or 'query', got '%s'", configName, a.Token.Source)
		}
		if a.Token.Key == "" {
			return fmt.Errorf("config '%s': auth.token.key is required", configName)
		}
		if a.Token.Value == "" {
			return fmt.Errorf("config '%s': auth.token.value is required", configName)
		}
	}
	if a.HMAC != nil {
		if a.HMAC.Header == "" {
			return fmt.Errorf("config '%s': auth.hmac.header is required", configName)
		}
		if a.HMAC.Secret == "" {
			return fmt.Errorf("config '%s': auth.hmac.secret is required", configName)
		}
		validAlgos := map[string]bool{"sha256": true, "sha1": true, "sha512": true, "": true}
		if !validAlgos[a.HMAC.Algorithm] {
			return fmt.Errorf("config '%s': auth.hmac.algorithm must be 'sha256', 'sha1', or 'sha512', got '%s'", configName, a.HMAC.Algorithm)
		}
		// Apply defaults
		if a.HMAC.Algorithm == "" {
			a.HMAC.Algorithm = "sha256"
		}
		if a.HMAC.Prefix == "" {
			a.HMAC.Prefix = a.HMAC.Algorithm + "="
		}
	}
	return nil
}

// Validate checks ExecutionConfig.
func (e *ExecutionConfig) Validate(configName string) error {
	validPolicies := map[string]bool{"block": true, "always": true, "cooldown": true}
	if !validPolicies[e.Policy] {
		return fmt.Errorf("config '%s': execution.policy must be 'block', 'always', or 'cooldown', got '%s'", configName, e.Policy)
	}
	if e.Policy == "cooldown" && e.CooldownSeconds <= 0 {
		return fmt.Errorf("config '%s': cooldown policy requires cooldown_seconds > 0", configName)
	}
	return nil
}

// Validate checks a Rule.
func (r *Rule) Validate(configName string, index int) error {
	prefix := fmt.Sprintf("config '%s' rule[%d]", configName, index)
	if r.Name == "" {
		return fmt.Errorf("%s: 'name' is required", prefix)
	}

	// Validate rule-level execution
	if r.Execution != nil {
		if err := r.Execution.Validate(configName + "/" + r.Name); err != nil {
			return err
		}
	}

	// Filters are optional — zero filters means catch-all (matches everything)
	for i, f := range r.Filters {
		if err := f.Validate(r.Name, i); err != nil {
			return err
		}
	}

	if len(r.Actions) == 0 {
		return fmt.Errorf("%s '%s': at least one action is required", prefix, r.Name)
	}

	for i, a := range r.Actions {
		if err := a.Validate(r.Name, i); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks a Filter.
func (f *Filter) Validate(ruleName string, index int) error {
	prefix := fmt.Sprintf("rule '%s' filter[%d]", ruleName, index)
	validTypes := map[string]bool{"header": true, "query": true, "body": true}
	if !validTypes[f.Type] {
		return fmt.Errorf("%s: type must be 'header', 'query', or 'body', got '%s'", prefix, f.Type)
	}
	if f.Key == "" {
		return fmt.Errorf("%s: 'key' is required", prefix)
	}
	validOps := map[string]bool{"eq": true, "ne": true, "contains": true, "regex": true}
	if !validOps[f.Operator] {
		return fmt.Errorf("%s: operator must be 'eq', 'ne', 'contains', or 'regex', got '%s'", prefix, f.Operator)
	}
	if f.Value == "" {
		return fmt.Errorf("%s: 'value' is required", prefix)
	}
	return nil
}

// Validate checks a PassArg.
func (p *PassArg) Validate(ruleName string, actionIndex, argIndex int) error {
	prefix := fmt.Sprintf("rule '%s' action[%d] pass_args[%d]", ruleName, actionIndex, argIndex)
	validSources := map[string]bool{"header": true, "query": true, "body": true}
	if !validSources[p.Source] {
		return fmt.Errorf("%s: source must be 'header', 'query', or 'body', got '%s'", prefix, p.Source)
	}
	if p.Key == "" {
		return fmt.Errorf("%s: 'key' is required", prefix)
	}
	return nil
}

// Validate checks an EnvSource.
func (es *EnvSource) Validate(ruleName string, actionIndex, envIndex int) error {
	prefix := fmt.Sprintf("rule '%s' action[%d] env_from[%d]", ruleName, actionIndex, envIndex)
	validSources := map[string]bool{"header": true, "query": true, "body": true}
	if !validSources[es.Source] {
		return fmt.Errorf("%s: source must be 'header', 'query', or 'body', got '%s'", prefix, es.Source)
	}
	if es.Key == "" {
		return fmt.Errorf("%s: 'key' is required", prefix)
	}
	if es.Env == "" {
		return fmt.Errorf("%s: 'env' is required", prefix)
	}
	return nil
}

// Validate checks a RelayConfig.
func (r *RelayConfig) Validate(prefix string) error {
	if len(r.Targets) == 0 {
		return fmt.Errorf("%s: 'targets' must not be empty", prefix)
	}
	for i, t := range r.Targets {
		if err := t.Validate(prefix, i); err != nil {
			return err
		}
	}
	if r.MaxRelayHops < 0 {
		return fmt.Errorf("%s: 'max_relay_hops' must be >= 0, got %d", prefix, r.MaxRelayHops)
	}
	return nil
}

// Validate checks a RelayTarget.
func (t *RelayTarget) Validate(prefix string, index int) error {
	if t.URL == "" {
		return fmt.Errorf("%s: targets[%d].url is required", prefix, index)
	}
	return nil
}

// Validate checks a DeduplicateConfig.
func (d *DeduplicateConfig) Validate(configName string) error {
	if d.Enabled && d.WindowSeconds <= 0 {
		return fmt.Errorf("config '%s': deduplicate.window_seconds must be > 0 when enabled, got %d", configName, d.WindowSeconds)
	}
	return nil
}

// Validate checks an Action.
func (a *Action) Validate(ruleName string, index int) error {
	prefix := fmt.Sprintf("rule '%s' action[%d]", ruleName, index)
	switch a.Type {
	case "command":
		if a.Cmd == "" {
			return fmt.Errorf("%s: 'cmd' is required for command type", prefix)
		}
	case "script":
		if a.Path == "" {
			return fmt.Errorf("%s: 'path' is required for script type", prefix)
		}
	case "relay":
		if a.Relay == nil {
			return fmt.Errorf("%s: 'relay' config is required for relay type", prefix)
		}
		if err := a.Relay.Validate(prefix); err != nil {
			return err
		}
	case "webhook":
		if a.URL == "" {
			return fmt.Errorf("%s: 'url' is required for webhook type", prefix)
		}
		if a.Method == "" {
			a.Method = "POST"
		}
		validMethods := map[string]bool{"POST": true, "PUT": true, "PATCH": true, "GET": true}
		if !validMethods[strings.ToUpper(a.Method)] {
			return fmt.Errorf("%s: webhook method must be POST, PUT, PATCH, or GET, got '%s'", prefix, a.Method)
		}
		a.Method = strings.ToUpper(a.Method)
	default:
		return fmt.Errorf("%s: type must be 'command', 'script', 'webhook', or 'relay', got '%s'", prefix, a.Type)
	}
	for i, pa := range a.PassArgs {
		if err := pa.Validate(ruleName, index, i); err != nil {
			return err
		}
	}
	for i, es := range a.EnvFrom {
		if err := es.Validate(ruleName, index, i); err != nil {
			return err
		}
	}
	if a.Retry != nil {
		if a.Retry.MaxAttempts < 1 {
			return fmt.Errorf("%s: retry.max_attempts must be >= 1, got %d", prefix, a.Retry.MaxAttempts)
		}
		if a.Retry.IntervalSeconds < 0 {
			return fmt.Errorf("%s: retry.interval_seconds must be >= 0, got %d", prefix, a.Retry.IntervalSeconds)
		}
	}
	return nil
}
