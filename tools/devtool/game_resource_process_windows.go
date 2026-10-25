// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type gameResourceJobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime int64
	PageFaults, TotalProcesses, Active, Terminated         uint32
}

func gameResourceLFSHostPreflight() error { return gameResourceLFSPlatform("windows") }

type gameResourceWindowsScope struct {
	mu      sync.Mutex
	job     windows.Handle
	limits  gameResourceLimits
	stopped bool
	end     chan struct{}
	done    chan struct{}
	metric  gameResourceMemory
}

func newGameResourceScope(limits gameResourceLimits) (gameResourceScope, error) {
	if limits.processes < 1 || limits.processes > 128 || limits.memory < 1 || limits.memory > uint64(^uintptr(0)) {
		return nil, fmt.Errorf("invalid resource job limits")
	}
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_JOB_MEMORY
	info.BasicLimitInformation.ActiveProcessLimit, info.JobMemoryLimit = uint32(limits.processes), uintptr(limits.memory)
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	s := &gameResourceWindowsScope{job: job, limits: limits, end: make(chan struct{}), done: make(chan struct{})}
	cap := limits.memory
	s.metric = gameResourceMemory{Available: true, Metric: "windows_job_peak_commit_bytes", Scope: "Kernel PeakJobMemoryUsed for all commands and descendants in this shared phase job; parent benchmark orchestrator excluded; committed private memory, not RSS", CleanupScope: "All processes in this owned phase job", MemoryLimitBytes: &cap, MemoryLimitEnforced: true, ProcessLimit: limits.processes, ProcessLimitKernel: true, SampleIntervalMS: 20}
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

func (s *gameResourceWindowsScope) sampleLocked() uint32 {
	var extended windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var accounting gameResourceJobAccounting
	if e := windows.QueryInformationJobObject(s.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&extended)), uint32(unsafe.Sizeof(extended)), nil); e != nil {
		s.metric.Available, s.metric.Error = false, e.Error()
		return 0
	}
	if e := windows.QueryInformationJobObject(s.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); e != nil {
		s.metric.Available, s.metric.Error = false, e.Error()
		return 0
	}
	peak := uint64(extended.PeakJobMemoryUsed)
	if s.metric.PeakBytes == nil || peak > *s.metric.PeakBytes {
		s.metric.PeakBytes = &peak
	}
	s.metric.PeakProcesses = max(s.metric.PeakProcesses, accounting.Active)
	s.metric.Samples++
	return accounting.Active
}

func (s *gameResourceWindowsScope) start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return fmt.Errorf("phase job is stopping")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED}
	if e := cmd.Start(); e != nil {
		return e
	}
	handle, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if e == nil {
		defer windows.CloseHandle(handle)
		e = windows.AssignProcessToJobObject(s.job, handle)
	}
	if e == nil {
		status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(handle))
		if status != 0 {
			e = fmt.Errorf("resource job NtResumeProcess: NTSTATUS %08x", status)
		}
	}
	if e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return e
	}
	s.sampleLocked()
	return nil
}

func (s *gameResourceWindowsScope) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	return windows.TerminateJobObject(s.job, 1)
}

func (s *gameResourceWindowsScope) finish() gameResourceMemory {
	close(s.end)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metric.Remaining = s.sampleLocked()
	s.stopped = true
	if e := windows.TerminateJobObject(s.job, 1); e != nil {
		s.metric.Error = e.Error()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		active := s.sampleLocked()
		if active == 0 {
			s.metric.CleanupOK = s.metric.Error == ""
			break
		}
		if time.Now().After(deadline) {
			s.metric.Error = "owned job processes remained after termination"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = windows.CloseHandle(s.job)
	s.job = 0
	return s.metric
}
