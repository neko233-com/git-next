package gitx

import (
	"encoding/base64"
	"encoding/json"
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

func (c *Context) gitPath(repo *git.Repository, name string) (string, error) {
	dir, err := c.gitDirectory(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func (c *Context) cmdMerge(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	mergeHeadPath, err := c.gitPath(repo, "MERGE_HEAD")
	if err != nil {
		return err
	}
	if p.Has("abort") {
		if _, err := os.Stat(mergeHeadPath); err != nil {
			return fmt.Errorf("no merge in progress")
		}
		data, err := os.ReadFile(mergeHeadPath)
		if err != nil {
			return err
		}
		hash := strings.TrimSpace(string(data))
		if err := os.Remove(mergeHeadPath); err != nil {
			return err
		}
		msgPath, _ := c.gitPath(repo, "MERGE_MSG")
		_ = os.Remove(msgPath)
		return wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: plumbing.NewHash(hash)})
	}
	if p.Has("continue") {
		return fmt.Errorf("merge --continue requires a pending merge with conflicts")
	}
	args := p.Positional()
	if len(args) == 0 {
		return fmt.Errorf("merge requires a revision")
	}
	revision := args[0]
	targetHash, err := resolve(repo, revision)
	if err != nil {
		return err
	}
	head, err := repo.Head()
	if err != nil {
		return err
	}
	ours := head.Hash().String()
	if !p.Has("no-commit") && (p.Has("ff-only") || !p.Has("no-ff") && isAncestor(repo, ours, targetHash)) {
		target := plumbing.NewHash(targetHash)
		if err := repo.Storer.SetReference(plumbing.NewHashReference(head.Name(), target)); err != nil {
			return err
		}
		return wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: target})
	}
	oursTree, err := treeForRevision(repo, ours)
	if err != nil {
		return err
	}
	theirsTree, err := treeForRevision(repo, targetHash)
	if err != nil {
		return err
	}
	baseHash := mergeBase(repo, ours, targetHash)
	baseTree := map[string]plumbing.Hash{}
	if baseHash != "" {
		if tree, err := treeForRevision(repo, baseHash); err == nil {
			for _, entry := range tree.Entries {
				baseTree[entry.Name] = entry.Hash
			}
		}
	}
	ourFiles := map[string]plumbing.Hash{}
	for _, entry := range oursTree.Entries {
		ourFiles[entry.Name] = entry.Hash
	}
	theirFiles := map[string]plumbing.Hash{}
	for _, entry := range theirsTree.Entries {
		theirFiles[entry.Name] = entry.Hash
	}
	merged := map[string]plumbing.Hash{}
	for path, hash := range theirFiles {
		base := baseTree[path]
		oursHash := ourFiles[path]
		if oursHash != base && hash != base && oursHash != hash {
			// Conflicting change: publish merge state and fail clearly.
			_ = os.WriteFile(mergeHeadPath, []byte(targetHash+"\n"), 0o644)
			msgPath, _ := c.gitPath(repo, "MERGE_MSG")
			message := p.Get("message")
			if message == "" {
				message = "Merge " + revision
			}
			_ = os.WriteFile(msgPath, []byte(message+"\n"), 0o644)
			return fmt.Errorf("merge conflict in %s; resolve manually or run merge --abort", path)
		}
		merged[path] = hash
	}
	for path, hash := range ourFiles {
		if _, ok := merged[path]; !ok {
			merged[path] = hash
		}
	}
	paths := make([]string, 0, len(merged))
	for path := range merged {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, err := blobData(repo, merged[path])
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
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return err
	}
	if p.Has("no-commit") {
		msgPath, _ := c.gitPath(repo, "MERGE_MSG")
		message := p.Get("message")
		if message == "" {
			message = "Merge " + revision
		}
		_ = os.WriteFile(msgPath, []byte(message+"\n"), 0o644)
		return os.WriteFile(mergeHeadPath, []byte(targetHash+"\n"), 0o644)
	}
	message := p.Get("message")
	if message == "" {
		message = "Merge " + revision
	}
	author, committer := commitSignature(repo)
	_, err = wt.Commit(message+"\n", &git.CommitOptions{
		Author: author, Committer: committer,
		Parents: []plumbing.Hash{plumbing.NewHash(ours), plumbing.NewHash(targetHash)},
	})
	return err
}

