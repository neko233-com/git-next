package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/neko233-com/git-next/internal/cli"
)

const attributesContent = "# git-next: shared text normalization; binary detection and later project rules remain authoritative.\n* text=auto eol=lf\n"
const editorContent = "# git-next: change line endings without changing indentation, encoding or whitespace rules.\n[*]\nend_of_line = lf\n"

func createIfMissing(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (c *Context) configureLF(repo *git.Repository) error {
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	cfg.Raw.SetOption("core", "", "autocrlf", "input")
	cfg.Raw.SetOption("core", "", "eol", "lf")
	return repo.SetConfig(cfg)
}

func (c *Context) cmdInit(p *cli.Parsed) error {
	args := p.Positional()
	directory := "."
	if len(args) > 0 {
		directory = args[0]
	}
	bare := p.Has("bare")
	if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
		return fmt.Errorf("destination already contains a repository; refusing to change its HEAD")
	}
	if _, err := os.Stat(filepath.Join(directory, "HEAD")); err == nil && bare {
		return fmt.Errorf("destination already contains a repository; refusing to change its HEAD")
	}
	branch := p.Get("initial-branch")
	if branch == "" {
		branch = "main"
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid initial branch %q", branch)
	}
	repo, err := git.PlainInitWithOptions(directory, &git.PlainInitOptions{
		Bare: bare,
		InitOptions: git.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName(branch),
		},
	})
	if err != nil {
		return err
	}
	if err := c.configureLF(repo); err != nil {
		return err
	}
	if !bare {
		if err := createIfMissing(filepath.Join(directory, ".gitattributes"), attributesContent); err != nil {
			return err
		}
		if err := createIfMissing(filepath.Join(directory, ".editorconfig"), editorContent); err != nil {
			return err
		}
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	gitDir := abs
	if !bare {
		gitDir = filepath.Join(abs, ".git")
	}
	fmt.Printf("Initialized %sGit repository in %s\n", map[bool]string{true: "bare ", false: ""}[bare], filepath.ToSlash(gitDir))
	return nil
}

func (c *Context) cmdSwitch(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	args := p.Positional()
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	createName := p.Get("create")
	if createName == "" {
		createName = p.Get("create-force")
	}
	if createName != "" {
		name := createName
		if name == "" {
			name = target
		}
		if name == "" {
			return fmt.Errorf("switch -c requires a branch name")
		}
		opts := &git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(name), Create: true}
		if target != "" {
			hash, err := resolve(repo, target)
			if err != nil {
				return err
			}
			opts.Hash = plumbing.NewHash(hash)
		}
		return wt.Checkout(opts)
	}
	if p.Has("detach") {
		if target == "" {
			return fmt.Errorf("checkout --detach requires a revision")
		}
		hash, err := resolve(repo, target)
		if err != nil {
			return err
		}
		return wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(hash)})
	}
	if target == "" {
		return fmt.Errorf("switch requires a target")
	}
	if hash, err := resolve(repo, target); err == nil && len(target) >= 7 && strings.EqualFold(target, hash[:len(target)]) {
		return wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(hash)})
	}
	return wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(target)})
}

func blobData(repo *git.Repository, hash plumbing.Hash) ([]byte, error) {
	obj, err := repo.Storer.EncodedObject(plumbing.BlobObject, hash)
	if err != nil {
		return nil, err
	}
	reader, err := obj.Reader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data := make([]byte, obj.Size())
	if _, err := readFull(reader, data); err != nil {
		return nil, err
	}
	return data, nil
}

func treeForRevision(repo *git.Repository, revision string) (*object.Tree, error) {
	hash, err := resolve(repo, revision)
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return nil, err
	}
	return commit.Tree()
}

