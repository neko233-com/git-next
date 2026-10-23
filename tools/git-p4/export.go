// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type gitDelta struct{ oldMode, newMode, oldID, newID, status, source, destination string }

func parseRawDiff(raw []byte) ([]gitDelta, error) {
	parts := bytes.Split(raw, []byte{0})
	var result []gitDelta
	for i := 0; i < len(parts) && len(parts[i]) > 0; {
		header := strings.Fields(strings.TrimPrefix(string(parts[i]), ":"))
		if len(header) != 5 {
			return nil, fmt.Errorf("invalid Git raw diff")
		}
		i++
		if i >= len(parts) {
			return nil, fmt.Errorf("missing Git diff path")
		}
		source := string(parts[i])
		i++
		destination := source
		if strings.HasPrefix(header[4], "R") || strings.HasPrefix(header[4], "C") {
			if i >= len(parts) {
				return nil, fmt.Errorf("missing copy/rename target")
			}
			destination = string(parts[i])
			i++
		}
		for _, p := range []string{source, destination} {
			if filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\r\n") || strings.Contains(p, "\\") {
				return nil, fmt.Errorf("unsafe exported path")
			}
			for _, part := range strings.Split(p, "/") {
				if part == ".." || strings.EqualFold(part, ".git") {
					return nil, fmt.Errorf("unsafe exported path")
				}
			}
		}
		result = append(result, gitDelta{header[0], header[1], header[2], header[3], header[4], source, destination})
	}
	return result, nil
}
func wildcardEncode(s string) string {
	s = filepath.ToSlash(s)
	return strings.NewReplacer("%", "%25", "@", "%40", "#", "%23", "*", "%2A").Replace(s)
}
func (d *driver) hook(name string, args ...string) error {
	_, e := d.gitCall(nil, nil, append([]string{"hook", "run", "--ignore-missing", name, "--"}, args...)...)
	return e
}
func (d *driver) p4Raw(input []byte, args ...string) ([]byte, error) {
	args, env := d.p4Args(args)
	b, _, e := d.process(input, env, d.p4, args...)
	return b, e
}
func (d *driver) clientPath(depot string) (string, error) {
	records, e := d.p4List("where", depot+"...")
	if e != nil {
		return "", e
	}
	for _, item := range records {
		if item["unmap"] != nil || strings.HasPrefix(text(item["depotFile"]), "-") {
			continue
		}
		p := text(item["path"])
		if p != "" {
			if runtime.GOOS == "windows" {
				if strings.HasPrefix(p, "/cygdrive/") {
					p = strings.TrimPrefix(p, "/cygdrive")
				}
				if len(p) >= 3 && p[0] == '/' && p[2] == '/' && (p[1] >= 'a' && p[1] <= 'z' || p[1] >= 'A' && p[1] <= 'Z') {
					p = strings.ToUpper(p[1:2]) + ":" + p[2:]
				}
			}
			return strings.TrimSuffix(strings.TrimSuffix(p, "..."), string(filepath.Separator)), nil
		}
	}
	return "", fmt.Errorf("cannot locate Perforce checkout for %s", depot)
}
func (d *driver) submitWorkspace(depot string) (string, error) {
	if d.cfgBool("git-p4.useclientspec") {
		refs, e := d.gitText("for-each-ref", "--format=%(refname)", "refs/remotes/p4", "refs/heads/p4")
		if e != nil {
			return "", e
		}
		count := 0
		for _, ref := range strings.Fields(refs) {
			if !strings.HasSuffix(ref, "/HEAD") {
				count++
			}
		}
		if count < 2 {
			client, e := d.p4Single("client", "-o")
			if e != nil {
				return "", e
			}
			root := text(client["Root"])
			if runtime.GOOS == "windows" {
				if strings.HasPrefix(root, "/cygdrive/") {
					root = strings.TrimPrefix(root, "/cygdrive")
				}
				if len(root) >= 3 && root[0] == '/' && root[2] == '/' {
					root = strings.ToUpper(root[1:2]) + ":" + root[2:]
				}
			}
			if root == "" {
				return "", fmt.Errorf("Perforce client root missing")
			}
			return root, nil
		}
	}
	return d.clientPath(depot)
}
func (d *driver) preflightUsers(commits, paths []string) error {
	d.commitUsers = map[string]string{}
	if len(commits) == 0 {
		return nil
	}
	for _, depot := range paths {
		permissions, e := d.p4List("protects", depot+"...")
		if e != nil {
			return e
		}
		allowed := false
		for _, item := range permissions {
			allowed = allowed || text(item["perm"]) == "admin" || text(item["perm"]) == "super"
		}
		if !allowed {
			return fmt.Errorf("preserve-user requires Perforce admin or super permission before submitting")
		}
	}
	users, e := d.p4List("users")
	if e != nil {
		return e
	}
	for _, commit := range commits {
		email, e := d.gitText("show", "-s", "--format=%ae", commit)
		if e != nil {
			return e
		}
		for _, item := range users {
			if strings.EqualFold(text(item["Email"]), email) {
				d.commitUsers[commit] = text(item["User"])
				break
			}
		}
		if d.commitUsers[commit] == "" {
			if !d.cfgBool("git-p4.allowMissingP4Users") {
				return fmt.Errorf("Git author %s has no Perforce user", email)
			}
			fmt.Fprintf(d.err, "Git author %s has no Perforce user; retaining submitting user\n", email)
		}
	}
	return nil
}
func (d *driver) submit(o cliOptions) error {
	d.promptReader = nil
	d.moveAvailable = nil
	if len(o.args) > 1 {
		return fmt.Errorf("submit accepts at most one Git branch")
	}
	branch := ""
	if len(o.args) == 1 {
		branch = o.args[0]
	}
	if branch == "" {
		branch = "HEAD"
	}
	if allowed := d.cfg("git-p4.allowSubmit"); allowed != "" {
		name, e := d.gitText("symbolic-ref", "--short", branch)
		if e != nil {
			name = branch
		}
		found := false
		for _, v := range strings.Split(allowed, ",") {
			found = found || v == name
		}
		if !found {
			return fmt.Errorf("branch is not permitted by git-p4.allowSubmit")
		}
	}
	if d.cfg("git-p4.largeFileSystem") != "" {
		return fmt.Errorf("large file system not supported for git-p4 submit; remove it from config")
	}
	if e := d.checkAccess(); e != nil {
		return e
	}
	upstream, settings, e := d.upstream("")
	if e != nil {
		return e
	}
	if len(settings.paths) == 0 {
		return fmt.Errorf("imported depot path missing")
	}
	origin := o.value("origin")
	if origin == "" {
		origin = upstream
	}
	limit := origin + ".." + branch
	if commit := o.value("commit"); commit != "" {
		if strings.Contains(commit, "..") {
			limit = commit
		} else {
			limit = commit + "^.." + commit
		}
	}
	commits, e := d.gitText("rev-list", "--reverse", "--no-merges", limit)
	if e != nil {
		return e
	}
	var pending []string
	for _, v := range strings.Split(commits, "\n") {
		if v != "" {
			pending = append(pending, v)
		}
	}
	if o.flags["preserve-user"] || d.cfgBool("git-p4.preserveUser") {
		if e = d.preflightUsers(pending, settings.paths); e != nil {
			return e
		}
	}
	shelves := o.values["update-shelve"]
	if len(shelves) > 0 && len(shelves) != len(pending) {
		return fmt.Errorf("number of commits must match updated shelved changelists")
	}
	for _, v := range shelves {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < 1 {
			return fmt.Errorf("invalid shelved changelist")
		}
	}
	conflict := o.value("conflict")
	if conflict == "" {
		conflict = d.cfg("git-p4.conflict")
	}
	if conflict == "" {
		conflict = "ask"
	}
	if conflict != "ask" && conflict != "skip" && conflict != "quit" {
		return fmt.Errorf("invalid conflict behavior")
	}
	workspace, e := d.submitWorkspace(settings.paths[0])
	if e != nil {
		return e
	}
	previousP4CWD := d.p4cwd
	if info, e := os.Stat(workspace); e == nil && info.IsDir() {
		d.p4cwd = workspace
	}
	defer func() { d.p4cwd = previousP4CWD }()
	opened, e := d.p4List("opened", settings.paths[0]+"...")
	if e != nil {
		return e
	}
	for _, item := range opened {
		if text(item["code"]) == "stat" {
			return fmt.Errorf("close files already opened in Perforce before submission")
		}
	}
	if !o.flags["no-verify"] {
		if e = d.hook("p4-pre-submit"); e != nil {
			return e
		}
	}
	if o.flags["dry-run"] {
		fmt.Fprintf(d.out, "Would synchronize p4 checkout in %s\nWould apply\n", workspace)
		for _, commit := range pending {
			subject, e := d.gitText("show", "-s", "--format=%h %s", commit)
			if e != nil {
				return e
			}
			fmt.Fprintln(d.out, " ", subject)
		}
		if o.flags["export-labels"] || d.cfgBool("git-p4.exportLabels") {
			return d.exportLabels(settings.paths, true)
		}
		return nil
	}
	newWorkspace := !existsPath(workspace)
	if e = os.MkdirAll(workspace, 0755); e != nil {
		return e
	}
	d.p4cwd = workspace
	syncArgs := []string{"sync"}
	if newWorkspace {
		syncArgs = append(syncArgs, "-f")
	}
	syncArgs = append(syncArgs, settings.paths[0]+"...")
	if _, e = d.p4List(syncArgs...); e != nil {
		return e
	}
	gitdir, e := d.gitText("rev-parse", "--absolute-git-dir")
	if e != nil {
		return e
	}
	var submitted []string
	var applied []string
	var firstFailure error
	for i, commit := range pending {
		updateShelf := ""
		if len(shelves) > 0 {
			updateShelf = shelves[i]
		}
		change, e := d.submitCommit(commit, workspace, gitdir, o, updateShelf)
		if e != nil {
			if firstFailure == nil {
				firstFailure = e
			}
			response := "q"
			if conflict == "skip" {
				response = "s"
			} else if conflict == "ask" && i < len(pending)-1 {
				response, _ = d.prompt("[s]kip this commit but apply the rest, or [q]uit? ", "sq")
			}
			if response == "s" {
				fmt.Fprintf(d.err, "Skipping commit %s: %v\n", commit, e)
				continue
			}
			d.reportApplied(pending, applied, o.flags["shelve"] || len(shelves) > 0)
			return e
		}
		applied = append(applied, commit)
		if change != "" {
			submitted = append(submitted, change)
		}
		if o.flags["prepare-p4-only"] {
			return nil
		}
	}
	if firstFailure != nil {
		d.reportApplied(pending, applied, o.flags["shelve"] || len(shelves) > 0)
		return firstFailure
	}
	shelved := o.flags["shelve"] || len(shelves) > 0
	if !o.flags["disable-p4sync"] && !d.cfgBool("git-p4.disableP4Sync") {
		values := map[string][]string{}
		if o.value("branch") != "" {
			values["branch"] = []string{o.value("branch")}
		}
		if e = d.sync(cliOptions{command: "sync", values: values, flags: map[string]bool{}}); e != nil {
			return e
		}
	} else if e = d.fetchOrigin(); e != nil {
		return e
	}
	if !shelved && !o.flags["disable-p4sync"] && !d.cfgBool("git-p4.disableP4Sync") && !o.flags["disable-rebase"] && !d.cfgBool("git-p4.disableRebase") && len(submitted) > 0 {
		if e = d.rebase(); e != nil {
			return e
		}
	}
	if o.flags["export-labels"] || d.cfgBool("git-p4.exportLabels") {
		if e = d.exportLabels(settings.paths); e != nil {
			return e
		}
	}
	return nil
}
func (d *driver) reportApplied(pending, applied []string, shelved bool) {
	action := "applied"
	if shelved {
		action = "shelved"
	}
	if len(applied) == 0 {
		fmt.Fprintf(d.out, "No commits %s.\n", action)
		return
	}
	fmt.Fprintf(d.out, "%s only the commits marked with '*':\n", strings.ToUpper(action[:1])+action[1:])
	for _, commit := range pending {
		mark := " "
		for _, done := range applied {
			if done == commit {
				mark = "*"
				break
			}
		}
		subject, e := d.gitText("show", "-s", "--format=%h %s", commit)
		if e == nil {
			fmt.Fprintln(d.out, mark, subject)
		}
	}
	fmt.Fprintln(d.out, "You will have to do 'git p4 sync' and rebase.")
}
func (d *driver) canMove() bool {
	if d.moveAvailable != nil {
		return *d.moveAvailable
	}
	_, e := d.p4Raw(nil, "move", "-k", "@from", "@to")
	message := ""
	if e != nil {
		message = strings.ToLower(e.Error())
	}
	allowed := !strings.Contains(message, "disabled") && !strings.Contains(message, "invalid option") && !strings.Contains(message, "unknown command")
	d.moveAvailable = &allowed
	return allowed
}
func (d *driver) submitCommit(commit, workspace, gitdir string, o cliOptions, updateShelf string) (change string, failure error) {
	diffOpts := []string{}
	renames := d.cfg("git-p4.detectRenames")
	if o.flags["M"] || renames == "true" {
		diffOpts = append(diffOpts, "-M")
	} else if renames != "" && renames != "false" {
		diffOpts = append(diffOpts, "-M"+renames)
	}
	copies := d.cfg("git-p4.detectCopies")
	if copies == "true" {
		diffOpts = append(diffOpts, "-C")
	} else if copies != "" && copies != "false" {
		diffOpts = append(diffOpts, "-C"+copies)
	}
	if d.cfgBool("git-p4.detectCopiesHarder") {
		diffOpts = append(diffOpts, "--find-copies-harder")
	}
	rawArgs := append([]string{"diff-tree", "-r", "--raw", "-z", "--no-commit-id"}, diffOpts...)
	rawArgs = append(rawArgs, commit+"^", commit)
	raw, e := d.gitCall(nil, nil, rawArgs...)
	if e != nil {
		return "", e
	}
	deltas, e := parseRawDiff(raw)
	if e != nil {
		return "", e
	}
	patchArgs := append([]string{"diff-tree", "-r", "-p", "--binary", "--full-index", "--no-commit-id"}, diffOpts...)
	patchArgs = append(patchArgs, commit+"^", commit)
	patch, e := d.gitCall(nil, nil, patchArgs...)
	if e != nil {
		return "", e
	}
	workspaceGit := []string{"--git-dir=" + gitdir, "--work-tree=" + workspace, "-C", workspace}
	checkArgs := append(append([]string{}, workspaceGit...), "apply", "--check", "--unsafe-paths", "-")
	_, checkError := d.gitCall(patch, nil, checkArgs...)
	if checkError != nil && !d.cfgBool("git-p4.attemptRCSCleanup") {
		return "", fmt.Errorf("commit conflicts with current Perforce workspace: %w", checkError)
	}
	var owned []string
	var added []string
	var pureCopies []string
	fallbackDeletes := map[string]bool{}
	type backup struct {
		data []byte
		mode os.FileMode
	}
	backups := map[string]backup{}
	keep := false
	defer func() {
		if keep {
			return
		}
		for _, p := range owned {
			_, e := d.p4List("revert", wildcardEncode(p))
			if e != nil && failure == nil {
				failure = e
			}
		}
		for p, original := range backups {
			mode := original.mode
			if info, e := os.Stat(p); e == nil {
				mode &= info.Mode().Perm()
			}
			e := os.Chmod(p, 0644)
			if e == nil {
				e = os.WriteFile(p, original.data, 0644)
			}
			if e == nil {
				e = os.Chmod(p, mode)
			}
			if e != nil && failure == nil {
				failure = e
			}
		}
		for _, p := range added {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) && failure == nil {
				failure = e
			}
		}
	}()
	p4 := func(args ...string) error { _, e := d.p4List(args...); return e }
	for _, delta := range deltas {
		src, dst := filepath.Join(workspace, filepath.FromSlash(delta.source)), filepath.Join(workspace, filepath.FromSlash(delta.destination))
		switch delta.status[0] {
		case 'M':
			if e = p4("edit", wildcardEncode(src)); e != nil {
				return "", e
			}
			owned = append(owned, src)
		case 'T':
			owned = append(owned, src)
		case 'D':
			owned = append(owned, src)
		case 'R':
			owned = append(owned, src, dst)
			if d.canMove() {
				if e = p4("edit", wildcardEncode(src)); e != nil {
					return "", e
				}
				if e = p4("move", "-k", wildcardEncode(src), wildcardEncode(dst)); e != nil {
					return "", e
				}
			} else {
				if e = p4("integrate", "-Dt", wildcardEncode(src), wildcardEncode(dst)); e != nil {
					return "", e
				}
				if delta.oldID != delta.newID || delta.oldMode != delta.newMode {
					if e = p4("edit", wildcardEncode(dst)); e != nil {
						return "", e
					}
				} else {
					pureCopies = append(pureCopies, dst)
				}
				if e = os.Chmod(dst, 0644); e != nil {
					return "", e
				}
				if e = os.Remove(dst); e != nil {
					return "", e
				}
				fallbackDeletes[src] = true
			}
		case 'C':
			if e = p4("integrate", "-Dt", wildcardEncode(src), wildcardEncode(dst)); e != nil {
				return "", e
			}
			owned = append(owned, dst)
			if delta.oldID != delta.newID || delta.oldMode != delta.newMode {
				if e = p4("edit", wildcardEncode(dst)); e != nil {
					return "", e
				}
			} else {
				pureCopies = append(pureCopies, dst)
			}
			if existsPath(dst) {
				if e = os.Chmod(dst, 0644); e != nil {
					return "", e
				}
				if e = os.Remove(dst); e != nil {
					return "", e
				}
			}
		case 'A':
			added = append(added, dst)
		default:
			return "", fmt.Errorf("unsupported Git delta %s", delta.status)
		}
	}
	if checkError != nil {
		for _, delta := range deltas {
			if !strings.ContainsRune("MTDR", rune(delta.status[0])) || delta.oldMode == "120000" {
				continue
			}
			p := filepath.Join(workspace, filepath.FromSlash(delta.source))
			stat, e := d.p4Single("fstat", wildcardEncode(p))
			if e != nil {
				return "", e
			}
			kind := text(stat["headType"])
			_, mods := splitFileType(kind)
			if !strings.Contains(mods, "k") {
				continue
			}
			data, e := os.ReadFile(p)
			if e != nil {
				return "", e
			}
			info, e := os.Stat(p)
			if e != nil {
				return "", e
			}
			keys := "Id|Header|Author|Date|DateTime|Change|File|Revision"
			if strings.Contains(mods, "ko") {
				keys = "Id|Header"
			}
			collapsed := regexp.MustCompile(`\$(`+keys+`)(?::[^$\r\n]*)?\$`).ReplaceAll(data, []byte("$$${1}$$"))
			if bytes.Equal(data, collapsed) {
				continue
			}
			backups[p] = backup{data, info.Mode().Perm()}
			if e = os.Chmod(p, 0644); e != nil {
				return "", e
			}
			if e = os.WriteFile(p, collapsed, 0644); e != nil {
				return "", e
			}
		}
		if _, e = d.gitCall(patch, nil, checkArgs...); e != nil {
			return "", fmt.Errorf("commit conflicts after RCS keyword cleanup: %w", e)
		}
	}
	applyArgs := append(append([]string{}, workspaceGit...), "apply", "--unsafe-paths", "-")
	if _, e = d.gitCall(patch, nil, applyArgs...); e != nil {
		return "", e
	}
	for _, delta := range deltas {
		dst := filepath.Join(workspace, filepath.FromSlash(delta.destination))
		if delta.status[0] == 'T' {
			if e = p4("edit", "-t", "auto", wildcardEncode(dst)); e != nil {
				return "", e
			}
		}
		if delta.status[0] == 'R' {
			src := filepath.Join(workspace, filepath.FromSlash(delta.source))
			if fallbackDeletes[src] {
				if e = p4("delete", wildcardEncode(src)); e != nil {
					return "", e
				}
			}
		}
		if delta.status[0] == 'A' {
			if e = p4("add", "-f", filepath.ToSlash(dst)); e != nil {
				return "", e
			}
			owned = append(owned, dst)
		}
		if delta.status[0] == 'D' {
			if e = p4("delete", wildcardEncode(dst)); e != nil {
				return "", e
			}
			continue
		}
		if delta.oldMode != delta.newMode || delta.status[0] == 'A' {
			kind := ""
			if delta.newMode == "120000" {
				kind = "symlink"
			} else {
				stat, e := d.p4Single("opened", wildcardEncode(dst))
				if e == nil {
					kind = text(stat["type"])
					if kind == "" {
						kind = text(stat["headType"])
					}
				}
				if kind == "" {
					kind = "text"
				}
				base, mods := splitFileType(kind)
				mods = strings.ReplaceAll(mods, "x", "")
				if delta.newMode == "100755" {
					mods += "x"
				}
				kind = base
				if mods != "" {
					kind += "+" + mods
				}
			}
			if e = p4("reopen", "-t", kind, wildcardEncode(dst)); e != nil {
				return "", e
			}
		}
	}
	description, e := d.gitText("show", "-s", "--format=%B", commit)
	if e != nil {
		return "", e
	}
	jobs := ""
	if i := strings.Index("\n"+description, "\nJobs:"); i >= 0 {
		body := strings.TrimSpace(("\n" + description)[i+1:])
		jobs = body
		description = strings.TrimSpace(("\n" + description)[:i])
	}
	client, e := d.p4Single("client", "-o")
	if e != nil {
		return "", e
	}
	currentUser, e := d.p4Single("user", "-o")
	if e != nil {
		return "", e
	}
	form := dictionary{"Change": "new", "Client": client["Client"], "User": currentUser["User"], "Description": description}
	if updateShelf != "" {
		form["Change"] = updateShelf
	}
	var opened []dictionary
	seenOwned := map[string]bool{}
	for _, p := range owned {
		if seenOwned[p] {
			continue
		}
		seenOwned[p] = true
		items, e := d.p4List("opened", wildcardEncode(p))
		if e != nil {
			return "", e
		}
		opened = append(opened, items...)
	}
	if updateShelf != "" {
		args := []string{"reopen", "-c", updateShelf}
		for _, p := range owned {
			args = append(args, wildcardEncode(p))
		}
		if _, e = d.p4List(args...); e != nil {
			return "", e
		}
	}
	index := 0
	for _, item := range opened {
		if depot := text(item["depotFile"]); depot != "" {
			form["Files"+strconv.Itoa(index)] = depot
			index++
		}
	}
	template := formatP4Form(form)
	if jobs != "" {
		template = bytes.Replace(template, []byte("\nFiles:\n"), []byte("\n"+jobs+"\n\nFiles:\n"), 1)
	}
	if o.flags["preserve-user"] || d.cfgBool("git-p4.preserveUser") {
		template = append(template, []byte("\n######## Actual user "+d.commitUsers[commit]+", modified after commit\n")...)
	}
	if !d.cfgBool("git-p4.skipUserNameCheck") {
		email, e := d.gitText("show", "-s", "--format=%ae", commit)
		if e != nil {
			return "", e
		}
		if !strings.EqualFold(email, text(currentUser["Email"])) {
			template = append(template, []byte("######## git author "+email+" does not match your p4 account.\n######## Use option --preserve-user to modify authorship.\n######## Variable git-p4.skipUserNameCheck hides this message.\n")...)
		}
	}
	const separator = "######## everything below this line is just the diff #######\n"
	if !o.flags["prepare-p4-only"] {
		template = append(template, []byte(separator)...)
		template = append(template, patch...)
	}
	temporary, e := os.CreateTemp("", "git-p4-changelist-")
	if e != nil {
		return "", e
	}
	templatePath := temporary.Name()
	if _, e = temporary.Write(template); e != nil {
		temporary.Close()
		return "", e
	}
	if e = temporary.Close(); e != nil {
		return "", e
	}
	defer func() {
		if !o.flags["prepare-p4-only"] {
			os.Remove(templatePath)
		}
	}()
	if e = d.hook("p4-prepare-changelist", templatePath); e != nil {
		return "", e
	}
	if o.flags["prepare-p4-only"] {
		keep = true
		fmt.Fprintf(d.out, "Perforce workspace prepared for submission in %s; submit template %s\n", workspace, templatePath)
		if len(added) > 0 {
			fmt.Fprintln(d.out, "Added files must be deleted when reverting this preparation:")
			for _, p := range added {
				fmt.Fprintln(d.out, p)
			}
		}
		return "", nil
	}
	if !d.cfgBool("git-p4.skipSubmitEdit") {
		before, e := os.Stat(templatePath)
		if e != nil {
			return "", e
		}
		editor := os.Getenv("P4EDITOR")
		if editor == "" {
			editor, e = d.gitText("var", "GIT_EDITOR")
			if e != nil {
				return "", e
			}
		}
		if e = d.runEditor(editor, templatePath); e != nil {
			return "", e
		}
		if !d.cfgBool("git-p4.skipSubmitEditCheck") {
			after, e := os.Stat(templatePath)
			if e != nil {
				return "", e
			}
			if !after.ModTime().After(before.ModTime()) {
				response, e := d.prompt("Submit template unchanged. Submit anyway? [y]es, [n]o (skip this patch) ", "yn")
				if e != nil {
					return "", e
				}
				if response != "y" {
					return "", fmt.Errorf("unchanged changelist submission declined")
				}
			}
		}
	}
	if !o.flags["no-verify"] {
		if e = d.hook("p4-changelist", templatePath); e != nil {
			return "", e
		}
	}
	template, e = os.ReadFile(templatePath)
	if e != nil {
		return "", e
	}
	template = bytes.ReplaceAll(template, []byte("\r\n"), []byte("\n"))
	if before, _, found := bytes.Cut(template, []byte(separator)); found {
		template = before
	}
	if len(bytes.TrimSpace(template)) == 0 {
		return "", fmt.Errorf("empty changelist aborts submission")
	}
	shelve := o.flags["shelve"] || updateShelf != ""
	args := []string{"submit", "-i"}
	if shelve {
		args = []string{"shelve", "-i"}
		if updateShelf != "" {
			args = []string{"shelve", "-r", "-i"}
		}
	}
	result, e := d.p4Raw(template, args...)
	if e != nil {
		return "", e
	}
	matches := regexp.MustCompile(`(?i)(?:change|changelist)\s+([0-9]+)`).FindAllStringSubmatch(string(result), -1)
	if len(matches) > 0 {
		change = matches[len(matches)-1][1]
	}
	if !shelve {
		keep = true
		for _, p := range pureCopies {
			if _, e = d.p4List("sync", "-f", wildcardEncode(p)); e != nil {
				return "", e
			}
		}
	}
	if o.flags["preserve-user"] || d.cfgBool("git-p4.preserveUser") {
		if change == "" {
			return "", fmt.Errorf("submitted changelist number unavailable for user preservation")
		}
		user := d.commitUsers[commit]
		if user != "" {
			spec, e := d.p4Single("change", "-o", change)
			if e != nil {
				return "", e
			}
			delete(spec, "code")
			spec["User"] = user
			if _, e = d.p4ListInput(spec, "change", "-f", "-i"); e != nil {
				return "", e
			}
		}
	}
	if e = d.hook("p4-post-changelist"); e != nil {
		fmt.Fprintf(d.err, "p4-post-changelist hook: %v\n", e)
	}
	fmt.Fprint(d.out, string(result))
	return change, nil
}
func existsPath(p string) bool { _, e := os.Lstat(p); return e == nil }
func formatP4Form(form dictionary) []byte {
	var b strings.Builder
	for _, key := range []string{"Change", "Client", "User", "Status"} {
		if value := text(form[key]); value != "" {
			fmt.Fprintf(&b, "%s:\t%s\n", key, value)
		}
	}
	b.WriteString("\nDescription:\n")
	for _, line := range strings.Split(text(form["Description"]), "\n") {
		fmt.Fprintf(&b, "\t%s\n", line)
	}
	for _, key := range []string{"Jobs", "Files"} {
		var lines []string
		for i := 0; ; i++ {
			value := text(form[key+strconv.Itoa(i)])
			if value == "" {
				break
			}
			lines = append(lines, value)
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\n%s:\n", key)
			for _, line := range lines {
				fmt.Fprintf(&b, "\t%s\n", line)
			}
		}
	}
	b.WriteByte('\n')
	return []byte(b.String())
}
func (d *driver) runEditor(editor, file string) error {
	shell := os.Getenv("GIT_P4_SHELL")
	if shell == "" {
		for directory := filepath.Dir(d.git); ; directory = filepath.Dir(directory) {
			for _, relative := range []string{"runtime/usr/bin/sh.exe", "runtime/usr/bin/sh", "usr/bin/sh.exe", "usr/bin/sh"} {
				candidate := filepath.Join(directory, filepath.FromSlash(relative))
				if existsPath(candidate) {
					shell = candidate
					break
				}
			}
			if shell != "" || filepath.Dir(directory) == directory {
				break
			}
		}
		if shell == "" {
			return fmt.Errorf("owned command shell missing; configure GIT_P4_SHELL for a development fixture")
		}
	}
	if runtime.GOOS == "windows" && editor == "notepad" {
		_, _, e := d.process(nil, nil, "notepad.exe", file)
		return e
	}
	ctx, cancel := d.commandContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", editor+` "$1"`, "git-p4-editor", file)
	cmd.Dir = d.cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = d.out
	cmd.Stderr = d.err
	return runChild(cmd)
}
func (d *driver) prompt(message, allowed string) (string, error) {
	input := d.in
	if input == nil {
		input = os.Stdin
	}
	if d.promptReader == nil {
		d.promptReader = bufio.NewReader(input)
	}
	reader := d.promptReader
	for {
		fmt.Fprint(d.out, message)
		line, e := reader.ReadString('\n')
		line = strings.ToLower(strings.TrimSpace(line))
		if len(line) > 0 && strings.ContainsRune(allowed, rune(line[0])) {
			return line[:1], nil
		}
		if e != nil {
			return "", fmt.Errorf("interactive response required: %w", e)
		}
	}
}
func (d *driver) unshelve(o cliOptions) error {
	if len(o.args) != 1 {
		return fmt.Errorf("unshelve requires one changelist")
	}
	change, e := strconv.ParseInt(o.args[0], 10, 64)
	if e != nil || change < 1 {
		return fmt.Errorf("invalid changelist")
	}
	origin := o.value("origin")
	if origin == "" {
		origin = "HEAD"
	}
	log, e := d.gitText("log", "--format=%B", origin)
	if e != nil {
		return e
	}
	settings, e := settingsFromLog(log)
	if e != nil {
		return e
	}
	ref := "refs/remotes/p4-unshelved/" + o.args[0]
	if _, e := d.gitText("rev-parse", "--verify", ref); e == nil {
		renamed := false
		for i := 0; i < 1000; i++ {
			backup := ref + "." + strconv.Itoa(i)
			if _, e := d.gitText("rev-parse", "--verify", backup); e != nil {
				value, e := d.gitText("rev-parse", ref)
				if e != nil {
					return e
				}
				if _, e = d.gitCall(nil, nil, "update-ref", backup, value); e != nil {
					return e
				}
				if _, e = d.gitCall(nil, nil, "update-ref", "-d", ref); e != nil {
					return e
				}
				renamed = true
				break
			}
		}
		if !renamed {
			return fmt.Errorf("no free unshelve backup branch")
		}
	}
	details, e := d.p4Single("describe", "-s", "-S", o.args[0])
	if e != nil {
		return e
	}
	files := describeFiles(details)
	parentFiles := []p4File{}
	for _, file := range files {
		if file.action == "add" || file.action == "move/add" || file.action == "branch" {
			continue
		}
		if file.action == "delete" || file.action == "move/delete" {
			file.action = "add"
		}
		parentFiles = append(parentFiles, file)
	}
	spool, e := os.CreateTemp("", "git-p4-unshelve-")
	if e != nil {
		return e
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	parent := dictionary{}
	for k, v := range details {
		parent[k] = v
	}
	parent["desc"] = fmt.Sprintf("parent for shelved changelist %d\n", change)
	opts := cliOptions{values: map[string][]string{}, flags: map[string]bool{"suppress-meta-comment": true}}
	if e = d.importCommit(spool, ref, settings.paths, parent, parentFiles, opts, origin, false, false); e != nil {
		return e
	}
	if e = d.importCommit(spool, ref, settings.paths, details, files, opts, "", false, true); e != nil {
		return e
	}
	if _, e = io.WriteString(spool, "done\n"); e != nil {
		return e
	}
	if _, e = spool.Seek(0, 0); e != nil {
		return e
	}
	if e = d.fastImport(spool); e != nil {
		return e
	}
	fmt.Fprintf(d.out, "unshelved changelist %d into %s\n", change, ref)
	return nil
}
func (d *driver) exportLabels(roots []string, dryRun ...bool) error {
	labels, e := d.p4List("labels")
	if e != nil {
		return e
	}
	existing := map[string]bool{}
	for _, item := range labels {
		existing[text(item["label"])] = true
	}
	tags, e := d.gitText("tag", "--list")
	if e != nil {
		return e
	}
	pattern := d.cfg("git-p4.labelExportRegexp")
	if pattern == "" {
		pattern = `[a-zA-Z0-9_\-.]+$`
	}
	valid, e := regexp.Compile("^(?:" + pattern + ")")
	if e != nil {
		return e
	}
	for _, name := range strings.Split(tags, "\n") {
		if name == "" || existing[name] || !valid.MatchString(name) {
			continue
		}
		log, e := d.gitText("log", "-1", "--format=%B", name)
		if e != nil {
			return e
		}
		settings, e := settingsFromLog(log)
		if e != nil {
			continue
		}
		if len(dryRun) > 0 && dryRun[0] {
			fmt.Fprintf(d.out, "Would create p4 label %s\n", name)
			continue
		}
		description := "lightweight tag imported by git p4\n"
		kind, e := d.gitText("cat-file", "-t", name)
		if e != nil {
			return e
		}
		if kind == "tag" {
			body, e := d.gitText("cat-file", "-p", name)
			if e != nil {
				return e
			}
			_, description, _ = strings.Cut(body, "\n\n")
		}
		var form strings.Builder
		fmt.Fprintf(&form, "Label: %s\nDescription:\n", name)
		for _, line := range strings.Split(description, "\n") {
			fmt.Fprintf(&form, "\t%s\n", line)
		}
		form.WriteString("View:\n")
		for _, root := range roots {
			fmt.Fprintf(&form, "\t%s...\n", root)
		}
		if _, e = d.p4Raw([]byte(form.String()), "label", "-i"); e != nil {
			return e
		}
		args := []string{"tag", "-l", name}
		for _, root := range roots {
			args = append(args, root+"...@"+strconv.FormatInt(settings.change, 10))
		}
		if _, e = d.p4List(args...); e != nil {
			return e
		}
	}
	return nil
}
