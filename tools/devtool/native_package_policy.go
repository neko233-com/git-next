// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var packageValidationCases = wordSet("msi-user msi-machine")

const packagePolicyAsset = "native-package-validation-policy.json"
const packageProbeAsset = "native-package-validation-probes.json"

var packageMigrationFiles = map[string]string{
	"migration/takeover.ps1":            "packaging/windows/takeover.ps1",
	"migration/Takeover.Core.psm1":      "packaging/windows/Takeover.Core.psm1",
	"updater/Update.Core.psm1":          "packaging/windows/Update.Core.psm1",
	"updater/Scope.Core.ps1":            "packaging/windows/Scope.Core.ps1",
	"updater/Scope.Native.cs":           "packaging/windows/Scope.Native.cs",
	"updater/Runtime.Configuration.ps1": "packaging/windows/Runtime.Configuration.ps1",
	"updater/messages.zh-CN.json":       "packaging/windows/messages.zh-CN.json",
}

var packageProbeSources = wordSet("tools/devtool/native_package_policy.go tools/devtool/packaging_msi.go tools/devtool/msi_windows.go tools/devtool/defaults.go tools/devtool/defaults_resource.go tools/devtool/process.go tools/devtool/process_windows.go tools/devtool/acceptance_common.go tests/takeover_registration_acceptance.ps1 packaging/runtime-defaults/gitconfig packaging/windows/Product.wxs install.ps1")

type nativePackagePolicy struct {
	path, sha, probePath, probeSHA string
	value                          record
}

func packagePublicValue(value any) bool {
	switch value := value.(type) {
	case string:
		return !regexp.MustCompile(`(?i)\b[a-z]:[\\/]|\\\\|/(?:Users|home|root|tmp)/|-----BEGIN|\bBearer\s`).MatchString(value)
	case map[string]any:
		for key, child := range value {
			if !packagePublicValue(key) || !packagePublicValue(child) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !packagePublicValue(child) {
				return false
			}
		}
	}
	return true
}

func init() {
	register("native-package-validation-probes", "Verify pinned packages, isolated migration and six real resource-default probes without MSI installation", "native-packages output", nativePackageValidationProbes)
}

func packagePinnedFile(matrix record, path string) (record, error) {
	want := object(matrix, "files")[path]
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(fmt.Sprint(want)) {
		return nil, fmt.Errorf("package validation file is not frozen")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("package validation requires a regular frozen file")
	}
	if e = requireHash(path, want); e != nil {
		return nil, fmt.Errorf("package validation file differs from frozen bytes")
	}
	return record{"name": filepath.Base(path), "bytes": info.Size(), "sha256": want}, nil
}

func packageValidationBindings(matrix record) (record, error) {
	platforms := object(matrix, "platforms")
	if number(matrix, "schema_version") != 1 || matrix["product"] != "git-next" || len(platforms) != len(nativePlatforms) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(stringValue(matrix, "source_tree_sha256")) {
		return nil, fmt.Errorf("package policy requires the exact complete native matrix")
	}
	bindings := record{}
	for _, platform := range nativePlatforms {
		item := object(platforms, platform)
		if item == nil {
			return nil, fmt.Errorf("package validation platform missing")
		}
		bound := record{}
		paths := map[string]string{"archive": stringValue(item, "archive")}
		if strings.HasPrefix(platform, "windows-") {
			root := stringValue(item, "directory")
			if root == "" {
				return nil, fmt.Errorf("Windows package directory missing")
			}
			paths = map[string]string{"distribution_manifest": filepath.Join(root, "git-next-distribution.json")}
			manifest, e := load(paths["distribution_manifest"])
			if e != nil || manifest["platform"] != platform || manifest["version"] != matrix["version"] || manifest["product"] != "git-next" || manifest["source_tree_sha256"] != matrix["source_tree_sha256"] {
				return nil, fmt.Errorf("Windows package identity differs from matrix")
			}
			for _, scope := range []string{"user", "machine"} {
				msi := stringValue(item, "msi_"+scope)
				if !strings.HasSuffix(msi, ".msi") {
					return nil, fmt.Errorf("frozen MSI scope missing")
				}
				paths["msi_"+scope] = msi
				paths["msi_"+scope+"_receipt"] = strings.TrimSuffix(msi, ".msi") + ".receipt.json"
				receipt, e := load(paths["msi_"+scope+"_receipt"])
				if e != nil || receipt["platform"] != platform || receipt["install_scope"] != scope || receipt["version"] != matrix["version"] || receipt["source_tree_sha256"] != matrix["source_tree_sha256"] || receipt["sha256"] != object(matrix, "files")[msi] || receipt["asset"] != filepath.Base(msi) || number(receipt, "payload_files") < 1 || receipt["distribution_manifest_sha256"] != object(matrix, "files")[paths["distribution_manifest"]] {
					return nil, fmt.Errorf("frozen MSI packaging receipt differs from package identity")
				}
				for name, want := range object(receipt, "packaging_files") {
					if object(matrix, "harnesses")[name] != want {
						return nil, fmt.Errorf("MSI recipe is not the frozen recipe")
					}
				}
				if len(object(receipt, "packaging_files")) != 2 || object(receipt, "packaging_files")["tools/devtool/packaging_msi.go"] == nil || object(receipt, "packaging_files")["packaging/windows/Product.wxs"] == nil {
					return nil, fmt.Errorf("MSI packaging receipt omits the exact compiler and recipe")
				}
			}
		}
		for role, path := range paths {
			entry, e := packagePinnedFile(matrix, path)
			if e != nil {
				return nil, e
			}
			if role == "msi_user" || role == "msi_machine" {
				receipt, e := load(strings.TrimSuffix(path, ".msi") + ".receipt.json")
				if e != nil {
					return nil, e
				}
				entry["payload_files"] = receipt["payload_files"]
			}
			bound[role] = entry
		}
		bindings[platform] = bound
	}
	return bindings, nil
}

