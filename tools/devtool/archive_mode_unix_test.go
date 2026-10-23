// Copyright 2026 git-next contributors. MIT license.
//go:build !windows

package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestArchivePreservesModesUnderPrivateUmask(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "modes.tar")
	file, e := os.Create(archive)
	if e != nil {
		t.Fatal(e)
	}
	writer := tar.NewWriter(file)
	for _, name := range []string{".", "bin", "share"} {
		if e = writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Typeflag: tar.TypeDir}); e != nil {
			t.Fatal(e)
		}
	}
	for name, mode := range map[string]int64{"bin/tool": 0755, "share/data": 0644, "share/group": 0664} {
		if e = writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: 1, Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e = writer.Write([]byte("x")); e != nil {
			t.Fatal(e)
		}
	}
	if e = writer.Close(); e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	output := filepath.Join(root, "output")
	if e = os.Mkdir(output, 0700); e != nil {
		t.Fatal(e)
	}
	if _, _, e = extractArchive(archive, output, extractPolicy{maxFiles: 5, maxBytes: 3}); e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]os.FileMode{".": 0755, "bin": 0755, "share": 0755, "bin/tool": 0755, "share/data": 0644, "share/group": 0664} {
		info, e := os.Stat(filepath.Join(output, name))
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%#o, want %#o", name, info.Mode().Perm(), want)
		}
	}
}

func TestDistributionStorePreservesModesUnderPrivateUmask(t *testing.T) {
	root := t.TempDir()
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	builder := distributionBuilder{root: root, records: record{}}
	for name, mode := range map[string]uint32{"bin/launcher": 0755, "share/data": 0644} {
		if e := builder.store(name, []byte("fixture\n"), "mode-fixture", mode, 0); e != nil {
			t.Fatal(e)
		}
		info, e := os.Stat(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != os.FileMode(mode) {
			t.Fatalf("%s mode=%#o, want %#o", name, info.Mode().Perm(), mode)
		}
	}
	if e := writeNewJSON(filepath.Join(root, "git-next-distribution.json"), record{"status": "fixture"}); e != nil {
		t.Fatal(e)
	}
	if e := publicPOSIXDistribution(root); e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]os.FileMode{".": 0755, "bin": 0755, "share": 0755, "git-next-distribution.json": 0644} {
		info, e := os.Stat(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s public mode=%#o, want %#o", name, info.Mode().Perm(), want)
		}
	}
}
