//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

import "fmt"

func gitFamily() ([]gitProcess, error) {
	return nil, fmt.Errorf("this supervisor reads the Windows process table")
}
func terminatePID(pid uint32) error { return fmt.Errorf("Windows process supervisor required") }
func processAlive(pid uint32) bool  { return true }
