// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type serviceSpec struct {
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Domain  string `json:"domain,omitempty"`
	Content string `json:"content,omitempty"`
}

func systemdToken(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "%", "%%")
	return "\"" + value + "\""
}

func xmlText(value string) string {
	var builder strings.Builder
	_ = xml.EscapeText(&builder, []byte(value))
	return builder.String()
}

func makeService(c *contextState) (serviceSpec, error) {
	hash := sha256.Sum256([]byte(c.base))
	identifier := fmt.Sprintf("%x", hash[:8])
	program := filepath.Join(c.base, "current/libexec/git-next/updater")
	if runtime.GOOS == "linux" {
		folder := "/etc/systemd/system"
		if c.receipt.Scope == "user" {
			config := os.Getenv("XDG_CONFIG_HOME")
			if config == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return serviceSpec{}, err
				}
				config = filepath.Join(home, ".config")
			}
			if !filepath.IsAbs(config) {
				return serviceSpec{}, fault("unsafe-path")
			}
			folder = filepath.Join(config, "systemd/user")
		}
		name := "git-next-updater-" + identifier + ".service"
		content := "# Copyright 2026 git-next contributors. MIT license.\n[Unit]\nDescription=git-next automatic updater\nAfter=network-online.target\n\n[Service]\nType=simple\nExecStart=" + systemdToken(program) + " Serve --base " + systemdToken(c.base) + "\nRestart=on-failure\nRestartSec=30\nNoNewPrivileges=yes\nUMask=0022\n\n[Install]\nWantedBy=default.target\n"
		if c.receipt.Scope == "machine" {
			content = strings.Replace(content, "WantedBy=default.target", "WantedBy=multi-user.target", 1)
		}
		return serviceSpec{Path: filepath.Join(folder, name), Name: name, Content: content}, nil
	}
	folder, domain := "/Library/LaunchDaemons", "system"
	if c.receipt.Scope == "user" {
		home, err := os.UserHomeDir()
		if err != nil {
			return serviceSpec{}, err
		}
		folder, domain = filepath.Join(home, "Library/LaunchAgents"), "gui/"+strconv.FormatUint(uint64(c.receipt.UID), 10)
	}
	name := "com.git-next.updater." + identifier
	content := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>" + name + "</string><key>ProgramArguments</key><array><string>" + xmlText(program) + "</string><string>Serve</string><string>--base</string><string>" + xmlText(c.base) + "</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>30</integer><key>Umask</key><integer>18</integer></dict></plist>\n"
	return serviceSpec{Path: filepath.Join(folder, name+".plist"), Name: name, Domain: domain, Content: content}, nil
}

