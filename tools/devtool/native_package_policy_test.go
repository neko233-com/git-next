// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func packagePolicyFixture(t *testing.T) (*app, record, string, record, record, string) {
	t.Helper()
	a := &app{root: t.TempDir(), out: io.Discard, err: io.Discard}
	matrix := record{"schema_version": 1, "product": "git-next", "version": "1.0.4", "source_tree_sha256": strings.Repeat("c", 64), "files": record{}, "harnesses": record{}, "platforms": record{}}
	put := func(path string, data []byte, collection record, key string) {
		t.Helper()
		if e := write(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		collection[key] = digestBytes(data)
	}
	sources := wordSet("")
	for name := range packageProbeSources {
		sources[name] = true
	}
	for _, name := range packageMigrationFiles {
		sources[name] = true
	}
	for name := range sources {
		put(a.path(filepath.FromSlash(name)), []byte("source fixture: "+name), object(matrix, "harnesses"), name)
	}
	fragment := "[core]\n\tautocrlf = input\n\tdeltaBaseCacheLimit = 32m\n[gc]\n\tauto = 0\n[maintenance]\n\tauto = false\n[pack]\n\tthreads = 1\n\twindowMemory = 32m\n\tdeltaCacheSize = 16m\n[lfs]\n\tconcurrenttransfers = 2\n"
	put(a.path("packaging/runtime-defaults/gitconfig"), []byte(fragment), object(matrix, "harnesses"), "packaging/runtime-defaults/gitconfig")
	d, e := readDefaults(a.root)
	if e != nil {
		t.Fatal(e)
	}
	put(a.path("packaging/windows/Takeover.Core.psm1"), []byte(`"`+d.powershell()+`"`), object(matrix, "harnesses"), "packaging/windows/Takeover.Core.psm1")
	for _, platform := range nativePlatforms {
		item := record{}
		object(matrix, "platforms")[platform] = item
		if !strings.HasPrefix(platform, "windows-") {
			path := a.path("packages", platform, "distribution.tar.gz")
			put(path, []byte("frozen "+platform+" archive"), object(matrix, "files"), path)
			item["archive"] = path
			continue
		}
		root := a.path("packages", platform)
		item["directory"] = root
		manifest := record{"schema_version": 1, "product": "git-next", "version": matrix["version"], "source_tree_sha256": matrix["source_tree_sha256"], "platform": platform, "files": record{}}
		member := func(name string, data []byte) {
			path := filepath.Join(root, filepath.FromSlash(name))
			put(path, data, object(matrix, "files"), path)
			object(manifest, "files")[name] = record{"sha256": digestBytes(data), "bytes": len(data)}
		}
		for name, source := range packageMigrationFiles {
			text, e := read(a.path(filepath.FromSlash(source)))
			if e != nil {
				t.Fatal(e)
			}
			member(name, []byte(text))
		}
		member("runtime/etc/gitconfig", []byte("[core]\n"+d.core()+"\n[http]\n\tsslBackend = schannel\n"+d.tail()))
		member("bin/git.exe", []byte("exact owned Git "+platform))
		prefix := "mingw64"
		if platform == "windows-arm64" {
			prefix = "clangarm64"
		}
		member("runtime/"+prefix+"/bin/git-lfs.exe", []byte("exact owned LFS "+platform))
		manifestPath := filepath.Join(root, "git-next-distribution.json")
		put(manifestPath, append(encoded(manifest), '\n'), object(matrix, "files"), manifestPath)
		for _, scope := range []string{"user", "machine"} {
			path := a.path("msi", "git-next-1.0.4-"+platform+"-"+scope+".msi")
			put(path, []byte("frozen MSI "+platform+" "+scope), object(matrix, "files"), path)
			item["msi_"+scope] = path
			receipt := record{"platform": platform, "install_scope": scope, "version": matrix["version"], "source_tree_sha256": matrix["source_tree_sha256"], "sha256": object(matrix, "files")[path], "asset": filepath.Base(path), "payload_files": 13, "distribution_manifest_sha256": object(matrix, "files")[manifestPath], "packaging_files": record{"tools/devtool/packaging_msi.go": object(matrix, "harnesses")["tools/devtool/packaging_msi.go"], "packaging/windows/Product.wxs": object(matrix, "harnesses")["packaging/windows/Product.wxs"]}}
			receiptPath := strings.TrimSuffix(path, ".msi") + ".receipt.json"
			put(receiptPath, encoded(receipt), object(matrix, "files"), receiptPath)
		}
	}
	identity := digestBytes(encoded(matrix))
	bindings, e := packageValidationBindings(matrix)
	if e != nil {
		t.Fatal(e)
	}
	boundSources, e := packageValidationSources(a, matrix)
	if e != nil {
		t.Fatal(e)
	}
	resources, _, e := packageResourceBindings(a, matrix)
	if e != nil {
		t.Fatal(e)
	}
	rows := []any{}
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		for _, scope := range []string{"user", "machine"} {
			binding := object(object(bindings, platform), "msi_"+scope)
			rows = append(rows, record{"case": "static-msi-" + platform + "-" + scope, "status": "pass", "method": "read-only-compiled-msi-database", "validation_executed": true, "installation_executed": false, "identity_and_replacement_guard_verified": true, "component_ownership_and_transaction_order_verified": true, "payload_files": binding["payload_files"], "asset": binding["name"]})
		}
	}
	rows = append(rows, record{"case": "isolated-takeover-migration-contracts", "status": "pass", "exit_code": 0, "report": record{"status": "pass", "cases": 47, "scope": "isolated takeover snapshot, simulated MSI removal/resume, repository and payload/stale guards", "git_or_terminal_executed": false, "installation_modified": false}})
	checks := []any{}
	for _, name := range []string{"maintenance.auto", "core.deltaBaseCacheLimit", "pack.threads", "pack.windowMemory", "pack.deltaCacheSize", "lfs.concurrenttransfers"} {
		checks = append(checks, record{"name": name, "ok": true})
	}
	rows = append(rows, record{"case": "common-resource-behavior", "status": "pass", "checks": checks}, record{"case": "frozen-input-readback", "status": "pass"})
	probe := record{"schema_version": 1, "kind": "package-validation-probes", "status": "pass", "matrix_sha256": identity, "source_tree_sha256": matrix["source_tree_sha256"], "version": matrix["version"], "installation_runtime_accepted": false, "host_msi_lifecycle_executed": false, "terminal_executed": false, "personal_configuration_accessed": false, "package_bindings": bindings, "source_bindings": boundSources, "resource_bindings": resources, "results": rows}
	policy := record{"schema_version": 1, "kind": "package-validation-policy", "authorized_by": "user", "authorization": "不需要 arm，docker 验证打包就可，能发布就行。不需要本机完全验证真实环境", "reason": "用户选择本次安装包验证范围，两个宿主 MSI 生命周期明确 waived。", "matrix_sha256": identity, "source_tree_sha256": matrix["source_tree_sha256"], "cases": []any{"msi-user", "msi-machine"}, "package_bindings": bindings, "installation_runtime_accepted": false}
	return a, matrix, identity, policy, probe, a.path("policy", packagePolicyAsset)
}

