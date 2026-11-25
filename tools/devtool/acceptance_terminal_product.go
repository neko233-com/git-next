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

// terminalTool resolves an installed Go tool, falling back to the active GOROOT.
func terminalTool(name string) (string, error) {
	if path, e := exec.LookPath(name); e == nil {
		return path, nil
	}
	suffix := ""
	if runtimeOSWindows() {
		suffix = ".exe"
	}
	path := filepath.Join(runtime.GOROOT(), "bin", name+suffix)
	if !exists(path) {
		return "", fmt.Errorf("%s unavailable from PATH or GOROOT", name)
	}
	return path, nil
}

// terminalModule resolves the peer Go module. Both the terminal repository root
// and its go/ module directory are accepted as --source.
func terminalModule(source string) (string, error) {
	if filepath.Base(source) == "go" && exists(filepath.Join(source, "go.mod")) {
		return source, nil
	}
	module := filepath.Join(source, "go")
	if exists(filepath.Join(module, "go.mod")) {
		return module, nil
	}
	return "", fmt.Errorf("peer terminal Go module missing: %s", module)
}

func terminalProduct(a *app, o options) error {
	if e := o.require("source", "output", "distribution"); e != nil {
		return e
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	module, e := terminalModule(source)
	if e != nil {
		return e
	}
	distribution, e := absolute(o.get("distribution"))
	if e != nil {
		return e
	}
	goTool, e := terminalTool("go")
	if e != nil {
		return e
	}
	formatTool, e := terminalTool("gofmt")
	if e != nil {
		return e
	}
	temporary, e := os.MkdirTemp("", "git-terminal-go-home-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temporary)
	git := filepath.Join(distribution, "bin/git")
	if runtimeOSWindows() {
		git += ".exe"
	}
	env := fixtureEnv(acceptEnv(temporary), map[string]string{
		"GOCACHE":               filepath.Join(temporary, "cache"),
		"GIT_TERMINAL_TEST_GIT": git,
		"LANG":                  "C.UTF-8",
		"LC_ALL":                "C.UTF-8",
	})
	output := filepath.Join(temporary, "bin", "git-terminal.exe")
	if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
		return e
	}
	commands := []struct {
		name    string
		program string
		args    []string
	}{
		{"format", formatTool, []string{"-l", "."}},
		{"vet", goTool, []string{"vet", "./..."}},
		{"unit", goTool, []string{"test", "-count=1", "./..."}},
		{"build", goTool, []string{"build", "-trimpath", "-ldflags", "-s -w", "-o", output, "./cmd/git-terminal"}},
	}
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
		value, e := runProcess(ctx, module, env, nil, command.program, command.args...)
		cancel()
		if restoreError := restoreClipboard(); restoreError != nil {
			value.stderr = append(value.stderr, []byte("clipboard restoration failed: "+restoreError.Error())...)
			value.code = 1
		}
		if e != nil {
			value.stderr = append(value.stderr, []byte(e.Error())...)
			value.code = 1
		}
		if command.name == "format" && strings.TrimSpace(string(value.stdout)) != "" {
			value.stderr = append(value.stderr, value.stdout...)
			value.stdout = nil
			value.code = 1
		}
		if command.name == "build" && value.code == 0 {
			if e = stageTerminalNativeVT(module, output); e != nil {
				value.stderr = append(value.stderr, []byte(e.Error())...)
				value.code = 1
			}
		}
		log := strings.TrimSuffix(o.get("output"), filepath.Ext(o.get("output"))) + "-" + command.name + ".log"
		if e = write(log, append(value.stdout, value.stderr...), 0644); e != nil {
			return e
		}
		status := "pass"
		if value.code != 0 {
			status = "fail"
		}
		results = append(results, record{"case": command.name, "status": status, "seconds": time.Since(start).Seconds(), "log_sha256": acceptHash(log)})
	}
	return acceptReport(a, o, record{"status": acceptStatus(results), "product": "git-terminal", "module": "../git-terminal/go", "results": results})
}

// stageTerminalNativeVT places the peer terminal's native library and notice
// beside the freshly built executable so the build lane proves a runnable pair.
func stageTerminalNativeVT(module, executable string) error {
	directory := filepath.Dir(executable)
	library := filepath.Join(module, "resources", "windows-amd64", "ghostty-vt.dll")
	if !exists(library) {
		return fmt.Errorf("peer terminal native VT library missing")
	}
	notice := filepath.Join(module, "resources", "NOTICE.md")
	if !exists(notice) {
		return fmt.Errorf("peer terminal native VT notice missing")
	}
	if e := copyFile(library, filepath.Join(directory, "ghostty-vt.dll")); e != nil {
		return e
	}
	return copyFile(notice, filepath.Join(directory, "NOTICE.ghostty-vt.md"))
}

func init() {
	register("terminal-product-acceptance", "Peer terminal Go formatting, vet, units and build lane", "source output distribution", terminalProduct)
}
