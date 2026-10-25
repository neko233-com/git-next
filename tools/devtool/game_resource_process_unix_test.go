// Copyright 2026 git-next contributors. MIT license.
//go:build !windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func gameResourceTestAlive(pid int) bool {
	if runtime.GOOS == "linux" {
		data, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if e != nil {
			return false
		}
		_, _, state, e := gameResourceProcStat(string(data))
		return e == nil && state != "Z" && state != "X"
	}
	return syscall.Kill(pid, 0) == nil
}

func TestGameResourceProcStatParsesNamesWithoutInventingRSS(t *testing.T) {
	fields := make([]string, 22)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[2], fields[21] = "S", "233", "17"
	group, rss, state, e := gameResourceProcStat("123 (complex ) process name) " + strings.Join(fields, " "))
	if e != nil || group != 233 || state != "S" || rss != 17*uint64(os.Getpagesize()) {
		t.Fatal(group, rss, state, e)
	}
	fields[21] = "-1"
	if _, _, _, e = gameResourceProcStat("123 (fixture) " + strings.Join(fields, " ")); e == nil {
		t.Fatal("negative RSS accepted")
	}
	fields[21] = strconv.FormatInt(1<<62, 10)
	if _, _, _, e = gameResourceProcStat("123 (fixture) " + strings.Join(fields, " ")); e == nil {
		t.Fatal("RSS multiplication overflow accepted")
	}
}
