// Copyright 2026 git-next contributors. MIT license.
//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type gameResourceUnixScope struct {
	mu       sync.Mutex
	groups   map[int]bool
	children map[int]bool
	stopped  bool
	limits   gameResourceLimits
	end      chan struct{}
	done     chan struct{}
	metric   gameResourceMemory
}

func gameResourceLFSHostPreflight() error {
	if e := gameResourceLFSPlatform(runtime.GOOS); e != nil {
		return e
	}
	data, e := os.ReadFile("/proc/self/stat")
	if e != nil {
		return fmt.Errorf("Linux LFS resource phases require readable /proc: %w", e)
	}
	if _, _, _, e = gameResourceProcStat(string(data)); e != nil {
		return fmt.Errorf("Linux LFS resource phases require usable /proc: %w", e)
	}
	return nil
}

func newGameResourceScope(limits gameResourceLimits) (gameResourceScope, error) {
	if limits.processes < 1 || limits.processes > 128 {
		return nil, fmt.Errorf("invalid resource process limit")
	}
	s := &gameResourceUnixScope{groups: map[int]bool{}, children: map[int]bool{}, limits: limits, end: make(chan struct{}), done: make(chan struct{})}
	s.metric = gameResourceMemory{Metric: "linux_owned_sessions_sampled_sum_rss_bytes", Scope: "20ms /proc samples across owned sessions, including descendant process groups; shared pages counted per process; short-lived peaks can be missed; deliberately escaped sessions are outside this metric", CleanupScope: "Observed process groups within owned sessions; deliberately escaped sessions are outside verification", SampleIntervalMS: 20, ProcessLimit: limits.processes}
	if runtime.GOOS == "linux" {
		if info, e := os.Stat("/proc/self/stat"); e == nil && !info.IsDir() {
			s.metric.Available = true
		} else {
			s.metric.UnavailableReason = "Linux /proc is unavailable"
		}
	} else {
		s.metric.Metric = "unavailable"
		s.metric.Scope = "Memory unavailable; only root process groups can be controlled on this operating system"
		s.metric.CleanupScope = "Root process groups only; newly created descendant groups are outside verification"
		s.metric.UnavailableReason = "No process-tree RSS sampler on this operating system; unavailable is not zero"
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				s.sampleLocked()
				s.mu.Unlock()
			case <-s.end:
				return
			}
		}
	}()
	return s, nil
}

func gameResourceProcStat(data string) (group int, rss uint64, state string, err error) {
	end := strings.LastIndexByte(data, ')')
	if end < 0 {
		return 0, 0, "", fmt.Errorf("invalid proc stat command field")
	}
	fields := strings.Fields(data[end+1:])
	if len(fields) < 22 {
		return 0, 0, "", fmt.Errorf("short proc stat")
	}
	group, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, "", err
	}
	pages, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil || pages < 0 {
		return 0, 0, "", fmt.Errorf("invalid proc RSS pages")
	}
	if uint64(pages) > ^uint64(0)/uint64(os.Getpagesize()) {
		return 0, 0, "", fmt.Errorf("proc RSS overflow")
	}
	return group, uint64(pages) * uint64(os.Getpagesize()), fields[0], nil
}

func (s *gameResourceUnixScope) sampleLocked() uint32 {
	if runtime.GOOS != "linux" || !s.metric.Available {
		var count uint32
		for group := range s.groups {
			if e := syscall.Kill(-group, 0); e == nil || e == syscall.EPERM {
				count++
			}
		}
		return count
	}
	entries, e := os.ReadDir("/proc")
	if e != nil {
		s.metric.Available, s.metric.UnavailableReason = false, "proc directory read failed"
		return 0
	}
	var total uint64
	var count uint32
	for _, entry := range entries {
		if _, e := strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		data, e := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if e != nil {
			continue // Processes may disappear between directory and stat reads.
		}
		group, rss, state, e := gameResourceProcStat(string(data))
		end := strings.LastIndexByte(string(data), ')')
		if e != nil || end < 0 {
			continue
		}
		fields := strings.Fields(string(data[end+1:]))
		session, parseErr := strconv.Atoi(fields[3])
		if parseErr != nil || !s.groups[session] || state == "Z" || state == "X" {
			continue
		}
		s.children[group] = true
		if ^uint64(0)-total < rss {
			s.metric.Error = "aggregate RSS overflow"
			continue
		}
		total += rss
		count++
	}
	s.metric.Samples++
	if count != 0 && (s.metric.PeakBytes == nil || total > *s.metric.PeakBytes) {
		value := total
		s.metric.PeakBytes = &value
	}
	s.metric.PeakProcesses = max(s.metric.PeakProcesses, count)
	if int(count) > s.limits.processes {
		s.metric.Error = "sampled owned process count exceeded the phase limit"
		s.stopped = true
		for group := range s.children {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
	}
	return count
}

func (s *gameResourceUnixScope) start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return fmt.Errorf("phase process groups are stopping")
	}
	// Child controllers may create their own process groups; a session keeps them observable.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e := cmd.Start(); e != nil {
		return e
	}
	s.groups[cmd.Process.Pid] = true
	s.children[cmd.Process.Pid] = true
	s.sampleLocked()
	return nil
}

func (s *gameResourceUnixScope) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.sampleLocked()
	var result error
	for group := range s.children {
		if e := syscall.Kill(-group, syscall.SIGKILL); e != nil && e != syscall.ESRCH {
			result = errors.Join(result, e)
		}
	}
	return result
}

func (s *gameResourceUnixScope) finish() gameResourceMemory {
	close(s.end)
	<-s.done
	s.mu.Lock()
	s.metric.Remaining = s.sampleLocked()
	s.mu.Unlock()
	if e := s.stop(); e != nil {
		s.metric.Error = e.Error()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		left := s.sampleLocked()
		s.mu.Unlock()
		if left == 0 {
			s.metric.CleanupOK = s.metric.Error == ""
			break
		}
		if time.Now().After(deadline) {
			s.metric.Error = "owned process groups remained after SIGKILL"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.metric.PeakBytes == nil {
		s.metric.Available = false
		if s.metric.UnavailableReason == "" {
			s.metric.UnavailableReason = "No owned live process was observed by the sampler"
		}
	}
	return s.metric
}
