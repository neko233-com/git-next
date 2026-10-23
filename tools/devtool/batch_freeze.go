// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var nativePlatforms = []string{"windows-x64", "windows-arm64", "linux-x64", "linux-arm64", "macos-arm64"}
var nativeBases = map[string]string{"linux-x64": "debian@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587", "linux-arm64": "debian@sha256:a1b86db52ce3daef089e45aabe36dfec4091f82464c25c1fdcf03de197cbe82a"}

func init() {
	register("freeze-native-batch", "Freeze complete package and actual Go/platform harness bytes", "windows-x64 windows-arm64 linux-x64 linux-arm64 macos-arm64 msi-directory base-msi-directory source-bundles race-binary terminal-source output", freezeNativeBatch)
}
func gitMetadata(a *app, args ...string) (string, error) {
	ctx, cancel := deadline()
	defer cancel()
	r, e := runProcess(ctx, a.root, nil, nil, "git", args...)
	if e != nil {
		return "", e
	}
	if r.code != 0 {
		return "", fmt.Errorf("reference Git metadata failed: %s", r.stderr)
	}
	return strings.TrimSpace(string(r.stdout)), nil
}
func freezeNativeBatch(a *app, o options) error {
	if e := o.require("windows-x64", "windows-arm64", "linux-x64", "linux-arm64", "macos-arm64", "msi-directory", "base-msi-directory", "source-bundles", "race-binary", "output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("use a new frozen matrix path")
	}
	pinned, platforms, identities := record{}, record{}, map[string]bool{}
	pin := func(p string) (string, error) {
		full, e := absolute(p)
		if e != nil {
			return "", e
		}
		hash, e := digest(full)
		if e != nil {
			return "", e
		}
		pinned[full] = hash
		return full, nil
	}
	version, e := productVersion(a)
	if e != nil {
		return e
	}
	peer, e := absolute(o.value("terminal-source", filepath.Join(filepath.Dir(a.root), "git-terminal")))
	if e != nil {
		return e
	}
	terminalCommit, e := gitMetadata(a, "-C", peer, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	for _, target := range nativePlatforms[:2] {
		root, e := absolute(o.get(target))
		if e != nil {
			return e
		}
		manifest, e := load(filepath.Join(root, "git-next-distribution.json"))
		if e != nil {
			return e
		}
		if manifest["platform"] != target || manifest["version"] != version {
			return fmt.Errorf("wrong Windows package identity")
		}
		built := stringValue(object(manifest, "git_terminal"), "source_commit")
		testModule, e := read(filepath.Join(peer, "src/main.rs"))
		if e != nil {
			return e
		}
		if !strings.Contains(testModule, "#[cfg(all(test, windows))]\nmod shell_acceptance;") {
			return fmt.Errorf("excluded terminal fixture must remain test-only")
		}
		if built == "" {
			return fmt.Errorf("missing terminal source commit")
		}
		if _, e = gitMetadata(a, "-C", peer, "merge-base", "--is-ancestor", built, terminalCommit); e != nil {
			return e
		}
		paths := []string{"Cargo.toml", "Cargo.lock", "src", "assets", "build.rs", "shell-integration", "vendor/gpui-terminal", ":(exclude)src/shell_acceptance.rs"}
		args := append([]string{"-C", peer, "diff", "--quiet", built, terminalCommit, "--"}, paths...)
		if _, e = gitMetadata(a, args...); e != nil {
			return fmt.Errorf("peer runtime source changed: %w", e)
		}
		args = append([]string{"-C", peer, "status", "--porcelain", "--untracked-files=no", "--"}, paths...)
		dirty, e := gitMetadata(a, args...)
		if e != nil {
			return e
		}
		if dirty != "" {
			return fmt.Errorf("peer runtime sources are not committed")
		}
		identities[stringValue(manifest, "source_tree_sha256")] = true
		for name, raw := range object(manifest, "files") {
			item, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid package file receipt")
			}
			p, e := confined(root, name)
			if e != nil {
				return e
			}
			info, e := os.Lstat(p)
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("Windows package link refused")
			}
			if e = requireHash(p, item["sha256"]); e != nil {
				return e
			}
			if _, e = pin(p); e != nil {
				return e
			}
		}
		if _, e = pin(filepath.Join(root, "git-next-distribution.json")); e != nil {
			return e
		}
		entry := record{"directory": root}
		prefix := "mingw64"
		if target == "windows-arm64" {
			prefix = "clangarm64"
		}
		for _, p := range []string{filepath.Join(root, "runtime", prefix, "bin/git-credential-manager.exe"), filepath.Join(root, "runtime/mingw64/bin/git-credential-manager.exe")} {
			if exists(p) {
				entry["gcm"] = p
				break
			}
		}
		if entry["gcm"] == nil {
			return fmt.Errorf("primary Windows GCM missing")
		}
		for _, scope := range []string{"user", "machine"} {
			suffix := ""
			if scope == "user" {
				suffix = "-user"
			}
			p := filepath.Join(o.get("msi-directory"), "git-next-"+version+"-"+target+suffix+".msi")
			receiptPath := strings.TrimSuffix(p, ".msi") + ".receipt.json"
			receipt, e := load(receiptPath)
			if e != nil {
				return e
			}
			if receipt["install_scope"] != scope {
				return fmt.Errorf("MSI receipt scope differs")
			}
			if e = requireHash(p, receipt["sha256"]); e != nil {
				return e
			}
			entry["msi_"+scope], e = pin(p)
			if e != nil {
				return e
			}
			if _, e = pin(receiptPath); e != nil {
				return e
			}
			if target == "windows-x64" {
				base := filepath.Join(o.get("base-msi-directory"), "git-next-0.2.999-"+target+suffix+".msi")
				entry["base_msi_"+scope], e = pin(base)
				if e != nil {
					return e
				}
				if _, e = pin(strings.TrimSuffix(base, ".msi") + ".receipt.json"); e != nil {
					return e
				}
			}
		}
		platforms[target] = entry
	}
	for _, target := range []string{"linux-x64", "linux-arm64"} {
		root := o.get(target)
		manifest, e := load(filepath.Join(root, "package.json"))
		if e != nil {
			return e
		}
		receipt, e := load(filepath.Join(root, "assembly.json"))
		if e != nil {
			return e
		}
		if manifest["platform"] != target || !allTrue(object(receipt, "cleanup")) {
			return fmt.Errorf("wrong or uncleaned POSIX package")
		}
		identities[stringValue(manifest, "source_tree_sha256")] = true
		archive := filepath.Join(root, "distribution.tar.gz")
		if e = requireHash(archive, object(receipt, "artifact")["sha256"]); e != nil {
			return e
		}
		p, e := pin(archive)
		if e != nil {
			return e
		}
		platforms[target] = record{"archive": p, "base_image": nativeBases[target]}
		for _, name := range []string{"package.json", "assembly.json"} {
			if _, e = pin(filepath.Join(root, name)); e != nil {
				return e
			}
		}
	}
	mac := o.get("macos-arm64")
	archive, e := pin(filepath.Join(mac, "macos-distribution.tar.gz"))
	if e != nil {
		return e
	}
	platforms["macos-arm64"] = record{"archive": archive, "base_image": nativeBases["linux-x64"]}
	core, e := load(filepath.Join(mac, "core-build.json"))
	if e != nil {
		return e
	}
	identities[stringValue(core, "source_tree_sha256")] = true
	if len(identities) != 1 || identities[""] {
		return fmt.Errorf("five cores must share a frozen source identity")
	}
	var sourceIdentity string
	for s := range identities {
		sourceIdentity = s
	}
	sources, e := load(filepath.Join(o.get("source-bundles"), "sources.json"))
	if e != nil {
		return e
	}
	for _, raw := range array(sources, "bundles") {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid source bundle")
		}
		p, e := confined(o.get("source-bundles"), stringValue(item, "name"))
		if e != nil {
			return e
		}
		if e = requireHash(p, item["sha256"]); e != nil {
			return e
		}
		if _, e = pin(p); e != nil {
			return e
		}
	}
	if _, e = pin(filepath.Join(o.get("source-bundles"), "sources.json")); e != nil {
		return e
	}
	harnesses := record{}
	extensions := map[string]bool{".ps1": true, ".psm1": true, ".cs": true, ".go": true, ".mod": true, ".sum": true, ".json": true, ".wxs": true, ".sh": true, ".Dockerfile": true, ".patch": true, ".c": true, ".h": true, ".rs": true, ".toml": true, ".lock": true}
	for _, folder := range []string{"tools", "tests", "packaging", "native-core", "accept-runner"} {
		e = filepath.WalkDir(a.path(folder), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				if d.Name() == "target" || d.Name() == ".git" || d.Name() == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if extensions[filepath.Ext(p)] || strings.HasPrefix(d.Name(), "Dockerfile") {
				h, e := digest(p)
				if e != nil {
					return e
				}
				rel, _ := filepath.Rel(a.root, p)
				harnesses[filepath.ToSlash(rel)] = h
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	e = filepath.WalkDir(peer, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(peer, p)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			for _, s := range parts {
				if s == ".git" || s == ".evidence" || s == "target" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(p) == ".rs" || parts[0] == "assets" || parts[0] == "shell-integration" || parts[0] == "tests" && filepath.Ext(p) == ".go" || filepath.ToSlash(rel) == "Cargo.toml" || filepath.ToSlash(rel) == "Cargo.lock" || filepath.ToSlash(rel) == "vendor/gpui-terminal/Cargo.toml" {
			h, e := digest(p)
			if e != nil {
				return e
			}
			harnesses["../git-terminal/"+filepath.ToSlash(rel)] = h
		}
		return nil
	})
	if e != nil {
		return e
	}
	h, e := digest(a.path("install.ps1"))
	if e != nil {
		return e
	}
	harnesses["install.ps1"] = h
	race, e := pin(o.get("race-binary"))
	if e != nil {
		return e
	}
	report := record{"schema_version": 1, "product": "git-next", "version": version, "source_tree_sha256": sourceIdentity, "terminal_source": peer, "platforms": platforms, "files": pinned, "harnesses": harnesses, "race_binary": race, "go_race_image": "golang@sha256:55395706e9703db746cc507abfc4eb2aea75918f8a8024e4848e2cb81004f5ad", "product_executed": false, "tests_executed": false, "release_eligible": false}
	if e = writeNewJSON(o.get("output"), report); e != nil {
		return e
	}
	return a.json(record{"status": "native-batch-inputs-frozen", "platforms": len(platforms), "files": len(pinned), "harnesses": len(harnesses)})
}
func allTrue(m record) bool {
	if len(m) == 0 {
		return false
	}
	for _, v := range m {
		if v != true {
			return false
		}
	}
	return true
}
func requireImplementationComplete(a *app) error {
	s, e := load(a.path("docs/DELIVERY_STATUS.json"))
	if e != nil {
		return e
	}
	if number(s, "schema_version") != 1 || !truth(s, "scope_frozen") || !truth(object(s, "policy"), "all_implementation_before_regression") {
		return fmt.Errorf("invalid fixed implementation scope")
	}
	expected := map[string]bool{"D01": true, "D02": true, "D03": true, "D04": true, "D05": true, "D06": true, "D07": true}
	count := 0
	for _, raw := range array(s, "deliverables") {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid deliverable")
		}
		if item["phase"] != "implementation" {
			continue
		}
		id := stringValue(item, "id")
		if !expected[id] {
			return fmt.Errorf("unexpected/duplicate implementation scope: %s", id)
		}
		delete(expected, id)
		count++
		if item["status"] != "complete" || !completionEvidencePresent(item["completion_evidence"]) {
			return fmt.Errorf("all features must be implemented before regression; pending %s", id)
		}
	}
	if count != 7 {
		return fmt.Errorf("seven complete implementation deliverables required")
	}
	return nil
}
func completionEvidencePresent(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return true
	}
}
