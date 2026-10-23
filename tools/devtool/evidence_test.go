// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
)

func cloneRecord(t *testing.T, r record) record {
	t.Helper()
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var v record
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestEvidenceReuse(t *testing.T) {
	previous := record{"source_tree_sha256": "source", "files": record{"package.msi": "bytes"}, "platforms": record{"windows-x64": record{"msi": "package.msi"}}, "harnesses": record{"tests/msi_acceptance.ps1": "original", "../git-terminal/tests/windows_desktop_acceptance.py": "old"}}
	report := record{"phase": "correctness", "source_tree_sha256": "source", "reference_sha256": "reference", "results": []any{record{"case": "msi-user", "status": "pass", "exit_code": 0, "seconds": 10}, record{"case": "msi-machine", "status": "pass", "exit_code": 0, "seconds": 10}, record{"case": "terminal-native-desktop", "status": "pass", "exit_code": 0, "seconds": 23}, record{"case": "frozen-input-readback", "status": "pass"}}}
	tests := []struct {
		name          string
		change        func(record, record)
		desktop, fail bool
		count         int
	}{
		{"unchanged", func(record, record) {}, false, false, 2},
		{"desktop changes unrelated to MSI", func(c, r record) {
			object(c, "harnesses")["../git-terminal/tests/windows_desktop_acceptance.py"] = "new"
		}, false, false, 2},
		{"test shell unrelated to MSI", func(c, r record) { object(c, "harnesses")["../git-terminal/src/shell_acceptance.rs"] = "new" }, false, false, 2},
		{"package bytes", func(c, r record) { object(c, "files")["package.msi"] = "changed" }, false, true, 0},
		{"installer validation", func(c, r record) { object(c, "harnesses")["tests/msi_acceptance.ps1"] = "changed" }, false, true, 0},
		{"terminal runtime source", func(c, r record) { object(c, "harnesses")["../git-terminal/src/session.rs"] = "changed" }, false, true, 0},
		{"native sources", func(c, r record) { c["source_tree_sha256"] = "changed" }, false, true, 0},
		{"wrong reference", func(c, r record) { r["reference_sha256"] = "other" }, false, true, 0},
		{"wrong phase", func(c, r record) { r["phase"] = "performance" }, false, true, 0},
		{"failed scope", func(c, r record) { array(r, "results")[1].(map[string]any)["status"] = "fail" }, false, false, 1},
		{"missing exit code", func(c, r record) { delete(array(r, "results")[1].(map[string]any), "exit_code") }, false, false, 1},
		{"duplicate scope", func(c, r record) { r["results"] = append(array(r, "results"), array(r, "results")[0]) }, false, true, 0},
		{"unchanged desktop", func(record, record) {}, true, false, 1},
		{"desktop changed test", func(c, r record) {
			object(c, "harnesses")["../git-terminal/tests/windows_desktop_acceptance.py"] = "changed"
		}, true, true, 0},
		{"desktop changed Go harness", func(c, r record) { object(c, "harnesses")["tools/devtool/desktop_windows.go"] = "changed" }, true, true, 0},
		{"desktop changed process bounds", func(c, r record) { object(c, "harnesses")["tools/devtool/process_windows.go"] = "changed" }, true, true, 0},
		{"desktop failed readback", func(c, r record) { array(r, "results")[3].(map[string]any)["status"] = "fail" }, true, true, 0},
		{"desktop missing readback", func(c, r record) { r["results"] = array(r, "results")[:3] }, true, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, r := cloneRecord(t, previous), cloneRecord(t, report)
			tt.change(current, r)
			cases := []string{"msi-user", "msi-machine"}
			if tt.desktop {
				cases = []string{"terminal-native-desktop"}
			}
			got, e := validateReuseAt(&app{t.TempDir(), io.Discard, io.Discard}, r, previous, current, "reference", cases)
			if (e != nil) != tt.fail {
				t.Fatalf("error=%v; want failure=%t", e, tt.fail)
			}
			if e == nil && len(got) != tt.count {
				t.Fatalf("reused %d, want %d", len(got), tt.count)
			}
		})
	}
}
func TestDesktopDependencyScope(t *testing.T) {
	root := t.TempDir()
	a := &app{root, io.Discard, io.Discard}
	if e := write(filepath.Join(root, "packaging/runtime-defaults/gitconfig"), []byte("[core]\n\tfsmonitor = false\n"), 0644); e != nil {
		t.Fatal(e)
	}
	makeMatrix := func(name, config string) record {
		dir := filepath.Join(root, name)
		files := record{}
		for path, data := range map[string]string{"bin/git-terminal.exe": "GUI", "bin/git.exe": "core", "runtime/usr/bin/bash.exe": "shell", "runtime/etc/gitconfig": config, "docs/TERMINAL.md": name, "migration/Takeover.Core.psm1": name} {
			p := filepath.Join(dir, path)
			if e := write(p, []byte(data), 0644); e != nil {
				t.Fatal(e)
			}
			files[p] = digestBytes([]byte(data))
		}
		return record{"product": "git-next", "version": "1.0.2", "platforms": record{"windows-x64": record{"directory": dir}}, "files": files}
	}
	old := makeMatrix("old", "header\n\tfsmonitor = false\n")
	next := makeMatrix("new", "header\n\tfsmonitor = false\n")
	if e := desktopDependencies(a, old, next); e != nil {
		t.Fatal(e)
	}
	t.Run("runtime changed", func(t *testing.T) {
		c := cloneRecord(t, next)
		object(c, "files")[filepath.Join(root, "new/bin/git.exe")] = "different"
		if desktopDependencies(a, old, c) == nil {
			t.Fatal("accepted changed Git")
		}
	})
	t.Run("defaults changed", func(t *testing.T) {
		c := makeMatrix("changed-defaults", "header\n\tfsmonitor = true\n")
		if desktopDependencies(a, old, c) == nil {
			t.Fatal("accepted changed defaults")
		}
	})
	t.Run("configuration outside defaults", func(t *testing.T) {
		c := makeMatrix("changed-header", "other\n\tfsmonitor = false\n")
		if desktopDependencies(a, old, c) == nil {
			t.Fatal("accepted unrelated configuration")
		}
	})
	t.Run("frozen bytes changed", func(t *testing.T) {
		if e := write(filepath.Join(root, "old/runtime/etc/gitconfig"), []byte("modified"), 0644); e != nil {
			t.Fatal(e)
		}
		c := makeMatrix("new-config", "header\nextra\n\tfsmonitor = false\n")
		if desktopDependencies(a, old, c) == nil {
			t.Fatal("accepted mutated original")
		}
	})
}
