// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/binary"
	"path/filepath"
	"testing"
)

func importImage(library string) []byte {
	b := make([]byte, 0x600)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x84:], 0x8664)
	binary.LittleEndian.PutUint16(b[0x86:], 1)
	binary.LittleEndian.PutUint16(b[0x94:], 0xf0)
	o := b[0x98:]
	binary.LittleEndian.PutUint16(o, 0x20b)
	binary.LittleEndian.PutUint64(o[24:], 0x140000000)
	binary.LittleEndian.PutUint32(o[108:], 16)
	binary.LittleEndian.PutUint32(o[120:], 0x1000)
	binary.LittleEndian.PutUint32(o[124:], 40)
	s := b[0x188:]
	copy(s, ".idata")
	binary.LittleEndian.PutUint32(s[8:], 0x400)
	binary.LittleEndian.PutUint32(s[12:], 0x1000)
	binary.LittleEndian.PutUint32(s[16:], 0x400)
	binary.LittleEndian.PutUint32(s[20:], 0x200)
	binary.LittleEndian.PutUint32(b[0x20c:], 0x1100)
	copy(b[0x300:], library+"\x00")
	return b
}
func TestPEImportTableIsRead(t *testing.T) {
	b := importImage("KERNEL32.dll")
	machine, imports, e := peDependencies(b)
	if e != nil {
		t.Fatal(e)
	}
	if machine != 0x8664 || !equalJSON(imports, []string{"kernel32.dll"}) {
		t.Fatalf("ISA=%x imports=%v", machine, imports)
	}
	root := t.TempDir()
	file := filepath.Join(root, "foreign.exe")
	if e = write(file, importImage("python314.dll"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = systemImports(file); e == nil {
		t.Fatal("foreign runtime import was silently accepted")
	}
	for _, name := range []string{"../kernel32.dll", "C:kernel32.dll", "kernel32"} {
		if _, _, e = peDependencies(importImage(name)); e == nil {
			t.Fatalf("unsafe import accepted: %s", name)
		}
	}
	broken := importImage("kernel32.dll")
	binary.LittleEndian.PutUint32(broken[0x20c:], 0xffffffff)
	if _, _, e = peDependencies(broken); e == nil {
		t.Fatal("out-of-image import RVA accepted")
	}
}
func TestPEDelayedRuntimeCannotBypassImportGate(t *testing.T) {
	b := importImage("kernel32.dll")
	o := b[0x98:]
	binary.LittleEndian.PutUint32(o[112+13*8:], 0x1140)
	binary.LittleEndian.PutUint32(o[116+13*8:], 64)
	binary.LittleEndian.PutUint32(b[0x340:], 1)
	binary.LittleEndian.PutUint32(b[0x344:], 0x1200)
	copy(b[0x400:], "python314.dll\x00")
	_, imports, e := peDependencies(b)
	if e != nil {
		t.Fatal(e)
	}
	if !equalJSON(imports, []string{"kernel32.dll", "python314.dll"}) {
		t.Fatalf("delayed import missing: %v", imports)
	}
	file := filepath.Join(t.TempDir(), "delayed.exe")
	if e = write(file, b, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = systemImports(file); e == nil {
		t.Fatal("delayed Python runtime bypassed packaging gate")
	}
}
