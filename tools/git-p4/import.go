// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type p4Settings struct {
	paths   []string
	change  int64
	options string
}

func settingsFromLog(log string) (p4Settings, error) {
	values := map[string]string{}
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[git-p4: ") || !strings.HasSuffix(line, "]") {
			continue
		}
		for _, assignment := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "[git-p4: "), "]"), ":") {
			key, value, ok := strings.Cut(assignment, "=")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
				value = value[1 : len(value)-1]
			}
			values[strings.TrimSpace(key)] = value
		}
	}
	paths := values["depot-paths"]
	if paths == "" {
		paths = values["depot-path"]
	}
	change, e := strconv.ParseInt(values["change"], 10, 64)
	if paths == "" || e != nil || change < 1 {
		return p4Settings{}, fmt.Errorf("cannot find valid git-p4 upstream metadata")
	}
	return p4Settings{strings.Split(paths, ","), change, values["options"]}, nil
}
func (d *driver) upstream(ref string) (string, p4Settings, error) {
	if ref != "" {
		log, e := d.gitText("log", "-1", "--format=%B", ref)
		if e != nil {
			return "", p4Settings{}, e
		}
		settings, e := settingsFromLog(log)
		return ref, settings, e
	}
	refs, e := d.gitText("for-each-ref", "--format=%(refname)", "refs/remotes/p4", "refs/heads/p4")
	if e != nil {
		return "", p4Settings{}, e
	}
	byPaths := map[string]string{}
	byChange := map[string]string{}
	for _, ref := range strings.Split(refs, "\n") {
		if ref == "" || strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		log, e := d.gitText("log", "-1", "--format=%B", ref)
		if e != nil {
			continue
		}
		settings, e := settingsFromLog(log)
		if e == nil {
			key := strings.Join(settings.paths, ",")
			byPaths[key] = ref
			byChange[key+";"+strconv.FormatInt(settings.change, 10)] = ref
		}
	}
	history, e := d.gitText("log", "--first-parent", "--max-count=65535", "--format=%H", "HEAD")
	if e != nil {
		return "", p4Settings{}, e
	}
	for _, commit := range strings.Split(history, "\n") {
		log, e := d.gitText("log", "-1", "--format=%B", commit)
		if e != nil {
			return "", p4Settings{}, e
		}
		settings, e := settingsFromLog(log)
		if e != nil {
			continue
		}
		key := strings.Join(settings.paths, ",")
		if ref := byChange[key+";"+strconv.FormatInt(settings.change, 10)]; ref != "" {
			return ref, settings, nil
		}
		if ref := byPaths[key]; ref != "" {
			return ref, settings, nil
		}
	}
	return "", p4Settings{}, fmt.Errorf("cannot find imported Perforce upstream branch")
}
func (d *driver) branches() error {
	if e := d.copyOriginBranches("refs/remotes/p4/"); e != nil {
		return e
	}
	refs, e := d.gitText("for-each-ref", "--format=%(refname)", "refs/remotes/p4")
	if e != nil {
		return e
	}
	for _, ref := range strings.Split(refs, "\n") {
		if ref == "" || strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		log, e := d.gitText("log", "-1", "--format=%B", ref)
		if e != nil {
			return e
		}
		settings, e := settingsFromLog(log)
		if e != nil {
			return e
		}
		fmt.Fprintf(d.out, "%s <= %s (%d)\n", strings.TrimPrefix(ref, "refs/remotes/"), strings.Join(settings.paths, ","), settings.change)
	}
	return nil
}
func normalizeDepot(value string) (string, string, error) {
	if !strings.HasPrefix(value, "//") {
		return "", "", fmt.Errorf("Depot paths must start with //: %s", value)
	}
	base, revision := value, ""
	if i := strings.IndexAny(base, "@#"); i >= 0 {
		base, revision = base[:i], base[i:]
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, "..."), "/") + "/"
	if strings.Contains(base, "\x00") || strings.Contains(base, "\n") {
		return "", "", fmt.Errorf("unsafe depot path")
	}
	return base, revision, nil
}
func branchRef(o cliOptions) string {
	branch := o.value("branch")
	if strings.HasPrefix(branch, "refs/") {
		return branch
	}
	if branch == "" {
		branch = "master"
	}
	if !strings.HasPrefix(branch, "p4/") {
		branch = "p4/" + branch
	}
	if o.flags["import-local"] {
		return "refs/heads/" + branch
	}
	if !strings.HasPrefix(branch, "p4/") {
		branch = "p4/" + branch
	}
	return "refs/remotes/" + branch
}
func (d *driver) sync(o cliOptions) error {
	d.clientPaths, d.clientKnown = nil, nil
	if o.command == "clone" {
		if len(o.args) == 0 {
			return fmt.Errorf("clone requires depot path")
		}
		destination := o.value("destination")
		if len(o.args) > 1 && !strings.HasPrefix(o.args[len(o.args)-1], "//") {
			if destination != "" {
				return fmt.Errorf("duplicate clone destination")
			}
			destination = o.args[len(o.args)-1]
			o.args = o.args[:len(o.args)-1]
		}
		if o.flags["keep-path"] && destination == "" {
			return fmt.Errorf("--keep-path requires explicit destination")
		}
		if destination == "" {
			base, _, e := normalizeDepot(o.args[0])
			if e != nil {
				return e
			}
			destination = path.Base(strings.TrimSuffix(base, "/"))
		}
		destination, e := filepath.Abs(destination)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(destination, 0755); e != nil {
			return e
		}
		d.cwd = destination
		args := []string{"init"}
		if o.flags["bare"] {
			args = append(args, "--bare")
		}
		if _, e = d.gitCall(nil, nil, args...); e != nil {
			return e
		}
		d.config = map[string][]string{}
	}
	if o.command != "clone" {
		if e := d.fetchOrigin(); e != nil {
			return e
		}
		prefix := "refs/remotes/p4/"
		if o.flags["import-local"] {
			prefix = "refs/heads/p4/"
		}
		if e := d.copyOriginBranches(prefix); e != nil {
			return e
		}
		if handled, e := d.autoBranches(o); handled || e != nil {
			return e
		}
	}
	ref := branchRef(o)
	var roots []string
	revision := ""
	last := int64(0)
	newBranch := len(o.args) > 0
	if newBranch {
		for _, value := range o.args {
			base, rev, e := normalizeDepot(value)
			if e != nil {
				return e
			}
			if revision != "" && rev != revision {
				return fmt.Errorf("depot paths must use the same revision range")
			}
			roots = append(roots, base)
			revision = rev
		}
	} else {
		_, settings, e := d.upstream(ref)
		if e != nil {
			return fmt.Errorf("Error: no branch %s. Perhaps you never did git p4 clone? %w", ref, e)
		}
		roots, last = settings.paths, settings.change
		if strings.Contains(settings.options, "keepRepoPath") {
			o.flags["keep-path"] = true
		}
		revision = fmt.Sprintf("@%d,#head", last+1)
	}
	if o.flags["detect-branches"] {
		if e := d.syncBranches(roots, revision, o); e != nil {
			return e
		}
		return d.persistCloneSettings(o)
	}
	if d.cfgBool("git-p4.useclientspec") {
		o.flags["use-client-spec"] = true
	}
	if e := d.prepareLargeFiles(ref); e != nil {
		return e
	}
	if _, e := d.gitCall(nil, nil, "check-ref-format", ref); e != nil {
		return e
	}
	if e := d.checkAccess(); e != nil {
		return e
	}
	var changes []int64
	snapshot := newBranch && (revision == "" || revision != "@all" && !strings.Contains(revision, ","))
	if o.value("changesfile") != "" {
		if newBranch && revision != "" {
			return fmt.Errorf("--changesfile is incompatible with revision specifiers")
		}
		b, e := os.ReadFile(o.value("changesfile"))
		if e != nil {
			return e
		}
		seen := map[int64]bool{}
		for _, value := range strings.Fields(string(b)) {
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || n < 1 {
				return fmt.Errorf("invalid changesfile entry")
			}
			seen[n] = true
		}
		for n := range seen {
			changes = append(changes, n)
		}
		snapshot = false
	} else if !snapshot {
		var e error
		changes, e = d.changesForPaths(roots, revision, last, o.value("changes-block-size"))
		if e != nil {
			return e
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i] < changes[j] })
	if o.value("max-changes") != "" {
		n, e := strconv.Atoi(o.value("max-changes"))
		if e != nil || n < 0 {
			return fmt.Errorf("invalid --max-changes")
		}
		if len(changes) > n {
			changes = changes[:n]
		}
	}
	spool, e := os.CreateTemp("", "git-p4-import-")
	if e != nil {
		return e
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	var imported int
	if snapshot {
		if revision == "" {
			revision = "#head"
		}
		files := []p4File{}
		head := int64(0)
		for _, root := range roots {
			records, e := d.p4List("files", root+"..."+revision)
			if e != nil {
				return e
			}
			for _, item := range records {
				if text(item["action"]) == "delete" || text(item["action"]) == "move/delete" || text(item["action"]) == "purge" {
					continue
				}
				change := integer(item["change"])
				if change > head {
					head = change
				}
				files = append(files, p4File{text(item["depotFile"]), text(item["rev"]), text(item["type"]), "add"})
			}
		}
		if head > 0 {
			details, e := d.p4Single("describe", "-s", strconv.FormatInt(head, 10))
			if e != nil {
				return e
			}
			details = dictionary{"user": "git perforce import user", "desc": "Initial import of " + strings.Join(roots, " ") + " from the state at revision " + revision + "\n", "change": head, "time": details["time"]}
			if e = d.importCommit(spool, ref, roots, details, files, o, "", true, false); e != nil {
				return e
			}
			imported++
		} else {
			return fmt.Errorf("no files found under requested depot path/revision")
		}
	} else {
		first := true
		for _, change := range changes {
			details, e := d.p4Single("describe", "-s", strconv.FormatInt(change, 10))
			if e != nil {
				return e
			}
			files := describeFiles(details)
			parent := ""
			reset := newBranch && first
			if first && newBranch && o.value("_initial-parent") != "" {
				parent, reset = o.value("_initial-parent"), false
			}
			if first && !newBranch {
				parent, e = d.gitText("rev-parse", ref)
				if e != nil {
					return e
				}
			}
			count, e := d.wantedCount(files, roots, o)
			if e != nil {
				return e
			}
			if count == 0 && !d.cfgBool("git-p4.keepEmptyCommits") {
				continue
			}
			if e = d.importCommit(spool, ref, roots, details, files, o, parent, reset, false); e != nil {
				return e
			}
			first = false
			imported++
		}
	}
	if _, e = spool.WriteString("done\n"); e != nil {
		return e
	}
	if _, e = spool.Seek(0, 0); e != nil {
		return e
	}
	if imported > 0 {
		if e = d.fastImport(spool); e != nil {
			return e
		}
	}
	if !o.flags["import-local"] {
		if _, e := d.gitText("rev-parse", "--verify", ref); e == nil {
			if _, e := d.gitText("rev-parse", "--verify", "refs/remotes/p4/HEAD"); e != nil {
				if _, e = d.gitCall(nil, nil, "symbolic-ref", "refs/remotes/p4/HEAD", ref); e != nil {
					return e
				}
			}
		}
	}
	if o.flags["detect-labels"] || o.flags["import-labels"] || d.cfgBool("git-p4.importLabels") {
		if e = d.importLabels(roots, ref, o.flags["detect-labels"]); e != nil {
			return e
		}
	}
	if o.command == "clone" && imported > 0 {
		head, e := d.gitText("symbolic-ref", "HEAD")
		if e != nil {
			return e
		}
		if _, e = d.gitCall(nil, nil, "update-ref", head, ref); e != nil {
			return e
		}
		if !o.flags["bare"] {
			if _, e = d.gitCall(nil, nil, "checkout", "-f"); e != nil {
				return e
			}
		}
		if e := d.persistCloneSettings(o); e != nil {
			return e
		}
	}
	if !o.flags["silent"] {
		if imported == 0 {
			fmt.Fprintln(d.out, "No changes to import!")
		}
		fmt.Fprintf(d.out, "Imported %d Perforce changelist(s) into %s\n", imported, ref)
	}
	return nil
}

