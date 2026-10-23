// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"
)

func (d *driver) syncBranches(roots []string, revision string, o cliOptions) error {
	if len(roots) != 1 {
		return fmt.Errorf("branch detection needs one common depot root")
	}
	root := roots[0]
	mapping := map[string]string{}
	args := []string{"branches"}
	if user := d.cfg("git-p4.branchUser"); user != "" {
		args = append(args, "-u", user)
	}
	branches, e := d.p4List(args...)
	if e != nil {
		return e
	}
	for _, item := range branches {
		name := text(item["branch"])
		if name == "" {
			continue
		}
		spec, e := d.p4Single("branch", "-o", name)
		if e != nil {
			return e
		}
		for i := 0; ; i++ {
			line := text(spec["View"+strconv.Itoa(i)])
			if line == "" {
				break
			}
			parts := strings.Fields(line)
			if len(parts) != 2 || !strings.HasPrefix(parts[0], root) || !strings.HasPrefix(parts[1], root) || !strings.HasSuffix(parts[0], "/...") || !strings.HasSuffix(parts[1], "/...") {
				continue
			}
			source := strings.TrimSuffix(strings.TrimPrefix(parts[0], root), "/...")
			destination := strings.TrimSuffix(strings.TrimPrefix(parts[1], root), "/...")
			if mapping[destination] == "" {
				mapping[destination] = source
			}
			if mapping[source] == "" {
				mapping[source] = source
			}
		}
	}
	for _, value := range d.cfgAll("git-p4.branchList") {
		source, destination, ok := strings.Cut(value, ":")
		if !ok || source == "" || destination == "" {
			return fmt.Errorf("invalid git-p4.branchList")
		}
		mapping[destination] = source
		if mapping[source] == "" {
			mapping[source] = source
		}
	}
	if len(mapping) == 0 {
		if o.flags["auto-detect-branches"] {
			return nil
		}
		return fmt.Errorf("no standard Perforce branch mappings or git-p4.branchList defined")
	}
	project := path.Base(strings.TrimSuffix(root, "/")) + "/"
	refFor := func(branch string) string {
		name := project + branch
		if branch == "main" {
			name = "master"
		}
		prefix := "refs/remotes/p4/"
		if o.flags["import-local"] {
			prefix = "refs/heads/p4/"
		}
		return prefix + name
	}
	done := map[string]bool{}
	var visit func(string) error
	visit = func(branch string) error {
		if done[branch] {
			return nil
		}
		done[branch] = true
		source := mapping[branch]
		if source != branch && source != "" {
			if e := visit(source); e != nil {
				return e
			}
		}
		ref := refFor(branch)
		_, existed := d.gitText("rev-parse", "--verify", ref)
		flags := map[string]bool{}
		for k, v := range o.flags {
			flags[k] = v
		}
		flags["detect-branches"] = false
		sub := cliOptions{command: "sync", values: map[string][]string{}, flags: flags}
		for k, v := range o.values {
			if k != "destination" && k != "branch" {
				sub.values[k] = v
			}
		}
		sub.values["branch"] = []string{ref}
		if existed != nil {
			sub.args = []string{root + branch + "/" + revision}
		}
		if e := d.sync(sub); e != nil {
			return e
		}
		if existed != nil && source != branch && source != "" {
			if e := d.attachBranchParent(ref, refFor(source)); e != nil {
				return e
			}
		}
		return nil
	}
	for branch := range mapping {
		if e = visit(branch); e != nil {
			return e
		}
	}
	if o.command == "clone" {
		ref := refFor("main")
		if mapping["main"] == "" {
			for branch := range mapping {
				ref = refFor(branch)
				break
			}
		}
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
	}
	return nil
}
func (d *driver) attachBranchParent(branch, source string) error {
	commits, e := d.gitText("rev-list", "--reverse", branch)
	if e != nil {
		return e
	}
	list := strings.Fields(commits)
	if len(list) == 0 {
		return nil
	}
	log, e := d.gitText("log", "-1", "--format=%B", list[0])
	if e != nil {
		return e
	}
	settings, e := settingsFromLog(log)
	if e != nil {
		return e
	}
	tree, e := d.gitText("rev-parse", list[0]+"^{tree}")
	if e != nil {
		return e
	}
	candidates, e := d.gitText("rev-list", source)
	if e != nil {
		return e
	}
	parent := ""
	for _, candidate := range strings.Fields(candidates) {
		message, e := d.gitText("log", "-1", "--format=%B", candidate)
		if e != nil {
			return e
		}
		other, e := settingsFromLog(message)
		if e != nil || other.change > settings.change {
			continue
		}
		if parent == "" {
			parent = candidate
		}
		otherTree, e := d.gitText("rev-parse", candidate+"^{tree}")
		if e != nil {
			return e
		}
		if otherTree == tree {
			parent = candidate
			break
		}
	}
	if parent == "" {
		return nil
	}
	mapping := map[string]string{}
	for i, commit := range list {
		raw, e := d.gitCall(nil, nil, "cat-file", "commit", commit)
		if e != nil {
			return e
		}
		head, body, ok := bytes.Cut(raw, []byte("\n\n"))
		if !ok {
			return fmt.Errorf("invalid commit object")
		}
		var out bytes.Buffer
		inserted := false
		for _, line := range bytes.Split(head, []byte{'\n'}) {
			if bytes.HasPrefix(line, []byte("parent ")) {
				old := string(bytes.TrimPrefix(line, []byte("parent ")))
				if replacement := mapping[old]; replacement != "" {
					line = []byte("parent " + replacement)
				}
				inserted = true
			}
			out.Write(line)
			out.WriteByte('\n')
			if i == 0 && !inserted && bytes.HasPrefix(line, []byte("tree ")) {
				out.WriteString("parent " + parent + "\n")
				inserted = true
			}
		}
		out.WriteByte('\n')
		out.Write(body)
		object, e := d.gitCall(out.Bytes(), nil, "hash-object", "-t", "commit", "-w", "--stdin")
		if e != nil {
			return e
		}
		mapping[commit] = strings.TrimSpace(string(object))
	}
	_, e = d.gitCall(nil, nil, "update-ref", branch, mapping[list[len(list)-1]], list[len(list)-1])
	return e
}
