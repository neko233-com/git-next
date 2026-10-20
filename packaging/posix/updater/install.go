// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func ensureDirectory(directory string, uid uint32) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return fault("unsafe-path")
	}
	cursor := "/"
	for _, part := range strings.Split(strings.TrimPrefix(directory, "/"), "/") {
		if part == "" {
			continue
		}
		parent, err := os.OpenRoot(cursor)
		if err != nil {
			return err
		}
		info, err := parent.Lstat(part)
		if os.IsNotExist(err) {
			err = parent.Mkdir(part, 0755)
			if err == nil {
				info, err = parent.Lstat(part)
			}
		}
		_ = parent.Close()
		if err != nil {
			return err
		}
		cursor = filepath.Join(cursor, part)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fault("unsafe-path")
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && cursor != directory && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0 {
			if err := safeTree(filepath.Dir(cursor), 0); err != nil {
				return err
			}
			continue
		}
		// Validate before creating a descendant, including all existing ancestors.
		if err := safeTree(cursor, uid); err != nil {
			if err := safeTree(cursor, 0); err != nil {
				return err
			}
		}
	}
	return safeTree(directory, uid)
}

func commandDirectory(scope string) (string, error) {
	if scope == "machine" {
		return "/usr/local/bin", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local/bin"), nil
}

func prepareLinks(c *contextState) error {
	bin, err := commandDirectory(c.receipt.Scope)
	if err != nil {
		return err
	}
	if err := ensureDirectory(bin, c.receipt.UID); err != nil {
		return err
	}
	for _, command := range []string{"git", "git-next", "gitk", "git-lfs", "git-credential-manager", "git-credential-manager-core"} {
		if _, ok := c.pkg.Files["bin/"+command]; !ok {
			continue
		}
		name := filepath.Join(bin, command)
		old, err := os.Readlink(name)
		if os.IsNotExist(err) {
			old = ""
		} else if err != nil {
			return fault("unsafe-path")
		}
		c.receipt.Links[name] = old
	}
	return nil
}

func changeLink(name, target, expected string) error {
	current, err := os.Readlink(name)
	if os.IsNotExist(err) {
		current = ""
	} else if err != nil {
		return fault("unsafe-path")
	}
	if current != expected {
		return fault("unsafe-path")
	}
	if target == "" {
		if current != "" {
			return os.Remove(name)
		}
		return nil
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(name), ".git-next-link-"+id)
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, name); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer root.Close()
	return syncDir(root)
}

func (c *contextState) applyLinks() error {
	for name, old := range c.receipt.Links {
		owned := filepath.Join(c.base, "current/bin", filepath.Base(name))
		if err := changeLink(name, owned, old); err != nil {
			return err
		}
	}
	return nil
}

func (c *contextState) restoreLinks() error {
	for name, old := range c.receipt.Links {
		owned := filepath.Join(c.base, "current/bin", filepath.Base(name))
		current, err := os.Readlink(name)
		if os.IsNotExist(err) || (err == nil && current != owned) {
			continue
		}
		if err != nil {
			return fault("unsafe-path")
		}
		if err := changeLink(name, old, owned); err != nil {
			return err
		}
	}
	return nil
}

