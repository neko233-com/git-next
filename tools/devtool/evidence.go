// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const benchmarkContextDeadlineBlock = "\tif contextErr := ctx.Err(); contextErr != nil {\n\t\treturn value, fmt.Errorf(\"benchmark process exceeded %s: %w\", timeout, contextErr)\n\t}\n"

func boundedBenchmarkDeadlineRepair(a *app, before, after any) bool {
	data, e := os.ReadFile(a.path("tools/devtool/benchmark_process.go"))
	block := []byte(benchmarkContextDeadlineBlock)
	if e != nil || digestBytes(data) != after || bytes.Count(data, block) != 1 {
		return false
	}
	return digestBytes(bytes.Replace(data, block, nil, 1)) == before
}

func desktopDependencies(a *app, previous, current record) error {
	for _, key := range []string{"product", "version"} {
		if !equalJSON(previous[key], current[key]) {
			return fmt.Errorf("desktop package identity changed: %s", key)
		}
	}
	runtime := func(matrix record) (string, record, error) {
		root := stringValue(object(object(matrix, "platforms"), "windows-x64"), "directory")
		root, e := filepath.EvalSymlinks(root)
		if e != nil {
			return "", nil, e
		}
		values := record{}
		for name, hash := range object(matrix, "files") {
			path, e := absolute(name)
			if e != nil {
				return "", nil, e
			}
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = resolved
			}
			if !inside(root, path) {
				continue
			}
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return "", nil, e
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "migration/") || strings.HasPrefix(rel, "updater/") || rel == "README.md" || rel == "README.zh-CN.md" || rel == "package.json" || rel == "git-next-distribution.json" {
				continue
			}
			values[rel] = hash
		}
		for _, key := range []string{"bin/git-terminal.exe", "bin/git.exe", "runtime/usr/bin/bash.exe", "runtime/etc/gitconfig"} {
			if values[key] == nil {
				return "", nil, fmt.Errorf("desktop runtime dependency missing: %s", key)
			}
		}
		return root, values, nil
	}
	oldRoot, old, e := runtime(previous)
	if e != nil {
		return e
	}
	newRoot, next, e := runtime(current)
	if e != nil {
		return e
	}
	oldConfig, newConfig := old["runtime/etc/gitconfig"], next["runtime/etc/gitconfig"]
	delete(old, "runtime/etc/gitconfig")
	delete(next, "runtime/etc/gitconfig")
	if !equalJSON(old, next) {
		return fmt.Errorf("desktop runtime bytes changed")
	}
	if !equalJSON(oldConfig, newConfig) {
		original, e := os.ReadFile(filepath.Join(oldRoot, "runtime/etc/gitconfig"))
		if e != nil {
			return e
		}
		actual, e := os.ReadFile(filepath.Join(newRoot, "runtime/etc/gitconfig"))
		if e != nil {
			return e
		}
		if digestBytes(original) != oldConfig || digestBytes(actual) != newConfig {
			return fmt.Errorf("desktop configuration differs from frozen receipt")
		}
		d, e := readDefaults(a.root)
		if e != nil {
			return e
		}
		block := d.core() + d.tail()
		if !strings.HasSuffix(string(actual), block) || !strings.HasSuffix(string(original), block) {
			return fmt.Errorf("product configuration defaults changed; desktop evidence cannot be reused")
		}
		if strings.TrimSuffix(string(original), block) != strings.TrimSuffix(string(actual), block) {
			return fmt.Errorf("desktop configuration differs outside product defaults")
		}
	}
	return nil
}

