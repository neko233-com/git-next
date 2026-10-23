// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func nativeContract(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	reference, e := acceptReference(o)
	if e != nil {
		return e
	}
	if e = o.require("output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("use a new batch result")
	}
	root, e := os.MkdirTemp("", "git-next-native-contract-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	if e = os.MkdirAll(home, 0700); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_AUTHOR_DATE": "2026-10-20T00:00:00Z", "GIT_COMMITTER_DATE": "2026-10-20T00:00:00Z", "GIT_NEXT_LANGUAGE": "english"})
	f := &cliFixture{root: root, binary: binary, reference: reference, env: env}
	seed := func(name, format string) *cliFixture {
		p := filepath.Join(root, name)
		f.run(root, true, true, nil, nil, "init", "-q", "-b", "main", "--object-format="+format, p)
		c := *f
		c.repo = p
		c.run(p, true, true, nil, nil, "config", "core.autocrlf", "input")
		c.run(p, true, true, nil, nil, "config", "core.eol", "lf")
		c.Write(".gitattributes", []byte("* text=auto eol=lf\n*.bin -text\n"))
		c.Write("scene.unity", []byte("%YAML 1.1\nvalue: original\n"))
		c.Write("data.bin", []byte{0, 13, 10, 255, 10})
		c.run(p, true, true, nil, nil, "add", "-A")
		c.run(p, true, true, nil, nil, "commit", "-qm", "seed")
		return &c
	}
	results := []record{}
	add := func(name string, fn func()) { results = append(results, acceptCapture(name, fn)) }
	add("D04-native-identity-and-stable-json", func() {
		acceptCheck(bytes.Contains(f.At(root, "--version"), []byte("git-next")), "identity version")
		acceptCheck(bytes.Contains(f.At(root, "version", "--build-options"), []byte("product: git-next")), "build product")
		data := f.At(root, "version", "--json")
		v := acceptJSON(data)
		acceptCheck(v["product"] == "git-next" && v["external_git_fallback"] == false && v["external_ssh_fallback"] == false, "fallback identity")
		acceptEqual(f.run(root, false, true, nil, map[string]string{"LANG": "zh_CN.UTF-8"}, "version", "--json"), data, "stable identity JSON")
	})
	add("D04-language-set-get-auto-and-cli-priority", func() {
		noLanguage := []string{}
		for _, v := range env {
			if !strings.HasPrefix(v, "GIT_NEXT_LANGUAGE=") {
				noLanguage = append(noLanguage, v)
			}
		}
		run := func(args ...string) []byte { return acceptRun(root, noLanguage, nil, true, binary, args...) }
		run("language", "set", "chinese")
		acceptCheck(acceptJSON(run("language", "get", "--json"))["language"] == "chinese", "Chinese preference")
		run("language", "set", "english")
		acceptCheck(acceptJSON(run("language", "get", "--json"))["language"] == "english", "English preference")
		run("language", "set", "auto")
		acceptCheck(acceptJSON(f.At(root, "--language=chinese", "language", "get", "--json"))["language"] == "chinese", "CLI priority")
	})
	for _, format := range []string{"sha1", "sha256"} {
		add("D01-objects-index-pack-gc-"+format, func() {
			c := seed("objects-"+format, format)
			for _, args := range [][]string{{"status", "--porcelain=v1", "-z"}, {"status", "--porcelain=v2", "-z"}, {"rev-parse", "HEAD"}, {"show", "--format=fuller", "HEAD"}, {"ls-files", "-s", "-z"}, {"ls-tree", "-rz", "HEAD"}, {"log", "--format=%H %s"}, {"diff", "--raw"}, {"cat-file", "-p", "HEAD"}} {
				c.Compare(args...)
			}
			c.G("gc", "--prune=now")
			c.G("commit-graph", "write", "--reachable")
			c.G("fsck", "--full", "--strict")
			c.Compare("cat-file", "-p", "HEAD:scene.unity")
			acceptEqual(c.run(c.repo, false, true, []byte("HEAD\n"), nil, "cat-file", "--batch-check"), c.run(c.repo, true, true, []byte("HEAD\n"), nil, "cat-file", "--batch-check"), "batch check")
		})
	}
	add("D03-unity-pure-newlines-real-edits-binary-and-explicit-policy", func() {
		c := seed("line-endings", "sha1")
		c.Write("scene.unity", bytes.ReplaceAll(acceptBytes(filepath.Join(c.repo, "scene.unity")), []byte("\n"), []byte("\r\n")))
		acceptEqual(c.G("status", "--porcelain=v1", "-z"), nil, "pure EOL status")
		acceptEqual(c.G("diff", "--exit-code"), nil, "pure EOL diff")
		acceptEqual(acceptBytes(filepath.Join(c.repo, "data.bin")), []byte{0, 13, 10, 255, 10}, "binary EOL")
		c.Write("scene.unity", []byte("%YAML 1.1\r\nvalue: changed\r\n"))
		acceptCheck(bytes.Contains(c.G("status", "--porcelain=v2", "-z"), []byte("scene.unity")), "real edit status")
		c.G("add", "scene.unity")
		acceptCheck(!bytes.Contains(c.G("show", ":scene.unity"), []byte("\r")), "LF staged")
		before := c.G("write-tree")
		v := acceptJSON(c.G("eol", "--apply", "--json"))
		acceptCheck(v["target_policy"] == "LF" && v["index_renormalized"] == false, "EOL report")
		acceptEqual(c.G("write-tree"), before, "EOL index")
	})
	add("D01-rename-switch-tag-stash-restore-merge-reset-pick-revert", func() {
		c := seed("changes", "sha1")
		c.G("switch", "-c", "topic")
		c.G("mv", "scene.unity", "renamed.unity")
		c.G("commit", "-qm", "rename")
		c.G("tag", "-a", "v-fixture", "-m", "tag fixture")
		c.Compare("show-ref", "--head")
		c.Write("renamed.unity", []byte("change\n"))
		c.G("stash", "push", "-m", "saved")
		acceptEqual(c.G("status", "--porcelain"), nil, "stash clean")
		c.G("stash", "pop")
		acceptEqual(acceptBytes(filepath.Join(c.repo, "renamed.unity")), []byte("change\n"), "stash pop")
		c.G("restore", "--source=HEAD", "--worktree", "renamed.unity")
		c.G("switch", "main")
		c.G("merge", "--ff-only", "topic")
		c.G("reset", "--hard", "HEAD^")
		c.G("cherry-pick", "topic")
		c.G("revert", "--no-edit", "HEAD")
		c.G("fsck", "--full")
	})
	add("D01-conflict-stages-and-merge-rebase-abort", func() {
		c := seed("conflicts", "sha1")
		c.G("switch", "-c", "topic")
		c.Write("scene.unity", []byte("topic\n"))
		c.G("commit", "-qam", "topic")
		c.G("switch", "main")
		c.Write("scene.unity", []byte("main\n"))
		c.G("commit", "-qam", "main")
		head := c.G("rev-parse", "HEAD")
		c.Bad("merge", "topic")
		c.Compare("ls-files", "--unmerged", "-z")
		c.G("merge", "--abort")
		acceptEqual(c.G("rev-parse", "HEAD"), head, "merge abort")
		c.Bad("rebase", "topic")
		c.G("rebase", "--abort")
		acceptEqual(c.G("rev-parse", "HEAD"), head, "rebase abort")
	})
	add("D01-owned-local-upload-receive-clone-fetch-pull-push", func() {
		c := seed("local-transport", "sha1")
		bare, clone := filepath.Join(root, "server.git"), filepath.Join(root, "clone")
		c.At(root, "clone", "--bare", c.repo, bare)
		c.At(root, "clone", "--no-local", bare, clone)
		acceptWrite(filepath.Join(clone, "new.txt"), []byte("push\n"))
		c.CommitAt(clone, "push")
		c.At(clone, "push", "origin", "HEAD:main")
		c.G("remote", "add", "origin", bare)
		c.G("fetch", "origin")
		c.G("pull", "--ff-only", "origin", "main")
		acceptEqual(c.G("rev-parse", "HEAD"), c.At(clone, "rev-parse", "HEAD"), "transport HEAD")
		c.Compare("ls-remote", bare)
		c.At(bare, "fsck", "--full")
	})
	add("D01-linked-worktree-index-and-removal", func() {
		c := seed("worktrees", "sha1")
		linked := filepath.Join(root, "linked")
		c.G("worktree", "add", "-b", "linked", linked)
		acceptCheck(len(c.At(linked, "rev-parse", "--git-common-dir")) > 0, "common dir")
		acceptWrite(filepath.Join(linked, "linked.txt"), []byte("linked\n"))
		c.CommitAt(linked, "linked")
		c.Compare("worktree", "list", "--porcelain")
		c.G("worktree", "remove", linked)
		c.G("worktree", "prune")
	})
	add("D01-recursive-submodule-clone-deinit-update", func() {
		c := seed("super", "sha1")
		sub := seed("sub", "sha1")
		c.G("-c", "protocol.file.allow=always", "submodule", "add", sub.repo, "module")
		c.G("commit", "-qam", "submodule")
		c.Compare("submodule", "status")
		clone := filepath.Join(root, "recursive")
		c.At(root, "-c", "protocol.file.allow=always", "clone", "--recurse-submodules", c.repo, clone)
		acceptCheck(exists(filepath.Join(clone, "module", "scene.unity")), "recursive file")
		c.At(clone, "submodule", "deinit", "-f", "--all")
		c.At(clone, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
	})
	add("D02-owned-shell-hooks-and-rejected-commit", func() {
		c := seed("hooks", "sha1")
		hook := c.Write(".git/hooks/pre-commit", []byte("#!/bin/sh\nprintf \"hook-ran\\n\" > hook.marker\nexit 1\n"))
		acceptMust(os.Chmod(hook, 0755))
		c.Write("new.txt", []byte("rejected\n"))
		c.G("add", "-A")
		head := c.G("rev-parse", "HEAD")
		c.Bad("commit", "-qm", "must reject")
		acceptEqual(acceptBytes(filepath.Join(c.repo, "hook.marker")), []byte("hook-ran\n"), "hook marker")
		acceptEqual(c.G("rev-parse", "HEAD"), head, "hook rejected head")
	})
	add("D01-sparse-checkout-and-split-index", func() {
		c := seed("sparse", "sha1")
		c.Write("folder/file.txt", []byte("folder\n"))
		c.Commit("folder")
		c.G("sparse-checkout", "init", "--cone")
		c.G("sparse-checkout", "set", "folder")
		c.Compare("ls-files", "-t")
		c.G("sparse-checkout", "disable")
		c.G("update-index", "--split-index")
		c.Compare("status", "--porcelain=v2", "-z")
		c.G("fsck", "--full")
	})
	add("D01-unknown-command-option-and-lock-preservation", func() {
		c := seed("failure", "sha1")
		head := c.G("rev-parse", "HEAD")
		c.Bad("this-command-does-not-exist")
		c.Bad("reset", "--invalid-option")
		acceptEqual(c.G("rev-parse", "HEAD"), head, "unknown head")
		acceptMust(filepath.WalkDir(filepath.Join(c.repo, ".git"), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			acceptCheck(!strings.HasSuffix(p, ".lock"), "left lock %s", p)
			return nil
		}))
	})
	return acceptReport(a, o, record{"schema_version": 1, "status": acceptStatus(results), "binary_sha256": acceptHash(binary), "reference_sha256": acceptHash(reference), "cases": results, "personal_profiles_accessed": false, "repositories_disposable": true})
}
func terminalAcceptance(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "terminal acceptance ")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	reference, e := acceptReference(o)
	before := ""
	if e == nil {
		before = acceptHash(reference)
	}
	r := acceptCapture("native-terminal", func() {
		env := acceptEnv(filepath.Join(root, "home"))
		setup := acceptJSON(acceptRun(root, env, nil, true, binary, "--cwd", root, "--print-setup"))
		acceptCheck(setup["system_git_replaced"] == false && setup["bash_compatibility"] == false, "terminal setup")
		acceptCheck(exists(stringValue(setup, "git_next")), "terminal Git missing")
		command := "printf \"terminal-marker\\n\"; \"$GIT_NEXT_EXECUTABLE\" --version; exit 7"
		if runtimeOSWindows() {
			command = "Write-Output terminal-marker; git-next --version; exit 7"
		}
		ctx, cancel := deadline()
		defer cancel()
		p, e := runProcess(ctx, root, env, nil, binary, "--cwd", root, "--command", command)
		acceptMust(e)
		acceptCheck(p.code == 7 && bytes.Contains(p.stdout, []byte("terminal-marker")) && bytes.Contains(p.stdout, []byte("git-next")), "terminal command/exit")
		p, e = runProcess(ctx, root, env, nil, binary, "--cwd", filepath.Join(root, "missing"), "--print-setup")
		acceptMust(e)
		acceptCheck(p.code != 0, "missing cwd accepted")
		if before != "" {
			acceptCheck(acceptHash(reference) == before, "system Git changed")
		}
	})
	return acceptReport(a, o, record{"status": r["status"], "results": []record{r}, "native_shell": true, "cwd_with_spaces": true, "exit_code": true, "system_git_unchanged": true})
}
func runtimeOSWindows() bool { return os.PathSeparator == '\\' }
func init() {
	register("native-contract-acceptance", "Disposable native-core contract matrix", "reference output", nativeContract)
	register("terminal-acceptance", "Native shell cwd, setup and exit propagation", "reference output", terminalAcceptance)
}
