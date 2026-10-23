//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

func acquireDesktopLease() (func(), error)            { return func() {}, nil }
func protectTerminalClipboard() (func() error, error) { return func() error { return nil }, nil }
