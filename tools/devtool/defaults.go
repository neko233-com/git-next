// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type setting struct{ section, key, value string }
type defaults struct {
	entries  []setting
	sections []string
	bodies   map[string][]string
}

func readDefaults(root string) (defaults, error) {
	text, e := read(filepath.Join(root, "packaging/runtime-defaults/gitconfig"))
	if e != nil {
		return defaults{}, e
	}
	d := defaults{bodies: map[string][]string{}}
	section := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return d, fmt.Errorf("invalid defaults section")
			}
			section = strings.Trim(line, "[]")
			d.sections = append(d.sections, section)
			continue
		}
		if section == "" {
			return d, fmt.Errorf("default before section")
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			return d, fmt.Errorf("invalid default %q", line)
		}
		if seen[section+"."+key] {
			return d, fmt.Errorf("duplicate default: %s.%s", section, key)
		}
		seen[section+"."+key] = true
		d.entries = append(d.entries, setting{section, key, value})
		d.bodies[section] = append(d.bodies[section], line)
	}
	if len(d.bodies["core"]) == 0 {
		return d, fmt.Errorf("defaults lack core section")
	}
	return d, nil
}
func (d defaults) core() string { return strings.Join(d.bodies["core"], "\n") + "\n" }
func (d defaults) tail() string {
	var b strings.Builder
	for _, section := range d.sections {
		if section != "core" {
			b.WriteString("[" + section + "]\n" + strings.Join(d.bodies[section], "\n") + "\n")
		}
	}
	return b.String()
}
func (d defaults) block() string { return "[core]\n" + d.core() + d.tail() }
func (d defaults) powershell() string {
	var b strings.Builder
	for _, section := range d.sections {
		b.WriteString("[" + section + "]`n")
		for _, line := range d.bodies[section] {
			b.WriteString(strings.ReplaceAll(line, "\t", "`t") + "`n")
		}
	}
	return b.String()
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func agreement(a *app) (defaults, []check, error) {
	d, e := readDefaults(a.root)
	if e != nil {
		return d, nil, e
	}
	text, e := read(a.path("packaging/windows/Takeover.Core.psm1"))
	if e != nil {
		return d, nil, e
	}
	checks := []check{{"fragment-duplicate-keys", true, fmt.Sprintf("%d defaults", len(d.entries))}, {"migration-literal-matches-fragment", strings.Contains(text, `"`+d.powershell()+`"`), "PowerShell literal equals authoritative fragment"}, {"fragment-renders", strings.HasPrefix(d.core(), "\tautocrlf = input\n") && strings.HasPrefix(d.tail(), "[gc]"), "core and remaining sections render from one fragment"}}
	enums := map[string]bool{}
	for _, value := range []string{"histogram", "zdiff3", "-committerdate", "version:refname", "main", "input", "lf", "true", "false"} {
		enums[value] = true
	}
	var bad []string
	for _, entry := range d.entries {
		if !enums[entry.value] {
			if _, e := strconv.ParseUint(entry.value, 10, 64); e != nil {
				bad = append(bad, entry.key)
			}
		}
	}
	checks = append(checks, check{"fragment-value-shapes", len(bad) == 0, strings.Join(bad, ", ")})
	return d, checks, nil
}
func init() {
	register("runtime-defaults", "Render the authoritative runtime defaults fragment", "format", func(a *app, o options) error {
		d, e := readDefaults(a.root)
		if e != nil {
			return e
		}
		switch o.value("format", "block") {
		case "block":
			_, e = fmt.Fprint(a.out, d.block())
		case "core":
			_, e = fmt.Fprint(a.out, d.core())
		case "tail":
			_, e = fmt.Fprint(a.out, d.tail())
		case "powershell":
			_, e = fmt.Fprint(a.out, d.powershell())
		default:
			return fmt.Errorf("format must be block/core/tail/powershell")
		}
		return e
	})
	register("check-runtime-defaults", "Verify default agreement and optional isolated behavior", "git json agreement-only?", checkRuntimeDefaults)
	register("install-runtime-defaults", "Apply missing defaults with preview, elevation and readback", "git json plan? force? elevated?", installRuntimeDefaults)
}
func checkRuntimeDefaults(a *app, o options) error {
	d, checks, e := agreement(a)
	if e != nil {
		return e
	}
	if !o.yes("agreement-only") {
		more, e := defaultBehavior(a, o.value("git", "git"), d)
		if e != nil {
			return e
		}
		checks = append(checks, more...)
	}
	ok := true
	for _, c := range checks {
		if !c.OK {
			ok = false
		}
		fmt.Fprintf(a.out, "%-40s %t %s\n", c.Name, c.OK, c.Detail)
	}
	report := record{"git": o.value("git", "git"), "default_count": len(d.entries), "long_paths_enabled": longPathsPolicy(), "checks": checks, "ok": ok}
	if o.get("json") != "" {
		if e = writeJSON(o.get("json"), report); e != nil {
			return e
		}
	}
	if !ok {
		return fmt.Errorf("runtime defaults check failed")
	}
	return nil
}
func installRuntimeDefaults(a *app, o options) error {
	d, e := readDefaults(a.root)
	if e != nil {
		return e
	}
	git := o.value("git", "git")
	resolved := func(key string) (string, error) {
		r, e := a.run(git, "config", "--get", key)
		if e != nil {
			return "", e
		}
		if r.code != 0 {
			return "", nil
		}
		value := strings.TrimSpace(string(r.stdout))
		if value == "<default>" {
			value = ""
		}
		return value, nil
	}
	report := record{"git": git}
	if o.yes("elevated") {
		done, detail := writeSystemDefaults(d)
		if e = a.json(record{"mode": "elevated", "system_applied": done, "system_detail": detail}); e != nil {
			return e
		}
		if !done {
			return fmt.Errorf("system defaults could not be written")
		}
		return nil
	}
	if !o.yes("plan") && runtime.GOOS != "windows" {
		return fmt.Errorf("runtime-default installation is Windows only; POSIX defaults ship in the package")
	}
	if !o.yes("plan") {
		done := false
		detail := ""
		if isAdmin() {
			done, detail = writeSystemDefaults(d)
		} else {
			args := []string{"install-runtime-defaults", "--elevated"}
			if o.yes("force") {
				args = append(args, "--force")
			}
			asked := elevateCurrent(a.root, args)
			text, _ := read(systemDefaultsPath())
			done = asked && strings.HasSuffix(strings.TrimRight(text, "\r\n"), strings.TrimRight(d.block(), "\n"))
			if done {
				detail = "written by elevated instance"
			} else {
				detail = "system write/elevation unavailable; preserving existing personal values and filling unset global keys"
			}
		}
		report["system_applied"] = done
		report["system_detail"] = detail
		report["system"] = systemDefaultsPath()
		report["mode"] = "global"
		if done {
			report["mode"] = "system"
		}
	} else {
		report["mode"] = "plan"
		delete(report, "git")
	}
	var changes [][]string
	var kept []string
	for _, entry := range d.entries {
		key := entry.section + "." + entry.key
		current, e := resolved(key)
		if e != nil {
			return e
		}
		if current != "" && strings.EqualFold(current, entry.value) {
			kept = append(kept, key+"="+current+" (already)")
			continue
		}
		if current != "" && !o.yes("force") {
			kept = append(kept, key+"="+current+" (kept)")
			continue
		}
		changes = append(changes, []string{key, entry.value})
		if !o.yes("plan") {
			r, e := a.run(git, "config", "--global", key, entry.value)
			if e != nil {
				return e
			}
			if r.code != 0 {
				return fmt.Errorf("write global default %s: %s", key, r.stderr)
			}
		}
	}
	report["left_alone"] = kept
	if o.yes("plan") {
		report["pending"] = changes
		report["pending_count"] = len(changes)
	} else {
		report["global_applied"] = changes
		effective := record{}
		var unresolved []string
		var mismatched []record
		for _, entry := range d.entries {
			key := entry.section + "." + entry.key
			value, e := resolved(key)
			if e != nil {
				return e
			}
			effective[key] = value
			if value == "" {
				unresolved = append(unresolved, key)
			} else if !strings.EqualFold(value, entry.value) {
				mismatched = append(mismatched, record{"key": key, "shipped": entry.value, "resolved": value})
			}
		}
		report["effective"] = effective
		report["unresolved"] = unresolved
		report["differs_from_shipped"] = mismatched
		report["ok"] = len(unresolved) == 0
	}
	if o.get("json") != "" {
		if e = writeJSON(o.get("json"), report); e != nil {
			return e
		}
	}
	return a.json(report)
}
func systemDefaultsPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "git-next/config/system.gitconfig")
}
func writeSystemDefaults(d defaults) (bool, string) {
	p := systemDefaultsPath()
	data, e := os.ReadFile(p)
	if e != nil {
		return false, "migrated system configuration is absent or unreadable"
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	var head []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "[") && strings.TrimSpace(line) != "[include]" {
			break
		}
		if line != "" {
			head = append(head, line)
		}
	}
	if e = write(p+".bak", data, 0600); e != nil {
		return false, e.Error()
	}
	if e = write(p, []byte(strings.Join(head, "\n")+"\n"+d.block()), 0600); e != nil {
		return false, e.Error()
	}
	return true, "wrote authoritative block and retained .bak backup"
}
func defaultBehavior(a *app, git string, d defaults) ([]check, error) {
	root, e := os.MkdirTemp("", "gn-defaults-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	if e = os.Mkdir(home, 0700); e != nil {
		return nil, e
	}
	env := cleanEnvironment(home)
	env = append(env, "GIT_AUTHOR_NAME=Check", "GIT_COMMITTER_NAME=Check", "GIT_AUTHOR_EMAIL=check@example.invalid", "GIT_COMMITTER_EMAIL=check@example.invalid")
	for i, entry := range d.entries {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s.%s", i, entry.section, entry.key), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, entry.value))
	}
	env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(d.entries)))
	ctx, cancel := deadline()
	defer cancel()
	run := func(repo string, args ...string) (result, error) {
		return runProcess(ctx, repo, env, nil, git, args...)
	}
	must := func(repo string, args ...string) (result, error) {
		r, e := run(repo, args...)
		if e != nil {
			return r, e
		}
		if r.code != 0 {
			return r, fmt.Errorf("defaults fixture %s failed: %s", args[0], r.stderr)
		}
		return r, nil
	}
	init := func(name string) (string, error) {
		repo := filepath.Join(root, name)
		_, e := must(root, "init", repo)
		return repo, e
	}
	commit := func(repo, msg string) error {
		if _, e := must(repo, "add", "-A"); e != nil {
			return e
		}
		_, e := must(repo, "commit", "-q", "-m", msg)
		return e
	}
	var checks []check
	add := func(name string, ok bool, detail string) { checks = append(checks, check{name, ok, detail}) }
	repo, e := init("longpaths")
	if e != nil {
		return nil, e
	}
	deep := repo
	for len(filepath.Join(deep, "leaf.txt")) < 300 {
		deep = filepath.Join(deep, "a-directory-segment-that-is-long-")
	}
	if e = write(filepath.Join(deep, "leaf.txt"), []byte("deep\n"), 0644); e != nil {
		return nil, e
	}
	r, e := must(repo, "add", "-A")
	if e != nil {
		return nil, e
	}
	r, e = run(repo, "status", "--porcelain")
	if e != nil {
		return nil, e
	}
	e = commit(repo, "deep")
	add("core.longpaths", e == nil && r.code == 0 && bytesContains(r.stdout, "leaf.txt"), fmt.Sprintf("path length %d", len(filepath.Join(deep, "leaf.txt"))))
	repo, e = init("manyfiles")
	if e != nil {
		return nil, e
	}
	for i := range 200 {
		if e = write(filepath.Join(repo, fmt.Sprintf("f%03d.txt", i)), []byte("x = 1\n"), 0644); e != nil {
			return nil, e
		}
	}
	if _, e = must(repo, "add", "-A"); e != nil {
		return nil, e
	}
	index, e := os.ReadFile(filepath.Join(repo, ".git/index"))
	if e != nil {
		return nil, e
	}
	add("feature.manyFiles", len(index) >= 8 && index[7] == 4 && bytesContains(index, "UNTR"), "version 4 index and untracked-cache extension")
	r, e = run(repo, "status", "--porcelain")
	if e != nil {
		return nil, e
	}
	grep, e := run(repo, "grep", "-c", "x = 1", "--", ".")
	if e != nil {
		return nil, e
	}
	add("index.threads/grep.threads", r.code == 0 && grep.code == 0, "actual status and grep execute")
	repo, e = init("diff")
	if e != nil {
		return nil, e
	}
	var lines strings.Builder
	for i := 1; i < 40; i++ {
		fmt.Fprintf(&lines, "line %d\n", i)
	}
	original := lines.String()
	if e = write(filepath.Join(repo, "a.txt"), []byte(original), 0644); e != nil {
		return nil, e
	}
	if e = commit(repo, "base"); e != nil {
		return nil, e
	}
	if e = write(filepath.Join(repo, "a.txt"), []byte(strings.Replace(original, "line 20\n", "inserted\nline 20\n", 1)), 0644); e != nil {
		return nil, e
	}
	first, e := run(repo, "diff")
	if e != nil {
		return nil, e
	}
	second, e := run(repo, "diff", "--histogram")
	if e != nil {
		return nil, e
	}
	add("diff.algorithm", first.code == 0 && second.code == 0 && string(first.stdout) == string(second.stdout), "config output equals --histogram")
	repo, e = init("merge")
	if e != nil {
		return nil, e
	}
	if e = write(filepath.Join(repo, "c.txt"), []byte("1\n2\nbase\n4\n5\n6\n7\n"), 0644); e != nil {
		return nil, e
	}
	if e = commit(repo, "base"); e != nil {
		return nil, e
	}
	if _, e = must(repo, "checkout", "-q", "-b", "theirs"); e != nil {
		return nil, e
	}
	if e = write(filepath.Join(repo, "c.txt"), []byte("1\n2\nTHEIRS\n4\n5\n6\n7\n"), 0644); e != nil {
		return nil, e
	}
	if e = commit(repo, "theirs"); e != nil {
		return nil, e
	}
	if _, e = must(repo, "checkout", "-q", "-"); e != nil {
		return nil, e
	}
	if e = write(filepath.Join(repo, "c.txt"), []byte("1\n2\nOURS\n4\n5\n6\n7\n"), 0644); e != nil {
		return nil, e
	}
	if e = commit(repo, "ours"); e != nil {
		return nil, e
	}
	if _, e = run(repo, "merge", "theirs"); e != nil {
		return nil, e
	}
	content, e := read(filepath.Join(repo, "c.txt"))
	if e != nil {
		return nil, e
	}
	markers := true
	for _, marker := range []string{"<<<<<<<", "|||||||", "=======", ">>>>>>>"} {
		markers = markers && strings.Contains(content, marker)
	}
	add("merge.conflictStyle", markers, "zdiff3 conflict includes base markers")
	var rebases []result
	for _, enabled := range []bool{true, false} {
		repo, e = init(fmt.Sprintf("rebase-%t", enabled))
		if e != nil {
			return nil, e
		}
		if e = write(filepath.Join(repo, "base.txt"), []byte("base\n"), 0644); e != nil {
			return nil, e
		}
		if e = commit(repo, "base"); e != nil {
			return nil, e
		}
		if _, e = must(repo, "checkout", "-q", "-b", "feature"); e != nil {
			return nil, e
		}
		if e = write(filepath.Join(repo, "f.txt"), []byte("feature\n"), 0644); e != nil {
			return nil, e
		}
		if e = commit(repo, "feature"); e != nil {
			return nil, e
		}
		if _, e = must(repo, "checkout", "-q", "main"); e != nil {
			return nil, e
		}
		if e = write(filepath.Join(repo, "m.txt"), []byte("main\n"), 0644); e != nil {
			return nil, e
		}
		if e = commit(repo, "main"); e != nil {
			return nil, e
		}
		if _, e = must(repo, "checkout", "-q", "feature"); e != nil {
			return nil, e
		}
		if e = write(filepath.Join(repo, "f.txt"), []byte("dirty\n"), 0644); e != nil {
			return nil, e
		}
		args := []string{"rebase", "main"}
		if !enabled {
			args = append([]string{"-c", "rebase.autoStash=false"}, args...)
		}
		r, e = run(repo, args...)
		if e != nil {
			return nil, e
		}
		rebases = append(rebases, r)
		if enabled {
			data, e := read(filepath.Join(repo, "f.txt"))
			if e != nil {
				return nil, e
			}
			add("rebase.autoStash-restores-worktree", data == "dirty\n", "dirty tracked bytes survive rebase")
		}
	}
	add("rebase.autoStash", rebases[0].code == 0 && rebases[1].code != 0, "same dirty fixture succeeds with default and refuses with autoStash=false")
	repo, e = init("push")
	if e != nil {
		return nil, e
	}
	upstream := filepath.Join(root, "upstream.git")
	if _, e = must(root, "init", "--bare", "-q", upstream); e != nil {
		return nil, e
	}
	if e = write(filepath.Join(repo, "p.txt"), []byte("p\n"), 0644); e != nil {
		return nil, e
	}
	if e = commit(repo, "push"); e != nil {
		return nil, e
	}
	if _, e = must(repo, "remote", "add", "origin", upstream); e != nil {
		return nil, e
	}
	r, e = run(repo, "push")
	if e != nil {
		return nil, e
	}
	tracking, e := run(repo, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if e != nil {
		return nil, e
	}
	add("push.autoSetupRemote", r.code == 0 && tracking.code == 0, "bare push creates upstream")
	branch, e := must(repo, "branch", "--show-current")
	if e != nil {
		return nil, e
	}
	add("init.defaultBranch", strings.TrimSpace(string(branch.stdout)) == "main", "new repository uses main")
	repo, e = init("sort")
	if e != nil {
		return nil, e
	}
	for i, name := range []string{"alpha", "zeta"} {
		if _, e = must(repo, "checkout", "-q", "-b", name); e != nil {
			return nil, e
		}
		if e = write(filepath.Join(repo, name+".txt"), []byte(name+"\n"), 0644); e != nil {
			return nil, e
		}
		if _, e = must(repo, "add", "-A"); e != nil {
			return nil, e
		}
		when := "2001-01-01T00:00:00+0000"
		if i == 1 {
			when = "2020-01-01T00:00:00+0000"
		}
		r, e = runProcess(ctx, repo, append(append([]string{}, env...), "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when), nil, git, "commit", "-q", "-m", name)
		if e != nil {
			return nil, e
		}
		if r.code != 0 {
			return nil, fmt.Errorf("dated branch fixture failed: %s", r.stderr)
		}
	}
	branches, e := must(repo, "branch", "--list")
	if e != nil {
		return nil, e
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(branches.stdout)), "\n") {
		names = append(names, strings.TrimSpace(strings.TrimLeft(line, "* ")))
	}
	add("branch.sort", len(names) == 2 && names[0] == "zeta" && names[1] == "alpha", fmt.Sprintf("committer-date ordering: %v", names))
	for _, tag := range []string{"v1.0", "v1.2", "v1.10"} {
		if _, e = must(repo, "tag", tag); e != nil {
			return nil, e
		}
	}
	tags, e := must(repo, "tag", "--list")
	if e != nil {
		return nil, e
	}
	add("tag.sort", strings.TrimSpace(string(tags.stdout)) == "v1.0\nv1.2\nv1.10", "version ordering")
	if e = write(filepath.Join(repo, "文档.txt"), []byte("x"), 0644); e != nil {
		return nil, e
	}
	if _, e = must(repo, "add", "-A"); e != nil {
		return nil, e
	}
	status, e := must(repo, "status", "--porcelain")
	if e != nil {
		return nil, e
	}
	quoted, e := must(repo, "-c", "core.quotePath=true", "status", "--porcelain")
	if e != nil {
		return nil, e
	}
	add("core.quotePath", bytesContains(status.stdout, "文档.txt") && !bytesContains(quoted.stdout, "文档.txt"), "default preserves Unicode while explicit true escapes it")
	if runtime.GOOS == "windows" {
		before, e := gitFamily()
		if e != nil {
			return nil, e
		}
		known := map[uint32]bool{}
		for _, p := range before {
			known[p.PID] = true
		}
		for _, name := range []string{"idle-a", "idle-b", "idle-c"} {
			repo, e = init(name)
			if e != nil {
				return nil, e
			}
			if e = write(filepath.Join(repo, "i.txt"), []byte("i"), 0644); e != nil {
				return nil, e
			}
			if _, e = must(repo, "add", "-A"); e != nil {
				return nil, e
			}
			if _, e = must(repo, "status", "--porcelain"); e != nil {
				return nil, e
			}
		}
		time.Sleep(time.Second)
		after, e := gitFamily()
		if e != nil {
			return nil, e
		}
		var leftover []uint32
		for _, p := range after {
			if !known[p.PID] {
				leftover = append(leftover, p.PID)
			}
		}
		add("no-process-left-behind", len(leftover) == 0, fmt.Sprintf("new Git processes after three repositories: %v", leftover))
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks, nil
}
func bytesContains(data []byte, text string) bool { return strings.Contains(string(data), text) }
