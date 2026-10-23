//go:build !windows

// Copyright 2026 git-next contributors. MIT license.
package main

func peNumericVersion(p string) (string, error) { return peNumericVersionStatic(p) }
