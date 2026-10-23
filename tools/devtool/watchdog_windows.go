// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"golang.org/x/sys/windows"
	"os"
	"strings"
	"time"
	"unsafe"
)

func gitFamily() ([]gitProcess, error) {
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	var found []gitProcess
	for e = windows.Process32First(snapshot, &entry); e == nil; e = windows.Process32Next(snapshot, &entry) {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if entry.ProcessID == uint32(os.Getpid()) || (name != "git.exe" && name != "git-next.exe" && name != "git-lfs.exe") {
			continue
		}
		item := gitProcess{PID: entry.ProcessID, Parent: entry.ParentProcessID, Name: name}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err == nil {
			buffer := make([]uint16, 32768)
			size := uint32(len(buffer))
			if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) == nil {
				item.Path = windows.UTF16ToString(buffer[:size])
			}
			var created, exited, kernel, user windows.Filetime
			if windows.GetProcessTimes(handle, &created, &exited, &kernel, &user) == nil {
				item.Started = time.Unix(0, created.Nanoseconds())
			}
			windows.CloseHandle(handle)
		}
		found = append(found, item)
	}
	if e != windows.ERROR_NO_MORE_FILES {
		return nil, e
	}
	return found, nil
}
func terminatePID(pid uint32) error {
	handle, e := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(handle)
	return windows.TerminateProcess(handle, 1)
}
func processAlive(pid uint32) bool {
	handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return e == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if windows.GetExitCodeProcess(handle, &code) != nil {
		return true
	}
	return code == 259
}
