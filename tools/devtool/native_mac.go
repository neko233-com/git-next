// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var macRuntimeLanes = []string{"common", "contracts", "integrations", "read-threads"}

func init() {
	register("native-mac-runtime-acceptance", "Execute the exact archive on Apple Silicon with disposable profiles and complete native lanes", "archive reference output", nativeMacRuntimeAcceptance)
}

func macRuntimeCaseIDs(lane string) []string {
	switch lane {
	case "common":
		var ids []string
		for name := range commonCases {
			for _, format := range []string{"sha1", "sha256"} {
				ids = append(ids, name+":"+format)
			}
		}
		sort.Strings(ids)
		return ids
	case "contracts":
		return []string{
			"D04-native-identity-and-stable-json", "D04-language-set-get-auto-and-cli-priority",
			"D01-objects-index-pack-gc-sha1", "D01-objects-index-pack-gc-sha256",
			"D03-unity-pure-newlines-real-edits-binary-and-explicit-policy",
			"D01-rename-switch-tag-stash-restore-merge-reset-pick-revert",
			"D01-conflict-stages-and-merge-rebase-abort", "D01-owned-local-upload-receive-clone-fetch-pull-push",
			"D01-linked-worktree-index-and-removal", "D01-recursive-submodule-clone-deinit-update",
			"D02-owned-shell-hooks-and-rejected-commit", "D01-sparse-checkout-and-split-index",
			"D01-unknown-command-option-and-lock-preservation",
		}
	case "integrations":
		return []string{"D02-primary-lfs-filter-https-upload-download-cache-integrity", "D02-owned-gpg-generated-key-commit-and-tag-verification"}
	case "read-threads":
		return []string{"native-index-default-cpu-times-two-and-explicit-single-thread", "preload-default-and-repository-command-line-precedence", "large-explicit-index-budget-bounded-without-integer-overflow", "readonly-grep-default-budget-and-explicit-override-consistency"}
	}
	return nil
}

func macRuntimeHarnesses(a *app) (record, error) {
	selected := wordSet("native_mac.go archive.go inputs.go build.go files.go main.go process.go process_unix.go posix_images.go go.mod go.sum")
	entries, e := os.ReadDir(a.path("tools/devtool"))
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_windows.go") || strings.HasSuffix(name, "_linux.go") {
			continue
		}
		if strings.HasPrefix(name, "acceptance_") || strings.HasPrefix(name, "fixture_") {
			selected[name] = true
		}
	}
	hashes := record{}
	for name := range selected {
		key := "tools/devtool/" + name
		h, e := digest(a.path(filepath.FromSlash(key)))
		if e != nil {
			return nil, e
		}
		hashes[key] = h
	}
	return hashes, nil
}

func checkMacRuntimeLane(lane string, report record) error {
	if report["status"] != "pass" {
		return fmt.Errorf("Mac %s report failed", lane)
	}
	want := map[string]bool{}
	for _, id := range macRuntimeCaseIDs(lane) {
		want[id] = true
	}
	rows := array(report, "results")
	if len(rows) == 0 {
		rows = array(report, "cases")
	}
	if len(rows) != len(want) || len(want) == 0 {
		return fmt.Errorf("Mac %s case set is incomplete", lane)
	}
	for _, raw := range rows {
		item := rec(raw)
		id := stringValue(item, "case")
		if lane == "common" {
			id += ":" + stringValue(item, "object_format")
		}
		if !want[id] || item["status"] != "pass" || item["skipped"] == true {
			return fmt.Errorf("Mac %s case missing, duplicated or failed: %s", lane, id)
		}
		delete(want, id)
	}
	if lane == "read-threads" && report["cpu_affinity_constraint_supported"] != false {
		return fmt.Errorf("Mac report must declare the Darwin affinity boundary")
	}
	return nil
}

func ownedP4Dispatch(binary, reference, temp string) (record, error) {
	home, repo := filepath.Join(temp, "p4-home"), filepath.Join(temp, "p4-repo")
	if e := os.MkdirAll(home, 0700); e != nil {
		return nil, e
	}
	var env []string
	for _, value := range cleanEnvironment(home) {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "P4") {
			env = append(env, value)
		}
	}
	env = fixtureEnv(env, map[string]string{"PATH": "", "P4CONFIG": ".git-next-owned-p4config", "P4TICKETS": filepath.Join(home, "tickets"), "P4TRUST": filepath.Join(home, "trust")})
	run := func(executable string, args ...string) (result, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cwd := temp
		if exists(repo) {
			cwd = repo
		}
		r, e := runProcess(ctx, cwd, env, nil, executable, args...)
		if e != nil || r.code != 0 {
			return r, fmt.Errorf("owned Git/P4 dispatch failed: %s: exit=%d: %v", filepath.Base(executable), r.code, e)
		}
		return r, nil
	}
	if _, e := run(reference, "init", "-q", "-b", "main", repo); e != nil {
		return nil, e
	}
	if e := write(filepath.Join(repo, ".git-next-owned-p4config"), nil, 0600); e != nil {
		return nil, e
	}
	if _, e := run(reference, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "commit", "--allow-empty", "-qm", `[git-p4: depot-paths = "//depot/": change = 1]`); e != nil {
		return nil, e
	}
	if _, e := run(reference, "update-ref", "refs/remotes/p4/fixture", "HEAD"); e != nil {
		return nil, e
	}
	want := []byte("p4/fixture <= //depot/ (1)\n")
	rows := []any{}
	for _, entry := range []struct {
		name, executable string
		args             []string
	}{{"git p4 branches", binary, []string{"p4", "branches"}}, {"bin/git-p4 branches", filepath.Join(filepath.Dir(binary), "git-p4"), []string{"branches"}}} {
		r, e := run(entry.executable, entry.args...)
		if e != nil || !bytes.Equal(r.stdout, want) {
			return nil, fmt.Errorf("packaged Go P4 did not locate owned Git with empty PATH: %s: %v", entry.name, e)
		}
		rows = append(rows, record{"entrypoint": entry.name, "status": "pass", "exit_code": r.code, "stdout_sha256": digestBytes(r.stdout)})
	}
	return record{"status": "pass", "empty_path": true, "git_override_present": false, "personal_p4_configuration_accessed": false, "results": rows}, nil
}

