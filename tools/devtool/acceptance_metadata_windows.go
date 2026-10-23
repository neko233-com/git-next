// Copyright 2026 git-next contributors. MIT license.
//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

type directoryMetadata struct {
	Creation, Write, Size uint64
	Attributes, Tag       uint32
}

func enumerateWindowsMetadata(directory string, class uint32, nameOffset int) (map[string]directoryMetadata, error) {
	name, e := windows.UTF16PtrFromString(directory)
	if e != nil {
		return nil, e
	}
	handle, e := windows.CreateFile(name, 0x00100001, 7, nil, 3, 0x02000000, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(handle)
	query := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetFileInformationByHandleEx")
	buffer := make([]uint64, 8192)
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&buffer[0])), len(buffer)*8)
	entries := map[string]directoryMetadata{}
	for {
		ok, _, err := query.Call(uintptr(handle), uintptr(class), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(raw)))
		if ok == 0 {
			if err == windows.ERROR_NO_MORE_FILES {
				return entries, nil
			}
			return nil, err
		}
		offset := 0
		for {
			if offset+nameOffset > len(raw) {
				return nil, fmt.Errorf("directory metadata header exceeds buffer")
			}
			part := raw[offset:]
			next := int(binary.LittleEndian.Uint32(part))
			length := int(binary.LittleEndian.Uint32(part[60:]))
			if length%2 != 0 || offset+nameOffset+length > len(raw) {
				return nil, fmt.Errorf("directory metadata name exceeds buffer")
			}
			units := make([]uint16, length/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(part[nameOffset+i*2:])
			}
			filename := string(utf16.Decode(units))
			if filename != "." && filename != ".." {
				if _, exists := entries[filename]; exists {
					return nil, fmt.Errorf("duplicate directory entry")
				}
				entries[filename] = directoryMetadata{binary.LittleEndian.Uint64(part[8:]), binary.LittleEndian.Uint64(part[24:]), binary.LittleEndian.Uint64(part[40:]), binary.LittleEndian.Uint32(part[56:]), binary.LittleEndian.Uint32(part[64:])}
			}
			if next == 0 {
				break
			}
			if next < nameOffset || offset+next >= len(raw) {
				return nil, fmt.Errorf("directory next offset invalid")
			}
			offset += next
		}
	}
}
func windowsMetadata(a *app, o options) error {
	root, e := os.MkdirTemp("", "git-next-metadata-api-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	count := 0
	symlinkTested := false
	r := acceptCapture("directory-metadata-equivalence", func() {
		acceptWrite(filepath.Join(root, "plain.txt"), []byte("content\n"))
		acceptWrite(filepath.Join(root, "empty.txt"), nil)
		acceptWrite(filepath.Join(root, "nämé-🚀.txt"), []byte("unicode\n"))
		acceptWrite(filepath.Join(root, "target/nested.txt"), []byte("nested\n"))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := filepath.Join(os.Getenv("SystemRoot"), "System32/cmd.exe")
		p, e := runProcess(ctx, root, nil, nil, command, "/d", "/c", "mklink", "/J", filepath.Join(root, "junction"), filepath.Join(root, "target"))
		acceptMust(e)
		acceptCheck(p.code == 0, "junction fixture creation failed")
		acceptMust(os.Link(filepath.Join(root, "plain.txt"), filepath.Join(root, "hardlink.txt")))
		if e = os.Symlink(filepath.Join(root, "plain.txt"), filepath.Join(root, "symlink.txt")); e == nil {
			symlinkTested = true
		} else {
			pathErr, ok := e.(*os.LinkError)
			acceptCheck(ok && pathErr.Err == syscall.Errno(1314), "symlink fixture error %v", e)
		}
		full, e := enumerateWindowsMetadata(root, 14, 68)
		acceptMust(e)
		both, e := enumerateWindowsMetadata(root, 10, 104)
		acceptMust(e)
		acceptCheck(equalJSON(full, both), "directory-only metadata differs from prior MFT query")
		count = len(full)
		for name, value := range full {
			ptr, e := windows.UTF16PtrFromString(filepath.Join(root, name))
			acceptMust(e)
			var live windows.Win32finddata
			handle, e := windows.FindFirstFile(ptr, &live)
			acceptMust(e)
			acceptMust(windows.FindClose(handle))
			created := uint64(live.CreationTime.HighDateTime)<<32 | uint64(live.CreationTime.LowDateTime)
			modified := uint64(live.LastWriteTime.HighDateTime)<<32 | uint64(live.LastWriteTime.LowDateTime)
			size := uint64(live.FileSizeHigh)<<32 | uint64(live.FileSizeLow)
			acceptCheck(value.Attributes == live.FileAttributes && value.Tag == live.Reserved0 && value.Creation == created && value.Write == modified, "live metadata differs for %s", name)
			if value.Tag == 0 {
				acceptCheck(value.Size == size, "live size differs for %s", name)
			}
		}
		acceptCheck(full["junction"].Tag == 0xA0000003, "junction reparse tag missing")
	})
	return acceptReport(a, o, record{"status": r["status"], "results": []record{r}, "scope": "Windows filesystem API metadata equivalence only; product conformance is separate", "harness_sha256": acceptHash(a.path("tools/devtool/acceptance_metadata_windows.go")), "entries_compared": count, "full_matches_prior_query_and_live_metadata": r["status"] == "pass", "unicode_empty_regular_directory_hardlink": true, "actual_junction_reparse_tag": true, "actual_symlink_tested": symlinkTested, "user_files_modified": false})
}
func init() {
	register("windows-metadata-api-acceptance", "Directory-only Win32 metadata versus prior query and live filesystem", "output", windowsMetadata)
}