func packageValidationSources(a *app, matrix record) (record, error) {
	names := wordSet("")
	for name := range packageProbeSources {
		names[name] = true
	}
	for _, name := range packageMigrationFiles {
		names[name] = true
	}
	values := record{}
	for name := range names {
		want := object(matrix, "harnesses")[name]
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(fmt.Sprint(want)) || requireHash(a.path(filepath.FromSlash(name)), want) != nil {
			return nil, fmt.Errorf("package/migration/resource probe source differs from matrix: %s", name)
		}
		values[name] = want
	}
	return values, nil
}

func packageResourceBindings(a *app, matrix record) (record, defaults, error) {
	d, agreementChecks, e := agreement(a)
	if e != nil {
		return nil, d, e
	}
	for _, check := range agreementChecks {
		if !check.OK {
			return nil, d, fmt.Errorf("resource fragment and migration literal disagree")
		}
	}
	resources, e := d.resources()
	if e != nil {
		return nil, d, e
	}
	fragment, e := digest(a.path("packaging/runtime-defaults/gitconfig"))
	if e != nil || fragment != object(matrix, "harnesses")["packaging/runtime-defaults/gitconfig"] {
		return nil, d, fmt.Errorf("resource fragment differs from frozen source")
	}
	values := record{"fragment_sha256": fragment, "rendered_resource_block_sha256": digestBytes([]byte(resources)), "rendered_full_block_sha256": digestBytes([]byte(d.block())), "migration_literal_matches_fragment": true, "windows_packages": record{}}
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		root := stringValue(object(object(matrix, "platforms"), platform), "directory")
		manifestPath := filepath.Join(root, "git-next-distribution.json")
		manifest, e := load(manifestPath)
		if e != nil {
			return nil, d, e
		}
		members := record{}
		for member, source := range packageMigrationFiles {
			want := object(matrix, "harnesses")[source]
			path := filepath.Join(root, filepath.FromSlash(member))
			if want == nil || object(matrix, "files")[path] != want || object(object(manifest, "files"), member)["sha256"] != want || requireHash(path, want) != nil {
				return nil, d, fmt.Errorf("migration payload differs from frozen probe source")
			}
			members[member] = want
		}
		configPath := filepath.Join(root, "runtime/etc/gitconfig")
		config, e := os.ReadFile(configPath)
		if e != nil || object(matrix, "files")[configPath] != digestBytes(config) || object(object(manifest, "files"), "runtime/etc/gitconfig")["sha256"] != digestBytes(config) || bytes.Count(config, []byte(d.core())) != 1 || !bytes.HasSuffix(config, []byte(d.tail())) {
			return nil, d, fmt.Errorf("packaged resource defaults differ from rendered fragment")
		}
		binaries := record{}
		prefix := "mingw64"
		if platform == "windows-arm64" {
			prefix = "clangarm64"
		}
		for _, member := range []string{"bin/git.exe", "runtime/" + prefix + "/bin/git-lfs.exe"} {
			path := filepath.Join(root, filepath.FromSlash(member))
			want := object(matrix, "files")[path]
			if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(fmt.Sprint(want)) || object(object(manifest, "files"), member)["sha256"] != want || requireHash(path, want) != nil {
				return nil, d, fmt.Errorf("resource probe requires exact manifest-bound owned Git and LFS bytes")
			}
			binaries[member] = want
		}
		object(values, "windows_packages")[platform] = record{"distribution_manifest_sha256": object(matrix, "files")[manifestPath], "runtime_gitconfig_sha256": digestBytes(config), "migration_modules": members, "owned_binaries": binaries}
	}
	return values, d, nil
}

