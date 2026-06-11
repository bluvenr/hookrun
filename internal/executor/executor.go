package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ActionResult holds the result of a command/script execution.
type ActionResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	Error    error
}

// Success returns true if the action completed without error and exit code 0.
func (r *ActionResult) Success() bool {
	return r.Error == nil && r.ExitCode == 0
}

// ExecuteCommand runs an inline shell command.
func ExecuteCommand(cmd string, timeoutSec int, isolate bool) *ActionResult {
	return runShell(cmd, nil, timeoutSec, isolate)
}

// ExecuteScript runs an external script file with optional arguments.
func ExecuteScript(path string, args []string, timeoutSec int, isolate bool) *ActionResult {
	// Build command: script path + args
	fullCmd := path
	if len(args) > 0 {
		fullCmd = path + " " + strings.Join(args, " ")
	}
	return runShell(fullCmd, args, timeoutSec, isolate)
}

// runShell executes a command through the system shell.
func runShell(cmd string, scriptArgs []string, timeoutSec int, isolate bool) *ActionResult {
	start := time.Now()

	// Create context with optional timeout
	var ctx context.Context
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	// Build the command based on platform
	var c *exec.Cmd
	if scriptArgs != nil && len(scriptArgs) > 0 {
		// Script execution: use the script path directly with args
		shell, flag := shellCommand()
		fullCmd := cmd
		c = exec.CommandContext(ctx, shell, flag, fullCmd)
	} else {
		shell, flag := shellCommand()
		c = exec.CommandContext(ctx, shell, flag, cmd)
	}

	// Set up environment
	c.Env = append(os.Environ(), "HOOKRUN=1")

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	// Execute
	err := c.Run()
	duration := time.Since(start)

	result := &ActionResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("command timed out after %d seconds", timeoutSec)
			result.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Error = fmt.Errorf("command exited with code %d", result.ExitCode)
		} else {
			result.Error = err
			result.ExitCode = -1
		}
	}

	return result
}

// shellCommand returns the appropriate shell and flag for the current OS.
func shellCommand() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "sh", "-c"
}
