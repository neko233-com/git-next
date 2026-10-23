// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var processJobs sync.Map

func prepareProcess(cmd *exec.Cmd) (func(), error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	limits.BasicLimitInformation.ActiveProcessLimit = 64
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	// A suspended start prevents a forwarding loop from spawning before assignment.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED}
	processJobs.Store(cmd, job)
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	return func() { processJobs.Delete(cmd); windows.TerminateJobObject(job, 1); windows.CloseHandle(job) }, nil
}
func attachProcess(cmd *exec.Cmd) error {
	value, _ := processJobs.Load(cmd)
	handle, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if e != nil {
		return e
	}
	defer windows.CloseHandle(handle)
	if e = windows.AssignProcessToJobObject(value.(windows.Handle), handle); e != nil {
		return e
	}
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(handle))
	if status != 0 {
		return fmt.Errorf("NtResumeProcess: NTSTATUS %08x", status)
	}
	return nil
}