func checkedMacRuntimeReceipt(a *app, receiptPath, archive, source, version string) (record, error) {
	r, e := load(receiptPath)
	if e != nil {
		return nil, e
	}
	if number(r, "schema_version") != 1 || number(r, "case_set_version") != 1 || r["product"] != "git-next" || r["platform"] != "macos-arm64" || r["version"] != version || r["status"] != "pass" || r["native_executed"] != true || r["git_p4_executed"] != true || r["personal_profile_accessed"] != false || r["source_tree_sha256"] != source || r["harness_language"] != "go" || r["cpu_affinity_constraint_supported"] != false {
		return nil, fmt.Errorf("complete actual macOS ARM64 receipt for these sources required")
	}
	p4 := object(r, "git_p4_dispatch")
	if p4["status"] != "pass" || p4["empty_path"] != true || p4["git_override_present"] != false || p4["personal_p4_configuration_accessed"] != false || len(array(p4, "results")) != 2 {
		return nil, fmt.Errorf("Mac P4 requires native and direct owned-Git dispatch with isolated empty PATH")
	}
	entrypoints := wordSet("native direct")
	for _, raw := range array(p4, "results") {
		item := rec(raw)
		key := map[string]string{"git p4 branches": "native", "bin/git-p4 branches": "direct"}[stringValue(item, "entrypoint")]
		_, hasExit := item["exit_code"]
		if !entrypoints[key] || item["status"] != "pass" || !hasExit || number(item, "exit_code") != 0 || item["stdout_sha256"] != digestBytes([]byte("p4/fixture <= //depot/ (1)\n")) {
			return nil, fmt.Errorf("Mac P4 dispatch evidence incomplete or changed")
		}
		delete(entrypoints, key)
	}
	if e = requireHash(archive, r["archive_sha256"]); e != nil {
		return nil, e
	}
	info, e := os.Stat(archive)
	if e != nil {
		return nil, e
	}
	if number(r, "archive_bytes") != info.Size() {
		return nil, fmt.Errorf("Mac archive size differs from runtime evidence")
	}
	harnesses, e := macRuntimeHarnesses(a)
	if e != nil {
		return nil, e
	}
	if !equalJSON(harnesses, r["harnesses"]) || !equalJSON(harnesses, r["harnesses_before"]) || !equalJSON(harnesses, r["harnesses_after"]) {
		return nil, fmt.Errorf("Mac runtime harness changed before/after execution")
	}
	static := object(r, "static_report")
	if static["status"] != "passed" || static["platform"] != "macos-arm64" || number(static, "files_inspected") < 1 || len(array(static, "native_images")) == 0 || len(array(static, "shell_entrypoints")) == 0 || len(array(static, "failures")) != 0 || r["static_report_sha256"] != digestBytes(encoded(static)) {
		return nil, fmt.Errorf("Mac static package inspection missing or changed")
	}
	lanes := map[string]bool{}
	for _, lane := range macRuntimeLanes {
		lanes[lane] = true
	}
	rows := array(r, "results")
	if len(rows) != len(lanes) || number(r, "passed_cases") != 51 {
		return nil, fmt.Errorf("four complete Mac runtime lanes with 51 actual cases required")
	}
	for _, raw := range rows {
		item := rec(raw)
		lane := stringValue(item, "case")
		report := object(item, "report")
		_, hasExit := item["exit_code"]
		if !lanes[lane] || item["status"] != "pass" || !hasExit || number(item, "exit_code") != 0 || number(item, "cases") != int64(len(macRuntimeCaseIDs(lane))) || item["report_sha256"] != digestBytes(encoded(report)) {
			return nil, fmt.Errorf("Mac runtime lane missing, duplicated or changed: %s", lane)
		}
		if e = checkMacRuntimeLane(lane, report); e != nil {
			return nil, e
		}
		delete(lanes, lane)
	}
	return r, nil
}

