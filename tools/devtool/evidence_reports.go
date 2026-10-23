// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var correctnessCases = wordSet("windows-core-contracts windows-common-cli-contracts windows-owned-integrations windows-dpapi-migration windows-update-contracts windows-read-thread-contracts windows-bootstrap-contracts windows-game-lfs-contracts terminal-rust-contracts terminal-native-desktop linux-x64 linux-arm64 macos-arm64 docker-login-migration posix-updater-race-contracts msi-user msi-machine frozen-input-readback")
var referenceCases = wordSet("windows-core-contracts windows-common-cli-contracts windows-dpapi-migration")
var guiBinaries = wordSet("bin/git-terminal.exe bin/git-terminal-shell.exe")

const terminalMetadata = "LICENSES/git-terminal/git-terminal-package.json"

var repairReportOnly = wordSet("tools/run_native_repair_cases.py tools/complete_native_repair_batch.py tests/native_repair_evidence_test.py tools/freeze_native_batch.py tools/devtool/evidence_reports.go tools/devtool/evidence_reports_test.go")
var repairUISources = wordSet("../git-terminal/Cargo.toml ../git-terminal/build.rs ../git-terminal/src/assets.rs ../git-terminal/src/main.rs ../git-terminal/src/presentation.rs ../git-terminal/tests/windows_desktop_acceptance.py ../git-terminal/tests/clipboard_snapshot.py ../git-terminal/assets/git-terminal.svg ../git-terminal/assets/git-terminal.png ../git-terminal/assets/git-terminal.ico")

