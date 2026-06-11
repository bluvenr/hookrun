//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// IsRunning checks if the process with the given PID is still running (Unix).
func IsRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