func nativeMacRuntimeAcceptance(a *app, o options) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("actual Apple Silicon host required; static inspection is a separate gate")
	}
	if e := o.require("archive", "reference", "output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("Mac runtime receipt must be new")
	}
	archive, e := absolute(o.get("archive"))
	if e != nil {
		return e
	}
	before, e := macRuntimeHarnesses(a)
	if e != nil {
		return e
	}
	archiveHash, e := digest(archive)
	if e != nil {
		return e
	}
	info, e := os.Stat(archive)
	if e != nil {
		return e
	}
	output, e := absolute(o.get("output"))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
		return e
	}
	temp, e := os.MkdirTemp(filepath.Dir(output), ".git-next-mac-runtime-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	root, e := unpackAcceptancePackage(archive, filepath.Join(temp, "package"))
	if e != nil {
		return e
	}
	manifest, e := load(filepath.Join(root, "package.json"))
	if e != nil {
		return e
	}
	if manifest["platform"] != "macos-arm64" || manifest["product"] != "git-next" || manifest["python_required"] != false {
		return fmt.Errorf("complete interpreter-free Mac product required")
	}
	if e = verifyAcceptancePackage(root, manifest); e != nil {
		return e
	}
	binary := filepath.Join(root, "bin/git")
	started := time.Now()
	results := []any{}
	passedCases := 0
	status := "pass"
	staticPath := filepath.Join(temp, "static.json")
	staticRun, staticErr := a.invoke("mac-package-static", "--package", root, "--output", staticPath)
	staticReport, staticLoadErr := load(staticPath)
	if staticErr != nil || staticRun.code != 0 || staticLoadErr != nil || staticReport["status"] != "passed" {
		_ = writeNewJSON(output, record{"schema_version": 1, "product": "git-next", "platform": "macos-arm64", "status": "fail", "native_executed": false, "archive_sha256": archiveHash, "static_report": staticReport})
		return fmt.Errorf("Mac package static inspection failed before native execution")
	}
	p4Dispatch, e := ownedP4Dispatch(binary, o.get("reference"), temp)
	if e != nil {
		return e
	}
	for _, lane := range macRuntimeLanes {
		reportPath := filepath.Join(temp, lane+".json")
		command := map[string]string{"common": "native-common-cli-acceptance", "contracts": "native-contract-acceptance", "integrations": "native-integrations-acceptance", "read-threads": "native-read-threads-acceptance"}[lane]
		args := []string{binary, "--output", reportPath}
		if lane == "common" || lane == "contracts" {
			args = append(args, "--reference", o.get("reference"))
		}
		if lane == "common" {
			args = append(args, "--jobs", "1")
		}
		run, runErr := a.invoke(command, args...)
		report, loadErr := load(reportPath)
		rows := array(report, "results")
		if len(rows) == 0 {
			rows = array(report, "cases")
		}
		for _, raw := range rows {
			if rec(raw)["status"] == "pass" {
				passedCases++
			}
		}
		item := record{"case": lane, "status": "pass", "cases": len(rows), "expected_cases": len(macRuntimeCaseIDs(lane)), "exit_code": run.code, "report": report, "report_sha256": digestBytes(encoded(report))}
		if runErr != nil || loadErr != nil || run.code != 0 || checkMacRuntimeLane(lane, report) != nil {
			item["status"], status = "fail", "fail"
			item["reason"] = "native lane failed or did not execute its complete case set"
		}
		results = append(results, item)
	}
	after, e := macRuntimeHarnesses(a)
	if e != nil {
		return e
	}
	if !equalJSON(before, after) {
		return fmt.Errorf("Mac harness source changed during native execution")
	}
	if e = requireHash(archive, archiveHash); e != nil {
		return e
	}
	if e = verifyAcceptancePackage(root, manifest); e != nil {
		return e
	}
	receipt := record{"schema_version": 1, "case_set_version": 1, "product": "git-next", "platform": "macos-arm64", "version": manifest["version"], "source_tree_sha256": manifest["source_tree_sha256"], "status": status, "native_executed": true, "git_p4_executed": true, "archive_sha256": archiveHash, "archive_bytes": info.Size(), "harness_language": "go", "harnesses": after, "harnesses_before": before, "harnesses_after": after, "cpu_affinity_constraint_supported": false, "personal_profile_accessed": false, "passed_cases": passedCases, "results": results, "static_report": staticReport, "static_report_sha256": digestBytes(encoded(staticReport)), "seconds": roundedSeconds(time.Since(started))}
	receipt["git_p4_dispatch"] = p4Dispatch
	if e = writeNewJSON(output, receipt); e != nil {
		return e
	}
	if status != "pass" {
		return fmt.Errorf("actual Mac runtime acceptance failed; inspect the bound lane reports")
	}
	if _, e = checkedMacRuntimeReceipt(a, output, archive, stringValue(manifest, "source_tree_sha256"), stringValue(manifest, "version")); e != nil {
		return e
	}
	return a.json(record{"status": "pass", "platform": "macos-arm64", "native_executed": true, "passed_cases": 51, "archive_sha256": archiveHash})
}
