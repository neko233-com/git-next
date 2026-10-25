// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func init() {
	register("prepare-native-release", "Stage one five-platform release after immutable correctness/performance gates", "native-packages correctness performance destination wix git source-commit windows-runtime-acl-fix windows-tls-rename-fix? terminal-waiver package-validation-policy", prepareNativeRelease)
}
func resultCases(gate record, expected []string) error {
	want := map[string]bool{}
	for _, name := range expected {
		want[name] = true
	}
	seen := map[string]bool{}
	for _, v := range array(gate, "results") {
		item := rec(v)
		name := stringValue(item, "case")
		if !want[name] || seen[name] || item["status"] != "pass" {
			return fmt.Errorf("missing/duplicate/failed acceptance case: %s", name)
		}
		seen[name] = true
	}
	if len(seen) != len(want) {
		return fmt.Errorf("required acceptance cases missing")
	}
	return nil
}
func releaseInputReadback(matrix record) error {
	for name, want := range object(matrix, "files") {
		if e := requireHash(name, want); e != nil {
			return fmt.Errorf("accepted package input changed: %s: %w", name, e)
		}
	}
	return nil
}

func releaseCoreRuntimeEntries(files record) (record, error) {
	entries := record{}
	for name, raw := range files {
		if !strings.HasPrefix(name, "bin/") && !strings.HasPrefix(name, "cmd/") && !strings.HasPrefix(name, "runtime/") {
			continue
		}
		if path.Clean(name) != name || path.IsAbs(name) || strings.ContainsAny(name, "\\:") {
			return nil, fmt.Errorf("unsafe core/runtime identity member")
		}
		item := rec(raw)
		if item["kind"] == "symlink" {
			target := stringValue(item, "target")
			resolved := path.Clean(path.Join(path.Dir(name), target))
			if target == "" || path.IsAbs(target) || strings.ContainsAny(target, "\\:") || resolved == ".." || strings.HasPrefix(resolved, "../") {
				return nil, fmt.Errorf("unsafe core/runtime identity link")
			}
			entries[name] = record{"kind": "symlink", "target": target}
			continue
		}
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(stringValue(item, "sha256")) || item["bytes"] == nil || number(item, "bytes") < 0 {
			return nil, fmt.Errorf("core/runtime identity lacks exact file bytes")
		}
		entry := record{"kind": "file", "sha256": item["sha256"], "bytes": number(item, "bytes")}
		for _, key := range []string{"mode", "windows_attributes"} {
			if item[key] != nil {
				entry[key] = item[key]
			}
		}
		entries[name] = entry
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("core/runtime identity cannot be empty")
	}
	return entries, nil
}

func releaseCoreRuntimeIdentity(candidate, staged record) (record, error) {
	before, e := releaseCoreRuntimeEntries(candidate)
	if e != nil {
		return nil, e
	}
	after, e := releaseCoreRuntimeEntries(staged)
	if e != nil {
		return nil, e
	}
	if !equalJSON(before, after) {
		return nil, fmt.Errorf("final core/runtime payload differs from accepted candidate bytes, links or modes")
	}
	var total int64
	for _, raw := range before {
		total += number(rec(raw), "bytes")
	}
	h := digestBytes(encoded(before))
	return record{"status": "pass", "scope": "bin/, cmd/ and runtime/; release metadata and manuals excluded", "candidate_identity_sha256": h, "final_identity_sha256": h, "members": len(before), "logical_bytes": total}, nil
}