func install(o options) (returned error) {
	if o.source == "" || (o.scope != "user" && o.scope != "machine") {
		return fault("invalid-arguments")
	}
	if o.scope == "machine" && os.Geteuid() != 0 {
		return fault("administrator-required")
	}
	if o.scope == "user" && os.Geteuid() == 0 {
		return fault("invalid-arguments")
	}
	source, err := filepath.Abs(o.source)
	if err != nil {
		return err
	}
	pkg, err := loadPackage(source, true)
	if err != nil {
		return err
	}
	if o.base == "" {
		o.base, err = defaultBase(o.scope)
		if err != nil {
			return err
		}
	}
	if !filepath.IsAbs(o.base) || filepath.Clean(o.base) != o.base || strings.ContainsAny(o.base, "\x00\r\n") {
		return fault("unsafe-path")
	}
	if err := recoverInstallation(o.base, o.scope); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(o.base, "install.json")); err == nil {
		c, err := openContext(o.base)
		if err != nil {
			return err
		}
		defer c.close()
		if c.receipt.Scope != o.scope {
			return fault("invalid-package")
		}
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
		newer, err := newerVersion(pkg.Version, c.pkg.Version)
		if err != nil || !newer {
			return fault("invalid-package")
		}
		if err := installVersion(c, source, pkg); err != nil {
			return err
		}
		return emit(o, result{Status: "installed", Version: pkg.Version, Scope: o.scope})
	} else if !os.IsNotExist(err) {
		return err
	}
	uid := uint32(os.Geteuid())
	if err := ensureDirectory(o.base, uid); err != nil {
		return err
	}
	root, err := os.OpenRoot(o.base)
	if err != nil {
		return err
	}
	statePath := filepath.Join(o.base, "state")
	if o.scope == "machine" {
		statePath = machineStatePath()
	}
	if err := ensureDirectory(statePath, uid); err != nil {
		_ = root.Close()
		return err
	}
	state, err := os.OpenRoot(statePath)
	if err != nil {
		_ = root.Close()
		return err
	}
	c := &contextState{base: o.base, root: root, state: state, statePath: statePath, pkg: pkg,
		receipt: installation{Schema: 1, Product: "git-next", Platform: pkg.Platform, Scope: o.scope, Base: o.base, State: statePath, UID: uid, Links: make(map[string]string)}}
	defer c.close()
	lock, err := c.lock("update.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := state.Lstat("owner.json"); err == nil {
		var prior installation
		if readJSON(state, "owner.json", 65536, &prior) != nil || prior.Base != o.base || prior.Scope != o.scope || prior.UID != uid {
			return fault("unsafe-path")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := atomicJSON(state, "owner.json", c.receipt, 0644); err != nil {
		return err
	}
	if err := prepareLinks(c); err != nil {
		return err
	}
	if !o.manual {
		c.receipt.Service, err = makeService(c)
		if err != nil {
			return err
		}
	}
	configuration, err := prepareConfiguration(c)
	if err != nil {
		return err
	}
	// Journal integration before changing any externally visible command or service.
	if err := atomicJSON(state, "installation.json", c.receipt, 0600); err != nil {
		return err
	}
	committed := false
	defer func() {
		if returned != nil && !committed {
			_ = c.rollbackInstallation()
		}
	}()
	if err := installVersion(c, source, pkg); err != nil {
		return err
	}
	if err := c.installConfiguration(configuration); err != nil {
		return err
	}
	if err := atomicJSON(root, "install.json", c.receipt, 0644); err != nil {
		return err
	}
	if err := c.applyLinks(); err != nil {
		return err
	}
	if err := atomicJSON(state, "policy.json", policy{Schema: 1, Enabled: !o.manual}, 0644); err != nil {
		return err
	}
	if err := c.installService(); err != nil {
		return err
	}
	if err := atomicJSON(state, "state.json", updateState{Schema: 1, Status: "idle", Version: pkg.Version}, 0644); err != nil {
		return err
	}
	if err := state.Remove("installation.json"); err != nil {
		return err
	}
	if err := syncDir(state); err != nil {
		return err
	}
	committed = true
	cleanupPending := c.commitConfiguration() != nil
	return emit(o, result{Status: "installed", Version: pkg.Version, Scope: o.scope, CleanupPending: cleanupPending})
}

func installVersion(c *contextState, source string, pkg packageManifest) error {
	if err := c.root.MkdirAll("releases", 0755); err != nil {
		return err
	}
	target := packageIdentity(pkg)
	if _, err := c.root.Lstat(target); os.IsNotExist(err) {
		id, err := randomID()
		if err != nil {
			return err
		}
		stage := "releases/.install-" + id
		defer c.root.RemoveAll(stage)
		if _, err := copyPackage(source, packagePath(c.base, stage)); err != nil {
			return err
		}
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
	return c.activate(target)
}

func recoverInstallation(base, scope string) error {
	statePath := filepath.Join(base, "state")
	if scope == "machine" {
		statePath = machineStatePath()
	}
	if _, err := os.Lstat(filepath.Join(statePath, "installation.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	uid := uint32(os.Geteuid())
	if err := safeTree(base, uid); err != nil {
		return err
	}
	if err := safeTree(statePath, uid); err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	state, err := os.OpenRoot(statePath)
	if err != nil {
		_ = root.Close()
		return err
	}
	c := &contextState{base: base, statePath: statePath, root: root, state: state}
	defer c.close()
	if err := readJSON(state, "installation.json", 65536, &c.receipt); err != nil {
		return err
	}
	if c.receipt.Schema != 1 || c.receipt.Product != "git-next" || c.receipt.Base != base || c.receipt.State != statePath || c.receipt.Scope != scope || c.receipt.UID != uid {
		return fault("unsafe-path")
	}
	lock, err := c.lock("update.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	return c.rollbackInstallation()
}

func (c *contextState) rollbackInstallation() error {
	if err := c.removeService(); err != nil {
		return err
	}
	if err := c.restoreLinks(); err != nil {
		return err
	}
	if err := c.rollbackConfiguration(); err != nil {
		return err
	}
	if value, err := c.root.Readlink("current"); err == nil {
		if !releasePath(value) {
			return fault("unsafe-path")
		}
		if err := c.root.Remove("current"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := c.root.Remove("install.json"); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, name := range []string{"activation.json", "previous.json", "installation.json"} {
		if err := c.state.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := syncDir(c.root); err != nil {
		return err
	}
	return syncDir(c.state)
}
