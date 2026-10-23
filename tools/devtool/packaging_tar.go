// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	register("refresh-native-posix-defaults", "Refresh POSIX defaults and notices without changing native images", "archive output", refreshPOSIXDefaults)
	register("refresh-release-bootstrap", "Refresh only an accepted standalone bootstrap asset", "stage acceptance git", refreshBootstrap)
	register("repack-posix-updater", "Replace source-pinned updater and existing manuals in POSIX candidate", "archive sha256 updater-directory destination", repackPOSIXUpdater)
}
func releaseDocuments(a *app) (map[string][]byte, error) {
	names := map[string]bool{"README.md": true, "README.zh-CN.md": true, "AGENTS.md": true, "docs/DELIVERY_STATUS.json": true}
	e := filepath.WalkDir(a.path("docs"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(p)
		publicEvidence := strings.HasPrefix(d.Name(), "native-") || strings.HasPrefix(d.Name(), "windows-read-safety-")
		if ext == ".md" || ext == ".html" || ext == ".gitignore" || ext == ".gitattributes" || ext == ".json" && publicEvidence {
			rel, _ := filepath.Rel(a.root, p)
			names[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	result := map[string][]byte{}
	for name := range names {
		p, e := confined(a.root, name)
		if e != nil {
			return nil, e
		}
		st, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe release document")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		result[name] = b
	}
	return result, nil
}
func tarJSON(archive string, names ...string) (map[string]record, error) {
	result := map[string]record{}
	want := map[string]bool{}
	for _, name := range names {
		want[name] = true
	}
	seen := map[string]bool{}
	var total int64
	e := eachArchive(archive, func(m archiveMember, r io.Reader) error {
		name := path.Clean(m.name)
		if name == "." {
			return nil
		}
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:") || seen[name] {
			return fmt.Errorf("unsafe/duplicate archive member: %s", name)
		}
		seen[name] = true
		total += m.size
		if total > 4<<30 || len(seen) > 100000 {
			return fmt.Errorf("archive bounds exceeded")
		}
		if !want[name] {
			return nil
		}
		if m.symlink || m.hardlink || m.directory || m.size > 128<<20 {
			return fmt.Errorf("invalid manifest member")
		}
		var value record
		if e := json.NewDecoder(io.LimitReader(r, (128<<20)+1)).Decode(&value); e != nil {
			return e
		}
		result[name] = value
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, name := range names {
		if result[name] == nil {
			return nil, fmt.Errorf("archive lacks %s", name)
		}
	}
	return result, nil
}
func rewriteTar(archive, destination, prefix string, replacements map[string][]byte, level int, canonical bool) error {
	if exists(destination) {
		return fmt.Errorf("archive destination must be new")
	}
	if e := os.MkdirAll(filepath.Dir(destination), 0755); e != nil {
		return e
	}
	out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(destination)
		}
	}()
	gz, e := gzip.NewWriterLevel(out, level)
	if e != nil {
		return e
	}
	tw := tar.NewWriter(gz)
	seen := map[string]bool{}
	var total int64
	e = eachArchive(archive, func(m archiveMember, r io.Reader) error {
		name := path.Clean(m.name)
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:") || seen[name] {
			return fmt.Errorf("unsafe/duplicate source archive member: %s", name)
		}
		seen[name] = true
		total += m.size
		if total > 4<<30 || len(seen) > 100000 {
			return fmt.Errorf("archive bounds exceeded")
		}
		h := &tar.Header{Name: m.name, Mode: int64(m.mode.Perm()), Size: m.size, ModTime: m.modified, Typeflag: tar.TypeReg, Linkname: m.link, Format: tar.FormatPAX}
		if canonical {
			if prefix == "" {
				h.Name = "./" + name
			} else if name == "." {
				h.Name = prefix
			} else {
				h.Name = prefix + "/" + name
			}
		}
		switch {
		case m.directory:
			h.Typeflag = tar.TypeDir
			h.Size = 0
		case m.symlink:
			h.Typeflag = tar.TypeSymlink
			h.Size = 0
			if strings.HasPrefix(m.link, "/") || strings.Contains(m.link, "\\") {
				return fmt.Errorf("unsafe archive symlink")
			}
			resolved := path.Clean(path.Join(path.Dir(name), m.link))
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return fmt.Errorf("escaping archive symlink")
			}
		case m.hardlink:
			h.Typeflag = tar.TypeLink
			h.Size = 0
			link := path.Clean(m.link)
			if path.IsAbs(link) || link == ".." || strings.HasPrefix(link, "../") || strings.ContainsAny(link, "\\:") {
				return fmt.Errorf("unsafe archive hard link")
			}
			if prefix != "" {
				h.Linkname = prefix + "/" + link
			}
		}
		if b, changed := replacements[name]; changed {
			if m.directory || m.symlink || m.hardlink {
				return fmt.Errorf("replacement is not regular file")
			}
			h.Size = int64(len(b))
			if canonical {
				h.Mode = 0644
				if strings.HasPrefix(name, "runtime/usr/share/git-core/templates/hooks/") {
					h.Mode = 0755
				}
			}
			r = bytes.NewReader(b)
		}
		if e := tw.WriteHeader(h); e != nil {
			return e
		}
		if h.Typeflag == tar.TypeReg {
			n, e := io.CopyN(tw, r, h.Size)
			if e != nil {
				return e
			}
			if n != h.Size {
				return fmt.Errorf("short archive member")
			}
		}
		return nil
	})
	if e == nil {
		keys := make([]string, 0, len(replacements))
		for name := range replacements {
			if !seen[name] {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		for _, name := range keys {
			if _, err := confined(filepath.Dir(destination), name); err != nil {
				e = err
				break
			}
			b := replacements[name]
			mode := int64(0644)
			if strings.HasPrefix(name, "runtime/usr/share/git-core/templates/hooks/") {
				mode = 0755
			}
			target := "./" + name
			if prefix != "" {
				target = prefix + "/" + name
			}
			if e = tw.WriteHeader(&tar.Header{Name: target, Size: int64(len(b)), Mode: mode, Typeflag: tar.TypeReg}); e != nil {
				break
			}
			if _, e = tw.Write(b); e != nil {
				break
			}
		}
	}
	ce := tw.Close()
	ge := gz.Close()
	fe := out.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if ge != nil {
		return ge
	}
	if fe != nil {
		return fe
	}
	ok = true
	return nil
}
func refreshPOSIXDefaults(a *app, o options) error {
	if e := o.require("archive", "output"); e != nil {
		return e
	}
	manifests, e := tarJSON(o.get("archive"), "package.json", "git-next-distribution.json")
	if e != nil {
		return e
	}
	pkg, report := manifests["package.json"], manifests["git-next-distribution.json"]
	replacements, e := releaseDocuments(a)
	if e != nil {
		return e
	}
	for _, name := range []string{"unity.gitignore", "unity.gitattributes", "unreal.gitignore", "unreal.gitattributes"} {
		b, e := os.ReadFile(a.path("docs/game-profiles", name))
		if e != nil {
			return e
		}
		replacements["docs/game-profiles/"+name] = b
	}
	for _, name := range []string{"pre-push", "post-checkout", "post-commit", "post-merge", "LICENSE.md", "source.json"} {
		b, e := os.ReadFile(a.path("packaging/lfs-hooks", name))
		if e != nil {
			return e
		}
		target := "runtime/usr/share/git-core/templates/hooks/" + name
		if name == "LICENSE.md" || name == "source.json" {
			target = "LICENSES/git-lfs-hook-templates/" + name
		}
		replacements[target] = b
	}
	e = eachArchive(o.get("archive"), func(m archiveMember, r io.Reader) error {
		if path.Clean(m.name) != "runtime/etc/gitconfig" {
			return nil
		}
		b, e := io.ReadAll(io.LimitReader(r, 1<<20))
		if e != nil {
			return e
		}
		if !bytes.Contains(b, []byte(`[filter "lfs"]`)) {
			b = append(b, []byte("[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n")...)
		}
		replacements["runtime/etc/gitconfig"] = b
		return nil
	})
	if e != nil {
		return e
	}
	for name, b := range replacements {
		mode := 0644
		if strings.HasPrefix(name, "runtime/usr/share/git-core/templates/hooks/") {
			mode = 0755
		}
		entry := record{"kind": "file", "sha256": digestBytes(b), "bytes": len(b), "mode": mode, "component": "product-defaults-and-documentation"}
		object(pkg, "files")[name] = entry
		copyEntry := record{}
		for k, v := range entry {
			copyEntry[k] = v
		}
		object(report, "files")[name] = copyEntry
	}
	h, _ := digest(o.get("archive"))
	for _, v := range []record{pkg, report} {
		v["release_eligible"] = false
		v["source_commit"] = ""
		v["packaging_delta"] = record{"source_archive_sha256": h, "core_source_tree_sha256": pkg["source_tree_sha256"], "native_core_and_runtime_images_changed": false, "builtin_lfs_filters_and_template_hooks": true}
	}
	replacements["package.json"] = encoded(pkg)
	entry := rec(object(report, "files")["package.json"])
	if entry == nil {
		return fmt.Errorf("package manifest not owned by distribution")
	}
	entry["sha256"] = digestBytes(replacements["package.json"])
	entry["bytes"] = len(replacements["package.json"])
	replacements["git-next-distribution.json"] = encoded(report)
	if e = rewriteTar(o.get("archive"), o.get("output"), "", replacements, 6, true); e != nil {
		return e
	}
	h, e = digest(o.get("output"))
	if e != nil {
		return e
	}
	return a.json(record{"status": "defaults-refreshed-not-runtime-accepted", "platform": pkg["platform"], "source_tree_sha256": pkg["source_tree_sha256"], "sha256": h})
}
func refreshBootstrap(a *app, o options) error {
	if e := o.require("stage", "acceptance", "git"); e != nil {
		return e
	}
	stage, e := absolute(o.get("stage"))
	if e != nil {
		return e
	}
	proof, e := load(filepath.Join(stage, "native-release-acceptance.json"))
	if e != nil {
		return e
	}
	checks, e := load(o.get("acceptance"))
	if e != nil {
		return e
	}
	passed := false
	for _, v := range array(checks, "results") {
		item := rec(v)
		passed = passed || item["case"] == "real-gui-terminal-version-is-awaited-and-captured" && item["status"] == "pass"
	}
	if checks["status"] != "pass" || !passed {
		return fmt.Errorf("actual GUI terminal acceptance required")
	}
	head, e := a.mustRun(o.get("git"), "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	commit := strings.TrimSpace(string(head))
	diff, e := a.mustRun(o.get("git"), "diff", "--name-only", stringValue(proof, "source_commit")+".."+commit)
	if e != nil {
		return e
	}
	allowed := map[string]bool{"install.ps1": true, "tests/bootstrap_acceptance.ps1": true, "tools/refresh_release_bootstrap.py": true, "tools/devtool/packaging_tar.go": true, "docs/INSTALLATION.md": true, "docs/site/installation.html": true}
	changed := map[string]bool{}
	for _, name := range strings.Fields(string(diff)) {
		if !allowed[name] {
			return fmt.Errorf("runtime/payload change cannot reuse accepted bytes: %s", name)
		}
		changed[name] = true
	}
	if !changed["install.ps1"] || !changed["tests/bootstrap_acceptance.ps1"] {
		return fmt.Errorf("bootstrap implementation and acceptance changes required")
	}
	sums, e := read(filepath.Join(stage, "SHA256SUMS"))
	if e != nil {
		return e
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(sums), "\n") {
		h, name, ok := strings.Cut(line, "  ")
		if !ok || name != filepathBase(name) {
			return fmt.Errorf("unsafe staged asset")
		}
		actual, e := digest(filepath.Join(stage, name))
		if e != nil || actual != h {
			return fmt.Errorf("staged asset changed")
		}
		names = append(names, name)
	}
	if e = copyFile(a.path("install.ps1"), filepath.Join(stage, "install.ps1")); e != nil {
		return e
	}
	h, _ := digest(filepath.Join(stage, "install.ps1"))
	ah, _ := digest(o.get("acceptance"))
	proof["standalone_bootstrap"] = record{"source_commit": commit, "sha256": h, "acceptance_sha256": ah, "included_in_msi_payload": false, "scope": "Explicit GUI process wait/capture and preflight before original removal; all previously accepted MSI and archive bytes unchanged"}
	if e = writeJSON(filepath.Join(stage, "native-release-acceptance.json"), proof); e != nil {
		return e
	}
	var b strings.Builder
	for _, name := range names {
		h, e := digest(filepath.Join(stage, name))
		if e != nil {
			return e
		}
		fmt.Fprintf(&b, "%s  %s\n", h, name)
	}
	if e = write(filepath.Join(stage, "SHA256SUMS"), []byte(b.String()), 0644); e != nil {
		return e
	}
	return a.json(record{"status": "standalone-bootstrap-refreshed", "runtime_source_commit": proof["source_commit"], "bootstrap_source_commit": commit, "unchanged_runtime_assets": len(names) - 2})
}
func repackPOSIXUpdater(a *app, o options) error {
	if e := o.require("archive", "sha256", "updater-directory", "destination"); e != nil {
		return e
	}
	h, e := digest(o.get("archive"))
	if e != nil {
		return e
	}
	if h != o.get("sha256") {
		return fmt.Errorf("original archive differs from frozen identity")
	}
	receipt, e := load(filepath.Join(o.get("updater-directory"), "receipt.json"))
	if e != nil {
		return e
	}
	sources := record{}
	entries, e := os.ReadDir(a.path("packaging/posix/updater"))
	if e != nil {
		return e
	}
	for _, v := range entries {
		if !v.IsDir() {
			h, e := digest(a.path("packaging/posix/updater", v.Name()))
			if e != nil {
				return e
			}
			sources[v.Name()] = h
		}
	}
	if !equalJSON(receipt["sources"], sources) {
		return fmt.Errorf("updater receipt differs from current sources")
	}
	manifests, e := tarJSON(o.get("archive"), "package.json", "git-next-distribution.json")
	if e != nil {
		return e
	}
	pkg, report := manifests["package.json"], manifests["git-next-distribution.json"]
	updater := filepath.Join(o.get("updater-directory"), stringValue(pkg, "platform"), "updater")
	if e = shaMatches(updater, object(object(receipt, "binaries"), stringValue(pkg, "platform"))); e != nil {
		return e
	}
	b, e := os.ReadFile(updater)
	if e != nil {
		return e
	}
	replacements := map[string][]byte{"libexec/git-next/updater": b}
	for name, v := range object(pkg, "files") {
		item := rec(v)
		if strings.HasPrefix(name, "docs/") && item["kind"] == "file" && exists(a.path(filepath.FromSlash(name))) {
			h, e := digest(a.path(filepath.FromSlash(name)))
			if e != nil {
				return e
			}
			if h != item["sha256"] {
				b, e := os.ReadFile(a.path(filepath.FromSlash(name)))
				if e != nil {
					return e
				}
				replacements[name] = b
			}
		}
	}
	for name, b := range replacements {
		for _, v := range []record{pkg, report} {
			entry := rec(object(v, "files")[name])
			if entry == nil || entry["kind"] != "file" {
				return fmt.Errorf("replacement not existing regular owned member: %s", name)
			}
			entry["sha256"] = digestBytes(b)
			entry["bytes"] = len(b)
		}
	}
	replacements["package.json"] = encoded(pkg)
	entry := rec(object(report, "files")["package.json"])
	if entry == nil {
		return fmt.Errorf("package manifest not owned")
	}
	entry["sha256"] = digestBytes(replacements["package.json"])
	entry["bytes"] = len(replacements["package.json"])
	rh, _ := digest(filepath.Join(o.get("updater-directory"), "receipt.json"))
	report["updater_build_receipt_sha256"] = rh
	report["repacked_from_sha256"] = o.get("sha256")
	replacements["git-next-distribution.json"] = encoded(report)
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	name := "distribution.tar.gz"
	if pkg["platform"] == "macos-arm64" {
		name = "macos-distribution.tar.gz"
	}
	target := filepath.Join(out, name)
	if e = rewriteTar(o.get("archive"), target, "", replacements, 3, false); e != nil {
		return e
	}
	for _, name := range []string{"package.json", "git-next-distribution.json"} {
		if e = write(filepath.Join(out, name), replacements[name], 0644); e != nil {
			return e
		}
	}
	assembly := record{"cleanup": record{"container_absent": true, "volume_absent": true}}
	original := filepath.Join(filepath.Dir(o.get("archive")), "assembly.json")
	if exists(original) {
		assembly, e = load(original)
		if e != nil {
			return e
		}
	}
	h, _ = digest(target)
	st, _ := os.Stat(target)
	for k, v := range (record{"schema_version": 1, "status": "assembled-not-runtime-accepted", "platform": pkg["platform"], "source_tree_sha256": pkg["source_tree_sha256"], "repacked_from_sha256": o.get("sha256"), "updater_build_receipt_sha256": rh, "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false, "artifact": record{"name": name, "bytes": st.Size(), "sha256": h}}) {
		assembly[k] = v
	}
	if e = writeNewJSON(filepath.Join(out, "assembly.json"), assembly); e != nil {
		return e
	}
	original = filepath.Join(filepath.Dir(o.get("archive")), "core-build.json")
	if exists(original) {
		if e = copyFile(original, filepath.Join(out, "core-build.json")); e != nil {
			return e
		}
	}
	return a.json(record{"status": assembly["status"], "platform": pkg["platform"], "replacements": len(replacements)})
}