func writeTreeFile(repo *git.Repository, root, path string, tree *object.Tree) error {
	file, err := tree.File(path)
	if err != nil {
		return fmt.Errorf("path %q does not exist in the source tree: %w", path, err)
	}
	data, err := blobData(repo, file.Hash)
	if err != nil {
		return err
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

func (c *Context) cmdRestore(p *cli.Parsed) error {
	repo, _, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	paths := p.Positional()
	if len(paths) == 0 {
		return fmt.Errorf("restore requires at least one path")
	}
	source := p.Get("source")
	if source == "" {
		source = "HEAD"
	}
	var tree *object.Tree
	if p.Has("staged") {
		tree, err = treeForRevision(repo, source)
		if err != nil {
			return err
		}
		idx, err := repo.Storer.Index()
		if err != nil {
			return err
		}
		for _, path := range paths {
			entry, err := tree.FindEntry(path)
			if err != nil {
				return fmt.Errorf("path %q does not exist in %s", path, source)
			}
			current, err := idx.Entry(path)
			if err != nil {
				current = &index.Entry{Name: filepath.ToSlash(path)}
				idx.Entries = append(idx.Entries, current)
			}
			current.Hash = entry.Hash
			current.Mode = entry.Mode
			current.Stage = 0
		}
		if err := repo.Storer.SetIndex(idx); err != nil {
			return err
		}
	}
	if !p.Has("staged") || p.Has("worktree") {
		idx, err := repo.Storer.Index()
		if err != nil {
			return err
		}
		for _, path := range paths {
			path = filepath.ToSlash(path)
			var hash plumbing.Hash
			if p.Has("worktree") && p.Has("staged") {
				entry, err := tree.FindEntry(path)
				if err != nil {
					return fmt.Errorf("path %q does not exist in %s", path, source)
				}
				hash = entry.Hash
			} else {
				entry, err := idx.Entry(path)
				if err != nil {
					return fmt.Errorf("path %q is not in the index", path)
				}
				hash = entry.Hash
			}
			data, err := blobData(repo, hash)
			if err != nil {
				return err
			}
			full := filepath.Join(c.Dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(full, data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Context) cmdReset(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	args := p.Positional()
	// Split revisions from pathspecs at the literal -- separator.
	revision := "HEAD"
	var paths []string
	literal := false
	for _, arg := range args {
		if arg == "--" {
			literal = true
			continue
		}
		if literal {
			paths = append(paths, arg)
		} else {
			revision = arg
		}
	}
	hash, err := resolve(repo, revision)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		tree, err := treeForRevision(repo, hash)
		if err != nil {
			return err
		}
		idx, err := repo.Storer.Index()
		if err != nil {
			return err
		}
		for _, path := range paths {
			path = filepath.ToSlash(path)
			entry, err := tree.FindEntry(path)
			if err != nil {
				return fmt.Errorf("path %q does not exist in %s", path, revision)
			}
			current, err := idx.Entry(path)
			if err != nil {
				return fmt.Errorf("path %q is not in the index", path)
			}
			current.Hash = entry.Hash
			current.Mode = entry.Mode
		}
		return repo.Storer.SetIndex(idx)
	}
	mode := git.MixedReset
	if p.Has("soft") {
		mode = git.SoftReset
	}
	if p.Has("hard") {
		mode = git.HardReset
	}
	dir, err := c.gitDirectory(repo)
	if err != nil {
		return err
	}
	if head, err := repo.Head(); err == nil {
		_ = atomicWrite(filepath.Join(dir, "ORIG_HEAD"), []byte(head.Hash().String()+"\n"))
	}
	return wt.Reset(&git.ResetOptions{Mode: mode, Commit: plumbing.NewHash(hash)})
}

func (c *Context) cmdWorktree(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	args := p.Positional()
	if len(args) == 0 {
		return fmt.Errorf("worktree requires list, add or remove")
	}
	dir, err := c.gitDirectory(repo)
	if err != nil {
		return err
	}
	worktreesDir := filepath.Join(dir, "worktrees")
	switch args[0] {
	case "list":
		entries, err := os.ReadDir(worktreesDir)
		if err == nil {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(worktreesDir, entry.Name(), "gitdir"))
				if err == nil {
					fmt.Printf("%s\n", strings.TrimSpace(string(data)))
				}
			}
		}
		return nil
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("worktree add requires a path")
		}
		target, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("worktree path already exists")
		}
		revision := "HEAD"
		if len(args) > 2 {
			revision = args[2]
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		name := filepath.Base(target)
		meta := filepath.Join(worktreesDir, name)
		if err := os.MkdirAll(meta, 0o755); err != nil {
			return err
		}
		hash, err := resolve(repo, revision)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(meta, "HEAD"), []byte(hash+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(meta, "gitdir"), []byte(filepath.Join(target, ".git")+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, ".git"), []byte("gitdir: "+filepath.ToSlash(meta)+"\n"), 0o644); err != nil {
			return err
		}
		commit, err := repo.CommitObject(plumbing.NewHash(hash))
		if err != nil {
			return err
		}
		tree, err := commit.Tree()
		if err != nil {
			return err
		}
		for _, entry := range tree.Entries {
			if err := writeTreeFile(repo, target, entry.Name, tree); err != nil {
				return err
			}
		}
		return nil
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("worktree remove requires a path")
		}
		target, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		name := filepath.Base(target)
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		return os.RemoveAll(filepath.Join(worktreesDir, name))
	default:
		return fmt.Errorf("unknown worktree action %q", args[0])
	}
}

func (c *Context) cmdSubmodule(p *cli.Parsed) error {
	_, _, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	args := p.Positional()
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, ".gitmodules"))
	if err != nil {
		return nil
	}
	type sub struct{ name, path, url string }
	var subs []sub
	var current sub
	flush := func() {
		if current.name != "" {
			subs = append(subs, current)
			current = sub{}
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[submodule ") {
			flush()
			name := strings.TrimSuffix(strings.TrimPrefix(line, "[submodule \""), "\"]")
			current.name = name
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"")
		switch strings.TrimSpace(key) {
		case "path":
			current.path = value
		case "url":
			current.url = value
		}
	}
	flush()
	switch action {
	case "init":
		for _, item := range subs {
			if item.path == "" || item.url == "" {
				return fmt.Errorf("submodule %s has no path or URL", item.name)
			}
		}
		return nil
	case "update":
		for _, item := range subs {
			if item.path == "" || item.url == "" {
				return fmt.Errorf("submodule %s has no path or URL", item.name)
			}
			if _, err := os.Stat(filepath.Join(c.Dir, item.path, ".git")); err == nil {
				continue
			}
			target := filepath.Join(c.Dir, item.path)
			if location := urlToLocalPath(item.url); location != "" {
				if err := cloneLocal(location, target, false, ""); err != nil {
					return fmt.Errorf("submodule %s update failed: %w", item.name, err)
				}
				continue
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if _, err := git.PlainClone(target, false, &git.CloneOptions{URL: item.url}); err != nil {
				return fmt.Errorf("submodule %s update failed: %w", item.name, err)
			}
		}
		return nil
	case "status":
		for _, item := range subs {
			fmt.Printf(" %s %s\n", item.path, item.name)
		}
		return nil
	default:
		return fmt.Errorf("unknown submodule action %q", action)
	}
}
