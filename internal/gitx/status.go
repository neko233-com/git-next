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
	"github.com/neko233-com/git-next/internal/cli"
)

func (c *Context) checkIndexLock(repo *git.Repository) error {
	dir, err := c.gitDirectory(repo)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "index.lock")); err == nil {
		return fmt.Errorf("another writer holds the index lock")
	}
	return nil
}

func (c *Context) cmdAdd(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	paths := p.Positional()
	if p.Has("all") || len(paths) == 0 {
		if !p.Has("all") {
			return fmt.Errorf("add requires paths or --all")
		}
		return wt.AddWithOptions(&git.AddOptions{All: true})
	}
	for _, path := range paths {
		if err := wt.AddWithOptions(&git.AddOptions{Path: filepath.ToSlash(path)}); err != nil {
			return err
		}
	}
	return nil
}

func cleanupMessage(message, mode string) string {
	switch mode {
	case "verbatim":
		return message
	case "scissors":
		lines := strings.Split(message, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "# ------------------------ >8 ------------------------") {
				lines = lines[:i]
				break
			}
		}
		message = strings.Join(lines, "\n")
	case "whitespace":
		lines := strings.Split(message, "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " \t")
		}
		message = strings.Join(lines, "\n")
	case "strip", "":
		lines := strings.Split(message, "\n")
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.HasPrefix(line, "#") {
				continue
			}
			out = append(out, strings.TrimRight(line, " \t"))
		}
		message = strings.Join(out, "\n")
	default:
		message = strings.TrimSpace(message)
	}
	return strings.Trim(message, "\n") + "\n"
}

func (c *Context) cmdCommit(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	if err := c.checkIndexLock(repo); err != nil {
		return err
	}
	messages := p.All("message")
	message := strings.Join(messages, "\n\n")
	if message == "" {
		if !p.Has("no-edit") {
			return fmt.Errorf("pass -m for a commit message, or --no-edit to finish a pending merge; interactive message editing is not yet supported")
		}
		dir, err := c.gitDirectory(repo)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, "MERGE_MSG"))
		if err != nil {
			return fmt.Errorf("no pending commit message: %w", err)
		}
		message = string(data)
	}
	cleanup := p.Get("cleanup")
	if cleanup == "" {
		if cfg, err := repo.Config(); err == nil && cfg.Raw != nil {
			for _, section := range cfg.Raw.Sections {
				if section.IsName("commit") {
					cleanup = section.Options.Get("cleanup")
				}
			}
		}
	}
	message = cleanupMessage(message, cleanup)
	author, committer := commitSignature(repo)
	_, err = wt.Commit(message, &git.CommitOptions{
		Author: author, Committer: committer, All: p.Has("all"),
		AllowEmptyCommits: p.Has("allow-empty"),
	})
	if err != nil {
		return err
	}
	if dir, dirErr := c.gitDirectory(repo); dirErr == nil {
		_ = os.Remove(filepath.Join(dir, "MERGE_MSG"))
		_ = os.Remove(filepath.Join(dir, "MERGE_HEAD"))
	}
	return nil
}

