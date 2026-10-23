// Copyright 2026 git-next contributors. MIT license.
//go:build !windows

package main

import (
	"context"
	"runtime"
	"strings"
	"time"
)

func benchmarkOS() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, e := runProcess(ctx, "/", nil, nil, "uname", "-sr")
	if e == nil && value.code == 0 {
		return strings.TrimSpace(string(value.stdout))
	}
	return runtime.GOOS
}

func benchmarkMemory(pid int) (func() int64, func()) { return func() int64 { return 0 }, func() {} }
