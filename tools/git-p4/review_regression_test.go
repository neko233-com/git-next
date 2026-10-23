// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewGit(t *testing.T, d *driver, args ...string) string {
	t.Helper()
	value, e := d.gitText(args...)
	if e != nil {
		t.Fatal(e)
	}
	return value
}

func TestP4ReviewMetadataFooterCompatibility(t *testing.T) {
	for name, message := range map[string]string{
		"last footer": "[git-p4: depot-paths = \"//wrong/\": change = 1]\nmessage body\n[git-p4: depot-paths = \"//correct/\": change = 42]\n",
		"legacy key":  "[git-p4: depot-path = \"//correct/\": change = 42]\n",
		"field order": "[git-p4: change = 42: depot-paths = \"//correct/\"]\n",
		"quoted body": "body quotes [git-p4: depot-paths = \"//wrong/\": change = 1] inline\n[git-p4: depot-paths = \"//correct/\": change = 42]\n",
	} {
		t.Run(name, func(t *testing.T) {
			settings, e := settingsFromLog(message)
			if e != nil || settings.change != 42 || len(settings.paths) != 1 || settings.paths[0] != "//correct/" {
				t.Fatalf("historical metadata parsing differs: %+v %v", settings, e)
			}
		})
	}
}

func TestP4ReviewUpstreamUsesNearestDepotMetadata(t *testing.T) {
	git := os.Getenv("GIT_P4_TEST_GIT")
	if git == "" {
		t.Skip("set GIT_P4_TEST_GIT for actual Git object/ref regression")
	}
	root := t.TempDir()
	for key, value := range map[string]string{"HOME": root, "USERPROFILE": root, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(root, "gitconfig"), "GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"} {
		t.Setenv(key, value)
	}
	d := &driver{git: git, cwd: root, out: io.Discard, err: io.Discard, config: map[string][]string{}}
	reviewGit(t, d, "init")
	tree := reviewGit(t, d, "mktree")
	main := reviewGit(t, d, "commit-tree", tree, "-m", `[git-p4: depot-paths = "//depot/main/": change = 1]`)
	feature := reviewGit(t, d, "commit-tree", tree, "-p", main, "-m", `[git-p4: depot-paths = "//depot/feature/": change = 2]`)
	local := reviewGit(t, d, "commit-tree", tree, "-p", feature, "-m", "local work without import metadata")
	for ref, value := range map[string]string{"refs/remotes/p4/aaa-main": main, "refs/remotes/p4/zzz-feature": feature, "refs/heads/work": local} {
		reviewGit(t, d, "update-ref", ref, value)
	}
	reviewGit(t, d, "symbolic-ref", "HEAD", "refs/heads/work")
	ref, settings, e := d.upstream("")
	if e != nil || ref != "refs/remotes/p4/zzz-feature" || len(settings.paths) != 1 || settings.paths[0] != "//depot/feature/" {
		t.Fatalf("shared main ancestor selected the wrong depot: %s %+v %v", ref, settings, e)
	}
}

func reviewClone(t *testing.T, d *driver, root, depot string) string {
	t.Helper()
	workspace := d.cwd
	clone := filepath.Join(root, "review-clone")
	if e := d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{}, args: []string{depot + "@all"}}); e != nil {
		t.Fatal(e)
	}
	for key, value := range map[string]string{"user.name": "Fixture", "user.email": "fixture@example.invalid", "git-p4.skipSubmitEdit": "true"} {
		reviewGit(t, d, "config", key, value)
	}
	d.config = map[string][]string{}
	return workspace
}

