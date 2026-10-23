// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWindowsSystemDLLNamesMixedCase(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"IPHLPAPI.DLL", "Kernel32.dLl", "user32.dll", "not-a-dll.exe", "old.dll.bak"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "directory.dll"), 0700); err != nil {
		t.Fatal(err)
	}
	names, err := windowsSystemDLLNames(directory)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"iphlpapi.dll": true, "kernel32.dll": true, "user32.dll": true}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("system DLLs = %v, want %v", names, want)
	}
}

func TestWindowsSystemDLLNamesMissingDirectory(t *testing.T) {
	if _, err := windowsSystemDLLNames(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing system directory error = %v", err)
	}
}
