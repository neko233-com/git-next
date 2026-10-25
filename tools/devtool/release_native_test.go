// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseCandidateAndFinalCoreIdentityExcludesOnlyMetadata(t *testing.T) {
	candidate := record{
		"bin/git":          record{"kind": "file", "sha256": digestBytes([]byte("core")), "bytes": 4, "mode": 0755},
		"runtime/lib/link": record{"kind": "symlink", "target": "../../bin/git"},
		"docs/manual.md":   record{"sha256": digestBytes([]byte("old manual")), "bytes": 10},
	}
	for _, mode := range []string{"metadata-only", "changed core", "missing runtime", "extra runtime", "changed link", "changed mode"} {
		t.Run(mode, func(t *testing.T) {
			var staged record
			if e := decodeJSON(encoded(candidate), &staged); e != nil {
				t.Fatal(e)
			}
			staged["docs/manual.md"] = record{"sha256": digestBytes([]byte("new release manual")), "bytes": 18}
			switch mode {
			case "changed core":
				object(staged, "bin/git")["sha256"] = digestBytes([]byte("wrong core"))
			case "missing runtime":
				delete(staged, "runtime/lib/link")
			case "extra runtime":
				staged["runtime/unaccepted"] = record{"sha256": digestBytes(nil), "bytes": 0}
			case "changed link":
				object(staged, "runtime/lib/link")["target"] = "../../bin/other"
			case "changed mode":
				object(staged, "bin/git")["mode"] = 0644
			}
			proof, e := releaseCoreRuntimeIdentity(candidate, staged)
			if (e == nil) != (mode == "metadata-only") {
				t.Fatal(mode, e)
			}
			if e == nil && (proof["candidate_identity_sha256"] != proof["final_identity_sha256"] || number(proof, "members") != 2) {
				t.Fatal("candidate and final runtime proof diverged", proof)
			}
		})
	}
}

func TestReleaseFinalArchiveRechecksActualCoreBytesAndLinks(t *testing.T) {
	for _, mode := range []string{"valid", "changed bytes", "missing member", "wrong link", "extra core"} {
		t.Run(mode, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "final.tar.gz")
			file, e := os.Create(archive)
			if e != nil {
				t.Fatal(e)
			}
			gz := gzip.NewWriter(file)
			tw := tar.NewWriter(gz)
			body := []byte("core")
			if mode == "changed bytes" {
				body = []byte("bad!")
			}
			if mode != "missing member" {
				if e = tw.WriteHeader(&tar.Header{Name: "git-next-final/bin/git", Mode: 0755, Size: int64(len(body))}); e != nil {
					t.Fatal(e)
				}
				if _, e = tw.Write(body); e != nil {
					t.Fatal(e)
				}
			}
			link := "../../bin/git"
			if mode == "wrong link" {
				link = "../../bin/other"
			}
			if e = tw.WriteHeader(&tar.Header{Name: "git-next-final/runtime/lib/link", Mode: 0777, Typeflag: tar.TypeSymlink, Linkname: link}); e != nil {
				t.Fatal(e)
			}
			if mode == "extra core" {
				if e = tw.WriteHeader(&tar.Header{Name: "git-next-final/runtime/extra", Mode: 0644}); e != nil {
					t.Fatal(e)
				}
			}
			if e = tw.Close(); e != nil {
				t.Fatal(e)
			}
			if e = gz.Close(); e != nil {
				t.Fatal(e)
			}
			if e = file.Close(); e != nil {
				t.Fatal(e)
			}
			expected := record{"bin/git": record{"kind": "file", "sha256": digestBytes([]byte("core")), "bytes": 4, "mode": 0755}, "runtime/lib/link": record{"kind": "symlink", "target": "../../bin/git"}}
			proof, e := releaseArchiveCoreRuntime(archive, "git-next-final", expected)
			if (e == nil) != (mode == "valid") {
				t.Fatal(mode, e)
			}
			if e == nil && proof["final_archive_members_streamed_and_hashed"] != true {
				t.Fatal("final byte readback was not recorded")
			}
		})
	}
}

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
