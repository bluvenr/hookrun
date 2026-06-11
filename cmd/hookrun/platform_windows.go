//go:build windows

package main

import (
	"os"
	"os/exec"
)

func unixSighup() os.Signal {
	return os.Interrupt
}

func setSysProcAttr(cmd *exec.Cmd) {
	// No-op on Windows
}
