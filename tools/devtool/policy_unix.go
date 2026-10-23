//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

func longPathsPolicy() record                        { return record{"supported": false, "value": nil} }
func isAdmin() bool                                  { return false }
func elevateCurrent(root string, args []string) bool { return false }
