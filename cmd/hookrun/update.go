package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bluvenr/hookrun/internal/config"
	"github.com/bluvenr/hookrun/internal/daemon"
	"github.com/bluvenr/hookrun/internal/update"
	"github.com/bluvenr/hookrun/internal/version"
	"github.com/spf13/cobra"
)

// ---- update command ----
func updateCmd() *cobra.Command {
	var targetVersion string
	var force bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update HookRun to the latest version",
		Long: `Update HookRun to the latest release (or a specific version) by downloading
the official binary from GitHub Releases and replacing the current executable.

Works on Linux, macOS, and Windows. If the binary lives in a protected location
(e.g. /usr/local/bin), run with sudo (Unix) or as Administrator (Windows).
After updating, run 'hookrun restart' to apply the new version.`,
		Example: `  hookrun update                     # Update to the latest version
  hookrun update --version 1.1.4     # Update (or downgrade) to a specific version
  hookrun update --force             # Reinstall even if already up to date`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(targetVersion, force)
		},
	}

	cmd.Flags().StringVar(&targetVersion, "version", "", "Target version (default: latest)")
	cmd.Flags().BoolVar(&force, "force", false, "Force update even if already up to date")

	return cmd
}

func runUpdate(targetVersion string, force bool) error {
	current := version.Version

	// Resolve the latest version unless a target is given or --force is set.
	if targetVersion == "" && !force {
		fmt.Println("Checking for the latest version...")
		info, err := update.CheckLatest(15 * time.Second)
		if err != nil {
			return fmt.Errorf("check latest version: %w", err)
		}
		if update.CompareSemver(info.Version, current) <= 0 {
			fmt.Printf("Already up to date (v%s).\n", current)
			return nil
		}
		fmt.Printf("New version available: v%s (current: v%s)\n", info.Version, current)
		targetVersion = info.Version
	}

	if targetVersion == "" {
		fmt.Println("Updating to the latest version...")
	} else {
		fmt.Printf("Updating to v%s...\n", strings.TrimPrefix(targetVersion, "v"))
	}

	installed, err := update.SelfUpdate(targetVersion)
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Updated to v%s. Restart the server to apply (hookrun restart).\n", installed)
	return nil
}

// checkForUpdate prints the result of an online version check (version --check).
func checkForUpdate() {
	fmt.Println()
	if update.IsDevBuild() {
		fmt.Println("Note: development build detected; version comparison may be inaccurate.")
	}
	fmt.Println("Checking for updates...")
	info, err := update.CheckLatest(10 * time.Second)
	if err != nil {
		fmt.Printf("Could not check for updates: %v\n", err)
		return
	}
	if update.CompareSemver(info.Version, version.Version) > 0 {
		fmt.Printf("New version available: v%s (current: v%s)\n", info.Version, version.Version)
		fmt.Println("Run 'hookrun update' to upgrade.")
	} else {
		fmt.Printf("You're up to date (v%s).\n", version.Version)
	}
}

// maybeNotifyUpdate prints a non-blocking update reminder on start.
// It is skipped inside containers/CI, for dev builds, or when disabled via config.
func maybeNotifyUpdate() {
	if update.InContainerOrCI() || update.IsDevBuild() {
		return
	}

	cfg := config.Defaults()
	if mgr := config.NewManager(configPath); mgr.Load() == nil {
		cfg = mgr.Global()
	}
	if !cfg.UpdateCheckEnabled() {
		return
	}

	cachePath := filepath.Join(daemon.GetHookRunDir(), "update-check.json")
	if cache, err := readUpdateCache(cachePath); err == nil && cache.LatestVersion != "" {
		if update.CompareSemver(cache.LatestVersion, version.Version) > 0 {
			fmt.Printf("A new version of HookRun is available: v%s (current: v%s). Run 'hookrun update' to upgrade.\n",
				cache.LatestVersion, version.Version)
		}
		// Cache still fresh: no background refresh needed.
		if time.Since(cache.CheckedAt) < 24*time.Hour {
			return
		}
	}

	// Refresh in the background (non-blocking); the result is shown on next start.
	go func() {
		info, err := update.CheckLatest(3 * time.Second)
		if err != nil {
			return
		}
		_ = writeUpdateCache(cachePath, updateCache{
			CheckedAt:     time.Now(),
			LatestVersion: info.Version,
		})
	}()
}

type updateCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

func readUpdateCache(path string) (updateCache, error) {
	var c updateCache
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

func writeUpdateCache(path string, c updateCache) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
