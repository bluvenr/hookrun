package engine

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/executor"
	"github.com/bluvenr/hookrun/internal/logger"
)

// rawBodyPattern matches {{.raw_body}} with optional whitespace.
var rawBodyPattern = regexp.MustCompile(`\{\{\s*\.raw_body\s*\}\}`)

// webhookResult wraps an HTTP response into an ActionResult-compatible structure.
type webhookResult struct {
	StatusCode int
	Body       string
	Duration   time.Duration
	Error      error
}

// Success returns true if the webhook completed without error and got a 2xx response.
func (r *webhookResult) Success() bool {
	return r.Error == nil && r.StatusCode >= 200 && r.StatusCode < 300
}

// toActionResult converts a webhookResult to an executor.ActionResult for unified logging.
func (r *webhookResult) toActionResult() *executor.ActionResult {
	result := &executor.ActionResult{
		Duration: r.Duration,
		Stdout:   fmt.Sprintf("HTTP %d", r.StatusCode),
	}
	if r.Body != "" {
		result.Stderr = r.Body // response body goes to stderr for logging
	}
	if r.Error != nil {
		result.Error = r.Error
		result.ExitCode = -1
	} else if r.StatusCode >= 400 {
		result.ExitCode = 1
		result.Error = fmt.Errorf("webhook returned HTTP %d", r.StatusCode)
	}
	return result
}

// executeWebhook sends an HTTP request based on the webhook action config.
func (e *Engine) executeWebhook(action *config.Action, req *RequestData, configName, ruleName string, log logger.LogWriter) *webhookResult {
	start := time.Now()

	// 1. Resolve URL templates
	url := e.resolveActionTemplates(action.URL, req, nil)

	// 2. Build headers (merge order: auto → forward → custom)
	headers := make(map[string]string)

	// 2a. Auto headers (X-HookRun-*)
	headers["X-HookRun-Source"] = "HookRun/v1.1.2"
	headers["X-HookRun-Config"] = configName
	headers["X-HookRun-Rule"] = ruleName

	// 2b. Forward original headers (whitelist)
	for _, name := range action.ForwardHeaders {
		lowerKey := strings.ToLower(name)
		if val, ok := req.Headers[lowerKey]; ok && val != "" {
			headers[name] = val
		}
	}

	// 2c. Custom headers (highest priority, supports templates)
	for name, tmpl := range action.Headers {
		headers[name] = e.resolveActionTemplates(tmpl, req, nil)
	}

	// 3. Build body
	var bodyReader io.Reader
	if action.Method == "GET" {
		bodyReader = nil // GET requests have no body
	} else if rawBodyPattern.MatchString(action.Body) {
		// Body contains {{.raw_body}} — replace with raw body bytes
		resolved := rawBodyPattern.ReplaceAllString(action.Body, string(req.BodyBytes))
		// Also resolve other templates in the same body (e.g. {{.header.x}})
		resolved = e.resolveWebhookTemplates(resolved, req)
		bodyReader = bytes.NewBufferString(resolved)
	} else {
		// Standard template resolution
		resolved := e.resolveActionTemplates(action.Body, req, nil)
		bodyReader = bytes.NewBufferString(resolved)
	}

	// 4. Create HTTP request
	httpReq, err := http.NewRequest(action.Method, url, bodyReader)
	if err != nil {
		return &webhookResult{Error: fmt.Errorf("create request: %w", err), Duration: time.Since(start)}
	}

	// Apply headers
	for name, value := range headers {
		httpReq.Header.Set(name, value)
	}

	// 5. Set timeout
	timeout := 30 * time.Second
	if action.Timeout > 0 {
		timeout = time.Duration(action.Timeout) * time.Second
	}
	client := &http.Client{Timeout: timeout}

	// 6. Execute
	resp, err := client.Do(httpReq)
	duration := time.Since(start)
	if err != nil {
		return &webhookResult{Error: fmt.Errorf("webhook request failed: %w", err), Duration: duration}
	}
	defer resp.Body.Close()

	// 7. Read response body (limited to 4KB for logging)
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	return &webhookResult{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Duration:   duration,
	}
}

// resolveWebhookTemplates resolves {{.body.x}}, {{.header.x}}, {{.query.x}} in a string.
// Unlike resolveActionTemplates, this does NOT handle pass_args (not applicable for webhook).
func (e *Engine) resolveWebhookTemplates(tmpl string, req *RequestData) string {
	result := tmpl

	// body
	bodyRe := regexp.MustCompile(`\{\{\s*\.body\.([^}\s]+)\s*\}\}`)
	result = bodyRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := bodyRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return e.extractBodyValue(req.Body, sub[1])
	})

	// header
	headerRe := regexp.MustCompile(`\{\{\s*\.header\.([^}\s]+)\s*\}\}`)
	result = headerRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := headerRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return req.Headers[strings.ToLower(sub[1])]
	})

	// query
	queryRe := regexp.MustCompile(`\{\{\s*\.query\.([^}\s]+)\s*\}\}`)
	result = queryRe.ReplaceAllStringFunc(result, func(match string) string {
		sub := queryRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return req.Query[sub[1]]
	})

	return result
}
