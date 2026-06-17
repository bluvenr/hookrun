package engine

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/executor"
	"github.com/bluvenr/hookrun/internal/logger"
	"github.com/bluvenr/hookrun/internal/version"
)

// defaultMaxRelayHops is the default anti-loop limit for relay chains.
const defaultMaxRelayHops = 3

// relayResult wraps the combined results of a multi-target relay operation.
type relayResult struct {
	Targets   int
	Succeeded int
	Failed    int
	Duration  time.Duration
	Details   []string
}

// Success returns true if at least one relay target responded successfully.
func (r *relayResult) Success() bool {
	return r.Succeeded > 0
}

// toActionResult converts a relayResult to an executor.ActionResult for unified logging.
func (r *relayResult) toActionResult() *executor.ActionResult {
	summary := fmt.Sprintf("%d/%d targets succeeded", r.Succeeded, r.Targets)
	result := &executor.ActionResult{
		Duration: r.Duration,
		Stdout:   summary,
	}
	if len(r.Details) > 0 {
		result.Stderr = strings.Join(r.Details, "; ")
	}
	if r.Succeeded == 0 {
		result.ExitCode = 1
		result.Error = fmt.Errorf("all relay targets failed")
	} else if r.Failed > 0 {
		result.ExitCode = 0 // partial success is still OK
	}
	return result
}

// executeRelay forwards the webhook request to multiple HookRun instances concurrently.
func (e *Engine) executeRelay(action *config.Action, req *RequestData, configName, ruleName string, log logger.LogWriter) *executor.ActionResult {
	start := time.Now()
	relayCfg := action.Relay

	// Determine max relay hops (0 means use default)
	maxHops := defaultMaxRelayHops
	if relayCfg.MaxRelayHops > 0 {
		maxHops = relayCfg.MaxRelayHops
	}

	// Anti-loop check: reject if hop count has reached the limit
	if req.RelayHops >= maxHops {
		msg := fmt.Sprintf("relay hop limit reached (%d/%d), aborting relay", req.RelayHops, maxHops)
		log.Warn("[%s/%s] %s", configName, ruleName, msg)
		return &executor.ActionResult{
			ExitCode: 1,
			Error:    fmt.Errorf("%s", msg),
			Stdout:   msg,
			Duration: time.Since(start),
		}
	}

	// Determine per-target timeout (default 30s)
	timeout := 30 * time.Second
	if relayCfg.Timeout > 0 {
		timeout = time.Duration(relayCfg.Timeout) * time.Second
	}

	// Resolve targets: expand tags to actual URLs, deduplicate by URL
	targets := e.resolveRelayTargets(relayCfg.Targets, configName, ruleName, log)
	if len(targets) == 0 {
		msg := "no relay targets resolved"
		log.Warn("[%s/%s] %s", configName, ruleName, msg)
		return &executor.ActionResult{
			ExitCode: 1,
			Error:    fmt.Errorf("%s", msg),
			Stdout:   msg,
			Duration: time.Since(start),
		}
	}

	result := &relayResult{Targets: len(targets)}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, target := range targets {
		wg.Add(1)
		go func(t config.RelayTarget) {
			defer wg.Done()

			detail := e.sendToTarget(t, req, relayCfg, configName, ruleName, maxHops, timeout, log)

			mu.Lock()
			defer mu.Unlock()
			if detail.success {
				result.Succeeded++
			} else {
				result.Failed++
			}
			result.Details = append(result.Details, detail.message)
		}(target)
	}

	wg.Wait()
	result.Duration = time.Since(start)

	log.Info("[%s/%s] Relay -> %d/%d targets succeeded (%v)", configName, ruleName, result.Succeeded, result.Targets, result.Duration)
	return result.toActionResult()
}