func changedKeys(previous, current record) []string {
	all := map[string]bool{}
	for name := range previous {
		all[name] = true
	}
	for name := range current {
		all[name] = true
	}
	var changed []string
	for _, name := range keys(all) {
		if !equalJSON(previous[name], current[name]) {
			changed = append(changed, name)
		}
	}
	return changed
}
func repairDocumentation(name string) bool {
	return name == "README.md" || name == "README.zh-CN.md" || strings.HasPrefix(name, "docs/") || strings.HasPrefix(name, "LICENSES/git-terminal/manuals/")
}
func validateWindowsFiles(previous, current, compiled record) ([]string, error) {
	changed := changedKeys(previous, current)
	for _, name := range changed {
		if !guiBinaries[name] && name != terminalMetadata && !repairDocumentation(name) {
			return nil, fmt.Errorf("core/runtime/installer changed outside terminal UI: %s", name)
		}
	}
	if current[terminalMetadata] != nil && !equalJSON(current[terminalMetadata], compiled["git-terminal-package.json"]) {
		return nil, fmt.Errorf("terminal provenance differs from compilation receipt")
	}
	for name := range guiBinaries {
		if current[name] == nil || !equalJSON(current[name], compiled[filepath.Base(name)]) {
			return nil, fmt.Errorf("terminal executable differs from compilation receipt: %s", name)
		}
	}
	return changed, nil
}
func validateRepairHarnesses(previous, current record) ([]string, error) {
	changed := changedKeys(previous, current)
	for _, name := range changed {
		if !repairReportOnly[name] && !repairUISources[name] {
			return nil, fmt.Errorf("unaffected functional/installer validation changed: %s", name)
		}
	}
	return changed, nil
}
func caseResults(report record) (map[string]record, error) {
	out := map[string]record{}
	for _, raw := range array(report, "results") {
		r, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid case result")
		}
		name := stringValue(r, "case")
		if name == "" || out[name] != nil {
			return nil, fmt.Errorf("missing or duplicate case: %s", name)
		}
		out[name] = r
	}
	return out, nil
}
func validateRebinding(previous, current, report record) error {
	for _, key := range []string{"product", "version", "source_tree_sha256", "files", "platforms"} {
		if !equalJSON(previous[key], current[key]) {
			return fmt.Errorf("accepted product inputs changed: %s", key)
		}
	}
	if !equalJSON(report["source_tree_sha256"], current["source_tree_sha256"]) {
		return fmt.Errorf("original report identifies different core sources")
	}
	allowed := wordSet("tools/native_msi_evidence.py tests/native_msi_evidence_test.py tools/complete_native_batch.py tools/devtool/evidence_reports.go tools/devtool/evidence_reports_test.go")
	for _, name := range changedKeys(object(previous, "harnesses"), object(current, "harnesses")) {
		if !allowed[name] {
			return fmt.Errorf("functional validation source changed: %s", name)
		}
	}
	results, e := caseResults(report)
	if e != nil {
		return e
	}
	expected := correctnessCases
	// Historical complete batches predate the common-CLI lane; keep their exact set.
	if len(results) == 17 {
		expected = wordSet("windows-core-contracts windows-owned-integrations windows-dpapi-migration windows-update-contracts windows-read-thread-contracts windows-bootstrap-contracts windows-game-lfs-contracts terminal-rust-contracts terminal-native-desktop linux-x64 linux-arm64 macos-arm64 docker-login-migration posix-updater-race-contracts msi-user msi-machine frozen-input-readback")
	}
	if stringValue(report, "phase") != "correctness" || len(results) != len(expected) {
		return fmt.Errorf("complete original correctness batch required")
	}
	for name := range expected {
		r := results[name]
		if r == nil {
			return fmt.Errorf("missing correctness case: %s", name)
		}
		if name == "terminal-native-desktop" {
			if stringValue(r, "status") == "pass" {
				return fmt.Errorf("only an interrupted desktop case may be finalized")
			}
			continue
		}
		if stringValue(r, "status") != "pass" {
			return fmt.Errorf("another case failed: %s", name)
		}
		if name != "frozen-input-readback" && (r["exit_code"] == nil || number(r, "exit_code") != 0) {
			return fmt.Errorf("case lacks successful process result: %s", name)
		}
	}
	return nil
}
func init() {
	register("complete-native-batch", "Finalize a proven external desktop interruption without weakening frozen evidence", "batch previous-matrix native-packages desktop-batch desktop-matrix reference output", completeNativeBatch)
	register("complete-native-repair-batch", "Bind unaffected exact acceptance lanes to a verified terminal-only repair", "batch previous-matrix native-packages repair desktop terminal-rust terminal-builds reference output", completeNativeRepair)
	register("run-native-repair-cases", "Rerun all four reference-dependent Windows and desktop cases", "native-packages reference output", runNativeRepairCases)
}
func loadOptionRecords(o options, names ...string) ([]record, error) {
	values := make([]record, len(names))
	for i, name := range names {
		if e := o.require(name); e != nil {
			return nil, e
		}
		v, e := load(o.get(name))
		if e != nil {
			return nil, e
		}
		values[i] = v
	}
	return values, nil
}
func completeNativeBatch(a *app, o options) error {
	if e := o.require("batch", "previous-matrix", "native-packages", "desktop-batch", "desktop-matrix", "reference", "output"); e != nil {
		return e
	}
	start := time.Now()
	values, e := loadOptionRecords(o, "batch", "previous-matrix", "native-packages")
	if e != nil {
		return e
	}
	original, previous, current := values[0], values[1], values[2]
	if e = requireHash(o.get("previous-matrix"), original["matrix_sha256"]); e != nil {
		return e
	}
	reference, e := digest(o.get("reference"))
	if e != nil {
		return e
	}
	if reference != stringValue(original, "reference_sha256") {
		return fmt.Errorf("original reference differs")
	}
	if e = validateRebinding(previous, current, original); e != nil {
		return e
	}
	focus := filepath.Join(filepath.Dir(o.get("batch")), "terminal-native-desktop.log")
	failure, e := read(focus)
	if e != nil {
		return e
	}
	for _, marker := range []string{"u.GetForegroundWindow()!=hwnd:activate_owned(hwnd)", "in activate_owned", "Owned terminal action did not complete in time"} {
		if !strings.Contains(failure, marker) {
			return fmt.Errorf("desktop failure is outside the known external focus interruption")
		}
	}
	desktop, e := loadReuse(a, o.get("desktop-batch"), o.get("desktop-matrix"), current, reference, []string{"terminal-native-desktop"})
	if e != nil {
		return e
	}
	item := object(desktop, "terminal-native-desktop")
	if item == nil {
		return fmt.Errorf("no dependency-matched passed desktop receipt")
	}
	if e = nativeMatrixReadback(a, current); e != nil {
		return e
	}
	result, e := cloneJSONRecord(original)
	if e != nil {
		return e
	}
	hash, e := digest(o.get("native-packages"))
	if e != nil {
		return e
	}
	oldHash, e := digest(o.get("batch"))
	if e != nil {
		return e
	}
	matrixHash, e := digest(o.get("previous-matrix"))
	if e != nil {
		return e
	}
	focusHash, e := digest(focus)
	if e != nil {
		return e
	}
	for _, raw := range array(result, "results") {
		r := raw.(map[string]any)
		if stringValue(r, "case") == "terminal-native-desktop" {
			stamp := r["started_seconds_from_batch"]
			for k := range r {
				delete(r, k)
			}
			for k, v := range item {
				r[k] = v
			}
			r["started_seconds_from_batch"] = stamp
			r["finished_seconds_from_batch"] = stamp
		}
	}
	result["status"] = "pass"
	result["matrix_sha256"] = hash
	result["release_eligible"] = false
	result["original_batch_sha256"] = oldHash
	result["original_matrix_sha256"] = matrixHash
	result["external_desktop_focus_failure_log_sha256"] = focusHash
	result["desktop_reusing_unchanged_passed_evidence"] = true
	result["desktop_evidence_scope"] = "identical GUI/core/Bash/runtime bytes and desktop tests; current defaults only; installer/docs excluded"
	result["product_cases_rerun_during_receipt_finalization"] = false
	result["receipt_finalization_seconds"] = roundedSeconds(time.Since(start))
	if e = writeNewJSON(o.get("output"), result); e != nil {
		return e
	}
	return a.json(record{"status": "pass", "items": len(array(result, "results")), "product_cases_rerun": false})
}
func cloneJSONRecord(r record) (record, error) {
	data, e := jsonBytes(r)
	if e != nil {
		return nil, e
	}
	var copy record
	e = decodeJSON(data, &copy)
	return copy, e
}
func completeNativeRepair(a *app, o options) error {
	if e := o.require("batch", "previous-matrix", "native-packages", "repair", "desktop", "terminal-rust", "terminal-builds", "reference", "output"); e != nil {
		return e
	}
	values, e := loadOptionRecords(o, "batch", "previous-matrix", "native-packages", "repair")
	if e != nil {
		return e
	}
	original, previous, current, repair := values[0], values[1], values[2], values[3]
	for _, receipt := range []record{original, repair} {
		if e = requireHash(o.get("previous-matrix"), receipt["matrix_sha256"]); e != nil {
			return e
		}
		if !equalJSON(receipt["source_tree_sha256"], current["source_tree_sha256"]) {
			return fmt.Errorf("actual acceptance source identity differs")
		}
	}
	for _, key := range []string{"product", "version", "source_tree_sha256"} {
		if !equalJSON(previous[key], current[key]) {
			return fmt.Errorf("core/product identity changed")
		}
	}
	changed, e := validateRepairHarnesses(object(previous, "harnesses"), object(current, "harnesses"))
	if e != nil {
		return e
	}
	results, e := caseResults(original)
	if e != nil {
		return e
	}
	if stringValue(original, "phase") != "correctness" || len(results) != len(correctnessCases) {
		return fmt.Errorf("complete original 18-case batch required")
	}
	expectedFailures := wordSet("windows-dpapi-migration terminal-native-desktop frozen-input-readback")
	for name := range correctnessCases {
		r := results[name]
		if r == nil || (stringValue(r, "status") != "pass") != expectedFailures[name] {
			return fmt.Errorf("unexpected failure set in original batch")
		}
	}
	if e = requireHash(o.get("reference"), repair["reference_sha256"]); e != nil {
		return e
	}
	if !truth(repair, "frozen_input_readback") || !equalJSON(repair["harnesses"], previous["harnesses"]) {
		return fmt.Errorf("complete-reference rerun inputs differ")
	}
	rerun, e := caseResults(repair)
	if e != nil {
		return e
	}
	for name := range referenceCases {
		r := rerun[name]
		if r == nil || stringValue(r, "status") != "pass" || r["exit_code"] == nil || number(r, "exit_code") != 0 {
			return fmt.Errorf("reference-dependent lane did not actually pass: %s", name)
		}
		if e = requireHash(filepath.Join(filepath.Dir(o.get("repair")), name+".log"), r["log_sha256"]); e != nil {
			return e
		}
	}
	gcmFailure, e := read(filepath.Join(filepath.Dir(o.get("batch")), "windows-dpapi-migration.log"))
	if e != nil {
		return e
	}
	for _, marker := range []string{"push_acceptance.py", "FileNotFoundError: [WinError 2]", "subprocess.run([backend]"} {
		if !strings.Contains(gcmFailure, marker) {
			return fmt.Errorf("credential failure outside exact known fixture repair")
		}
	}
	deltas := record{}
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		old, next := object(object(previous, "platforms"), platform), object(object(current, "platforms"), platform)
		before, e := load(filepath.Join(stringValue(old, "directory"), "git-next-distribution.json"))
		if e != nil {
			return e
		}
		after, e := load(filepath.Join(stringValue(next, "directory"), "git-next-distribution.json"))
		if e != nil {
			return e
		}
		compiledPath := filepath.Join(o.get("terminal-builds"), platform, "git-terminal-package.json")
		compiled, e := load(compiledPath)
		if e != nil {
			return e
		}
		if !equalJSON(object(after, "git_terminal")["source_commit"], compiled["source_commit"]) {
			return fmt.Errorf("terminal source differs from compilation")
		}
		maps := []record{}
		for _, manifest := range []record{before, after} {
			files := record{}
			for name, raw := range object(manifest, "files") {
				files[name] = stringValue(raw.(map[string]any), "sha256")
			}
			maps = append(maps, files)
		}
		provenance, e := cloneJSONRecord(object(compiled, "files"))
		if e != nil {
			return e
		}
		provenance["git-terminal-package.json"], e = digest(compiledPath)
		if e != nil {
			return e
		}
		deltas[platform], e = validateWindowsFiles(maps[0], maps[1], provenance)
		if e != nil {
			return e
		}
		for _, scope := range []string{"user", "machine"} {
			key := "msi_" + scope
			oldPath, newPath := stringValue(old, key), stringValue(next, key)
			left, e := load(strings.TrimSuffix(oldPath, filepath.Ext(oldPath)) + ".receipt.json")
			if e != nil {
				return e
			}
			right, e := load(strings.TrimSuffix(newPath, filepath.Ext(newPath)) + ".receipt.json")
			if e != nil {
				return e
			}
			for _, field := range []string{"product_code", "upgrade_code", "version", "msi_version", "install_scope", "platform", "packaging_files", "source_tree_sha256"} {
				if !equalJSON(left[field], right[field]) {
					return fmt.Errorf("exercised MSI contract changed: %s", field)
				}
			}
			if e = requireHash(newPath, right["sha256"]); e != nil {
				return e
			}
		}
	}
	for _, platform := range []string{"linux-x64", "linux-arm64", "macos-arm64"} {
		old := stringValue(object(object(previous, "platforms"), platform), "archive")
		next := stringValue(object(object(current, "platforms"), platform), "archive")
		hash, e := digest(old)
		if e != nil {
			return e
		}
		if e = requireHash(next, hash); e != nil {
			return e
		}
	}
	proofs, e := loadOptionRecords(o, "desktop", "terminal-rust")
	if e != nil {
		return e
	}
	desktop, rust := proofs[0], proofs[1]
	win := stringValue(object(object(current, "platforms"), "windows-x64"), "directory")
	binary := filepath.Join(win, "bin/git.exe")
	if e = validateDesktopProof(a, desktop, win); e != nil {
		return e
	}
	rustCases, e := caseResults(rust)
	if e != nil {
		return e
	}
	if stringValue(rust, "status") != "pass" || len(rustCases) != 4 {
		return fmt.Errorf("terminal Rust and shell proof required")
	}
	for _, name := range []string{"format", "clippy", "unit", "shell-integration"} {
		if stringValue(rustCases[name], "status") != "pass" {
			return fmt.Errorf("terminal source case failed: %s", name)
		}
	}
	common, e := load(filepath.Join(filepath.Dir(o.get("repair")), "windows-common-cli.json"))
	if e != nil {
		return e
	}
	if stringValue(common, "status") != "pass" || len(array(common, "results")) != 32 {
		return fmt.Errorf("full 32-case common CLI proof required")
	}
	if e = requireHash(binary, common["binary_sha256"]); e != nil {
		return e
	}
	for _, m := range []record{previous, current} {
		for name, want := range object(m, "files") {
			if e = requireHash(name, want); e != nil {
				return e
			}
		}
	}
	if e = nativeMatrixReadback(a, current); e != nil {
		return e
	}
	result, e := cloneJSONRecord(original)
	if e != nil {
		return e
	}
	result["status"] = "pass"
	result["reference_sha256"] = repair["reference_sha256"]
	result["original_reference_sha256"] = original["reference_sha256"]
	for field, option := range map[string]string{"matrix_sha256": "native-packages", "original_batch_sha256": "batch", "original_matrix_sha256": "previous-matrix", "repair_receipt_sha256": "repair", "desktop_receipt_sha256": "desktop", "terminal_rust_receipt_sha256": "terminal-rust"} {
		result[field], e = digest(o.get(option))
		if e != nil {
			return e
		}
	}
	result["terminal_only_payload_deltas"] = deltas
	result["validation_source_deltas"] = changed
	result["msi_scope"] = "previous actual lifecycle retained for identical installer/worker/configuration; only verified terminal binaries/manuals changed; final installation remains separate"
	result["reference_transition"] = "three reference-dependent cases actually rerun with complete reference"
	result["original_batch_elapsed_seconds"] = original["elapsed_seconds"]
	result["repair_seconds"] = repair["seconds"]
	result["desktop_seconds"] = desktop["seconds"]
	result["unaffected_product_cases_rerun"] = false
	for _, raw := range array(result, "results") {
		r := raw.(map[string]any)
		name := stringValue(r, "case")
		if referenceCases[name] {
			for k := range r {
				delete(r, k)
			}
			for _, key := range []string{"case", "status", "exit_code", "seconds", "log_sha256"} {
				r[key] = rerun[name][key]
			}
			r["actual_repair_run"] = true
		} else if name == "terminal-native-desktop" || name == "terminal-rust-contracts" {
			r["status"] = "pass"
			r["exit_code"] = 0
			if name == "terminal-native-desktop" {
				r["seconds"] = desktop["seconds"]
				r["actual_current_gui_run"] = true
			} else {
				seconds := float64(0)
				for _, item := range rustCases {
					v, _ := item["seconds"].(float64)
					seconds += v
				}
				r["seconds"] = seconds
				r["actual_current_source_run"] = true
			}
		} else if name == "frozen-input-readback" {
			r["status"] = "pass"
			r["current_and_prior_artifact_sets_rehashed"] = true
		} else {
			r["retained_actual_lifecycle_or_unchanged_runtime"] = true
		}
	}
	if e = writeNewJSON(o.get("output"), result); e != nil {
		return e
	}
	return a.json(record{"status": "pass", "items": len(array(result, "results")), "desktop_cases": len(array(desktop, "results")), "common_cli_cases": 32})
}