func packageProbeCases() map[string]bool {
	return wordSet("static-msi-windows-x64-user static-msi-windows-x64-machine static-msi-windows-arm64-user static-msi-windows-arm64-machine isolated-takeover-migration-contracts common-resource-behavior frozen-input-readback")
}

func validatePackageProbeReport(probe, matrix record, identity string, bindings, sources, resources record) error {
	allowed := wordSet("schema_version kind status matrix_sha256 source_tree_sha256 version installation_runtime_accepted host_msi_lifecycle_executed terminal_executed personal_configuration_accessed package_bindings source_bindings resource_bindings results seconds")
	for key := range probe {
		if !allowed[key] {
			return fmt.Errorf("unknown field in public package validation probes")
		}
	}
	if !packagePublicValue(probe) {
		return fmt.Errorf("public package probes must not contain private paths or credentials")
	}
	if number(probe, "schema_version") != 1 || probe["kind"] != "package-validation-probes" || probe["status"] != "pass" || probe["matrix_sha256"] != identity || probe["source_tree_sha256"] != matrix["source_tree_sha256"] || probe["version"] != matrix["version"] || probe["installation_runtime_accepted"] != false || probe["host_msi_lifecycle_executed"] != false || probe["terminal_executed"] != false || probe["personal_configuration_accessed"] != false || !equalJSON(probe["package_bindings"], bindings) || !equalJSON(probe["source_bindings"], sources) || !equalJSON(probe["resource_bindings"], resources) {
		return fmt.Errorf("complete exact-package probes with truthful runtime boundaries required")
	}
	if e := resultCases(probe, keys(packageProbeCases())); e != nil {
		return e
	}
	rows, e := caseResults(probe)
	if e != nil {
		return e
	}
	for name, row := range rows {
		if strings.HasPrefix(name, "static-msi-") && (row["method"] != "read-only-compiled-msi-database" || row["validation_executed"] != true || row["installation_executed"] != false || row["identity_and_replacement_guard_verified"] != true || row["component_ownership_and_transaction_order_verified"] != true || number(row, "payload_files") < 1) {
			return fmt.Errorf("compiled MSI read-only validation must actually execute")
		}
	}
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		for _, scope := range []string{"user", "machine"} {
			row, binding := rows["static-msi-"+platform+"-"+scope], object(object(bindings, platform), "msi_"+scope)
			if row["asset"] != binding["name"] || number(row, "payload_files") != number(binding, "payload_files") {
				return fmt.Errorf("MSI static probe differs from exact frozen asset and file count")
			}
		}
	}
	if row := rows["isolated-takeover-migration-contracts"]; row["exit_code"] == nil || number(row, "exit_code") != 0 {
		return fmt.Errorf("migration probe requires an actual successful PowerShell exit")
	}
	migration := object(rows["isolated-takeover-migration-contracts"], "report")
	if migration["status"] != "pass" || number(migration, "cases") != 47 || migration["scope"] != "isolated takeover snapshot, simulated MSI removal/resume, repository and payload/stale guards" || migration["git_or_terminal_executed"] != false || migration["installation_modified"] != false {
		return fmt.Errorf("complete isolated takeover migration fixture required")
	}
	want := wordSet("maintenance.auto core.deltaBaseCacheLimit pack.threads pack.windowMemory pack.deltaCacheSize lfs.concurrenttransfers")
	checks := array(rows["common-resource-behavior"], "checks")
	if len(checks) != len(want) {
		return fmt.Errorf("six actual common resource behavior checks required")
	}
	for _, raw := range checks {
		item := rec(raw)
		name := stringValue(item, "name")
		if !want[name] || item["ok"] != true {
			return fmt.Errorf("common resource behavior check missing, duplicated or failed")
		}
		delete(want, name)
	}
	return nil
}

