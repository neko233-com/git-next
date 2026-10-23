// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const terminalWaiverInstruction = "不用再测试git-terminal了"

var terminalWaiverCases = wordSet("terminal-rust-contracts terminal-native-desktop")
var terminalWaiverMSISources = wordSet("tools/devtool/batch_native.go tools/devtool/release_native.go tools/devtool/evidence.go tools/devtool/native_waiver.go tools/devtool/native_waiver_test.go")

type nativeWaiver struct {
	path, sha string
	value     record
}

func readNativeWaiver(a *app, path string, matrix record, matrixSHA string) (*nativeWaiver, error) {
	if path == "" {
		return nil, nil
	}
	h, e := digest(path)
	if e != nil {
		return nil, e
	}
	v, e := load(path)
	if e != nil {
		return nil, e
	}
	if number(v, "schema_version") != 1 || v["kind"] != "terminal-test-waiver" || v["authorized_by"] != "user" || !strings.Contains(stringValue(v, "authorization"), terminalWaiverInstruction) || !strings.Contains(stringValue(v, "reason"), terminalWaiverInstruction) {
		return nil, fmt.Errorf("explicit current user terminal-test authorization and reason required")
	}
	if v["matrix_sha256"] != matrixSHA || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(matrixSHA) {
		return nil, fmt.Errorf("terminal waiver identifies a different frozen matrix")
	}
	seen := map[string]bool{}
	for _, raw := range array(v, "cases") {
		name, ok := raw.(string)
		if !ok || !terminalWaiverCases[name] || seen[name] {
			return nil, fmt.Errorf("only the exact two terminal cases may be waived")
		}
		seen[name] = true
	}
	if len(seen) != 2 || len(object(v, "terminal_binaries")) != 2 {
		return nil, fmt.Errorf("both terminal cases and Windows platform identities required")
	}
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		root := stringValue(object(object(matrix, "platforms"), platform), "directory")
		if root == "" {
			return nil, fmt.Errorf("terminal package directory missing: %s", platform)
		}
		manifestPath := filepath.Join(root, "git-next-distribution.json")
		if e = requireHash(manifestPath, object(matrix, "files")[manifestPath]); e != nil {
			return nil, e
		}
		manifest, e := load(manifestPath)
		if e != nil {
			return nil, e
		}
		binding := object(object(v, "terminal_binaries"), platform)
		commit := stringValue(object(manifest, "git_terminal"), "source_commit")
		if manifest["platform"] != platform || manifest["version"] != matrix["version"] || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(commit) || binding["source_commit"] != commit || len(object(binding, "files")) != 2 {
			return nil, fmt.Errorf("terminal source commit or package identity differs: %s", platform)
		}
		for _, name := range []string{"bin/git-terminal.exe", "bin/git-terminal-shell.exe"} {
			binary := filepath.Join(root, filepath.FromSlash(name))
			want := object(binding, "files")[name]
			if want == nil || want != object(matrix, "files")[binary] || want != object(object(manifest, "files"), name)["sha256"] {
				return nil, fmt.Errorf("terminal bytes differ from waiver or frozen package: %s/%s", platform, name)
			}
			if e = requireHash(binary, want); e != nil {
				return nil, e
			}
		}
	}
	if e = requireHash(path, h); e != nil {
		return nil, e
	}
	return &nativeWaiver{path, h, v}, nil
}

func (w *nativeWaiver) proof() record {
	if w == nil {
		return nil
	}
	return record{"schema_version": 1, "kind": w.value["kind"], "authorized_by": "user", "authorization": w.value["authorization"], "reason": w.value["reason"], "matrix_sha256": w.value["matrix_sha256"], "cases": w.value["cases"], "terminal_binaries": w.value["terminal_binaries"], "sha256": w.sha, "asset": "native-terminal-waiver.json"}
}

func (w *nativeWaiver) unchanged() error {
	if w == nil {
		return nil
	}
	return requireHash(w.path, w.sha)
}

