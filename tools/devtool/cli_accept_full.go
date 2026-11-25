// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

func acceptFullCases(h *acceptHarness) []acceptCase {
	out := make([]acceptCase, 0)
	out = append(out, acceptDeepCases(h)...)
	out = append(out, acceptBroadCases(h)...)
	out = append(out, acceptConformanceCases(h)...)
	return out
}

// acceptSuiteSeed is the full-tier fixture: unlike the L0 seed it writes
// "seed\n" so stash and restore round trips can assert the original content.
func acceptSuiteSeed(h *acceptHarness, name string) (string, []byte, error) {
	repo := h.repo(name)
	if err := h.initFixtureRepo(repo, "main"); err != nil {
		return "", nil, err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("seed\n")); err != nil {
		return "", nil, err
	}
	if _, err := h.referenceGit(repo, "add", "README.md"); err != nil {
		return "", nil, err
	}
	if _, err := h.referenceGit(repo, "commit", "-m", "seed"); err != nil {
		return "", nil, err
	}
	head, err := h.referenceGit(repo, "rev-parse", "HEAD")
	return repo, head, err
}

// ---- local-deep -----------------------------------------------------------

type acceptSimpleCase struct {
	id string
	fn func(*acceptHarness) (string, error)
}

func acceptDeepCases(h *acceptHarness) []acceptCase {
	cases := []acceptSimpleCase{
		{"extended-stash", deepExtendedStash},
		{"hooks-pre-commit", deepHooksPreCommit},
		{"merge-ff-files", deepMergeFFFiles},
		{"restore-worktree", deepRestoreWorktree},
		{"status-porcelain-v2", deepStatusPorcelainV2},
		{"show-patch", deepShowPatch},
		{"diff-staged", deepDiffStaged},
		{"reset-soft", deepResetSoft},
	}
	out := make([]acceptCase, 0, len(cases))
	for _, item := range cases {
		id, fn := item.id, item.fn
		out = append(out, acceptRunCase("local-deep", id, func(h *acceptHarness, _ bool) (string, error) { return fn(h) }, h, false))
	}
	return out
}

func deepExtendedStash(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-stash")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("dirty\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "stash", "push", "-m", "wip"); err != nil {
		return "", err
	}
	status, err := h.clientOK(repo, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if len(status) != 0 {
		return "", fmt.Errorf("worktree not clean after stash push: %q", status)
	}
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		return "", err
	}
	if string(data) != "seed\n" {
		return "", fmt.Errorf("stashed file not restored: %q", data)
	}
	if _, err = h.clientOK(repo, "stash", "pop"); err != nil {
		return "", err
	}
	data, err = os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		return "", err
	}
	if string(data) != "dirty\n" {
		return "", fmt.Errorf("popped file differs: %q", data)
	}
	return "push pop round-trip", nil
}

func deepHooksPreCommit(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-hooks")
	if err != nil {
		return "", err
	}
	hooks := filepath.Join(repo, ".git", "hooks")
	if err = os.MkdirAll(hooks, 0o755); err != nil {
		return "", err
	}
	marker := h.repo("hook.ran")
	hook := filepath.Join(hooks, "pre-commit")
	if runtime.GOOS == "windows" {
		body := fmt.Sprintf("@echo off\r\necho ran> \"%s\"\r\n", filepath.ToSlash(marker))
		if err = cliAcceptWrite(hook, []byte(body)); err != nil {
			return "", err
		}
	} else {
		body := fmt.Sprintf("#!/bin/sh\necho ran > '%s'\n", filepath.ToSlash(marker))
		if err = cliAcceptWrite(hook, []byte(body)); err != nil {
			return "", err
		}
		if err = os.Chmod(hook, 0o755); err != nil {
			return "", err
		}
	}
	if err = cliAcceptWrite(filepath.Join(repo, "hook.txt"), []byte("x\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "hook.txt"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "commit", "-m", "hook test"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return "", fmt.Errorf("hook marker: %w", err)
	}
	if strings.TrimSpace(string(data)) != "ran" {
		return "", fmt.Errorf("hook marker is %q", data)
	}
	return "pre-commit ran", nil
}

func deepMergeFFFiles(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-merge")
	if err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "switch", "-c", "side"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "side.txt"), []byte("side\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "side.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "side"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "switch", "main"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "merge", "side", "--ff-only"); err != nil {
		return "", err
	}
	if !isFile(filepath.Join(repo, "side.txt")) {
		return "", fmt.Errorf("merged file missing")
	}
	return "ff-only merge tree", nil
}

func deepRestoreWorktree(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-restore")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("changed\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "restore", "README.md"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		return "", err
	}
	if string(data) != "seed\n" {
		return "", fmt.Errorf("restored file differs: %q", data)
	}
	return "restore path", nil
}

func deepStatusPorcelainV2(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-status-v2")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "new.txt"), []byte("n\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "new.txt"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain=v2"); err != nil {
		return "", err
	}
	return "porcelain v2", nil
}

