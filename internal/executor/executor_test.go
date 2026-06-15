package executor

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// --- ActionResult.Success ---

func TestActionResult_Success_True(t *testing.T) {
	r := &ActionResult{ExitCode: 0, Error: nil}
	if !r.Success() {
		t.Error("exit code 0 + nil error should be success")
	}
}

func TestActionResult_Success_FalseOnError(t *testing.T) {
	r := &ActionResult{ExitCode: 0, Error: os.ErrNotExist}
	if r.Success() {
		t.Error("error present should not be success")
	}
}

func TestActionResult_Success_FalseOnExitCode(t *testing.T) {
	r := &ActionResult{ExitCode: 1, Error: nil}
	if r.Success() {
		t.Error("non-zero exit code should not be success")
	}
}

// --- ExecuteCommand: success ---

func TestExecuteCommand_EchoHello(t *testing.T) {
	result := ExecuteCommand("echo hello", 10, false, nil)
	if !result.Success() {
		t.Fatalf("echo should succeed: %v", result.Error)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	out := strings.TrimSpace(result.Stdout)
	if out != "hello" {
		t.Errorf("expected 'hello', got '%s'", out)
	}
	if result.Duration <= 0 {
		t.Error("duration should be > 0")
	}
}

// --- ExecuteCommand: failure (non-zero exit code) ---

func TestExecuteCommand_ExitCode1(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "exit 1"
	} else {
		cmd = "exit 1"
	}
	result := ExecuteCommand(cmd, 10, false, nil)
	if result.Success() {
		t.Error("exit 1 should not be success")
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
}

func TestExecuteCommand_ExitCode42(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "exit 42"
	} else {
		cmd = "exit 42"
	}
	result := ExecuteCommand(cmd, 10, false, nil)
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

// --- ExecuteCommand: timeout ---

func TestExecuteCommand_Timeout(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 10 127.0.0.1" // sleep ~10s on Windows
	} else {
		cmd = "sleep 10"
	}
	result := ExecuteCommand(cmd, 1, false, nil) // 1 second timeout
	if result.Success() {
		t.Error("timed out command should not be success")
	}
	if result.ExitCode != -1 {
		t.Errorf("timed out exit code should be -1, got %d", result.ExitCode)
	}
	if result.Error == nil {
		t.Error("timed out command should have error")
	}
	if !strings.Contains(result.Error.Error(), "timed out") {
		t.Errorf("error should mention timeout, got: %v", result.Error)
	}
}

// --- ExecuteCommand: HOOKRUN env var ---

func TestExecuteCommand_HookrunEnvVar(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "echo %HOOKRUN%"
	} else {
		cmd = "echo $HOOKRUN"
	}
	result := ExecuteCommand(cmd, 10, false, nil)
	if !result.Success() {
		t.Fatalf("echo env var should succeed: %v", result.Error)
	}
	out := strings.TrimSpace(result.Stdout)
	if out != "1" {
		t.Errorf("expected HOOKRUN=1, got '%s'", out)
	}
}

// --- ExecuteCommand: stderr capture ---

func TestExecuteCommand_StderrCapture(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "echo errormsg 1>&2"
	} else {
		cmd = "echo errormsg >&2"
	}
	result := ExecuteCommand(cmd, 10, false, nil)
	// On some systems this still exits 0; we just check stderr is captured
	if !strings.Contains(result.Stderr, "errormsg") {
		t.Errorf("stderr should contain 'errormsg', got '%s'", result.Stderr)
	}
}

// --- ExecuteCommand: invalid command ---

func TestExecuteCommand_InvalidCommand(t *testing.T) {
	result := ExecuteCommand("nonexistent_command_xyz_12345", 10, false, nil)
	if result.Success() {
		t.Error("nonexistent command should fail")
	}
	if result.Error == nil {
		t.Error("should have error for nonexistent command")
	}
}

// --- ExecuteScript ---

func TestExecuteScript_NoArgs(t *testing.T) {
	// Create a temp script
	dir := t.TempDir()
	var scriptPath string
	var content string
	if runtime.GOOS == "windows" {
		scriptPath = dir + "\\test.bat"
		content = "@echo scripted"
	} else {
		scriptPath = dir + "/test.sh"
		content = "#!/bin/sh\necho scripted"
	}
	if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	result := ExecuteScript(scriptPath, nil, 10, false, nil)
	if !result.Success() {
		t.Fatalf("script should succeed: %v", result.Error)
	}
	out := strings.TrimSpace(result.Stdout)
	if out != "scripted" {
		t.Errorf("expected 'scripted', got '%s'", out)
	}
}

// --- buildQuotedArgs ---

func TestBuildQuotedArgs_SingleArg(t *testing.T) {
	result := buildQuotedArgs([]string{"hello world"})
	if runtime.GOOS == "windows" {
		if result != `"hello world"` {
			t.Errorf("expected double-quoted, got '%s'", result)
		}
	} else {
		if result != "'hello world'" {
			t.Errorf("expected single-quoted, got '%s'", result)
		}
	}
}

func TestBuildQuotedArgs_MultipleArgs(t *testing.T) {
	result := buildQuotedArgs([]string{"a", "b c"})
	if runtime.GOOS == "windows" {
		if result != `"a" "b c"` {
			t.Errorf("unexpected result: '%s'", result)
		}
	} else {
		if result != "'a' 'b c'" {
			t.Errorf("unexpected result: '%s'", result)
		}
	}
}

func TestBuildQuotedArgs_EmptySlice(t *testing.T) {
	result := buildQuotedArgs([]string{})
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

// --- shellCommand ---

func TestShellCommand(t *testing.T) {
	shell, flag := shellCommand()
	if runtime.GOOS == "windows" {
		if shell != "cmd" || flag != "/c" {
			t.Errorf("expected cmd /c on windows, got %s %s", shell, flag)
		}
	} else {
		if shell != "sh" || flag != "-c" {
			t.Errorf("expected sh -c on unix, got %s %s", shell, flag)
		}
	}
}
