//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func unixSighup() os.Signal {
	return syscall.SIGHUP
}

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}