func TestP4ReviewSubmitPreservesOtherDepotOpenedFiles(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "main/file.txt", []byte("main base\n"), "text")
	liveAdd(t, d, "other/file.txt", []byte("other base\n"), "text")
	liveSubmit(t, d, "review base")
	workspace := reviewClone(t, d, root, "//depot/main/")
	clone := d.cwd
	if e := os.WriteFile(filepath.Join(clone, "file.txt"), []byte("main submitted\n"), 0644); e != nil {
		t.Fatal(e)
	}
	reviewGit(t, d, "add", "file.txt")
	reviewGit(t, d, "commit", "-m", "only main")
	other := filepath.Join(workspace, "other/file.txt")
	if _, e := d.p4List("edit", other); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(other, []byte("unrelated pending work\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"disable-p4sync": true}}); e != nil {
		t.Fatal(e)
	}
	change, e := d.p4Single("describe", "-s", "2")
	if e != nil {
		t.Fatal(e)
	}
	files := describeFiles(change)
	if len(files) != 1 || files[0].depot != "//depot/main/file.txt" {
		t.Fatalf("unrelated opened file entered submitted changelist: %+v", files)
	}
	opened, e := d.p4Single("opened", other)
	if e != nil || text(opened["depotFile"]) != "//depot/other/file.txt" || text(opened["action"]) != "edit" {
		t.Fatalf("unrelated opened state changed: %+v %v", opened, e)
	}
	bytes, e := os.ReadFile(other)
	if e != nil || string(bytes) != "unrelated pending work\n" {
		t.Fatalf("unrelated bytes changed: %q %v", bytes, e)
	}
}

func TestP4ReviewPreserveUserValidatesWholeBatchBeforeSubmit(t *testing.T) {
	d, root := liveP4(t)
	if _, e := d.p4ListInput(dictionary{"Protections0": "super user fixtureuser * //..."}, "protect", "-i"); e != nil {
		t.Fatal(e)
	}
	liveAdd(t, d, "file.txt", []byte("base\n"), "text")
	liveSubmit(t, d, "review base")
	workspace := reviewClone(t, d, root, "//depot/")
	for i, author := range []string{"Fixture <fixture@example.invalid>", "Missing <missing-review@example.invalid>"} {
		if e := os.WriteFile(filepath.Join(d.cwd, "file.txt"), []byte(fmt.Sprintf("local commit %d\n", i)), 0644); e != nil {
			t.Fatal(e)
		}
		reviewGit(t, d, "add", "file.txt")
		reviewGit(t, d, "commit", "--author", author, "-m", fmt.Sprintf("local %d", i))
	}
	head := reviewGit(t, d, "rev-parse", "HEAD")
	e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"preserve-user": true, "disable-p4sync": true}})
	if e == nil || !strings.Contains(e.Error(), "has no Perforce user") {
		t.Fatalf("missing author did not fail preflight: %v", e)
	}
	changes, e := d.p4List("changes", "-s", "submitted")
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, change := range changes {
		if text(change["change"]) != "" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("preflight failure partially submitted %d changelists", count-1)
	}
	if reviewGit(t, d, "rev-parse", "HEAD") != head {
		t.Fatal("preflight failure changed Git HEAD")
	}
	bytes, e := os.ReadFile(filepath.Join(workspace, "file.txt"))
	if e != nil || string(bytes) != "base\n" {
		t.Fatalf("preflight failure changed P4 worktree: %q %v", bytes, e)
	}
}

