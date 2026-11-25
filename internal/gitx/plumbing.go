package gitx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/neko233-com/git-next/internal/cli"
)

func (c *Context) repoAndWorktree() (*git.Repository, *git.Worktree, error) {
	repo, err := c.Open()
	if err != nil {
		return nil, nil, err
	}
	wt, err := c.Worktree(repo)
	return repo, wt, err
}

func gitPathsToSlash(path string) string { return filepath.ToSlash(path) }

// findGitDir walks up from start without resolving symbolic links, so a
// Windows 8.3 short path stays short and `rev-parse --git-dir` matches Git.
func findGitDir(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil && info.Mode().IsRegular() {
			if objects, err := os.Stat(filepath.Join(dir, "objects")); err == nil && objects.IsDir() {
				return dir, nil
			}
		}
		candidate := filepath.Join(dir, git.GitDirName)
		if info, err := os.Stat(candidate); err == nil {
			if info.IsDir() {
				return candidate, nil
			}
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(candidate)
				if err == nil {
					line := strings.TrimSpace(string(data))
					if target, ok := strings.CutPrefix(line, "gitdir: "); ok {
						if !filepath.IsAbs(target) {
							target = filepath.Join(dir, target)
						}
						return filepath.Abs(target)
					}
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not a git repository: %s", start)
}

func (c *Context) gitDirectory(repo *git.Repository) (string, error) {
	if c.GitDir != "" {
		return filepath.Abs(c.GitDir)
	}
	return findGitDir(c.Dir)
}

func (c *Context) cmdRevParse(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	switch {
	case p.Has("show-toplevel"):
		dir, err := c.gitDirectory(repo)
		if err != nil {
			return err
		}
		if _, err := repo.Worktree(); err != nil {
			return fmt.Errorf("bare repository has no working tree")
		}
		fmt.Println(gitPathsToSlash(filepath.Dir(dir)))
	case p.Has("absolute-git-dir"):
		dir, err := c.gitDirectory(repo)
		if err != nil {
			return err
		}
		fmt.Println(gitPathsToSlash(dir))
	case p.Has("git-dir"):
		dir, err := c.gitDirectory(repo)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(c.Dir, dir)
		if err != nil {
			return err
		}
		fmt.Println(gitPathsToSlash(rel))
	case p.Has("is-bare-repository"):
		_, err := repo.Worktree()
		if err != nil {
			fmt.Println("true")
		} else {
			fmt.Println("false")
		}
	default:
		rev := "HEAD"
		if args := p.Positional(); len(args) > 0 {
			rev = args[0]
		}
		hash, err := resolve(repo, rev)
		if err != nil {
			return err
		}
		if p.Has("short") {
			hash = shortHash(hash)
		}
		fmt.Println(hash)
	}
	return nil
}

func (c *Context) cmdCatFile(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	args := p.Positional()
	if len(args) != 1 {
		return fmt.Errorf("cat-file requires exactly one object")
	}
	spec := args[0]
	hashString, path, hasPath := strings.Cut(spec, ":")
	hashString, err = resolve(repo, hashString)
	if err != nil {
		return err
	}
	hash := plumbing.NewHash(hashString)
	if hasPath {
		commit, err := repo.CommitObject(hash)
		if err != nil {
			return err
		}
		file, err := commit.File(path)
		if err != nil {
			return fmt.Errorf("object %q not found: %w", spec, err)
		}
		blob, err := file.Contents()
		if err != nil {
			return err
		}
		if p.Has("kind") {
			fmt.Println("blob")
		} else if p.Has("size") {
			fmt.Println(len(blob))
		} else {
			fmt.Print(blob)
		}
		return nil
	}
	obj, err := repo.Object(plumbing.AnyObject, hash)
	if err != nil {
		return fmt.Errorf("object %q not found: %w", spec, err)
	}
	if p.Has("kind") {
		fmt.Println(obj.Type())
		return nil
	}
	if tree, ok := obj.(*object.Tree); ok {
		for _, entry := range tree.Entries {
			kind := "blob"
			if entry.Mode == filemodeTree {
				kind = "tree"
			}
			fmt.Printf("%06o %s %s\t%s\n", entry.Mode, kind, entry.Hash, entry.Name)
		}
		return nil
	}
	encoded, err := repo.Storer.EncodedObject(plumbing.AnyObject, hash)
	if err != nil {
		return err
	}
	if p.Has("size") {
		fmt.Println(encoded.Size())
		return nil
	}
	reader, err := encoded.Reader()
	if err != nil {
		return err
	}
	defer reader.Close()
	buf := make([]byte, encoded.Size())
	if _, err := io.ReadFull(reader, buf); err != nil {
		return err
	}
	_, err = os.Stdout.Write(buf)
	return err
}

const filemodeTree = 040000

func (c *Context) cmdHashObject(p *cli.Parsed) error {
	args := p.Positional()
	if len(args) != 1 {
		return fmt.Errorf("hash-object requires one file")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	hash := plumbing.ComputeHash(plumbing.BlobObject, data)
	if p.Has("write") {
		repo, err := c.Open()
		if err != nil {
			return err
		}
		obj := &plumbing.MemoryObject{}
		obj.SetType(plumbing.BlobObject)
		obj.SetSize(int64(len(data)))
		if _, err := obj.Write(data); err != nil {
			return err
		}
		if _, err := repo.Storer.SetEncodedObject(obj); err != nil {
			return err
		}
	}
	fmt.Println(hash.String())
	return nil
}

func (c *Context) cmdLsFiles(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return err
	}
	entries := append([]*index.Entry(nil), idx.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, entry := range entries {
		if p.Has("stage") {
			fmt.Printf("%06o %s %d\t", entry.Mode, entry.Hash, entry.Stage)
		}
		if p.Has("nul") {
			fmt.Print(entry.Name, "\x00")
		} else {
			fmt.Println(entry.Name)
		}
	}
	return nil
}

func (c *Context) cmdShowRef(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	if p.Has("head") {
		if head, err := repo.Head(); err == nil {
			fmt.Printf("%s HEAD\n", head.Hash())
		}
	}
	refs, err := repo.References()
	if err != nil {
		return err
	}
	type row struct{ name, hash string }
	var rows []row
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name() == plumbing.HEAD {
			return nil
		}
		rows = append(rows, row{ref.Name().String(), ref.Hash().String()})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, item := range rows {
		if p.Has("heads") && !strings.HasPrefix(item.name, "refs/heads/") {
			continue
		}
		if p.Has("tags") && !strings.HasPrefix(item.name, "refs/tags/") {
			continue
		}
		fmt.Printf("%s %s\n", item.hash, item.name)
	}
	return nil
}

func splitRevArgs(args []string) (revisions, paths []string) {
	literal := false
	for _, arg := range args {
		if arg == "--" {
			literal = true
			continue
		}
		if literal {
			paths = append(paths, arg)
			continue
		}
		if strings.Contains(arg, ":") || strings.HasPrefix(arg, "-") {
			revisions = append(revisions, arg)
			continue
		}
		revisions = append(revisions, arg)
	}
	return revisions, paths
}

func (c *Context) cmdLog(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	positional := p.Positional()
	revisions, _ := splitRevArgs(positional)
	start := "HEAD"
	if len(revisions) > 0 && !strings.HasPrefix(revisions[0], "-") {
		start = revisions[0]
	}
	count := parseInt(p.Get("max-count"), -1)
	if count < 0 {
		for _, arg := range positional {
			if len(arg) > 1 && arg[0] == '-' && allDigits(arg[1:]) {
				count = parseInt(arg[1:], -1)
				break
			}
		}
	}
	hash, err := resolve(repo, start)
	if err != nil {
		return err
	}
	iter, err := repo.Log(&git.LogOptions{From: plumbing.NewHash(hash), Order: git.LogOrderCommitterTime})
	if err != nil {
		return err
	}
	defer iter.Close()
	formatName := p.Get("format")
	oneline := p.Has("oneline") || formatName == "oneline"
	separator := "\n"
	if p.Has("nul") {
		separator = "\x00"
	}
	printed := 0
	err = iter.ForEach(func(commit *object.Commit) error {
		if count >= 0 && printed >= count {
			return storerStop
		}
		switch {
		case oneline:
			fmt.Printf("%s %s%s", shortHash(commit.Hash.String()), firstLine(commit.Message), separator)
		case formatName != "":
			fmt.Println(renderFormat(commit, formatName, p.Get("date")))
		default:
			fmt.Print(commit.String())
		}
		printed++
		return nil
	})
	if err == storerStop {
		return nil
	}
	return err
}

var storerStop = fmt.Errorf("stop")

func firstLine(message string) string {
	if index := strings.IndexByte(message, '\n'); index >= 0 {
		return message[:index]
	}
	return message
}

func renderFormat(commit *object.Commit, format, date string) string {
	replacer := strings.NewReplacer(
		"%H", commit.Hash.String(),
		"%h", shortHash(commit.Hash.String()),
		"%s", firstLine(commit.Message),
		"%B", strings.TrimSuffix(commit.Message, "\n"),
		"%an", commit.Author.Name,
		"%ae", commit.Author.Email,
		"%ad", formatGitDate(commit.Author.When),
	)
	return replacer.Replace(format)
}

func (c *Context) cmdBranch(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	args := p.Positional()
	if p.Has("delete") || p.Has("delete-force") {
		if len(args) == 0 {
			return fmt.Errorf("branch deletion requires a name")
		}
		return repo.DeleteBranch(args[0])
	}
	if len(args) == 0 {
		head, _ := repo.Head()
		var current string
		if head != nil && head.Name().IsBranch() {
			current = head.Name().Short()
		}
		iter, err := repo.Branches()
		if err != nil {
			return err
		}
		var names []string
		err = iter.ForEach(func(ref *plumbing.Reference) error {
			names = append(names, ref.Name().Short())
			return nil
		})
		if err != nil {
			return err
		}
		sort.Strings(names)
		for _, name := range names {
			marker := " "
			if name == current {
				marker = "*"
			}
			fmt.Printf("%s %s\n", marker, name)
		}
		return nil
	}
	name := args[0]
	if strings.HasPrefix(name, "-") || name == "HEAD" {
		return fmt.Errorf("invalid branch name %q", name)
	}
	revision := "HEAD"
	if len(args) > 1 {
		revision = args[1]
	}
	hash, err := resolve(repo, revision)
	if err != nil {
		return err
	}
	return repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), plumbing.NewHash(hash)))
}

