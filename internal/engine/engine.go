package engine

import (
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/execstore"
	"github.com/bluvenr/hookrun/internal/executor"
	"github.com/bluvenr/hookrun/internal/logger"
)

// Precompiled template patterns (compiled once, reused per request).
var (
	tmplRawBodyRe = regexp.MustCompile(`\{\{\s*\.raw_body\s*\}\}`)
	tmplBodyRe    = regexp.MustCompile(`\{\{\s*\.body\.([^}\s]+)\s*\}\}`)
	tmplHeaderRe  = regexp.MustCompile(`\{\{\s*\.header\.([^}\s]+)\s*\}\}`)
	tmplQueryRe   = regexp.MustCompile(`\{\{\s*\.query\.([^}\s]+)\s*\}\}`)
	arrayIndexRe  = regexp.MustCompile(`^(\w+)\[(\d+)\]$`)
)

// regexCache caches compiled filter regexes (pattern -> *regexp.Regexp).
var regexCache sync.Map

// compileCached returns a compiled regex for the pattern, reusing cached results.
func compileCached(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// Response is the standard JSON response structure.
type Response struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Config    string `json:"config,omitempty"`
	Rule      string `json:"rule,omitempty"`
	Actions   int    `json:"actions,omitempty"`
	RequestID string `json:"request_id,omitempty"` // set for 202 async responses
}

// RequestData holds parsed request information.
type RequestData struct {
	Headers   map[string]string
	Query     map[string]string
	Body      map[string]interface{}
	BodyRaw   string
	BodyBytes []byte // raw body bytes for HMAC signature verification
	IP        string
	RequestID string // unique request identifier (generated or inherited from relay)
	RelayHops int    // current relay hop count (parsed from X-HookRun-Relay-Hops)
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
	// Deduplication cache for relay idempotency
	dedup     *dedupCache
	dedupStop chan struct{}
	// Relay target registry for dynamic discovery (nil when not enabled)
	registry     *TargetRegistry
	registryStop chan struct{}
	// Async execution control
	asyncSem  chan struct{}    // concurrency semaphore for background tasks
	asyncWG   sync.WaitGroup   // tracks in-flight background tasks
	execStore *execstore.Store // execution records for /api/executions
	// Guard against double-stop (channel close panic)
	stoppedMu sync.Mutex
	stopped   bool
}

// New creates a new Engine instance.
func New(configs []*config.RuleConfig, log logger.LogWriter, logMode string, logRetention int, logMaxSizeMB int, maxAsyncTasks int) *Engine {
	dedup := newDedupCache()
	stop := make(chan struct{})
	dedup.startCleanupLoop(60*time.Second, stop)

	if maxAsyncTasks <= 0 {
		maxAsyncTasks = 32
	}

	return &Engine{
		configs:      configs,
		logger:       log,
		logMode:      logMode,
		logRetention: logRetention,
		logMaxSizeMB: logMaxSizeMB,
		ruleLoggers:  make(map[string]*logger.Logger),
		running:      make(map[string]bool),
		lastRun:      make(map[string]time.Time),
		dedup:        dedup,
		dedupStop:    stop,
		asyncSem:     make(chan struct{}, maxAsyncTasks),
		execStore:    execstore.NewStore(execstore.DefaultCapacity),
	}
}

// ExecStore returns the async execution record store.
func (e *Engine) ExecStore() *execstore.Store {
	return e.execStore
}

// WaitForAsync blocks until all background async tasks finish or the timeout
// elapses, whichever comes first. Used by graceful shutdown so in-flight
// tasks get a grace period before the process exits.
func (e *Engine) WaitForAsync(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		e.asyncWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		e.logger.Warn("Shutdown: async tasks still running after %v, forcing exit", timeout)
	}
}

// UpdateConfigs replaces the engine's rule configs (for hot reload).
func (e *Engine) UpdateConfigs(configs []*config.RuleConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.configs = configs
}

// Stop gracefully shuts down the engine, stopping background goroutines.
// Safe to call multiple times — subsequent calls are no-ops.
func (e *Engine) Stop() {
	e.stoppedMu.Lock()
	if e.stopped {
		e.stoppedMu.Unlock()
		return
	}
	e.stopped = true
	e.stoppedMu.Unlock()

	if e.dedupStop != nil {
		close(e.dedupStop)
	}
	if e.registryStop != nil {
		close(e.registryStop)
	}
}

