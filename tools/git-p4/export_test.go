// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestP4LiveRenameDeleteAndEditorHooks(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "old.txt", []byte("rename me\n"), "text")
	liveAdd(t, d, "remove.txt", []byte("delete me\n"), "text")
	liveSubmit(t, d, "export base")
	workspace := d.cwd
	clone := filepath.Join(root, "clone")
	if e := d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"user.name": "Fixture User", "user.email": "fixture@example.invalid", "git-p4.skipSubmitEditCheck": "true"} {
		if _, e := d.gitCall(nil, nil, "config", k, v); e != nil {
			t.Fatal(e)
		}
	}
	d.config = map[string][]string{}
	t.Setenv("P4EDITOR", "true")
	if _, e := d.gitCall(nil, nil, "mv", "old.txt", "renamed.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "rm", "remove.txt"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(clone, "added.dat"), []byte{0, 255, 128}, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "add", "added.dat"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "commit", "-m", "rename delete add"); e != nil {
		t.Fatal(e)
	}
	hooks := filepath.Join(clone, ".git/hooks")
	if e := os.WriteFile(filepath.Join(hooks, "p4-pre-submit"), []byte("#!/bin/sh\nexit 1\n"), 0755); e != nil {
		t.Fatal(e)
	}
	opts := cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"M": true, "disable-p4sync": true}}
	if e := d.submit(opts); e == nil {
		t.Fatal("pre-submit rejection was ignored")
	}
	if e := os.WriteFile(filepath.Join(hooks, "p4-prepare-changelist"), []byte("#!/bin/sh\nexit 9\n"), 0755); e != nil {
		t.Fatal(e)
	}
	opts.flags["no-verify"] = true
	if e := d.submit(opts); e == nil {
		t.Fatal("prepare hook must run with no-verify")
	}
	if _, e := os.Stat(filepath.Join(workspace, "old.txt")); e != nil {
		t.Fatalf("failed submit did not restore rename source: %v", e)
	}
	if e := os.Remove(filepath.Join(hooks, "p4-prepare-changelist")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(hooks, "p4-changelist"), []byte("#!/bin/sh\nexit 7\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := d.submit(opts); e != nil {
		t.Fatal(e)
	}
	details, e := d.p4Single("describe", "-s", "2")
	if e != nil {
		t.Fatal(e)
	}
	actions := map[string]string{}
	types := map[string]string{}
	for _, file := range describeFiles(details) {
		actions[file.depot] = file.action
		types[file.depot] = file.kind
	}
	if actions["//depot/old.txt"] != "move/delete" || actions["//depot/renamed.txt"] != "move/add" || actions["//depot/remove.txt"] != "delete" || !strings.HasPrefix(types["//depot/added.dat"], "binary") {
		t.Fatalf("Perforce export actions/types changed: %v %v", actions, types)
	}
}

func TestP4LiveRCSCleanupAndUnchangedEditorDecline(t *testing.T) {
	d, root := liveP4(t)
	liveAdd(t, d, "keyword.txt", []byte("$Id$\nnear line\nmiddle\n"), "text+k")
	liveSubmit(t, d, "keyword base")
	if _, e := d.p4List("sync", "-f", "//depot/keyword.txt"); e != nil {
		t.Fatal(e)
	}
	clone := filepath.Join(root, "clone")
	if e := d.sync(cliOptions{command: "clone", values: map[string][]string{"destination": {clone}}, flags: map[string]bool{}, args: []string{"//depot/@all"}}); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"user.name": "Fixture User", "user.email": "fixture@example.invalid", "git-p4.attemptRCSCleanup": "true"} {
		if _, e := d.gitCall(nil, nil, "config", k, v); e != nil {
			t.Fatal(e)
		}
	}
	d.config = map[string][]string{}
	if e := os.WriteFile(filepath.Join(clone, "keyword.txt"), []byte("$Id$\nnear line edited\nmiddle\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "add", "keyword.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.gitCall(nil, nil, "commit", "-m", "keyword edit"); e != nil {
		t.Fatal(e)
	}
	t.Setenv("P4EDITOR", "true")
	d.in = strings.NewReader("n\n")
	opts := cliOptions{command: "submit", values: map[string][]string{}, flags: map[string]bool{"disable-p4sync": true}}
	if e := d.submit(opts); e == nil || !strings.Contains(e.Error(), "declined") {
		t.Fatalf("expected interactive decline after RCS cleanup, got %v", e)
	}
	opened, e := d.p4List("opened")
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range opened {
		if text(item["code"]) == "stat" {
			t.Fatal("declined submit left files opened")
		}
	}
	d.in = strings.NewReader("y\n")
	if e := d.submit(opts); e != nil {
		t.Fatal(e)
	}
	details, e := d.p4Single("describe", "-s", "2")
	if e != nil || !strings.Contains(text(details["desc"]), "keyword edit") {
		t.Fatal(details, e)
	}
}