func deepShowPatch(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-show")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "extra.txt"), []byte("e\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "extra.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "extra"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "show", "--no-patch", "HEAD"); err != nil {
		return "", err
	}
	return "show HEAD", nil
}

func deepDiffStaged(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-diff")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("staged\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "README.md"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "diff", "--cached", "--no-ext-diff"); err != nil {
		return "", err
	}
	return "cached diff", nil
}

func deepResetSoft(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "full-reset")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "extra.txt"), []byte("e\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "extra.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "extra"); err != nil {
		return "", err
	}
	before, err := h.clientOK(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "reset", "--soft", "HEAD~1"); err != nil {
		return "", err
	}
	after, err := h.clientOK(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if bytes.Equal(before, after) {
		return "", fmt.Errorf("soft reset did not move HEAD")
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	return "soft reset + staged status", nil
}

// ---- local-broad ----------------------------------------------------------

func acceptBroadCases(h *acceptHarness) []acceptCase {
	cases := []acceptSimpleCase{
		{"clone-local-tree", broadCloneLocal},
		{"fetch-pull-ff", broadFetchPullFF},
		{"version-doctor-json", broadVersionDoctor},
		{"plumbing-rev-parse", broadPlumbing},
		{"status-short-sb", broadStatusFormats},
		{"commit-message-cleanup", broadCommitCleanup},
		{"restore-source-head1", broadRestoreSource},
		{"restore-staged", broadRestoreStaged},
		{"reset-mixed", broadResetMixed},
		{"fail-clone-exists", broadFailClosed},
		{"index-lock-refuse", broadIndexLock},
	}
	out := make([]acceptCase, 0, len(cases))
	for _, item := range cases {
		id, fn := item.id, item.fn
		out = append(out, acceptRunCase("local-broad", id, func(h *acceptHarness, _ bool) (string, error) { return fn(h) }, h, false))
	}
	return out
}

func broadCloneLocal(h *acceptHarness) (string, error) {
	source := h.repo("broad-source")
	if err := h.initFixtureRepo(source, "main"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(source, "folder", "data.txt"), []byte("nested\n")); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(source, "README.md"), []byte("hello\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "add", "."); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "commit", "-m", "fixture"); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "tag", "-a", "v-fixture", "-m", "tag"); err != nil {
		return "", err
	}
	head, err := h.referenceGit(source, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	target := h.repo("broad-clone")
	if _, err = h.clientOK(h.root, "clone", source, target); err != nil {
		return "", err
	}
	got, err := h.clientOK(target, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, head) {
		return "", fmt.Errorf("clone head differs: %q vs %q", got, head)
	}
	status, err := h.clientOK(target, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if len(status) != 0 {
		return "", fmt.Errorf("clone worktree dirty: %q", status)
	}
	if err = h.compareStdout(target, "cat-file", "-p", "HEAD:README.md"); err != nil {
		return "", err
	}
	if err = h.compareStdout(target, "show-ref"); err != nil {
		return "", err
	}
	return "clone + refs + cat-file", nil
}

func broadFetchPullFF(h *acceptHarness) (string, error) {
	source := h.repo("broad-fetch-src")
	if err := h.initFixtureRepo(source, "main"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(source, "README.md"), []byte("hello\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "add", "."); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "commit", "-m", "one"); err != nil {
		return "", err
	}
	target := h.repo("broad-fetch-clone")
	if _, err := h.clientOK(h.root, "clone", source, target); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(source, "README.md"), []byte("second\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "add", "."); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "commit", "-m", "two"); err != nil {
		return "", err
	}
	if _, err := h.clientOK(target, "fetch"); err != nil {
		return "", err
	}
	if _, err := h.clientOK(target, "pull", "--ff-only"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(target, "README.md"))
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(data), "second") {
		return "", fmt.Errorf("pull did not update file: %q", data)
	}
	return "fetch + ff pull", nil
}

func broadVersionDoctor(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "broad-meta")
	if err != nil {
		return "", err
	}
	version, err := h.clientOK(repo, "version", "--json")
	if err != nil {
		return "", err
	}
	if !bytes.Contains(version, []byte("git-next")) {
		return "", fmt.Errorf("version JSON missing product")
	}
	doctor, err := h.clientOK(repo, "doctor", "--json")
	if err != nil {
		return "", err
	}
	if !bytes.Contains(doctor, []byte("replacement_ready")) {
		return "", fmt.Errorf("doctor JSON missing replacement_ready")
	}
	return "version + doctor json", nil
}

func broadPlumbing(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "broad-plumb")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "branch", "topic"); err != nil {
		return "", err
	}
	if err = h.compareCanonicalPathStdout(repo, "rev-parse", "--show-toplevel"); err != nil {
		return "", err
	}
	bare, err := h.clientOK(repo, "rev-parse", "--is-bare-repository")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(bare)) != "false" {
		return "", fmt.Errorf("is-bare-repository is %q", bare)
	}
	if _, err = h.clientOK(repo, "config", "user.name", "Plumbing"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "config", "user.name"); err != nil {
		return "", err
	}
	compat, err := h.clientOK(repo, "compatibility", "--json")
	if err != nil {
		return "", err
	}
	if !bytes.Contains(compat, []byte("git-next")) {
		return "", fmt.Errorf("compatibility JSON missing product")
	}
	if _, err = h.clientOK(repo, "language", "list"); err != nil {
		return "", err
	}
	return "plumbing meta", nil
}

