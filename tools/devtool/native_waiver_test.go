// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func nativeWaiverFixture(t *testing.T) (*app, record, string, record, string) {
	t.Helper()
	a := &app{root: t.TempDir(), out: io.Discard, err: io.Discard}
	matrix := record{"schema_version": 1, "product": "git-next", "version": "1.0.3", "source_tree_sha256": strings.Repeat("c", 64), "platforms": record{}, "files": record{}}
	waiver := record{"schema_version": 1, "kind": "terminal-test-waiver", "authorized_by": "user", "authorization": "不用再测试git-terminal了，其他没问题全部push和发布，维护.gitignore", "reason": "用户明确授权：不用再测试git-terminal了；保留其他全部门禁。", "cases": []any{"terminal-rust-contracts", "terminal-native-desktop"}, "terminal_binaries": record{}}
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		root := a.path("packages", platform)
		commit := strings.Repeat("a", 40)
		manifest := record{"version": "1.0.3", "platform": platform, "git_terminal": record{"source_commit": commit}, "files": record{}}
		binding := record{"source_commit": commit, "files": record{}}
		for _, name := range []string{"bin/git-terminal.exe", "bin/git-terminal-shell.exe"} {
			path, data := filepath.Join(root, filepath.FromSlash(name)), []byte(platform+" "+name)
			if e := write(path, data, 0644); e != nil {
				t.Fatal(e)
			}
			h := digestBytes(data)
			object(manifest, "files")[name] = record{"sha256": h}
			object(binding, "files")[name], object(matrix, "files")[path] = h, h
		}
		manifestPath := filepath.Join(root, "git-next-distribution.json")
		if e := writeNewJSON(manifestPath, manifest); e != nil {
			t.Fatal(e)
		}
		h, e := digest(manifestPath)
		if e != nil {
			t.Fatal(e)
		}
		object(matrix, "files")[manifestPath] = h
		object(matrix, "platforms")[platform] = record{"directory": root}
		object(waiver, "terminal_binaries")[platform] = binding
	}
	matrixSHA := digestBytes(encoded(matrix))
	waiver["matrix_sha256"] = matrixSHA
	path := a.path("terminal-waiver.json")
	return a, matrix, matrixSHA, waiver, path
}

