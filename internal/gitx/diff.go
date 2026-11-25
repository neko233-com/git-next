package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/neko233-com/git-next/internal/cli"
	"github.com/pmezard/go-difflib/difflib"
)

type snapshot struct {
	exists  bool
	mode    string
	hash    string
	content []byte
}

var zeroHash = strings.Repeat("0", 40)

func snapshotFromBlob(hash plumbing.Hash, mode string, store storer.Storer) snapshot {
	if hash.IsZero() {
		return snapshot{mode: "000000"}
	}
	obj, err := store.EncodedObject(plumbing.BlobObject, hash)
	if err != nil {
		return snapshot{mode: mode, hash: hash.String()}
	}
	reader, err := obj.Reader()
	if err != nil {
		return snapshot{mode: mode, hash: hash.String()}
	}
	defer reader.Close()
	data := make([]byte, obj.Size())
	if _, err := readFull(reader, data); err != nil {
		return snapshot{mode: mode, hash: hash.String()}
	}
	return snapshot{exists: true, mode: mode, hash: hash.String(), content: data}
}

func readFull(reader interface{ Read([]byte) (int, error) }, data []byte) (int, error) {
	total := 0
	for total < len(data) {
		n, err := reader.Read(data[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (c *Context) headTree(repo *git.Repository) (*object.Tree, error) {
	hash, err := resolve(repo, "HEAD")
	if err != nil {
		return nil, err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return nil, err
	}
	return commit.Tree()
}

func (c *Context) worktreeSnapshot(path string) snapshot {
	full := filepath.Join(c.Dir, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err != nil {
		return snapshot{mode: "000000"}
	}
	info, err := os.Stat(full)
	mode := "100644"
	if err == nil && info.Mode()&0o111 != 0 {
		mode = "100755"
	}
	return snapshot{
		exists: true, mode: mode, hash: plumbing.ComputeHash(plumbing.BlobObject, data).String(), content: data,
	}
}

func treePaths(tree *object.Tree) map[string]snapshot {
	out := map[string]snapshot{}
	if tree == nil {
		return out
	}
	for _, entry := range tree.Entries {
		if entry.Name == "" {
			continue
		}
		mode := "100644"
		if entry.Mode == 0o100755 {
			mode = "100755"
		}
		out[entry.Name] = snapshot{exists: true, mode: mode, hash: entry.Hash.String()}
	}
	return out
}

func (c *Context) indexPaths(repo *git.Repository) (map[string]snapshot, error) {
	out := map[string]snapshot{}
	idx, err := repo.Storer.Index()
	if err != nil {
		return out, err
	}
	for _, entry := range idx.Entries {
		mode := "100644"
		if entry.Mode == 0o100755 {
			mode = "100755"
		}
		out[entry.Name] = snapshot{exists: true, mode: mode, hash: entry.Hash.String()}
	}
	return out, nil
}

func (c *Context) contentOf(repo *git.Repository, snap snapshot) snapshot {
	if !snap.exists || snap.content != nil {
		return snap
	}
	obj, err := repo.Storer.EncodedObject(plumbing.BlobObject, plumbing.NewHash(snap.hash))
	if err != nil {
		return snap
	}
	reader, err := obj.Reader()
	if err != nil {
		return snap
	}
	defer reader.Close()
	data := make([]byte, obj.Size())
	if _, err := readFull(reader, data); err == nil {
		snap.content = data
	}
	return snap
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func renderFileDiff(path string, before, after snapshot, fullIndex bool) (string, error) {
	if before.hash == after.hash && before.exists == after.exists {
		return "", nil
	}
	mode := after.mode
	if !after.exists {
		mode = before.mode
	}
	oldShort, newShort := shortHash(before.hash), shortHash(after.hash)
	if fullIndex {
		oldShort, newShort = before.hash, after.hash
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", path, path))
	out.WriteString(fmt.Sprintf("index %s..%s %s\n", oldShort, newShort, mode))
	diff := difflib.UnifiedDiff{
		A: splitLines(before.content), B: splitLines(after.content),
		FromFile: "a/" + path, ToFile: "b/" + path, Context: 3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return "", err
	}
	out.WriteString(text)
	return out.String(), nil
}

func (c *Context) collectDiff(repo *git.Repository, left, right map[string]snapshot, p *cli.Parsed) (string, int, error) {
	paths := map[string]bool{}
	for path := range left {
		paths[path] = true
	}
	for path := range right {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var out strings.Builder
	changed := 0
	for _, path := range ordered {
		if len(p.Positional()) > 0 && !containsPath(p.Positional(), path) {
			continue
		}
		before := c.contentOf(repo, left[path])
		after := c.contentOf(repo, right[path])
		if before.hash == after.hash && before.exists == after.exists {
			continue
		}
		changed++
		switch {
		case p.Has("name-only"):
			out.WriteString(path + "\n")
		case p.Has("name-status"):
			code := "M"
			if !before.exists {
				code = "A"
			} else if !after.exists {
				code = "D"
			}
			out.WriteString(code + "\t" + path + "\n")
		case p.Has("numstat"):
			added, removed := countLines(before.content), countLines(after.content)
			if !before.exists {
				removed = 0
			}
			if !after.exists {
				added = 0
			}
			out.WriteString(fmt.Sprintf("%d\t%d\t%s\n", added, removed, path))
		default:
			text, err := renderFileDiff(path, before, after, p.Has("full-index"))
			if err != nil {
				return "", changed, err
			}
			out.WriteString(text)
		}
	}
	return out.String(), changed, nil
}

func containsPath(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return len(strings.SplitAfter(string(data), "\n"))
}

func (c *Context) cmdDiff(p *cli.Parsed, show bool) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	if show {
		return c.cmdShow(repo, p)
	}
	var left, right map[string]snapshot
	switch {
	case p.Has("cached"):
		tree, err := c.headTree(repo)
		if err != nil {
			left = map[string]snapshot{}
		} else {
			left = treePaths(tree)
		}
		right, err = c.indexPaths(repo)
		if err != nil {
			return err
		}
	default:
		right, err = c.indexPaths(repo)
		if err != nil {
			return err
		}
		left = make(map[string]snapshot, len(right))
		for path, snap := range right {
			loaded := c.contentOf(repo, snap)
			loaded.content = nil
			left[path] = loaded
		}
		for path, snap := range right {
			right[path] = c.worktreeSnapshot(path)
			_ = snap
		}
	}
	text, changed, err := c.collectDiff(repo, left, right, p)
	if err != nil {
		return err
	}
	fmt.Print(text)
	if p.Has("exit-code") && changed > 0 {
		return exitWithCode(1)
	}
	return nil
}

func (c *Context) cmdShow(repo *git.Repository, p *cli.Parsed) error {
	args := p.Positional()
	revision := "HEAD"
	if len(args) > 0 {
		revision = args[0]
	}
	hash, err := resolve(repo, revision)
	if err != nil {
		return err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return fmt.Errorf("object %q is not a commit", revision)
	}
	fmt.Printf("commit %s\n", commit.Hash)
	fmt.Printf("Author: %s <%s>\n", commit.Author.Name, commit.Author.Email)
	fmt.Printf("Date:   %s\n\n", formatGitDate(commit.Author.When))
	for _, line := range strings.Split(strings.TrimSuffix(commit.Message, "\n"), "\n") {
		if line == "" {
			fmt.Println()
		} else {
			fmt.Printf("    %s\n", line)
		}
	}
	if p.Has("no-patch") {
		return nil
	}
	fmt.Println()
	var before map[string]snapshot
	if commit.NumParents() > 0 {
		parent, err := commit.Parent(0)
		if err != nil {
			return err
		}
		tree, err := parent.Tree()
		if err != nil {
			return err
		}
		before = treePaths(tree)
	} else {
		before = map[string]snapshot{}
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	text, _, err := c.collectDiff(repo, before, treePaths(tree), p)
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}
