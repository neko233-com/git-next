// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

func acceptCheck(ok bool, format string, args ...any) {
	if !ok {
		panic(fmt.Errorf(format, args...))
	}
}
func acceptMust(e error) {
	if e != nil {
		panic(e)
	}
}
func acceptBytes(p string) []byte           { b, e := os.ReadFile(p); acceptMust(e); return b }
func acceptHash(p string) string            { v, e := digest(p); acceptMust(e); return v }
func acceptWrite(p string, b []byte)        { acceptMust(write(p, b, 0644)) }
func acceptJSON(b []byte) record            { var v record; acceptMust(json.Unmarshal(b, &v)); return v }
func acceptEqual(a, b []byte, label string) { acceptCheck(bytes.Equal(a, b), "%s differs", label) }

var fixtureCredentialText = regexp.MustCompile(`(?i)(Basic|Bearer)\s+[A-Za-z0-9+/=_-]+|(?:password|token|authorization|secret)\s*[=:]\s*[^\r\n]+`)

func acceptCommandDescription(args []string) string {
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	return strings.Join(args[:min(len(args), 2)], " ")
}
func acceptRun(cwd string, env []string, input []byte, ok bool, exe string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	value, e := runProcess(ctx, cwd, env, input, exe, args...)
	acceptMust(e)
	acceptCheck((value.code == 0) == ok, "command [%s] exited %d: %.700s", acceptCommandDescription(args), value.code, fixtureCredentialText.ReplaceAllString(string(value.stderr), "<redacted>"))
	if !ok {
		acceptCheck(value.code == 1 || value.code == 2 || value.code == 128 || value.code == 129, "command [%s] abnormal exit %d", acceptCommandDescription(args), value.code)
	}
	return value.stdout
}
func acceptCapture(name string, fn func()) (r record) {
	r = record{"case": name, "status": "pass"}
	defer func() {
		if e := recover(); e != nil {
			r["status"] = "fail"
			r["error"] = fmt.Sprint(e)
		}
	}()
	fn()
	return
}
func acceptReport(a *app, o options, r record) error {
	if o.get("output") != "" {
		if e := writeNewJSON(o.get("output"), r); e != nil {
			return e
		}
	}
	if e := a.json(r); e != nil {
		return e
	}
	if r["status"] != "pass" && r["status"] != "passed" {
		return fmt.Errorf("acceptance failed; inspect result cases")
	}
	return nil
}
func acceptStatus(results []record) string {
	for _, r := range results {
		if r["status"] != "pass" {
			return "fail"
		}
	}
	return "pass"
}
func acceptEnv(home string) []string {
	env := []string{}
	for _, v := range cleanEnvironment(home) {
		key, _, _ := strings.Cut(v, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GCM_") || strings.HasPrefix(upper, "SSH_") || strings.HasPrefix(upper, "GNUPG") {
			continue
		}
		env = append(env, v)
	}
	return fixtureEnv(env, map[string]string{"PROGRAMDATA": filepath.Join(home, "programdata"), "APPDATA": filepath.Join(home, "appdata"), "LOCALAPPDATA": filepath.Join(home, "local"), "GIT_NEXT_CONFIG_HOME": filepath.Join(home, "git-next"), "GIT_NEXT_COMPONENT_HOME": filepath.Join(home, "components"), "GNUPGHOME": filepath.Join(home, "gnupg"), "GIT_AUTHOR_NAME": "Fixture", "GIT_COMMITTER_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_EMAIL": "fixture@example.invalid", "GIT_AUTHOR_DATE": "2001-02-03T04:05:06Z", "GIT_COMMITTER_DATE": "2001-02-03T04:05:06Z", "GIT_EDITOR": "true", "GIT_SEQUENCE_EDITOR": "true", "LANG": "C", "LC_ALL": "C"})
}
func acceptBinary(o options) (string, error) {
	if len(o.args) != 1 {
		return "", fmt.Errorf("one binary positional argument is required")
	}
	p, e := filepath.Abs(o.args[0])
	if e != nil {
		return "", e
	}
	if !exists(p) {
		return "", fmt.Errorf("binary does not exist: %s", p)
	}
	return p, nil
}
func acceptReference(o options) (string, error) {
	value := o.get("reference")
	if value == "" {
		return exec.LookPath("git")
	}
	return filepath.Abs(value)
}

