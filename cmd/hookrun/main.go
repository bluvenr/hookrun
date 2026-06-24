package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/daemon"
	"github.com/bluvenr/hookrun/internal/engine"
	"github.com/bluvenr/hookrun/internal/logger"
	"github.com/bluvenr/hookrun/internal/server"
	"github.com/bluvenr/hookrun/internal/version"
	"github.com/spf13/cobra"
)

var (
	configPath string
	foreground bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "hookrun",
		Short: "HookRun - Webhook Action Engine",
		Long:  "A lightweight webhook listener that executes custom commands based on YAML rules.",
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to global config file")

	rootCmd.AddCommand(
		startCmd(),
		stopCmd(),
		restartCmd(),
		statusCmd(),
		reloadCmd(),
		validateCmd(),
		initCmd(),
		versionCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ---- start command ----
func startCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the HookRun server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if already running
			pid, err := daemon.ReadPID()
			if err == nil && daemon.IsRunning(pid) {
				return fmt.Errorf("HookRun is already running (PID: %d)", pid)
			}

			if foreground {
				return runServer()
			}

			// Daemon mode: start a background process
			return startDaemon()
		},
	}
	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "Run in foreground mode")
	return cmd
}

// runServer starts the server in the current process.
func runServer() error {
	// Load config
	configMgr := config.NewManager(configPath)
	if err := configMgr.Load(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg := configMgr.Global()

	// Init logger
	log := logger.New(logger.Options{
		Mode:          cfg.Log.Mode,
		Prefix:        "hookrun",
		Path:          cfg.Log.Path,
		RetentionDays: cfg.Log.RetentionDays,
		MaxSizeMB:     cfg.Log.MaxSizeMB,
		MinLevel:      logger.LevelInfo,
		Console:       true,
	})
	defer log.Close()

	// Write PID and status
	if err := daemon.WritePID(); err != nil {
		log.Error("Failed to write PID file: %v", err)
	}
	defer daemon.RemovePID()

	if err := daemon.WriteStatus(cfg.Server.Port, configMgr.RuleCount()); err != nil {
		log.Error("Failed to write status: %v", err)
	}

	// Init engine
	eng := engine.New(configMgr.Rules(), log, cfg.Log.Mode, cfg.Log.RetentionDays, cfg.Log.MaxSizeMB)

	// Init relay registry (if enabled)
	if cfg.Server.IsRelayRegistryEnabled() {
		registry := engine.NewTargetRegistry(cfg.Server.MaxRegistryEntries, cfg.Server.MaxRelayTTL)
		eng.SetRegistry(registry)
	}

	// Init relay client (if configured)
	var relayClient *engine.RelayClient
	if cfg.HasRelayClient() {
		relayClient = engine.NewRelayClient(cfg.RelayClient, cfg.Server.Port, log)
		relayClient.Start()
		defer relayClient.Stop()
	}

	// Create server and start HTTP listener (non-blocking)
	srv := server.New(configMgr, eng, log)
	if err := srv.ListenAndServe(); err != nil {
		return fmt.Errorf("server start: %w", err)
	}

	// Shared shutdown channel (closed by either signal source)
	stopCh := make(chan struct{})

	// Start signal file watcher (primary IPC on Windows, reload on all platforms)
	go watchSignalFiles(srv, eng, relayClient, configMgr, log, stopCh)

	// On non-Windows: also listen for OS signals (SIGTERM/SIGINT)
	if runtime.GOOS != "windows" {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		// Use a goroutine to translate OS signal into stopCh
		go func() {
			sig := <-sigCh
			log.Info("Received signal %v, initiating graceful shutdown...", sig)
			if relayClient != nil {
				relayClient.Stop()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := srv.GracefulShutdown(ctx); err != nil {
				log.Error("Graceful shutdown error: %v", err)
			}
			cancel()
			select {
			case <-stopCh: // already closed by watcher
			default:
				close(stopCh)
			}
		}()
	}

	// Wait for graceful shutdown or HTTP server error
	select {
	case <-stopCh:
		// Graceful shutdown completed
	case err := <-srv.ErrCh():
		log.Error("HTTP server error: %v", err)
		// Emergency cleanup
		if relayClient != nil {
			relayClient.Stop()
		}
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// watchSignalFiles polls for signal files (cross-platform IPC).
// On Windows, it handles graceful shutdown by stopping relay client and HTTP server
// before signaling the main goroutine to return (instead of os.Exit(0)).
func watchSignalFiles(srv *server.Server, eng *engine.Engine, relayClient *engine.RelayClient, configMgr *config.Manager, log *logger.Logger, stopCh chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Check reload signal
		if daemon.CheckSignalFile(daemon.ReloadSignalFile) {
			log.Info("Reload signal received")
			if err := configMgr.Reload(); err != nil {
				log.Error("Reload failed: %v", err)
			} else {
				eng.CloseRuleLoggers() // close old rule-level loggers
				eng.UpdateConfigs(configMgr.Rules())
				log.Info("Reload successful, loaded %d rule(s)", configMgr.RuleCount())
			}
			cfg := configMgr.Global()
			_ = daemon.WriteStatus(cfg.Server.Port, configMgr.RuleCount())
		}

		// Check stop signal — graceful shutdown (no os.Exit!)
		if daemon.CheckSignalFile(daemon.StopSignalFile) {
			log.Info("Stop signal received, initiating graceful shutdown...")

			// 1. Stop relay client (sends unregister to upstream)
			if relayClient != nil {
				relayClient.Stop()
			}

			// 2. Gracefully shutdown HTTP server (drains active connections)
			if srv != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := srv.GracefulShutdown(ctx); err != nil {
					log.Error("HTTP graceful shutdown error: %v", err)
				}
				cancel()
			}

			// 3. Signal main goroutine to return (triggers remaining defers)
			close(stopCh)
			return
		}
	}
}

// startDaemon starts the server as a background process.
func startDaemon() error {
	// Validate config first
	configMgr := config.NewManager(configPath)
	if err := configMgr.Load(); err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	// Start background process
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	args := []string{"start", "-f", "-c", configPath}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	// Detach from current process group
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	fmt.Printf("HookRun started in background (PID: %d)\n", cmd.Process.Pid)
	return nil
}

// ---- stop command ----
func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the HookRun server",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := daemon.ReadPID()
			if err != nil {
				return fmt.Errorf("HookRun is not running (no PID file)")
			}

			if !daemon.IsRunning(pid) {
				daemon.RemovePID()
				return fmt.Errorf("HookRun is not running (stale PID file removed)")
			}

			if runtime.GOOS == "windows" {
				// Use signal file on Windows
				if err := daemon.WriteSignalFile(daemon.StopSignalFile); err != nil {
					return fmt.Errorf("failed to send stop signal: %w", err)
				}
				fmt.Println("Stop signal sent to HookRun")
			} else {
				// Use SIGTERM on Unix
				process, err := os.FindProcess(pid)
				if err != nil {
					return fmt.Errorf("cannot find process: %w", err)
				}
				if err := process.Signal(os.Interrupt); err != nil {
					return fmt.Errorf("failed to stop process: %w", err)
				}
				fmt.Printf("HookRun stopped (PID: %d)\n", pid)
			}

			return nil
		},
	}
}