func (c *contextState) serviceCommand(action string) error {
	spec := c.receipt.Service
	if spec.Path == "" {
		return nil
	}
	var program string
	var args []string
	if runtime.GOOS == "linux" {
		program = "/usr/bin/systemctl"
		if c.receipt.Scope == "user" {
			args = append(args, "--user")
		}
		switch action {
		case "reload":
			args = append(args, "daemon-reload")
		case "start":
			args = append(args, "enable", "--now", spec.Name)
		case "stop":
			args = append(args, "disable", "--now", spec.Name)
		}
	} else {
		program = "/bin/launchctl"
		switch action {
		case "reload":
			return nil
		case "start":
			args = []string{"bootstrap", spec.Domain, spec.Path}
		case "stop":
			args = []string{"bootout", spec.Domain + "/" + spec.Name}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Env = c.serviceEnvironment()
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if command.Run() != nil {
		return fault("service-unavailable")
	}
	return nil
}

func (c *contextState) serviceEnvironment() []string {
	environment := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C"}
	if c.receipt.Scope == "user" {
		for _, key := range []string{"HOME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
			if value := os.Getenv(key); value != "" {
				environment = append(environment, key+"="+value)
			}
		}
	}
	return environment
}

func (c *contextState) installService() error {
	spec := c.receipt.Service
	if spec.Path == "" {
		return nil
	}
	if err := ensureDirectory(filepath.Dir(spec.Path), c.receipt.UID); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(spec.Path))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(spec.Path)
	if info, err := root.Lstat(name); err == nil {
		if checkOwner(info, c.receipt.UID, false) != nil {
			return fault("unsafe-path")
		}
		data, err := root.ReadFile(name)
		if err != nil || string(data) != spec.Content {
			return fault("unsafe-path")
		}
	} else if os.IsNotExist(err) {
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(spec.Content)
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
	} else {
		return err
	}
	if err := syncDir(root); err != nil {
		return err
	}
	if err := c.serviceCommand("reload"); err != nil {
		return err
	}
	return c.serviceCommand("start")
}

func (c *contextState) removeService() error {
	spec := c.receipt.Service
	if spec.Path == "" {
		return nil
	}
	data, err := os.ReadFile(spec.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || string(data) != spec.Content {
		return fault("unsafe-path")
	}
	if err := c.serviceCommand("stop"); err != nil && c.serviceRunning() {
		return err
	}
	if err := os.Remove(spec.Path); err != nil {
		return err
	}
	return c.serviceCommand("reload")
}

func requestApply(c *contextState) error {
	socket := filepath.Join(c.statePath, "request.sock")
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return fault("service-unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != c.receipt.UID {
		return fault("unsafe-path")
	}
	connection, err := net.DialTimeout("unix", socket, 3*time.Second)
	if err != nil {
		return fault("service-unavailable")
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(connection, "apply\n"); err != nil {
		return fault("service-unavailable")
	}
	value, err := io.ReadAll(io.LimitReader(connection, 32))
	if err != nil || string(value) != "queued\n" {
		return fault("service-unavailable")
	}
	return nil
}

func (c *contextState) serve() error {
	worker, err := c.lock("worker.lock")
	if err != nil {
		return err
	}
	defer worker.Close()
	socket := filepath.Join(c.statePath, "request.sock")
	if len(socket) >= 100 {
		return fault("unsafe-path")
	}
	if info, err := os.Lstat(socket); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != c.receipt.UID || info.Mode()&os.ModeSocket == 0 {
			return fault("unsafe-path")
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0666); err != nil {
		return err
	}
	requests := make(chan struct{}, 1)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		var accepted time.Time
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
			data := make([]byte, 6)
			_, err = io.ReadFull(connection, data)
			if err == nil && string(data) == "apply\n" && time.Since(accepted) >= time.Minute {
				select {
				case requests <- struct{}{}:
					accepted = time.Now()
					_, _ = io.WriteString(connection, "queued\n")
				default:
					_, _ = io.WriteString(connection, "busy\n")
				}
			}
			_ = connection.Close()
		}
	}()
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	for {
		scheduled := false
		select {
		case <-ctx.Done():
			return nil
		case <-requests:
			// Only the fixed apply action crosses the public socket; no caller paths or URLs.
		case <-timer.C:
			scheduled = true
		}
		before := c.active
		_ = c.update(scheduled)
		if c.active != before {
			return fault("worker-restart")
		}
		if scheduled {
			timer.Reset(time.Hour)
		}
	}
}

func (c *contextState) serviceRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	program := "/bin/launchctl"
	args := []string{"print", c.receipt.Service.Domain + "/" + c.receipt.Service.Name}
	if runtime.GOOS == "linux" {
		program = "/usr/bin/systemctl"
		args = []string{"is-active", "--quiet", c.receipt.Service.Name}
		if c.receipt.Scope == "user" {
			args = append([]string{"--user"}, args...)
		}
	}
	command := exec.CommandContext(ctx, program, args...)
	command.Env = c.serviceEnvironment()
	command.Stdout, command.Stderr = io.Discard, io.Discard
	return command.Run() == nil
}
