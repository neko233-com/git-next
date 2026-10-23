// Copyright 2026 git-next contributors. MIT license.
//go:build linux

package main

import "golang.org/x/sys/unix"

func acceptanceAffinity() (int, func() error, func() error, error) {
	var original unix.CPUSet
	if e := unix.SchedGetaffinity(0, &original); e != nil {
		return 0, nil, nil, e
	}
	var one unix.CPUSet
	for i := 0; i < 1024; i++ {
		if original.IsSet(i) {
			one.Set(i)
			break
		}
	}
	return original.Count(), func() error { return unix.SchedSetaffinity(0, &original) }, func() error { return unix.SchedSetaffinity(0, &one) }, nil
}
