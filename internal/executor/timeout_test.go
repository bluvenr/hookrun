package executor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Regression for D-03b: with a fork-type shell (dash), ctx expiry only kills
// the direct sh child; a backgrounded grandchild keeps the stdout pipe's
// write end open, so cmd.Wait blocks until the grandchild exits on its own.
// The action timeout must bound the call's duration regardless of shell
// fork behaviour. Skipped on Windows (cmd.exe has no sh -c semantics).
func TestRunShell_TimeoutBoundsDurationWithOrphanChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("orphan-grandchild pipe scenario is POSIX-shell specific")
	}
	// sh forks a backgrounded sleep; the grandchild inherits the stdout
	// pipe write end and outlives the killed shell.
	res := ExecuteCommand("sleep 6 &", 1, false, nil)

	if res.Duration > 4*time.Second {
		t.Errorf("timeout=1s but ExecuteCommand took %v — orphaned child held the stdout pipe", res.Duration)
	}
	if res.Error == nil || !strings.Contains(res.Error.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v (exit=%d)", res.Error, res.ExitCode)
	}
}

// Regression for D-03b follow-up: when the shell exits immediately after
// backgrounding the real work, Go's watchCtx hands off before ctx expiry and
// never calls Cancel, so the process-group kill never fires. WaitDelay bounds
// the call, but the orphan keeps running to its natural end. A timed-out
// action must not leave its work running: the orphan has to be killed too.
// The marker file is written by the orphan ~3s in — well after the 1s
// timeout and the 2s WaitDelay backstop — so its presence proves survival.
func TestRunShell_TimeoutKillsOrphanedWork(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "orphan-marker")
	var cmd string
	if runtime.GOOS == "windows" {
		// Outer cmd stays alive past the deadline (ping ~2s) so Cancel fires;
		// the start-spawned grandchild is detached from the tree taskkill /T
		// walks, so it survives and writes the marker at ~3s.
		cmd = "start /b cmd /c \"ping -n 4 127.0.0.1 >NUL & echo ran > " + marker + "\" & ping -n 3 127.0.0.1 >NUL"
	} else {
		cmd = "{ sleep 3; echo ran > '" + marker + "'; } &"
	}

	res := ExecuteCommand(cmd, 1, false, nil)
	if res.Error == nil || !strings.Contains(res.Error.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v (exit=%d)", res.Error, res.ExitCode)
	}

	// The orphan writes the marker at ~3s; give it ample time to prove
	// whether it survived the timeout.
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("orphaned work survived the action timeout: marker %s was written", marker)
	}
}
