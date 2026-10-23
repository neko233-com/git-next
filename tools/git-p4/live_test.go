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

func liveP4(t *testing.T) (*driver, string) {
	t.Helper()
	p4, p4d, git := os.Getenv("GIT_P4_TEST_P4"), os.Getenv("GIT_P4_TEST_P4D"), os.Getenv("GIT_P4_TEST_GIT")
	if p4 == "" || p4d == "" || git == "" {
		t.Skip("set GIT_P4_TEST_P4/P4D/GIT for an isolated local P4 fixture")
	}
	root := t.TempDir()
	server, workspace, home := filepath.Join(root, "server"), filepath.Join(root, "workspace"), filepath.Join(root, "home")
	for _, p := range []string{server, workspace, home} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("P4PORT", `rsh:"`+p4d+`" -r "`+server+`" -L "`+filepath.Join(root, "p4d.log")+`" -J off -i`)
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(home, "gitconfig"), "P4ENVIRO": filepath.Join(home, "p4env"), "P4CONFIG": ".missing-p4config", "P4USER": "fixtureuser", "P4CLIENT": "fixtureclient", "P4HOST": "fixturehost", "P4TICKETS": filepath.Join(home, "tickets"), "P4TRUST": filepath.Join(home, "trust")} {
		t.Setenv(k, v)
	}
	d := &driver{git: git, p4: p4, cwd: workspace, out: io.Discard, err: io.Discard, config: map[string][]string{}, users: map[string]string{}}
	if _, e := d.p4List("info"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4ListInput(dictionary{"User": "fixtureuser", "FullName": "Fixture User", "Email": "fixture@example.invalid"}, "user", "-i"); e != nil {
		t.Fatal(e)
	}
	client := dictionary{"Client": "fixtureclient", "Owner": "fixtureuser", "Root": workspace, "Options": "noallwrite noclobber nocompress unlocked nomodtime normdir", "SubmitOptions": "submitunchanged", "LineEnd": "unix", "View0": "//depot/... //fixtureclient/..."}
	if _, e := d.p4ListInput(client, "client", "-i"); e != nil {
		t.Fatal(e)
	}
	return d, root
}
func TestP4LiveCloneAndIncrementalSync(t *testing.T) {
	d, root := liveP4(t)
	binary := []byte{0, 1, 255, 13, 10, 128}
	for name, data := range map[string][]byte{"hello.txt": []byte("first\n"), "binary.dat": binary} {
		if e := os.WriteFile(filepath.Join(d.cwd, name), data, 0644); e != nil {
			t.Fatal(e)
		}
		kind := "text"
		if name == "binary.dat" {
			kind = "binary"
		}
		if _, e := d.p4List("add", "-t", kind, filepath.Join(d.cwd, name)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := d.p4List("submit", "-d", "initial fixture"); e != nil {
		t.Fatal(e)
	}
	clone := filepath.Join(root, "clone")
	o := cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{}, args: []string{"//depot/@all"}}
	originalWorkspace := d.cwd
	if e := d.sync(o); e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string][]byte{"hello.txt": []byte("first\n"), "binary.dat": binary} {
		got, e := os.ReadFile(filepath.Join(clone, name))
		if e != nil || !bytes.Equal(got, want) {
			t.Fatalf("imported file differs %s: %x %v", name, got, e)
		}
	}
	log, e := d.gitText("log", "-1", "--format=%B")
	if e != nil || !strings.Contains(log, `[git-p4: depot-paths = "//depot/": change = 1]`) {
		t.Fatalf("metadata differs: %s %v", log, e)
	}
	d.cwd = originalWorkspace
	d.config = map[string][]string{}
	if _, e = d.p4List("edit", filepath.Join(originalWorkspace, "hello.txt")); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(originalWorkspace, "hello.txt"), []byte("second\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = d.p4List("submit", "-d", "second fixture"); e != nil {
		t.Fatal(e)
	}
	d.cwd = clone
	d.config = map[string][]string{}
	if e = d.sync(cliOptions{command: "sync", values: map[string][]string{}, flags: map[string]bool{}}); e != nil {
		t.Fatal(e)
	}
	got, e := d.gitText("show", "refs/remotes/p4/master:hello.txt")
	if e != nil || got != "second" {
		t.Fatalf("incremental sync differs: %q %v", got, e)
	}
	count, e := d.gitText("rev-list", "--count", "refs/remotes/p4/master")
	if e != nil || count != "2" {
		t.Fatalf("import ancestry differs: %q %v", count, e)
	}
}
func TestP4OptionsAndMetadataContracts(t *testing.T) {
	o, e := parseCLI([]string{"clone", "--keep-path", "-//depot/excluded", "--dest", "dst", "//depot/@all"})
	if e != nil || o.value("/") != "//depot/excluded" || !o.flags["keep-path"] {
		t.Fatal(o, e)
	}
	o, e = parseCLI([]string{"submit", "--shelve", "--update-shelve", "1", "--update-shelve=2", "--no-verify"})
	if e != nil || len(o.values["update-shelve"]) != 2 || !o.flags["shelve"] {
		t.Fatal(o, e)
	}
	if _, e = parseCLI([]string{"sync", "--unknown"}); e == nil {
		t.Fatal("unknown option accepted")
	}
	settings, e := settingsFromLog("description\n[git-p4: depot-paths = \"//depot/main/,//other/\": change = 42: options = keepRepoPath]\n")
	if e != nil || settings.change != 42 || len(settings.paths) != 2 || settings.options != "keepRepoPath" {
		t.Fatal(settings, e)
	}
	if quoteGitPath("é\n\"") != `"\303\251\n\""` {
		t.Fatal("Git C quoting changed")
	}
}