func (d *driver) persistCloneSettings(o cliOptions) error {
	if o.command != "clone" {
		return nil
	}
	if o.flags["use-client-spec"] {
		if _, e := d.gitCall(nil, nil, "config", "--bool", "git-p4.useclientspec", "true"); e != nil {
			return e
		}
		d.config["git-p4.useclientspec"] = []string{"true"}
	}
	for _, key := range []string{"metadataDecodingStrategy", "metadataFallbackEncoding", "pathEncoding"} {
		if value := d.cfg("git-p4." + key); value != "" {
			if _, e := d.gitCall(nil, nil, "config", "git-p4."+key, value); e != nil {
				return e
			}
		}
	}
	return nil
}

type p4File struct{ depot, revision, kind, action string }

func (d *driver) shadowedFiles(files []p4File, roots []string, change string) ([]p4File, error) {
	queries := map[string]bool{}
	underRoot := func(name string) bool {
		for _, root := range roots {
			if strings.HasPrefix(name, root) {
				return true
			}
		}
		return false
	}
	for _, file := range files {
		if file.action != "delete" && file.action != "move/delete" && file.action != "purge" {
			continue
		}
		queries[file.depot+"/..."] = true
		for parent := file.depot; strings.LastIndex(parent, "/") > 1; {
			parent = parent[:strings.LastIndex(parent, "/")]
			if underRoot(parent) {
				queries[parent] = true
			}
			if parent == "//" || parent == "/" {
				break
			}
		}
	}
	var added []p4File
	seen := map[string]bool{}
	for query := range queries {
		if !underRoot(query) {
			continue
		}
		records, e := d.p4List("files", query+"@"+change)
		if e != nil {
			message := strings.ToLower(e.Error())
			if strings.Contains(message, "no such file") || strings.Contains(message, "no file(s)") {
				continue
			}
			return nil, e
		}
		for _, item := range records {
			name, action := text(item["depotFile"]), text(item["action"])
			if name == "" || action == "delete" || action == "move/delete" || action == "purge" || seen[name] {
				continue
			}
			seen[name] = true
			added = append(added, p4File{name, text(item["rev"]), text(item["type"]), "add"})
		}
	}
	return append(files, added...), nil
}

