// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

func init() {
	register("native-msi-acceptance", "Run serialized actual user/machine MSI lifecycle gates without desktop lanes", "native-packages reference output", nativeMSIAcceptance)
}

var msiLifecycleChecks = []string{
	"base product registered",
	"actual installed product identity",
	"repair preserves actual payload bytes",
	"failed upgrade restores old registration",
	"failed upgrade restores actual old bytes",
	"failed upgrade restores updater task",
	"newer product replaces old registration",
	"upgraded native bytes identify requested version",
	"downgrade leaves newer product registered",
	"product registration removed",
	"owned task removed",
	"selected scope PATH restored exactly",
	"no SSH profile files created",
}

func msiLifecycleProof(data []byte, scope, base, upgrade string) (record, error) {
	var proof record
	// PowerShell may print progress before its final JSON object.
	for _, start := range append([]int{0}, msiJSONStarts(data)...) {
		if decodeJSON(bytes.TrimSpace(data[start:]), &proof) == nil && proof != nil {
			break
		}
		proof = nil
	}
	if proof == nil || proof["status"] != "pass" || proof["base_sha256"] != base || proof["upgrade_sha256"] != upgrade {
		return nil, fmt.Errorf("MSI lifecycle lacks a passed exact-package proof")
	}
	wantScope := "actual Windows " + scope + " MSI install/repair/upgrade/injected-failure rollback/uninstall; original Git retained"
	if proof["scope"] != wantScope {
		return nil, fmt.Errorf("MSI lifecycle proof scope differs")
	}
	checks := map[string]bool{}
	for _, raw := range array(proof, "checks") {
		if name, ok := raw.(string); ok {
			checks[name] = true
		}
	}
	for _, name := range msiLifecycleChecks {
		if !checks[name] {
			return nil, fmt.Errorf("MSI lifecycle assertion missing: %s", name)
		}
	}
	for name := range checks {
		if strings.HasPrefix(name, "original Git bytes preserved: ") {
			return proof, nil
		}
	}
	return nil, fmt.Errorf("MSI lifecycle lacks original Git preservation proof")
}

func msiJSONStarts(data []byte) []int {
	var starts []int
	for i := 1; i < len(data); i++ {
		if data[i-1] == '\n' && data[i] == '{' {
			starts = append(starts, i)
		}
	}
	return starts
}

func msiBatchInputs(matrix record) error {
	platforms := object(matrix, "platforms")
	if number(matrix, "schema_version") != 1 || matrix["product"] != "git-next" || len(platforms) != len(nativePlatforms) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(stringValue(matrix, "source_tree_sha256")) {
		return fmt.Errorf("complete frozen native package matrix required")
	}
	for _, platform := range nativePlatforms {
		if object(platforms, platform) == nil {
			return fmt.Errorf("missing native platform: %s", platform)
		}
	}
	files, harnesses := object(matrix, "files"), object(matrix, "harnesses")
	win := object(platforms, "windows-x64")
	for _, scope := range []string{"user", "machine"} {
		for _, prefix := range []string{"base_msi_", "msi_"} {
			path := stringValue(win, prefix+scope)
			if path == "" || files[path] == nil || !strings.HasSuffix(strings.ToLower(path), ".msi") || files[strings.TrimSuffix(path, filepath.Ext(path))+".receipt.json"] == nil {
				return fmt.Errorf("MSI and packaging receipt must be frozen: %s%s", prefix, scope)
			}
		}
	}
	for _, name := range []string{"tools/devtool/batch_msi.go", "tools/devtool/process.go", "tools/devtool/process_windows.go", "tests/msi_acceptance.ps1", "tests/elevated_msi_acceptance.ps1", "tests/windows_file_snapshot.cs"} {
		if harnesses[name] == nil {
			return fmt.Errorf("MSI validation source must be frozen: %s", name)
		}
	}
	return nil
}

func msiBatchReadback(a *app, matrix record, matrixPath, matrixSHA, reference, referenceSHA string) error {
	if e := nativeMatrixReadback(a, matrix); e != nil {
		return e
	}
	if e := requireHash(matrixPath, matrixSHA); e != nil {
		return e
	}
	return requireHash(reference, referenceSHA)
}