// ---- restart command ----
func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the HookRun server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Stop
			pid, err := daemon.ReadPID()
			if err == nil && daemon.IsRunning(pid) {
				if runtime.GOOS == "windows" {
					_ = daemon.WriteSignalFile(daemon.StopSignalFile)
				} else {
					process, _ := os.FindProcess(pid)
					_ = process.Signal(os.Interrupt)
				}
				// Wait for process to stop
				for i := 0; i < 30; i++ {
					if !daemon.IsRunning(pid) {
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
			}
			daemon.RemovePID()

			// Start
			fmt.Println("Restarting HookRun...")
			if foreground {
				return runServer()
			}
			return startDaemon()
		},
	}
}

// ---- status command ----
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show HookRun server status",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := daemon.ReadPID()
			if err != nil {
				fmt.Println("Status: stopped (no PID file)")
				return nil
			}

			if !daemon.IsRunning(pid) {
				daemon.RemovePID()
				fmt.Println("Status: stopped (stale PID file removed)")
				return nil
			}

			fmt.Printf("Status:  running\n")
			fmt.Printf("PID:     %d\n", pid)

			// Try to get status from API
			statusData, err := daemon.ReadStatus()
			if err == nil {
				var info struct {
					Port      int    `json:"port"`
					Rules     int    `json:"rules"`
					StartTime string `json:"start_time"`
				}
				if json.Unmarshal([]byte(statusData), &info) == nil {
					fmt.Printf("Port:    %d\n", info.Port)
					fmt.Printf("Rules:   %d config(s)\n", info.Rules)
					if info.StartTime != "" {
						if t, err := time.Parse(time.RFC3339, info.StartTime); err == nil {
							uptime := time.Since(t).Round(time.Second)
							fmt.Printf("Uptime:  %s\n", uptime)
							fmt.Printf("Started: %s\n", t.Format("2006-01-02 15:04:05"))
						}
					}
				}
			}

			// Try health endpoint
			cfg := config.Defaults()
			configMgr := config.NewManager(configPath)
			if err := configMgr.Load(); err == nil {
				cfg = configMgr.Global()
			}
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", cfg.Server.Port))
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == 200 {
					var health map[string]interface{}
					if json.NewDecoder(resp.Body).Decode(&health) == nil {
						if uptime, ok := health["uptime"].(string); ok {
							fmt.Printf("Uptime:  %s\n", uptime)
						}
					}
				}
			}

			return nil
		},
	}
}

