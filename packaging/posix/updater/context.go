// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type installation struct {
	Schema          int               `json:"schema_version"`
	Product         string            `json:"product"`
	Platform        string            `json:"platform"`
	Scope           string            `json:"install_scope"`
	Base            string            `json:"base"`
	State           string            `json:"state_root"`
	UID             uint32            `json:"uid"`
	Links           map[string]string `json:"command_links"`
	Service         serviceSpec       `json:"service"`
	Configuration   string            `json:"configuration,omitempty"`
	ConfigSHA       string            `json:"configuration_sha256,omitempty"`
	ConfigCreated   bool              `json:"configuration_created,omitempty"`
	ConfigTemporary string            `json:"configuration_temporary,omitempty"`
}

type policy struct {
	Schema  int  `json:"schema_version"`
	Enabled bool `json:"enabled"`
}

type updateState struct {
	Schema      int       `json:"schema_version"`
	Status      string    `json:"status"`
	Version     string    `json:"version,omitempty"`
	Error       string    `json:"error,omitempty"`
	LastAttempt time.Time `json:"last_attempt,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
}

type contextState struct {
	base, statePath, active string
	root, state             *os.Root
	receipt                 installation
	pkg                     packageManifest
}

func platformID() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return map[string]string{"linux": "linux", "darwin": "macos"}[runtime.GOOS] + "-" + arch
}

func defaultBase(scope string) (string, error) {
	if scope == "machine" {
		if runtime.GOOS == "darwin" {
			return filepath.Join("/Library/git-next", platformID()), nil
		}
		return filepath.Join("/opt/git-next", platformID()), nil
	}
	if scope != "user" || os.Geteuid() == 0 {
		return "", fault("invalid-arguments")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local/share")
	}
	if !filepath.IsAbs(base) {
		return "", fault("unsafe-path")
	}
	return filepath.Join(base, "git-next", platformID()), nil
}

func machineStatePath() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join("/Library/Application Support/git-next", platformID())
	}
	return filepath.Join("/var/lib/git-next", platformID())
}

func checkOwner(info os.FileInfo, uid uint32, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uid || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || info.Mode().Perm()&0022 != 0 {
		return fault("unsafe-path")
	}
	if !directory && (!info.Mode().IsRegular() || stat.Nlink != 1) {
		return fault("unsafe-path")
	}
	return nil
}

func safeTree(path string, uid uint32) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fault("unsafe-path")
	}
	leaf := path
	for {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (stat.Uid != uid && stat.Uid != 0) {
			return fault("unsafe-path")
		}
		// A root-owned sticky ancestor cannot replace this owned child.
		sticky := path != leaf && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0022 != 0 && !sticky {
			return fault("unsafe-path")
		}
		if path == leaf && stat.Uid != uid {
			return fault("unsafe-path")
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func readJSON(root *os.Root, path string, limit int64, value any) error {
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return fault("invalid-package")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || json.Unmarshal(data, value) != nil {
		return fault("invalid-package")
	}
	return nil
}

func openContext(base string) (*contextState, error) {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return nil, fault("unsafe-path")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	c := &contextState{base: base, root: root}
	valid := false
	defer func() {
		if !valid {
			c.close()
		}
	}()
	if err := readJSON(root, "install.json", 65536, &c.receipt); err != nil {
		return nil, err
	}
	r := c.receipt
	if r.Schema != 1 || r.Product != "git-next" || r.Platform != platformID() || r.Base != base || (r.Scope != "user" && r.Scope != "machine") || (r.Scope == "machine" && (r.UID != 0 || r.State != machineStatePath())) || (r.Scope == "user" && (r.UID == 0 || r.State != filepath.Join(base, "state"))) {
		return nil, fault("invalid-package")
	}
	if r.Scope == "user" && uint32(os.Geteuid()) != r.UID {
		return nil, fault("unsafe-path")
	}
	if err := safeTree(base, r.UID); err != nil {
		return nil, err
	}
	info, err := root.Lstat("install.json")
	if err != nil {
		return nil, err
	}
	if err := checkOwner(info, r.UID, false); err != nil {
		return nil, err
	}
	if err := safeTree(r.State, r.UID); err != nil {
		return nil, err
	}
	c.statePath = r.State
	c.state, err = os.OpenRoot(r.State)
	if err != nil {
		return nil, err
	}
	var owner installation
	if err := readJSON(c.state, "owner.json", 65536, &owner); err != nil || owner.Base != r.Base || owner.Scope != r.Scope || owner.UID != r.UID {
		return nil, fault("unsafe-path")
	}
	if _, err := c.state.Lstat("activation.json"); err == nil && uint32(os.Geteuid()) == r.UID {
		lock, err := c.lock("update.lock")
		if err != nil {
			return nil, err
		}
		err = c.recoverActivation()
		_ = lock.Close()
		if err != nil {
			return nil, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	c.active, err = root.Readlink("current")
	if err != nil || !releasePath(c.active) {
		return nil, fault("invalid-package")
	}
	if err := safeTree(filepath.Join(base, c.active), r.UID); err != nil {
		return nil, err
	}
	c.pkg, err = loadPackage(filepath.Join(base, c.active), false)
	if err != nil {
		return nil, err
	}
	valid = true
	return c, nil
}

func (c *contextState) close() {
	if c.state != nil {
		_ = c.state.Close()
	}
	if c.root != nil {
		_ = c.root.Close()
	}
}

func (c *contextState) writable() error {
	if c.receipt.Scope == "machine" && os.Geteuid() != 0 {
		return fault("administrator-required")
	}
	if uint32(os.Geteuid()) != c.receipt.UID {
		return fault("unsafe-path")
	}
	return nil
}

func (c *contextState) lock(name string) (*os.File, error) {
	if err := c.writable(); err != nil {
		return nil, err
	}
	file, err := c.state.OpenFile(name, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		err = checkOwner(info, c.receipt.UID, false)
	}
	if err == nil {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			err = fault("busy")
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func syncDir(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func atomicJSON(root *os.Root, name string, value any, mode os.FileMode) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := "." + filepath.Base(name) + "." + id + ".new"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = file.Chmod(mode)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	return syncDir(root)
}

func (c *contextState) enabled() (bool, error) {
	var saved policy
	err := readJSON(c.state, "policy.json", 65536, &saved)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil || saved.Schema != 1 {
		return false, fault("invalid-package")
	}
	return saved.Enabled, nil
}

func (c *contextState) readState() (updateState, error) {
	var saved updateState
	err := readJSON(c.state, "state.json", 65536, &saved)
	if os.IsNotExist(err) {
		return updateState{Schema: 1, Status: "idle"}, nil
	}
	if err != nil || saved.Schema != 1 {
		return saved, fault("invalid-package")
	}
	return saved, nil
}

func releasePath(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && parts[0] == "releases" && regexpRelease.MatchString(parts[1])
}
