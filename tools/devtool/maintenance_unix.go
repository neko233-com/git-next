//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

import "fmt"

func recordedInstallRoot(string) string { return "" }
func listBridgeRegistrations() ([]bridgeRegistration, error) {
	return nil, fmt.Errorf("Windows registry required")
}
func removeBridgeRegistration(bridgeRegistration) error {
	return fmt.Errorf("Windows registry required")
}
func uninstallEntries(string) (record, error) { return nil, fmt.Errorf("Windows registry required") }
func msiProductState(string) int32            { return -2 }
func msiRelatedProducts(string) ([]string, error) {
	return nil, fmt.Errorf("Windows Installer required")
}
func verifyBridgeScenarios(*app, options) error {
	return fmt.Errorf("bridge runtime acceptance requires Windows")
}
