// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestProductArchiveAuditChecksLinkTargets(t *testing.T) {
	for _, target := range []string{"../runtime/bin/python3", "../../outside"} {
		var buffer bytes.Buffer
		tarball := tar.NewWriter(&buffer)
		manifest := []byte(`{"schema_version":1,"product":"git-next","version":"fixture","files":{"bin/git":{"sha256":"fixture"}}}`)
		if e := tarball.WriteHeader(&tar.Header{Name: "git-next-distribution.json", Mode: 0644, Size: int64(len(manifest))}); e != nil {
			t.Fatal(e)
		}
		if _, e := tarball.Write(manifest); e != nil {
			t.Fatal(e)
		}
		if e := tarball.WriteHeader(&tar.Header{Name: "bin/helper", Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0755}); e != nil {
			t.Fatal(e)
		}
		if e := tarball.Close(); e != nil {
			t.Fatal(e)
		}
		archive := filepath.Join(t.TempDir(), "product.tar")
		if e := os.WriteFile(archive, buffer.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
		report, e := inspectNoPythonProduct("", archive)
		if target == "../../outside" {
			if e == nil {
				t.Fatal("escaping archive symlink accepted")
			}
		} else if e != nil || report["complete"] != false || report["python_entry_count"] != 1 {
			t.Fatalf("Python symlink target was missed: %v %+v", e, report)
		}
	}
}

func TestMigrationAuditFindsExecutionButIgnoresProtocolAndHistoricalNames(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"tools", "tests", "packaging"} {
		if e := os.MkdirAll(filepath.Join(root, folder), 0755); e != nil {
			t.Fatal(e)
		}
	}
	source := `package main
import("context";"os/exec")
const historical="tests/legacy_fixture.py"
const protocol="python marshal byte tags; not an interpreter invocation"
func sample(){exec.CommandContext(context.Background(),"python3","fixture.py")}
`
	if e := write(filepath.Join(root, "tools/entry.go"), []byte(source), 0644); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "tests/probe.ps1"), []byte("$value=@{python = $false}\n# python legacy.py\n& python3.exe fixture.py --token PRIVATE\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "packaging/Dockerfile"), []byte("FROM python:3.14-slim\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "packaging/source.zip"), []byte("opaque python source archive"), 0644); e != nil {
		t.Fatal(e)
	}
	refs, e := scanPythonEntrypoints(root)
	if e != nil {
		t.Fatal(e)
	}
	if len(refs) != 3 {
		t.Fatalf("expected exactly 3 executable references: %+v", refs)
	}
	for _, ref := range refs {
		if ref.Command == "PRIVATE" || ref.Command == "& python3.exe fixture.py --token PRIVATE" {
			t.Fatal("audit leaked command arguments")
		}
	}
}
func TestProductAuditChecksUnlistedPythonAndExtensionlessShebang(t *testing.T) {
	root := t.TempDir()
	if e := writeJSON(filepath.Join(root, "git-next-distribution.json"), record{"schema_version": 1, "product": "git-next", "version": "fixture", "platform": "linux-x64", "files": record{"bin/git": record{"sha256": "fixture"}}}); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "bin/git"), []byte("ELF fixture"), 0755); e != nil {
		t.Fatal(e)
	}
	report, e := inspectNoPythonProduct(root, "")
	if e != nil || report["complete"] != true {
		t.Fatalf("clean package rejected: %v %+v", e, report)
	}
	if e = write(filepath.Join(root, "bin/git-p4"), []byte("#!/usr/bin/env python3\nprint('old integration')\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if e = write(filepath.Join(root, "runtime/usr/lib/python3.14/os.py"), []byte("third party runtime"), 0644); e != nil {
		t.Fatal(e)
	}
	report, e = inspectNoPythonProduct(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if report["complete"] != false || report["python_entry_count"] != 2 {
		t.Fatalf("unlisted Python payload was missed: %+v", report)
	}
}

func TestProductAuditRecognizesEmbeddedPythonLibraryArchives(t *testing.T) {
	for _, name := range []string{"runtime/python314.zip", "runtime/lib/libpython3.14.a", "bin/tool.pyz", "Python.framework/Versions/3.14/Python"} {
		if pythonAssetReason(name, nil) == "" {
			t.Fatalf("Python asset was missed: %s", name)
		}
	}
	if pythonAssetReason("runtime/share/vim/syntax/python.vim", nil) != "" {
		t.Fatal("editor syntax data was mistaken for a Python runtime")
	}
}
func TestFixtureErrorRedactsCredentials(t *testing.T) {
	input := "Authorization: Basic YWJjZGVmZw==\npassword=fixture-token\nplain failure"
	got := fixtureCredentialText.ReplaceAllString(input, "<redacted>")
	if got == input || len(got) > 100 {
		t.Fatal("fixture credentials were not redacted")
	}
	if got := acceptCommandDescription([]string{"-c", "http.extraHeader=Basic PRIVATE", "config", "http.extraHeader", "Basic PRIVATE"}); got != "config http.extraHeader" {
		t.Fatal(got)
	}
}