func readNativePackagePolicy(a *app, path string, matrix record, identity string) (*nativePackagePolicy, error) {
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
	allowed := wordSet("schema_version kind authorized_by authorization reason matrix_sha256 source_tree_sha256 cases package_bindings probes installation_runtime_accepted")
	for key := range v {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown package validation policy field")
		}
	}
	for _, key := range []string{"authorization", "reason"} {
		text := stringValue(v, key)
		if len(text) > 4096 || !packagePublicValue(text) {
			return nil, fmt.Errorf("public package authorization must not contain private paths or credentials")
		}
	}
	authorization := strings.ToLower(strings.Join(strings.Fields(stringValue(v, "authorization")), ""))
	legacyAuthorization := strings.Contains(authorization, "docker验证打包就可") && strings.Contains(authorization, "不需要本机完全验证真实环境")
	explicitAuthorization := authorization == "接受docker、交叉编译和安装包验证，明确豁免msi用户级与机器级生命周期测试"
	if number(v, "schema_version") != 1 || v["kind"] != "package-validation-policy" || v["authorized_by"] != "user" || !(legacyAuthorization || explicitAuthorization) || strings.TrimSpace(stringValue(v, "reason")) == "" || v["installation_runtime_accepted"] != false || v["matrix_sha256"] != identity || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(identity) || v["source_tree_sha256"] != matrix["source_tree_sha256"] {
		return nil, fmt.Errorf("explicit user package-validation authorization bound to this exact matrix required")
	}
	seen := map[string]bool{}
	for _, raw := range array(v, "cases") {
		name, ok := raw.(string)
		if !ok || !packageValidationCases[name] || seen[name] {
			return nil, fmt.Errorf("package policy only permits the two MSI host lifecycle cases")
		}
		seen[name] = true
	}
	if len(seen) != 2 {
		return nil, fmt.Errorf("both MSI lifecycle scope decisions must be explicit")
	}
	bindings, e := packageValidationBindings(matrix)
	if e != nil {
		return nil, e
	}
	if !equalJSON(v["package_bindings"], bindings) {
		return nil, fmt.Errorf("package policy bytes differ from frozen packages")
	}
	sources, e := packageValidationSources(a, matrix)
	if e != nil {
		return nil, e
	}
	resources, _, e := packageResourceBindings(a, matrix)
	if e != nil {
		return nil, e
	}
	probeBinding := object(v, "probes")
	if len(probeBinding) != 3 || probeBinding["name"] != packageProbeAsset || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(stringValue(probeBinding, "sha256")) {
		return nil, fmt.Errorf("package policy requires its sibling complete probe asset")
	}
	probePath := filepath.Join(filepath.Dir(path), packageProbeAsset)
	info, e := os.Lstat(probePath)
	if e != nil || !info.Mode().IsRegular() || info.Size() != number(probeBinding, "bytes") || requireHash(probePath, probeBinding["sha256"]) != nil {
		return nil, fmt.Errorf("package policy probe bytes changed or missing")
	}
	probe, e := load(probePath)
	if e != nil {
		return nil, e
	}
	if e = validatePackageProbeReport(probe, matrix, identity, bindings, sources, resources); e != nil {
		return nil, e
	}
	p := &nativePackagePolicy{path: path, sha: h, probePath: probePath, probeSHA: stringValue(probeBinding, "sha256"), value: v}
	if e = p.unchanged(); e != nil {
		return nil, e
	}
	return p, nil
}

func (p *nativePackagePolicy) unchanged() error {
	if p == nil {
		return nil
	}
	return errors.Join(requireHash(p.path, p.sha), requireHash(p.probePath, p.probeSHA))
}

func (p *nativePackagePolicy) proof() record {
	if p == nil {
		return nil
	}
	return record{"schema_version": 1, "kind": "package-validation-policy", "binding_scope": "input_candidate_package_validation", "authorized_by": "user", "authorization": p.value["authorization"], "reason": p.value["reason"], "matrix_sha256": p.value["matrix_sha256"], "source_tree_sha256": p.value["source_tree_sha256"], "cases": p.value["cases"], "package_bindings": p.value["package_bindings"], "probes": p.value["probes"], "sha256": p.sha, "asset": packagePolicyAsset, "host_msi_lifecycle_executed": false, "installation_runtime_accepted": false}
}

