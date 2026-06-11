//go:build windows

package daemon

import (
	"os/exec"
	"strconv"
	"strings"
)

// IsRunning checks if the process with the given PID is still running (Windows).
func IsRunning(pid int) bool {
	// Use tasklist to check if the process exists
	cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// tasklist returns a CSV line if the process exists, or
	// "INFO: No tasks are running..." if not found
	output := strings.TrimSpace(string(out))
	if strings.HasPrefix(output, "INFO:") || output == "" {
		return false
	}
	return strings.Contains(output, strconv.Itoa(pid))
}
