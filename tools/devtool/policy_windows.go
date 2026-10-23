// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func longPathsPolicy() record {
	key, e := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\FileSystem`, registry.QUERY_VALUE)
	if e != nil {
		return record{"supported": true, "value": nil}
	}
	defer key.Close()
	value, _, e := key.GetIntegerValue("LongPathsEnabled")
	if e != nil {
		return record{"supported": true, "value": nil}
	}
	return record{"supported": true, "value": value, "note": "machine policy is reported, never set"}
}
func isAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }
func elevateCurrent(root string, args []string) bool {
	exe, e := os.Executable()
	if e != nil {
		return false
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = syscall.EscapeArg(arg)
	}
	file, e := windows.UTF16PtrFromString(exe)
	if e != nil {
		return false
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	parameters, e := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if e != nil {
		return false
	}
	dir, e := windows.UTF16PtrFromString(root)
	if e != nil {
		return false
	}
	type shellExecuteInfo struct {
		Size                              uint32
		Mask                              uint32
		Window                            windows.Handle
		Verb, File, Parameters, Directory *uint16
		Show                              int32
		Instance                          windows.Handle
		IDList                            uintptr
		Class                             *uint16
		ClassKey                          windows.Handle
		HotKey                            uint32
		Icon                              windows.Handle
		Process                           windows.Handle
	}
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file, Parameters: parameters, Directory: dir}
	info.Size = uint32(unsafe.Sizeof(info))
	proc := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return false
	}
	defer windows.CloseHandle(info.Process)
	wait, e := windows.WaitForSingleObject(info.Process, 300000)
	if e != nil || wait != windows.WAIT_OBJECT_0 {
		return false
	}
	var code uint32
	if e = windows.GetExitCodeProcess(info.Process, &code); e != nil {
		return false
	}
	return code == 0
}