func (p *nativePackagePolicy) result(name string) record {
	return record{"case": name, "status": "waived", "policy_sha256": p.sha, "reason": p.value["reason"], "seconds": 0, "host_msi_lifecycle_executed": false, "installation_runtime_accepted": false}
}

func nativeCorrectnessWithPackagePolicy(gate record, waiver *nativeWaiver, policy *nativePackagePolicy) error {
	if policy == nil {
		if gate["package_validation_policy"] != nil {
			return fmt.Errorf("package policy was not supplied for this correctness scope")
		}
		return nativeCorrectnessCases(gate, waiver)
	}
	if gate["status"] != "pass-with-user-waiver" || gate["installation_runtime_accepted"] != false || gate["host_msi_lifecycle_executed"] != false || !equalJSON(gate["package_validation_policy"], policy.proof()) || (waiver == nil && gate["terminal_waiver"] != nil) || (waiver != nil && !equalJSON(gate["terminal_waiver"], waiver.proof())) {
		return fmt.Errorf("correctness must retain the exact independent package and terminal scope decisions")
	}
	rows, e := caseResults(gate)
	if e != nil || len(rows) != len(correctnessCases) {
		return fmt.Errorf("all original 18 correctness cases required")
	}
	for name := range correctnessCases {
		row := rows[name]
		if row == nil {
			return fmt.Errorf("required correctness case missing")
		}
		if packageValidationCases[name] {
			if !equalJSON(rowPolicyFields(row), policy.result(name)) {
				return fmt.Errorf("MSI host lifecycle must remain explicitly waived without an execution exit code")
			}
		} else if waiver != nil && terminalWaiverCases[name] {
			if row["status"] != "waived" || row["waiver_sha256"] != waiver.sha || row["reason"] != waiver.value["reason"] || row["exit_code"] != nil {
				return fmt.Errorf("terminal policy scope differs from its own authorization")
			}
		} else if row["status"] != "pass" || name != "frozen-input-readback" && (row["exit_code"] == nil || number(row, "exit_code") != 0) {
			return fmt.Errorf("a non-waived correctness case did not pass: %s", name)
		}
	}
	return nil
}

func rowPolicyFields(row record) record {
	fields := record{}
	for _, name := range []string{"case", "status", "policy_sha256", "reason", "seconds", "host_msi_lifecycle_executed", "installation_runtime_accepted", "exit_code"} {
		if value, exists := row[name]; exists {
			fields[name] = value
		}
	}
	return fields
}

func validateNativeCorrectnessWithPackagePolicy(gate, matrix record, identity, reference string, waiver *nativeWaiver, policy *nativePackagePolicy) error {
	if gate["phase"] != "correctness" || gate["matrix_sha256"] != identity || gate["source_tree_sha256"] != matrix["source_tree_sha256"] || gate["reference_sha256"] != reference {
		return fmt.Errorf("correctness must identify exact matrix/source/reference")
	}
	return nativeCorrectnessWithPackagePolicy(gate, waiver, policy)
}

func validateNativeReleaseWithPackagePolicy(correctness, performance, matrix record, identity, correctnessSHA string, waiver *nativeWaiver, policy *nativePackagePolicy) error {
	if policy == nil {
		if correctness["package_validation_policy"] != nil || performance["package_validation_policy"] != nil {
			return fmt.Errorf("release package-validation policy was not supplied")
		}
		return validateNativeReleaseGates(correctness, performance, matrix, identity, correctnessSHA, waiver)
	}
	if e := validateNativeCorrectnessWithPackagePolicy(correctness, matrix, identity, stringValue(performance, "reference_sha256"), waiver, policy); e != nil {
		return e
	}
	if performance["status"] != "pass" || performance["phase"] != "performance" || performance["installation_runtime_accepted"] != false || performance["host_msi_lifecycle_executed"] != false || performance["matrix_sha256"] != identity || performance["source_tree_sha256"] != matrix["source_tree_sha256"] || performance["correctness_sha256"] != correctnessSHA || !equalJSON(performance["package_validation_policy"], policy.proof()) || waiver == nil && performance["terminal_waiver"] != nil || waiver != nil && !equalJSON(performance["terminal_waiver"], waiver.proof()) {
		return fmt.Errorf("five real timing gates must bind the same exact correctness and policies")
	}
	if e := resultCases(performance, []string{"performance-1000", "performance-10000", "performance-100000", "performance-network", "frozen-input-readback"}); e != nil {
		return e
	}
	for _, raw := range array(performance, "results") {
		row := rec(raw)
		if row["case"] != "frozen-input-readback" && (row["exit_code"] == nil || number(row, "exit_code") != 0) {
			return fmt.Errorf("timing gate lacks actual successful execution")
		}
	}
	return nil
}

