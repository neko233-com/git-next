// Copyright 2026 git-next contributors. MIT license.
//go:build !windows && !linux

package main

import "fmt"

func acceptanceAffinity() (int, func() error, func() error, error) {
	return 0, nil, nil, fmt.Errorf("CPU affinity fixture requires Windows or Linux")
}