func broadStatusFormats(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "broad-status")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "dirty.txt"), []byte("d\n")); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "-sb"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--short"); err != nil {
		return "", err
	}
	return "porcelain + short + branch", nil
}

func broadCommitCleanup(h *acceptHarness) (string, error) {
	repo := h.repo("broad-commit")
	if err := h.initFixtureRepo(repo, "main"); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "config", "commit.cleanup", "strip"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "file.txt"), []byte("base\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "add", "."); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "commit", "-m", "base"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "file.txt"), []byte("changed\n")); err != nil {
		return "", err
	}
	if _, err := h.clientOK(repo, "add", "file.txt"); err != nil {
		return "", err
	}
	msg := "\n# heading\r\n\r\nsubject with space  \r\n\r\n\r\nbody\t\r\n"
	if _, err := h.clientOK(repo, "commit", "-m", msg); err != nil {
		return "", err
	}
	if err := h.compareStdout(repo, "cat-file", "-p", "HEAD"); err != nil {
		return "", err
	}
	return "commit.cleanup strip", nil
}

func broadRestoreFixture(h *acceptHarness, name string) (string, error) {
	repo := h.repo(name)
	if err := h.initFixtureRepo(repo, "main"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(repo, ".gitattributes"), []byte("*.txt text eol=crlf\n")); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("first\r\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "add", "."); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "commit", "-m", "first"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("second\r\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "add", "a.txt"); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "commit", "-m", "second"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("dirty\r\n")); err != nil {
		return "", err
	}
	return repo, nil
}

func broadRestoreSource(h *acceptHarness) (string, error) {
	repo, err := broadRestoreFixture(h, "broad-restore-source")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "restore", "--source=HEAD~1", "a.txt"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	if err != nil {
		return "", err
	}
	if string(data) != "first\r\n" {
		return "", fmt.Errorf("restore source differs: %q", data)
	}
	return "restore --source=HEAD~1", nil
}

func broadRestoreStaged(h *acceptHarness) (string, error) {
	repo, err := broadRestoreFixture(h, "broad-restore-staged")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("staged\r\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "a.txt"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "restore", "-S", "a.txt"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	return "restore -S", nil
}

func broadResetMixed(h *acceptHarness) (string, error) {
	repo, err := broadRestoreFixture(h, "broad-reset-mixed")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "reset", "--mixed", "HEAD~1"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	return "reset --mixed", nil
}

func broadFailClosed(h *acceptHarness) (string, error) {
	source, _, err := acceptSuiteSeed(h, "broad-fail-src")
	if err != nil {
		return "", err
	}
	target := h.repo("broad-fail-dst")
	if _, err = h.clientOK(h.root, "clone", source, target); err != nil {
		return "", err
	}
	if err = h.clientExpect(h.root, false, "clone", source, target); err != nil {
		return "", err
	}
	if err = h.clientExpect(target, false, "not-a-command"); err != nil {
		return "", err
	}
	return "clone exists + unknown cmd", nil
}

func broadIndexLock(h *acceptHarness) (string, error) {
	repo, _, err := acceptSuiteSeed(h, "broad-lock")
	if err != nil {
		return "", err
	}
	lock := filepath.Join(repo, ".git", "index.lock")
	if err = cliAcceptWrite(lock, []byte("other writer")); err != nil {
		return "", err
	}
	if err = h.clientExpect(repo, false, "add", "-A"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(lock)
	if err != nil {
		return "", err
	}
	if string(data) != "other writer" {
		return "", fmt.Errorf("index.lock modified: %q", data)
	}
	return "index.lock preserved", nil
}

// ---- local-conformance ----------------------------------------------------

func acceptSnapshot(root string, includeGit bool) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !includeGit {
			for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
				if part == ".git" {
					return nil
				}
			}
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = data
		}
		return nil
	})
	return files, err
}