func releaseArchiveCoreRuntime(archive, prefix string, expected record) (record, error) {
	entries, e := releaseCoreRuntimeEntries(expected)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	var total int64
	count := 0
	e = eachArchive(archive, func(m archiveMember, r io.Reader) error {
		name := path.Clean(m.name)
		count++
		if m.size < 0 || m.size > 4<<30 || total > (4<<30)-m.size || count > 100000 {
			return fmt.Errorf("final archive inspection exceeds package bounds")
		}
		total += m.size
		if name == prefix && m.directory {
			return nil
		}
		if !strings.HasPrefix(name, prefix+"/") {
			return fmt.Errorf("final archive root differs from release identity")
		}
		name = strings.TrimPrefix(name, prefix+"/")
		if !strings.HasPrefix(name, "bin/") && !strings.HasPrefix(name, "cmd/") && !strings.HasPrefix(name, "runtime/") {
			return nil
		}
		if m.directory {
			return nil
		}
		entry := rec(entries[name])
		if entry == nil || seen[name] {
			return fmt.Errorf("unexpected or duplicate final core/runtime archive member")
		}
		seen[name] = true
		if entry["kind"] == "symlink" {
			if !m.symlink || m.link != entry["target"] {
				return fmt.Errorf("final core/runtime archive link differs")
			}
			return nil
		}
		if m.symlink || m.hardlink || m.size != number(entry, "bytes") || entry["mode"] != nil && int64(m.mode.Perm()) != number(entry, "mode") {
			return fmt.Errorf("final core/runtime archive file identity differs")
		}
		h := sha256.New()
		n, e := io.Copy(h, r)
		if e != nil || n != m.size || hex.EncodeToString(h.Sum(nil)) != entry["sha256"] {
			return fmt.Errorf("final core/runtime archive file SHA differs")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if len(seen) != len(entries) {
		return nil, fmt.Errorf("final archive omitted accepted core/runtime members")
	}
	identity, e := releaseCoreRuntimeIdentity(entries, entries)
	if e == nil {
		identity["final_archive_members_streamed_and_hashed"] = true
	}
	return identity, e
}
func releaseOwnedReplace(root, name string, data []byte, mode os.FileMode) error {
	target, e := confined(root, name)
	if e != nil {
		return e
	}
	regularTarget := func() error {
		info, e := os.Lstat(target)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release replacement target is not a regular file: %s", name)
		}
		return nil
	}
	if e = regularTarget(); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return e
	}
	temporary, e := os.CreateTemp(filepath.Dir(target), ".git-next-release-*")
	if e != nil {
		return e
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	_, writeErr := temporary.Write(data)
	closeErr := temporary.Close()
	if e = errors.Join(writeErr, closeErr); e != nil {
		return e
	}
	if e = os.Chmod(temporaryPath, mode.Perm()); e != nil {
		return e
	}
	if e = regularTarget(); e != nil {
		return e
	}
	// Staged payloads share source hardlinks until a replacement needs a fresh inode.
	if e = os.Remove(target); e != nil && !os.IsNotExist(e) {
		return e
	}
	return os.Rename(temporaryPath, target)
}
func boundedReleaseRepairs(a *app, o options, matrix record) (record, error) {
	repairs := record{}
	tlsScript := "packaging/windows/install-tls-provider.ps1"
	aclScript := "packaging/windows/Runtime.Configuration.ps1"
	if o.yes("windows-tls-rename-fix") {
		added := []byte("    # Windows cannot rename a directory while its child file handles remain open.\n    # The protected ACL and exclusive platform lock still guard publication.\n    foreach ($handle in $held) { $handle.Dispose() }\n    $held.Clear()\n")
		current, e := os.ReadFile(a.path(filepath.FromSlash(tlsScript)))
		if e != nil {
			return nil, e
		}
		current = bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n"))
		if bytes.Count(current, added) != 1 || digestBytes(bytes.ReplaceAll(current, added, nil)) != object(matrix, "harnesses")[tlsScript] {
			return nil, fmt.Errorf("only exact accepted TLS child-handle repair may reuse gates")
		}
		h, e := digest(a.path(filepath.FromSlash(tlsScript)))
		if e != nil {
			return nil, e
		}
		repairs["windows_tls_rename_fix"] = record{"before_sha256": object(matrix, "harnesses")[tlsScript], "after_sha256": h, "payload_path": "components/install-tls-provider.ps1", "scope": "Only release verified child handles before directory rename; all core, runtime, updater, GUI and MSI recipes remain accepted unchanged", "required_before_publication": "Fresh real machine TLS preparation, idempotence, pinned-file/ACL verification and actual workstation takeover"}
	}
	if o.get("windows-runtime-acl-fix") != "" {
		replacement := []byte("        $inherited = @($security.GetAccessRules($false,$true,[Security.Principal.SecurityIdentifier]))\n        $security.SetAccessRuleProtection($true,$false)\n        foreach ($rule in $inherited) {\n            $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($rule.IdentityReference,$rule.FileSystemRights,$rule.AccessControlType))\n        }\n")
		current, e := os.ReadFile(a.path(filepath.FromSlash(aclScript)))
		if e != nil {
			return nil, e
		}
		current = bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n"))
		previous := bytes.ReplaceAll(current, replacement, []byte("        $security.SetAccessRuleProtection($true,$true)\n"))
		if !o.yes("windows-tls-rename-fix") || bytes.Count(current, replacement) != 1 || digestBytes(previous) != object(matrix, "harnesses")[aclScript] {
			return nil, fmt.Errorf("only exact inherited-ACE repair may reuse gates")
		}
		gate, e := load(o.get("windows-runtime-acl-fix"))
		if e != nil {
			return nil, e
		}
		checks := map[string]bool{}
		for _, v := range array(gate, "checks") {
			if s, ok := v.(string); ok {
				checks[s] = true
			}
		}
		if gate["status"] != "pass" || !checks["inherited user full control becomes read-only machine configuration"] || !checks["machine configuration ACL is protected after inherited ACE conversion"] {
			return nil, fmt.Errorf("actual inherited-ACE updater regression required")
		}
		h, e := digest(a.path(filepath.FromSlash(aclScript)))
		if e != nil {
			return nil, e
		}
		ah, e := digest(o.get("windows-runtime-acl-fix"))
		if e != nil {
			return nil, e
		}
		repairs["windows_runtime_acl_fix"] = record{"before_sha256": object(matrix, "harnesses")[aclScript], "after_sha256": h, "payload_path": "updater/Runtime.Configuration.ps1", "updater_acceptance_sha256": ah, "scope": "Convert inherited ACEs into explicit rules before removing non-administrator writes; preserve read/deny restrictions", "required_before_publication": "Actual configuration migration and workstation takeover using this installed script"}
	}
	return repairs, nil
}
func prepareNativeRelease(a *app, o options) (releaseErr error) {
	if e := o.require("native-packages", "correctness", "performance", "destination", "wix", "git", "source-commit"); e != nil {
		return e
	}
	matrix, e := load(o.get("native-packages"))
	if e != nil {
		return e
	}
	identity, e := digest(o.get("native-packages"))
	if e != nil {
		return e
	}
	correctness, e := load(o.get("correctness"))
	if e != nil {
		return e
	}
	performance, e := load(o.get("performance"))
	if e != nil {
		return e
	}
	waiver, e := readNativeWaiver(a, o.get("terminal-waiver"), matrix, identity)
	if e != nil {
		return e
	}
	packagePolicy, e := readNativePackagePolicy(a, o.get("package-validation-policy"), matrix, identity)
	if e != nil {
		return e
	}
	ch, e := digest(o.get("correctness"))
	if e != nil {
		return e
	}
	if e = validateNativeReleaseWithPackagePolicy(correctness, performance, matrix, identity, ch, waiver, packagePolicy); e != nil {
		return e
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(o.get("source-commit")) {
		return fmt.Errorf("full immutable project commit required")
	}
	head, e := a.mustRun(o.get("git"), "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(head)) != o.get("source-commit") {
		return fmt.Errorf("HEAD differs from release source commit")
	}
	if e = releaseInputReadback(matrix); e != nil {
		return e
	}
	defer func() {
		if releaseErr != nil {
			releaseErr = errors.Join(releaseErr, releaseInputReadback(matrix), packagePolicy.unchanged())
		}
	}()
	repairs, e := boundedReleaseRepairs(a, o, matrix)
	if e != nil {
		return e
	}
	for name, h := range object(matrix, "harnesses") {
		if o.yes("windows-tls-rename-fix") && (name == "packaging/windows/install-tls-provider.ps1" || name == "tools/prepare_native_release.py" || name == "tools/devtool/release_native.go") {
			continue
		}
		if o.get("windows-runtime-acl-fix") != "" && (name == "packaging/windows/Runtime.Configuration.ps1" || name == "tests/updater_acceptance.ps1") {
			continue
		}
		path, e := matrixHarnessPath(a, matrix, name)
		if e != nil {
			return e
		}
		actual, e := digest(path)
		if e != nil || actual != h {
			return fmt.Errorf("accepted source/harness input changed: %s", name)
		}
	}
	platformsInput := object(matrix, "platforms")
	for _, platform := range []string{"windows-x64", "windows-arm64", "linux-x64", "linux-arm64", "macos-arm64"} {
		if platformsInput[platform] == nil {
			return fmt.Errorf("required release platform missing: %s", platform)
		}
	}
	if len(platformsInput) != 5 {
		return fmt.Errorf("release requires exact supported five platforms")
	}
	mac := object(platformsInput, "macos-arm64")
	if mac["native_executed"] != true || stringValue(mac, "runtime_receipt") == "" {
		return fmt.Errorf("release requires actual macOS ARM64 runtime evidence")
	}
	macRuntimeHash := object(matrix, "files")[stringValue(mac, "runtime_receipt")]
	if e = requireHash(stringValue(mac, "runtime_receipt"), macRuntimeHash); e != nil {
		return e
	}
	macRuntime, e := checkedMacRuntimeReceipt(a, stringValue(mac, "runtime_receipt"), stringValue(mac, "archive"), stringValue(matrix, "source_tree_sha256"), stringValue(matrix, "version"))
	if e != nil {
		return e
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	ph, e := digest(o.get("performance"))
	if e != nil {
		return e
	}
	proof := record{"schema_version": 1, "product": "git-next", "source_commit": o.get("source-commit"), "source_tree_sha256": matrix["source_tree_sha256"], "matrix_sha256": identity, "correctness_sha256": ch, "performance_sha256": ph, "native_mac_product_executed": true, "native_mac_runtime_cases": macRuntime["passed_cases"], "native_mac_runtime_receipt_sha256": macRuntimeHash, "personal_account_accessed": false, "scope": "accepted core/runtime bytes; release metadata, documentation and archive root finalized after local gates"}
	if waiver != nil {
		proof["correctness_status"], proof["terminal_waiver"] = correctness["status"], waiver.proof()
		if e = waiver.unchanged(); e != nil {
			return e
		}
		waiverAsset := filepath.Join(out, "native-terminal-waiver.json")
		if e = copyFile(waiver.path, waiverAsset); e != nil {
			return e
		}
		if e = requireHash(waiverAsset, waiver.sha); e != nil {
			return e
		}
	}
	if packagePolicy != nil {
		if e = packagePolicy.unchanged(); e != nil {
			return e
		}
		proof["correctness_status"], proof["package_validation_policy"] = correctness["status"], packagePolicy.proof()
		proof["input_candidate_package_validation"] = packagePolicy.proof()
		proof["embedded_acceptance_scope"] = "input_candidate_package_validation; final download hashes and static checks are in standalone native-release-acceptance.json"
		proof["host_msi_lifecycle_executed"], proof["installation_runtime_accepted"] = false, false
		for _, item := range [][3]string{{packagePolicy.path, packagePolicyAsset, packagePolicy.sha}, {packagePolicy.probePath, packageProbeAsset, packagePolicy.probeSHA}} {
			if e = copyFile(item[0], filepath.Join(out, item[1])); e != nil {
				return e
			}
			if e = requireHash(filepath.Join(out, item[1]), item[2]); e != nil {
				return e
			}
		}
	}
	for k, v := range repairs {
		proof[k] = v
	}
	if packagePolicy == nil {
		if e = writeNewJSON(filepath.Join(out, "native-release-acceptance.json"), proof); e != nil {
			return e
		}
	}
	manuals, e := releaseDocuments(a)
	if e != nil {
		return e
	}
	version := stringValue(matrix, "version")
	if _, _, e = msiVersion(version); e != nil {
		return e
	}
	platforms := record{}
	finalPackages := record{}
	for _, platform := range sortedKeys(platformsInput) {
		item := rec(platformsInput[platform])
		if strings.HasPrefix(platform, "windows-") {
			target := filepath.Join(out, "distributions", platform)
			if e = copyRuntimeTree(stringValue(item, "directory"), target); e != nil {
				return e
			}
			manifestPath := filepath.Join(target, "git-next-distribution.json")
			manifest, e := load(manifestPath)
			if e != nil {
				return e
			}
			if !truth(manifest, "complete_git_parity") || manifest["source_tree_sha256"] != matrix["source_tree_sha256"] {
				return fmt.Errorf("native package lacks accepted complete identity")
			}
			var candidateCore record
			if packagePolicy != nil {
				candidateCore, e = releaseCoreRuntimeEntries(object(manifest, "files"))
				if e != nil {
					return e
				}
			}
			manifest["status"] = "accepted-release-distribution"
			manifest["source_commit"] = o.get("source-commit")
			manifest["release_eligible"] = true
			manifest["remaining"] = []string{}
			manifest["acceptance"] = proof
			for key, v := range repairs {
				repair := rec(v)
				name := stringValue(repair, "payload_path")
				p := filepath.Join(target, filepath.FromSlash(name))
				h, e := digest(p)
				if e != nil || h != repair["before_sha256"] {
					return fmt.Errorf("accepted repair baseline differs: %s", key)
				}
				source := "packaging/windows/install-tls-provider.ps1"
				if key == "windows_runtime_acl_fix" {
					source = "packaging/windows/Runtime.Configuration.ps1"
				}
				repairData, e := os.ReadFile(a.path(filepath.FromSlash(source)))
				if e != nil {
					return e
				}
				if e = releaseOwnedReplace(target, name, repairData, 0644); e != nil {
					return e
				}
				h, e = digest(p)
				if e != nil {
					return e
				}
				st, e := os.Stat(p)
				if e != nil {
					return e
				}
				entry := rec(object(manifest, "files")[name])
				if entry == nil {
					return fmt.Errorf("repair payload is not owned")
				}
				entry["sha256"] = h
				entry["bytes"] = st.Size()
			}
			for name, data := range manuals {
				if e = releaseOwnedReplace(target, name, data, 0644); e != nil {
					return e
				}
				entry := rec(object(manifest, "files")[name])
				if entry == nil {
					entry = record{"kind": "release-documentation"}
					object(manifest, "files")[name] = entry
				}
				entry["sha256"] = digestBytes(data)
				entry["bytes"] = len(data)
			}
			manifestData, e := json.MarshalIndent(manifest, "", "  ")
			if e != nil {
				return e
			}
			if e = releaseOwnedReplace(target, "git-next-distribution.json", append(manifestData, '\n'), 0644); e != nil {
				return e
			}
			for _, scope := range []string{"machine", "user"} {
				opts := options{strings: map[string]*string{}, bools: map[string]*bool{}}
				for k, v := range map[string]string{"distribution": target, "wix": o.get("wix"), "output-dir": out, "scope": scope, "source-commit": o.get("source-commit")} {
					value := v
					opts.strings[k] = &value
				}
				quiet := *a
				quiet.out = os.Stdout
				if e = nativeMSI(&quiet, opts); e != nil {
					return e
				}
				suffix := ""
				if scope == "user" {
					suffix = "-user"
				}
				receipt, e := load(filepath.Join(out, "git-next-"+version+"-"+platform+suffix+".receipt.json"))
				if e != nil {
					return e
				}
				asset := filepath.Join(out, stringValue(receipt, "asset"))
				if e = shaMatches(asset, record{"sha256": receipt["sha256"]}); e != nil || !truth(receipt, "release_eligible") {
					return fmt.Errorf("MSI packaging gate failed")
				}
				st, e := os.Stat(asset)
				if e != nil {
					return e
				}
				entry := record{"size": st.Size(), "install_kind": "msi"}
				if packagePolicy != nil {
					entry["installation_runtime_accepted"], entry["host_msi_lifecycle_executed"] = false, false
					entry["package_validation_policy_sha256"] = packagePolicy.sha
					entry["package_validation_policy_binding_scope"] = "input_candidate_package_validation"
					if e = checkCompiledMSIPayload(asset, int(number(receipt, "payload_files"))); e != nil {
						return fmt.Errorf("final MSI static payload readback failed: %w", e)
					}
					if e = checkPackageMSIIdentity(asset, receipt); e != nil {
						return e
					}
					coreIdentity, e := releaseCoreRuntimeIdentity(candidateCore, object(manifest, "files"))
					if e != nil {
						return e
					}
					finalPackages[filepath.Base(asset)] = record{"platform": platform, "install_scope": scope, "sha256": receipt["sha256"], "bytes": st.Size(), "source_tree_sha256": matrix["source_tree_sha256"], "compiled_msi_payload_tables_rechecked": true, "msi_identity_and_replacement_guard_rechecked": true, "payload_validation_method": "manifest-bound payload source bytes checked before and after WiX; final MSI tables and identity read back", "core_runtime_identity": coreIdentity, "installation_runtime_accepted": false}
				}
				for _, key := range []string{"asset", "sha256", "install_scope", "msi_version", "product_code", "upgrade_code"} {
					entry[key] = receipt[key]
				}
				if scope == "machine" {
					platforms[platform] = entry
				} else {
					object(platforms, platform)["variants"] = record{"user": entry}
				}
			}
			continue
		}
		archive := stringValue(item, "archive")
		manifests, e := tarJSON(archive, "package.json", "git-next-distribution.json")
		if e != nil {
			return e
		}
		manifest, report := manifests["package.json"], manifests["git-next-distribution.json"]
		if !truth(manifest, "complete_git_parity") || manifest["source_tree_sha256"] != matrix["source_tree_sha256"] {
			return fmt.Errorf("POSIX package lacks accepted complete identity")
		}
		var candidateCore record
		if packagePolicy != nil {
			candidateCore, e = releaseCoreRuntimeEntries(object(manifest, "files"))
			if e != nil {
				return e
			}
		}
		manifest["source_commit"] = o.get("source-commit")
		manifest["release_eligible"] = true
		manifest["acceptance"] = proof
		replacements := map[string][]byte{}
		for name, data := range manuals {
			replacements[name] = data
			entry := record{"kind": "file", "sha256": digestBytes(data), "bytes": len(data), "mode": 0644, "component": "release-documentation"}
			object(manifest, "files")[name] = entry
			other := record{}
			for k, v := range entry {
				other[k] = v
			}
			object(report, "files")[name] = other
		}
		replacements["package.json"] = encoded(manifest)
		entry := rec(object(report, "files")["package.json"])
		if entry == nil {
			return fmt.Errorf("package manifest not owned")
		}
		entry["sha256"] = digestBytes(replacements["package.json"])
		entry["bytes"] = len(replacements["package.json"])
		report["status"] = "accepted-release-distribution"
		report["source_commit"] = o.get("source-commit")
		report["release_eligible"] = true
		report["acceptance"] = proof
		replacements["git-next-distribution.json"] = encoded(report)
		prefix := "git-next-" + version + "-" + platform
		destination := filepath.Join(out, prefix+".tar.gz")
		if e = rewriteTar(archive, destination, prefix, replacements, 6, true); e != nil {
			return e
		}
		h, e := digest(destination)
		if e != nil {
			return e
		}
		st, e := os.Stat(destination)
		if e != nil {
			return e
		}
		platforms[platform] = record{"asset": filepath.Base(destination), "sha256": h, "size": st.Size(), "install_kind": "tar"}
		if packagePolicy != nil {
			coreIdentity, e := releaseArchiveCoreRuntime(destination, prefix, candidateCore)
			if e != nil {
				return e
			}
			finalPackages[filepath.Base(destination)] = record{"platform": platform, "sha256": h, "bytes": st.Size(), "source_tree_sha256": matrix["source_tree_sha256"], "core_runtime_identity": coreIdentity, "validation_method": "streamed final archive core/runtime SHA, size, link and mode readback"}
		}
	}
	if packagePolicy != nil {
		if len(finalPackages) != 7 {
			return fmt.Errorf("all four final MSI and three final POSIX package readbacks required")
		}
		proof["final_download_package_validation"] = record{"status": "pass", "binding_scope": "final_download_packages", "assets": finalPackages, "msi_host_lifecycle_executed": false, "installation_runtime_accepted": false}
		if e = writeNewJSON(filepath.Join(out, "native-release-acceptance.json"), proof); e != nil {
			return e
		}
	}
	update := record{"schema_version": 1, "product": "git-next", "version": version, "source_commit": o.get("source-commit"), "complete_git_parity": true, "platforms": platforms}
	if waiver != nil || packagePolicy != nil {
		update["acceptance"] = proof
	}
	if e = writeNewJSON(filepath.Join(out, "git-next-update.json"), update); e != nil {
		return e
	}
	for _, v := range [][2]string{{o.get("correctness"), "native-correctness.json"}, {o.get("performance"), "native-performance.json"}} {
		if e = copyFile(v[0], filepath.Join(out, v[1])); e != nil {
			return e
		}
	}
	for _, name := range []string{"performance-1000.json", "performance-10000.json", "performance-100000.json", "performance-network.json"} {
		if e = copyFile(filepath.Join(filepath.Dir(o.get("performance")), name), filepath.Join(out, name)); e != nil {
			return e
		}
	}
	var bundles []string
	for name := range object(matrix, "files") {
		if strings.HasPrefix(filepath.Base(name), "git-next-corresponding-sources-") && filepath.Ext(name) == ".zip" {
			bundles = append(bundles, name)
		}
	}
	if len(bundles) != 4 {
		return fmt.Errorf("four accepted corresponding-source bundles required")
	}
	for _, p := range bundles {
		if e = copyFile(p, filepath.Join(out, filepath.Base(p))); e != nil {
			return e
		}
	}
	if e = copyFile(a.path("install.ps1"), filepath.Join(out, "install.ps1")); e != nil {
		return e
	}
	entries, e := os.ReadDir(out)
	if e != nil {
		return e
	}
	var assets []string
	for _, v := range entries {
		if v.IsDir() || strings.HasSuffix(v.Name(), ".receipt.json") {
			continue
		}
		switch filepath.Ext(v.Name()) {
		case ".msi", ".gz", ".zip", ".json", ".ps1":
			assets = append(assets, v.Name())
		}
	}
	sort.Strings(assets)
	var sums strings.Builder
	for _, name := range assets {
		h, e := digest(filepath.Join(out, name))
		if e != nil {
			return e
		}
		fmt.Fprintf(&sums, "%s  %s\n", h, name)
	}
	if e = write(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0644); e != nil {
		return e
	}
	if e = releaseInputReadback(matrix); e != nil {
		return e
	}
	if e = packagePolicy.unchanged(); e != nil {
		return e
	}
	return a.json(record{"status": "release-staged-not-published", "source_commit": o.get("source-commit"), "platforms": len(platforms), "assets": len(assets)})
}