func TestNativeTerminalWaiverBindsAuthorizationMatrixAndBothPlatforms(t *testing.T) {
	for _, mode := range []string{"valid", "no authorization", "not user", "missing reason", "wrong matrix", "extra case", "missing case", "duplicate case", "missing platform", "wrong commit", "wrong bytes", "changed binary", "changed manifest"} {
		t.Run(mode, func(t *testing.T) {
			a, matrix, identity, value, path := nativeWaiverFixture(t)
			switch mode {
			case "no authorization":
				delete(value, "authorization")
			case "not user":
				value["authorized_by"] = "agent"
			case "missing reason":
				value["reason"] = "save time"
			case "wrong matrix":
				value["matrix_sha256"] = strings.Repeat("0", 64)
			case "extra case":
				value["cases"] = append(array(value, "cases"), "msi-machine")
			case "missing case":
				value["cases"] = array(value, "cases")[:1]
			case "duplicate case":
				value["cases"] = []any{"terminal-native-desktop", "terminal-native-desktop"}
			case "missing platform":
				delete(object(value, "terminal_binaries"), "windows-arm64")
			case "wrong commit":
				object(object(value, "terminal_binaries"), "windows-x64")["source_commit"] = strings.Repeat("b", 40)
			case "wrong bytes":
				object(object(object(value, "terminal_binaries"), "windows-arm64"), "files")["bin/git-terminal-shell.exe"] = strings.Repeat("b", 64)
			case "changed binary", "changed manifest":
				root := stringValue(object(object(matrix, "platforms"), "windows-x64"), "directory")
				name := "bin/git-terminal.exe"
				if mode == "changed manifest" {
					name = "git-next-distribution.json"
				}
				if e := write(filepath.Join(root, filepath.FromSlash(name)), []byte("changed"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			if e := writeNewJSON(path, value); e != nil {
				t.Fatal(e)
			}
			got, e := readNativeWaiver(a, path, matrix, identity)
			if (e == nil) != (mode == "valid") {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
			if e == nil {
				if e = write(path, []byte("changed authorization"), 0644); e != nil {
					t.Fatal(e)
				}
				if got.unchanged() == nil {
					t.Fatal("accepted waiver changed during execution")
				}
			}
		})
	}
	a, matrix, identity, _, _ := nativeWaiverFixture(t)
	if got, e := readNativeWaiver(a, "", matrix, identity); e != nil || got != nil {
		t.Fatalf("absent waiver must not invent authorization: %v %v", got, e)
	}
}

func waivedGateFixture(t *testing.T) (record, record, record, string, *nativeWaiver) {
	t.Helper()
	a, matrix, identity, value, path := nativeWaiverFixture(t)
	if e := writeNewJSON(path, value); e != nil {
		t.Fatal(e)
	}
	w, e := readNativeWaiver(a, path, matrix, identity)
	if e != nil {
		t.Fatal(e)
	}
	rows := []any{}
	for _, name := range keys(correctnessCases) {
		item := record{"case": name, "status": "pass", "exit_code": 0}
		if terminalWaiverCases[name] {
			item = w.result(name)
		}
		rows = append(rows, item)
	}
	gate := record{"status": "pass-with-user-waiver", "phase": "correctness", "matrix_sha256": identity, "source_tree_sha256": matrix["source_tree_sha256"], "reference_sha256": "reference", "terminal_waiver": w.proof(), "results": rows}
	perfRows := []any{}
	for _, name := range []string{"performance-1000", "performance-10000", "performance-100000", "performance-network", "frozen-input-readback"} {
		perfRows = append(perfRows, record{"case": name, "status": "pass", "exit_code": 0})
	}
	performance := record{"status": "pass", "phase": "performance", "matrix_sha256": identity, "source_tree_sha256": matrix["source_tree_sha256"], "reference_sha256": "reference", "terminal_waiver": w.proof(), "correctness_sha256": "correctness", "results": perfRows}
	return gate, performance, matrix, identity, w
}

func TestNativeTerminalWaiverNeverHidesAnotherFailedOrWaivedGate(t *testing.T) {
	for _, mode := range []string{"valid", "no waiver", "fake all-pass status", "missing waiver binding", "nonterminal failed", "nonterminal waived", "readback failed", "terminal fake pass", "terminal fake exit", "terminal wrong waiver", "missing case", "duplicate case", "performance waived", "performance failed", "performance missing exit", "performance wrong correctness", "performance wrong waiver"} {
		t.Run(mode, func(t *testing.T) {
			gate, performance, matrix, identity, w := waivedGateFixture(t)
			results, _ := caseResults(gate)
			switch mode {
			case "no waiver":
				w = nil
			case "fake all-pass status":
				gate["status"] = "pass"
			case "missing waiver binding":
				delete(gate, "terminal_waiver")
			case "nonterminal failed":
				results["msi-machine"]["status"] = "fail"
			case "nonterminal waived":
				results["windows-core-contracts"] = w.result("windows-core-contracts")
				for i, raw := range array(gate, "results") {
					if rec(raw)["case"] == "windows-core-contracts" {
						array(gate, "results")[i] = results["windows-core-contracts"]
					}
				}
			case "readback failed":
				results["frozen-input-readback"]["status"] = "fail"
			case "terminal fake pass":
				results["terminal-native-desktop"]["status"] = "pass"
			case "terminal fake exit":
				results["terminal-rust-contracts"]["exit_code"] = 0
			case "terminal wrong waiver":
				results["terminal-rust-contracts"]["waiver_sha256"] = "other"
			case "missing case":
				gate["results"] = array(gate, "results")[1:]
			case "duplicate case":
				gate["results"] = append(array(gate, "results"), array(gate, "results")[0])
			case "performance waived":
				rec(array(performance, "results")[0])["status"] = "waived"
			case "performance failed":
				rec(array(performance, "results")[0])["status"] = "fail"
			case "performance missing exit":
				delete(rec(array(performance, "results")[0]), "exit_code")
			case "performance wrong correctness":
				performance["correctness_sha256"] = "other"
			case "performance wrong waiver":
				delete(performance, "terminal_waiver")
			}
			e := validateNativeReleaseGates(gate, performance, matrix, identity, "correctness", w)
			if (e == nil) != (mode == "valid") {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
		})
	}
}

func TestNativeTerminalWaiverBatchStatusesRemainHonest(t *testing.T) {
	_, _, _, _, w := waivedGateFixture(t)
	rows := []record{record{"case": "msi-user", "status": "pass"}, w.result("terminal-native-desktop"), w.result("terminal-rust-contracts")}
	if nativeBatchStatus(rows, w, false) != "pass-with-user-waiver" || nativeBatchStatus(rows, nil, false) != "fail" || nativeBatchStatus(rows, w, true) != "fail" {
		t.Fatal("terminal waiver was treated as ordinary pass or performance authorization")
	}
	rows[0]["status"] = "fail"
	if nativeBatchStatus(rows, w, false) != "fail" {
		t.Fatal("terminal waiver hid failed MSI")
	}
}

func TestNativeTerminalWaiverKeepsOrdinaryPassedReleaseGates(t *testing.T) {
	gate, performance, matrix, identity, _ := waivedGateFixture(t)
	gate["status"] = "pass"
	delete(gate, "terminal_waiver")
	delete(performance, "terminal_waiver")
	for _, raw := range array(gate, "results") {
		item := rec(raw)
		if terminalWaiverCases[stringValue(item, "case")] {
			item["status"], item["exit_code"] = "pass", 0
			delete(item, "waiver_sha256")
			delete(item, "reason")
		}
	}
	if e := validateNativeReleaseGates(gate, performance, matrix, identity, "correctness", nil); e != nil {
		t.Fatal(e)
	}
}

func TestTerminalWaiverMSIReuseStillPinsProcessesAndInstallerSources(t *testing.T) {
	a := &app{root: t.TempDir(), out: io.Discard, err: io.Discard}
	previous := record{"source_tree_sha256": "source", "files": record{"package.msi": "same"}, "platforms": record{"windows-x64": record{"msi": "same"}}, "harnesses": record{}}
	report := record{"phase": "correctness", "source_tree_sha256": "source", "reference_sha256": "reference", "results": []any{record{"case": "msi-user", "status": "pass", "exit_code": 0}, record{"case": "msi-machine", "status": "pass", "exit_code": 0}}}
	for _, name := range []string{"tools/devtool/native_waiver.go", "tools/devtool/native_waiver_test.go", "tools/devtool/batch_native.go", "tools/devtool/release_native.go", "tools/devtool/evidence.go", "tools/devtool/process.go", "tools/devtool/process_windows.go", "tools/devtool/main.go", "tools/devtool/files.go", "tools/devtool/packaging_msi.go", "tools/devtool/batch_msi.go", "tests/msi_acceptance.ps1", "tests/elevated_msi_acceptance.ps1", "packaging/windows/Product.wxs"} {
		t.Run(name, func(t *testing.T) {
			current := cloneRecord(t, previous)
			object(current, "harnesses")[name] = "changed"
			_, e := validateReuseAt(a, report, previous, current, "reference", []string{"msi-user", "msi-machine"})
			if (e == nil) != terminalWaiverMSIReuseSource(name) {
				t.Fatalf("name=%s error=%v", name, e)
			}
		})
	}
}