func TestP4LiveSubmitShelveAndUnshelve(t *testing.T) {
	d, root := liveP4(t)
	p := filepath.Join(d.cwd, "hello.txt")
	if e := os.WriteFile(p, []byte("base\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("add", p); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("submit", "-d", "base"); e != nil {
		t.Fatal(e)
	}
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
	if e := os.WriteFile(filepath.Join(clone, "hello.txt"), []byte("submitted\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "add", "hello.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "commit", "-m", "Go submit fixture"); e != nil {
		t.Fatal(e)
	}
	if e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"disable-rebase": true}}); e != nil {
		t.Fatal(e)
	}
	files, e := d.p4List("print", "-q", "//depot/hello.txt#head")
	if e != nil {
		t.Fatal(e)
	}
	var content string
	for _, item := range files {
		content += text(item["data"])
	}
	if content != "submitted\n" {
		t.Fatalf("submitted bytes differ %q", content)
	}
	if _, e := d.gitCall(nil, nil, "reset", "--hard", "refs/remotes/p4/master"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(clone, "hello.txt"), []byte("shelved\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "add", "hello.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "commit", "-m", "Go shelve fixture"); e != nil {
		t.Fatal(e)
	}
	if e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"shelve": true, "disable-p4sync": true}}); e != nil {
		t.Fatal(e)
	}
	shelves, e := d.p4List("changes", "-s", "shelved")
	if e != nil {
		t.Fatal(e)
	}
	change := ""
	for _, item := range shelves {
		if text(item["change"]) != "" {
			change = text(item["change"])
			break
		}
	}
	if change == "" {
		t.Fatal("shelved changelist missing")
	}
	if e := d.unshelve(cliOptions{command: "unshelve", values: map[string][]string{"origin": {"refs/remotes/p4/master"}}, flags: map[string]bool{}, args: []string{change}}); e != nil {
		t.Fatal(e)
	}
	got, e := d.gitText("show", "refs/remotes/p4-unshelved/"+change+":hello.txt")
	if e != nil || got != "shelved" {
		t.Fatalf("unshelved bytes differ %q %v", got, e)
	}
}
