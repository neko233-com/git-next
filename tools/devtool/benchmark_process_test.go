// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"path/filepath"
	"testing"
)

func TestBenchmarkSourceIdentityPreservesPathComponentOrdering(t *testing.T) {
	root := t.TempDir()
	names := []string{"Cargo.lock", "Cargo.toml", "compatibility.json", "docs/COMPATIBILITY.md", "src/foo/a.rs", "src/foo.rs"}
	var expected []byte
	for _, name := range names {
		content := []byte("fixture:" + name)
		if e := write(filepath.Join(root, filepath.FromSlash(name)), content, 0644); e != nil {
			t.Fatal(e)
		}
		expected = append(expected, []byte(name+"\x00")...)
		expected = append(expected, content...)
	}
	if got := benchmarkSourceHash(root); got != digestBytes(expected) {
		t.Fatalf("source identity ordering differs: %s", got)
	}
}
