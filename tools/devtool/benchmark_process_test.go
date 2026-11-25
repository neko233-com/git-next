// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"path/filepath"
	"testing"
)

func TestBenchmarkSourceIdentityPreservesPathComponentOrdering(t *testing.T) {
	root := t.TempDir()
	names := []string{"cmd/a/foo.go", "cmd/foo.go", "compatibility.json", "docs/COMPATIBILITY.md", "go.mod", "go.sum", "packaging/product.json", "tools/devtool/main.go"}
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
