// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func terminalProduct(a *app, o options) error {
	if e := o.require("source", "output", "distribution"); e != nil {
		return e
	}
	source, e := filepath.Abs(o.get("source"))
	if e != nil {
		return e
	}
	distribution, e := filepath.Abs(o.get("distribution"))
	if e != nil {
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	cargo, e := exec.LookPath("cargo")
	if e != nil {
		suffix := ""
		if runtimeOSWindows() {
			suffix = ".exe"
		}
		cargo = filepath.Join(home, ".cargo/bin/cargo"+suffix)
	}
	target := o.get("target")
	if target == "" {
		targets := map[string]string{"windows/amd64": "x86_64-pc-windows-msvc", "windows/arm64": "aarch64-pc-windows-msvc", "linux/amd64": "x86_64-unknown-linux-gnu", "linux/arm64": "aarch64-unknown-linux-gnu", "darwin/arm64": "aarch64-apple-darwin"}
		target = targets[runtime.GOOS+"/"+runtime.GOARCH]
	}
	if target == "" {
		return fmt.Errorf("provide --target for this platform")
	}
	commands := []struct {
		name string
		args []string
	}{{"format", []string{"fmt", "--all", "--check"}}, {"clippy", []string{"clippy", "--locked", "--release", "--package", "git-terminal", "--all-targets", "--target", target, "--no-deps", "--", "-D", "warnings"}}, {"unit", []string{"test", "--locked", "--release", "--workspace", "--lib", "--bins", "--target", target}}, {"shell-integration", []string{"test", "--locked", "--release", "--bin", "git-terminal", "--target", target, "shell_acceptance::shell_features_real_pty", "--", "--ignored", "--exact"}}}
	temporary, e := os.MkdirTemp("", "git-terminal-rust-home-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temporary)
	git := filepath.Join(distribution, "bin/git")
	if runtimeOSWindows() {
		git += ".exe"
	}
	env := fixtureEnv(acceptEnv(temporary), map[string]string{"PATH": filepath.Dir(cargo) + string(os.PathListSeparator) + os.Getenv("PATH"), "GIT_TERMINAL_TEST_GIT": git, "CARGO_HOME": filepath.Join(home, ".cargo"), "RUSTUP_HOME": filepath.Join(home, ".rustup"), "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"})
	results := []record{}
	for _, command := range commands {
		restoreClipboard := func() error { return nil }
		if command.name == "unit" {
			restoreClipboard, e = protectTerminalClipboard()
			if e != nil {
				return e
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		start := time.Now()
		value, e := runProcess(ctx, source, env, nil, cargo, command.args...)
		cancel()
		if restoreError := restoreClipboard(); restoreError != nil {
			value.stderr = append(value.stderr, []byte("clipboard restoration failed: "+restoreError.Error())...)
			value.code = 1
		}
		if e != nil {
			value.stderr = append(value.stderr, []byte(e.Error())...)
			value.code = 1
		}
		log := strings.TrimSuffix(o.get("output"), filepath.Ext(o.get("output"))) + "-" + command.name + ".log"
		if e = write(log, append(value.stdout, value.stderr...), 0644); e != nil {
			return e
		}
		status := "pass"
		if value.code != 0 {
			status = "fail"
		}
		results = append(results, record{"case": command.name, "status": status, "seconds": time.Since(start).Seconds()})
	}
	return acceptReport(a, o, record{"status": acceptStatus(results), "product": "git-terminal", "results": results})
}
func init() {
	register("terminal-product-acceptance", "Peer terminal Rust formatting, lint, units and real PTY shell lane", "source output distribution target", terminalProduct)
}
