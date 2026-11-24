//go:build windows

package executor

import (
	"os/exec"
	"strconv"
)

// configureProcessKill kills the whole process tree when the context expires.
// cmd.exe's direct children (e.g. ping) otherwise survive as orphans holding
// the stdout pipe handles, stalling cmd.Wait past the timeout (D-03b).
// taskkill /T walks the tree; errors are ignored because the process may
// already be gone — WaitDelay in runShell is the backstop either way.
func configureProcessKill(c *exec.Cmd) {
	c.Cancel = func() error {
		killProcessTree(c)
		return nil
	}
}

// killProcessTree force-kills the shell's process tree via taskkill /T.
// runShell also calls it after Wait when the deadline fired, covering the
// case where the shell exited early and Cancel never ran (D-03b). Note the
// tree walk follows parent-PID links: once the shell is gone, detached
// descendants (e.g. start-spawned processes re-parented by the kernel) can
// no longer be reached — WaitDelay still bounds the call's duration.
func killProcessTree(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(c.Process.Pid))
	_ = kill.Run()
}
