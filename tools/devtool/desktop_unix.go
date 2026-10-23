//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

import "fmt"

func terminalDesktopAcceptance(a *app, o options) error {
	return fmt.Errorf("native desktop acceptance requires Windows; no static substitute is reported as a pass")
}
