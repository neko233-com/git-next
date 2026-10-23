// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestP4RejectsGitAliasOutsideOwnedPackage(t *testing.T) {
	for _, adjacent := range []bool{false, true} {
		t.Run(map[bool]string{false: "public-bin", true: "adjacent"}[adjacent], func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			helperDir := filepath.Join(root, "runtime", "usr", "libexec", "git-core")
			for _, directory := range []string{helperDir, filepath.Join(root, "bin"), filepath.Join(root, "runtime", "etc")} {
				if e := os.MkdirAll(directory, 0755); e != nil {
					t.Fatal(e)
				}
			}
			helper := filepath.Join(helperDir, "git-p4")
			for _, name := range []string{helper, filepath.Join(root, "git-next-distribution.json"), filepath.Join(root, "runtime", "etc", "gitconfig"), filepath.Join(external, "git")} {
				if e := os.WriteFile(name, []byte("fixture\n"), 0755); e != nil {
					t.Fatal(e)
				}
			}
			gitDir := filepath.Join(root, "bin")
			if adjacent {
				gitDir = helperDir
			}
			if e := os.Symlink(filepath.Join(external, "git"), filepath.Join(gitDir, "git")); e != nil {
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege unavailable; Linux runs this boundary")
				}
				t.Fatal(e)
			}
			if got, e := ownedGit(helper); e == nil {
				t.Fatalf("accepted Git alias outside owned package: %s", got)
			}
		})
	}
}