// ---- reload command ----
func reloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Hot-reload all YAML configurations",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := daemon.ReadPID()
			if err != nil {
				return fmt.Errorf("HookRun is not running")
			}
			if !daemon.IsRunning(pid) {
				daemon.RemovePID()
				return fmt.Errorf("HookRun is not running (stale PID)")
			}

			if runtime.GOOS == "windows" {
				// Use signal file
				if err := daemon.WriteSignalFile(daemon.ReloadSignalFile); err != nil {
					return fmt.Errorf("failed to send reload signal: %w", err)
				}
				fmt.Println("Reload signal sent to HookRun")
			} else {
				// Use the reload API endpoint
				cfg := config.Defaults()
				configMgr := config.NewManager(configPath)
				if err := configMgr.Load(); err == nil {
					cfg = configMgr.Global()
				}
				resp, err := http.Post(
					fmt.Sprintf("http://127.0.0.1:%d/_reload", cfg.Server.Port),
					"application/json", nil)
				if err != nil {
					// Fallback to signal
					process, _ := os.FindProcess(pid)
					_ = process.Signal(syscallSighup())
					fmt.Println("Reload signal sent (SIGHUP)")
				} else {
					defer resp.Body.Close()
					var result map[string]interface{}
					json.NewDecoder(resp.Body).Decode(&result)
					fmt.Printf("Reload successful: %v\n", result)
				}
			}

			return nil
		},
	}
}

