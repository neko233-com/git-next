// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"sync"
	"unsafe"
)

var installer = windows.NewLazySystemDLL("msi.dll")
var msiDatabases sync.Map

//go:uintptrescapes
func msiCall(name string, args ...uintptr) (uintptr, error) {
	code, _, _ := installer.NewProc(name).Call(args...)
	if code != 0 {
		return code, fmt.Errorf("%s: Windows Installer error %d", name, code)
	}
	return code, nil
}
func openMSI(path string) (uintptr, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	var handle uint32
	runtime.LockOSThread()
	_, e = msiCall("MsiOpenDatabaseW", uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&handle)))
	if e != nil {
		runtime.UnlockOSThread()
	} else {
		msiDatabases.Store(uintptr(handle), true)
	}
	return uintptr(handle), e
}
func closeMSI(handle uintptr) {
	_, _, _ = installer.NewProc("MsiCloseHandle").Call(handle)
	if _, ok := msiDatabases.LoadAndDelete(handle); ok {
		runtime.UnlockOSThread()
	}
}
func queryMSI(database uintptr, sql string) ([][]string, error) {
	query, e := windows.UTF16PtrFromString(sql)
	if e != nil {
		return nil, e
	}
	var view uint32
	if _, e = msiCall("MsiDatabaseOpenViewW", database, uintptr(unsafe.Pointer(query)), uintptr(unsafe.Pointer(&view))); e != nil {
		return nil, e
	}
	defer closeMSI(uintptr(view))
	if _, e = msiCall("MsiViewExecute", uintptr(view), 0); e != nil {
		return nil, e
	}
	var rows [][]string
	for {
		var record uint32
		code, e := msiCall("MsiViewFetch", uintptr(view), uintptr(unsafe.Pointer(&record)))
		if code == 259 {
			return rows, nil
		}
		if e != nil {
			return nil, e
		}
		count, _, _ := installer.NewProc("MsiRecordGetFieldCount").Call(uintptr(record))
		if count > 1024 {
			closeMSI(uintptr(record))
			return nil, fmt.Errorf("MSI record exceeds field limit: count=%d record=%d query=%s", count, record, sql)
		}
		row := make([]string, count)
		for i := range count {
			var size uint32
			_, _, _ = installer.NewProc("MsiRecordGetStringW").Call(uintptr(record), i+1, 0, uintptr(unsafe.Pointer(&size)))
			if size > 16<<20 {
				closeMSI(uintptr(record))
				return nil, fmt.Errorf("MSI field too large")
			}
			size++
			buffer := make([]uint16, size)
			if _, e = msiCall("MsiRecordGetStringW", uintptr(record), i+1, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size))); e != nil {
				closeMSI(uintptr(record))
				return nil, e
			}
			row[i] = windows.UTF16ToString(buffer)
		}
		closeMSI(uintptr(record))
		rows = append(rows, row)
	}
}
