// Copyright 2026 git-next contributors. MIT license.
//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func prepareProcess(cmd *exec.Cmd) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stop := func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Cancel = stop
	return func() { _ = stop() }, nil
}
func attachProcess(cmd *exec.Cmd) error { return nil }
