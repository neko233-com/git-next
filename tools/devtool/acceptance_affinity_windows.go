// Copyright 2026 git-next contributors. MIT license.
//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"math/bits"
	"unsafe"
)

func acceptanceAffinity() (int, func() error, func() error, error) {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	get := kernel.NewProc("GetProcessAffinityMask")
	set := kernel.NewProc("SetProcessAffinityMask")
	var mask, system uintptr
	handle := windows.CurrentProcess()
	ok, _, err := get.Call(uintptr(handle), uintptr(unsafe.Pointer(&mask)), uintptr(unsafe.Pointer(&system)))
	if ok == 0 || mask == 0 {
		return 0, nil, nil, fmt.Errorf("GetProcessAffinityMask: %w", err)
	}
	apply := func(value uintptr) error {
		ok, _, err := set.Call(uintptr(handle), value)
		if ok == 0 {
			return fmt.Errorf("SetProcessAffinityMask: %w", err)
		}
		return nil
	}
	return bits.OnesCount64(uint64(mask)), func() error { return apply(mask) }, func() error { return apply(mask & -mask) }, nil
}
