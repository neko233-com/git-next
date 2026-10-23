// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseStagingReplacementsPreserveHardlinkedSource(t *testing.T) {
	root := t.TempDir()
	source, staged := filepath.Join(root, "source"), filepath.Join(root, "staged")
	originals := map[string]string{
		"components/install-tls-provider.ps1": "accepted repair script\n",
		"docs/GO_TOOLING.md":                  "accepted old manual\n",
		"git-next-distribution.json":          "{\"status\":\"candidate\"}\n",
		"bin/native-core":                     "unchanged accepted native bytes\n",
	}
	matrix := record{"files": record{}}
	for name, data := range originals {
		p := filepath.Join(source, filepath.FromSlash(name))
		if e := write(p, []byte(data), 0644); e != nil {
			t.Fatal(e)
		}
		object(matrix, "files")[p] = digestBytes([]byte(data))
	}
	if e := copyRuntimeTree(source, staged); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"components/install-tls-provider.ps1", "docs/GO_TOOLING.md", "git-next-distribution.json"} {
		t.Run(name, func(t *testing.T) {
			original := filepath.Join(source, filepath.FromSlash(name))
			target := filepath.Join(staged, filepath.FromSlash(name))
			before, e := os.Stat(original)
			if e != nil {
				t.Fatal(e)
			}
			linked, e := os.Stat(target)
			if e != nil || !os.SameFile(before, linked) {
				t.Fatalf("fixture must initially share a real hardlink: %v", e)
			}
			updated := []byte("new release-only bytes for " + name + "\n")
			if e = releaseOwnedReplace(staged, name, updated, 0644); e != nil {
				t.Fatal(e)
			}
			got, e := os.ReadFile(target)
			if e != nil || string(got) != string(updated) {
				t.Fatalf("staged bytes differ: %v", e)
			}
			got, e = os.ReadFile(original)
			if e != nil || string(got) != originals[name] {
				t.Fatalf("source bytes changed: %v", e)
			}
			after, e := os.Stat(target)
			if e != nil || os.SameFile(before, after) {
				t.Fatalf("replacement must have its own inode: %v", e)
			}
			if runtime.GOOS != "windows" && after.Mode().Perm() != 0644 {
				t.Fatalf("replacement mode differs: %v", after.Mode())
			}
			if e = releaseInputReadback(matrix); e != nil {
				t.Fatal(e)
			}
		})
	}
	if e := releaseOwnedReplace(staged, "docs/new-release-manual.md", []byte("new manual\n"), 0644); e != nil {
		t.Fatal(e)
	}
	left, e := filepath.Glob(filepath.Join(staged, "docs", ".git-next-release-*"))
	if e != nil || len(left) != 0 {
		t.Fatalf("temporary replacement file leaked: %v, %v", left, e)
	}
	if e = releaseInputReadback(matrix); e != nil {
		t.Fatal(e)
	}
}

func TestReleaseStagingReplacementRejectsNonregularTargets(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		if e := os.Mkdir(filepath.Join(root, "manual"), 0755); e != nil {
			t.Fatal(e)
		}
		if e := releaseOwnedReplace(root, "manual", []byte("replacement"), 0644); e == nil {
			t.Fatal("directory replacement accepted")
		}
	})
	t.Run("escape", func(t *testing.T) {
		if e := releaseOwnedReplace(t.TempDir(), "../outside", []byte("replacement"), 0644); e == nil {
			t.Fatal("escaping replacement accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "owned")
		if e := write(source, []byte("unchanged source"), 0644); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink("owned", filepath.Join(root, "manual")); e != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink creation unavailable: %v", e)
			}
			t.Fatal(e)
		}
		if e := releaseOwnedReplace(root, "manual", []byte("replacement"), 0644); e == nil {
			t.Fatal("symlink replacement accepted")
		}
		got, e := os.ReadFile(source)
		if e != nil || string(got) != "unchanged source" {
			t.Fatalf("symlink source changed: %v", e)
		}
	})
}

func TestReleaseStagingReadbackRejectsMutatedInput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "accepted")
	if e := write(p, []byte("accepted"), 0644); e != nil {
		t.Fatal(e)
	}
	matrix := record{"files": record{p: digestBytes([]byte("accepted"))}}
	if e := releaseInputReadback(matrix); e != nil {
		t.Fatal(e)
	}
	if e := write(p, []byte("changed"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := releaseInputReadback(matrix); e == nil {
		t.Fatal("changed accepted source passed readback")
	}
}

func TestReleaseStagingMSIReuseOnlyAddsExactRegressionFile(t *testing.T) {
	a := &app{root: t.TempDir()}
	previous := record{"source_tree_sha256": "same", "files": record{"package.msi": "same"}, "platforms": record{}, "harnesses": record{}}
	report := record{"phase": "correctness", "source_tree_sha256": "same", "reference_sha256": "reference", "results": []any{record{"case": "msi-user", "status": "pass", "exit_code": 0}}}
	for _, name := range []string{"tools/devtool/release_native_test.go", "tools/devtool/release_native_other_test.go", "tools/devtool/process.go", "tools/devtool/files.go", "tools/devtool/packaging_msi.go", "packaging/windows/Product.wxs", "tests/msi_acceptance.ps1"} {
		t.Run(name, func(t *testing.T) {
			current := cloneRecord(t, previous)
			object(current, "harnesses")[name] = "changed"
			_, e := validateReuseAt(a, report, previous, current, "reference", []string{"msi-user"})
			if (e == nil) != (name == "tools/devtool/release_native_test.go") {
				t.Fatalf("reuse boundary differs for %s: %v", name, e)
			}
		})
	}
}
