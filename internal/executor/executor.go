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

// ExecuteCommand runs an inline shell command with optional extra env vars.
func ExecuteCommand(cmd string, timeoutSec int, isolate bool, envVars []string) *ActionResult {
	return runShell(cmd, nil, timeoutSec, isolate, envVars)
}

// ExecuteScript runs an external script file with optional arguments and env vars.
func ExecuteScript(path string, args []string, timeoutSec int, isolate bool, envVars []string) *ActionResult {
	return runShell(path, args, timeoutSec, isolate, envVars)
}

// runShell executes a command through the system shell.
func runShell(cmd string, scriptArgs []string, timeoutSec int, isolate bool, envVars []string) *ActionResult {
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
	shell, flag := shellCommand()
	var c *exec.Cmd
	if scriptArgs != nil && len(scriptArgs) > 0 {
		// Script execution: pass args as separate arguments (safe for values with spaces)
		shellCmd := cmd + " " + buildQuotedArgs(scriptArgs)
		c = exec.CommandContext(ctx, shell, flag, shellCmd)
	} else {
		c = exec.CommandContext(ctx, shell, flag, cmd)
	}

	// Set up environment: system env + HOOKRUN marker + custom env vars
	c.Env = append(os.Environ(), "HOOKRUN=1")
	c.Env = append(c.Env, envVars...)

	// Kill the whole process tree on timeout, not just the direct shell
	// child: a forked grandchild (e.g. "sleep 10" under dash) otherwise
	// survives as an orphan holding the stdout pipe's write end, stalling
	// cmd.Wait far past the timeout (D-03b). WaitDelay is the backstop that
	// force-closes the pipes if anything still holds them open.
	configureProcessKill(c)
	c.WaitDelay = 2 * time.Second

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	// Execute
	err := c.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		// If the shell exited before the deadline (it backgrounded the real
		// work), Go's watchCtx never calls Cancel, so sweep the tree/group
		// explicitly — otherwise the orphaned work keeps running (D-03b).
		killProcessTree(c)
	}

	result := &ActionResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if ctx.Err() == context.DeadlineExceeded {
		// The deadline fired even if the shell itself exited 0 (e.g. it
		// backgrounded the real work and left an orphan holding the pipes).
		result.Error = fmt.Errorf("command timed out after %d seconds", timeoutSec)
		result.ExitCode = -1
	} else if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
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

// buildQuotedArgs joins arguments with proper quoting for shell execution.
func buildQuotedArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quoteShellArg(arg)
	}
	return strings.Join(quoted, " ")
}

// QuoteShellArg quotes a single argument for safe inclusion in a shell
// command line. Values consisting only of safe characters are returned
// unchanged so ordinary values stay readable.
func QuoteShellArg(arg string) string {
	if arg != "" && isShellSafe(arg) {
		return arg
	}
	return quoteShellArg(arg)
}

// isShellSafe reports whether the argument contains only characters that
// need no quoting in sh or cmd.exe.
func isShellSafe(arg string) bool {
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == '=' || r == ':' || r == ',':
		default:
			return false
		}
	}
	return true
}

// quoteShellArg always wraps the argument in quotes with proper escaping.
func quoteShellArg(arg string) string {
	if runtime.GOOS == "windows" {
		// cmd.exe treats "" as a literal quote inside a quoted string
		return `"` + strings.ReplaceAll(arg, `"`, `""`) + `"`
	}
	// Unix sh uses single quotes
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