func acceptCopyRepo(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

func acceptSameFiles(left, right map[string][]byte) bool { return reflect.DeepEqual(left, right) }

func acceptConformanceInit(h *acceptHarness, name, format string) (string, error) {
	repo := h.repo(name + "-" + format)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(repo, "init", "-b", "main", "--object-format="+format, "."); err != nil {
		return "", err
	}
	return repo, nil
}

func acceptConformanceCommit(h *acceptHarness, repo, message string) error {
	if _, err := h.referenceGit(repo, "add", "-A"); err != nil {
		return err
	}
	_, err := h.referenceGit(repo, "commit", "-m", message)
	return err
}

func acceptRestoreFixture(h *acceptHarness, format, name string) (string, error) {
	repo, err := acceptConformanceInit(h, name, format)
	if err != nil {
		return "", err
	}
	files := []struct {
		path string
		data []byte
	}{
		{".gitattributes", []byte("*.txt text eol=crlf\n")},
		{"a.txt", []byte("first\r\n")},
		{"old.txt", []byte("old\r\n")},
		{"dir/nested.txt", []byte("nested\r\n")},
	}
	for _, item := range files {
		if err = cliAcceptWrite(filepath.Join(repo, item.path), item.data); err != nil {
			return "", err
		}
	}
	if err = acceptConformanceCommit(h, repo, "first"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("second\r\n")); err != nil {
		return "", err
	}
	if err = os.Remove(filepath.Join(repo, "old.txt")); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "new.txt"), []byte("new\r\n")); err != nil {
		return "", err
	}
	if err = acceptConformanceCommit(h, repo, "second"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("staged\r\n")); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "staged.txt"), []byte("staged new\r\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "."); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "a.txt"), []byte("unstaged\r\n")); err != nil {
		return "", err
	}
	if err = os.Remove(filepath.Join(repo, "dir", "nested.txt")); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "untracked.txt"), []byte("preserve\n")); err != nil {
		return "", err
	}
	return repo, nil
}

func acceptConformanceRestore(h *acceptHarness, format, id string, args []string) (string, error) {
	actual, err := acceptRestoreFixture(h, format, "restore-"+id)
	if err != nil {
		return "", err
	}
	expected := actual + ".reference"
	if err = acceptCopyRepo(actual, expected); err != nil {
		return "", err
	}
	indexBefore, err := os.ReadFile(filepath.Join(actual, ".git", "index"))
	if err != nil {
		return "", err
	}
	actualCwd, expectedCwd := actual, expected
	if id == "nested-cwd" {
		actualCwd = filepath.Join(actual, "dir")
		expectedCwd = filepath.Join(expected, "dir")
	}
	reference, err := h.referenceGit(expectedCwd, args...)
	if err != nil {
		return "", err
	}
	client, err := h.clientOK(actualCwd, args...)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(client, reference) {
		return "", fmt.Errorf("stdout differs for %v: client=%q reference=%q", args, client, reference)
	}
	actualTree, err := acceptSnapshot(actual, false)
	if err != nil {
		return "", err
	}
	expectedTree, err := acceptSnapshot(expected, false)
	if err != nil {
		return "", err
	}
	if !acceptSameFiles(actualTree, expectedTree) {
		return "", fmt.Errorf("worktree differs for %v", args)
	}
	for _, arg := range args {
		if arg == "--soft" {
			indexAfter, err := os.ReadFile(filepath.Join(actual, ".git", "index"))
			if err != nil {
				return "", err
			}
			if !bytes.Equal(indexBefore, indexAfter) {
				return "", fmt.Errorf("soft reset changed index bytes")
			}
		}
	}
	inspections := [][]string{
		{"ls-files", "--stage", "-z"},
		{"rev-parse", "HEAD"},
		{"status", "--porcelain"},
		{"reflog", "--format=%H%x00%gs", "HEAD"},
		{"reflog", "--format=%H%x00%gs", "main"},
	}
	for _, inspect := range inspections {
		left, err := h.referenceGit(actual, inspect...)
		if err != nil {
			return "", err
		}
		right, err := h.referenceGit(expected, inspect...)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(left, right) {
			return "", fmt.Errorf("repository state differs for %v", inspect)
		}
	}
	for _, name := range []string{"HEAD", "ORIG_HEAD"} {
		left, leftErr := os.ReadFile(filepath.Join(actual, ".git", name))
		right, rightErr := os.ReadFile(filepath.Join(expected, ".git", name))
		if (leftErr == nil) != (rightErr == nil) || !bytes.Equal(left, right) {
			return "", fmt.Errorf("%s differs", name)
		}
	}
	if _, err = h.referenceGit(actual, "fsck", "--full"); err != nil {
		return "", err
	}
	return "stdout, tree, index, HEAD, ORIG_HEAD and reflogs", nil
}

func acceptRestoreRefusal(h *acceptHarness, format string) (string, error) {
	repo, err := acceptRestoreFixture(h, format, "restore-refusal")
	if err != nil {
		return "", err
	}
	commands := [][]string{
		{"restore", "a.txt", "unknown-path"},
		{"restore", "--source=missing", "a.txt"},
		{"reset", "--soft", "--", "a.txt"},
		{"reset", "--hard", "--", "a.txt"},
		{"reset", "--merge"},
		{"restore", "--ours", "a.txt"},
	}
	for _, args := range commands {
		before, err := acceptSnapshot(repo, true)
		if err != nil {
			return "", err
		}
		if err = h.clientExpect(repo, false, args...); err != nil {
			return "", err
		}
		after, err := acceptSnapshot(repo, true)
		if err != nil {
			return "", err
		}
		if !acceptSameFiles(before, after) {
			return "", fmt.Errorf("refused %v wrote repository bytes", args)
		}
	}
	locks := []struct {
		path string
		args []string
	}{
		{"index.lock", []string{"restore", "a.txt"}},
		{"HEAD.lock", []string{"reset", "--soft", "HEAD~1"}},
		{"refs/heads/main.lock", []string{"reset", "--mixed", "HEAD~1"}},
		{"ORIG_HEAD.lock", []string{"reset", "--hard", "HEAD~1"}},
	}
	for _, item := range locks {
		path := filepath.Join(repo, ".git", filepath.FromSlash(item.path))
		if err = cliAcceptWrite(path, []byte("another writer\n")); err != nil {
			return "", err
		}
		before, err := acceptSnapshot(repo, true)
		if err != nil {
			return "", err
		}
		if err = h.clientExpect(repo, false, item.args...); err != nil {
			return "", err
		}
		after, err := acceptSnapshot(repo, true)
		if err != nil {
			return "", err
		}
		if !acceptSameFiles(before, after) {
			return "", fmt.Errorf("%s refusal modified files", item.path)
		}
		if err = os.Remove(path); err != nil {
			return "", err
		}
	}
	return "6 invalid commands and 4 writer locks preserve every byte", nil
}

