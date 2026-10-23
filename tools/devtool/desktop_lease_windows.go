// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

func acquireDesktopLease() (func(), error) {
	runtime.LockOSThread()
	name, e := windows.UTF16PtrFromString(`Local\git-next-acceptance-desktop`)
	if e != nil {
		runtime.UnlockOSThread()
		return nil, e
	}
	handle, e := windows.CreateMutex(nil, false, name)
	if e != nil && e != windows.ERROR_ALREADY_EXISTS {
		runtime.UnlockOSThread()
		return nil, e
	}
	status, e := windows.WaitForSingleObject(handle, 600000)
	if e != nil || status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED {
		windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("cannot acquire exclusive acceptance desktop: status=%d: %v", status, e)
	}
	return func() { _ = windows.ReleaseMutex(handle); _ = windows.CloseHandle(handle); runtime.UnlockOSThread() }, nil
}

func protectTerminalClipboard() (func() error, error) {
	release, e := acquireDesktopLease()
	if e != nil {
		return nil, e
	}
	runtime.LockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("")
	window := desktopCall(desktopUser, "CreateWindowExW", 0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if window == 0 {
		runtime.UnlockOSThread()
		release()
		return nil, fmt.Errorf("cannot create clipboard restoration owner")
	}
	snapshot := &clipboardSnapshot{}
	if e = snapshot.capture(); e != nil {
		snapshot.close()
		desktopCall(desktopUser, "DestroyWindow", window)
		runtime.UnlockOSThread()
		release()
		return nil, e
	}
	if e = desktopSetClipboard(window, "git-next Rust unit clipboard fixture"); e != nil {
		_ = snapshot.restore(window)
		snapshot.close()
		desktopCall(desktopUser, "DestroyWindow", window)
		runtime.UnlockOSThread()
		release()
		return nil, e
	}
	return func() error {
		defer release()
		defer runtime.UnlockOSThread()
		defer desktopCall(desktopUser, "DestroyWindow", window)
		defer snapshot.close()
		return snapshot.restore(window)
	}, nil
}