func validateDesktopProof(a *app, desktop record, win string) error {
	if stringValue(desktop, "status") != "pass" || stringValue(desktop, "scope") != "all" {
		return fmt.Errorf("complete actual desktop proof required")
	}
	cases, e := caseResults(desktop)
	if e != nil {
		return e
	}
	checks := map[string]any{filepath.Join(win, "bin/git-terminal.exe"): desktop["binary_sha256"], filepath.Join(win, "bin/git.exe"): desktop["git_sha256"]}
	if stringValue(desktop, "harness_language") == "go" {
		if number(desktop, "case_set_version") != 1 {
			return fmt.Errorf("unknown Go desktop case set")
		}
		expected := wordSet("original-clipboard-restored owned-window-exit-and-process-cleanup")
		for _, name := range desktopActivityCases {
			expected[name] = true
		}
		if len(cases) != len(expected) {
			return fmt.Errorf("complete actual %d-case Go desktop proof required", len(expected))
		}
		for name := range expected {
			if _, ok := cases[name]; !ok {
				return fmt.Errorf("missing desktop case: %s", name)
			}
		}
		harnesses := object(desktop, "harnesses")
		expectedHarnesses := wordSet("tools/devtool/desktop.go tools/devtool/desktop_windows.go tools/devtool/clipboard_windows.go tools/devtool/process.go tools/devtool/process_windows.go tools/devtool/files.go tools/devtool/main.go tools/devtool/go.mod tools/devtool/go.sum")
		switch number(desktop, "harness_set_version") {
		case 3:
			expectedHarnesses["tools/devtool/desktop_drag_windows.go"] = true
			fallthrough
		case 2:
			expectedHarnesses["tools/devtool/desktop_lease_windows.go"] = true
		case 0:
		default:
			return fmt.Errorf("unknown desktop harness set")
		}
		if len(harnesses) != len(expectedHarnesses) {
			return fmt.Errorf("complete Go desktop harness provenance required")
		}
		for name := range expectedHarnesses {
			checks[a.path(name)] = harnesses[name]
		}
	} else if stringValue(desktop, "harness_language") == "" {
		// Historical receipts retain their original validation contract.
		if len(cases) != 15 {
			return fmt.Errorf("complete historical 15-case desktop proof required")
		}
		checks[a.path("../git-terminal/tests/windows_desktop_acceptance.py")] = desktop["harness_sha256"]
		checks[a.path("../git-terminal/tests/clipboard_snapshot.py")] = desktop["clipboard_harness_sha256"]
	} else {
		return fmt.Errorf("unknown desktop harness language")
	}
	for name, row := range cases {
		if stringValue(row, "status") != "pass" {
			return fmt.Errorf("desktop case failed: %s", name)
		}
	}
	for path, want := range checks {
		if e = requireHash(path, want); e != nil {
			return e
		}
	}
	return nil
}

