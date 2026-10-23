// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureTar(t *testing.T, entries []tar.Header) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "fixture.tar")
	f, e := os.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	w := tar.NewWriter(f)
	for _, h := range entries {
		if e = w.WriteHeader(&h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			if _, e = w.Write(bytes.Repeat([]byte("x"), int(h.Size))); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return name
}
func TestArchiveRefusesUnsafeMembers(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "C:/drive", "dir\\file"} {
		t.Run(name, func(t *testing.T) {
			archive := fixtureTar(t, []tar.Header{{Name: name, Mode: 0644, Size: 1, Typeflag: tar.TypeReg}})
			_, _, e := extractArchive(archive, t.TempDir(), extractPolicy{maxFiles: 10, maxBytes: 100})
			if e == nil {
				t.Fatalf("accepted unsafe member %s", name)
			}
		})
	}
}
func TestArchiveLimitsAndDuplicate(t *testing.T) {
	for _, entries := range [][]tar.Header{{{Name: "same", Mode: 0644, Typeflag: tar.TypeReg}, {Name: "same", Mode: 0644, Typeflag: tar.TypeReg}}, {{Name: "large", Mode: 0644, Size: 11, Typeflag: tar.TypeReg}}, {{Name: "link", Linkname: "../../outside", Typeflag: tar.TypeSymlink}}} {
		archive := fixtureTar(t, entries)
		_, _, e := extractArchive(archive, t.TempDir(), extractPolicy{links: true, maxFiles: 10, maxBytes: 10})
		if e == nil {
			t.Fatal("accepted duplicate, oversized, or escaping member")
		}
	}
}
func TestArchivePreservesFilesAndSkipsWorkflows(t *testing.T) {
	archive := fixtureTar(t, []tar.Header{{Name: "source/data", Mode: 0644, Size: 3, Typeflag: tar.TypeReg}, {Name: "source/.github/workflows/ci.yml", Mode: 0644, Size: 4, Typeflag: tar.TypeReg}})
	root := t.TempDir()
	h, skipped, e := extractArchive(archive, root, extractPolicy{strip: "source/", skipWorkflows: true, maxFiles: 10, maxBytes: 10})
	if e != nil {
		t.Fatal(e)
	}
	if len(h) != 1 || len(skipped) != 1 || exists(filepath.Join(root, ".github/workflows")) {
		t.Fatal("unexpected extraction result")
	}
	data, e := os.ReadFile(filepath.Join(root, "data"))
	if e != nil || string(data) != "xxx" {
		t.Fatal("file content differs")
	}
}
func TestLocaleRejectsDuplicateAndPlaceholderDrift(t *testing.T) {
	data := []byte("{\"code\":\"x\",\"name\":\"x\",\"english_name\":\"x\",\"messages\":{\"hello {name}\":\"hello {other}\",\"hello {name}\":\"hello {other}\"}}\n")
	_, problems := validateLocale("x.json", data, `"hello {name}"`, map[string]bool{})
	combined := strings.Join(problems, "\n")
	if !strings.Contains(combined, "duplicate") || !strings.Contains(combined, "placeholders") {
		t.Fatal(combined)
	}
}
func TestHTMLRelativeLinksPreserveQueryAndFragment(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "docs/source.md")
	target := filepath.Join(root, "docs/other.md")
	value, e := renderLink(root, source, "other.md?mode=x#anchor", map[string]string{target: "other.html"})
	if e != nil || value != "other.html?mode=x#anchor" {
		t.Fatalf("%s %v", value, e)
	}
	value, e = renderLink(root, source, "https://example.invalid/a", nil)
	if e != nil || value != "https://example.invalid/a" {
		t.Fatal(value, e)
	}
}
func TestPackageRefusesInterpreterAndForeignRuntime(t *testing.T) {
	for _, name := range []string{"bin/python.exe", "runtime/mingw/bin/file", "bin/foreign.dll", "bin/bash.exe", "a/../python/tool"} {
		if rejectRelative(name) == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if e := rejectRelative("bin/git.exe"); e != nil {
		t.Fatal(e)
	}
}
func TestPinnedPatchRequiresExactContextAndCounts(t *testing.T) {
	patched, e := applyUnified("one\ntwo\n", "--- a/file\n+++ b/file\n@@ -1,2 +1,2 @@\n one\n-two\n+three\n")
	if e != nil || patched != "one\nthree\n" {
		t.Fatal(patched, e)
	}
	if _, e = applyUnified("other\ntwo\n", "--- a/file\n+++ b/file\n@@ -1,2 +1,2 @@\n one\n-two\n+three\n"); e == nil {
		t.Fatal("accepted mismatched pinned source")
	}
}
func TestNativeCatalogPreservesCOperatorsAndPortableFormats(t *testing.T) {
	text := cLiteral("count < total & value > 1: %<PRIuMAX>")
	if strings.Contains(text, `\u003c`) || !strings.Contains(text, "PRIuMAX") {
		t.Fatal(text)
	}
}
func TestDefaultsHaveOneSourceAndPreserveSectionOrder(t *testing.T) {
	root := t.TempDir()
	if e := write(filepath.Join(root, "packaging/runtime-defaults/gitconfig"), []byte("# comment\n[core]\n\tautocrlf = input\n[gc]\n\tauto = 256\n"), 0644); e != nil {
		t.Fatal(e)
	}
	d, e := readDefaults(root)
	if e != nil {
		t.Fatal(e)
	}
	if d.block() != "[core]\n\tautocrlf = input\n[gc]\n\tauto = 256\n" || d.powershell() != "[core]`n`tautocrlf = input`n[gc]`n`tauto = 256`n" {
		t.Fatal("default rendering drifted")
	}
}