type cliFixture struct {
	root, repo, binary, reference, base string
	env                                 []string
	commands                            int
	stable                              bool
}

func newCLIFixture(root, binary, reference, format string, stable bool) *cliFixture {
	home := filepath.Join(root, "home")
	acceptMust(os.MkdirAll(home, 0700))
	f := &cliFixture{root: root, repo: filepath.Join(root, "中文 repo"), binary: binary, reference: reference, env: acceptEnv(home), stable: stable}
	f.At(root, "init", "-q", "-b", "main", "--object-format="+format, f.repo)
	f.Write("source.txt", []byte("base\n"))
	f.Write("中文 目录/asset.unity", []byte("%YAML 1.1\nvalue: base\n"))
	f.Commit("base")
	f.base = strings.TrimSpace(string(f.G("rev-parse", "HEAD")))
	return f
}
func (f *cliFixture) run(where string, reference, ok bool, input []byte, extra map[string]string, args ...string) []byte {
	exe := f.binary
	env := f.env
	if reference {
		exe = f.reference
	} else if f.stable {
		prefix := filepath.Dir(filepath.Dir(f.binary))
		path := strings.Join([]string{filepath.Dir(f.binary), filepath.Join(prefix, "runtime", "bin"), "/usr/bin", "/bin"}, string(os.PathListSeparator))
		if runtime.GOOS == "windows" {
			path = strings.Join([]string{filepath.Dir(f.binary), filepath.Join(prefix, "runtime", "usr", "bin"), filepath.Join(os.Getenv("SystemRoot"), "System32")}, string(os.PathListSeparator))
		}
		env = fixtureEnv(env, map[string]string{"PATH": path})
	}
	if f.stable {
		flags := []string{}
		for _, v := range []string{"core.fscache=false", "core.fsmonitor=false", "core.preloadIndex=false", "index.threads=1", "grep.threads=1"} {
			flags = append(flags, "-c", v)
		}
		args = append(flags, args...)
	}
	env = fixtureEnv(env, extra)
	f.commands++
	return acceptRun(where, env, input, ok, exe, args...)
}
func (f *cliFixture) G(args ...string) []byte { return f.run(f.repo, false, true, nil, nil, args...) }
func (f *cliFixture) Bad(args ...string) []byte {
	return f.run(f.repo, false, false, nil, nil, args...)
}
func (f *cliFixture) At(where string, args ...string) []byte {
	return f.run(where, false, true, nil, nil, args...)
}
func (f *cliFixture) Compare(args ...string) []byte {
	value := f.G(args...)
	acceptEqual(value, f.run(f.repo, true, true, nil, nil, args...), fmt.Sprint(args))
	return value
}
func (f *cliFixture) Write(name string, data []byte) string {
	p := filepath.Join(f.repo, filepath.FromSlash(name))
	acceptWrite(p, data)
	return p
}
func (f *cliFixture) Commit(msg string) { f.G("add", "."); f.G("commit", "-qm", msg) }
func (f *cliFixture) CommitAt(where, msg string) {
	f.At(where, "add", ".")
	f.At(where, "commit", "-qm", msg)
}
func (f *cliFixture) Clean() { acceptEqual(f.Compare("status", "--porcelain"), nil, "clean status") }
func (f *cliFixture) Verify() {
	f.Compare("ls-files", "--stage", "-z")
	f.Compare("log", "--format=%H", "-10")
	f.G("fsck", "--full")
	f.run(f.repo, true, true, nil, nil, "fsck", "--full")
}

