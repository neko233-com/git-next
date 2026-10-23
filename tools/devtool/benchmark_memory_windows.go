// Copyright 2026 git-next contributors. MIT license.
//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

func benchmarkOS() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("Windows-%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

func benchmarkMemory(pid int) (func() int64, func()) {
	handle, e := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if e != nil {
		return func() int64 { return 0 }, func() {}
	}
	return func() int64 {
		var counters struct {
			Size, PageFaultCount                                                                         uint32
			PeakWorkingSet, WorkingSet, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile uintptr
		}
		counters.Size = uint32(unsafe.Sizeof(counters))
		ok, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo").Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
		if ok == 0 {
			return 0
		}
		return int64(counters.PeakWorkingSet)
	}, func() { _ = windows.CloseHandle(handle) }
}