func (w *nativeWaiver) result(name string) record {
	return record{"case": name, "status": "waived", "reason": w.value["reason"], "waiver_sha256": w.sha, "seconds": 0}
}

func nativeCorrectnessCases(gate record, waiver *nativeWaiver) error {
	if waiver == nil {
		if e := resultCases(gate, keys(correctnessCases)); e != nil {
			return e
		}
		if gate["status"] != "pass" || gate["terminal_waiver"] != nil {
			return fmt.Errorf("passed correctness receipt required without a terminal waiver")
		}
	} else if gate["status"] != "pass-with-user-waiver" || !equalJSON(gate["terminal_waiver"], waiver.proof()) {
		return fmt.Errorf("correctness must explicitly identify the exact user terminal waiver")
	}
	results, e := caseResults(gate)
	if e != nil {
		return e
	}
	if len(results) != len(correctnessCases) {
		return fmt.Errorf("all 18 correctness cases required")
	}
	for name := range correctnessCases {
		item := results[name]
		if item == nil {
			return fmt.Errorf("missing correctness case: %s", name)
		}
		if waiver != nil && terminalWaiverCases[name] {
			if item["status"] != "waived" || item["waiver_sha256"] != waiver.sha || item["reason"] != waiver.value["reason"] || item["exit_code"] != nil {
				return fmt.Errorf("terminal case must retain exact waiver status: %s", name)
			}
			continue
		}
		if item["status"] != "pass" || name != "frozen-input-readback" && (item["exit_code"] == nil || number(item, "exit_code") != 0) {
			return fmt.Errorf("non-waived correctness case did not pass: %s", name)
		}
	}
	return nil
}

func validateNativeCorrectness(gate, matrix record, identity, reference string, waiver *nativeWaiver) error {
	if gate["phase"] != "correctness" || gate["matrix_sha256"] != identity || gate["source_tree_sha256"] != matrix["source_tree_sha256"] || gate["reference_sha256"] != reference {
		return fmt.Errorf("correctness must identify exact matrix/source/reference")
	}
	return nativeCorrectnessCases(gate, waiver)
}

func validateNativeReleaseGates(correctness, performance, matrix record, identity, correctnessSHA string, waiver *nativeWaiver) error {
	if e := validateNativeCorrectness(correctness, matrix, identity, stringValue(performance, "reference_sha256"), waiver); e != nil {
		return e
	}
	if performance["status"] != "pass" || performance["phase"] != "performance" || performance["matrix_sha256"] != identity || performance["source_tree_sha256"] != matrix["source_tree_sha256"] {
		return fmt.Errorf("passed timing gate must identify exact matrix/source")
	}
	if waiver == nil && performance["terminal_waiver"] != nil || waiver != nil && (!equalJSON(performance["terminal_waiver"], waiver.proof()) || performance["correctness_sha256"] != correctnessSHA) {
		return fmt.Errorf("performance must bind the same exact authorized correctness report")
	}
	if e := resultCases(performance, []string{"performance-1000", "performance-10000", "performance-100000", "performance-network", "frozen-input-readback"}); e != nil {
		return e
	}
	for _, raw := range array(performance, "results") {
		item := rec(raw)
		if item["case"] != "frozen-input-readback" && (item["exit_code"] == nil || number(item, "exit_code") != 0) {
			return fmt.Errorf("timing case lacks actual successful process result: %s", item["case"])
		}
	}
	return nil
}

func nativeBatchStatus(results []record, waiver *nativeWaiver, performance bool) string {
	status := "pass"
	for _, item := range results {
		if item["status"] == "pass" {
			continue
		}
		if !performance && waiver != nil && terminalWaiverCases[stringValue(item, "case")] && item["status"] == "waived" && item["waiver_sha256"] == waiver.sha {
			status = "pass-with-user-waiver"
			continue
		}
		return "fail"
	}
	return status
}

func terminalWaiverMSIReuseSource(name string) bool {
	return terminalWaiverMSISources[name]
}
