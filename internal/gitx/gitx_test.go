package gitx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neko233-com/git-next/internal/cli"
)

func parse(t *testing.T, command string, args []string) *cli.Parsed {
	t.Helper()
	parsed, err := cli.ParseCommand(cli.Specs[command], args)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestInitAddCommitStatusRevParse(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	ctx := &Context{Inv: &cli.Invocation{Command: "init"}, Dir: dir}
	if err := ctx.cmdInit(parse(t, "init", []string{"-b", "main", "."})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err != nil {
		t.Fatalf("missing .git/HEAD: %v", err)
	}
	for _, name := range []string{".gitattributes", ".editorconfig"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	repo, err := ctx.Open()
	if err != nil {
		t.Fatal(err)
	}
	headFile, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	if string(headFile) != "ref: refs/heads/main\n" {
		t.Fatalf("HEAD = %q", headFile)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ctx.cmdAdd(parse(t, "add", []string{"README.md"})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.cmdCommit(parse(t, "commit", []string{"-m", "seed"})); err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if commit.Message != "seed\n" {
		t.Fatalf("commit message = %q", commit.Message)
	}
	if err := ctx.cmdStatus(parse(t, "status", []string{"--porcelain"})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.cmdRevParse(parse(t, "rev-parse", []string{"--git-dir"})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.cmdRevParse(parse(t, "rev-parse", []string{"HEAD"})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.cmdLsFiles(parse(t, "ls-files", []string{"--stage"})); err != nil {
		t.Fatal(err)
	}
}

func TestCmdInitRefusesExistingRepository(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	ctx := &Context{Inv: &cli.Invocation{Command: "init"}, Dir: dir}
	if err := ctx.cmdInit(parse(t, "init", []string{"-b", "main", "."})); err != nil {
		t.Fatal(err)
	}
	if err := ctx.cmdInit(parse(t, "init", []string{"-b", "main", "."})); err == nil {
		t.Fatal("expected init to refuse an existing repository")
	}
}

func TestHashObjectMatchesGoGit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Inv: &cli.Invocation{Command: "hash-object"}, Dir: dir}
	if err := ctx.cmdHashObject(parse(t, "hash-object", []string{path})); err != nil {
		t.Fatal(err)
	}
}