var commonCases = map[string]func(*cliFixture){
	"history": func(f *cliFixture) {
		f.Clean()
		f.Compare("show", "HEAD:source.txt")
		acceptEqual(f.Compare("diff", "--no-ext-diff"), nil, "empty diff")
		f.Write("source.txt", []byte("changed\n"))
		acceptCheck(bytes.Contains(f.Compare("diff", "--no-ext-diff"), []byte("+changed")), "changed diff")
		f.Commit("changed")
		acceptCheck(len(bytes.Fields(f.Compare("rev-list", "HEAD"))) == 2, "history count")
	},
	"branch_checkout": func(f *cliFixture) {
		f.G("branch", "topic")
		f.G("branch", "-m", "topic", "feature")
		f.G("checkout", "feature")
		acceptEqual(bytes.TrimSpace(f.G("branch", "--show-current")), []byte("feature"), "current branch")
		f.Write("source.txt", []byte("dirty\n"))
		f.G("checkout", "--", "source.txt")
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("base\n"), "checkout")
		f.G("checkout", "-b", "temporary", "main")
		f.G("checkout", "--detach", f.base)
		f.Bad("symbolic-ref", "-q", "HEAD")
		acceptEqual(bytes.TrimSpace(f.G("rev-parse", "HEAD")), []byte(f.base), "detached head")
		f.G("checkout", "main")
		f.G("branch", "-d", "temporary", "feature")
		f.Compare("branch", "--format=%(refname:short)")
		f.Clean()
	},
	"merge_revert": func(f *cliFixture) {
		f.G("checkout", "-b", "topic")
		f.Write("topic.txt", []byte("topic\n"))
		f.Commit("topic")
		topic := strings.TrimSpace(string(f.G("rev-parse", "HEAD")))
		f.G("checkout", "main")
		f.G("merge", "--no-ff", "--no-edit", "topic")
		acceptCheck(len(bytes.Fields(f.G("rev-list", "--parents", "-n", "1", "HEAD"))) == 3, "merge parents")
		f.G("checkout", "-b", "picked", f.base)
		f.G("cherry-pick", topic)
		tree := f.G("rev-parse", "HEAD^{tree}")
		f.G("revert", "--no-edit", "HEAD")
		acceptCheck(!exists(filepath.Join(f.repo, "topic.txt")), "revert retained topic")
		f.G("revert", "--no-edit", "HEAD")
		acceptEqual(f.G("rev-parse", "HEAD^{tree}"), tree, "double revert")
		f.Compare("show-ref")
		f.Clean()
	},
	"rebase": func(f *cliFixture) {
		f.G("checkout", "-b", "topic")
		f.Write("topic.txt", []byte("topic\n"))
		f.Commit("topic")
		f.G("checkout", "main")
		f.Write("main.txt", []byte("main\n"))
		f.Commit("main advance")
		f.G("checkout", "topic")
		f.G("rebase", "main")
		acceptEqual(f.G("merge-base", "main", "HEAD"), f.G("rev-parse", "main"), "rebase ancestor")
		acceptCheck(exists(filepath.Join(f.repo, "topic.txt")) && exists(filepath.Join(f.repo, "main.txt")), "rebase tree")
		f.Clean()
	},
	"restore_reset": func(f *cliFixture) {
		f.Write("second.txt", []byte("second\n"))
		f.Commit("second")
		head := strings.TrimSpace(string(f.G("rev-parse", "HEAD")))
		tree := f.G("write-tree")
		f.Write("source.txt", []byte("staged\n"))
		f.G("add", "source.txt")
		f.G("restore", "--staged", "source.txt")
		acceptEqual(f.G("write-tree"), tree, "unstage tree")
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("staged\n"), "unstage worktree")
		f.G("restore", "--worktree", "source.txt")
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("base\n"), "restore")
		f.G("reset", "--soft", "HEAD^")
		acceptEqual(f.G("write-tree"), tree, "soft tree")
		f.G("reset", "--mixed", head)
		f.Write("source.txt", []byte("discarded fixture\n"))
		f.G("reset", "--hard", head)
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("base\n"), "hard tree")
		f.Clean()
	},
	"stash_untracked": func(f *cliFixture) {
		f.Write("source.txt", []byte("staged\n"))
		f.G("add", "source.txt")
		p := f.Write("untracked.txt", []byte("untracked\n"))
		f.G("stash", "push", "--include-untracked", "-m", "fixture")
		acceptCheck(!exists(p), "stash untracked")
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("base\n"), "stash tracked")
		f.Clean()
		f.G("stash", "pop", "--index")
		acceptEqual(acceptBytes(p), []byte("untracked\n"), "pop untracked")
		acceptEqual(f.G("show", ":source.txt"), []byte("staged\n"), "pop index")
		f.G("reset", "--hard", f.base)
		acceptMust(os.Remove(p))
		acceptEqual(f.G("stash", "list"), nil, "stash list")
		f.Clean()
	},
	"invalid_operations": func(f *cliFixture) {
		index := acceptHash(filepath.Join(f.repo, ".git", "index"))
		head := f.G("rev-parse", "HEAD")
		f.Bad("checkout", "missing-fixture-reference")
		f.Bad("branch", "-D", "main")
		f.Bad("revert", "not-a-fixture-commit")
		acceptEqual(f.G("rev-parse", "HEAD"), head, "invalid head")
		acceptCheck(acceptHash(filepath.Join(f.repo, ".git", "index")) == index, "invalid index")
		f.Clean()
	},
	"worktree": func(f *cliFixture) {
		linked, moved := filepath.Join(f.root, "linked 中文"), filepath.Join(f.root, "moved 中文")
		f.G("worktree", "add", "-b", "wt", linked, "main")
		acceptCheck(exists(filepath.Join(linked, ".git")), "linked .git")
		acceptWrite(filepath.Join(linked, "linked.txt"), []byte("linked\n"))
		f.CommitAt(linked, "linked fixture")
		acceptEqual(bytes.TrimSpace(f.G("rev-parse", "HEAD")), []byte(f.base), "main head")
		f.Compare("worktree", "list", "--porcelain")
		f.G("worktree", "lock", "--reason", "fixture", linked)
		f.Bad("worktree", "remove", linked)
		acceptCheck(exists(linked), "locked worktree lost")
		f.G("worktree", "unlock", linked)
		f.G("worktree", "move", linked, moved)
		acceptCheck(exists(moved) && !exists(linked), "worktree move")
		acceptWrite(filepath.Join(moved, "source.txt"), []byte("dirty linked\n"))
		f.Bad("worktree", "remove", moved)
		acceptCheck(exists(moved), "dirty worktree lost")
		f.G("worktree", "remove", "--force", moved)
		f.G("worktree", "prune")
		f.G("branch", "-D", "wt")
		acceptCheck(!exists(moved), "worktree retained")
		f.Compare("worktree", "list", "--porcelain")
		f.Clean()
	},
	"transport": func(f *cliFixture) {
		remote, second := filepath.Join(f.root, "remote.git"), filepath.Join(f.root, "second")
		f.At(f.root, "clone", "--bare", f.repo, remote)
		f.G("remote", "add", "origin", remote)
		f.G("push", "-u", "origin", "main")
		f.G("branch", "--set-upstream-to", "origin/main", "main")
		f.At(f.root, "clone", "--no-local", remote, second)
		acceptWrite(filepath.Join(second, "remote.txt"), []byte("remote\n"))
		f.CommitAt(second, "remote fixture")
		f.At(second, "push", "origin", "main")
		f.G("fetch", "origin")
		f.G("pull", "--ff-only", "origin", "main")
		acceptEqual(f.G("rev-parse", "HEAD"), f.At(second, "rev-parse", "HEAD"), "transport head")
		f.G("checkout", "--track", "-b", "tracked", "origin/main")
		acceptEqual(f.G("rev-parse", "@{upstream}"), f.G("rev-parse", "origin/main"), "tracking")
		f.Clean()
	},
	"conflicts": func(f *cliFixture) {
		f.G("checkout", "-b", "topic")
		f.Write("source.txt", []byte("topic\n"))
		f.Commit("topic")
		topic := strings.TrimSpace(string(f.G("rev-parse", "HEAD")))
		f.G("checkout", "main")
		f.Write("source.txt", []byte("main\n"))
		f.Commit("main")
		head := f.G("rev-parse", "HEAD")
		for _, pair := range [][]string{{"merge", "--no-edit", "topic"}, {"cherry-pick", topic}} {
			f.Bad(pair...)
			acceptCheck(len(f.Compare("ls-files", "--unmerged")) > 0, "conflict stages")
			f.G(pair[0], "--abort")
			acceptEqual(f.G("rev-parse", "HEAD"), head, "abort head")
			f.Clean()
		}
		f.G("checkout", "topic")
		f.Bad("rebase", "main")
		f.G("rebase", "--abort")
		acceptEqual(bytes.TrimSpace(f.G("rev-parse", "HEAD")), []byte(topic), "rebase abort")
		f.Clean()
	},
	"bundle_archive_tags": func(f *cliFixture) {
		f.G("tag", "lightweight")
		f.G("tag", "-a", "v1", "-m", "fixture tag")
		f.Compare("tag", "--list")
		acceptEqual(bytes.TrimSpace(f.G("rev-parse", "v1^{}")), []byte(f.base), "tag target")
		archive, bundle, clone := filepath.Join(f.root, "repo.zip"), filepath.Join(f.root, "repo.bundle"), filepath.Join(f.root, "bundle-clone")
		f.G("archive", "--format=zip", "-o", archive, "HEAD")
		z, e := zip.OpenReader(archive)
		acceptMust(e)
		defer z.Close()
		found := false
		for _, file := range z.File {
			if file.Name == "source.txt" {
				r, e := file.Open()
				acceptMust(e)
				b, e := io.ReadAll(r)
				acceptMust(e)
				acceptMust(r.Close())
				acceptEqual(b, []byte("base\n"), "archive content")
				found = true
			}
		}
		acceptCheck(found, "archive source missing")
		f.G("bundle", "create", bundle, "--all")
		f.G("bundle", "verify", bundle)
		f.At(f.root, "clone", bundle, clone)
		acceptEqual(bytes.TrimSpace(f.At(clone, "rev-parse", "HEAD")), []byte(f.base), "bundle clone")
		acceptCheck(len(bytes.Fields(f.G("reflog", "--format=%H"))) > 0, "reflog")
		f.G("tag", "-d", "lightweight", "v1")
		f.Clean()
	},
	"ignore_clean_attributes": func(f *cliFixture) {
		f.Write(".gitignore", []byte("ignored/\n"))
		f.Write(".gitattributes", []byte("*.unity text eol=lf\n*.bin -text\n"))
		binary := []byte{0, 13, 10, 255, 13, 10}
		f.Write("blob.bin", binary)
		f.Commit("attributes")
		ignored, scratch := f.Write("ignored/cache.txt", []byte("ignored\n")), f.Write("scratch.txt", []byte("scratch\n"))
		acceptEqual(bytes.TrimSpace(f.G("check-ignore", "ignored/cache.txt")), []byte("ignored/cache.txt"), "ignore")
		acceptCheck(bytes.Contains(f.G("clean", "-ndx"), []byte("ignored/")) && exists(ignored) && exists(scratch), "dry clean")
		f.G("clean", "-fd")
		acceptCheck(exists(ignored) && !exists(scratch), "clean ignored")
		f.G("clean", "-fdx")
		acceptCheck(!exists(ignored), "clean x")
		f.Write("中文 目录/asset.unity", []byte("%YAML 1.1\r\nvalue: base\r\n"))
		f.G("add", ".")
		acceptEqual(f.G("diff", "--cached", "--no-ext-diff"), nil, "normalized diff")
		acceptEqual(f.G("show", "HEAD:blob.bin"), binary, "binary intact")
		acceptCheck(bytes.Contains(f.Compare("check-attr", "text", "eol", "--", "中文 目录/asset.unity"), []byte("eol: lf")), "attributes")
	},
	"locks_and_move": func(f *cliFixture) {
		head := f.G("rev-parse", "HEAD")
		index := filepath.Join(f.repo, ".git", "index")
		original := acceptHash(index)
		lock := index + ".lock"
		acceptWrite(lock, []byte("owned fixture lock\n"))
		f.Write("source.txt", []byte("modified\n"))
		f.Bad("add", ".")
		acceptEqual(acceptBytes(lock), []byte("owned fixture lock\n"), "owned lock")
		acceptCheck(acceptHash(index) == original, "index changed under lock")
		acceptMust(os.Remove(lock))
		f.G("restore", "source.txt")
		f.G("mv", "source.txt", "中文 renamed.txt")
		f.Commit("rename")
		acceptEqual(f.G("show", "HEAD:中文 renamed.txt"), []byte("base\n"), "rename")
		f.G("rm", "中文 renamed.txt")
		f.Commit("remove")
		acceptCheck(!exists(filepath.Join(f.repo, "中文 renamed.txt")), "rm retained file")
		acceptCheck(!bytes.Equal(f.G("rev-parse", "HEAD"), head), "commits missing")
		f.Clean()
	},
	"checkout_protection": func(f *cliFixture) {
		f.G("checkout", "-b", "topic")
		f.Write("source.txt", []byte("topic\n"))
		f.Write("topic-only.txt", []byte("committed topic\n"))
		f.Commit("topic")
		f.G("checkout", "main")
		f.Write("source.txt", []byte("user work\n"))
		index := acceptHash(filepath.Join(f.repo, ".git", "index"))
		f.Bad("checkout", "topic")
		acceptEqual(bytes.TrimSpace(f.G("branch", "--show-current")), []byte("main"), "protection branch")
		acceptCheck(acceptHash(filepath.Join(f.repo, ".git", "index")) == index, "protection index")
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("user work\n"), "protected content")
		f.G("restore", "source.txt")
		collision := f.Write("topic-only.txt", []byte("untracked work\n"))
		f.Bad("checkout", "topic")
		acceptEqual(acceptBytes(collision), []byte("untracked work\n"), "protected untracked")
		acceptEqual(bytes.TrimSpace(f.G("branch", "--show-current")), []byte("main"), "collision branch")
		acceptMust(os.Remove(collision))
		f.Clean()
	},
	"resolved_conflicts": func(f *cliFixture) {
		f.G("checkout", "-b", "topic")
		f.Write("source.txt", []byte("topic\n"))
		f.Commit("topic")
		topic := strings.TrimSpace(string(f.G("rev-parse", "HEAD")))
		f.G("checkout", "main")
		f.Write("source.txt", []byte("main\n"))
		f.Commit("main")
		f.Bad("cherry-pick", topic)
		f.Write("source.txt", []byte("merged\n"))
		f.G("add", "source.txt")
		f.G("cherry-pick", "--continue")
		acceptEqual(f.G("show", "HEAD:source.txt"), []byte("merged\n"), "pick resolution")
		f.Clean()
		f.G("checkout", "topic")
		f.Bad("rebase", "main")
		f.Write("source.txt", []byte("rebased resolution\n"))
		f.G("add", "source.txt")
		f.G("rebase", "--continue")
		acceptEqual(f.G("merge-base", "main", "HEAD"), f.G("rev-parse", "main"), "resolution ancestor")
		f.Clean()
		f.Bad("revert", "--no-edit", topic)
		acceptCheck(len(f.Compare("ls-files", "--unmerged")) > 0, "revert stages")
		f.G("revert", "--abort")
		acceptEqual(acceptBytes(filepath.Join(f.repo, "source.txt")), []byte("rebased resolution\n"), "revert abort resolution")
		f.Clean()
	},
	"rejected_push": func(f *cliFixture) {
		remote, second := filepath.Join(f.root, "remote.git"), filepath.Join(f.root, "second")
		f.At(f.root, "clone", "--bare", f.repo, remote)
		f.G("remote", "add", "origin", remote)
		f.G("push", "-u", "origin", "main")
		f.At(f.root, "clone", remote, second)
		acceptWrite(filepath.Join(second, "other.txt"), []byte("other writer\n"))
		f.CommitAt(second, "other writer")
		f.At(second, "push", "origin", "main")
		remoteHead := f.At(remote, "rev-parse", "refs/heads/main")
		f.Write("local.txt", []byte("local writer\n"))
		f.Commit("local writer")
		head := f.G("rev-parse", "HEAD")
		f.Bad("push", "origin", "main")
		f.Bad("push", "--force-with-lease=refs/heads/main:"+f.base, "origin", "main")
		acceptEqual(f.G("rev-parse", "HEAD"), head, "rejected local ref")
		acceptEqual(f.At(remote, "rev-parse", "refs/heads/main"), remoteHead, "rejected remote ref")
		f.G("fetch", "origin")
		f.G("rebase", "origin/main")
		f.G("push", "origin", "main")
		acceptEqual(f.G("rev-parse", "HEAD"), f.At(remote, "rev-parse", "refs/heads/main"), "recovered push")
		f.Clean()
	},
}