func nativeBatchStatusWithPackagePolicy(results []record, waiver *nativeWaiver, policy *nativePackagePolicy, performance bool) string {
	if policy == nil || performance {
		return nativeBatchStatus(results, waiver, performance)
	}
	status := "pass"
	for _, row := range results {
		name := stringValue(row, "case")
		if packageValidationCases[name] {
			if !equalJSON(rowPolicyFields(row), policy.result(name)) {
				return "fail"
			}
			status = "pass-with-user-waiver"
			continue
		}
		if row["status"] == "pass" {
			continue
		}
		if waiver != nil && terminalWaiverCases[name] && row["status"] == "waived" && row["waiver_sha256"] == waiver.sha && row["exit_code"] == nil {
			status = "pass-with-user-waiver"
			continue
		}
		return "fail"
	}
	return status
}

func packageProbeJSON(data []byte) (record, error) {
	for _, start := range append([]int{0}, msiJSONStarts(data)...) {
		var value record
		if decodeJSON(bytes.TrimSpace(data[start:]), &value) == nil && value != nil {
			return value, nil
		}
	}
	return nil, fmt.Errorf("isolated migration probe omitted its JSON result")
}

func checkPackageMSIIdentity(path string, receipt record) error {
	database, e := openMSI(path)
	if e != nil {
		return e
	}
	defer closeMSI(database)
	rows, e := queryMSI(database, "SELECT `Property`, `Value` FROM `Property`")
	if e != nil {
		return e
	}
	properties := map[string]string{}
	for _, row := range rows {
		if len(row) >= 2 {
			properties[row[0]] = row[1]
		}
	}
	if properties["ProductName"] != "git-next" || properties["ProductCode"] != receipt["product_code"] || properties["UpgradeCode"] != receipt["upgrade_code"] || properties["ProductVersion"] != receipt["msi_version"] || properties["GITNEXTLAYOUT"] != "git-default-v2" || (receipt["install_scope"] == "machine") != (properties["ALLUSERS"] == "1") {
		return fmt.Errorf("actual MSI identity or scope differs from packaging receipt")
	}
	conditions, e := queryMSI(database, "SELECT `Condition` FROM `LaunchCondition`")
	if e != nil {
		return e
	}
	guard := false
	for _, row := range conditions {
		if len(row) == 1 && row[0] == `Installed OR NOT OFFICIALGIT OR REPLACEGIT = "1"` {
			guard = true
		}
	}
	if !guard {
		return fmt.Errorf("actual MSI lacks its explicit Git replacement authorization guard")
	}
	return nil
}

