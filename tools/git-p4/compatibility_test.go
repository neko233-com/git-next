// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestP4ShortFlagsAndHelp(t *testing.T) {
	o, e := parseCLI([]string{"submit", "-vMn"})
	if e != nil || !o.flags["verbose"] || !o.flags["M"] || !o.flags["dry-run"] {
		t.Fatal(o, e)
	}
	var out bytes.Buffer
	if e := executeP4([]string{"submit", "--help"}, &out, &out); e != nil || !strings.Contains(out.String(), "--update-shelve") {
		t.Fatal(out.String(), e)
	}
}

func TestP4OwnedCompressionMatchesZIPThreshold(t *testing.T) {
	git := os.Getenv("GIT_P4_TEST_GIT")
	if git == "" {
		t.Skip("set GIT_P4_TEST_GIT for owned Git zlib classification")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	d := &driver{git: git, cwd: root, out: io.Discard, err: io.Discard, config: map[string][]string{}}
	if _, e := d.gitCall(nil, nil, "init"); e != nil {
		t.Fatal(e)
	}
	first, e := d.compressedSize([]byte("content 5 bin 40 bytes XXXXXXXXXXXXXXXX\n"))
	if e != nil {
		t.Fatal(e)
	}
	second, e := d.compressedSize([]byte("content 6 bin 39 bytes XXXXXYYYYYZZZZZ\n"))
	if e != nil || first > 28 || second <= 28 {
		t.Fatalf("ZIP_DEFLATED threshold changed: %d/%d %v", first, second, e)
	}
}

func TestP4LiveRevisionRangesLocalBranchesAndExportLabels(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "a.txt", []byte("first\n"), "text")
	liveSubmit(t, d, "first")
	if _, e := d.p4ListInput(dictionary{"Label": "start-label", "Description": "preserve this existing label", "View0": "//depot/..."}, "label", "-i"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("tag", "-l", "start-label", "//depot/...@1"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("edit", filepath.Join(d.cwd, "a.txt")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d.cwd, "a.txt"), []byte("second\n"), 0644); e != nil {
		t.Fatal(e)
	}
	liveSubmit(t, d, "second")
	changes, e := d.changesForPaths([]string{"//depot/"}, "@start-label,#head", 0, "")
	if e != nil || len(changes) != 2 || changes[0] != 1 || changes[1] != 2 {
		t.Fatal(changes, e)
	}
	if _, e := d.changesForPaths([]string{"//depot/"}, "@start-label,#head", 0, "1"); e == nil {
		t.Fatal("explicit blocks accepted label range")
	}
	clone := filepath.Join(root, "local-clone")
	if e = d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{"import-local": true}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	if count, e := d.gitText("rev-list", "--count", "refs/heads/p4/master"); e != nil || count != "2" {
		t.Fatal(count, e)
	}
	if _, e = d.gitCall(nil, nil, "tag", "exported-label", "refs/heads/p4/master"); e != nil {
		t.Fatal(e)
	}
	if _, e = d.gitCall(nil, nil, "tag", "start-label", "refs/heads/p4/master"); e != nil {
		t.Fatal(e)
	}
	if e = d.exportLabels([]string{"//depot/"}); e != nil {
		t.Fatal(e)
	}
	label, e := d.p4Single("label", "-o", "start-label")
	if e != nil || !strings.Contains(text(label["Description"]), "preserve this existing label") {
		t.Fatal(label, e)
	}
	files, e := d.p4List("files", "//depot/...@exported-label")
	if e != nil || len(files) != 1 || text(files[0]["rev"]) != "2" {
		t.Fatal(files, e)
	}
}

func TestP4LiveUpdateShelf(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "a.txt", []byte("base\n"), "text")
	liveSubmit(t, d, "base")
	clone := filepath.Join(root, "clone")
	if e := d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"user.name": "Fixture User", "user.email": "fixture@example.invalid", "git-p4.skipSubmitEdit": "true"} {
		if _, e := d.gitCall(nil, nil, "config", k, v); e != nil {
			t.Fatal(e)
		}
	}
	d.config = map[string][]string{}
	commit := func(data string, amend bool) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(clone, "a.txt"), []byte(data), 0644); e != nil {
			t.Fatal(e)
		}
		if _, e := d.gitCall(nil, nil, "add", "a.txt"); e != nil {
			t.Fatal(e)
		}
		args := []string{"commit", "-m", "shelved edit"}
		if amend {
			args = append(args, "--amend")
		}
		if _, e := d.gitCall(nil, nil, args...); e != nil {
			t.Fatal(e)
		}
	}
	commit("shelf first\n", false)
	if e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"shelve": true, "disable-p4sync": true}}); e != nil {
		t.Fatal(e)
	}
	commit("shelf updated\n", true)
	if e := d.submit(cliOptions{command: "submit", values: map[string][]string{"update-shelve": {"2"}}, flags: map[string]bool{"disable-p4sync": true}}); e != nil {
		t.Fatal(e)
	}
	files, e := d.p4List("print", "-q", "//depot/a.txt@=2")
	if e != nil {
		t.Fatal(e)
	}
	var content string
	for _, item := range files {
		content += text(item["data"])
	}
	if content != "shelf updated\n" {
		t.Fatalf("updated shelf bytes differ %q", content)
	}
	opened, e := d.p4List("opened")
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range opened {
		if text(item["code"]) == "stat" {
			t.Fatal("shelf update left owned files opened")
		}
	}
}

func TestP4LiveNativeAltRootSubmit(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native/AltRoot path interoperability")
	}
	d, root := liveP4(t)
	workspace := d.cwd
	liveAdd(t, d, "a.txt", []byte("base\n"), "text")
	liveSubmit(t, d, "base")
	client, e := d.p4Single("client", "-o")
	if e != nil {
		t.Fatal(e)
	}
	delete(client, "code")
	forward := filepath.ToSlash(workspace)
	client["Root"] = "/" + strings.ToLower(forward[:1]) + forward[2:]
	client["AltRoots0"] = forward
	if _, e = d.p4ListInput(client, "client", "-i"); e != nil {
		t.Fatal(e)
	}
	clone := filepath.Join(root, "clone")
	if e = d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"user.name": "Fixture User", "user.email": "fixture@example.invalid", "git-p4.skipSubmitEdit": "true"} {
		if _, e := d.gitCall(nil, nil, "config", k, v); e != nil {
			t.Fatal(e)
		}
	}
	d.config = map[string][]string{}
	if e = os.WriteFile(filepath.Join(clone, "a.txt"), []byte("native alt root submit\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = d.gitCall(nil, nil, "add", "a.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e = d.gitCall(nil, nil, "commit", "-m", "native alt root"); e != nil {
		t.Fatal(e)
	}
	if e = d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"disable-p4sync": true}}); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(workspace, "a.txt"))
	if e != nil || string(data) != "native alt root submit\n" {
		t.Fatal(string(data), e)
	}
	if d.p4cwd != "" {
		t.Fatal("submit leaked Perforce client cwd into later commands")
	}
}
