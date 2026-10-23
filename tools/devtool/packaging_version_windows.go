// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func peNumericVersion(p string) (string, error) {
	name, e := windows.UTF16PtrFromString(p)
	if e != nil {
		return "", e
	}
	library := windows.NewLazySystemDLL("version.dll")
	sizeProc := library.NewProc("GetFileVersionInfoSizeW")
	readProc := library.NewProc("GetFileVersionInfoW")
	queryProc := library.NewProc("VerQueryValueW")
	size, _, _ := sizeProc.Call(uintptr(unsafe.Pointer(name)), 0)
	if size == 0 || size > 16<<20 {
		return "", fmt.Errorf("native core lacks its numeric version resource")
	}
	buffer := make([]byte, size)
	ok, _, e := readProc.Call(uintptr(unsafe.Pointer(name)), 0, size, uintptr(unsafe.Pointer(&buffer[0])))
	if ok == 0 {
		return "", e
	}
	block, _ := windows.UTF16PtrFromString("\\")
	var value uintptr
	var length uint32
	ok, _, e = queryProc.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(block)), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&length)))
	if ok == 0 {
		return "", e
	}
	start := uintptr(unsafe.Pointer(&buffer[0]))
	if length < 52 || value < start || value > start+size || uintptr(length) > start+size-value {
		return "", fmt.Errorf("invalid fixed version resource")
	}
	data := buffer[value-start : value-start+16]
	if binary.LittleEndian.Uint32(data) != 0xfeef04bd {
		return "", fmt.Errorf("invalid fixed version signature")
	}
	majorMinor, buildRevision := binary.LittleEndian.Uint32(data[8:]), binary.LittleEndian.Uint32(data[12:])
	return fmt.Sprintf("%d.%d.%d.%d", majorMinor>>16, majorMinor&65535, buildRevision>>16, buildRevision&65535), nil
}