func quotePorcelainPath(path string) string {
	if !strings.ContainsAny(path, " \t\n\r\"\\") {
		return path
	}
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range path {
		switch r {
		case '\\', '"':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\n':
			out.WriteString("\\n")
		case '\t':
			out.WriteString("\\t")
		case '\r':
			out.WriteString("\\r")
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

func currentBranchName(repo *git.Repository) string {
	head, err := repo.Head()
	if err != nil || !head.Name().IsBranch() {
		return ""
	}
	return head.Name().Short()
}

func headHash(repo *git.Repository) string {
	head, err := repo.Head()
	if err != nil {
		return strings.Repeat("0", 40)
	}
	return head.Hash().String()
}

func (c *Context) branchHeader(repo *git.Repository, v2, nul bool) string {
	if v2 {
		branch := currentBranchName(repo)
		out := fmt.Sprintf("# branch.oid %s\n# branch.head %s\n", headHash(repo), branch)
		cfg, err := repo.Config()
		if err == nil {
			remote := ""
			merge := ""
			for _, section := range cfg.Raw.Sections {
				if !section.IsName("branch") {
					continue
				}
				for _, subsection := range section.Subsections {
					if subsection.IsName(branch) {
						remote = subsection.Options.Get("remote")
						merge = subsection.Options.Get("merge")
					}
				}
			}
			if remote != "" && merge != "" {
				ref := plumbing.NewRemoteReferenceName(remote, strings.TrimPrefix(merge, "refs/heads/"))
				if reference, err := repo.Reference(ref, true); err == nil {
					out += fmt.Sprintf("# branch.upstream %s\n", ref.Short())
					ahead, behind := 0, 0
					if head, err := repo.Head(); err == nil {
						ahead, behind = countDivergence(repo, head.Hash(), reference.Hash())
					}
					out += fmt.Sprintf("# branch.ab +%d -%d\n", ahead, behind)
				}
			}
		}
		return out
	}
	branch := currentBranchName(repo)
	if branch == "" {
		return "## HEAD (no branch)\n"
	}
	return "## " + branch + "\n"
}

func countDivergence(repo *git.Repository, local, upstream plumbing.Hash) (int, int) {
	localIter, err := repo.Log(&git.LogOptions{From: local})
	if err != nil {
		return 0, 0
	}
	localSet := map[plumbing.Hash]bool{}
	_ = localIter.ForEach(func(commit *object.Commit) error {
		localSet[commit.Hash] = true
		return nil
	})
	localIter.Close()
	upIter, err := repo.Log(&git.LogOptions{From: upstream})
	if err != nil {
		return 0, 0
	}
	ahead, behind := 0, 0
	_ = upIter.ForEach(func(commit *object.Commit) error {
		if localSet[commit.Hash] {
			return storerStop
		}
		behind++
		return nil
	})
	upIter.Close()
	for hash := range localSet {
		if hash == upstream {
			break
		}
	}
	for hash := range localSet {
		iter, err := repo.Log(&git.LogOptions{From: hash})
		if err != nil {
			continue
		}
		found := false
		_ = iter.ForEach(func(commit *object.Commit) error {
			if commit.Hash == upstream {
				found = true
				return storerStop
			}
			return nil
		})
		iter.Close()
		if !found {
			ahead++
		}
	}
	return ahead, behind
}

func (c *Context) cmdStatus(p *cli.Parsed) error {
	repo, wt, err := c.repoAndWorktree()
	if err != nil {
		return err
	}
	porcelain := p.Get("porcelain")
	nul := p.Has("nul")
	v2 := porcelain == "v2" || porcelain == "2"
	isPorcelain := porcelain != "" || nul
	short := p.Has("short")
	branch := p.Has("branch") || (short && !isPorcelain && !p.Has("long"))
	status, err := wt.StatusWithOptions(git.StatusOptions{Strategy: git.Preload})
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(status))
	for path := range status {
		paths = append(paths, filepath.ToSlash(path))
	}
	sort.Strings(paths)
	var out strings.Builder
	if branch && (isPorcelain || short) {
		out.WriteString(c.branchHeader(repo, v2, nul))
	}
	if !isPorcelain && !short {
		return c.renderLongStatus(repo, status, paths, branch)
	}
	grouped := [3][]string{}
	for _, path := range paths {
		item := status[path]
		if item.Staging == ' ' && item.Worktree == ' ' {
			continue
		}
		group := 0
		if item.Worktree == '?' && item.Staging == '?' {
			group = 1
		}
		grouped[group] = append(grouped[group], path)
	}
	for group := 0; group < 3; group++ {
		for _, path := range grouped[group] {
			item := status[path]
			if v2 {
				if item.Worktree == '?' && item.Staging == '?' {
					out.WriteString("? " + path + "\n")
				} else {
					out.WriteString(c.renderV2Row(repo, path, item))
				}
				continue
			}
			out.WriteString(string(item.Staging))
			out.WriteString(string(item.Worktree))
			out.WriteByte(' ')
			if nul {
				out.WriteString(path)
				out.WriteByte(0)
			} else {
				out.WriteString(quotePorcelainPath(path))
				out.WriteByte('\n')
			}
		}
	}
	fmt.Print(out.String())
	return nil
}

func (c *Context) renderV2Row(repo *git.Repository, path string, item *git.FileStatus) string {
	mH, mI, mW := "000000", "000000", "000000"
	hH, hI := strings.Repeat("0", 40), strings.Repeat("0", 40)
	if commit, err := repo.CommitObject(plumbing.NewHash(headHash(repo))); err == nil {
		if tree, err := commit.Tree(); err == nil {
			if file, err := tree.File(path); err == nil {
				mH = "100644"
				hH = file.Hash.String()
			}
		}
	}
	idx, err := repo.Storer.Index()
	if err == nil {
		for _, entry := range idx.Entries {
			if entry.Name == path {
				mI = fmt.Sprintf("%06o", entry.Mode)
				hI = entry.Hash.String()
				break
			}
		}
	}
	if _, err := os.Stat(filepath.Join(c.Dir, filepath.FromSlash(path))); err == nil {
		mW = "100644"
	}
	code := []byte{byte(item.Staging), byte(item.Worktree)}
	for i, b := range code {
		if b == ' ' {
			code[i] = '.'
		}
	}
	return fmt.Sprintf("1 %s N... %s %s %s %s %s %s\n", string(code), mH, mI, mW, hH, hI, path)
}

func (c *Context) renderLongStatus(repo *git.Repository, status git.Status, paths []string, branch bool) error {
	var out strings.Builder
	if branch {
		out.WriteString(c.branchHeader(repo, false, false))
	}
	seen := map[string]bool{}
	for _, want := range []string{"Changes to be committed:", "Changes not staged for commit:", "Untracked files:"} {
		var rows []string
		for _, path := range paths {
			item := status[path]
			var label, detail string
			switch {
			case item.Staging != ' ' && item.Staging != '?':
				label, detail = "Changes to be committed:", stagedAction(byte(item.Staging))
			case item.Worktree == '?':
				label, detail = "Untracked files:", "  (use \"git add <file>...\" to update what will be committed)"
			case item.Worktree != ' ':
				label, detail = "Changes not staged for commit:", "  (use \"git add <file>...\" to update what will be committed)"
			default:
				continue
			}
			if label != want {
				continue
			}
			rows = append(rows, fmt.Sprintf("\t%s:   %s\n", detail, quotePorcelainPath(path)))
		}
		if len(rows) == 0 {
			continue
		}
		if !seen[want] {
			out.WriteString(want + "\n")
			out.WriteString("  (use \"git add <file>...\" to update what will be committed)\n\n")
			seen[want] = true
		}
		out.WriteString(strings.Join(rows, ""))
		out.WriteByte('\n')
	}
	if out.Len() == 0 {
		out.WriteString("nothing to commit, working tree clean\n")
	}
	fmt.Print(out.String())
	return nil
}

func stagedAction(code byte) string {
	switch code {
	case 'A':
		return "new file"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	default:
		return "modified"
	}
}
