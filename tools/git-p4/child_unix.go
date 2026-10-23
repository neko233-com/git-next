//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os/exec"
	"syscall"
)

func childProcess(cmd *exec.Cmd) (func() error, func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.Cancel = kill
	return func() error { return nil }, func() { _ = kill() }, nil
}