func (d *driver) clientKey(depot string) string {
	if d.cfgBool("core.ignorecase") {
		return strings.ToLower(depot)
	}
	return depot
}
func (d *driver) prepareClientMap(files []p4File, o cliOptions) error {
	if !o.flags["use-client-spec"] {
		return nil
	}
	if d.clientPaths == nil {
		d.clientPaths = map[string]string{}
		d.clientKnown = map[string]bool{}
	}
	var pending []string
	for _, file := range files {
		key := d.clientKey(file.depot)
		if !d.clientKnown[key] {
			pending = append(pending, file.depot)
		}
	}
	for _, depot := range pending {
		d.clientKnown[d.clientKey(depot)] = true
	}
	for len(pending) > 0 {
		count, total := 0, 0
		for count < len(pending) && total+len(pending[count]) < 12000 {
			total += len(pending[count]) + 1
			count++
		}
		if count == 0 {
			count = 1
		}
		var records []dictionary
		e := d.p4EachBytes([]string{"-x", "-", "where"}, []byte(strings.Join(pending[:count], "\n")+"\n"), func(item dictionary) error {
			records = append(records, item)
			return nil
		})
		if e != nil && !strings.Contains(strings.ToLower(e.Error()), "not in client view") {
			return e
		}
		for _, item := range records {
			depot, client := text(item["depotFile"]), text(item["clientFile"])
			if depot == "" || item["unmap"] != nil || strings.HasPrefix(depot, "-") || !strings.HasPrefix(client, "//") {
				continue
			}
			_, suffix, ok := strings.Cut(strings.TrimPrefix(client, "//"), "/")
			if ok {
				d.clientPaths[d.clientKey(depot)] = suffix
			}
		}
		pending = pending[count:]
	}
	return nil
}