// resolveRelayTargets expands relay targets, resolving tags to actual URLs and deduplicating.
// Static targets (with URL) take priority over dynamic targets (with tag) for the same URL.
func (e *Engine) resolveRelayTargets(configTargets []config.RelayTarget, configName, ruleName string, log logger.LogWriter) []config.RelayTarget {
	seen := make(map[string]bool) // URL -> already added
	var resolved []config.RelayTarget

	// Phase 1: add all static targets (URL-based)
	for _, t := range configTargets {
		if t.URL != "" {
			seen[t.URL] = true
			resolved = append(resolved, t)
		}
	}

	// Phase 2: resolve tag targets from registry
	for _, t := range configTargets {
		if t.Tag == "" {
			continue
		}

		if e.registry == nil {
			log.Warn("[%s/%s] Relay tag '%s' cannot be resolved: registry is not enabled", configName, ruleName, t.Tag)
			continue
		}

		entries := e.registry.FindByTag(t.Tag)
		if len(entries) == 0 {
			log.Warn("[%s/%s] Relay tag '%s' matched 0 registered targets", configName, ruleName, t.Tag)
			continue
		}

		for _, entry := range entries {
			if seen[entry.URL] {
				continue // skip duplicate (static takes priority)
			}
			seen[entry.URL] = true
			resolved = append(resolved, config.RelayTarget{
				URL:   entry.URL,
				Token: entry.Token,
			})
		}
		log.Info("[%s/%s] Relay tag '%s' resolved to %d target(s)", configName, ruleName, t.Tag, len(entries))
	}

	return resolved
}

// targetResult holds the outcome of sending to a single relay target.
type targetResult struct {
	success bool
	message string
}

// sendToTarget sends the webhook to a single relay target and returns the result.
func (e *Engine) sendToTarget(target config.RelayTarget, req *RequestData, relayCfg *config.RelayConfig, configName, ruleName string, maxHops int, timeout time.Duration, log logger.LogWriter) targetResult {
	// Build headers
	headers := make(map[string]string)

	// Relay identity headers
	headers["X-HookRun-Relay"] = "true"
	headers["X-HookRun-Relay-From"] = req.IP
	headers["X-HookRun-Request-ID"] = req.RequestID
	headers["X-HookRun-Relay-Hops"] = strconv.Itoa(req.RelayHops + 1)
	headers["X-HookRun-Source"] = "HookRun/v" + version.Version
	headers["X-HookRun-Config"] = configName
	headers["X-HookRun-Rule"] = ruleName

	// Per-target auth token
	if target.Token != "" {
		headers["X-HookRun-Relay-Token"] = target.Token
	}

	// Forward original headers (whitelist)
	for _, name := range relayCfg.ForwardHeaders {
		lowerKey := strings.ToLower(name)
		if val, ok := req.Headers[lowerKey]; ok && val != "" {
			headers[name] = val
		}
	}

	// Create HTTP POST request with original raw body
	httpReq, err := http.NewRequest("POST", target.URL, bytes.NewReader(req.BodyBytes))
	if err != nil {
		return targetResult{false, fmt.Sprintf("%s: create request failed: %v", target.URL, err)}
	}

	for name, value := range headers {
		httpReq.Header.Set(name, value)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Execute with timeout
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Warn("[%s/%s] Relay to %s failed: %v", configName, ruleName, target.URL, err)
		return targetResult{false, fmt.Sprintf("%s: %v", target.URL, err)}
	}
	defer resp.Body.Close()

	// Read response body (limited to 4KB)
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Info("[%s/%s] Relay to %s -> HTTP %d", configName, ruleName, target.URL, resp.StatusCode)
		return targetResult{true, fmt.Sprintf("%s: HTTP %d", target.URL, resp.StatusCode)}
	}

	log.Warn("[%s/%s] Relay to %s -> HTTP %d: %s", configName, ruleName, target.URL, resp.StatusCode, string(respBody))
	return targetResult{false, fmt.Sprintf("%s: HTTP %d", target.URL, resp.StatusCode)}
}