func nativePackageValidationProbes(a *app, o options) (returned error) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("package probes require a native 64-bit Windows controller; no installation is performed")
	}
	if e := o.require("native-packages", "output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("package probe output must be new")
	}
	matrix, e := load(o.get("native-packages"))
	if e != nil {
		return e
	}
	identity, e := digest(o.get("native-packages"))
	if e != nil {
		return e
	}
	if e = nativeMatrixReadback(a, matrix); e != nil {
		return e
	}
	bindings, e := packageValidationBindings(matrix)
	if e != nil {
		return e
	}
	sources, e := packageValidationSources(a, matrix)
	if e != nil {
		return e
	}
	resources, d, e := packageResourceBindings(a, matrix)
	if e != nil {
		return e
	}
	report := record{"schema_version": 1, "kind": "package-validation-probes", "status": "running", "matrix_sha256": identity, "source_tree_sha256": matrix["source_tree_sha256"], "version": matrix["version"], "package_bindings": bindings, "source_bindings": sources, "resource_bindings": resources, "installation_runtime_accepted": false, "host_msi_lifecycle_executed": false, "terminal_executed": false, "personal_configuration_accessed": false}
	results := []any{}
	started := time.Now()
	defer func() {
		unchanged := nativeMatrixReadback(a, matrix) == nil && requireHash(o.get("native-packages"), identity) == nil
		status := "fail"
		if unchanged {
			status = "pass"
		}
		results = append(results, record{"case": "frozen-input-readback", "status": status})
		report["results"], report["seconds"], report["status"] = results, roundedSeconds(time.Since(started)), "pass"
		if returned != nil || !unchanged {
			report["status"] = "fail"
		}
		if report["status"] == "pass" {
			returned = validatePackageProbeReport(report, matrix, identity, bindings, sources, resources)
		}
		if returned != nil {
			report["status"] = "fail"
		}
		returned = errors.Join(returned, writeNewJSON(o.get("output"), report))
		if !unchanged {
			returned = errors.Join(returned, fmt.Errorf("package probe inputs changed during execution"))
		}
	}()
	for _, platform := range []string{"windows-x64", "windows-arm64"} {
		item := object(object(matrix, "platforms"), platform)
		for _, scope := range []string{"user", "machine"} {
			path := stringValue(item, "msi_"+scope)
			receipt, e := load(strings.TrimSuffix(path, ".msi") + ".receipt.json")
			if e != nil {
				return e
			}
			err := checkCompiledMSIPayload(path, int(number(receipt, "payload_files")))
			if err == nil {
				err = checkPackageMSIIdentity(path, receipt)
			}
			status := "pass"
			if err != nil {
				status = "fail"
			}
			results = append(results, record{"case": "static-msi-" + platform + "-" + scope, "status": status, "method": "read-only-compiled-msi-database", "validation_executed": true, "asset": filepath.Base(path), "payload_files": receipt["payload_files"], "installation_executed": false, "identity_and_replacement_guard_verified": err == nil, "component_ownership_and_transaction_order_verified": err == nil})
			if err != nil {
				return fmt.Errorf("compiled MSI static package probe failed")
			}
		}
	}
	home, e := os.MkdirTemp("", "gn-package-migration-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(home)
	system32 := filepath.Join(os.Getenv("SystemRoot"), "System32")
	shell := filepath.Join(system32, "WindowsPowerShell/v1.0/powershell.exe")
	modules := filepath.Join(system32, "WindowsPowerShell/v1.0/Modules")
	env := fixtureEnv(acceptEnv(home), map[string]string{"PSModulePath": modules, "TEMP": home, "TMP": home})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	run, runError := runProcess(ctx, a.root, env, nil, shell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", a.path("tests/takeover_registration_acceptance.ps1"))
	cancel()
	migration, loadError := packageProbeJSON(run.stdout)
	status := "pass"
	if runError != nil || run.code != 0 || loadError != nil {
		status = "fail"
	}
	publicMigration := record{}
	for _, key := range []string{"status", "cases", "scope", "git_or_terminal_executed", "installation_modified"} {
		publicMigration[key] = migration[key]
	}
	results = append(results, record{"case": "isolated-takeover-migration-contracts", "status": status, "exit_code": run.code, "argument_class": "native-WinPS5.1-NoProfile-owned-temp-migration-fixture", "stdout_sha256": digestBytes(run.stdout), "stderr_sha256": digestBytes(run.stderr), "report": publicMigration})
	if status != "pass" {
		return fmt.Errorf("isolated takeover migration contract probe failed")
	}
	binary := filepath.Join(stringValue(object(object(matrix, "platforms"), "windows-x64"), "directory"), "bin/git.exe")
	checks, resourceError := defaultResourceBehavior(binary, d)
	status = "pass"
	if resourceError != nil {
		status = "fail"
	}
	for _, check := range checks {
		if !check.OK {
			status = "fail"
		}
	}
	checkRecords := []any{}
	for _, check := range checks {
		checkRecords = append(checkRecords, record{"name": check.Name, "ok": check.OK, "detail": check.Detail})
	}
	results = append(results, record{"case": "common-resource-behavior", "status": status, "checks": checkRecords, "argument_class": "isolated-native-Git-Trace2-pack-verify-cache-resolution-owned-lfs-env"})
	if status != "pass" {
		return fmt.Errorf("six actual resource-default probes did not pass")
	}
	return nil
}
