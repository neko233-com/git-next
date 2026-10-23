// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func liveAdd(t *testing.T, d *driver, name string, data []byte, kind string) {
	t.Helper()
	p := filepath.Join(d.cwd, filepath.FromSlash(name))
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, data, 0644); e != nil {
		t.Fatal(e)
	}
	args := []string{"add"}
	if kind != "" {
		args = append(args, "-t", kind)
	}
	if _, e := d.p4List(append(args, p)...); e != nil {
		t.Fatal(e)
	}
}
func liveSubmit(t *testing.T, d *driver, message string) {
	t.Helper()
	if _, e := d.p4List("submit", "-d", message); e != nil {
		t.Fatal(e)
	}
}
func TestP4LiveBranchAncestryAndLabels(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "main/a.txt", []byte("branch base\n"), "text")
	liveSubmit(t, d, "branch base")
	if _, e := d.p4ListInput(dictionary{"Branch": "fixturebranch", "View0": "//depot/main/... //depot/feature/..."}, "branch", "-i"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("integrate", "-b", "fixturebranch"); e != nil {
		t.Fatal(e)
	}
	liveSubmit(t, d, "create feature branch")
	if _, e := d.p4List("edit", filepath.Join(d.cwd, "main/a.txt")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d.cwd, "main/a.txt"), []byte("main evolves\n"), 0644); e != nil {
		t.Fatal(e)
	}
	liveSubmit(t, d, "main edit")
	if _, e := d.p4ListInput(dictionary{"Label": "fixture-label", "Description": "fixture label description", "View0": "//depot/main/..."}, "label", "-i"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("tag", "-l", "fixture-label", "//depot/main/...@1"); e != nil {
		t.Fatal(e)
	}
	clone := filepath.Join(root, "branch-clone")
	if e := d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{"detect-branches": true, "import-labels": true}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	main, e := d.gitText("show", "refs/remotes/p4/master:a.txt")
	if e != nil || main != "main evolves" {
		t.Fatalf("main branch differs %q %v", main, e)
	}
	feature, e := d.gitText("show", "refs/remotes/p4/depot/feature:a.txt")
	if e != nil || feature != "branch base" {
		t.Fatalf("feature branch differs %q %v", feature, e)
	}
	base, e := d.gitText("merge-base", "refs/remotes/p4/master", "refs/remotes/p4/depot/feature")
	if e != nil {
		t.Fatal(e)
	}
	log, e := d.gitText("log", "-1", "--format=%B", base)
	if e != nil || !strings.Contains(log, "change = 1]") {
		t.Fatalf("branch ancestry differs %s %v", log, e)
	}
	tag, e := d.gitText("show", "fixture-label:a.txt")
	if e != nil || tag != "branch base" {
		t.Fatalf("imported label differs %q %v", tag, e)
	}
}
func TestP4LiveLFSAndMappedClientView(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "main/large.dat", []byte("large binary fixture\n"), "binary")
	liveAdd(t, d, "main/ignored.txt", []byte("excluded\n"), "text")
	liveSubmit(t, d, "large files")
	client, e := d.p4Single("client", "-o")
	if e != nil {
		t.Fatal(e)
	}
	delete(client, "code")
	client["View0"] = "//depot/main/... //fixtureclient/remapped/..."
	client["View1"] = "-//depot/main/ignored.txt //fixtureclient/remapped/ignored.txt"
	if _, e = d.p4ListInput(client, "client", "-i"); e != nil {
		t.Fatal(e)
	}
	clone := filepath.Join(root, "client-clone")
	if e = os.Mkdir(clone, 0755); e != nil {
		t.Fatal(e)
	}
	old := d.cwd
	d.cwd = clone
	if _, e = d.gitCall(nil, nil, "init"); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"git-p4.largeFileSystem": "GitLFS", "git-p4.largeFileExtensions": "dat"} {
		if _, e = d.gitCall(nil, nil, "config", k, v); e != nil {
			t.Fatal(e)
		}
	}
	d.cwd = old
	d.config = map[string][]string{}
	if e = d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{"use-client-spec": true}, args: []string{"//depot/main/@all"}}); e != nil {
		t.Fatal(e)
	}
	pointer, e := os.ReadFile(filepath.Join(clone, "remapped/large.dat"))
	if e != nil || !bytes.Equal(pointer, lfsPointer([]byte("large binary fixture\n"))) {
		t.Fatalf("LFS pointer differs %q %v", pointer, e)
	}
	if _, e = os.Stat(filepath.Join(clone, "remapped/ignored.txt")); !os.IsNotExist(e) {
		t.Fatalf("excluded client view file was imported %v", e)
	}
	attrs, e := os.ReadFile(filepath.Join(clone, ".gitattributes"))
	if e != nil || !bytes.Contains(attrs, []byte("*.dat filter=lfs diff=lfs merge=lfs -text")) {
		t.Fatalf("LFS attributes differ %q %v", attrs, e)
	}
}
func TestP4EncodingAndRawDiffContracts(t *testing.T) {
	d := &driver{out: io.Discard, err: io.Discard, config: map[string][]string{}, users: map[string]string{}}
	if got := d.metadata([]byte{0x80, 0xe6}); got != "€æ" {
		t.Fatalf("cp1252 fallback differs %q", got)
	}
	d.config["git-p4.metadataFallbackEncoding"] = []string{"cp850"}
	if got := d.metadata([]byte{0x82}); got != "é" {
		t.Fatalf("cp850 fallback differs %q", got)
	}
	d.config["git-p4.metadataFallbackEncoding"] = []string{"cp1252"}
	if got := d.metadata([]byte{0x81, 0xe6}); got != "%81%E6" {
		t.Fatalf("undefined cp1252 escaping differs %q", got)
	}
	d.config["git-p4.metadataDecodingStrategy"] = []string{"strict"}
	_ = d.metadata([]byte{0xff})
	if d.metadataError == nil {
		t.Fatal("strict metadata decoding did not fail")
	}
	raw := []byte(":100644 100755 abc def R100\x00old name\x00new name\x00:100644 000000 abc 000 D\x00gone\x00")
	deltas, e := parseRawDiff(raw)
	if e != nil || len(deltas) != 2 || deltas[0].destination != "new name" || deltas[1].status != "D" {
		t.Fatal(deltas, e)
	}
}
