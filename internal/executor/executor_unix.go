//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

// configureProcessKill puts the shell in its own process group and kills the
// whole group when the context expires, so forked grandchildren (orphans
// holding the stdout pipe) die with the shell instead of stalling cmd.Wait.
func configureProcessKill(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		return killProcessTree(c)
	}
}

// killProcessTree SIGKILLs the shell's whole process group. Besides Cancel,
// runShell calls it after Wait when the deadline fired: if the shell exited
// before the deadline (e.g. it backgrounded the real work), Go's watchCtx
// hands off without ever calling Cancel, and the orphaned group members must
// be swept explicitly or they keep running to their natural end (D-03b).
// Processes that detached into a new session (Setsid) are not in the group
// and are deliberately left alone.
func killProcessTree(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		// Group already gone — nothing to kill.
		return nil
	}
	return err
}