func isAncestor(repo *git.Repository, ancestor, descendant string) bool {
	iter, err := repo.Log(&git.LogOptions{From: plumbing.NewHash(descendant)})
	if err != nil {
		return false
	}
	defer iter.Close()
	found := false
	_ = iter.ForEach(func(commit *object.Commit) error {
		if commit.Hash.String() == ancestor {
			found = true
			return storerStop
		}
		return nil
	})
	return found
}

func mergeBase(repo *git.Repository, ours, theirs string) string {
	iter, err := repo.Log(&git.LogOptions{From: plumbing.NewHash(ours)})
	if err != nil {
		return ""
	}
	defer iter.Close()
	base := ""
	_ = iter.ForEach(func(commit *object.Commit) error {
		if isAncestor(repo, commit.Hash.String(), theirs) {
			base = commit.Hash.String()
			return storerStop
		}
		return nil
	})
	return base
}

type stashSnapshot struct {
	Message string            `json:"message"`
	Head    string            `json:"head"`
	Files   map[string]string `json:"files"`
}

func (c *Context) stashPath(repo *git.Repository) (string, error) {
	return c.gitPath(repo, "git-next-stash.json")
}

func (c *Context) cmdStash(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	args := p.Positional()
	action := "push"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	path, err := c.stashPath(repo)
	if err != nil {
		return err
	}
	switch action {
	case "push":
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("a git-next stash already exists; nested stash entries are not supported")
		}
		head, err := resolve(repo, "HEAD")
		if err != nil {
			return err
		}
		tree, err := treeForRevision(repo, head)
		if err != nil {
			return err
		}
		idx, err := repo.Storer.Index()
		if err != nil {
			return err
		}
		snapshot := stashSnapshot{
			Message: p.Get("message"), Head: head, Files: map[string]string{},
		}
		for _, entry := range idx.Entries {
			full := filepath.Join(c.Dir, filepath.FromSlash(entry.Name))
			data, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			var headData []byte
			if file, err := tree.File(entry.Name); err == nil {
				headData, _ = blobData(repo, file.Hash)
			}
			if string(data) == string(headData) {
				continue
			}
			snapshot.Files[entry.Name] = base64.StdEncoding.EncodeToString(data)
			if err := os.WriteFile(full, headData, 0o644); err != nil {
				return err
			}
		}
		if len(snapshot.Files) == 0 {
			return fmt.Errorf("no local changes to save")
		}
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWrite(path, append(data, '\n')); err != nil {
			return err
		}
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			return err
		}
		return nil
	case "pop", "apply", "drop":
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("no stash entry: %w", err)
		}
		var snapshot stashSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return fmt.Errorf("unreadable stash entry: %w", err)
		}
		if action != "drop" {
			for name, encoded := range snapshot.Files {
				content, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					return err
				}
				full := filepath.Join(c.Dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(full, content, 0o644); err != nil {
					return err
				}
			}
		}
		if action != "apply" {
			return os.Remove(path)
		}
		return nil
	case "list":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var snapshot stashSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return err
		}
		fmt.Printf("stash@{0}: %s\n", snapshot.Message)
		return nil
	default:
		return fmt.Errorf("unknown stash action %q", action)
	}
}

