package engine

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/executor"
	"github.com/bluvenr/hookrun/internal/logger"
)

// Response is the standard JSON response structure.
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Config  string `json:"config,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Actions int    `json:"actions,omitempty"`
}

// RequestData holds parsed request information.
type RequestData struct {
	Headers   map[string]string
	Query     map[string]string
	Body      map[string]interface{}
	BodyRaw   string
	BodyBytes []byte // raw body bytes for HMAC signature verification
	IP        string
}

// Engine is the core webhook processing engine.
type Engine struct {
	mu      sync.RWMutex
	configs []*config.RuleConfig
	logger  logger.LogWriter
	// Global log settings for creating rule-level loggers
	logMode      string
	logRetention int
	logMaxSizeMB int
	// Rule-level logger cache (config name -> logger)
	ruleLoggers map[string]*logger.Logger
	// Execution state tracking
	running map[string]bool      // configName/ruleName -> running
	lastRun map[string]time.Time // configName/ruleName -> last start time
}

// New creates a new Engine instance.
func New(configs []*config.RuleConfig, log logger.LogWriter, logMode string, logRetention int, logMaxSizeMB int) *Engine {
	return &Engine{
		configs:      configs,
		logger:       log,
		logMode:      logMode,
		logRetention: logRetention,
		logMaxSizeMB: logMaxSizeMB,
		ruleLoggers:  make(map[string]*logger.Logger),
		running:      make(map[string]bool),
		lastRun:      make(map[string]time.Time),
	}
}

// UpdateConfigs replaces the engine's rule configs (for hot reload).
func (e *Engine) UpdateConfigs(configs []*config.RuleConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.configs = configs
}

// getRuleLogger returns (or creates) a rule-level logger for the given config.
func (e *Engine) getRuleLogger(cfg *config.RuleConfig) logger.LogWriter {
	if cfg.Log == nil || cfg.Log.Path == "" {
		return nil
	}

	// Check cache
	if rl, ok := e.ruleLoggers[cfg.Name]; ok {
		return logger.NewMulti(e.logger, rl)
	}

	// Create new rule-level logger
	rl := logger.NewRuleLogger(cfg.Log.Path, e.logMode, e.logRetention, e.logMaxSizeMB)
	e.ruleLoggers[cfg.Name] = rl
	return logger.NewMulti(e.logger, rl)
}

// CloseRuleLoggers closes all rule-level loggers.
func (e *Engine) CloseRuleLoggers() {
	for _, rl := range e.ruleLoggers {
		rl.Close()
	}
	e.ruleLoggers = make(map[string]*logger.Logger)
}

// Process handles an incoming webhook request by iterating all configs.
// Stops at the first matching rule (first match wins).
func (e *Engine) Process(req *RequestData) []Response {
	e.mu.RLock()
	configs := make([]*config.RuleConfig, len(e.configs))
	copy(configs, e.configs)
	e.mu.RUnlock()

	for _, cfg := range configs {
		resp := e.processConfig(cfg, req)
		if len(resp) > 0 {
			return resp // first match stops
		}
	}

	return []Response{{
		Code:    200,
		Message: "No matching rules",
	}}
}

// ProcessTargeted handles a webhook request for a specific config file (by filename).
func (e *Engine) ProcessTargeted(cfg *config.RuleConfig, req *RequestData) []Response {
	resp := e.processConfig(cfg, req)
	if len(resp) > 0 {
		return resp
	}
	return []Response{{
		Code:    200,
		Message: "No matching rules",
		Config:  cfg.Name,
	}}
}

// processConfig processes a single rule config file against the request.
// Returns on first matching rule (first match wins).
func (e *Engine) processConfig(cfg *config.RuleConfig, req *RequestData) []Response {
	// Use rule-level dual-write logger if configured, otherwise global logger
	log := e.logger
	if ruleLog := e.getRuleLogger(cfg); ruleLog != nil {
		log = ruleLog
	}

	// Step 1: Auth check (AND relationship)
	if cfg.Auth != nil {
		if !e.checkAuth(cfg.Auth, req) {
			log.Warn("Auth failed for config '%s' from IP %s", cfg.Name, req.IP)
			return []Response{{
				Code:    401,
				Message: "Authentication failed",
				Config:  cfg.Name,
			}}
		}
	}

	// Step 1.5: Check file-level filters (AND with rule-level, short-circuit)
	if len(cfg.Filters) > 0 && !e.matchFilters(cfg.Filters, req) {
		return nil // file-level filters failed, skip all rules in this config
	}

	for _, rule := range cfg.Rules {
		// Step 2: Match rule-level filters (AND relationship, empty = catch-all)
		if !e.matchFilters(rule.Filters, req) {
			continue
		}

		// Step 3: Check execution policy
		taskKey := cfg.Name + "/" + rule.Name
		policy := e.resolvePolicy(cfg.Execution, rule.Execution)

		if blocked := e.checkPolicy(taskKey, policy); blocked != nil {
			blocked.Config = cfg.Name
			blocked.Rule = rule.Name
			log.Info("Task '%s' blocked: %s", taskKey, blocked.Message)
			return []Response{*blocked}
		}

		// Step 4: Execute actions
		log.Info("Rule '%s' matched, executing %d actions", taskKey, len(rule.Actions))
		e.markRunning(taskKey)
		actionCount := e.executeActions(taskKey, rule.Actions, req, log)
		e.markDone(taskKey)

		return []Response{{
			Code:    200,
			Message: "ok",
			Config:  cfg.Name,
			Rule:    rule.Name,
			Actions: actionCount,
		}}
	}

	return nil // no matching rule in this config
}

// checkAuth validates authentication (AND relationship between token, HMAC, and IP whitelist).
func (e *Engine) checkAuth(auth *config.AuthConfig, req *RequestData) bool {
	// Check token if configured
	if auth.Token != nil {
		if !e.checkToken(auth.Token, req) {
			return false
		}
	}

	// Check HMAC signature if configured
	if auth.HMAC != nil {
		if !e.checkHMAC(auth.HMAC, req) {
			return false
		}
	}

	// Check IP whitelist if configured
	if len(auth.IPWhitelist) > 0 {
		if !e.checkIPWhitelist(auth.IPWhitelist, req.IP) {
			return false
		}
	}

	return true
}

// checkToken validates the token from header or query.
func (e *Engine) checkToken(token *config.TokenConfig, req *RequestData) bool {
	var actual string
	switch token.Source {
	case "header":
		actual = req.Headers[strings.ToLower(token.Key)]
	case "query":
		actual = req.Query[token.Key]
	default:
		return false
	}
	return actual == token.Value
}

// checkHMAC validates the HMAC signature from the request header.
// Supports GitHub (X-Hub-Signature-256, sha256=hex), GitLab, and other formats.
func (e *Engine) checkHMAC(cfg *config.HMACConfig, req *RequestData) bool {
	// Get the signature header value (lowercase key)
	sigHeader := req.Headers[strings.ToLower(cfg.Header)]
	if sigHeader == "" {
		e.logger.Warn("HMAC verification failed: header '%s' is missing or empty", cfg.Header)
		return false
	}

	// Strip prefix from signature (e.g. "sha256=abc123" -> "abc123")
	expectedHex := sigHeader
	if cfg.Prefix != "" && strings.HasPrefix(sigHeader, cfg.Prefix) {
		expectedHex = strings.TrimPrefix(sigHeader, cfg.Prefix)
	}

	// Decode the expected signature from hex
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		e.logger.Warn("HMAC verification failed: invalid hex in signature header")
		return false
	}

	// Select hash function
	var newHash func() hash.Hash
	switch cfg.Algorithm {
	case "sha1":
		newHash = sha1.New
	case "sha512":
		newHash = sha512.New
	case "sha256":
		newHash = sha256.New
	default:
		newHash = sha256.New
	}

	// Compute HMAC over raw body bytes
	mac := hmac.New(newHash, []byte(cfg.Secret))
	mac.Write(req.BodyBytes)
	actual := mac.Sum(nil)

	// Constant-time comparison to prevent timing attacks
	if !hmac.Equal(actual, expected) {
		e.logger.Warn("HMAC verification failed: signature mismatch")
		return false
	}

	return true
}

// checkIPWhitelist checks if the request IP is in the whitelist (supports CIDR).
func (e *Engine) checkIPWhitelist(whitelist []string, ip string) bool {
	// Strip port from IP if present
	host := ip
	if h, _, err := net.SplitHostPort(ip); err == nil {
		host = h
	}

	parsedIP := net.ParseIP(host)
	if parsedIP == nil {
		return false
	}

	for _, entry := range whitelist {
		// Check CIDR
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err != nil {
				continue
			}
			if cidr.Contains(parsedIP) {
				return true
			}
		} else {
			// Exact IP match
			if host == entry {
				return true
			}
		}
	}

	return false
}

// matchFilters checks if all filters match (AND relationship).
func (e *Engine) matchFilters(filters []config.Filter, req *RequestData) bool {
	for _, f := range filters {
		if !e.matchFilter(f, req) {
			return false
		}
	}
	return true
}

// matchFilter checks a single filter against the request.
func (e *Engine) matchFilter(f config.Filter, req *RequestData) bool {
	var actual string

	switch f.Type {
	case "header":
		actual = req.Headers[strings.ToLower(f.Key)]
	case "query":
		actual = req.Query[f.Key]
	case "body":
		actual = e.extractBodyValue(req.Body, f.Key)
	default:
		return false
	}

	switch f.Operator {
	case "eq":
		return actual == f.Value
	case "ne":
		return actual != f.Value
	case "contains":
		return strings.Contains(actual, f.Value)
	case "regex":
		matched, err := regexp.MatchString(f.Value, actual)
		return err == nil && matched
	default:
		return false
	}
}

// extractBodyValue extracts a value from the JSON body using a simple path.
// Supports dot notation: "commits[0].message", "ref", "repository.owner.name"
func (e *Engine) extractBodyValue(body map[string]interface{}, path string) string {
	if body == nil {
		return ""
	}

	parts := parseJSONPath(path)
	var current interface{} = body

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part.key]
			if !ok {
				return ""
			}
			current = val
		default:
			return ""
		}
		// Handle array index
		if part.index >= 0 {
			if arr, ok := current.([]interface{}); ok && part.index < len(arr) {
				current = arr[part.index]
			} else {
				return ""
			}
		}
	}

	// Convert to string
	switch v := current.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// pathPart represents a segment of a JSON path.
type pathPart struct {
	key   string
	index int // -1 means no array index
}

// parseJSONPath parses a JSON path like "commits[0].message" into parts.
func parseJSONPath(path string) []pathPart {
	var parts []pathPart
	segments := strings.Split(path, ".")

	arrayRe := regexp.MustCompile(`^(\w+)\[(\d+)\]$`)

	for _, seg := range segments {
		if matches := arrayRe.FindStringSubmatch(seg); matches != nil {
			idx, _ := strconv.Atoi(matches[2])
			parts = append(parts, pathPart{key: matches[1], index: idx})
		} else {
			parts = append(parts, pathPart{key: seg, index: -1})
		}
	}

	return parts
}

// resolvePolicy determines the effective execution policy for a rule.
// Priority: rule-level > file-level > default (block)
func (e *Engine) resolvePolicy(fileLevel, ruleLevel *config.ExecutionConfig) config.ExecutionConfig {
	if ruleLevel != nil {
		return *ruleLevel
	}
	if fileLevel != nil {
		return *fileLevel
	}
	return config.ExecutionConfig{Policy: "block"}
}

// checkPolicy checks if execution is allowed under the given policy.
// Returns nil if allowed, or a Response if blocked.
func (e *Engine) checkPolicy(taskKey string, policy config.ExecutionConfig) *Response {
	e.mu.RLock()
	running := e.running[taskKey]
	lastRun := e.lastRun[taskKey]
	e.mu.RUnlock()

	switch policy.Policy {
	case "always":
		return nil
	case "block":
		if running {
			return &Response{
				Code:    409,
				Message: fmt.Sprintf("Task '%s' is running, please try again later", taskKey),
			}
		}
	case "cooldown":
		if running {
			elapsed := time.Since(lastRun)
			remaining := time.Duration(policy.CooldownSeconds)*time.Second - elapsed
			if remaining > 0 {
				return &Response{
					Code:    429,
					Message: fmt.Sprintf("Task '%s' is in cooldown, retry in %d seconds", taskKey, int(remaining.Seconds())+1),
				}
			}
		}
	}

	return nil
}

// markRunning marks a task as running.
func (e *Engine) markRunning(taskKey string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running[taskKey] = true
	e.lastRun[taskKey] = time.Now()
}

// markDone marks a task as no longer running.
func (e *Engine) markDone(taskKey string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running[taskKey] = false
}

// executeActions runs all actions for a rule sequentially.
// Returns the number of successfully completed actions.
func (e *Engine) executeActions(taskKey string, actions []config.Action, req *RequestData, log logger.LogWriter) int {
	completed := 0

	// Extract config/rule names from taskKey ("configName/ruleName")
	parts := strings.SplitN(taskKey, "/", 2)
	configName, ruleName := "", ""
	if len(parts) == 2 {
		configName, ruleName = parts[0], parts[1]
	}

	for i, action := range actions {
		log.Info("[%s] Executing action %d/%d: type=%s", taskKey, i+1, len(actions), action.Type)

		var result *executor.ActionResult

		switch action.Type {
		case "command":
			resolvedCmd := e.resolveActionTemplates(action.Cmd, req, action.PassArgs)
			result = executor.ExecuteCommand(resolvedCmd, action.Timeout, action.Isolate)
		case "script":
			resolvedPath := e.resolveActionTemplates(action.Path, req, nil)
			var resolvedArgs []string
			for _, arg := range action.Args {
				resolvedArgs = append(resolvedArgs, e.resolveActionTemplates(arg, req, nil))
			}
			result = executor.ExecuteScript(resolvedPath, resolvedArgs, action.Timeout, action.Isolate)
		case "webhook":
			whResult := e.executeWebhook(&action, req, configName, ruleName, log)
			log.Info("[%s] Webhook %s %s -> HTTP %d (%v)", taskKey, action.Method, action.URL, whResult.StatusCode, whResult.Duration)
			result = whResult.toActionResult()
		default:
			log.Error("[%s] Unknown action type: %s", taskKey, action.Type)
			continue
		}

		// Log result
		if result.Success() {
			log.Info("[%s] Action %d/%d completed in %v", taskKey, i+1, len(actions), result.Duration)
			completed++
		} else {
			log.Error("[%s] Action %d/%d failed (code=%d, duration=%v): %v",
				taskKey, i+1, len(actions), result.ExitCode, result.Duration, result.Error)
			if result.Stderr != "" {
				log.Error("[%s] response: %s", taskKey, result.Stderr)
			}
			if !action.ContinueOnError {
				log.Warn("[%s] Stopping execution due to action failure (continue_on_error=false)", taskKey)
				break
			}
		}

		if result.Stdout != "" {
			log.Debug("[%s] stdout: %s", taskKey, result.Stdout)
		}
	}

	return completed
}

// resolveActionTemplates resolves template variables in a string and appends pass_args.
// Template syntax:
//
//	{{.body.<path>}}    - extract from JSON body (supports dot notation and array index)
//	{{.header.<name>}}  - extract from request header
//	{{.query.<name>}}   - extract from query parameter
func (e *Engine) resolveActionTemplates(tmpl string, req *RequestData, passArgs []config.PassArg) string {
	result := tmpl

	// 1. Resolve {{.body.xxx}} templates
	bodyRe := regexp.MustCompile(`\{\{\s*\.body\.([^}\s]+)\s*\}\}`)
	result = bodyRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := bodyRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		val := e.extractBodyValue(req.Body, sub[1])
		if val == "" {
			e.logger.Warn("Template variable not found: %s", match)
		}
		return val
	})

	// 2. Resolve {{.header.xxx}} templates
	headerRe := regexp.MustCompile(`\{\{\s*\.header\.([^}\s]+)\s*\}\}`)
	result = headerRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := headerRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		key := strings.ToLower(sub[1])
		val := req.Headers[key]
		if val == "" {
			e.logger.Warn("Template variable not found: %s", match)
		}
		return val
	})

	// 3. Resolve {{.query.xxx}} templates
	queryRe := regexp.MustCompile(`\{\{\s*\.query\.([^}\s]+)\s*\}\}`)
	result = queryRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := queryRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		val := req.Query[sub[1]]
		if val == "" {
			e.logger.Warn("Template variable not found: %s", match)
		}
		return val
	})

	// 4. Append pass_args as trailing arguments
	if len(passArgs) > 0 {
		for _, pa := range passArgs {
			val := e.extractPassArgValue(&pa, req)
			if result != "" {
				result += " " + val
			} else {
				result = val
			}
		}
	}

	return result
}

// extractPassArgValue extracts a value from the request based on PassArg config.
func (e *Engine) extractPassArgValue(pa *config.PassArg, req *RequestData) string {
	switch pa.Source {
	case "header":
		return req.Headers[strings.ToLower(pa.Key)]
	case "query":
		return req.Query[pa.Key]
	case "body":
		return e.extractBodyValue(req.Body, pa.Key)
	default:
		return ""
	}
}

// ParseRequest extracts RequestData from an HTTP request.
// Returns an error if the request body exceeds the configured size limit.
func ParseRequest(r *http.Request) (*RequestData, error) {
	data := &RequestData{
		Headers: make(map[string]string),
		Query:   make(map[string]string),
	}

	// Extract headers (lowercase keys)
	for key := range r.Header {
		data.Headers[strings.ToLower(key)] = r.Header.Get(key)
	}

	// Extract query parameters
	for key := range r.URL.Query() {
		data.Query[key] = r.URL.Query().Get(key)
	}

	// Extract client IP (supports proxy headers)
	data.IP = r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		data.IP = strings.Split(xff, ",")[0]
	} else if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		data.IP = xri
	}

	// Read raw body bytes (needed for HMAC signature verification)
	if r.Body != nil {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			return data, err
		}
		data.BodyBytes = rawBody
		// Parse body as JSON from raw bytes
		var body map[string]interface{}
		if err := json.Unmarshal(rawBody, &body); err == nil {
			data.Body = body
		}
	}

	return data, nil
}
