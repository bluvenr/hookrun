package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PIDFileName is the name of the PID file.
const PIDFileName = "hookrun.pid"

// Signal file names for Windows IPC.
const (
	ReloadSignalFile = "reload.signal"
	StopSignalFile   = "stop.signal"
	StatusSignalFile = "status.signal"
)

// GetHookRunDir returns the HookRun data directory (~/.hookrun).
func GetHookRunDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".hookrun")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// WritePID writes the current process PID to the PID file.
func WritePID() error {
	pid := os.Getpid()
	pidFile := filepath.Join(GetHookRunDir(), PIDFileName)
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// ReadPID reads the PID from the PID file.
func ReadPID() (int, error) {
	pidFile := filepath.Join(GetHookRunDir(), PIDFileName)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, fmt.Errorf("cannot read PID file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in file: %w", err)
	}
	return pid, nil
}

// RemovePID removes the PID file.
func RemovePID() {
	pidFile := filepath.Join(GetHookRunDir(), PIDFileName)
	_ = os.Remove(pidFile)
}

// IsRunning is defined in platform-specific files:
// - daemon_unix.go
// - daemon_windows.go

// WriteSignalFile creates a signal file for Windows IPC.
func WriteSignalFile(signal string) error {
	signalFile := filepath.Join(GetHookRunDir(), signal)
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	return os.WriteFile(signalFile, []byte(timestamp), 0644)
}

// CheckSignalFile checks if a signal file exists and removes it.
// Returns true if the signal was found.
func CheckSignalFile(signal string) bool {
	signalFile := filepath.Join(GetHookRunDir(), signal)
	if _, err := os.Stat(signalFile); err == nil {
		_ = os.Remove(signalFile)
		return true
	}
	return false
}

// GetStartTime reads the process start time.
func GetStartTime(pid int) (time.Time, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return time.Time{}, err
	}
	_ = process // Platform-specific implementation would go here
	return time.Time{}, fmt.Errorf("not implemented for this platform")
}

// WriteStatus writes current status info for the status command to read.
func WriteStatus(port int, ruleCount int) error {
	statusFile := filepath.Join(GetHookRunDir(), "status.json")
	content := fmt.Sprintf(`{"pid":%d,"port":%d,"rules":%d,"start_time":"%s"}`,
		os.Getpid(), port, ruleCount, time.Now().Format(time.RFC3339))
	return os.WriteFile(statusFile, []byte(content), 0644)
}

// ReadStatus reads the current status info.
func ReadStatus() (string, error) {
	statusFile := filepath.Join(GetHookRunDir(), "status.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
