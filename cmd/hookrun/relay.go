package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/spf13/cobra"
)

// relayStatusResponse matches the server response structure.
type relayStatusResponse struct {
	Role     string `json:"role"`
	Upstream struct {
		Enabled      bool `json:"enabled"`
		TargetsCount int  `json:"targets_count"`
		MaxEntries   int  `json:"max_entries"`
		MaxTTL       int  `json:"max_ttl"`
	} `json:"upstream"`
	Downstream struct {
		Enabled       bool     `json:"enabled"`
		UpstreamURL   string   `json:"upstream_url"`
		RegisteredURL string   `json:"registered_url"`
		Tags          []string `json:"tags"`
		TTL           int      `json:"ttl"`
		Connected     bool     `json:"connected"`
		LastHeartbeat string   `json:"last_heartbeat"`
		FailCount     int      `json:"fail_count"`
	} `json:"downstream"`
}

// targetsResponse matches the server response for /api/relay/targets.
type targetsResponse struct {
	Targets []struct {
		URL       string   `json:"url"`
		Tags      []string `json:"tags"`
		TTL       int      `json:"ttl"`
		LastSeen  string   `json:"last_seen"`
		ExpiresAt string   `json:"expires_at"`
	} `json:"targets"`
	Total int `json:"total"`
}

func relayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Manage and view Relay status",
		Long: `View and manage Relay configuration and status.

A HookRun instance can be:
  - Upstream: accepts downstream registrations (relay_registry_token configured)
  - Downstream: registers to an upstream (relay_client configured)
  - Both: acts as both upstream and downstream
  - None: no relay configuration`,
	}

	cmd.AddCommand(
		relayStatusCmd(),
		relayTargetsCmd(),
	)

	return cmd
}

func relayStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Relay status for this instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showRelayStatus()
		},
	}
}

func relayTargetsCmd() *cobra.Command {
	var token string

	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List registered downstream targets (requires Bearer token auth)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showRelayTargets(token)
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Registry auth token (defaults to relay_registry_token from config)")

	return cmd
}

func showRelayStatus() error {
	// Load config to get port
	configMgr := newConfigManager(configPath)
	if err := configMgr.Load(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cfg := configMgr.Global()
	port := cfg.Server.Port

	// Call the status API
	url := fmt.Sprintf("http://127.0.0.1:%d/api/relay/status", port)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("cannot connect to HookRun at port %d: %w", port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, errResp["error"])
	}

	var status relayStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Print formatted output
	fmt.Println("Relay Status")
	fmt.Println("============")
	fmt.Printf("Role: %s\n", formatRole(status.Role))
	fmt.Println()

	// Upstream section
	fmt.Println("Upstream (Registry):")
	if status.Upstream.Enabled {
		fmt.Printf("  Enabled:     yes\n")
		fmt.Printf("  Targets:     %d registered\n", status.Upstream.TargetsCount)
		if status.Upstream.MaxEntries > 0 {
			fmt.Printf("  Max entries: %d\n", status.Upstream.MaxEntries)
		}
		if status.Upstream.MaxTTL > 0 {
			fmt.Printf("  Max TTL:     %ds\n", status.Upstream.MaxTTL)
		}
	} else {
		fmt.Printf("  Enabled:     no\n")
	}
	fmt.Println()

	// Downstream section
	fmt.Println("Downstream (Client):")
	if status.Downstream.Enabled {
		fmt.Printf("  Enabled:        yes\n")
		fmt.Printf("  Upstream:       %s\n", status.Downstream.UpstreamURL)
		fmt.Printf("  Registered as:  %s\n", status.Downstream.RegisteredURL)
		if len(status.Downstream.Tags) > 0 {
			fmt.Printf("  Tags:           %s\n", strings.Join(status.Downstream.Tags, ", "))
		}
		if status.Downstream.TTL > 0 {
			fmt.Printf("  TTL:            %ds\n", status.Downstream.TTL)
		}
		if status.Downstream.Connected {
			fmt.Printf("  Connected:      yes\n")
		} else {
			fmt.Printf("  Connected:      no\n")
		}
		if status.Downstream.LastHeartbeat != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z", status.Downstream.LastHeartbeat); err == nil {
				ago := time.Since(t).Round(time.Second)
				fmt.Printf("  Last heartbeat: %s (%s ago)\n", t.Format("2006-01-02 15:04:05"), ago)
			}
		}
		if status.Downstream.FailCount > 0 {
			fmt.Printf("  Failures:       %d\n", status.Downstream.FailCount)
		}
	} else {
		fmt.Printf("  Enabled:        no\n")
	}

	return nil
}

