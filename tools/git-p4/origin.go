// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"strings"
)

func (d *driver) fetchOrigin() error {
	if d.cfg("git-p4.syncFromOrigin") == "false" {
		return nil
	}
	refs, e := d.gitText("for-each-ref", "--format=%(refname)", "refs/remotes/origin")
	if e != nil {
		return e
	}
	if refs == "" {
		return nil
	}
	_, e = d.gitCall(nil, nil, "fetch", "origin")
	return e
}
func (d *driver) copyOriginBranches(localPrefix string) error {
	refs, e := d.gitText("for-each-ref", "--format=%(refname)", "refs/remotes/origin/p4")
	if e != nil {
		return e
	}
	for _, remote := range strings.Fields(refs) {
		if strings.HasSuffix(remote, "/HEAD") {
			continue
		}
		message, e := d.gitText("log", "-1", "--format=%B", remote)
		if e != nil {
			return e
		}
		original, e := settingsFromLog(message)
		if e != nil {
			continue
		}
		local := localPrefix + strings.TrimPrefix(remote, "refs/remotes/origin/p4/")
		old, e := d.gitText("rev-parse", "--verify", local)
		update := e != nil
		if !update {
			message, e = d.gitText("log", "-1", "--format=%B", local)
			if e != nil {
				return e
			}
			settings, e := settingsFromLog(message)
			if e != nil {
				continue
			}
			update = strings.Join(settings.paths, "\x00") == strings.Join(original.paths, "\x00") && original.change > settings.change
		}
		if update {
			args := []string{"update-ref", local, remote}
			if old != "" {
				args = append(args, old)
			}
			if _, e = d.gitCall(nil, nil, args...); e != nil {
				return e
			}
		}
	}
	return nil
}
func (d *driver) autoBranches(o cliOptions) (bool, error) {
	if o.command == "clone" || len(o.args) != 0 || o.value("branch") != "" {
		return false, nil
	}
	prefix := "refs/remotes/p4/"
	if o.flags["import-local"] {
		prefix = "refs/heads/p4/"
	}
	refs, e := d.gitText("for-each-ref", "--format=%(refname)", strings.TrimSuffix(prefix, "/"))
	if e != nil {
		return false, e
	}
	var paths []string
	count := 0
	for _, ref := range strings.Fields(refs) {
		if strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		message, e := d.gitText("log", "-1", "--format=%B", ref)
		if e != nil {
			return false, e
		}
		settings, e := settingsFromLog(message)
		if e != nil {
			continue
		}
		paths = append(paths, settings.paths...)
		count++
	}
	if count < 2 {
		return false, nil
	}
	common := strings.Split(paths[0], "/")
	for _, value := range paths[1:] {
		parts := strings.Split(value, "/")
		n := 0
		for n < len(parts) && n < len(common) && parts[n] == common[n] {
			n++
		}
		common = common[:n]
	}
	root := strings.TrimSuffix(strings.Join(common, "/"), "/") + "/"
	if root == "/" || !strings.HasPrefix(root, "//") {
		return true, fmt.Errorf("imported branches do not share one Perforce depot")
	}
	o.flags["auto-detect-branches"] = true
	return true, d.syncBranches([]string{root}, "", o)
}
