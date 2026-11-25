// Package gitx implements git commands on top of go-git. It never invokes the
// system git or ssh executables.
package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/neko233-com/git-next/internal/cli"
)

// Version is the product version reported by `version` and `doctor`.
const Version = "0.1.0-go"

// exitError requests a specific process exit code without a fatal message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func exitWithCode(code int) error { return exitError{code: code} }

// ExitCode reports whether err requests a specific exit status.
func ExitCode(err error) (int, bool) {
	if code, ok := err.(exitError); ok {
		return code.code, true
	}
	return 1, false
}

// Context carries global command line options into command implementations.
type Context struct {
	Inv       *cli.Invocation
	Dir       string
	GitDir    string
	WorkTree  string
	Bare      bool
	Overrides []string
}

func NewContext(inv *cli.Invocation) (*Context, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &Context{
		Inv: inv, Dir: dir, GitDir: inv.GitDir, WorkTree: inv.WorkTree,
		Bare:      inv.Bare || inv.Command == "clone" && hasBool(inv.Args, "--bare"),
		Overrides: inv.Config,
	}, nil
}

func hasBool(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

// Open resolves the repository from the current directory or --git-dir.
func (c *Context) Open() (*git.Repository, error) {
	path := c.Dir
	if c.GitDir != "" {
		path = c.GitDir
	}
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: c.GitDir == ""})
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	if cfg, err := repo.Config(); err == nil && cfg.Extensions.ObjectFormat == "sha256" {
		return nil, fmt.Errorf("SHA-256 repositories are not supported by the Go client (go-git limitation)")
	}
	gitDir, err := c.gitDirectory(repo)
	if err != nil {
		return nil, err
	}
	return openWithTolerantIndex(repo, gitDir)
}

func (c *Context) Worktree(repo *git.Repository) (*git.Worktree, error) {
	if c.Bare {
		return nil, fmt.Errorf("this operation requires a working tree")
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("repository has no working tree: %w", err)
	}
	return wt, nil
}

func atomicWrite(path string, data []byte) error {
	lock := path + ".lock"
	file, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("another writer holds the lock on %s: %w", path, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(lock)
		}
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(lock, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func parseDate(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now()
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05-0700", time.RFC3339,
		"2006-01-02 15:04:05 -0700", "2006-01-02T15:04:05Z07:00",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Now()
}

func signature(repo *git.Repository, prefix string) *object.Signature {
	name := os.Getenv(prefix + "_NAME")
	email := os.Getenv(prefix + "_EMAIL")
	if name == "" || email == "" {
		if cfg, err := repo.Config(); err == nil {
			if name == "" {
				name = cfg.User.Name
			}
			if email == "" {
				email = cfg.User.Email
			}
		}
	}
	return &object.Signature{
		Name: name, Email: email, When: parseDate(os.Getenv(prefix + "_DATE")),
	}
}

func commitSignature(repo *git.Repository) (*object.Signature, *object.Signature) {
	return signature(repo, "GIT_AUTHOR"), signature(repo, "GIT_COMMITTER")
}

func gitDirOf(repo *git.Repository) string {
	return filepath.ToSlash(repo.Storer.(interface{ String() string }).String())
}

func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func resolve(r *git.Repository, revision string) (string, error) {
	if revision == "" {
		revision = "HEAD"
	}
	hash, err := r.ResolveRevision(plumbing.Revision(revision))
	if err != nil {
		return "", fmt.Errorf("bad revision %q: %w", revision, err)
	}
	return hash.String(), nil
}

func pathFromRevision(r *git.Repository, spec string) (string, string, error) {
	rev, path, ok := strings.Cut(spec, ":")
	if !ok {
		return "", "", fmt.Errorf("unsupported object spec %q; use <revision>:<path>", spec)
	}
	hash, err := resolve(r, rev)
	if err != nil {
		return "", "", err
	}
	return hash, path, nil
}

func formatGitDate(t time.Time) string {
	return t.Format("Mon Jan 2 15:04:05 2006 -0700")
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseInt(value string, fallback int) int {
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return fallback
}

// Dispatch routes a parsed command to its implementation.
func (c *Context) Dispatch(p *cli.Parsed) error {
	switch c.Inv.Command {
	case "init":
		return c.cmdInit(p)
	case "clone":
		return c.cmdClone(p)
	case "add":
		return c.cmdAdd(p)
	case "commit":
		return c.cmdCommit(p)
	case "status":
		return c.cmdStatus(p)
	case "diff":
		return c.cmdDiff(p, false)
	case "show":
		return c.cmdDiff(p, true)
	case "log":
		return c.cmdLog(p)
	case "rev-parse":
		return c.cmdRevParse(p)
	case "cat-file":
		return c.cmdCatFile(p)
	case "hash-object":
		return c.cmdHashObject(p)
	case "ls-files":
		return c.cmdLsFiles(p)
	case "show-ref":
		return c.cmdShowRef(p)
	case "branch":
		return c.cmdBranch(p)
	case "tag":
		return c.cmdTag(p)
	case "remote":
		return c.cmdRemote(p)
	case "config":
		return c.cmdConfig(p)
	case "switch", "checkout":
		return c.cmdSwitch(p)
	case "restore":
		return c.cmdRestore(p)
	case "reset":
		return c.cmdReset(p)
	case "merge":
		return c.cmdMerge(p)
	case "fetch":
		return c.cmdFetch(p)
	case "pull":
		return c.cmdPull(p)
	case "push":
		return c.cmdPush(p)
	case "ls-remote":
		return c.cmdLsRemote(p)
	case "stash":
		return c.cmdStash(p)
	case "cherry-pick":
		return c.cmdCherryPick(p)
	case "revert":
		return c.cmdRevert(p)
	case "rebase":
		return c.cmdRebase(p)
	case "submodule":
		return c.cmdSubmodule(p)
	case "worktree":
		return c.cmdWorktree(p)
	case "lfs":
		return c.cmdLfs(p)
	case "eol":
		return c.cmdEol(p)
	case "language":
		return c.cmdLanguage(p)
	case "compatibility":
		return c.cmdCompatibility(p)
	case "version":
		return c.cmdVersion(p)
	case "doctor":
		return c.cmdDoctor(p)
	case "install":
		return c.cmdInstall(p)
	default:
		return fmt.Errorf("command %q is not implemented", c.Inv.Command)
	}
}
