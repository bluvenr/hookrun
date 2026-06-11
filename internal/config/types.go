package config

import "fmt"

// GlobalConfig represents the top-level config.yaml structure.
type GlobalConfig struct {
	Server    ServerConfig `yaml:"server"`
	Log       LogConfig    `yaml:"log"`
	ConfigDir string       `yaml:"config_dir"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port           int    `yaml:"port"`
	Route          string `yaml:"route"`
	AllowAll       *bool  `yaml:"allow_all,omitempty"`        // allow /webhook to iterate all configs (default: true)
	FirstMatchOnly *bool  `yaml:"first_match_only,omitempty"` // stop at first matching rule (default: true, only for allow_all)
}

// LogConfig holds logging settings.
type LogConfig struct {
	Path          string `yaml:"path"`
	RetentionDays int    `yaml:"retention_days"`
}

// RuleConfig represents a single hooks/*.yaml file structure.
type RuleConfig struct {
	Name      string           `yaml:"name"`
	Auth      *AuthConfig      `yaml:"auth,omitempty"`
	Execution *ExecutionConfig `yaml:"execution,omitempty"`
	Rules     []Rule           `yaml:"rules"`
	FilePath  string           `yaml:"-"` // source file path (not from YAML)
	FileName  string           `yaml:"-"` // file name without extension (used for routing)
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
	Filters   []Filter         `yaml:"filters"`
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

// Action represents a command or script to execute.
type Action struct {
	Type            string    `yaml:"type"`                // "command" | "script"
	Cmd             string    `yaml:"cmd,omitempty"`       // for type "command"
	Path            string    `yaml:"path,omitempty"`      // for type "script"
	Args            []string  `yaml:"args,omitempty"`      // for type "script"
	PassArgs        []PassArg `yaml:"pass_args,omitempty"` // extract from request and append as arguments
	Timeout         int       `yaml:"timeout,omitempty"`   // seconds, 0 = no limit
	Isolate         bool      `yaml:"isolate,omitempty"`
	ContinueOnError bool      `yaml:"continue_on_error,omitempty"`
}

// Defaults returns a GlobalConfig with default values applied.
func Defaults() GlobalConfig {
	return GlobalConfig{
		Server: ServerConfig{
			Port:  9000,
			Route: "/webhook",
		},
		Log: LogConfig{
			Path:          "./logs",
			RetentionDays: 30,
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
	// Default AllowAll to true
	if g.Server.AllowAll == nil {
		t := true
		g.Server.AllowAll = &t
	}
	// Default FirstMatchOnly to true
	if g.Server.FirstMatchOnly == nil {
		t := true
		g.Server.FirstMatchOnly = &t
	}
	if g.Log.Path == "" {
		g.Log.Path = "./logs"
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
		return true
	}
	return *s.AllowAll
}

// IsFirstMatchOnly returns whether to stop at the first matching rule.
func (s *ServerConfig) IsFirstMatchOnly() bool {
	if s.FirstMatchOnly == nil {
		return true
	}
	return *s.FirstMatchOnly
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

	if len(r.Filters) == 0 {
		return fmt.Errorf("%s '%s': at least one filter is required", prefix, r.Name)
	}

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

// Validate checks an Action.
func (a *Action) Validate(ruleName string, index int) error {
	prefix := fmt.Sprintf("rule '%s' action[%d]", ruleName, index)
	if a.Type != "command" && a.Type != "script" {
		return fmt.Errorf("%s: type must be 'command' or 'script', got '%s'", prefix, a.Type)
	}
	if a.Type == "command" && a.Cmd == "" {
		return fmt.Errorf("%s: 'cmd' is required for command type", prefix)
	}
	if a.Type == "script" && a.Path == "" {
		return fmt.Errorf("%s: 'path' is required for script type", prefix)
	}
	for i, pa := range a.PassArgs {
		if err := pa.Validate(ruleName, index, i); err != nil {
			return err
		}
	}
	return nil
}
