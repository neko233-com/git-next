package gitx

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// urlToLocalPath converts a local repository location to a filesystem path.
// A non-local URL is returned unchanged.
func urlToLocalPath(location string) string {
	if strings.HasPrefix(location, "file://") {
		raw := strings.TrimPrefix(location, "file://")
		// Rust canonicalize() on Windows yields a \\?\ prefix, which becomes
		// //?/ after slash normalization. net/url would read the '?' as the
		// start of a query and truncate the path, so handle it literally.
		if index := strings.Index(raw, "?"); index >= 0 && strings.Trim(raw[:index], "/") == "" {
			return `\\?\` + strings.ReplaceAll(raw[index+1:], "/", `\`)
		}
		if decoded, err := url.PathUnescape(raw); err == nil {
			raw = decoded
		}
		if runtime.GOOS == "windows" && len(raw) > 2 && raw[0] == '/' && raw[2] == ':' {
			raw = raw[1:]
		}
		return filepath.FromSlash(raw)
	}
	if info, err := os.Stat(location); err == nil && info.IsDir() {
		return location
	}
	return ""
}

func openLocal(path string) (*git.Repository, error) {
	if path == "" {
		return nil, fmt.Errorf("repository location is not a local directory")
	}
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("open local repository %s: %w", path, err)
	}
	return repo, nil
}

func copyObjects(src, dst *git.Repository) error {
	iter, err := src.Storer.IterEncodedObjects(plumbing.AnyObject)
	if err != nil {
		return err
	}
	defer iter.Close()
	return iter.ForEach(func(obj plumbing.EncodedObject) error {
		_, err := dst.Storer.SetEncodedObject(obj)
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return err
		}
		return nil
	})
}

func referenceSnapshot(repo *git.Repository) ([]*plumbing.Reference, error) {
	iter, err := repo.References()
	if err != nil {
		return nil, err
	}
	refs := []*plumbing.Reference{}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		refs = append(refs, ref)
		return nil
	})
	return refs, err
}

func cloneLocal(source, destination string, bare bool, branch string) error {
	src, err := openLocal(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(destination)
	if err == nil && len(entries) > 0 {
		return fmt.Errorf("destination path already exists and is not empty")
	}
	head, err := src.Head()
	if err != nil {
		return fmt.Errorf("source repository has no HEAD: %w", err)
	}
	opts := &git.PlainInitOptions{Bare: bare, InitOptions: git.InitOptions{
		DefaultBranch: plumbing.NewBranchReferenceName(head.Name().Short()),
	}}
	dst, err := git.PlainInitWithOptions(destination, opts)
	if err != nil {
		return err
	}
	if err := copyObjects(src, dst); err != nil {
		return err
	}
	refs, err := referenceSnapshot(src)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Name() == plumbing.HEAD {
			continue
		}
		if !ref.Name().IsBranch() && !ref.Name().IsTag() && !ref.Name().IsRemote() {
			continue
		}
		if err := dst.Storer.SetReference(plumbing.NewHashReference(ref.Name(), ref.Hash())); err != nil {
			return err
		}
	}
	target := head.Hash()
	if branch != "" {
		ref, err := src.Reference(plumbing.NewBranchReferenceName(branch), true)
		if err != nil {
			return fmt.Errorf("branch %s not found in source: %w", branch, err)
		}
		target = ref.Hash()
		if err := dst.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), target)); err != nil {
			return err
		}
	}
	if err := dst.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(head.Name().Short()), target)); err != nil {
		return err
	}
	if head.Name().IsBranch() {
		if err := dst.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, head.Name())); err != nil {
			return err
		}
	} else {
		if err := dst.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, target)); err != nil {
			return err
		}
	}
	if _, err := dst.CreateRemote(&config.RemoteConfig{
		Name: "origin", URLs: []string{source},
		Fetch: []config.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
	}); err != nil {
		return err
	}
	if bare {
		return nil
	}
	wt, err := dst.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: target})
}

func fetchLocal(repo *git.Repository, remote, location string) error {
	src, err := openLocal(location)
	if err != nil {
		return err
	}
	if err := copyObjects(src, repo); err != nil {
		return err
	}
	refs, err := referenceSnapshot(src)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if !ref.Name().IsBranch() && !ref.Name().IsTag() {
			continue
		}
		name := ref.Name()
		if name.IsBranch() {
			name = plumbing.NewRemoteReferenceName(remote, name.Short())
		}
		if err := repo.Storer.SetReference(plumbing.NewHashReference(name, ref.Hash())); err != nil {
			return err
		}
	}
	return nil
}

func pushLocal(repo *git.Repository, location string, refspecs []string) error {
	dst, err := openLocal(location)
	if err != nil {
		return err
	}
	if err := copyObjects(repo, dst); err != nil {
		return err
	}
	if len(refspecs) == 0 {
		head, err := repo.Head()
		if err != nil {
			return err
		}
		refspecs = []string{fmt.Sprintf("%s:%s", head.Name(), head.Name())}
	}
	for _, spec := range refspecs {
		source, target, ok := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
		if !ok {
			return fmt.Errorf("malformed refspec %q", spec)
		}
		ref, err := repo.Reference(plumbing.ReferenceName(source), true)
		if err != nil {
			return fmt.Errorf("refspec source %q not found: %w", source, err)
		}
		if err := dst.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(target), ref.Hash())); err != nil {
			return err
		}
	}
	return nil
}

func listLocal(location string) ([]*plumbing.Reference, error) {
	repo, err := openLocal(location)
	if err != nil {
		return nil, err
	}
	refs, err := referenceSnapshot(repo)
	if err != nil {
		return nil, err
	}
	filtered := []*plumbing.Reference{}
	for _, ref := range refs {
		if ref.Name() == plumbing.HEAD {
			continue
		}
		filtered = append(filtered, ref)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name() < filtered[j].Name() })
	if head, err := repo.Head(); err == nil {
		filtered = append([]*plumbing.Reference{plumbing.NewHashReference(plumbing.HEAD, head.Hash())}, filtered...)
	}
	return filtered, nil
}