func nativeMSIAcceptance(a *app, o options) error {
	if e := o.require("native-packages", "reference", "output"); e != nil {
		return e
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("actual MSI lifecycle acceptance requires Windows")
	}
	if e := requireImplementationComplete(a); e != nil {
		return e
	}
	matrixPath, e := absolute(o.get("native-packages"))
	if e != nil {
		return e
	}
	identity, e := digest(matrixPath)
	if e != nil {
		return e
	}
	matrix, e := load(matrixPath)
	if e != nil {
		return e
	}
	if e = msiBatchInputs(matrix); e != nil {
		return e
	}
	reference, e := absolute(o.get("reference"))
	if e != nil {
		return e
	}
	referenceSHA, e := digest(reference)
	if e != nil {
		return e
	}
	if e = msiBatchReadback(a, matrix, matrixPath, identity, reference, referenceSHA); e != nil {
		return e
	}
	out, e := newDestination(o.get("output"))
	if e != nil {
		return e
	}
	startedBatch := time.Now()
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32/WindowsPowerShell/v1.0/powershell.exe")
	env := fixtureEnv(os.Environ(), map[string]string{"GIT_TERMINAL_PROMPT": "0", "LANG": "C", "LC_ALL": "C", "PATH": filepath.Dir(reference) + string(os.PathListSeparator) + os.Getenv("PATH")})
	win, results := object(object(matrix, "platforms"), "windows-x64"), []record{}
	for _, scope := range []string{"user", "machine"} {
		name, script := "msi-"+scope, "tests/msi_acceptance.ps1"
		if scope == "machine" {
			script = "tests/elevated_msi_acceptance.ps1"
		}
		args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", a.path(filepath.FromSlash(script)), "-Package", stringValue(win, "base_msi_"+scope), "-UpgradePackage", stringValue(win, "msi_"+scope)}
		if scope == "machine" {
			args = append(args, "-Output", filepath.Join(out, "msi-machine-elevated.log"))
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		value, runErr := runProcess(ctx, a.root, env, nil, shell, args...)
		cancel()
		item := record{"case": name, "status": "pass", "exit_code": value.code, "seconds": roundedSeconds(time.Since(started)), "started_seconds_from_batch": roundedSeconds(started.Sub(startedBatch)), "finished_seconds_from_batch": roundedSeconds(time.Since(startedBatch))}
		logPath := filepath.Join(out, name+".log")
		log := append(append(append([]byte{}, value.stdout...), '\n'), value.stderr...)
		if logErr := write(logPath, log, 0644); logErr != nil {
			runErr = logErr
		} else {
			item["log_sha256"] = digestBytes(log)
		}
		if runErr != nil {
			item["status"], item["reason"] = "fail", runErr.Error()
			delete(item, "exit_code")
		} else if value.code != 0 {
			item["status"] = "fail"
		} else {
			proof, proofErr := msiLifecycleProof(value.stdout, scope, stringValue(object(matrix, "files"), stringValue(win, "base_msi_"+scope)), stringValue(object(matrix, "files"), stringValue(win, "msi_"+scope)))
			if proofErr == nil {
				proofErr = writeNewJSON(filepath.Join(out, name+".json"), proof)
			}
			if proofErr != nil {
				item["status"], item["reason"] = "fail", proofErr.Error()
			}
		}
		results = append(results, item)
		fmt.Fprintf(a.out, "%s => %s\n", name, item["status"])
	}
	readback := record{"case": "frozen-input-readback", "status": "pass"}
	if e = msiBatchReadback(a, matrix, matrixPath, identity, reference, referenceSHA); e != nil {
		readback["status"], readback["reason"] = "fail", e.Error()
	}
	results = append(results, readback)
	status := "pass"
	for _, item := range results {
		if item["status"] != "pass" {
			status = "fail"
		}
	}
	report := record{"schema_version": 1, "status": status, "phase": "correctness", "scope": "msi-lifecycle-only", "matrix_sha256": identity, "reference_sha256": referenceSHA, "source_tree_sha256": matrix["source_tree_sha256"], "results": results, "release_eligible": false, "workers": 1, "msi_transactions_serialized": true, "elapsed_seconds": roundedSeconds(time.Since(startedBatch)), "personal_account_accessed": false}
	if e = writeNewJSON(filepath.Join(out, "batch.json"), report); e != nil {
		return e
	}
	if e = a.json(report); e != nil {
		return e
	}
	if status != "pass" {
		return fmt.Errorf("MSI lifecycle acceptance failed; see batch.json")
	}
	return nil
}