// ---- validate command ----
func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate all YAML configuration files",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Validating config: %s\n", configPath)

			configMgr := config.NewManager(configPath)
			if err := configMgr.Load(); err != nil {
				fmt.Printf("FAIL: %v\n", err)
				return fmt.Errorf("validation failed")
			}

			errs := configMgr.ValidateAll()
			if len(errs) > 0 {
				fmt.Printf("FAIL: %d error(s) found:\n", len(errs))
				for i, err := range errs {
					fmt.Printf("  %d. %v\n", i+1, err)
				}
				return fmt.Errorf("validation failed")
			}

			cfg := configMgr.Global()
			fmt.Println("PASS: All configurations are valid")
			fmt.Printf("  Server port: %d\n", cfg.Server.Port)
			fmt.Printf("  Webhook route: %s\n", cfg.Server.Route)
			fmt.Printf("  Allow all: %v\n", cfg.Server.IsAllowAll())
			if cfg.Server.MaxBodySizeMB != nil {
				if *cfg.Server.MaxBodySizeMB == 0 {
					fmt.Printf("  Max body size: unlimited\n")
				} else {
					fmt.Printf("  Max body size: %d MB\n", *cfg.Server.MaxBodySizeMB)
				}
			}
			fmt.Printf("  Log mode: %s\n", cfg.Log.Mode)
			fmt.Printf("  Log path: %s\n", cfg.Log.Path)
			fmt.Printf("  Log retention: %d days\n", cfg.Log.RetentionDays)
			if cfg.Log.Mode == "single" {
				if cfg.Log.MaxSizeMB > 0 {
					fmt.Printf("  Log max size: %d MB\n", cfg.Log.MaxSizeMB)
				} else {
					fmt.Printf("  Log max size: unlimited\n")
				}
			}
			fmt.Printf("  Config dir: %s\n", cfg.ConfigDir)
			fmt.Printf("  Rule files loaded: %d\n", configMgr.RuleCount())

			// Relay info
			if cfg.Server.IsRelayRegistryEnabled() {
				fmt.Printf("  Relay registry: enabled (max entries: %d, max TTL: %ds)\n", cfg.Server.MaxRegistryEntries, cfg.Server.MaxRelayTTL)
			} else {
				fmt.Printf("  Relay registry: disabled\n")
			}
			if cfg.HasRelayClient() {
				fmt.Printf("  Relay client: upstream=%s, tags=%v, ttl=%ds\n", cfg.RelayClient.Upstream, cfg.RelayClient.Tags, cfg.RelayClient.TTL)
			}

			for _, r := range configMgr.Rules() {
				ruleNames := make([]string, 0, len(r.Rules))
				for _, rule := range r.Rules {
					ruleNames = append(ruleNames, rule.Name)
				}
				fmt.Printf("    - %s (%d rules: %s)", r.Name, len(r.Rules), strings.Join(ruleNames, ", "))
				// Show auth types configured
				if r.Auth != nil {
					var authTypes []string
					if r.Auth.Token != nil {
						authTypes = append(authTypes, "token")
					}
					if r.Auth.HMAC != nil {
						authTypes = append(authTypes, "hmac")
					}
					if len(r.Auth.IPWhitelist) > 0 {
						authTypes = append(authTypes, "ip_whitelist")
					}
					if len(authTypes) > 0 {
						fmt.Printf(" [auth: %s]", strings.Join(authTypes, "+"))
					}
					if r.Log != nil && r.Log.Path != "" {
						fmt.Printf(" [log: %s]", r.Log.Path)
					}
				}
				fmt.Println()
			}

			return nil
		},
	}
}

// ---- version command ----
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("HookRun v%s\n", version.Version)
			fmt.Printf("Build time: %s\n", version.BuildTime)
			fmt.Printf("Go version: %s\n", runtime.Version())
			fmt.Printf("OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}

// syscallSighup returns SIGHUP for Unix systems.
// On Windows this is a no-op stub.
func syscallSighup() os.Signal {
	if runtime.GOOS == "windows" {
		return os.Interrupt
	}
	// Use dynamic import to avoid Windows compilation issues
	return unixSighup()
}

// setProcessGroup detaches the child process on Unix.
func setProcessGroup(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		setSysProcAttr(cmd)
	}
}

// unixSighup() and setSysProcAttr() are defined in platform-specific files:
// - platform_unix.go (Linux/macOS)
// - platform_windows.go (Windows)

// Helper to suppress unused import warnings
func init() {
	_ = strconv.Itoa
}
