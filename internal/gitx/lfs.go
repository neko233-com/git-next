package gitx

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/neko233-com/git-next/internal/cli"
	"github.com/neko233-com/git-next/internal/i18n"
)

// Pointer is the Git LFS pointer metadata embedded in tracked files.
type Pointer struct {
	OID  string
	Size int64
}

// EncodePointer renders the LFS v1 pointer with a trailing newline.
func EncodePointer(oid string, size int64) string {
	return fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, size)
}

// DecodePointer parses an LFS pointer, returning ok=false for ordinary files.
func DecodePointer(data []byte) (pointer Pointer, ok bool) {
	text := string(data)
	if !strings.HasPrefix(text, "version https://git-lfs.github.com/spec/v1\n") {
		return Pointer{}, false
	}
	for _, line := range strings.Split(text, "\n") {
		if value, found := strings.CutPrefix(line, "oid sha256:"); found {
			pointer.OID = value
		}
		if value, found := strings.CutPrefix(line, "size "); found {
			if size, err := strconv.ParseInt(value, 10, 64); err == nil {
				pointer.Size = size
			}
		}
	}
	if pointer.OID == "" || pointer.Size < 0 {
		return Pointer{}, false
	}
	return pointer, true
}

func (c *Context) cmdLfs(p *cli.Parsed) error {
	args := p.Positional()
	action := "version"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	switch action {
	case "version":
		fmt.Println("git-lfs/3.0.0 (git-next Go client; pointer support only)")
		return nil
	case "track":
		if len(args) == 0 {
			return fmt.Errorf("lfs track requires at least one pattern")
		}
		file, err := os.OpenFile(filepath.Join(c.Dir, ".gitattributes"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer file.Close()
		for _, pattern := range args {
			if _, err := fmt.Fprintf(file, "%s filter=lfs diff=lfs merge=lfs -text\n", pattern); err != nil {
				return err
			}
		}
		return nil
	case "clean":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if _, ok := DecodePointer(data); ok {
			_, err = os.Stdout.Write(data)
			return err
		}
		digest := sha256.Sum256(data)
		_, err = io.WriteString(os.Stdout, EncodePointer(hex.EncodeToString(digest[:]), int64(len(data))))
		return err
	case "smudge":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		pointer, ok := DecodePointer(data)
		if !ok {
			_, err = os.Stdout.Write(data)
			return err
		}
		repo, err := c.Open()
		if err != nil {
			_, err = os.Stdout.Write(data)
			return err
		}
		dir, err := c.gitDirectory(repo)
		if err != nil {
			return err
		}
		cached := filepath.Join(dir, "lfs", "objects", pointer.OID[:2], pointer.OID[2:4], pointer.OID)
		if content, err := os.ReadFile(cached); err == nil {
			_, err = os.Stdout.Write(content)
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	case "install":
		if !p.Has("local") {
			return fmt.Errorf("lfs install requires --local")
		}
		repo, err := c.Open()
		if err != nil {
			return err
		}
		cfg, err := repo.Config()
		if err != nil {
			return err
		}
		for _, setting := range []struct{ key, value string }{
			{"filter.lfs.clean", "git-next lfs clean"},
			{"filter.lfs.smudge", "git-next lfs smudge"},
			{"filter.lfs.process", "git-next lfs filter-process"},
			{"filter.lfs.required", "true"},
		} {
			section, subsection, option, ok := splitConfigKey(setting.key)
			if !ok {
				return fmt.Errorf("invalid lfs config key %q", setting.key)
			}
			cfg.Raw.SetOption(section, subsection, option, setting.value)
		}
		return repo.SetConfig(cfg)
	case "fetch", "push", "filter-process":
		return fmt.Errorf("lfs %s transport is not supported by the Go client", action)
	default:
		return fmt.Errorf("unknown lfs action %q", action)
	}
}

func (c *Context) cmdEol(p *cli.Parsed) error {
	repo, err := c.Open()
	if err != nil {
		return err
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	if p.Has("apply") {
		if _, err := c.Worktree(repo); err != nil {
			return err
		}
		if err := c.configureLF(repo); err != nil {
			return err
		}
		if err := createIfMissing(filepath.Join(c.Dir, ".gitattributes"), attributesContent); err != nil {
			return err
		}
		if err := createIfMissing(filepath.Join(c.Dir, ".editorconfig"), editorContent); err != nil {
			return err
		}
		cfg, err = repo.Config()
		if err != nil {
			return err
		}
		if err := c.normalizeTextFiles(); err != nil {
			return err
		}
	}
	autocrlf, eol := "", ""
	for _, section := range cfg.Raw.Sections {
		if section.IsName("core") {
			autocrlf = section.Options.Get("autocrlf")
			eol = section.Options.Get("eol")
		}
	}
	if p.Has("json") {
		fmt.Printf(`{
  "target_policy": "LF",
  "core_autocrlf": %q,
  "core_eol": %q,
  "attributes_present": %t,
  "editorconfig_present": %t,
  "index_renormalized": false
}
`, autocrlf, eol,
			fileExists(filepath.Join(c.Dir, ".gitattributes")),
			fileExists(filepath.Join(c.Dir, ".editorconfig")))
		return nil
	}
	fmt.Printf("Line endings (target LF): core.autocrlf=%s, core.eol=%s\n", quoteJSON(autocrlf), quoteJSON(eol))
	fmt.Println("Existing project rules preserved; no files staged or renormalized.")
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func quoteJSON(value string) string {
	if value == "" {
		return "null"
	}
	return strconv.Quote(value)
}

func (c *Context) normalizeTextFiles() error {
	path := filepath.Join(c.Dir, ".gitattributes")
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	patterns := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && !strings.Contains(line, "-text") {
			patterns = append(patterns, fields[0])
		}
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(c.Dir, pattern))
		if err != nil {
			return err
		}
		for _, match := range matches {
			data, err := os.ReadFile(match)
			if err != nil {
				continue
			}
			normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
			if normalized != string(data) {
				if err := os.WriteFile(match, []byte(normalized), 0o644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *Context) cmdLanguage(p *cli.Parsed) error {
	args := append([]string(nil), p.Positional()...)
	if p.Has("json") && len(args) > 0 && args[0] == "get" {
		args = append(args, "--json")
	}
	return i18n.Run(args)
}
