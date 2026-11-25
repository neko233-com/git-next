// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The acceptance harness owns an isolated temporary workspace. A reference Git
// executable is used only to build fixtures and to compute expected output; the
// client under test never receives a Git or SSH executable on its PATH.

const acceptTimeout = 120 * time.Second

type acceptResult struct {
	stdout, stderr []byte
	code           int
}

type acceptHarness struct {
	root      string
	empty     string
	client    string
	reference string
	env       []string
}

type acceptCase struct {
	Suite   string `json:"suite"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Detail  string `json:"detail"`
}

type cliAcceptReport struct {
	Client  string       `json:"client"`
	Tier    string       `json:"tier"`
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Skipped int          `json:"skipped"`
	Cases   int          `json:"cases"`
	Results []acceptCase `json:"results"`
}

func init() {
	register("cli-accept", "Accept a built Go client (l0/full/network) with an isolated harness", "tier json full? network?", cliAccept)
}

func acceptEnvSet(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		k, _, _ := strings.Cut(item, "=")
		if k == key {
			continue
		}
		out = append(out, item)
	}
	return append(out, key+"="+value)
}

func newAcceptHarness(clientPath string) (*acceptHarness, error) {
	client := clientPath
	if runtime.GOOS == "windows" && !isFile(client) && filepath.Ext(client) == "" {
		client += ".exe"
	}
	canonical, err := filepath.EvalSymlinks(client)
	if err != nil {
		return nil, fmt.Errorf("client binary: %w", err)
	}
	client = canonical
	reference, err := acceptReferenceGit()
	if err != nil {
		return nil, err
	}
	refCanonical, err := filepath.EvalSymlinks(reference)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(client, refCanonical) {
		return nil, fmt.Errorf("client must differ from reference Git")
	}
	root, err := os.MkdirTemp("", "git-next-accept-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(root)
		}
	}()
	home := filepath.Join(root, "home")
	empty := filepath.Join(root, "empty-path")
	if err = os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(empty, 0o755); err != nil {
		return nil, err
	}
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") {
			continue
		}
		env = append(env, item)
	}
	env = acceptEnvSet(env, "HOME", home)
	env = acceptEnvSet(env, "USERPROFILE", home)
	env = acceptEnvSet(env, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	env = acceptEnvSet(env, "SSH_ROOT_PATH", filepath.Join(home, ".ssh"))
	env = acceptEnvSet(env, "GIT_OPTIONAL_LOCKS", "0")
	env = acceptEnvSet(env, "GIT_TERMINAL_PROMPT", "0")
	env = acceptEnvSet(env, "GIT_AUTHOR_DATE", "2026-01-01T12:00:00+0000")
	env = acceptEnvSet(env, "GIT_COMMITTER_DATE", "2026-01-01T12:00:00+0000")
	config := "[init]\n\tdefaultBranch = main\n[core]\n\tautocrlf = false\n\tfilemode = false\n\tsymlinks = false\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n"
	if err = os.WriteFile(filepath.Join(home, "config"), []byte(config), 0o644); err != nil {
		return nil, err
	}
	env = acceptEnvSet(env, "GIT_CONFIG_NOSYSTEM", "1")
	env = acceptEnvSet(env, "GIT_TEMPLATE_DIR", empty)
	env = acceptEnvSet(env, "GIT_CONFIG_GLOBAL", filepath.Join(home, "config"))
	env = acceptEnvSet(env, "GIT_AUTHOR_NAME", "Accept")
	env = acceptEnvSet(env, "GIT_COMMITTER_NAME", "Accept")
	env = acceptEnvSet(env, "GIT_AUTHOR_EMAIL", "acceptance@example.invalid")
	env = acceptEnvSet(env, "GIT_COMMITTER_EMAIL", "acceptance@example.invalid")
	env = acceptEnvSet(env, "LC_ALL", "C")
	env = acceptEnvSet(env, "LANG", "C")
	ok = true
	return &acceptHarness{root: root, empty: empty, client: client, reference: refCanonical, env: env}, nil
}

func acceptReferenceGit() (string, error) {
	path := os.Getenv("PATH")
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	for _, dir := range filepath.SplitList(path) {
		candidate := filepath.Join(dir, name)
		if isFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("reference git not found on PATH (fixtures only)")
}

func (h *acceptHarness) repo(name string) string { return filepath.Join(h.root, name) }

func (h *acceptHarness) exec(exe, cwd string, isolate bool, args ...string) (acceptResult, error) {
	argv := []string{}
	if isolate {
		argv = append(argv, "--language", "english")
	}
	argv = append(argv, args...)
	env := h.env
	if isolate {
		missing := filepath.Join(h.empty, "missing-ssh")
		env = acceptEnvSet(env, "PATH", h.empty)
		env = acceptEnvSet(env, "GIT_EXEC_PATH", h.empty)
		env = acceptEnvSet(env, "GIT_SSH", missing)
		env = acceptEnvSet(env, "GIT_SSH_COMMAND", missing)
	}
	ctx, cancel := context.WithTimeout(context.Background(), acceptTimeout)
	defer cancel()
	ran, err := runProcess(ctx, cwd, env, nil, exe, argv...)
	r := acceptResult{ran.stdout, ran.stderr, ran.code}
	if err != nil && strings.Contains(err.Error(), "exceeded its execution context") {
		return r, fmt.Errorf("process timed out after %s: %s %v", acceptTimeout, filepathBase(exe), argv)
	}
	return r, err
}

func (h *acceptHarness) referenceGit(cwd string, args ...string) ([]byte, error) {
	r, err := h.exec(h.reference, cwd, false, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fmt.Errorf("reference fixture %v failed: %s", args, strings.TrimSpace(string(r.stderr)))
	}
	return r.stdout, nil
}

func (h *acceptHarness) clientGit(cwd string, args ...string) (acceptResult, error) {
	return h.exec(h.client, cwd, true, args...)
}

func (h *acceptHarness) clientOK(cwd string, args ...string) ([]byte, error) {
	r, err := h.clientGit(cwd, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fmt.Errorf("client %v failed: %s", args, strings.TrimSpace(string(r.stderr)))
	}
	return r.stdout, nil
}

func (h *acceptHarness) clientExpect(cwd string, success bool, args ...string) error {
	r, err := h.clientGit(cwd, args...)
	if err != nil {
		return err
	}
	if (r.code == 0) != success {
		return fmt.Errorf("client %v expected success=%t, got exit %d: %s", args, success, r.code, strings.TrimSpace(string(r.stderr)))
	}
	return nil
}

func (h *acceptHarness) compareStdout(cwd string, args ...string) error {
	expected, err := h.referenceGit(cwd, args...)
	if err != nil {
		return err
	}
	actual, err := h.clientGit(cwd, args...)
	if err != nil {
		return err
	}
	if actual.code != 0 {
		return fmt.Errorf("compare %v: client exit %d: %s", args, actual.code, strings.TrimSpace(string(actual.stderr)))
	}
	if !bytes.Equal(expected, actual.stdout) {
		return fmt.Errorf("stdout mismatch for %v: client=%q reference=%q", args, string(actual.stdout), string(expected))
	}
	return nil
}

func (h *acceptHarness) compareCanonicalPathStdout(cwd string, args ...string) error {
	expected, err := h.referenceGit(cwd, args...)
	if err != nil {
		return err
	}
	actual, err := h.clientGit(cwd, args...)
	if err != nil {
		return err
	}
	if actual.code != 0 {
		return fmt.Errorf("compare %v: client exit %d: %s", args, actual.code, strings.TrimSpace(string(actual.stderr)))
	}
	left := acceptCanonical(strings.TrimSpace(string(expected)))
	right := acceptCanonical(strings.TrimSpace(string(actual.stdout)))
	if left != right {
		return fmt.Errorf("path stdout mismatch for %v: client=%q reference=%q", args, right, left)
	}
	return nil
}

func acceptCanonical(value string) string {
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		return resolved
	}
	absolute, _ := filepath.Abs(value)
	return absolute
}

func (h *acceptHarness) initFixtureRepo(path, branch string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	_, err := h.referenceGit(path, "init", "-b", branch, ".")
	return err
}

func cliAcceptWrite(path string, data []byte) error {
	if parent := filepath.Dir(path); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

func acceptSeedRepo(h *acceptHarness, name string) (string, []byte, error) {
	repo := h.repo(name)
	if err := h.initFixtureRepo(repo, "main"); err != nil {
		return "", nil, err
	}
	if err := cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("hello\n")); err != nil {
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

type acceptCaseFunc func(*acceptHarness, bool) (string, error)

func acceptRunCase(suite, id string, fn acceptCaseFunc, h *acceptHarness, full bool) acceptCase {
	detail, err := fn(h, full)
	if err != nil {
		return acceptCase{suite, id, false, false, err.Error()}
	}
	return acceptCase{suite, id, true, false, detail}
}

func acceptSkip(suite, id, detail string) acceptCase {
	return acceptCase{suite, id, true, true, detail}
}

func acceptContains(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}

func l0Cases() []struct {
	name string
	fn   acceptCaseFunc
} {
	return []struct {
		name string
		fn   acceptCaseFunc
	}{
		{"init", caseInit}, {"clone", caseClone}, {"add", caseAdd},
		{"commit", caseCommit}, {"status", caseStatus}, {"diff", caseDiff},
		{"show", caseShow}, {"log", caseLog}, {"branch", caseBranch},
		{"switch", caseSwitch}, {"checkout", caseCheckout}, {"merge", caseMerge},
		{"fetch", caseFetch}, {"pull", casePull}, {"push", casePush},
		{"remote", caseRemote}, {"tag", caseTag}, {"stash", caseStash},
		{"cherry-pick", caseCherryPick}, {"revert", caseRevert}, {"rebase", caseRebase},
		{"submodule", caseSubmodule}, {"worktree", caseWorktree}, {"restore", caseRestore},
		{"reset", caseReset}, {"rev-parse", caseRevParse}, {"cat-file", caseCatFile},
		{"hash-object", caseHashObject}, {"ls-files", caseLsFiles}, {"show-ref", caseShowRef},
		{"ls-remote", caseLsRemote}, {"config", caseConfig}, {"lfs", caseLFS},
		{"eol", caseEOL}, {"language", caseLanguage}, {"compatibility", caseCompatibility},
		{"version", caseVersion}, {"doctor", caseDoctor}, {"install", caseInstall},
	}
}

func caseInit(h *acceptHarness, _ bool) (string, error) {
	repo := h.repo("init-case")
	if _, err := h.clientOK(h.root, "init", "-b", "main", repo); err != nil {
		return "", err
	}
	if !isDir(filepath.Join(repo, ".git")) {
		return "", fmt.Errorf("bare init layout missing .git")
	}
	return "bare init layout", nil
}

func caseClone(h *acceptHarness, _ bool) (string, error) {
	source, head, err := acceptSeedRepo(h, "clone-src")
	if err != nil {
		return "", err
	}
	dest := h.repo("clone-dst")
	if _, err = h.clientOK(h.root, "clone", source, dest); err != nil {
		return "", err
	}
	got, err := h.clientOK(dest, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, head) {
		return "", fmt.Errorf("local clone head differs: %q vs %q", got, head)
	}
	return "local clone", nil
}

func caseAdd(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "add-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "extra.txt"), []byte("x\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "extra.txt"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	return "path add", nil
}

func caseCommit(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "commit-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "c.txt"), []byte("c\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "c.txt"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "commit", "-m", "second"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "log", "--oneline", "-1"); err != nil {
		return "", err
	}
	return "commit -m", nil
}

func caseStatus(h *acceptHarness, full bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "status-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "dirty.txt"), []byte("d\n")); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	if full {
		if err = h.compareStdout(repo, "status", "-sb"); err != nil {
			return "", err
		}
	}
	return "porcelain v1", nil
}

func caseDiff(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "diff-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("changed\n")); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "diff", "--no-ext-diff"); err != nil {
		return "", err
	}
	return "worktree diff", nil
}

func caseShow(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "show-case")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "show", "--no-patch", "HEAD"); err != nil {
		return "", err
	}
	return "show header", nil
}

func caseLog(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "log-case")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "log", "--oneline", "-1"); err != nil {
		return "", err
	}
	return "oneline", nil
}

func caseBranch(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "branch-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "branch", "topic"); err != nil {
		return "", err
	}
	listed, err := h.clientOK(repo, "branch")
	if err != nil {
		return "", err
	}
	if !bytes.Contains(listed, []byte("topic")) {
		return "", fmt.Errorf("topic branch not listed")
	}
	return "create + list", nil
}

func caseSwitch(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "switch-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "branch", "topic"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "switch", "topic"); err != nil {
		return "", err
	}
	branches, err := h.clientOK(repo, "branch")
	if err != nil {
		return "", err
	}
	text := string(branches)
	if !strings.Contains(text, "* topic") && !strings.Contains(text, "*\ttopic") {
		return "", fmt.Errorf("topic is not the current branch")
	}
	return "switch branch", nil
}

func caseCheckout(h *acceptHarness, _ bool) (string, error) {
	repo, head, err := acceptSeedRepo(h, "checkout-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "checkout", "--detach", strings.TrimSpace(string(head))); err != nil {
		return "", err
	}
	return "detach", nil
}

func caseMerge(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "merge-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "branch", "side"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "switch", "side"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "m.txt"), []byte("m\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "m.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "side"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "switch", "main"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "merge", "--ff-only", "side"); err != nil {
		return "", err
	}
	return "ff merge", nil
}

func acceptBareFixture(h *acceptHarness, name string) (string, error) {
	bare := h.repo(name)
	if err := os.MkdirAll(bare, 0o755); err != nil {
		return "", err
	}
	if _, err := h.referenceGit(bare, "init", "--bare", "."); err != nil {
		return "", err
	}
	return bare, nil
}

func caseFetch(h *acceptHarness, _ bool) (string, error) {
	bare, err := acceptBareFixture(h, "fetch-bare")
	if err != nil {
		return "", err
	}
	repo, _, err := acceptSeedRepo(h, "fetch-wt")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "remote", "add", "origin", bare); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "fetch", "origin"); err != nil {
		return "", err
	}
	return "fetch origin", nil
}

func casePull(h *acceptHarness, _ bool) (string, error) {
	bare, err := acceptBareFixture(h, "pull-bare")
	if err != nil {
		return "", err
	}
	repo, _, err := acceptSeedRepo(h, "pull-wt")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "remote", "add", "origin", bare); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "push", "-u", "origin", "main"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "pull", "--ff-only", "origin"); err != nil {
		return "", err
	}
	return "ff-only pull", nil
}

func casePush(h *acceptHarness, _ bool) (string, error) {
	bare, err := acceptBareFixture(h, "push-bare")
	if err != nil {
		return "", err
	}
	repo, _, err := acceptSeedRepo(h, "push-wt")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "remote", "add", "origin", bare); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "push", "-u", "origin", "main"); err != nil {
		return "", err
	}
	return "push main", nil
}

func caseRemote(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "remote-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "remote", "add", "origin", "https://example.invalid/repo.git"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "remote", "get-url", "origin"); err != nil {
		return "", err
	}
	return "add + get-url", nil
}

func caseTag(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "tag-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "tag", "light"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "tag", "-a", "annotated", "-m", "note"); err != nil {
		return "", err
	}
	tags, err := h.clientOK(repo, "tag")
	if err != nil {
		return "", err
	}
	for _, line := range bytes.Split(tags, []byte("\n")) {
		if string(line) == "annotated" {
			return "light + annotated", nil
		}
	}
	return "", fmt.Errorf("annotated tag missing")
}

func caseStash(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "stash-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("wip\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "stash", "push", "-m", "wip"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "stash", "pop"); err != nil {
		return "", err
	}
	return "push pop", nil
}

func caseCherryPick(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "pick-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "switch", "-c", "feature"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "pick.txt"), []byte("p\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "pick.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "feature"); err != nil {
		return "", err
	}
	feature, err := h.referenceGit(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "switch", "main"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "cherry-pick", strings.TrimSpace(string(feature))); err != nil {
		return "", err
	}
	if !isFile(filepath.Join(repo, "pick.txt")) {
		return "", fmt.Errorf("picked file missing")
	}
	return "linear pick", nil
}

func caseRevert(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "revert-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "r.txt"), []byte("r\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "r.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "add r"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "revert", "HEAD"); err != nil {
		return "", err
	}
	if exists(filepath.Join(repo, "r.txt")) {
		return "", fmt.Errorf("reverted file still present")
	}
	return "revert HEAD", nil
}

func caseRebase(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "rebase-case")
	if err != nil {
		return "", err
	}
	mainTip, err := h.referenceGit(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	tip := strings.TrimSpace(string(mainTip))
	if _, err = h.clientOK(repo, "switch", "-c", "topic"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "rebase.txt"), []byte("rb\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", "rebase.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "topic commit"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "switch", "main"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "rebase", "--onto", tip, tip, "topic"); err != nil {
		return "", err
	}
	if !isFile(filepath.Join(repo, "rebase.txt")) {
		return "", fmt.Errorf("rebased file missing")
	}
	return "rebase --onto", nil
}

func caseSubmodule(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "sub-parent")
	if err != nil {
		return "", err
	}
	sub := h.repo("sub-upstream")
	if err = h.initFixtureRepo(sub, "main"); err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(sub, "sub.txt"), []byte("sub\n")); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(sub, "add", "sub.txt"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(sub, "commit", "-m", "sub"); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(sub)
	if err != nil {
		return "", err
	}
	normalized := strings.ReplaceAll(filepath.ToSlash(resolved), "\\", "/")
	url := "file://" + normalized
	if runtime.GOOS == "windows" {
		url = "file:///" + normalized
	}
	meta := fmt.Sprintf("[submodule \"child\"]\n\tpath = child\n\turl = \"%s\"\n", url)
	if err = cliAcceptWrite(filepath.Join(repo, ".gitmodules"), []byte(meta)); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "add", ".gitmodules"); err != nil {
		return "", err
	}
	if _, err = h.referenceGit(repo, "commit", "-m", "add submodule meta"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "submodule", "init"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "submodule", "update"); err != nil {
		return "", err
	}
	if !isFile(filepath.Join(repo, "child", "sub.txt")) {
		return "", fmt.Errorf("submodule checkout missing")
	}
	if _, err = h.clientOK(repo, "submodule", "status"); err != nil {
		return "", err
	}
	return "init update status", nil
}

func caseWorktree(h *acceptHarness, _ bool) (string, error) {
	repo, head, err := acceptSeedRepo(h, "wt-case")
	if err != nil {
		return "", err
	}
	linked := h.repo("wt-linked")
	if _, err = h.clientOK(repo, "worktree", "add", "--detach", linked, strings.TrimSpace(string(head))); err != nil {
		return "", err
	}
	list, err := h.clientOK(repo, "worktree", "list")
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(list), "wt-linked") {
		return "", fmt.Errorf("linked worktree not listed")
	}
	if _, err = h.clientOK(repo, "worktree", "remove", "--force", linked); err != nil {
		return "", err
	}
	return "add list remove", nil
}

func caseRestore(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "restore-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("dirty\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "restore", "README.md"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	return "restore worktree", nil
}

func caseReset(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "reset-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, "README.md"), []byte("dirty\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", "README.md"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "reset", "--hard", "HEAD"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "status", "--porcelain"); err != nil {
		return "", err
	}
	return "hard reset", nil
}

func caseRevParse(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "revparse-case")
	if err != nil {
		return "", err
	}
	if err = h.compareCanonicalPathStdout(repo, "rev-parse", "--show-toplevel"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "rev-parse", "--git-dir"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "rev-parse", "--is-bare-repository"); err != nil {
		return "", err
	}
	return "toplevel git-dir bare", nil
}

func caseCatFile(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "catfile-case")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "cat-file", "-p", "HEAD:README.md"); err != nil {
		return "", err
	}
	return "blob peel", nil
}

func caseHashObject(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "hash-case")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "hash-object", "README.md"); err != nil {
		return "", err
	}
	return "hash file", nil
}

func caseLsFiles(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "lsfiles-case")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "ls-files"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "ls-files", "--stage"); err != nil {
		return "", err
	}
	return "list + stage", nil
}

func caseShowRef(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "showref-case")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "show-ref"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "show-ref", "--head"); err != nil {
		return "", err
	}
	return "refs", nil
}

func caseLsRemote(h *acceptHarness, _ bool) (string, error) {
	bare, _, err := acceptSeedRepo(h, "lsremote-bare")
	if err != nil {
		return "", err
	}
	if err = h.compareStdout(h.root, "ls-remote", bare); err != nil {
		return "", err
	}
	return "local ls-remote", nil
}

func caseConfig(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "config-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "config", "user.name", "Acceptance"); err != nil {
		return "", err
	}
	if err = h.compareStdout(repo, "config", "user.name"); err != nil {
		return "", err
	}
	return "set + get", nil
}

func caseLFS(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "lfs-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "lfs", "version"); err != nil {
		return "", err
	}
	return "lfs version", nil
}

func caseEOL(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "eol-case")
	if err != nil {
		return "", err
	}
	if err = cliAcceptWrite(filepath.Join(repo, ".gitattributes"), []byte("*.txt text eol=lf\n")); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "add", ".gitattributes"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "commit", "-m", "attrs"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "eol", "--apply"); err != nil {
		return "", err
	}
	return "eol check", nil
}

func caseLanguage(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "lang-case")
	if err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "language", "list"); err != nil {
		return "", err
	}
	if _, err = h.clientOK(repo, "language", "get"); err != nil {
		return "", err
	}
	return "list + get", nil
}

func caseCompatibility(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "compat-case")
	if err != nil {
		return "", err
	}
	out, err := h.clientOK(repo, "compatibility", "--json")
	if err != nil {
		return "", err
	}
	if !acceptContains(out, []byte("git-next")) {
		return "", fmt.Errorf("compatibility JSON missing product identity")
	}
	return "json contract", nil
}

func caseVersion(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "version-case")
	if err != nil {
		return "", err
	}
	out, err := h.clientOK(repo, "version", "--json")
	if err != nil {
		return "", err
	}
	if !acceptContains(out, []byte(`"go"`)) {
		return "", fmt.Errorf("version JSON missing Go implementation identity")
	}
	return "json version", nil
}

func caseDoctor(h *acceptHarness, _ bool) (string, error) {
	repo, _, err := acceptSeedRepo(h, "doctor-case")
	if err != nil {
		return "", err
	}
	out, err := h.clientOK(repo, "doctor", "--json")
	if err != nil {
		return "", err
	}
	if !acceptContains(out, []byte("replacement_ready")) {
		return "", fmt.Errorf("doctor JSON missing replacement_ready")
	}
	return "doctor json", nil
}

func caseInstall(h *acceptHarness, _ bool) (string, error) {
	r, err := h.clientGit(h.root, "install", "--help")
	if err != nil {
		return "", err
	}
	if r.code != 0 {
		return "", fmt.Errorf("install --help exited %d: %s", r.code, strings.TrimSpace(string(r.stderr)))
	}
	return "install help", nil
}

func runAcceptTier(h *acceptHarness, tier string, fullMatrix bool) []acceptCase {
	results := make([]acceptCase, 0)
	for _, item := range l0Cases() {
		results = append(results, acceptRunCase("l0", item.name, item.fn, h, fullMatrix))
	}
	if tier == "full" || tier == "network" {
		results = append(results, acceptFullCases(h)...)
	}
	if tier == "network" {
		results = append(results, acceptNetworkCases(h)...)
	}
	return results
}

func cliAccept(a *app, o options) error {
	tier := o.value("tier", "l0")
	if o.yes("network") {
		tier = "network"
	} else if o.yes("full") {
		tier = "full"
	}
	if tier != "l0" && tier != "full" && tier != "network" {
		return fmt.Errorf("unknown --tier %q (use l0, full or network)", tier)
	}
	client := ""
	if len(o.args) > 0 {
		client = o.args[0]
	}
	if client == "" {
		client = a.path("target/release/git")
	}
	h, err := newAcceptHarness(client)
	if err != nil {
		return err
	}
	defer os.RemoveAll(h.root)
	results := runAcceptTier(h, tier, tier != "l0")
	passed, skipped, failed := 0, 0, 0
	for _, item := range results {
		switch {
		case item.Skipped:
			skipped++
		case item.OK:
			passed++
		default:
			failed++
		}
		mark := "ok"
		if item.Skipped {
			mark = "skip"
		} else if !item.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(a.out, "[%s] %s::%s — %s\n", mark, item.Suite, item.ID, item.Detail)
	}
	fmt.Fprintf(a.out, "\ntier %s: %d passed, %d failed, %d skipped (reference Git fixtures only)\n", tier, passed, failed, skipped)
	if path := o.get("json"); path != "" {
		report := cliAcceptReport{
			Client: h.client, Tier: tier, Passed: passed, Failed: failed,
			Skipped: skipped, Cases: len(results), Results: results,
		}
		if err = writeJSON(path, report); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d acceptance case(s) failed", failed)
	}
	return nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