func runNativeRepairCases(a *app, o options) error {
	if e := o.require("native-packages", "reference", "output"); e != nil {
		return e
	}
	matrix, e := load(o.get("native-packages"))
	if e != nil {
		return e
	}
	if e = nativeMatrixReadback(a, matrix); e != nil {
		return e
	}
	reference, e := absolute(o.get("reference"))
	if e != nil {
		return e
	}
	execPath, e := a.mustRun(reference, "--exec-path")
	if e != nil {
		return e
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend.exe")
	if !exists(backend) {
		return fmt.Errorf("complete reference HTTP backend required")
	}
	out, e := newDestination(o.get("output"))
	if e != nil {
		return e
	}
	win := object(object(matrix, "platforms"), "windows-x64")
	binary := filepath.Join(stringValue(win, "directory"), "bin/git.exe")
	terminal, e := prepareTerminalRuntime(a, stringValue(win, "directory"), filepath.Join(out, "terminal-runtime"))
	if e != nil {
		return e
	}
	start := time.Now()
	cases := []nativeCase{{"terminal-native-desktop", toolCommand("terminal-desktop-acceptance", "--binary", filepath.Join(terminal, "bin/git-terminal.exe"), "--git", filepath.Join(terminal, "bin/git.exe"), "--output", filepath.Join(out, "terminal-desktop.json")), 20 * time.Minute}, {"windows-core-contracts", toolCommand("native-contract-acceptance", binary, "--reference", reference, "--output", filepath.Join(out, "windows-core.json")), 20 * time.Minute}, {"windows-common-cli-contracts", toolCommand("native-common-cli-acceptance", binary, "--reference", reference, "--output", filepath.Join(out, "windows-common-cli.json")), 20 * time.Minute}, {"windows-dpapi-migration", toolCommand("gcm-acceptance", binary, "--helper", stringValue(win, "gcm"), "--native-distribution"), 20 * time.Minute}}
	results := []record{}
	for _, c := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		begin := time.Now()
		r, e := runProcess(ctx, a.root, fixtureEnv(os.Environ(), map[string]string{"PATH": filepath.Dir(reference) + string(os.PathListSeparator) + os.Getenv("PATH"), "GIT_TERMINAL_PROMPT": "0", "LANG": "C", "LC_ALL": "C"}), nil, c.command[0], c.command[1:]...)
		cancel()
		log := filepath.Join(out, c.name+".log")
		if err := write(log, append(append(r.stdout, '\n'), r.stderr...), 0600); err != nil {
			return err
		}
		hash, err := digest(log)
		if err != nil {
			return err
		}
		status := "pass"
		if e != nil || r.code != 0 {
			status = "fail"
		}
		results = append(results, record{"case": c.name, "status": status, "exit_code": r.code, "command": c.command, "seconds": roundedSeconds(time.Since(begin)), "log_sha256": hash})
	}
	unchanged := nativeMatrixReadback(a, matrix) == nil
	status := "pass"
	if !unchanged {
		status = "fail"
	}
	for _, r := range results {
		if r["status"] != "pass" {
			status = "fail"
		}
	}
	report := record{"schema_version": 1, "status": status, "scope": "unchanged product/test sources; complete reference; four actual Windows reruns", "source_tree_sha256": matrix["source_tree_sha256"], "harnesses": matrix["harnesses"], "frozen_input_readback": unchanged, "results": results, "seconds": roundedSeconds(time.Since(start))}
	for key, path := range map[string]string{"matrix_sha256": o.get("native-packages"), "reference_sha256": reference, "server_backend_sha256": backend, "binary_sha256": binary} {
		report[key], e = digest(path)
		if e != nil {
			return e
		}
	}
	if e = writeNewJSON(filepath.Join(out, "repair.json"), report); e != nil {
		return e
	}
	if e = a.json(report); e != nil {
		return e
	}
	if status != "pass" {
		return fmt.Errorf("repair rerun failed")
	}
	return nil
}