func init() {
	register("native-msi-evidence", "Validate exact-input MSI evidence reuse", "report matrix current reference-sha256 cases", verifyMSIEvidence)
	register("terminal-runtime-fixture", "Stage initialized terminal defaults without changing frozen inputs", "source destination", terminalRuntimeFixture)
}
func validateReuseAt(a *app, report, previous, current record, reference string, cases []string) (record, error) {
	desktop := len(cases) == 1 && cases[0] == "terminal-native-desktop"
	if stringValue(report, "phase") != "correctness" || stringValue(report, "reference_sha256") != reference {
		return nil, fmt.Errorf("evidence phase/reference differs")
	}
	if report["source_tree_sha256"] != current["source_tree_sha256"] || previous["source_tree_sha256"] != current["source_tree_sha256"] {
		return nil, fmt.Errorf("native sources differ")
	}
	if !equalJSON(previous["files"], current["files"]) || !equalJSON(previous["platforms"], current["platforms"]) {
		if !desktop {
			return nil, fmt.Errorf("immutable package inputs differ")
		}
		if e := desktopDependencies(a, previous, current); e != nil {
			return nil, e
		}
	}
	allowed := map[string]bool{}
	for _, name := range []string{"tools/native_product_batch.py", "tools/freeze_native_batch.py", "tools/native_msi_evidence.py", "tests/native_msi_evidence_test.py", "../git-terminal/tests/windows_desktop_acceptance.py", "../git-terminal/src/shell_acceptance.rs", "tools/devtool/batch_native.go", "tools/devtool/batch_freeze.go", "tools/devtool/evidence_test.go", "tools/devtool/desktop.go", "tools/devtool/desktop_windows.go", "tools/devtool/clipboard_windows.go"} {
		allowed[name] = true
	}
	before, after := object(previous, "harnesses"), object(current, "harnesses")
	all := map[string]bool{}
	for name := range before {
		all[name] = true
	}
	for name := range after {
		all[name] = true
	}
	for name := range all {
		permitted := allowed[name] || !desktop && terminalWaiverMSIReuseSource(name)
		if !desktop && name == "tools/devtool/benchmark_process.go" && !equalJSON(before[name], after[name]) {
			permitted = boundedBenchmarkDeadlineRepair(a, before[name], after[name])
		}
		if !desktop && name == "tools/devtool/benchmark_process_timeout_test.go" {
			permitted = true
		}
		if desktop {
			permitted = !(strings.HasPrefix(name, "../git-terminal/") && name != "../git-terminal/src/shell_acceptance.rs") && name != "tools/terminal_runtime_fixture.py" && name != "tools/devtool/evidence.go"
			if strings.HasPrefix(name, "tools/devtool/") {
				base := filepath.Base(name)
				if strings.HasPrefix(base, "desktop") || strings.HasPrefix(base, "clipboard") || strings.HasPrefix(base, "process") || base == "main.go" || base == "files.go" || base == "go.mod" || base == "go.sum" {
					permitted = false
				}
			}
		}
		if !equalJSON(before[name], after[name]) && !permitted {
			return nil, fmt.Errorf("MSI validation sources changed: %s", name)
		}
	}
	if desktop {
		readback := false
		for _, raw := range array(report, "results") {
			item, _ := raw.(map[string]any)
			readback = readback || stringValue(item, "case") == "frozen-input-readback" && stringValue(item, "status") == "pass"
		}
		if !readback {
			return nil, fmt.Errorf("desktop evidence requires successful frozen-input readback")
		}
	}
	results := record{}
	for _, name := range cases {
		matches := 0
		for _, raw := range array(report, "results") {
			item, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid batch result")
			}
			if stringValue(item, "case") == name {
				matches++
				if stringValue(item, "status") == "pass" && item["exit_code"] != nil && number(item, "exit_code") == 0 {
					results[name] = item
				}
			}
		}
		if matches > 1 {
			return nil, fmt.Errorf("duplicate evidence case: %s", name)
		}
	}
	return results, nil
}
func verifyMSIEvidence(a *app, o options) error {
	if e := o.require("report", "matrix", "current", "reference-sha256"); e != nil {
		return e
	}
	current, e := load(o.get("current"))
	if e != nil {
		return e
	}
	reused, e := loadReuse(a, o.get("report"), o.get("matrix"), current, o.get("reference-sha256"), strings.Split(o.value("cases", "msi-user,msi-machine"), ","))
	if e != nil {
		return e
	}
	return a.json(reused)
}
func loadReuse(a *app, reportPath, matrixPath string, current record, reference string, cases []string) (record, error) {
	report, e := load(reportPath)
	if e != nil {
		return nil, e
	}
	previous, e := load(matrixPath)
	if e != nil {
		return nil, e
	}
	matrixHash, e := digest(matrixPath)
	if e != nil {
		return nil, e
	}
	if matrixHash != stringValue(report, "matrix_sha256") {
		return nil, fmt.Errorf("previous matrix differs from receipt")
	}
	results, e := validateReuseAt(a, report, previous, current, reference, cases)
	if e != nil {
		return nil, e
	}
	batchHash, e := digest(reportPath)
	if e != nil {
		return nil, e
	}
	reused := record{}
	for name, raw := range results {
		item := raw.(map[string]any)
		scope := "all immutable package bytes and installer tests"
		if name == "terminal-native-desktop" {
			scope = "terminal runtime bytes and exact desktop test"
		}
		reused[name] = record{"case": name, "status": "pass", "exit_code": 0, "seconds": 0, "dependency_scope": scope, "original_seconds": item["seconds"], "reused_evidence_batch_sha256": batchHash, "reused_evidence_matrix_sha256": matrixHash}
	}
	return reused, nil
}
func copyRuntimeTree(source, destination string) error {
	return filepath.WalkDir(source, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		target := filepath.Join(destination, relative)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, e := os.Readlink(p)
			if e != nil {
				return e
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(p), link))
			if !inside(source, resolved) {
				return fmt.Errorf("runtime symlink escapes source")
			}
			return os.Symlink(link, target)
		}
		return os.Link(p, target)
	})
}
func terminalRuntimeFixture(a *app, o options) error {
	if e := o.require("source", "destination"); e != nil {
		return e
	}
	dest, e := prepareTerminalRuntime(a, o.get("source"), o.get("destination"))
	if e != nil {
		return e
	}
	return a.json(record{"destination": dest, "frozen_inputs_changed": false})
}
func prepareTerminalRuntime(a *app, sourcePath, destinationPath string) (string, error) {
	source, e := absolute(sourcePath)
	if e != nil {
		return "", e
	}
	dest, e := absolute(destinationPath)
	if e != nil {
		return "", e
	}
	if exists(dest) {
		return "", fmt.Errorf("fixture destination must be new")
	}
	if e = copyRuntimeTree(source, dest); e != nil {
		return "", e
	}
	manifest, e := load(filepath.Join(dest, "runtime/defaults/manifest.json"))
	if e != nil {
		return "", e
	}
	if stringValue(manifest, "product") != "git-next" {
		return "", fmt.Errorf("unexpected runtime defaults")
	}
	for _, raw := range array(manifest, "files") {
		item, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid runtime default")
		}
		target, e := confined(dest, stringValue(item, "target"))
		if e != nil {
			return "", e
		}
		defaultPath, e := confined(dest, stringValue(item, "source"))
		if e != nil {
			return "", e
		}
		hash, e := digest(defaultPath)
		if e != nil {
			return "", e
		}
		if hash != stringValue(item, "sha256") {
			return "", fmt.Errorf("runtime default changed")
		}
		if exists(target) {
			return "", fmt.Errorf("initial runtime configuration already exists")
		}
		if e = copyFile(defaultPath, target); e != nil {
			return "", e
		}
		if number(item, "attributes")&4 != 0 {
			if e = setSystemFileAttribute(target); e != nil {
				return "", e
			}
		}
	}
	return dest, nil
}
