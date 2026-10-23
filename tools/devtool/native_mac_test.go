// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func macReceiptFixture(t *testing.T) (*app, record, string, string) {
	t.Helper()
	root := t.TempDir()
	for name := range wordSet("native_mac.go archive.go inputs.go build.go files.go main.go process.go process_unix.go posix_images.go go.mod go.sum") {
		if e := write(filepath.Join(root, "tools/devtool", name), []byte("fixture "+name), 0644); e != nil {
			t.Fatal(e)
		}
	}
	a := &app{root: root}
	harnesses, e := macRuntimeHarnesses(a)
	if e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(root, "package.tar.gz")
	if e = os.WriteFile(archive, []byte("immutable package bytes\n"), 0644); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(archive)
	hash, _ := digest(archive)
	static := record{"status": "passed", "platform": "macos-arm64", "files_inspected": 1, "native_images": []any{"bin/git"}, "shell_entrypoints": []any{"install.sh"}, "failures": []any{}}
	r := record{"schema_version": 1, "case_set_version": 1, "product": "git-next", "platform": "macos-arm64", "version": "1.0.3", "status": "pass", "native_executed": true, "personal_profile_accessed": false, "source_tree_sha256": "source-fixture", "harness_language": "go", "cpu_affinity_constraint_supported": false, "archive_sha256": hash, "archive_bytes": info.Size(), "harnesses": harnesses, "harnesses_before": harnesses, "harnesses_after": harnesses, "static_report": static, "static_report_sha256": digestBytes(encoded(static)), "passed_cases": 51}
	lanes := []any{}
	for _, lane := range macRuntimeLanes {
		rows := []any{}
		for _, id := range macRuntimeCaseIDs(lane) {
			item := record{"case": id, "status": "pass"}
			if lane == "common" {
				before, format, _ := strings.Cut(id, ":")
				item["case"], item["object_format"] = before, format
			}
			rows = append(rows, item)
		}
		report := record{"status": "pass", "results": rows, "cpu_affinity_constraint_supported": false}
		lanes = append(lanes, record{"case": lane, "status": "pass", "cases": len(rows), "exit_code": 0, "report": report, "report_sha256": digestBytes(encoded(report))})
	}
	r["results"] = lanes
	r["git_p4_executed"] = true
	r["git_p4_dispatch"] = record{"status": "pass", "empty_path": true, "git_override_present": false, "personal_p4_configuration_accessed": false, "results": []any{
		record{"entrypoint": "git p4 branches", "status": "pass", "exit_code": 0, "stdout_sha256": digestBytes([]byte("p4/fixture <= //depot/ (1)\n"))},
		record{"entrypoint": "bin/git-p4 branches", "status": "pass", "exit_code": 0, "stdout_sha256": digestBytes([]byte("p4/fixture <= //depot/ (1)\n"))},
	}}
	return a, r, archive, filepath.Join(root, "runtime.json")
}

func TestMacRuntimeReceiptRequiresCompleteBoundEvidence(t *testing.T) {
	mutations := map[string]func(record){
		"missing-p4-exit":  func(r record) { delete(rec(array(object(r, "git_p4_dispatch"), "results")[0]), "exit_code") },
		"missing-owned-p4": func(r record) { delete(r, "git_p4_dispatch") },
		"missing-p4":       func(r record) { delete(r, "git_p4_executed") },
		"complete":         func(record) {},
		"missing-lane":     func(r record) { r["results"] = array(r, "results")[:3] },
		"static-only":      func(r record) { r["native_executed"] = false },
		"archive-size":     func(r record) { r["archive_bytes"] = number(r, "archive_bytes") + 1 },
		"before-harness":   func(r record) { r["harnesses_before"] = record{} },
		"after-harness":    func(r record) { r["harnesses_after"] = record{} },
		"missing-exit":     func(r record) { delete(rec(array(r, "results")[0]), "exit_code") },
		"missing-subcase": func(r record) {
			lane := rec(array(r, "results")[0])
			report := object(lane, "report")
			report["results"] = array(report, "results")[1:]
			lane["report_sha256"] = digestBytes(encoded(report))
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			a, r, archive, output := macReceiptFixture(t)
			mutate(r)
			if e := writeNewJSON(output, r); e != nil {
				t.Fatal(e)
			}
			_, e := checkedMacRuntimeReceipt(a, output, archive, "source-fixture", "1.0.3")
			if (e == nil) != (name == "complete") {
				t.Fatalf("unexpected evidence validation: %v", e)
			}
		})
	}
}