func describeFiles(item dictionary) []p4File {
	var files []p4File
	for i := 0; ; i++ {
		suffix := strconv.Itoa(i)
		depot := text(item["depotFile"+suffix])
		if depot == "" {
			break
		}
		files = append(files, p4File{depot, text(item["rev"+suffix]), text(item["type"+suffix]), text(item["action"+suffix])})
	}
	return files
}
func (d *driver) relativeFile(depot string, roots []string, o cliOptions) (string, bool, error) {
	wanted := false
	relative := ""
	for _, root := range roots {
		if strings.HasPrefix(depot, root) || d.cfgBool("core.ignorecase") && len(depot) >= len(root) && strings.EqualFold(depot[:len(root)], root) {
			wanted = true
			relative = depot[len(root):]
			break
		}
	}
	if !wanted {
		return "", false, nil
	}
	for _, excluded := range o.values["/"] {
		excluded = strings.TrimPrefix(excluded, "//")
		target := strings.TrimPrefix(depot, "//")
		if target == excluded || strings.HasSuffix(excluded, "...") && strings.HasPrefix(target, strings.TrimSuffix(excluded, "...")) {
			return "", false, nil
		}
	}
	if o.flags["keep-path"] {
		relative = strings.TrimPrefix(depot, "//")
		_, relative, _ = strings.Cut(relative, "/")
	}
	if o.flags["use-client-spec"] {
		if e := d.prepareClientMap([]p4File{{depot: depot}}, o); e != nil {
			return "", false, e
		}
		mapped, found := d.clientPaths[d.clientKey(depot)]
		if !found {
			return "", false, nil
		}
		relative = mapped
		for _, branch := range o.values["_client-branch-prefixes"] {
			prefix := branch + "/"
			if strings.HasPrefix(relative, prefix) || d.cfgBool("core.ignorecase") && len(relative) >= len(prefix) && strings.EqualFold(relative[:len(prefix)], prefix) {
				relative = relative[len(prefix):]
				break
			}
		}
	}
	for _, pair := range [][2]string{{"%40", "@"}, {"%23", "#"}, {"%2A", "*"}, {"%25", "%"}} {
		relative = strings.ReplaceAll(relative, pair[0], pair[1])
	}
	encoding := d.cfg("git-p4.pathEncoding")
	if encoding == "" {
		encoding = "utf-8"
	}
	decoded, e := decodeEncoding(relative, encoding, true)
	if e != nil {
		return "", false, e
	}
	relative = decoded
	if relative == "" || strings.HasPrefix(relative, "/") || strings.ContainsAny(relative, "\x00\r\n") || strings.Contains(relative, "\\") {
		return "", false, fmt.Errorf("unsafe imported file path")
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return "", false, fmt.Errorf("unsafe imported path component")
		}
	}
	return relative, true, nil
}
func (d *driver) wantedCount(files []p4File, roots []string, o cliOptions) (int, error) {
	if e := d.prepareClientMap(files, o); e != nil {
		return 0, e
	}
	n := 0
	for _, file := range files {
		_, wanted, e := d.relativeFile(file.depot, roots, o)
		if e != nil {
			return 0, e
		}
		if wanted {
			n++
		}
	}
	return n, nil
}
func quoteGitPath(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range []byte(value) {
		switch c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 32 || c >= 127 {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
func splitFileType(kind string) (string, string) {
	aliases := map[string]string{"ctext": "text+C", "cxtext": "text+Cx", "ktext": "text+k", "kxtext": "text+kx", "ltext": "text+F", "tempobj": "binary+Sw", "ubinary": "binary+F", "xtext": "text+x", "xbinary": "binary+x", "xunicode": "unicode+x", "xutf16": "utf16+x"}
	if v := aliases[kind]; v != "" {
		kind = v
	}
	base, mods, _ := strings.Cut(kind, "+")
	return base, mods
}
func (d *driver) printFile(file p4File, change string, shelved bool) ([]byte, error) {
	revision := "#" + file.revision
	if shelved {
		revision = "@=" + change
	}
	base, _ := splitFileType(file.kind)
	if base == "utf16" {
		args, env := d.p4Args([]string{"print", "-q", "-o", "-", file.depot + revision})
		b, _, e := d.process(nil, env, d.p4, args...)
		if e == nil {
			return bytes.ReplaceAll(b, []byte{13, 0, 10, 0}, []byte{10, 0}), nil
		}
		if !strings.Contains(e.Error(), "Translation of file content failed") {
			return nil, e
		}
	}
	var contents bytes.Buffer
	var failure error
	e := d.p4Each([]string{"print", "-q", file.depot + revision}, nil, func(item dictionary) error {
		if text(item["code"]) == "error" {
			failure = fmt.Errorf("p4 print: %s", d.metadata(item["data"]))
			return nil
		}
		if data, ok := item["data"].([]byte); ok {
			_, e := contents.Write(data)
			return e
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if failure != nil {
		return nil, failure
	}
	return contents.Bytes(), nil
}
func (d *driver) importCommit(w io.Writer, ref string, roots []string, details dictionary, files []p4File, o cliOptions, parent string, reset, shelved bool) error {
	var e error
	if !shelved {
		files, e = d.shadowedFiles(files, roots, text(details["change"]))
		if e != nil {
			return e
		}
	}
	if e := d.prepareClientMap(files, o); e != nil {
		return e
	}
	change := text(details["change"])
	author, e := d.author(text(details["user"]))
	if e != nil {
		return e
	}
	epoch := text(details["time"])
	if _, e := strconv.ParseInt(epoch, 10, 64); e != nil {
		return fmt.Errorf("invalid Perforce commit timestamp")
	}
	description := d.metadata(details["desc"])
	if d.metadataError != nil {
		return d.metadataError
	}
	var jobs []string
	for i := 0; ; i++ {
		job := text(details["job"+strconv.Itoa(i)])
		if job == "" {
			break
		}
		jobs = append(jobs, job)
	}
	if len(jobs) > 0 {
		description += "\nJobs: " + strings.Join(jobs, " ")
	}
	options := ""
	if o.flags["keep-path"] {
		options = "keepRepoPath"
	}
	if !o.flags["suppress-meta-comment"] {
		description += "\n[git-p4: depot-paths = \"" + strings.Join(roots, ",") + "\": change = " + change
		if options != "" {
			description += ": options = " + options
		}
		description += "]\n"
	}
	zone := time.Now().Format("-0700")
	if _, e = fmt.Fprintf(w, "commit %s\ncommitter %s %s %s\ndata %d\n%s\n", ref, author, epoch, zone, len([]byte(description)), description); e != nil {
		return e
	}
	if parent != "" {
		if _, e = fmt.Fprintf(w, "from %s\n", parent); e != nil {
			return e
		}
	}
	if reset {
		if _, e = io.WriteString(w, "deleteall\n"); e != nil {
			return e
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		deleted := func(action string) bool { return action == "delete" || action == "move/delete" || action == "purge" }
		if deleted(files[i].action) != deleted(files[j].action) {
			return deleted(files[i].action)
		}
		return false
	})
	for _, file := range files {
		relative, wanted, e := d.relativeFile(file.depot, roots, o)
		if e != nil {
			return e
		}
		if !wanted {
			continue
		}
		if file.action == "delete" || file.action == "move/delete" || file.action == "purge" {
			if _, e = fmt.Fprintf(w, "D %s\n", quoteGitPath(relative)); e != nil {
				return e
			}
			if d.largeFiles[relative] {
				delete(d.largeFiles, relative)
				if e = d.writeAttributes(w); e != nil {
					return e
				}
			}
			continue
		}
		base, mods := splitFileType(file.kind)
		if base == "apple" {
			fmt.Fprintf(d.err, "Ignoring apple filetype file %s\n", file.depot)
			continue
		}
		data, e := d.printFile(file, change, shelved)
		if e != nil {
			return fmt.Errorf("Error from p4 print: %w", e)
		}
		mode := "100644"
		if strings.Contains(mods, "x") {
			mode = "100755"
		}
		if base == "symlink" {
			mode = "120000"
			data = bytes.TrimSuffix(data, []byte{'\n'})
			if len(data) == 0 {
				continue
			}
		}
		if base == "utf8" {
			data = append([]byte{0xef, 0xbb, 0xbf}, data...)
		}
		if strings.Contains(mods, "k") && (base == "text" || base == "unicode" || base == "binary") {
			pattern := `\$(Id|Header|Author|Date|DateTime|Change|File|Revision)(:[^$\n]+)?\$`
			if strings.Contains(mods, "ko") {
				pattern = `\$(Id|Header)(:[^$\n]+)?\$`
			}
			data = regexp.MustCompile(pattern).ReplaceAll(data, []byte(`$$${1}$$`))
		}
		mode, data, e = d.largeContent(w, mode, relative, data)
		if e != nil {
			return e
		}
		if _, e = fmt.Fprintf(w, "M %s inline %s\ndata %d\n", mode, quoteGitPath(relative), len(data)); e != nil {
			return e
		}
		if _, e = w.Write(data); e != nil {
			return e
		}
		if _, e = io.WriteString(w, "\n"); e != nil {
			return e
		}
	}
	_, e = io.WriteString(w, "\n")
	return e
}
func (d *driver) fastImport(spool *os.File) error {
	ctx, cancel := d.commandContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, d.git, "fast-import", "--quiet", "--done")
	cmd.Dir = d.cwd
	cmd.Stdin = spool
	cmd.Stdout = d.out
	cmd.Stderr = d.err
	return runChild(cmd)
}
func (d *driver) rebase() error {
	if _, e := d.gitCall(nil, nil, "update-index", "--refresh"); e != nil {
		return fmt.Errorf("refresh or stash modified files before rebase")
	}
	dirty, e := d.gitText("diff-index", "HEAD", "--")
	if e != nil {
		return e
	}
	if dirty != "" {
		return fmt.Errorf("commit or stash uncommitted changes before rebase")
	}
	ref, _, e := d.upstream("")
	if e != nil {
		return e
	}
	_, e = d.gitCall(nil, nil, "rebase", ref)
	return e
}
func (d *driver) importLabels(roots []string, ref string, detect bool) error {
	args := []string{"labels"}
	for _, root := range roots {
		args = append(args, root+"...")
	}
	labels, e := d.p4List(args...)
	if e != nil {
		return e
	}
	pattern := d.cfg("git-p4.labelImportRegexp")
	if pattern == "" {
		pattern = `[a-zA-Z0-9_\-.]+$`
	}
	valid, e := regexp.Compile("^(?:" + pattern + ")")
	if e != nil {
		return e
	}
	ignored := map[string]bool{}
	for _, name := range d.cfgAll("git-p4.ignoredP4Labels") {
		ignored[name] = true
	}
	for _, item := range labels {
		name := text(item["label"])
		if name == "" || !detect && (ignored[name] || !valid.MatchString(name)) {
			continue
		}
		files := dictionary{}
		highest := int64(0)
		for _, root := range roots {
			records, e := d.p4List("files", root+"...@"+name)
			if e != nil {
				return e
			}
			for _, file := range records {
				if text(file["depotFile"]) == "" {
					continue
				}
				files[text(file["depotFile"])] = text(file["rev"])
				if n := integer(file["change"]); n > highest {
					highest = n
				}
			}
		}
		if highest == 0 {
			continue
		}
		commit, e := d.commitByChange(ref, highest)
		if e != nil {
			return e
		}
		if commit == "" {
			if _, e = d.gitCall(nil, nil, "config", "--add", "git-p4.ignoredP4Labels", name); e != nil {
				return e
			}
			continue
		}
		if detect {
			same := true
			other := dictionary{}
			for _, root := range roots {
				records, e := d.p4List("files", root+"...@"+strconv.FormatInt(highest, 10))
				if e != nil {
					return e
				}
				for _, file := range records {
					if text(file["depotFile"]) != "" {
						other[text(file["depotFile"])] = text(file["rev"])
					}
				}
			}
			if len(other) != len(files) {
				same = false
			}
			for k, v := range files {
				same = same && other[k] == v
			}
			if !same {
				continue
			}
		}
		tag := name
		if detect {
			tag = "tag_" + name
		}
		if _, e := d.gitText("rev-parse", "--verify", "refs/tags/"+tag); e == nil {
			continue
		}
		if _, e = d.gitCall(nil, nil, "check-ref-format", "refs/tags/"+tag); e != nil {
			continue
		}
		details, e := d.p4Single("label", "-o", name)
		if e != nil {
			return e
		}
		owner := text(details["Owner"])
		if owner == "" {
			info, e := d.p4Single("info")
			if e != nil {
				return e
			}
			owner = text(info["userName"])
		}
		author, e := d.author(owner)
		if e != nil {
			return e
		}
		when, e := time.ParseInLocation("2006/01/02 15:04:05", text(details["Update"]), time.Local)
		if e != nil {
			when = time.Unix(integer(item["Update"]), 0)
		}
		content := fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger %s %d %s\n\n%s", commit, tag, author, when.Unix(), when.Format("-0700"), d.metadata(details["Description"]))
		object, e := d.gitCall([]byte(content), nil, "mktag")
		if e != nil {
			return e
		}
		if _, e = d.gitCall(nil, nil, "update-ref", "refs/tags/"+tag, strings.TrimSpace(string(object))); e != nil {
			return e
		}
	}
	return nil
}
func (d *driver) commitByChange(ref string, change int64) (string, error) {
	raw, e := d.gitCall(nil, nil, "log", "--reverse", "--format=%H%x00%B%x00", ref)
	if e != nil {
		return "", e
	}
	parts := bytes.Split(raw, []byte{0})
	for i := 0; i+1 < len(parts); i += 2 {
		settings, e := settingsFromLog(string(parts[i+1]))
		if e == nil && settings.change == change {
			return strings.TrimSpace(string(parts[i])), nil
		}
	}
	return "", nil
}
func lfsPointer(data []byte) []byte {
	h := sha256.Sum256(data)
	return []byte(fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", hex.EncodeToString(h[:]), len(data)))
}