func applyCommitFiles(repo *git.Repository, commit *object.Commit, root string, revert bool) error {
	parentHash := plumbing.ZeroHash
	if commit.NumParents() > 0 {
		parent, err := commit.Parent(0)
		if err != nil {
			return err
		}
		parentHash = parent.Hash
	}
	before := map[string]plumbing.Hash{}
	if !parentHash.IsZero() {
		parent, err := repo.CommitObject(parentHash)
		if err != nil {
			return err
		}
		tree, err := parent.Tree()
		if err != nil {
			return err
		}
		for _, entry := range tree.Entries {
			before[entry.Name] = entry.Hash
		}
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	after := map[string]plumbing.Hash{}
	for _, entry := range tree.Entries {
		after[entry.Name] = entry.Hash
	}
	source, target := before, after
	if revert {
		source, target = after, before
	}
	paths := map[string]bool{}
	for path := range source {
		paths[path] = true
	}
	for path := range target {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	idx, err := repo.Storer.Index()
	if err != nil {
		return err
	}
	for _, path := range ordered {
		full := filepath.Join(root, filepath.FromSlash(path))
		hash, ok := target[path]
		if !ok {
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				return err
			}
			if _, err := idx.Remove(path); err != nil {
				return err
			}
			continue
		}
		data, err := blobData(repo, hash)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
		if _, err := wtAddHash(repo, idx, path, hash); err != nil {
			return err
		}
	}
	return repo.Storer.SetIndex(idx)
}

func wtAddHash(repo *git.Repository, idx *index.Index, path string, hash plumbing.Hash) (bool, error) {
	if entry, err := idx.Entry(path); err == nil {
		entry.Hash = hash
		return false, nil
	}
	entry := idx.Add(path)
	if entry == nil {
		return false, fmt.Errorf("could not stage %s", path)
	}
	entry.Hash = hash
	return true, nil
}

func commitByRevision(repo *git.Repository, revision string) (*object.Commit, error) {
	hash, err := resolve(repo, revision)
	if err != nil {
		return nil, err
	}
	return repo.CommitObject(plumbing.NewHash(hash))
}

func (c *Context) cmdCherryPick(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	args := p.Positional()
	if len(args) == 0 {
		return fmt.Errorf("cherry-pick requires a revision")
	}
	commit, err := commitByRevision(repo, args[0])
	if err != nil {
		return err
	}
	if err := applyCommitFiles(repo, commit, c.Dir, false); err != nil {
		return err
	}
	author, committer := commitSignature(repo)
	_, err = wt.Commit(strings.TrimSuffix(commit.Message, "\n")+"\n", &git.CommitOptions{
		Author:    &object.Signature{Name: commit.Author.Name, Email: commit.Author.Email, When: commit.Author.When},
		Committer: committer,
	})
	_ = author
	return err
}

func (c *Context) cmdRevert(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	args := p.Positional()
	revision := "HEAD"
	if len(args) > 0 {
		revision = args[0]
	}
	commit, err := commitByRevision(repo, revision)
	if err != nil {
		return err
	}
	if err := applyCommitFiles(repo, commit, c.Dir, true); err != nil {
		return err
	}
	message := "Revert \"" + firstLine(commit.Message) + "\""
	author, committer := commitSignature(repo)
	_, err = wt.Commit(message+"\n", &git.CommitOptions{Author: author, Committer: committer})
	return err
}

func (c *Context) cmdRebase(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	args := p.Positional()
	onto := p.Get("onto")
	upstream := ""
	branch := ""
	if len(args) > 0 {
		upstream = args[0]
	}
	if len(args) > 1 {
		branch = args[1]
	}
	if p.Has("abort") || p.Has("continue") {
		return fmt.Errorf("no rebase in progress")
	}
	if onto == "" || upstream == "" {
		return fmt.Errorf("rebase requires --onto and an upstream")
	}
	ontoHash, err := resolve(repo, onto)
	if err != nil {
		return err
	}
	upstreamHash, err := resolve(repo, upstream)
	if err != nil {
		return err
	}
	if ontoHash != upstreamHash {
		return fmt.Errorf("rebase with a different onto and upstream is not supported by the Go client")
	}
	if branch == "" {
		return nil
	}
	return wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch)})
}
