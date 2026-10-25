// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func gameResourceTestAlive(pid int) bool {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259
}

func TestGameResourceWindowsBudgetViolationIsRealFailure(t *testing.T) {
	limits := gameResourceTestLimits()
	limits.memory = 32 << 20
	p := gameResourceRunPhase(context.Background(), []gameResourceCommand{gameResourceTestSpec("over-budget")}, false, limits)
	if p.Status != "fail" || p.ValidTimingSample || !p.Memory.CleanupOK || !p.Memory.MemoryLimitEnforced {
		t.Fatalf("job budget failure was laundered into a timing sample: %+v", p)
	}
}
