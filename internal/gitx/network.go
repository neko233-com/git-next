package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/neko233-com/git-next/internal/cli"
)

func localRepositoryURL(url string) string {
	if info, err := os.Stat(url); err == nil && info.IsDir() {
		abs, err := filepath.Abs(url)
		if err == nil {
			return "file://" + filepath.ToSlash(abs)
		}
	}
	return url
}

func (c *Context) cmdClone(p *cli.Parsed) error {
	args := p.Positional()
	if len(args) < 1 {
		return fmt.Errorf("clone requires a repository URL")
	}
	url := args[0]
	destination := ""
	if len(args) > 1 {
		destination = args[1]
	} else {
		base := strings.TrimSuffix(filepath.Base(strings.TrimRight(url, "/\\")), ".git")
		destination = base
	}
	if local := urlToLocalPath(url); local != "" {
		return cloneLocal(local, destination, p.Has("bare"), p.Get("branch"))
	}
	opts := &git.CloneOptions{URL: url, Progress: nil}
	if branch := p.Get("branch"); branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		opts.SingleBranch = true
	}
	if depth := parseInt(p.Get("depth"), 0); depth > 0 {
		opts.Depth = depth
		opts.SingleBranch = true
	}
	_, err := git.PlainClone(destination, p.Has("bare"), opts)
	if err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}
	return nil
}

func ignoreUpToDate(err error) error {
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}

func (c *Context) cmdFetch(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	args := p.Positional()
	remote := "origin"
	if len(args) > 0 {
		remote = args[0]
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	if location := urlToLocalPath(remoteURL(cfg, remote)); location != "" {
		return fetchLocal(repo, remote, location)
	}
	return ignoreUpToDate(repo.Fetch(&git.FetchOptions{
		RemoteName: remote, Tags: git.AllTags,
	}))
}

func (c *Context) cmdPull(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	args := p.Positional()
	remote := "origin"
	if len(args) > 0 {
		remote = args[0]
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	if location := urlToLocalPath(remoteURL(cfg, remote)); location != "" {
		if err := fetchLocal(repo, remote, location); err != nil {
			return err
		}
	} else if err := ignoreUpToDate(repo.Fetch(&git.FetchOptions{RemoteName: remote})); err != nil {
		return err
	}
	branch := currentBranchName(repo)
	if branch == "" {
		return fmt.Errorf("pull requires a branch")
	}
	refName := plumbing.NewRemoteReferenceName(remote, branch)
	ref, err := repo.Reference(refName, true)
	if err != nil {
		return fmt.Errorf("remote tracking branch %s not found: %w", refName.Short(), err)
	}
	head, err := repo.Head()
	if err != nil {
		return err
	}
	if head.Hash() == ref.Hash() {
		return nil
	}
	if err := repo.Merge(*ref, git.MergeOptions{}); err != nil {
		return fmt.Errorf("pull requires a fast-forward: %w", err)
	}
	return wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: ref.Hash()})
}

func (c *Context) cmdPush(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	args := p.Positional()
	remote := "origin"
	if len(args) > 0 {
		remote = args[0]
		args = args[1:]
	}
	if p.Has("dry-run") {
		return fmt.Errorf("push --dry-run is not supported by the Go client")
	}
	branch := currentBranchName(repo)
	if branch == "" {
		return fmt.Errorf("push requires a branch")
	}
	refspecs := args
	if len(refspecs) == 0 {
		refspecs = []string{fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch)}
	}
	for i, spec := range refspecs {
		if !strings.Contains(spec, ":") {
			refspecs[i] = fmt.Sprintf("refs/heads/%s:refs/heads/%s", spec, spec)
		}
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	if location := urlToLocalPath(remoteURL(cfg, remote)); location != "" {
		if err := pushLocal(repo, location, refspecs); err != nil {
			return err
		}
	} else {
		parsed := make([]config.RefSpec, 0, len(refspecs))
		for _, spec := range refspecs {
			parsed = append(parsed, config.RefSpec(spec))
		}
		opts := &git.PushOptions{
			RemoteName: remote, RefSpecs: parsed, Force: p.Has("force"),
			Atomic: p.Has("atomic"), Prune: p.Has("delete"),
		}
		if err := ignoreUpToDate(repo.Push(opts)); err != nil {
			return err
		}
	}
	if p.Has("set-upstream") {
		cfg.Raw.SetOption("branch", branch, "remote", remote)
		cfg.Raw.SetOption("branch", branch, "merge", "refs/heads/"+branch)
		return repo.SetConfig(cfg)
	}
	return nil
}

func (c *Context) cmdLsRemote(p *cli.Parsed) error {
	args := p.Positional()
	if len(args) < 1 {
		return fmt.Errorf("ls-remote requires a URL")
	}
	var refs []*plumbing.Reference
	if location := urlToLocalPath(args[0]); location != "" {
		var err error
		refs, err = listLocal(location)
		if err != nil {
			return err
		}
	} else {
		remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
			Name: "origin", URLs: []string{args[0]},
		})
		var err error
		refs, err = remote.List(&git.ListOptions{})
		if err != nil {
			return err
		}
	}
	for _, ref := range refs {
		if p.Has("heads") && !ref.Name().IsBranch() {
			continue
		}
		if p.Has("tags") && !ref.Name().IsTag() {
			continue
		}
		fmt.Printf("%s\t%s\n", ref.Hash(), ref.Name())
	}
	return nil
}
