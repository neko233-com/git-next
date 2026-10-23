// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedAssemblyAcceptsReadyToRunMachineAndRequiresCLRDirectory(t *testing.T) {
	data := make([]byte, 64+264)
	copy(data, []byte("MZ"))
	binary.LittleEndian.PutUint32(data[60:], 64)
	header := data[64:]
	copy(header, []byte("PE\x00\x00"))
	binary.LittleEndian.PutUint16(header[4:], 0xfd1d)
	binary.LittleEndian.PutUint16(header[24:], 0x20b)
	binary.LittleEndian.PutUint32(header[132:], 16)
	binary.LittleEndian.PutUint32(header[248:], 0x2000)
	binary.LittleEndian.PutUint32(header[252:], 72)
	path := filepath.Join(t.TempDir(), "ready-to-run.dll")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	if !managedAssembly(path) {
		t.Fatal("managed ARM64 ReadyToRun image rejected")
	}
	binary.LittleEndian.PutUint32(header[248:], 0)
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	if managedAssembly(path) {
		t.Fatal("native image without CLR descriptor accepted")
	}
}