func writePackagePolicyFixture(t *testing.T, policy, probe record, path string) {
	t.Helper()
	probePath := filepath.Join(filepath.Dir(path), packageProbeAsset)
	if e := write(probePath, encoded(probe), 0600); e != nil {
		t.Fatal(e)
	}
	sha, e := digest(probePath)
	if e != nil {
		t.Fatal(e)
	}
	policy["probes"] = record{"name": packageProbeAsset, "sha256": sha, "bytes": len(encoded(probe))}
	if e = write(path, encoded(policy), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestPackageValidationPolicyBindsOnlyUserAuthorizedExactPackagesAndCompleteProbes(t *testing.T) {
	for _, mode := range []string{"valid", "no authorization", "not user", "no reason", "wrong matrix", "wrong source", "extra case", "missing case", "duplicate case", "wrong package bytes", "changed package", "changed source", "wrong probe bytes", "missing probe", "probe failed", "partial probe", "static not executed", "static wrong bytes", "migration partial", "migration installed", "resource failed", "resource partial", "resource wrong render", "duplicate resource", "readback failed", "fake installation pass", "unknown field", "private path"} {
		t.Run(mode, func(t *testing.T) {
			a, matrix, identity, policy, probe, path := packagePolicyFixture(t)
			rows, _ := caseResults(probe)
			switch mode {
			case "no authorization":
				delete(policy, "authorization")
			case "not user":
				policy["authorized_by"] = "agent"
			case "no reason":
				policy["reason"] = ""
			case "wrong matrix":
				policy["matrix_sha256"] = strings.Repeat("0", 64)
			case "wrong source":
				policy["source_tree_sha256"] = strings.Repeat("d", 64)
			case "extra case":
				policy["cases"] = append(array(policy, "cases"), "windows-core-contracts")
			case "missing case":
				policy["cases"] = []any{"msi-machine"}
			case "duplicate case":
				policy["cases"] = []any{"msi-user", "msi-user"}
			case "wrong package bytes":
				object(object(object(policy, "package_bindings"), "linux-x64"), "archive")["sha256"] = strings.Repeat("d", 64)
			case "changed package":
				p := stringValue(object(object(matrix, "platforms"), "linux-arm64"), "archive")
				if e := write(p, []byte("changed"), 0600); e != nil {
					t.Fatal(e)
				}
			case "changed source":
				if e := write(a.path("tools/devtool/defaults_resource.go"), []byte("changed source"), 0600); e != nil {
					t.Fatal(e)
				}
			case "probe failed":
				probe["status"] = "fail"
			case "partial probe":
				probe["results"] = array(probe, "results")[:2]
			case "static not executed":
				rows["static-msi-windows-arm64-machine"]["validation_executed"] = false
			case "static wrong bytes":
				rows["static-msi-windows-x64-user"]["asset"] = "another.msi"
			case "migration partial":
				object(rows["isolated-takeover-migration-contracts"], "report")["cases"] = 46
			case "migration installed":
				object(rows["isolated-takeover-migration-contracts"], "report")["installation_modified"] = true
			case "resource failed":
				rec(array(rows["common-resource-behavior"], "checks")[0])["ok"] = false
			case "resource partial":
				rows["common-resource-behavior"]["checks"] = array(rows["common-resource-behavior"], "checks")[:5]
			case "resource wrong render":
				object(probe, "resource_bindings")["rendered_resource_block_sha256"] = strings.Repeat("e", 64)
			case "duplicate resource":
				array(rows["common-resource-behavior"], "checks")[0] = array(rows["common-resource-behavior"], "checks")[1]
			case "readback failed":
				rows["frozen-input-readback"]["status"] = "fail"
			case "fake installation pass":
				probe["installation_runtime_accepted"] = true
			case "unknown field":
				policy["bypass_all"] = true
			case "private path":
				policy["reason"] = `C:\Users\private\secret`
			}
			writePackagePolicyFixture(t, policy, probe, path)
			if mode == "wrong probe bytes" {
				if e := write(filepath.Join(filepath.Dir(path), packageProbeAsset), []byte("changed"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "missing probe" {
				object(policy, "probes")["name"] = "another.json"
				if e := write(path, encoded(policy), 0600); e != nil {
					t.Fatal(e)
				}
			}
			got, e := readNativePackagePolicy(a, path, matrix, identity)
			if (e == nil) != (mode == "valid") {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
			if got != nil {
				if e = write(got.probePath, []byte("changed after acceptance"), 0600); e != nil {
					t.Fatal(e)
				}
				if got.unchanged() == nil {
					t.Fatal("probe drift after acceptance was ignored")
				}
			}
		})
	}
	if got, e := readNativePackagePolicy(&app{}, "", nil, ""); got != nil || e != nil {
		t.Fatal("missing policy invented scope", got, e)
	}
}

func TestPackageValidationPolicyPreservesOtherCorrectnessAndAllPerformanceGates(t *testing.T) {
	for _, mode := range []string{"valid", "without terminal waiver", "no policy", "wrong policy binding", "fake pass status", "msi fake pass", "msi fake exit", "msi fake runtime", "top fake runtime", "terminal fake pass", "other failed", "other waived", "readback failed", "partial batch", "performance failed", "performance waived", "performance wrong policy", "performance wrong correctness"} {
		t.Run(mode, func(t *testing.T) {
			gate, perf, matrix, identity, terminal := waivedGateFixture(t)
			policy := &nativePackagePolicy{sha: strings.Repeat("d", 64), value: record{"reason": "explicit user package scope", "cases": []any{"msi-user", "msi-machine"}}}
			gate["package_validation_policy"], perf["package_validation_policy"] = policy.proof(), policy.proof()
			gate["installation_runtime_accepted"], gate["host_msi_lifecycle_executed"] = false, false
			perf["installation_runtime_accepted"], perf["host_msi_lifecycle_executed"] = false, false
			for i, raw := range array(gate, "results") {
				row := rec(raw)
				if packageValidationCases[stringValue(row, "case")] {
					array(gate, "results")[i] = policy.result(stringValue(row, "case"))
				}
			}
			rows, _ := caseResults(gate)
			switch mode {
			case "without terminal waiver":
				terminal = nil
				delete(gate, "terminal_waiver")
				delete(perf, "terminal_waiver")
				for name := range terminalWaiverCases {
					rows[name]["status"], rows[name]["exit_code"] = "pass", 0
					delete(rows[name], "waiver_sha256")
					delete(rows[name], "reason")
				}
			case "no policy":
				policy = nil
			case "wrong policy binding":
				gate["package_validation_policy"] = record{}
			case "fake pass status":
				gate["status"] = "pass"
			case "msi fake pass":
				rows["msi-user"]["status"] = "pass"
			case "msi fake exit":
				rows["msi-machine"]["exit_code"] = 0
			case "msi fake runtime":
				rows["msi-user"]["installation_runtime_accepted"] = true
			case "top fake runtime":
				gate["host_msi_lifecycle_executed"] = true
			case "terminal fake pass":
				rows["terminal-native-desktop"]["status"] = "pass"
			case "other failed":
				rows["windows-core-contracts"]["status"] = "fail"
			case "other waived":
				rows["linux-x64"]["status"] = "waived"
			case "readback failed":
				rows["frozen-input-readback"]["status"] = "fail"
			case "partial batch":
				gate["results"] = array(gate, "results")[:3]
			case "performance failed":
				rec(array(perf, "results")[0])["status"] = "fail"
			case "performance waived":
				rec(array(perf, "results")[0])["status"] = "waived"
			case "performance wrong policy":
				delete(perf, "package_validation_policy")
			case "performance wrong correctness":
				perf["correctness_sha256"] = "another"
			}
			e := validateNativeReleaseWithPackagePolicy(gate, perf, matrix, identity, "correctness", terminal, policy)
			valid := mode == "valid" || mode == "without terminal waiver"
			if (e == nil) != valid {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
		})
	}
}

func TestPackageValidationPolicyNeverChangesOriginalUnscopedGates(t *testing.T) {
	gate, perf, matrix, identity, waiver := waivedGateFixture(t)
	if e := validateNativeReleaseWithPackagePolicy(gate, perf, matrix, identity, "correctness", waiver, nil); e != nil {
		t.Fatal(e)
	}
	rows, _ := caseResults(gate)
	rows["msi-machine"]["status"] = "waived"
	if e := validateNativeReleaseWithPackagePolicy(gate, perf, matrix, identity, "correctness", waiver, nil); e == nil {
		t.Fatal("MSI waiver was inferred without a package policy")
	}
	policy := &nativePackagePolicy{sha: "policy", value: record{"reason": "user scope"}}
	values := []record{policy.result("msi-user"), policy.result("msi-machine"), {"case": "windows-core-contracts", "status": "pass", "exit_code": 0}}
	if nativeBatchStatusWithPackagePolicy(values, nil, policy, false) != "pass-with-user-waiver" || nativeBatchStatusWithPackagePolicy(values, nil, policy, true) != "fail" {
		t.Fatal("package policy was reported as a full pass or a timing waiver")
	}
	values[2]["status"] = "fail"
	if nativeBatchStatusWithPackagePolicy(values, nil, policy, false) != "fail" {
		t.Fatal("package scope hid another failed gate")
	}
}

func TestFreezeNativeRuntimeDefaultsPinsExtensionlessFileAndRejectsDrift(t *testing.T) {
	a := &app{root: t.TempDir(), out: io.Discard, err: io.Discard}
	harnesses := record{}
	if freezeNativeRuntimeDefaults(a, harnesses) == nil {
		t.Fatal("missing authoritative fragment was frozen")
	}
	const name = "packaging/runtime-defaults/gitconfig"
	if e := write(a.path(name), []byte("frozen fragment\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := freezeNativeRuntimeDefaults(a, harnesses); e != nil {
		t.Fatal(e)
	}
	if harnesses[name] != digestBytes([]byte("frozen fragment\n")) {
		t.Fatal("extensionless fragment was not pinned")
	}
	matrix := record{"harnesses": harnesses}
	if e := nativeMatrixReadback(a, matrix); e != nil {
		t.Fatal(e)
	}
	if e := write(a.path(name), []byte("changed fragment\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if nativeMatrixReadback(a, matrix) == nil {
		t.Fatal("fragment drift bypassed frozen-input readback")
	}
}

func TestPackageValidationPolicyRejectsMissingFrozenFragment(t *testing.T) {
	a, matrix, _, policy, probe, file := packagePolicyFixture(t)
	delete(object(matrix, "harnesses"), "packaging/runtime-defaults/gitconfig")
	identity := digestBytes(encoded(matrix))
	policy["matrix_sha256"], probe["matrix_sha256"] = identity, identity
	writePackagePolicyFixture(t, policy, probe, file)
	if _, e := readNativePackagePolicy(a, file, matrix, identity); e == nil {
		t.Fatal("missing fragment pin was silently accepted")
	}
}
