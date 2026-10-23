// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestMSILifecycleProofRequiresRollbackAndOwnedInputs(t *testing.T) {
	checks := make([]any, 0, len(msiLifecycleChecks)+1)
	for _, name := range msiLifecycleChecks {
		checks = append(checks, name)
	}
	checks = append(checks, "original Git bytes preserved: fixture-reference")
	proof := record{"status": "pass", "scope": "actual Windows user MSI install/repair/upgrade/injected-failure rollback/uninstall; original Git retained", "base_sha256": "base", "upgrade_sha256": "upgrade", "checks": checks}
	for _, mode := range []string{"complete", "missing rollback", "different base", "different upgrade", "wrong scope", "incomplete", "original Git omitted", "no JSON"} {
		t.Run(mode, func(t *testing.T) {
			candidate := cloneRecord(t, proof)
			switch mode {
			case "missing rollback":
				filtered := []any{}
				for _, v := range array(candidate, "checks") {
					if v != "failed upgrade restores actual old bytes" {
						filtered = append(filtered, v)
					}
				}
				candidate["checks"] = filtered
			case "different base":
				candidate["base_sha256"] = "other"
			case "different upgrade":
				candidate["upgrade_sha256"] = "other"
			case "wrong scope":
				candidate["scope"] = "install only"
			case "incomplete":
				candidate["status"] = "incomplete"
			case "original Git omitted":
				candidate["checks"] = array(candidate, "checks")[:len(msiLifecycleChecks)]
			}
			data := append([]byte("PowerShell progress\r\n"), encoded(candidate)...)
			if mode == "no JSON" {
				data = []byte("success")
			}
			_, err := msiLifecycleProof(data, "user", "base", "upgrade")
			if (err == nil) != (mode == "complete") {
				t.Fatalf("mode=%s error=%v", mode, err)
			}
		})
	}
}

func TestMSIBatchRequiresFrozenMSIPairsAndHarness(t *testing.T) {
	platforms, files, harnesses := record{}, record{}, record{}
	for _, name := range nativePlatforms {
		platforms[name] = record{}
	}
	win := object(platforms, "windows-x64")
	for _, scope := range []string{"user", "machine"} {
		for _, prefix := range []string{"base_msi_", "msi_"} {
			path := prefix + scope + ".msi"
			win[prefix+scope], files[path] = path, "bytes"
			files[strings.TrimSuffix(path, ".msi")+".receipt.json"] = "receipt"
		}
	}
	for _, path := range []string{"tools/devtool/batch_msi.go", "tools/devtool/process.go", "tools/devtool/process_windows.go", "tests/msi_acceptance.ps1", "tests/elevated_msi_acceptance.ps1", "tests/windows_file_snapshot.cs"} {
		harnesses[path] = "source"
	}
	matrix := record{"schema_version": 1, "product": "git-next", "source_tree_sha256": strings.Repeat("a", 64), "platforms": platforms, "files": files, "harnesses": harnesses}
	if e := msiBatchInputs(matrix); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"missing platform", "missing package", "missing receipt", "missing rollback harness", "missing process bounds", "missing new helper", "unidentified source"} {
		t.Run(mode, func(t *testing.T) {
			bad := cloneRecord(t, matrix)
			switch mode {
			case "missing platform":
				delete(object(bad, "platforms"), "windows-arm64")
			case "missing package":
				delete(object(bad, "files"), "msi_machine.msi")
			case "missing receipt":
				delete(object(bad, "files"), "base_msi_user.receipt.json")
			case "missing rollback harness":
				delete(object(bad, "harnesses"), "tests/msi_acceptance.ps1")
			case "missing process bounds":
				delete(object(bad, "harnesses"), "tools/devtool/process_windows.go")
			case "missing new helper":
				delete(object(bad, "harnesses"), "tools/devtool/batch_msi.go")
			case "unidentified source":
				bad["source_tree_sha256"] = ""
			}
			if msiBatchInputs(bad) == nil {
				t.Fatal("accepted incomplete frozen lifecycle inputs")
			}
		})
	}
}

func TestMSIBatchReadbackRejectsChangedReferenceMatrixAndPayload(t *testing.T) {
	root := t.TempDir()
	a := &app{root: root, out: io.Discard, err: io.Discard}
	matrixPath, reference, payload, harness := filepath.Join(root, "matrix.json"), filepath.Join(root, "reference.exe"), filepath.Join(root, "package.msi"), a.path("tests/msi_acceptance.ps1")
	for _, mode := range []string{"unchanged", "reference", "matrix", "payload", "harness"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{matrixPath, reference, payload, harness} {
				if e := write(path, []byte("original"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			original := digestBytes([]byte("original"))
			matrix := record{"files": record{payload: original}, "harnesses": record{"tests/msi_acceptance.ps1": original}}
			path := map[string]string{"reference": reference, "matrix": matrixPath, "payload": payload, "harness": harness}[mode]
			if path != "" {
				if e := write(path, []byte("changed"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			e := msiBatchReadback(a, matrix, matrixPath, original, reference, original)
			if (e == nil) != (mode == "unchanged") {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
		})
	}
}

func TestMSIPartialBatchReusesOnlyInstallerCasesAndCannotStageRelease(t *testing.T) {
	root := t.TempDir()
	a := &app{root: root, out: io.Discard, err: io.Discard}
	matrix := record{"source_tree_sha256": "source", "files": record{"package.msi": "bytes"}, "platforms": record{"windows-x64": record{"msi": "package.msi"}}, "harnesses": record{"tests/msi_acceptance.ps1": "original"}}
	matrixPath, reportPath, perfPath := filepath.Join(root, "matrix.json"), filepath.Join(root, "batch.json"), filepath.Join(root, "performance.json")
	if e := writeNewJSON(matrixPath, matrix); e != nil {
		t.Fatal(e)
	}
	matrixSHA, e := digest(matrixPath)
	if e != nil {
		t.Fatal(e)
	}
	report := record{"schema_version": 1, "status": "pass", "phase": "correctness", "scope": "msi-lifecycle-only", "matrix_sha256": matrixSHA, "reference_sha256": "reference", "source_tree_sha256": "source", "results": []any{record{"case": "msi-user", "status": "pass", "exit_code": 0}, record{"case": "msi-machine", "status": "pass", "exit_code": 0}, record{"case": "frozen-input-readback", "status": "pass"}}}
	if e = writeNewJSON(reportPath, report); e != nil {
		t.Fatal(e)
	}
	got, e := loadReuse(a, reportPath, matrixPath, matrix, "reference", []string{"msi-user", "msi-machine"})
	if e != nil || len(got) != 2 || got["msi-user"] == nil || got["msi-machine"] == nil {
		t.Fatalf("installer reuse: %v %v", got, e)
	}
	if e = resultCases(report, keys(correctnessCases)); e == nil {
		t.Fatal("three-case MSI report accepted as complete correctness")
	}
	if e = writeNewJSON(perfPath, record{"status": "pass", "phase": "performance", "matrix_sha256": matrixSHA, "reference_sha256": "reference"}); e != nil {
		t.Fatal(e)
	}
	e = prepareNativeRelease(a, packagingOptions(map[string]string{"native-packages": matrixPath, "correctness": reportPath, "performance": perfPath, "destination": filepath.Join(root, "release"), "wix": "unexecuted", "git": "unexecuted", "source-commit": strings.Repeat("a", 40)}))
	if e == nil || !strings.Contains(e.Error(), "required acceptance cases missing") || exists(filepath.Join(root, "release")) {
		t.Fatalf("partial report must be rejected before staging: %v", e)
	}
}