func nativeCommonCLI(a *app, o options) error {
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
	jobs, e := o.integer("jobs", 4)
	if e != nil || jobs < 1 || jobs > 8 {
		return fmt.Errorf("--jobs must be 1..8")
	}
	names := []string{}
	if selected := o.all("case"); len(selected) > 0 {
		for _, group := range selected {
			names = append(names, strings.Split(group, ",")...)
		}
	} else {
		for name := range commonCases {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	for _, name := range names {
		if commonCases[name] == nil {
			return fmt.Errorf("unknown case %s", name)
		}
	}
	before := acceptHash(binary)
	started := time.Now()
	type job struct{ name, format string }
	queue := make(chan job)
	var mu sync.Mutex
	results := []record{}
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Go(func() {
			for j := range queue {
				begin := time.Now()
				var f *cliFixture
				r := acceptCapture(j.name, func() {
					temp, e := os.MkdirTemp("", "git-next-common-cli-")
					acceptMust(e)
					defer os.RemoveAll(temp)
					f = newCLIFixture(temp, binary, reference, j.format, true)
					commonCases[j.name](f)
					f.Verify()
				})
				r["object_format"] = j.format
				r["seconds"] = time.Since(begin).Seconds()
				r["commands"] = 0
				if f != nil {
					r["commands"] = f.commands
				}
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		})
	}
	for _, name := range names {
		for _, format := range []string{"sha1", "sha256"} {
			queue <- job{name, format}
		}
	}
	close(queue)
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		return fmt.Sprint(results[i]["case"], results[i]["object_format"]) < fmt.Sprint(results[j]["case"], results[j]["object_format"])
	})
	acceptCheck(acceptHash(binary) == before, "binary changed during test")
	return acceptReport(a, o, record{"status": acceptStatus(results), "binary_sha256": before, "reference_sha256": acceptHash(reference), "harness_sha256": acceptHash(a.path("tools/devtool/acceptance_common.go")), "scope": "independent common CLI scenarios and recovery; SHA-1 and SHA-256; reference object/index readers", "seconds": time.Since(started).Seconds(), "parallel_jobs": jobs, "personal_profiles_accessed": false, "external_git_runtime_fallback": false, "results": results})
}
func init() {
	register("native-common-cli-acceptance", "Independent SHA-1/SHA-256 native CLI recovery matrix", "reference output case jobs", nativeCommonCLI)
}
