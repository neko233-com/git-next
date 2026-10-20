// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func gitConfigQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return "\"" + value + "\""
}

func legacySystemConfig(base string) (string, error) {
	var candidates []string
	if explicit := os.Getenv("GIT_CONFIG_SYSTEM"); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", fault("unsafe-path")
		}
		candidates = append(candidates, explicit)
	}
	// Inspect paths only; never execute an old Git to discover its configuration.
	if program, err := exec.LookPath("git"); err == nil {
		if real, err := filepath.EvalSymlinks(program); err == nil && !strings.HasPrefix(real, base+"/") {
			switch {
			case strings.HasPrefix(real, "/opt/homebrew/"):
				candidates = append(candidates, "/opt/homebrew/etc/gitconfig")
			case strings.HasPrefix(real, "/usr/local/"):
				candidates = append(candidates, "/usr/local/etc/gitconfig")
			case strings.HasPrefix(real, "/usr/"):
				if runtime.GOOS == "darwin" {
					candidates = append(candidates, "/Library/Developer/CommandLineTools/usr/share/git-core/gitconfig")
				}
			default:
				candidates = append(candidates, filepath.Join(filepath.Dir(filepath.Dir(real)), "etc/gitconfig"))
			}
		}
	}
	candidates = append(candidates, "/etc/gitconfig")
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/opt/homebrew/etc/gitconfig", "/usr/local/etc/gitconfig", "/Library/Developer/CommandLineTools/usr/share/git-core/gitconfig")
	}
	for _, name := range candidates {
		info, err := os.Stat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fault("unsafe-path")
		}
		return name, nil
	}
	return "", nil
}

func configurationPath(c *contextState) (string, error) {
	if c.receipt.Scope == "machine" {
		root := "/var/lib/git-next/config"
		if runtime.GOOS == "darwin" {
			root = "/Library/Application Support/git-next/config"
		}
		return filepath.Join(root, "system.gitconfig"), nil
	}
	root := os.Getenv("GIT_NEXT_CONFIG_HOME")
	if root == "" {
		root = os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			root = filepath.Join(home, ".config")
		}
		root = filepath.Join(root, "git-next")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fault("unsafe-path")
	}
	return filepath.Join(root, "system.gitconfig"), nil
}

func prepareConfiguration(c *contextState) ([]byte, error) {
	name, err := configurationPath(c)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(name); err == nil {
		return nil, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	prior, err := legacySystemConfig(c.base)
	if err != nil {
		return nil, err
	}
	if prior != "" && c.receipt.Scope == "machine" {
		real, err := filepath.EvalSymlinks(prior)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(real)
		if err != nil {
			return nil, err
		}
		if err := checkOwner(info, 0, false); err != nil {
			return nil, err
		}
		if err := safeTree(filepath.Dir(real), 0); err != nil {
			return nil, err
		}
	}
	var data strings.Builder
	data.WriteString("# git-next system configuration bridge; original configuration stays in place.\n")
	if prior != "" {
		data.WriteString("[include]\n\tpath = " + gitConfigQuote(prior) + "\n")
	}
	data.WriteString("[include]\n\tpath = " + gitConfigQuote(filepath.Join(c.base, "current/runtime/etc/gitconfig")) + "\n")
	value := []byte(data.String())
	digest := sha256.Sum256(value)
	c.receipt.Configuration, c.receipt.ConfigSHA, c.receipt.ConfigCreated = name, hex.EncodeToString(digest[:]), true
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	c.receipt.ConfigTemporary = filepath.Join(filepath.Dir(name), ".git-next-config-"+id)
	return value, nil
}

func (c *contextState) installConfiguration(data []byte) error {
	if !c.receipt.ConfigCreated {
		return nil
	}
	name := c.receipt.Configuration
	if err := ensureDirectory(filepath.Dir(name), c.receipt.UID); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if c.receipt.Scope == "machine" {
		mode = 0644
	}
	root, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer root.Close()
	temporary := filepath.Base(c.receipt.ConfigTemporary)
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
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
	if err := root.Link(temporary, filepath.Base(name)); err != nil {
		return err
	}
	return syncDir(root)
}

func (c *contextState) rollbackConfiguration() error {
	if !c.receipt.ConfigCreated || c.receipt.Configuration == "" {
		return nil
	}
	temporary, err := os.Lstat(c.receipt.ConfigTemporary)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := os.Lstat(c.receipt.Configuration)
	if err == nil && current.Mode().IsRegular() && os.SameFile(current, temporary) {
		data, err := os.ReadFile(c.receipt.Configuration)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) == c.receipt.ConfigSHA {
			if err := os.Remove(c.receipt.Configuration); err != nil {
				return err
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(c.receipt.ConfigTemporary)
}

func (c *contextState) commitConfiguration() error {
	if !c.receipt.ConfigCreated || c.receipt.ConfigTemporary == "" {
		return nil
	}
	if err := os.Remove(c.receipt.ConfigTemporary); err != nil && !os.IsNotExist(err) {
		return err
	}
	c.receipt.ConfigTemporary = ""
	return atomicJSON(c.root, "install.json", c.receipt, 0644)
}
