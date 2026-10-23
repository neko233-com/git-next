// Copyright 2026 git-next contributors. MIT license.
package main

import "golang.org/x/sys/windows"

func setSystemFileAttribute(path string) error {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	return windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_SYSTEM)
}
