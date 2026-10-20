// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

type activation struct {
	Schema int    `json:"schema_version"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

type previousRelease struct {
	Schema  int    `json:"schema_version"`
	Release string `json:"release"`
}

func (c *contextState) switchCurrent(target string) error {
	if !releasePath(target) {
		return fault("invalid-package")
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	temporary := ".current." + id
	if err := c.root.Symlink(target, temporary); err != nil {
		return err
	}
	defer c.root.Remove(temporary)
	if err := c.root.Rename(temporary, "current"); err != nil {
		return err
	}
	return syncDir(c.root)
}

func (c *contextState) activate(target string) error {
	if _, err := loadPackage(packagePath(c.base, target), true); err != nil {
		return err
	}
	old, err := c.root.Readlink("current")
	if os.IsNotExist(err) {
		old, err = "", nil
	}
	if err != nil || (old != "" && !releasePath(old)) {
		return fault("invalid-package")
	}
	if old == target {
		return nil
	}
	journal := activation{Schema: 1, Old: old, New: target}
	if err := atomicJSON(c.state, "activation.json", journal, 0600); err != nil {
		return err
	}
	if err := c.switchCurrent(target); err != nil {
		return err
	}
	return c.finishActivation(journal)
}

func (c *contextState) finishActivation(journal activation) error {
	if journal.Old != "" {
		if err := atomicJSON(c.state, "previous.json", previousRelease{Schema: 1, Release: journal.Old}, 0600); err != nil {
			return err
		}
	}
	if err := c.state.Remove("activation.json"); err != nil {
		return err
	}
	return syncDir(c.state)
}

func (c *contextState) recoverActivation() error {
	var journal activation
	err := readJSON(c.state, "activation.json", 65536, &journal)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || journal.Schema != 1 || !releasePath(journal.New) || (journal.Old != "" && !releasePath(journal.Old)) {
		return fault("invalid-package")
	}
	current, err := c.root.Readlink("current")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if current == journal.New {
		if _, err := loadPackage(packagePath(c.base, journal.New), true); err == nil {
			return c.finishActivation(journal)
		}
	}
	if current != journal.Old && current != journal.New && current != "" {
		return fault("unsafe-path")
	}
	if journal.Old == "" {
		if current == journal.New {
			if err := c.root.Remove("current"); err != nil {
				return err
			}
		}
	} else {
		if _, err := loadPackage(packagePath(c.base, journal.Old), true); err != nil {
			return err
		}
		if err := c.switchCurrent(journal.Old); err != nil {
			return err
		}
	}
	if err := c.state.Remove("activation.json"); err != nil {
		return err
	}
	return syncDir(c.state)
}

func (c *contextState) refresh() error {
	current, err := c.root.Readlink("current")
	if err != nil || !releasePath(current) {
		return fault("invalid-package")
	}
	pkg, err := loadPackage(packagePath(c.base, current), false)
	if err != nil {
		return err
	}
	c.active, c.pkg = current, pkg
	return nil
}

func (c *contextState) rollback() error {
	lock, err := c.lock("update.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := c.recoverActivation(); err != nil {
		return err
	}
	var previous previousRelease
	if err := readJSON(c.state, "previous.json", 65536, &previous); err != nil || previous.Schema != 1 || !releasePath(previous.Release) {
		return fault("no-rollback")
	}
	if err := c.activate(previous.Release); err != nil {
		return err
	}
	if err := c.refresh(); err != nil {
		return err
	}
	return atomicJSON(c.state, "state.json", updateState{Schema: 1, Status: "rolled-back", Version: c.pkg.Version}, 0644)
}

func (c *contextState) update(scheduled bool) (returned error) {
	lock, err := c.lock("update.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := c.recoverActivation(); err != nil {
		return err
	}
	if err := c.refresh(); err != nil {
		return err
	}
	if scheduled {
		enabled, err := c.enabled()
		if err != nil || !enabled {
			return err
		}
	}
	saved, err := c.readState()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if scheduled && (now.Sub(saved.LastSuccess) < 20*time.Hour || now.Sub(saved.LastAttempt) < time.Hour) {
		return nil
	}
	saved.Schema, saved.Status, saved.Error, saved.LastAttempt = 1, "checking", "", now
	if err := atomicJSON(c.state, "state.json", saved, 0644); err != nil {
		return err
	}
	defer func() {
		if returned != nil {
			saved.Status, saved.Error = "failed", "operation-failed"
			var known fault
			if errors.As(returned, &known) {
				saved.Error = string(known)
			}
			_ = atomicJSON(c.state, "state.json", saved, 0644)
		}
	}()
	plan, err := officialPlan(c.pkg)
	if err != nil {
		return err
	}
	if !plan.Newer {
		saved.Status, saved.Version, saved.LastSuccess = "current", c.pkg.Version, now
		return atomicJSON(c.state, "state.json", saved, 0644)
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	work := "download-" + id
	if err := c.state.Mkdir(work, 0700); err != nil {
		return err
	}
	defer c.state.RemoveAll(work)
	archive := filepath.Join(c.statePath, work, "package.tar.gz")
	if err := download(plan, archive); err != nil {
		return err
	}
	if err := c.root.MkdirAll("releases", 0755); err != nil {
		return err
	}
	stage := "releases/.stage-" + id
	defer c.root.RemoveAll(stage)
	if err := extractPackage(archive, packagePath(c.base, stage), "git-next-"+plan.Version+"-"+c.pkg.Platform); err != nil {
		return err
	}
	pkg, err := loadPackage(packagePath(c.base, stage), true)
	if err != nil || pkg.Version != plan.Version || pkg.Commit != plan.Commit {
		return fault("invalid-package")
	}
	target := packageIdentity(pkg)
	if _, err := c.root.Lstat(target); os.IsNotExist(err) {
		if err := c.root.Rename(stage, target); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		existing, err := loadPackage(packagePath(c.base, target), true)
		if err != nil || packageIdentity(existing) != target {
			return fault("invalid-package")
		}
	}
	if err := syncDir(c.root); err != nil {
		return err
	}
	if err := c.activate(target); err != nil {
		return err
	}
	c.pkg, c.active = pkg, target
	saved.Status, saved.Version, saved.LastSuccess = "updated", pkg.Version, time.Now().UTC()
	return atomicJSON(c.state, "state.json", saved, 0644)
}
