package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/logger"
)

// RelayClient handles auto-registration and heartbeat with an upstream HookRun instance.
type RelayClient struct {
	config *config.RelayClientConfig
	logger logger.LogWriter
	port   int
	url    string // resolved local URL
	stop   chan struct{}

	mu            sync.Mutex
	failCount     int       // consecutive failure count for log degradation
	lastHeartbeat time.Time // last successful heartbeat time
	connected     bool      // current connection status
}

// ClientStatus represents the current relay client status.
type ClientStatus struct {
	Upstream      string    `json:"upstream"`
	RegisteredURL string    `json:"registered_url"`
	Tags          []string  `json:"tags"`
	TTL           int       `json:"ttl"`
	Connected     bool      `json:"connected"`
	LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
	FailCount     int       `json:"fail_count"`
}

// Status returns the current relay client status.
func (rc *RelayClient) Status() ClientStatus {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	return ClientStatus{
		Upstream:      rc.config.Upstream,
		RegisteredURL: rc.url,
		Tags:          rc.config.Tags,
		TTL:           rc.config.TTL,
		Connected:     rc.connected,
		LastHeartbeat: rc.lastHeartbeat,
		FailCount:     rc.failCount,
	}
}

// NewRelayClient creates a new relay client for auto-registration.
func NewRelayClient(cfg *config.RelayClientConfig, port int, log logger.LogWriter) *RelayClient {
	return &RelayClient{
		config: cfg,
		logger: log,
		port:   port,
		stop:   make(chan struct{}),
	}
}

// Start performs the initial registration and starts the heartbeat goroutine.
// Registration failure does not block startup — heartbeat will retry.
func (rc *RelayClient) Start() {
	rc.url = rc.resolveURL()
	rc.logger.Info("Relay client: registering as %s (tags: %s, ttl: %ds)", rc.url, strings.Join(rc.config.Tags, ", "), rc.config.TTL)

	// Initial registration (non-blocking: failure is acceptable)
	if err := rc.register(); err != nil {
		rc.logger.Warn("Relay client: initial registration failed: %v (will retry via heartbeat)", err)
		rc.mu.Lock()
		rc.failCount = 1
		rc.connected = false
		rc.mu.Unlock()
	} else {
		rc.logger.Info("Relay client: registered successfully")
		rc.mu.Lock()
		rc.connected = true
		rc.lastHeartbeat = time.Now()
		rc.mu.Unlock()
	}

	// Start heartbeat goroutine
	go rc.heartbeatLoop()
}

// Stop sends an unregistration request and stops the heartbeat.
func (rc *RelayClient) Stop() {
	close(rc.stop)

	// Attempt unregistration with a short timeout
	if err := rc.unregister(); err != nil {
		rc.logger.Warn("Relay client: unregistration failed: %v", err)
	} else {
		rc.logger.Info("Relay client: unregistered from upstream")
	}
}

// resolveURL determines the local URL to register with upstream.
// Priority: explicit config.URL > auto-detect from IP + port + webhook_path.
func (rc *RelayClient) resolveURL() string {
	if rc.config.URL != "" {
		return strings.TrimRight(rc.config.URL, "/")
	}

	ip := detectLocalIP()
	if ip == "" {
		ip = "127.0.0.1"
	}

	path := rc.config.WebhookPath
	if path == "" {
		path = "/webhook"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return fmt.Sprintf("http://%s:%d%s", ip, rc.port, path)
}

// detectLocalIP returns the first non-loopback IPv4 address.
func detectLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		// Skip down, loopback, and point-to-point interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			// Only IPv4
			if ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return ""
}

// register sends a POST /api/relay/register to the upstream.
func (rc *RelayClient) register() error {
	if rc.url == "" {
		rc.url = rc.resolveURL()
	}

	body := map[string]interface{}{
		"url":  rc.url,
		"tags": rc.config.Tags,
	}
	if rc.config.Token != "" {
		body["token"] = rc.config.Token
	}
	if rc.config.TTL > 0 {
		body["ttl"] = rc.config.TTL
	}

	return rc.sendRequest(http.MethodPost, "/api/relay/register", body, 10*time.Second)
}

// unregister sends a DELETE /api/relay/register to the upstream.
func (rc *RelayClient) unregister() error {
	if rc.url == "" {
		rc.url = rc.resolveURL()
	}

	body := map[string]interface{}{
		"url": rc.url,
	}
	return rc.sendRequest(http.MethodDelete, "/api/relay/register", body, 3*time.Second)
}

// sendRequest sends an HTTP request to the upstream relay API.
func (rc *RelayClient) sendRequest(method, path string, body interface{}, timeout time.Duration) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	upstreamURL := strings.TrimRight(rc.config.Upstream, "/") + path
	req, err := http.NewRequest(method, upstreamURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if rc.config.RegistryToken != "" {
		req.Header.Set("Authorization", "Bearer "+rc.config.RegistryToken)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", upstreamURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Read error body for diagnostics
	var errResp map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	errMsg := errResp["error"]

	return fmt.Errorf("upstream returned %d: %s", resp.StatusCode, errMsg)
}

// heartbeatLoop periodically re-registers with the upstream.
func (rc *RelayClient) heartbeatLoop() {
	// Interval = ttl / 2, minimum 10s
	interval := time.Duration(rc.config.TTL/2) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := rc.register(); err != nil {
				rc.mu.Lock()
				rc.failCount++
				rc.connected = false
				count := rc.failCount
				rc.mu.Unlock()

				if count >= 3 {
					// Degrade to debug-level after 3 consecutive failures
					rc.logger.Debug("Relay client: heartbeat failed (%d consecutive): %v", count, err)
				} else {
					rc.logger.Warn("Relay client: heartbeat failed: %v", err)
				}
			} else {
				rc.mu.Lock()
				if rc.failCount > 0 {
					rc.logger.Info("Relay client: heartbeat recovered after %d failures", rc.failCount)
				}
				rc.failCount = 0
				rc.connected = true
				rc.lastHeartbeat = time.Now()
				rc.mu.Unlock()
			}

		case <-rc.stop:
			return
		}
	}
}