// SetRegistry sets the target registry for dynamic relay discovery.
func (e *Engine) SetRegistry(reg *TargetRegistry) {
	e.registry = reg
	e.registryStop = make(chan struct{})
	reg.StartCleanupLoop(30*time.Second, e.registryStop)
}

// Registry returns the target registry (nil when not enabled).
func (e *Engine) Registry() *TargetRegistry {
	return e.registry
}

// getRuleLogger returns (or creates) a rule-level logger for the given config.
// Access to the ruleLoggers cache is guarded by e.mu because reload may
// close and replace cached loggers concurrently with request handling.
func (e *Engine) getRuleLogger(cfg *config.RuleConfig) logger.LogWriter {
	if cfg.Log == nil || cfg.Log.Path == "" {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

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
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rl := range e.ruleLoggers {
		rl.Close()
	}
	e.ruleLoggers = make(map[string]*logger.Logger)
}

// Process handles an incoming webhook request by iterating all configs.
// Stops at the first matching rule (first match wins). Configs whose auth
// check fails are skipped so they cannot block other configs in the chain.
func (e *Engine) Process(req *RequestData) []Response {
	e.mu.RLock()
	configs := make([]*config.RuleConfig, len(e.configs))
	copy(configs, e.configs)
	e.mu.RUnlock()

	for _, cfg := range configs {
		if cfg.Auth != nil && !e.checkAuth(cfg.Auth, req) {
			e.logger.Warn("Auth failed for config '%s' from IP %s, skipping", cfg.Name, req.IP)
			continue
		}
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
	// Auth check first: targeted requests must receive an explicit 401.
	if cfg.Auth != nil && !e.checkAuth(cfg.Auth, req) {
		log := e.logger
		if ruleLog := e.getRuleLogger(cfg); ruleLog != nil {
			log = ruleLog
		}
		log.Warn("Auth failed for config '%s' from IP %s", cfg.Name, req.IP)
		return []Response{{
			Code:    401,
			Message: "Authentication failed",
			Config:  cfg.Name,
		}}
	}

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

	// Step 1.5: Check file-level filters (AND with rule-level, short-circuit)
	if len(cfg.Filters) > 0 && !e.matchFilters(cfg.Filters, req) {
		return nil // file-level filters failed, skip all rules in this config
	}

	// Step 1.6: Deduplication check (prevent relay-triggered duplicates)
	if cfg.Deduplicate != nil && cfg.Deduplicate.Enabled && req.RequestID != "" {
		window := time.Duration(cfg.Deduplicate.WindowSeconds) * time.Second
		if window == 0 {
			window = 300 * time.Second // default 5 minutes
		}
		if e.dedup.IsDuplicate(req.RequestID, window) {
			log.Info("Duplicate request detected (id=%s), skipping config '%s'", req.RequestID, cfg.Name)
			return []Response{{
				Code:    200,
				Message: "Deduplicated, request already processed",
				Config:  cfg.Name,
			}}
		}
	}

	for _, rule := range cfg.Rules {
		// Step 2: Match rule-level filters (AND relationship, empty = catch-all)
		if !e.matchFilters(rule.Filters, req) {
			continue
		}

		// Step 3: Atomically check execution policy and mark the task running.
		// Check-and-set happens under a single lock so concurrent requests
		// cannot both pass the policy check.
		taskKey := cfg.Name + "/" + rule.Name
		policy := e.resolvePolicy(cfg.Execution, rule.Execution)

		if blocked := e.tryAcquire(taskKey, policy); blocked != nil {
			blocked.Config = cfg.Name
			blocked.Rule = rule.Name
			log.Info("Task '%s' blocked: %s", taskKey, blocked.Message)
			return []Response{*blocked}
		}

		// Step 4: Execute actions (synchronously, or dispatch to background
		// when the config opts into async mode)
		if cfg.Async {
			return e.dispatchAsync(cfg, rule, taskKey, req, log)
		}
		log.Info("Rule '%s' matched, executing %d actions", taskKey, len(rule.Actions))
		actionCount, _ := e.executeActions(taskKey, rule.Actions, req, log)
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

// dispatchAsync accepts a matched rule for background execution and returns
// 202 immediately. The policy check has already passed via tryAcquire, so the
// task stays marked running until the background goroutine finishes.
func (e *Engine) dispatchAsync(cfg *config.RuleConfig, rule config.Rule, taskKey string, req *RequestData, log logger.LogWriter) []Response {
	// Backpressure: reject synchronously when the async task limit is reached
	select {
	case e.asyncSem <- struct{}{}:
	default:
		e.markDone(taskKey)
		log.Warn("Async task limit reached, rejecting request for task '%s'", taskKey)
		return []Response{{
			Code:    429,
			Message: "Async task limit reached, please try again later",
			Config:  cfg.Name,
			Rule:    rule.Name,
		}}
	}

	e.execStore.Add(execstore.Record{
		RequestID: req.RequestID,
		Config:    cfg.Name,
		Rule:      rule.Name,
		Status:    execstore.StatusRunning,
		StartedAt: time.Now(),
	})

	log.Info("Rule '%s' matched (async), request %s accepted", taskKey, req.RequestID)

	e.asyncWG.Add(1)
	go func() {
		defer e.asyncWG.Done()
		defer func() { <-e.asyncSem }()
		defer e.markDone(taskKey)
		defer func() {
			if r := recover(); r != nil {
				log.Error("[async] Task '%s' panicked: %v", taskKey, r)
				e.execStore.Complete(req.RequestID, execstore.StatusFailed, -1, fmt.Sprintf("panic: %v", r))
			}
		}()

		completed, failed := e.executeActions(taskKey, rule.Actions, req, log)
		if failed == nil {
			log.Info("[async] Task '%s' completed (%d actions, request %s)", taskKey, completed, req.RequestID)
			e.execStore.Complete(req.RequestID, execstore.StatusSucceeded, 0, "")
		} else {
			errMsg := ""
			if failed.Error != nil {
				errMsg = failed.Error.Error()
			}
			log.Error("[async] Task '%s' failed (request %s, exit=%d): %s", taskKey, req.RequestID, failed.ExitCode, errMsg)
			e.execStore.Complete(req.RequestID, execstore.StatusFailed, failed.ExitCode, errMsg)
		}
	}()

	return []Response{{
		Code:      202,
		Message:   "Accepted, executing asynchronously",
		Config:    cfg.Name,
		Rule:      rule.Name,
		RequestID: req.RequestID,
	}}
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
	return subtle.ConstantTimeCompare([]byte(actual), []byte(token.Value)) == 1
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
			// Exact IP match (use net.IP.Equal to handle representations
			// like IPv4-mapped IPv6 addresses)
			if entryIP := net.ParseIP(entry); entryIP != nil && entryIP.Equal(parsedIP) {
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
		re, err := compileCached(f.Value)
		if err != nil {
			return false
		}
		return re.MatchString(actual)
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

	for _, seg := range segments {
		if matches := arrayIndexRe.FindStringSubmatch(seg); matches != nil {
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

// tryAcquire atomically checks the execution policy and, when allowed,
// marks the task as running. Returns nil if execution may proceed, or a
// Response describing why the task is blocked. Holding a single lock across
// check-and-set prevents concurrent requests from racing past the policy.
func (e *Engine) tryAcquire(taskKey string, policy config.ExecutionConfig) *Response {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch policy.Policy {
	case "always":
		// never blocked
	case "block":
		if e.running[taskKey] {
			return &Response{
				Code:    409,
				Message: fmt.Sprintf("Task '%s' is running, please try again later", taskKey),
			}
		}
	case "cooldown":
		// Block when the last start is within the cooldown window, whether
		// the previous run is still active or already finished.
		if lastRun, ok := e.lastRun[taskKey]; ok {
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

	e.running[taskKey] = true
	e.lastRun[taskKey] = time.Now()
	return nil
}

// markDone marks a task as no longer running.
func (e *Engine) markDone(taskKey string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running[taskKey] = false
}

// executeActions runs all actions for a rule sequentially.
// Returns the number of successfully completed actions and the result of the
// last failed action (nil when every action succeeded).
func (e *Engine) executeActions(taskKey string, actions []config.Action, req *RequestData, log logger.LogWriter) (int, *executor.ActionResult) {
	completed := 0
	var lastFailed *executor.ActionResult

	// Extract config/rule names from taskKey ("configName/ruleName")
	parts := strings.SplitN(taskKey, "/", 2)
	configName, ruleName := "", ""
	if len(parts) == 2 {
		configName, ruleName = parts[0], parts[1]
	}

	for i, action := range actions {
		maxAttempts := 1
		baseInterval := 0
		if action.Retry != nil {
			maxAttempts = action.Retry.MaxAttempts
			baseInterval = action.Retry.IntervalSeconds
		}

		var result *executor.ActionResult
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if attempt > 1 {
				log.Info("[%s] Action %d/%d retry %d/%d", taskKey, i+1, len(actions), attempt, maxAttempts)
			} else {
				log.Info("[%s] Executing action %d/%d: type=%s", taskKey, i+1, len(actions), action.Type)
			}

			result = e.executeSingleAction(&action, req, configName, ruleName, log)

			if result.Success() {
				break
			}

			// Failed — log this attempt
			log.Error("[%s] Action %d/%d failed (attempt %d/%d, code=%d, duration=%v): %v",
				taskKey, i+1, len(actions), attempt, maxAttempts, result.ExitCode, result.Duration, result.Error)
			if result.Stderr != "" {
				log.Error("[%s] response: %s", taskKey, result.Stderr)
			}

			// Check if we should retry
			if attempt >= maxAttempts {
				break // exhausted all attempts
			}

			// Calculate and apply retry wait
			wait := calcRetryInterval(attempt, baseInterval)
			if wait > 0 {
				log.Info("[%s] Retrying in %v...", taskKey, wait.Round(time.Millisecond))
				time.Sleep(wait)
			}
		}

		// Final result handling
		if result.Success() {
			log.Info("[%s] Action %d/%d completed in %v", taskKey, i+1, len(actions), result.Duration)
			completed++
		} else {
			lastFailed = result
			if !action.ContinueOnError {
				log.Warn("[%s] Stopping execution due to action failure (continue_on_error=false)", taskKey)
				break
			}
		}

		if result.Stdout != "" {
			log.Debug("[%s] stdout: %s", taskKey, result.Stdout)
		}
	}

	return completed, lastFailed
}

// executeSingleAction runs a single action and returns the result.
func (e *Engine) executeSingleAction(action *config.Action, req *RequestData, configName, ruleName string, log logger.LogWriter) *executor.ActionResult {
	switch action.Type {
	case "command":
		envVars := e.buildEnvVars(action, req)
		resolvedCmd := e.resolveActionTemplates(action.Cmd, req, action.PassArgs)
		return executor.ExecuteCommand(resolvedCmd, action.Timeout, action.Isolate, envVars)
	case "script":
		envVars := e.buildEnvVars(action, req)
		resolvedPath := e.resolveActionTemplates(action.Path, req, nil)
		var resolvedArgs []string
		for _, arg := range action.Args {
			resolvedArgs = append(resolvedArgs, e.resolveActionTemplates(arg, req, nil))
		}
		return executor.ExecuteScript(resolvedPath, resolvedArgs, action.Timeout, action.Isolate, envVars)
	case "webhook":
		whResult := e.executeWebhook(action, req, configName, ruleName, log)
		log.Info("[%s/%s] Webhook %s %s -> HTTP %d (%v)", configName, ruleName, action.Method, action.URL, whResult.StatusCode, whResult.Duration)
		return whResult.toActionResult()
	case "relay":
		return e.executeRelay(action, req, configName, ruleName, log)
	default:
		log.Error("[%s/%s] Unknown action type: %s", configName, ruleName, action.Type)
		return &executor.ActionResult{ExitCode: 1, Error: fmt.Errorf("unknown action type: %s", action.Type)}
	}
}

// calcRetryInterval computes the wait time for a retry attempt using exponential backoff with jitter.
// attempt is the current attempt number (1-based, so attempt=1 means first retry).
// baseInterval is the base interval in seconds.
// Returns interval * 2^(attempt-1) with ±25% jitter, capped at 5 minutes.
func calcRetryInterval(attempt, baseInterval int) time.Duration {
	if baseInterval <= 0 {
		return 0
	}
	// Exponential: base * 2^(attempt-1)
	multiplier := 1 << (attempt - 1)
	interval := baseInterval * multiplier

	// Cap at 300 seconds (5 minutes)
	if interval > 300 {
		interval = 300
	}

	// Apply ±25% jitter
	// jitter range: [interval * 0.75, interval * 1.25]
	jitter := float64(interval) * (0.75 + rand.Float64()*0.5)
	return time.Duration(jitter) * time.Second
}

// resolveActionTemplates resolves template variables in a string and appends pass_args.
// Template syntax:
//
//	{{.raw_body}}       - raw request body as-is (command/script only)
//	{{.body.<path>}}    - extract from JSON body (supports dot notation and array index)
//	{{.header.<name>}}  - extract from request header
//	{{.query.<name>}}   - extract from query parameter
func (e *Engine) resolveActionTemplates(tmpl string, req *RequestData, passArgs []config.PassArg) string {
	result := tmpl

	// 0. Resolve {{.raw_body}} template
	result = tmplRawBodyRe.ReplaceAllStringFunc(result, func(_ string) string {
		return req.BodyRaw
	})

	// 1. Resolve {{.body.xxx}} templates
	result = tmplBodyRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := tmplBodyRe.FindStringSubmatch(match)
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
	result = tmplHeaderRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := tmplHeaderRe.FindStringSubmatch(match)
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
	result = tmplQueryRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := tmplQueryRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		val := req.Query[sub[1]]
		if val == "" {
			e.logger.Warn("Template variable not found: %s", match)
		}
		return val
	})

	// 4. Append pass_args as trailing arguments (shell-quoted so values with
	// spaces or metacharacters cannot break or inject into the command)
	if len(passArgs) > 0 {
		for _, pa := range passArgs {
			val := e.extractPassArgValue(&pa, req)
			if result != "" {
				result += " " + executor.QuoteShellArg(val)
			} else {
				result = executor.QuoteShellArg(val)
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

// buildEnvVars constructs environment variables for command/script actions.
// It injects default HOOKRUN_* vars and user-declared env_from vars with HOOKRUN_ prefix.
func (e *Engine) buildEnvVars(action *config.Action, req *RequestData) []string {
	var envVars []string

	// Default env vars (always injected)
	envVars = append(envVars, "HOOKRUN_RAW_BODY="+req.BodyRaw)
	envVars = append(envVars, "HOOKRUN_TRIGGER_IP="+req.IP)

	// User-declared env_from
	for _, es := range action.EnvFrom {
		val := e.extractEnvSourceValue(&es, req)
		name := envVarName(es.Env)
		envVars = append(envVars, name+"="+val)
	}

	return envVars
}

// extractEnvSourceValue extracts a value from the request based on EnvSource config.
func (e *Engine) extractEnvSourceValue(es *config.EnvSource, req *RequestData) string {
	switch es.Source {
	case "header":
		return req.Headers[strings.ToLower(es.Key)]
	case "query":
		return req.Query[es.Key]
	case "body":
		return e.extractBodyValue(req.Body, es.Key)
	default:
		return ""
	}
}

// envVarName ensures the env var name has the HOOKRUN_ prefix to avoid conflicts.
func envVarName(userInput string) string {
	name := strings.ToUpper(userInput)
	if strings.HasPrefix(name, "HOOKRUN_") {
		return name
	}
	return "HOOKRUN_" + name
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

	// Extract client IP. Proxy headers are only honored when the direct
	// peer is a trusted proxy (loopback or private network); otherwise a
	// remote client could forge X-Forwarded-For to bypass ip_whitelist.
	data.IP = r.RemoteAddr
	directHost := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		directHost = h
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" && isTrustedProxy(directHost) {
		// Rightmost entry is appended by the closest trusted proxy
		parts := strings.Split(xff, ",")
		data.IP = strings.TrimSpace(parts[len(parts)-1])
	} else if xri := r.Header.Get("X-Real-Ip"); xri != "" && isTrustedProxy(directHost) {
		data.IP = strings.TrimSpace(xri)
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

	// Generate or inherit Request-ID for idempotency
	if existingID, ok := data.Headers["x-hookrun-request-id"]; ok && existingID != "" {
		data.RequestID = existingID // inherit from relay sender
	} else {
		data.RequestID = generateRequestID()
	}

	// Parse relay hop count
	if hopsStr := data.Headers["x-hookrun-relay-hops"]; hopsStr != "" {
		if hops, err := strconv.Atoi(hopsStr); err == nil {
			data.RelayHops = hops
		}
	}

	return data, nil
}

// isTrustedProxy reports whether the direct peer address may be trusted to
// supply proxy headers: loopback or RFC1918 private networks.
func isTrustedProxy(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if _, block, err := net.ParseCIDR(cidr); err == nil && block.Contains(ip) {
			return true
		}
	}
	return false
}

// generateRequestID creates a unique request identifier: timestamp-8hexchars.
func generateRequestID() string {
	b := make([]byte, 4)
	_, _ = crand.Read(b)
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}
