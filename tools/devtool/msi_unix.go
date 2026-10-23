//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

import "fmt"

func openMSI(path string) (uintptr, error) {
	return 0, fmt.Errorf("MSI database inspection requires Windows")
}
func closeMSI(handle uintptr) {}
func queryMSI(database uintptr, sql string) ([][]string, error) {
	return nil, fmt.Errorf("MSI API requires Windows")
}