func acceptStatusCompare(h *acceptHarness, repo string, args []string) error {
	dir := repo
	var gitDir string
	for {
		candidate := filepath.Join(dir, ".git")
		if isDir(candidate) {
			gitDir = candidate
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("status repository not found from %s", repo)
		}
		dir = parent
	}
	before, err := acceptSnapshot(gitDir, true)
	if err != nil {
		return err
	}
	if err = h.compareStdout(repo, args...); err != nil {
		return err
	}
	after, err := acceptSnapshot(gitDir, true)
	if err != nil {
		return err
	}
	if !acceptSameFiles(before, after) {
		return fmt.Errorf("status wrote metadata: %v", args)
	}
	return nil
}

func acceptConformanceStatus(h *acceptHarness, format string) (string, error) {
	repo, err := acceptConformanceInit(h, "status-v2", format)
	if err != nil {
		return "", err
	}
	branchFlags := [][]string{
		{"status", "--porcelain=v2", "--branch"},
		{"status", "--porcelain=v2", "-bz"},
		{"status", "--porcelain", "-b"},
		{"status", "-sb", "-z"},
		{"status", "--porcelain=v2", "-b", "--no-ahead-behind"},
	}
	for _, args := range branchFlags {
		if err = acceptStatusCompare(h, repo, args); err != nil {
			return "", err
		}
	}
	for _, name := range []string{"root.txt", "deleted.txt", "space name.txt", "unicode-文.txt", "folder/child.txt", "old.txt"} {
		if err = cliAcceptWrite(filepath.Join(repo, filepath.FromSlash(name)), []byte("base\n")); err != nil {
			return "", err
		}
	}
	if err = acceptConformanceCommit(h, repo, "status base"); err != nil {
		return "", err
	}
	baseOut, err := h.referenceGit(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(string(baseOut))
	for _, tracking := range []string{"absent", "equal"} {
		if _, err = h.referenceGit(repo, "config", "branch.main.remote", "origin"); err != nil {
			return "", err
		}
		if _, err = h.referenceGit(repo, "config", "branch.main.merge", "refs/heads/main"); err != nil {
			return "", err
		}
		if _, err = h.referenceGit(repo, "remote", "add", "origin", "https://example.invalid/status"); err != nil {
			if _, err = h.referenceGit(repo, "remote", "set-url", "origin", "https://example.invalid/status"); err != nil {
				return "", err
			}
		}
		if tracking == "equal" {
			if _, err = h.referenceGit(repo, "update-ref", "refs/remotes/origin/main", base); err != nil {
				return "", err
			}
		}
		for _, args := range branchFlags {
			if err = acceptStatusCompare(h, repo, args); err != nil {
				return "", err
			}
		}
	}
	if err = cliAcceptWrite(filepath.Join(repo, "root.txt"), []byte("staged\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "root.txt"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "root.txt"), []byte("unstaged\n")); err != nil {
		return "", err
	}
	if err = os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "space name.txt"), []byte("dirty\n")); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "unicode-文.txt"), []byte("dirty\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "mv", "old.txt", "new.txt"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "intent.txt"), []byte("intent\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "-N", "intent.txt"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "new dir", "untracked.txt"), []byte("untracked\n")); err != nil {
		return "", err
	}
	statusArgs := [][]string{
		{"status", "--porcelain=v2"},
		{"status", "--porcelain=2", "-z"},
		{"status", "--porcelain=v2", "-uall"},
		{"status", "--porcelain=v2", "-uno"},
		{"status", "--porcelain=v2", "--", "folder", "space name.txt"},
		{"-c", "status.renames=false", "status", "--porcelain=v2", "-z"},
		{"-c", "core.quotePath=false", "status", "--porcelain=v2"},
	}
	for _, args := range statusArgs {
		if err = acceptStatusCompare(h, repo, args); err != nil {
			return "", err
		}
	}
	if err = acceptStatusCompare(h, filepath.Join(repo, "folder"), []string{"status", "--porcelain=v2", "-z"}); err != nil {
		return "", err
	}
	return "23 unborn/tracking/mixed/rename/intent/Unicode/NUL/nested checks, metadata unchanged", nil
}