func (c *Context) cmdTag(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	args := p.Positional()
	if len(args) == 0 {
		iter, err := repo.Tags()
		if err != nil {
			return err
		}
		var names []string
		err = iter.ForEach(func(ref *plumbing.Reference) error {
			names = append(names, ref.Name().Short())
			return nil
		})
		if err != nil {
			return err
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Println(name)
		}
		return nil
	}
	name := args[0]
	if p.Has("delete") {
		return repo.DeleteTag(name)
	}
	revision := "HEAD"
	if len(args) > 1 {
		revision = args[1]
	}
	hash, err := resolve(repo, revision)
	if err != nil {
		return err
	}
	if !p.Has("annotate") && p.Get("message") == "" {
		// A nil option list creates a lightweight tag.
		_, err = repo.CreateTag(name, plumbing.NewHash(hash), nil)
		return err
	}
	opts := &git.CreateTagOptions{Message: p.Get("message")}
	if opts.Message == "" {
		return fmt.Errorf("annotated tag requires -m")
	}
	opts.Tagger = signature(repo, "GIT_COMMITTER")
	_, err = repo.CreateTag(name, plumbing.NewHash(hash), opts)
	return err
}

func remoteNames(cfg *config.Config) []string {
	names := []string{}
	for _, section := range cfg.Raw.Sections {
		if section.IsName("remote") {
			for _, subsection := range section.Subsections {
				names = append(names, subsection.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func remoteURL(cfg *config.Config, name string) string {
	for _, section := range cfg.Raw.Sections {
		if section.IsName("remote") {
			for _, subsection := range section.Subsections {
				if subsection.IsName(name) {
					return subsection.Options.Get("url")
				}
			}
		}
	}
	return ""
}

func (c *Context) cmdRemote(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	args := p.Positional()
	action := "show"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	switch action {
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("remote add requires a name and a URL")
		}
		name, url := args[0], args[1]
		if remoteURL(cfg, name) != "" {
			return fmt.Errorf("remote %s already exists", name)
		}
		_, err := repo.CreateRemote(&config.RemoteConfig{
			Name: name, URLs: []string{url},
			Fetch: []config.RefSpec{config.RefSpec(fmt.Sprintf("+refs/heads/*:refs/remotes/%s/*", name))},
		})
		return err
	case "get-url":
		if len(args) == 0 {
			return fmt.Errorf("remote get-url requires a name")
		}
		url := remoteURL(cfg, args[0])
		if url == "" {
			return fmt.Errorf("remote %s not found", args[0])
		}
		fmt.Println(url)
		return nil
	case "set-url":
		if len(args) < 2 {
			return fmt.Errorf("remote set-url requires a name and a URL")
		}
		cfg.Raw.SetOption("remote", args[0], "url", args[1])
		return repo.SetConfig(cfg)
	case "remove", "rm":
		if len(args) == 0 {
			return fmt.Errorf("remote remove requires a name")
		}
		return repo.DeleteRemote(args[0])
	case "show", "":
		for _, name := range remoteNames(cfg) {
			if !p.Has("verbose") {
				fmt.Println(name)
				continue
			}
			fmt.Printf("%s\t%s (fetch)\n", name, remoteURL(cfg, name))
		}
		return nil
	default:
		return fmt.Errorf("unknown remote action %q", action)
	}
}

func splitConfigKey(key string) (section, subsection, option string, ok bool) {
	parts := strings.Split(key, ".")
	if len(parts) == 2 {
		return parts[0], "", parts[1], true
	}
	if len(parts) >= 3 {
		return parts[0], strings.Join(parts[1:len(parts)-1], "."), parts[len(parts)-1], true
	}
	return "", "", "", false
}

func lookupConfig(cfg *format.Config, key string) (string, bool) {
	sectionName, subsectionName, optionName, ok := splitConfigKey(key)
	if !ok {
		return "", false
	}
	for _, section := range cfg.Sections {
		if !section.IsName(sectionName) {
			continue
		}
		if subsectionName == "" {
			if section.Options.Has(optionName) {
				return section.Options.Get(optionName), true
			}
			continue
		}
		for _, subsection := range section.Subsections {
			if subsection.IsName(subsectionName) && subsection.Options.Has(optionName) {
				return subsection.Options.Get(optionName), true
			}
		}
	}
	return "", false
}

func setConfigValue(cfg *format.Config, key, value string) error {
	sectionName, subsectionName, optionName, ok := splitConfigKey(key)
	if !ok {
		return fmt.Errorf("invalid config key %q", key)
	}
	cfg.SetOption(sectionName, subsectionName, optionName, value)
	return nil
}

func (c *Context) cmdConfig(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	args := p.Positional()
	if p.Has("list") {
		for _, section := range cfg.Raw.Sections {
			for _, option := range section.Options {
				fmt.Printf("%s.%s=%s\n", strings.ToLower(section.Name), strings.ToLower(option.Key), option.Value)
			}
			for _, subsection := range section.Subsections {
				for _, option := range subsection.Options {
					fmt.Printf("%s.%s.%s=%s\n", strings.ToLower(section.Name), strings.ToLower(subsection.Name), strings.ToLower(option.Key), option.Value)
				}
			}
		}
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("config requires a key")
	}
	key := args[0]
	if len(args) == 1 && !p.Has("unset") {
		value, ok := lookupConfig(cfg.Raw, key)
		if !ok {
			if p.Has("get") || p.Has("get-all") {
				return fmt.Errorf("key %q not found", key)
			}
			return nil
		}
		fmt.Println(value)
		return nil
	}
	if p.Has("unset") {
		sectionName, subsectionName, optionName, ok := splitConfigKey(key)
		if !ok {
			return fmt.Errorf("invalid config key %q", key)
		}
		for _, section := range cfg.Raw.Sections {
			if section.IsName(sectionName) {
				if subsectionName == "" {
					section.RemoveOption(optionName)
				} else {
					for _, subsection := range section.Subsections {
						if subsection.IsName(subsectionName) {
							subsection.RemoveOption(optionName)
						}
					}
				}
			}
		}
		return repo.SetConfig(cfg)
	}
	if len(args) < 2 {
		return fmt.Errorf("config set requires a value")
	}
	if err := setConfigValue(cfg.Raw, key, args[1]); err != nil {
		return err
	}
	return repo.SetConfig(cfg)
}

func (c *Context) cmdCompatibility(p *cli.Parsed) error {
	if p.Has("json") {
		fmt.Println(`{
  "schema_version": 1,
  "product": "git-next",
  "status": "experimental",
  "default_branch": "main",
  "implementation": "go",
  "native_git_linked": false,
  "external_git_fallback": false,
  "external_ssh_fallback": false,
  "complete_git_parity": false,
  "features": {
    "native_status_porcelain_v1_subset": true,
    "native_status_porcelain_v2_subset": true,
    "native_diff_show_log_subset": true,
    "native_restore_reset_subset": true,
    "cross_platform_lf_defaults": true,
    "lfs_pointer_cache": true
  },
  "replacement_blockers": [
    "Complete Git CLI, protocol and hook parity is not accepted",
    "Full merge/rebase/stash recovery semantics are not accepted",
    "SHA-256 repositories are not supported by go-git"
  ]
}`)
		return nil
	}
	fmt.Println("git-next compatibility: experimental; see compatibility --json")
	return nil
}

func (c *Context) cmdVersion(p *cli.Parsed) error {
	if p.Has("json") {
		fmt.Printf(`{
  "product": "git-next",
  "version": %q,
  "implementation": "go",
  "acceptance_contract": "go",
  "engine": "go-git/v5 v5.19.1",
  "behavior_spec": "https://git-scm.com/docs",
  "native_git_linked": false,
  "ssh": "embedded (no external ssh fallback)",
  "os": %q,
  "arch": %q,
  "complete_git_parity": false,
  "external_git_fallback": false,
  "external_ssh_fallback": false
}
`, Version, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	fmt.Printf("git-next version %s (experimental)\n", Version)
	if p.Has("build-options") {
		fmt.Printf("implementation: go\nengine: go-git/v5 v5.19.1\nbehavior spec: https://git-scm.com/docs\nnative Git linked: false\nssh: embedded\ncpu: %s\nos: %s\nexternal Git fallback: disabled\nexternal OpenSSH fallback: disabled\ncomplete Git parity: false\n", runtime.GOARCH, runtime.GOOS)
	}
	return nil
}

func (c *Context) cmdDoctor(p *cli.Parsed) error {
	if p.Has("json") {
		exe, _ := os.Executable()
		fmt.Printf(`{
  "product": "git-next",
  "version": %q,
  "executable": %q,
  "replacement_ready": false,
  "implementation": "go",
  "blockers": [
    "Complete push refspec/hook/output parity is not accepted",
    "Full merge/recovery semantics is not accepted",
    "SHA-256 object format is not supported"
  ],
  "external_git_fallback": false,
  "external_ssh_fallback": false
}
`, Version, exe)
		return nil
	}
	buildOptions := cli.Empty()
	buildOptions.BoolValue("build-options", true)
	if err := c.cmdVersion(buildOptions); err != nil {
		return err
	}
	fmt.Println("default branch: main")
	fmt.Println("system Git replacement: blocked")
	return nil
}

func (c *Context) cmdInstall(p *cli.Parsed) error {
	args := p.Positional()
	if len(args) == 0 || args[0] == "verify" {
		if p.Has("json") {
			fmt.Println(`{"product":"git-next","verified":false,"reason":"installation verification is not implemented in the Go client"}`)
			return nil
		}
		fmt.Println("git-next install verify is not implemented in the Go client")
		return fmt.Errorf("installation verification is not implemented")
	}
	return fmt.Errorf("unknown install action %q", args[0])
}