func TestP4ReviewCopyHookFailureRestoresWorkspace(t *testing.T) {
	d, root := liveP4(t)
	base := []byte(strings.Repeat("source content retained\n", 20))
	liveAdd(t, d, "source.txt", base, "text")
	liveSubmit(t, d, "review base")
	workspace := reviewClone(t, d, root, "//depot/")
	for _, key := range []string{"git-p4.detectCopies", "git-p4.detectCopiesHarder"} {
		reviewGit(t, d, "config", key, "true")
	}
	d.config = map[string][]string{}
	if e := os.WriteFile(filepath.Join(d.cwd, "copied.txt"), base, 0644); e != nil {
		t.Fatal(e)
	}
	reviewGit(t, d, "add", "copied.txt")
	reviewGit(t, d, "commit", "-m", "copy source")
	if e := os.WriteFile(filepath.Join(d.cwd, ".git/hooks/p4-prepare-changelist"), []byte("#!/bin/sh\nprintf rejected > review-hook-reached\nexit 9\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"disable-p4sync": true}}); e == nil {
		t.Fatal("prepare hook rejection did not abort copy submission")
	}
	if data, e := os.ReadFile(filepath.Join(d.cwd, "review-hook-reached")); e != nil || string(data) != "rejected" {
		t.Fatalf("copy failed before reaching the prepare hook: %q %v", data, e)
	}
	if _, e := os.Lstat(filepath.Join(workspace, "copied.txt")); !os.IsNotExist(e) {
		t.Fatalf("copy abort left an untracked destination in P4 workspace: %v", e)
	}
	if data, e := os.ReadFile(filepath.Join(workspace, "source.txt")); e != nil || string(data) != string(base) {
		t.Fatalf("copy abort changed source bytes: %v", e)
	}
	opened, e := d.p4List("opened")
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range opened {
		if text(item["code"]) == "stat" {
			t.Fatal("copy abort left a Perforce file opened")
		}
	}
}

func TestP4ReviewPreserveUserRefusesNonAdminBeforeSubmit(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "file.txt", []byte("base\n"), "text")
	liveSubmit(t, d, "review base")
	workspace := reviewClone(t, d, root, "//depot/")
	if _, e := d.p4ListInput(dictionary{"User": "ordinary", "FullName": "Ordinary", "Email": "ordinary@example.invalid"}, "user", "-f", "-i"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4ListInput(dictionary{"Protections0": "super user fixtureuser * //...", "Protections1": "write user ordinary * //..."}, "protect", "-i"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d.cwd, "file.txt"), []byte("local work\n"), 0644); e != nil {
		t.Fatal(e)
	}
	reviewGit(t, d, "add", "file.txt")
	reviewGit(t, d, "commit", "-m", "local work")
	t.Setenv("P4USER", "ordinary")
	e := d.submit(cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"preserve-user": true, "disable-p4sync": true}})
	if e == nil || !strings.Contains(e.Error(), "admin or super") {
		t.Fatalf("non-admin author preservation was not rejected before submit: %v", e)
	}
	change, e := d.p4Single("changes", "-m", "1", "-s", "submitted")
	if e != nil || text(change["change"]) != "1" {
		t.Fatalf("permission failure submitted a changelist: %+v %v", change, e)
	}
	if data, e := os.ReadFile(filepath.Join(workspace, "file.txt")); e != nil || string(data) != "base\n" {
		t.Fatalf("permission failure changed P4 bytes: %q %v", data, e)
	}
}

func TestP4ReviewBranchDetectionUsesImportLocalNamespace(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "main/file.txt", []byte("branch base\n"), "text")
	liveSubmit(t, d, "review base")
	if _, e := d.p4ListInput(dictionary{"Branch": "reviewbranch", "View0": "//depot/main/... //depot/feature/..."}, "branch", "-i"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.p4List("integrate", "-b", "reviewbranch"); e != nil {
		t.Fatal(e)
	}
	liveSubmit(t, d, "review feature")
	if e := d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {filepath.Join(root, "clone-local")}}, flags: map[string]bool{"detect-branches": true, "import-local": true}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	for _, ref := range []string{"refs/heads/p4/master", "refs/heads/p4/depot/feature"} {
		if data := reviewGit(t, d, "show", ref+":file.txt"); data != "branch base" {
			t.Fatalf("local imported branch contents differ: %s %q", ref, data)
		}
	}
	if _, e := d.gitText("rev-parse", "--verify", "refs/remotes/p4/master"); e == nil {
		t.Fatal("import-local wrote a remote tracking branch")
	}
	if _, e := d.gitText("rev-parse", "--verify", "refs/heads/depot/feature"); e == nil {
		t.Fatal("import-local omitted the compatibility p4 namespace")
	}
}