func acceptConformanceMerge(h *acceptHarness, format, mode string) (string, error) {
	repo, err := acceptConformanceInit(h, "merge-"+mode, format)
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "shared.txt"), []byte("base\n")); err != nil {
		return "", err
	}
	if err = acceptConformanceCommit(h, repo, "base"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "switch", "-c", "topic"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "their.txt"), []byte("theirs\n")); err != nil {
		return "", err
	}
	if mode == "conflict-abort" {
		if err = cliAcceptWrite(filepath.Join(repo, "shared.txt"), []byte("theirs\n")); err != nil {
			return "", err
		}
	}
	if err = acceptConformanceCommit(h, repo, "theirs"); err != nil {
		return "", err
	}
	theirs, err := h.clientOK(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "switch", "main"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "our.txt"), []byte("ours\n")); err != nil {
		return "", err
	}
	if mode == "conflict-abort" {
		if err = cliAcceptWrite(filepath.Join(repo, "shared.txt"), []byte("ours\n")); err != nil {
			return "", err
		}
	}
	if err = acceptConformanceCommit(h, repo, "ours"); err != nil {
		return "", err
	}
	ours, err := h.clientOK(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	before, err := acceptSnapshot(repo, false)
	if err != nil {
		return "", err
	}
	if mode == "conflict-abort" {
		if err = h.clientExpect(repo, false, "merge", "topic", "-m", "merge"); err != nil {
			return "", err
		}
		if !isFile(filepath.Join(repo, ".git", "MERGE_HEAD")) {
			return "", fmt.Errorf("conflict did not publish merge state")
		}
		stages, err := h.referenceGit(repo, "ls-files", "--unmerged")
		if err != nil {
			return "", err
		}
		if len(stages) == 0 {
			return "", fmt.Errorf("missing conflict index stages")
		}
		if _, err = h.clientOK(repo, "merge", "--abort"); err != nil {
			return "", err
		}
		after, err := acceptSnapshot(repo, false)
		if err != nil {
			return "", err
		}
		if !acceptSameFiles(before, after) {
			return "", fmt.Errorf("abort did not restore original tree")
		}
		head, err := h.clientOK(repo, "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		if !bytes.Equal(head, ours) {
			return "", fmt.Errorf("HEAD differs after abort")
		}
	} else {
		args := []string{"merge", "topic", "-m", "merge"}
		if mode == "pending" {
			args = []string{"merge", "--no-commit", "topic", "-m", "merge"}
		}
		if _, err = h.clientOK(repo, args...); err != nil {
			return "", err
		}
		if mode == "pending" {
			head, err := h.clientOK(repo, "rev-parse", "HEAD")
			if err != nil {
				return "", err
			}
			if !bytes.Equal(head, ours) {
				return "", fmt.Errorf("pending merge moved HEAD")
			}
			data, err := os.ReadFile(filepath.Join(repo, ".git", "MERGE_HEAD"))
			if err != nil {
				return "", err
			}
			if !bytes.Equal(data, theirs) {
				return "", fmt.Errorf("MERGE_HEAD differs")
			}
			if _, err = h.clientOK(repo, "commit", "--no-edit"); err != nil {
				return "", err
			}
		}
		data, err := os.ReadFile(filepath.Join(repo, "our.txt"))
		if err != nil {
			return "", err
		}
		if string(data) != "ours\n" {
			return "", fmt.Errorf("our.txt differs: %q", data)
		}
		data, err = os.ReadFile(filepath.Join(repo, "their.txt"))
		if err != nil {
			return "", err
		}
		if string(data) != "theirs\n" {
			return "", fmt.Errorf("their.txt differs: %q", data)
		}
		commit, err := h.clientOK(repo, "cat-file", "-p", "HEAD")
		if err != nil {
			return "", err
		}
		parents := []string{}
		for _, line := range strings.Split(string(commit), "\n") {
			if parent, found := strings.CutPrefix(line, "parent "); found {
				parents = append(parents, parent)
			}
		}
		want := []string{strings.TrimSpace(string(ours)), strings.TrimSpace(string(theirs))}
		if !reflect.DeepEqual(parents, want) {
			return "", fmt.Errorf("merge parents differ: %v vs %v", parents, want)
		}
	}
	if exists(filepath.Join(repo, ".git", "MERGE_HEAD")) {
		return "", fmt.Errorf("stale merge state")
	}
	status, err := h.clientOK(repo, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if len(status) != 0 {
		return "", fmt.Errorf("worktree dirty after merge: %q", status)
	}
	if _, err = h.referenceGit(repo, "fsck", "--full"); err != nil {
		return "", err
	}
	return "tree, ordered parents, index, merge state and fsck", nil
}

func acceptConformanceCommitCleanup(h *acceptHarness, format string) (string, error) {
	for _, cleanup := range []string{"strip", "whitespace", "verbatim", "scissors"} {
		actual, err := acceptConformanceInit(h, "commit-"+cleanup, format)
		if err != nil {
			return "", err
		}
		if err = cliAcceptWrite(filepath.Join(actual, "file.txt"), []byte("base\n")); err != nil {
			return "", err
		}
		if err = acceptConformanceCommit(h, actual, "base"); err != nil {
			return "", err
		}
		if err = cliAcceptWrite(filepath.Join(actual, "file.txt"), []byte("changed\n")); err != nil {
			return "", err
		}
		if _, err = h.referenceGit(actual, "add", "file.txt"); err != nil {
			return "", err
		}
		expected := actual + ".reference"
		if err = acceptCopyRepo(actual, expected); err != nil {
			return "", err
		}
		msg := "\n# comment\n\nsubject with space  \n\n\nbody\t\n# ------------------------ >8 ------------------------\ncut\n"
		args := []string{"commit", "--cleanup=" + cleanup, "-m", msg}
		if _, err = h.referenceGit(expected, args...); err != nil {
			return "", err
		}
		if _, err = h.clientOK(actual, args...); err != nil {
			return "", err
		}
		clientCommit, err := h.clientOK(actual, "cat-file", "-p", "HEAD")
		if err != nil {
			return "", err
		}
		referenceCommit, err := h.referenceGit(expected, "cat-file", "commit", "HEAD")
		if err != nil {
			return "", err
		}
		if !bytes.Equal(clientCommit, referenceCommit) {
			return "", fmt.Errorf("commit object differs for %s", cleanup)
		}
		if _, err = h.referenceGit(actual, "fsck", "--full"); err != nil {
			return "", err
		}
	}
	return "4 cleanup modes compare raw commit objects", nil
}

func acceptConformanceCases(h *acceptHarness) []acceptCase {
	out := make([]acceptCase, 0)
	commands := []struct {
		id   string
		args []string
	}{
		{"worktree-path", []string{"restore", "a.txt"}},
		{"worktree-directory", []string{"restore", "dir"}},
		{"worktree-all", []string{"restore", "."}},
		{"staged-path", []string{"restore", "-S", "a.txt"}},
		{"staged-worktree", []string{"restore", "-SW", "a.txt", "dir"}},
		{"source-worktree", []string{"restore", "--source=HEAD~1", "a.txt", "old.txt", "new.txt"}},
		{"source-staged", []string{"restore", "--source=HEAD~1", "-S", "."}},
		{"source-both", []string{"restore", "--source=HEAD~1", "-SW", "."}},
		{"nested-cwd", []string{"restore", "nested.txt"}},
		{"reset-default", []string{"reset"}},
		{"reset-mixed", []string{"reset", "--mixed", "HEAD~1"}},
		{"reset-soft", []string{"reset", "--soft", "HEAD~1"}},
		{"reset-hard", []string{"reset", "--hard", "HEAD~1"}},
		{"reset-path", []string{"reset", "HEAD", "--", "a.txt"}},
		{"reset-directory", []string{"reset", "HEAD~1", "--", "dir"}},
	}
	for _, format := range []string{"sha1", "sha256"} {
		restoreSuite := "restore-" + format
		statusSuite := "status-" + format
		mergeSuite := "merge-" + format
		commitSuite := "commit-" + format
		for _, cmd := range commands {
			id, args := cmd.id, cmd.args
			out = append(out, acceptRunCase(restoreSuite, id, func(h *acceptHarness, _ bool) (string, error) {
				return acceptConformanceRestore(h, format, id, args)
			}, h, false))
		}
		out = append(out, acceptRunCase(restoreSuite, "refusal-no-write", func(h *acceptHarness, _ bool) (string, error) {
			return acceptRestoreRefusal(h, format)
		}, h, false))
		out = append(out, acceptRunCase(statusSuite, "porcelain-v2-records", func(h *acceptHarness, _ bool) (string, error) {
			return acceptConformanceStatus(h, format)
		}, h, false))
		for _, mode := range []string{"clean", "pending", "conflict-abort"} {
			mode := mode
			out = append(out, acceptRunCase(mergeSuite, mode, func(h *acceptHarness, _ bool) (string, error) {
				return acceptConformanceMerge(h, format, mode)
			}, h, false))
		}
		out = append(out, acceptRunCase(commitSuite, "cleanup-objects", func(h *acceptHarness, _ bool) (string, error) {
			return acceptConformanceCommitCleanup(h, format)
		}, h, false))
	}
	return out
}

// ---- network --------------------------------------------------------------

type acceptDaemon struct {
	cmd     *exec.Cmd
	cleanup func()
	cancel  context.CancelFunc
	done    chan error
	port    uint16
}

func acceptStartDaemon(h *acceptHarness, base string) (*acceptDaemon, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind daemon port: %w", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, h.reference, "daemon", "--reuseaddr", "--base-path="+base, "--export-all", "--listen=127.0.0.1", fmt.Sprintf("--port=%d", port))
	cmd.Dir = base
	cmd.Env = h.env
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cleanup, err := prepareProcess(cmd)
	if err != nil {
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		cleanup()
		return nil, fmt.Errorf("spawn git daemon: %w", err)
	}
	if err = attachProcess(cmd); err != nil {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cleanup()
		return nil, err
	}
	d := &acceptDaemon{cmd: cmd, cleanup: cleanup, cancel: cancel, done: make(chan error, 1), port: port}
	go func() { d.done <- cmd.Wait() }()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for attempt := 0; attempt < 30; attempt++ {
		conn, dialErr := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return d, nil
		}
		select {
		case waitErr := <-d.done:
			d.cleanup()
			return nil, fmt.Errorf("git daemon exited before listening on %s: %v", address, waitErr)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.stop()
	return nil, fmt.Errorf("git daemon did not accept connections on %s", address)
}

func (d *acceptDaemon) stop() {
	d.cleanup()
	<-d.done
	d.cancel()
}

func (d *acceptDaemon) url(name string) string {
	return fmt.Sprintf("git://127.0.0.1:%d/%s", d.port, name)
}

func acceptNetworkCases(h *acceptHarness) []acceptCase {
	if os.Getenv("GIT_NEXT_ACCEPT_SKIP_NETWORK") == "1" {
		return []acceptCase{acceptSkip("network", "network-tier", "GIT_NEXT_ACCEPT_SKIP_NETWORK=1")}
	}
	out := []acceptCase{
		acceptRunCase("network", "git-daemon-clone", func(h *acceptHarness, _ bool) (string, error) { return acceptDaemonClone(h) }, h, false),
		acceptRunCase("network", "git-daemon-fetch-pull", func(h *acceptHarness, _ bool) (string, error) { return acceptDaemonFetchPull(h) }, h, false),
	}
	if os.Getenv("GIT_NEXT_ACCEPT_PUBLIC_HTTPS") == "1" {
		out = append(out, acceptRunCase("network", "public-https-ls-remote", func(h *acceptHarness, _ bool) (string, error) {
			if err := h.compareStdout(h.root, "ls-remote", "https://github.com/octocat/Hello-World.git"); err != nil {
				return "", err
			}
			return "public HTTPS ls-remote", nil
		}, h, false))
	} else {
		out = append(out, acceptSkip("network", "public-https-ls-remote", "set GIT_NEXT_ACCEPT_PUBLIC_HTTPS=1 to enable"))
	}
	return out
}

func acceptDaemonClone(h *acceptHarness) (string, error) {
	daemonRoot := h.repo("daemon-root")
	if err := os.MkdirAll(daemonRoot, 0o755); err != nil {
		return "", err
	}
	source := h.repo("daemon-clone-src")
	if err := h.initFixtureRepo(source, "main"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(source, "README.md"), []byte("daemon\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "add", "README.md"); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "commit", "-m", "daemon"); err != nil {
		return "", err
	}
	const bareName = "daemon-clone.git"
	bare := filepath.Join(daemonRoot, bareName)
	if _, err := h.referenceGit(h.root, "init", "--bare", bare); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "push", bare, "main"); err != nil {
		return "", err
	}
	daemon, err := acceptStartDaemon(h, daemonRoot)
	if err != nil {
		return "", err
	}
	defer daemon.stop()
	head, err := h.referenceGit(bare, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	target := h.repo("daemon-clone-dst")
	if _, err = h.clientOK(h.root, "clone", daemon.url(bareName), target); err != nil {
		return "", err
	}
	got, err := h.clientOK(target, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, head) {
		return "", fmt.Errorf("git:// clone head differs: %q vs %q", got, head)
	}
	status, err := h.clientOK(target, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if len(status) != 0 {
		return "", fmt.Errorf("git:// clone worktree dirty: %q", status)
	}
	return "git:// clone", nil
}

func acceptDaemonFetchPull(h *acceptHarness) (string, error) {
	daemonRoot := h.repo("daemon-fetch-root")
	if err := os.MkdirAll(daemonRoot, 0o755); err != nil {
		return "", err
	}
	source := h.repo("daemon-fetch-src")
	if err := h.initFixtureRepo(source, "main"); err != nil {
		return "", err
	}
	if err := cliAcceptWrite(filepath.Join(source, "README.md"), []byte("v1\n")); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "add", "."); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "commit", "-m", "v1"); err != nil {
		return "", err
	}
	const bareName = "daemon-fetch.git"
	bare := filepath.Join(daemonRoot, bareName)
	if _, err := h.referenceGit(h.root, "init", "--bare", bare); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(source, "push", bare, "main"); err != nil {
		return "", err
	}
	daemon, err := acceptStartDaemon(h, daemonRoot)
	if err != nil {
		return "", err
	}
	defer daemon.stop()
	clone := h.repo("daemon-fetch-clone")
	if _, err = h.clientOK(h.root, "clone", daemon.url(bareName), clone); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(source, "README.md"), []byte("v2\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(source, "add", "."); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(source, "commit", "-m", "v2"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(source, "push", bare, "main"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(clone, "fetch", "origin"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(clone, "pull", "--ff-only"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(clone, "README.md"))
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(data), "v2") {
		return "", fmt.Errorf("git:// pull did not update file: %q", data)
	}
	return "git:// fetch + pull", nil
}