func showRelayTargets(token string) error {
	// Load config to get port and token
	configMgr := newConfigManager(configPath)
	if err := configMgr.Load(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cfg := configMgr.Global()
	port := cfg.Server.Port

	// Get token from flag or config
	if token == "" {
		token = cfg.Server.RelayRegistryToken
	}

	// Check if upstream role is enabled
	if !cfg.Server.IsRelayRegistryEnabled() {
		return fmt.Errorf("this instance is not configured as an upstream (relay_registry_token not set)")
	}

	// Call the targets API
	url := fmt.Sprintf("http://127.0.0.1:%d/api/relay/targets", port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to HookRun at port %d: %w", port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("unauthorized: please provide a valid registry token")
	}

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, errResp["error"])
	}

	var targets targetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Print formatted output
	fmt.Printf("Registered Targets (%d)\n", targets.Total)

	if targets.Total == 0 {
		fmt.Println("No targets registered.")
		return nil
	}

	// Calculate column widths
	urlWidth := 3  // "URL"
	tagsWidth := 4 // "Tags"
	ttlWidth := 3  // "TTL"

	for _, t := range targets.Targets {
		if len(t.URL) > urlWidth {
			urlWidth = len(t.URL)
		}
		tags := strings.Join(t.Tags, ", ")
		if len(tags) > tagsWidth {
			tagsWidth = len(tags)
		}
	}

	// Print table
	fmt.Printf("+%s+%s+%s+%s+\n",
		strings.Repeat("-", urlWidth+2),
		strings.Repeat("-", tagsWidth+2),
		strings.Repeat("-", ttlWidth+2),
		strings.Repeat("-", 22))
	fmt.Printf("| %-*s | %-*s | %-*s | %-20s |\n",
		urlWidth, "URL",
		tagsWidth, "Tags",
		ttlWidth, "TTL",
		"Last Seen")
	fmt.Printf("+%s+%s+%s+%s+\n",
		strings.Repeat("-", urlWidth+2),
		strings.Repeat("-", tagsWidth+2),
		strings.Repeat("-", ttlWidth+2),
		strings.Repeat("-", 22))

	for _, t := range targets.Targets {
		tags := strings.Join(t.Tags, ", ")
		ttl := fmt.Sprintf("%ds", t.TTL)
		lastSeen := "-"
		if t.LastSeen != "" {
			if parsed, err := time.Parse("2006-01-02T15:04:05Z", t.LastSeen); err == nil {
				lastSeen = parsed.Format("2006-01-02 15:04:05")
			}
		}
		fmt.Printf("| %-*s | %-*s | %-*s | %-20s |\n",
			urlWidth, t.URL,
			tagsWidth, tags,
			ttlWidth, ttl,
			lastSeen)
	}

	fmt.Printf("+%s+%s+%s+%s+\n",
		strings.Repeat("-", urlWidth+2),
		strings.Repeat("-", tagsWidth+2),
		strings.Repeat("-", ttlWidth+2),
		strings.Repeat("-", 22))

	return nil
}

func formatRole(role string) string {
	switch role {
	case "upstream+downstream":
		return "upstream + downstream"
	case "upstream":
		return "upstream only"
	case "downstream":
		return "downstream only"
	case "none":
		return "none (relay not configured)"
	default:
		return role
	}
}

// newConfigManager creates a config manager (helper to avoid import issues).
func newConfigManager(path string) *config.Manager {
	return config.NewManager(path)
}
