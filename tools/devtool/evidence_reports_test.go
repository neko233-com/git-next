// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"path/filepath"
	"testing"
)

func TestGoDesktopEvidenceRequiresEveryCaseAndHarness(t *testing.T) {
	a := &app{root: t.TempDir()}
	harnesses := record{}
	for _, name := range []string{"desktop.go", "desktop_windows.go", "desktop_lease_windows.go", "desktop_drag_windows.go", "clipboard_windows.go", "process.go", "process_windows.go", "files.go", "main.go", "go.mod", "go.sum"} {
		path := "tools/devtool/" + name
		if e := write(a.path(path), []byte(name), 0644); e != nil {
			t.Fatal(e)
		}
		h, e := digest(a.path(path))
		if e != nil {
			t.Fatal(e)
		}
		harnesses[path] = h
	}
	win := a.path("distribution")
	checks := record{}
	for _, name := range []string{"git.exe", "git-terminal.exe"} {
		path := filepath.Join(win, "bin", name)
		if e := write(path, []byte(name), 0644); e != nil {
			t.Fatal(e)
		}
		h, e := digest(path)
		if e != nil {
			t.Fatal(e)
		}
		checks[name] = h
	}
	rows := []any{}
	for _, name := range append(append([]string{}, desktopActivityCases...), "original-clipboard-restored", "owned-window-exit-and-process-cleanup") {
		rows = append(rows, record{"case": name, "status": "pass"})
	}
	proof := record{"status": "pass", "scope": "all", "harness_language": "go", "case_set_version": 1, "harness_set_version": 3, "results": rows, "harnesses": harnesses, "git_sha256": checks["git.exe"], "binary_sha256": checks["git-terminal.exe"]}
	if e := validateDesktopProof(a, proof, win); e != nil {
		t.Fatal(e)
	}
	for _, version := range []int{0, 2} {
		historical := cloneRecord(t, proof)
		historical["harness_set_version"] = version
		delete(object(historical, "harnesses"), "tools/devtool/desktop_drag_windows.go")
		if version == 0 {
			delete(object(historical, "harnesses"), "tools/devtool/desktop_lease_windows.go")
		}
		if e := validateDesktopProof(a, historical, win); e != nil {
			t.Fatalf("historical harness set %d: %v", version, e)
		}
	}
	for _, mode := range []string{"missing case", "duplicate case", "failed case", "missing harness", "changed harness", "missing drag harness", "changed drag harness", "unknown harness schema", "unknown schema"} {
		t.Run(mode, func(t *testing.T) {
			bad := cloneRecord(t, proof)
			switch mode {
			case "missing case":
				bad["results"] = array(bad, "results")[1:]
			case "duplicate case":
				r := array(bad, "results")
				r[1] = r[0]
			case "failed case":
				array(bad, "results")[0].(map[string]any)["status"] = "fail"
			case "missing harness":
				delete(object(bad, "harnesses"), "tools/devtool/process.go")
			case "changed harness":
				object(bad, "harnesses")["tools/devtool/process.go"] = "unverified"
			case "missing drag harness":
				delete(object(bad, "harnesses"), "tools/devtool/desktop_drag_windows.go")
			case "changed drag harness":
				object(bad, "harnesses")["tools/devtool/desktop_drag_windows.go"] = "unverified"
			case "unknown harness schema":
				bad["harness_set_version"] = 4
			case "unknown schema":
				bad["case_set_version"] = 2
			}
			if validateDesktopProof(a, bad, win) == nil {
				t.Fatal("accepted incomplete or altered Go desktop proof")
			}
		})
	}
}

func TestRepairEvidenceBoundaries(t *testing.T) {
	old := record{"bin/git.exe": "core", "bin/git-terminal.exe": "old", "bin/git-terminal-shell.exe": "shell", "updater/msi-worker.exe": "worker", "docs/old.md": "manual"}
	next := cloneRecord(t, old)
	next["bin/git-terminal.exe"] = "new"
	next["docs/old.md"] = "updated"
	next["docs/new.html"] = "new manual"
	compiled := record{"git-terminal.exe": "new", "git-terminal-shell.exe": "shell"}
	changed, e := validateWindowsFiles(old, next, compiled)
	if e != nil || len(changed) != 3 {
		t.Fatalf("terminal update: %v %v", changed, e)
	}
	for _, tt := range []struct {
		name, key string
		value     any
		remove    bool
	}{{"core", "bin/git.exe", "changed", false}, {"worker", "updater/msi-worker.exe", "changed", false}, {"uncompiled GUI", "bin/git-terminal.exe", "unknown", false}, {"unbound provenance", terminalMetadata, "unbound", false}, {"removed runtime", "bin/git.exe", nil, true}} {
		t.Run(tt.name, func(t *testing.T) {
			bad := cloneRecord(t, next)
			if tt.remove {
				delete(bad, tt.key)
			} else {
				bad[tt.key] = tt.value
			}
			if _, e := validateWindowsFiles(old, bad, compiled); e == nil {
				t.Fatal("accepted unrelated or unbound payload delta")
			}
		})
	}
	for _, key := range []string{"tests/msi_acceptance.ps1", "../git-terminal/src/session.rs"} {
		if _, e = validateRepairHarnesses(record{key: "old"}, record{key: "new"}); e == nil {
			t.Fatalf("accepted functional validation change: %s", key)
		}
	}
	if _, e = validateRepairHarnesses(record{}, record{"../git-terminal/assets/git-terminal.ico": "icon"}); e != nil {
		t.Fatal(e)
	}
}
func TestBatchRebindingBoundaries(t *testing.T) {
	previous := record{"product": "git-next", "version": "0.3.0", "source_tree_sha256": "source", "files": record{"runtime": "same"}, "platforms": record{"windows-x64": "same"}, "harnesses": record{"tests/native_read_threads_acceptance.py": "same"}}
	current := cloneRecord(t, previous)
	object(current, "harnesses")["tools/complete_native_batch.py"] = "report-only"
	var rows []any
	for _, name := range keys(correctnessCases) {
		status := "pass"
		exit := 0
		if name == "terminal-native-desktop" {
			status = "fail"
			exit = 1
		}
		rows = append(rows, record{"case": name, "status": status, "exit_code": exit})
	}
	report := record{"phase": "correctness", "source_tree_sha256": "source", "results": rows}
	if e := validateRebinding(previous, current, report); e != nil {
		t.Fatal(e)
	}
	bad := cloneRecord(t, current)
	object(bad, "harnesses")["tests/native_read_threads_acceptance.py"] = "changed"
	if validateRebinding(previous, bad, report) == nil {
		t.Fatal("accepted functional change")
	}
	bad = cloneRecord(t, current)
	object(bad, "files")["runtime"] = "changed"
	if validateRebinding(previous, bad, report) == nil {
		t.Fatal("accepted product change")
	}
	failed := cloneRecord(t, report)
	for _, raw := range array(failed, "results") {
		r := raw.(map[string]any)
		if r["case"] == "msi-machine" {
			r["status"] = "fail"
		}
	}
	if validateRebinding(previous, current, failed) == nil {
		t.Fatal("hid second functional failure")
	}
}
