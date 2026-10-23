// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"

	"strings"
)

func init() {
	register("assemble-native-windows", "Assemble owned Windows core and runtime without loading target code", "source build-dir build-receipt runtime-assets crt-dir psl bridge terminal msi-worker git-p4 destination", assembleNativeWindows)
	register("assemble-native-posix", "Assemble POSIX core, runtime and updater with static image relocation", "source core-stage core-receipt runtime-assets shared-assets updater git-p4 destination source-commit", assembleNativePOSIX)
}

type distributionBuilder struct {
	root, target string
	records      record
	images       []string
	links        [][3]string
	windows      bool
}

func (b *distributionBuilder) store(name string, data []byte, component string, mode uint32, attributes int64) error {
	p, e := confined(b.root, name)
	if e != nil {
		return e
	}
	if b.records[name] != nil && !b.windows {
		return fmt.Errorf("duplicate distribution member: %s", name)
	}
	if e = write(p, data, os.FileMode(mode)); e != nil {
		return e
	}
	if attributes&4 != 0 {
		if e = setSystemFileAttribute(p); e != nil {
			return e
		}
	}
	entry := record{"kind": "file", "sha256": digestBytes(data), "bytes": len(data), "component": component, "mode": mode}
	if b.windows {
		entry = record{"sha256": digestBytes(data), "bytes": len(data), "kind": component, "windows_attributes": attributes}
	}
	b.records[name] = entry
	return nil
}
func (b *distributionBuilder) copy(source, name, component string, mode uint32, attributes int64) error {
	p, e := confined(b.root, name)
	if e != nil {
		return e
	}
	if b.records[name] != nil && !b.windows {
		return fmt.Errorf("duplicate distribution member: %s", name)
	}
	if e = copyFile(source, p); e != nil {
		return e
	}
	if e = os.Chmod(p, os.FileMode(mode)); e != nil {
		return e
	}
	if attributes&4 != 0 {
		if e = setSystemFileAttribute(p); e != nil {
			return e
		}
	}
	h, e := digest(p)
	if e != nil {
		return e
	}
	st, e := os.Stat(p)
	if e != nil {
		return e
	}
	entry := record{"kind": "file", "sha256": h, "bytes": st.Size(), "mode": mode, "component": component, "input_sha256": h}
	if b.windows {
		entry = record{"sha256": h, "bytes": st.Size(), "kind": component, "windows_attributes": attributes}
	} else {
		machine, e := imagePlatform(p)
		if e != nil {
			return e
		}
		if machine != "" {
			if machine != b.target {
				return fmt.Errorf("mixed native architecture: %s", name)
			}
			b.images = append(b.images, p)
		} else {
			f, e := os.Open(p)
			if e != nil {
				return e
			}
			magic := make([]byte, 4)
			_, _ = f.Read(magic)
			f.Close()
			if string(magic) == "\x7fELF" || string(magic) == "\xca\xfe\xba\xbe" || string(magic) == "\xcf\xfa\xed\xfe" {
				return fmt.Errorf("uninspected native image: %s", name)
			}
		}
	}
	b.records[name] = entry
	return nil
}
func (b *distributionBuilder) link(name, target, component string) error {
	if _, e := confined(b.root, name); e != nil {
		return e
	}
	if b.records[name] != nil {
		return fmt.Errorf("duplicate distribution link")
	}
	if path.IsAbs(target) || strings.ContainsAny(target, "\\:") {
		return fmt.Errorf("distribution link must remain relative")
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if _, e := confined(b.root, resolved); e != nil {
		return e
	}
	b.links = append(b.links, [3]string{name, target, resolved})
	b.records[name] = record{"kind": "symlink", "target": target, "component": component}
	return nil
}
func (b *distributionBuilder) finishLinks() error {
	pending := b.links
	for len(pending) > 0 {
		next := [][3]string{}
		progress := false
		for _, v := range pending {
			if b.records[v[2]] == nil {
				return fmt.Errorf("link target not owned: %s", v[2])
			}
			target := filepath.Join(b.root, filepath.FromSlash(v[2]))
			if !exists(target) {
				next = append(next, v)
				continue
			}
			p := filepath.Join(b.root, filepath.FromSlash(v[0]))
			if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
				return e
			}
			if e := os.Symlink(v[1], p); e != nil {
				return e
			}
			resolved, e := filepath.EvalSymlinks(p)
			if e != nil || !inside(b.root, resolved) {
				return fmt.Errorf("distribution link escaped root")
			}
			progress = true
		}
		if !progress {
			return fmt.Errorf("circular/unresolved distribution links")
		}
		pending = next
	}
	return nil
}
func (b *distributionBuilder) notices(a *app) error {
	for _, name := range []string{"README.md", "README.zh-CN.md", "AGENTS.md"} {
		if e := b.copy(a.path(name), name, "product-documentation", 0644, 0); e != nil {
			return e
		}
	}
	for _, pair := range [][2]string{{"packaging/native-compatibility.json", "compatibility.json"}, {"native-core/LICENSE.GPL-2.0", "LICENSE"}, {"packaging/corresponding-sources.lock.json", "LICENSES/corresponding-sources.lock.json"}} {
		if e := b.copy(a.path(filepath.FromSlash(pair[0])), pair[1], "product-documentation-and-source", 0644, 0); e != nil {
			return e
		}
	}
	for _, folder := range []string{"docs", "native-core"} {
		e := filepath.WalkDir(a.path(folder), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				if d.Name() == "__pycache__" || d.Name() == ".github" {
					return filepath.SkipDir
				}
				return nil
			}
			relative, _ := filepath.Rel(a.root, p)
			relative = filepath.ToSlash(relative)
			if b.records[relative] != nil {
				return nil
			}
			return b.copy(p, relative, "product-documentation-and-source", 0644, 0)
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func builtinsAt(source string) ([]string, error) {
	text, e := read(filepath.Join(source, "git.c"))
	if e != nil {
		return nil, e
	}
	_, table, ok := strings.Cut(text, "static struct cmd_struct commands[] = {")
	if !ok {
		return nil, fmt.Errorf("native builtin table missing")
	}
	table, _, _ = strings.Cut(table, "\n};")
	seen := map[string]bool{}
	for _, v := range regexp.MustCompile(`\{\s*"([a-z][a-z0-9-]*)"\s*,`).FindAllStringSubmatch(table, -1) {
		seen[v[1]] = true
	}
	if len(seen) < 100 {
		return nil, fmt.Errorf("native builtin table incomplete")
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
func posixRelative(from, to string) string {
	p, e := filepath.Rel(filepath.FromSlash(from), filepath.FromSlash(to))
	if e != nil {
		return ""
	}
	return filepath.ToSlash(p)
}
func assembleNativePOSIX(a *app, o options) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("POSIX assembly requires native filesystem/image tools")
	}
	if e := o.require("source", "core-stage", "core-receipt", "runtime-assets", "shared-assets", "updater", "git-p4", "destination"); e != nil {
		return e
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	core, e := absolute(o.get("core-stage"))
	if e != nil {
		return e
	}
	assets, e := absolute(o.get("runtime-assets"))
	if e != nil {
		return e
	}
	shared, e := absolute(o.get("shared-assets"))
	if e != nil {
		return e
	}
	build, e := load(o.get("core-receipt"))
	if e != nil {
		return e
	}
	runtimeManifest, e := load(filepath.Join(assets, "git-next-posix-runtime.json"))
	if e != nil {
		return e
	}
	sharedManifest, e := load(filepath.Join(shared, "git-next-shared-runtime.json"))
	if e != nil {
		return e
	}
	target := stringValue(build, "platform")
	if target != "linux-x64" && target != "linux-arm64" && target != "macos-arm64" || runtimeManifest["platform"] != target || number(sharedManifest, "schema_version") != 1 || sharedManifest["version"] != "2.54.0" {
		return fmt.Errorf("core/runtime/shared targets differ")
	}
	identity, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	if build["source_tree_sha256"] != identity {
		return fmt.Errorf("core receipt differs from integrated source")
	}
	machine, e := imagePlatform(o.get("updater"))
	if e != nil || machine != target {
		return fmt.Errorf("updater architecture differs")
	}
	if o.get("source-commit") != "" && !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(o.get("source-commit")) {
		return fmt.Errorf("complete immutable source SHA required")
	}
	compat, e := nativeCompatibility(a)
	if e != nil {
		return e
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	b := distributionBuilder{root: out, target: target, records: record{}}
	for _, name := range sortedKeys(object(runtimeManifest, "files")) {
		if pythonRuntimeMember(name) {
			continue
		}
		item := rec(object(runtimeManifest, "files")[name])
		switch item["kind"] {
		case "file":
			p, e := verifiedFile(assets, name, item)
			if e != nil {
				return e
			}
			if e = b.copy(p, "runtime/"+name, "owned-runtime/"+stringValue(item, "component"), uint32(number(item, "mode")), 0); e != nil {
				return e
			}
		case "symlink":
			if e = b.link("runtime/"+name, stringValue(item, "target"), "owned-runtime-link"); e != nil {
				return e
			}
		default:
			return fmt.Errorf("unsupported runtime member")
		}
	}
	for _, command := range []string{"gpg", "gpgsm", "gpg-agent"} {
		name := "runtime/usr/bin/" + command
		if item := rec(b.records[name]); item == nil || item["kind"] != "file" {
			continue
		}
		actual := "runtime/usr/bin/.gnupg/" + command
		old := filepath.Join(out, filepath.FromSlash(name))
		moved := filepath.Join(out, filepath.FromSlash(actual))
		if e = os.MkdirAll(filepath.Dir(moved), 0755); e != nil {
			return e
		}
		if e = os.Rename(old, moved); e != nil {
			return e
		}
		b.records[actual] = b.records[name]
		delete(b.records, name)
		for i, p := range b.images {
			if p == old {
				b.images[i] = moved
			}
		}
		argument := " --agent-program \"$directory/gpg-agent\""
		if command == "gpg-agent" {
			argument = ""
			if b.records["runtime/usr/bin/pinentry"] != nil {
				argument += " --pinentry-program \"$directory/pinentry\""
			}
			if b.records["runtime/usr/lib/gnupg/scdaemon"] != nil {
				argument += " --scdaemon-program \"$directory/../lib/gnupg/scdaemon\""
			}
		}
		launcher := "#!/bin/sh\nset -eu\ndirectory=$(CDPATH= cd -- \"${0%/*}\" && pwd -P)\nexec \"$directory/.gnupg/" + command + "\"" + argument + " \"$@\"\n"
		if e = b.store(name, []byte(launcher), "owned-signing-agent-paths", 0755, 0); e != nil {
			return e
		}
	}
	staged := object(build, "staged_files")
	mainHash := stringValue(rec(staged["bin/git"]), "sha256")
	if e = shaMatches(filepath.Join(core, "bin/git"), record{"sha256": mainHash}); e != nil {
		return e
	}
	if e = b.copy(filepath.Join(core, "bin/git"), "bin/git", "source-built-git-next-core", 0755, 0); e != nil {
		return e
	}
	if e = b.link("bin/git-next", "git", "git-next-command-alias"); e != nil {
		return e
	}
	for _, name := range sortedKeys(staged) {
		if pythonRuntimeMember(name) {
			continue
		}
		item := rec(staged[name])
		output := "runtime/usr/" + name
		if item["kind"] == "file" {
			p, e := verifiedFile(core, name, item)
			if e != nil {
				return e
			}
			if item["sha256"] == mainHash {
				e = b.link(output, posixRelative(path.Dir(output), "bin/git"), "owned-core-alias")
			} else {
				e = b.copy(p, output, "source-built-git-next-core-helper", uint32(number(item, "mode")), 0)
			}
		} else if item["kind"] == "symlink" {
			e = b.link(output, stringValue(item, "target"), "source-built-core-link")
		} else {
			return fmt.Errorf("unexpected core staging member")
		}
		if e != nil {
			return e
		}
		if strings.HasPrefix(name, "bin/") && name != "bin/git" {
			if e = b.link(name, posixRelative("bin", output), "public-git-tool"); e != nil {
				return e
			}
		}
	}
	for _, name := range sortedKeys(object(sharedManifest, "files")) {
		if pythonRuntimeMember(name) {
			continue
		}
		item := rec(object(sharedManifest, "files")[name])
		if item["kind"] != "file" {
			return fmt.Errorf("shared member not regular")
		}
		p, e := verifiedFile(shared, name, item)
		if e != nil {
			return e
		}
		if e = b.copy(p, "runtime/"+name, "portable-git-data", uint32(number(item, "mode")), 0); e != nil {
			return e
		}
	}
	for _, command := range []string{"gitk", "git-lfs", "git-credential-manager", "git-credential-manager-core"} {
		launcher := "#!/bin/sh\n# Copyright 2026 git-next contributors. MIT license.\nset -eu\nself=$0\ncase \"$self\" in */*) ;; *) self=$(command -v \"$self\");; esac\nbase=${self%/*}\nroot=$(CDPATH= cd -- \"$base/..\" && pwd -P)\nPATH=\"$root/bin:$root/runtime/usr/bin${PATH:+:$PATH}\"\nexport PATH\nexec \"$root/runtime/usr/bin/" + command + "\" \"$@\"\n"
		if e = b.store("bin/"+command, []byte(launcher), "public-owned-helper-launcher", 0755, 0); e != nil {
			return e
		}
	}
	builtins, e := builtinsAt(source)
	if e != nil {
		return e
	}
	for _, command := range builtins {
		name := "runtime/usr/libexec/git-core/git-" + command
		if b.records[name] == nil {
			if e = b.link(name, posixRelative(path.Dir(name), "bin/git"), "owned-builtin-alias"); e != nil {
				return e
			}
		}
	}
	for _, name := range []string{"usr/bin/ssh", "usr/bin/sh", "usr/bin/git-lfs", "usr/bin/git-credential-manager", "usr/bin/gpg"} {
		if b.records["runtime/"+name] == nil {
			return fmt.Errorf("required runtime entrypoint missing: %s", name)
		}
	}
	if e = b.copy(o.get("updater"), "libexec/git-next/updater", "owned-native-updater", 0755, 0); e != nil {
		return e
	}
	p4Receipt, e := b.addGitP4(a, o.get("git-p4"), target, "runtime/usr")
	if e != nil {
		return e
	}
	if e = b.copy(a.path("packaging/posix/install.sh"), "install.sh", "native-package-installer", 0755, 0); e != nil {
		return e
	}
	perl := map[string]bool{}
	rf := object(runtimeManifest, "files")
	tcl := map[string]map[string]bool{"init.tcl": {}, "tk.tcl": {}}
	for name := range rf {
		lower := strings.ToLower(name)
		if (strings.HasSuffix(name, "/FindBin.pm") || strings.HasSuffix(name, "/strict.pm")) && strings.Contains(lower, "/perl") {
			perl["runtime/"+path.Dir(name)] = true
		}
		if strings.HasSuffix(name, "/Config.pm") && rf[strings.TrimSuffix(name, "Config.pm")+"Config_heavy.pl"] != nil {
			perl["runtime/"+path.Dir(name)] = true
		}
		if strings.Contains(name, "/auto/") && strings.Contains(lower, "/perl") {
			prefix, _, _ := strings.Cut(name, "/auto/")
			perl["runtime/"+prefix] = true
		}
		for _, prefix := range []string{"usr/share/perl5", "usr/lib/perl5"} {
			if strings.HasPrefix(name, prefix+"/") {
				perl["runtime/"+prefix] = true
			}
		}
		for sentinel := range tcl {
			if strings.HasSuffix(name, "/"+sentinel) {
				tcl[sentinel]["runtime/"+path.Dir(name)] = true
			}
		}
	}
	if len(perl) == 0 {
		return fmt.Errorf("owned interpreter SDK lacks Perl standard libraries")
	}
	var environment []string
	for name := range perl {
		environment = append(environment, "PERL5LIB="+name)
	}
	sort.Strings(environment)
	for _, v := range [][2]string{{"init.tcl", "TCL_LIBRARY"}, {"tk.tcl", "TK_LIBRARY"}} {
		if len(tcl[v[0]]) != 1 {
			return fmt.Errorf("ambiguous Tcl/Tk standard library")
		}
		for name := range tcl[v[0]] {
			environment = append(environment, v[1]+"="+name)
		}
	}
	if e = b.store("runtime/interpreter-environment", []byte(strings.Join(environment, "\n")+"\n"), "owned-interpreter-paths", 0644, 0); e != nil {
		return e
	}
	if e = b.store("runtime/etc/gitconfig", []byte("[core]\n\tautocrlf = input\n\teol = lf\n[init]\n\tdefaultBranch = main\n[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n"), "managed-product-defaults", 0644, 0); e != nil {
		return e
	}
	if e = b.addLFSHooks(a, "runtime/usr"); e != nil {
		return e
	}
	if e = b.notices(a); e != nil {
		return e
	}
	if e = b.help(false, "runtime/usr"); e != nil {
		return e
	}
	if e = b.finishLinks(); e != nil {
		return e
	}
	if strings.HasPrefix(target, "linux-") {
		for _, p := range b.images {
			rel, _ := filepath.Rel(out, p)
			if rec(b.records[filepath.ToSlash(rel)])["component"] != "owned-runtime/primary-gcm" {
				if e = relocateELF(a, p, filepath.Join(out, "runtime/lib")); e != nil {
					return e
				}
			}
		}
		for _, p := range b.images {
			needed, e := elfNeeded(p)
			if e != nil {
				return e
			}
			for _, name := range needed {
				if !elfSystemLibraries[name] && b.records["runtime/lib/"+name] == nil {
					return fmt.Errorf("unresolved owned ELF dependency: %s", name)
				}
			}
		}
	} else {
		mapping := map[string]string{}
		for name, v := range object(runtimeManifest, "macho_library_map") {
			mapping[name] = "runtime/" + v.(string)
		}
		for _, p := range b.images {
			if e = relocateMacho(a, p, out, mapping); e != nil {
				return e
			}
			needed, e := machoNeeded(p)
			if e != nil {
				return e
			}
			for _, name := range needed {
				if machoSystem(name) {
					continue
				}
				if !strings.HasPrefix(name, "@loader_path/") {
					return fmt.Errorf("unresolved Mach-O dependency")
				}
				actual, e := filepath.EvalSymlinks(filepath.Join(filepath.Dir(p), strings.TrimPrefix(name, "@loader_path/")))
				if e != nil || !inside(out, actual) {
					return fmt.Errorf("Mach-O dependency escaped package")
				}
			}
		}
	}
	for name, v := range b.records {
		item := rec(v)
		if item["kind"] == "file" {
			p := filepath.Join(out, filepath.FromSlash(name))
			h, e := digest(p)
			if e != nil {
				return e
			}
			st, e := os.Stat(p)
			if e != nil {
				return e
			}
			item["sha256"] = h
			item["bytes"] = st.Size()
		}
	}
	pkg := record{"schema_version": 1, "product": "git-next", "engine": "source-built-git-next-native-core", "install_kind": "tar", "version": build["product_version"], "platform": target, "source_commit": o.get("source-commit"), "source_tree_sha256": build["source_tree_sha256"], "files": b.records, "builtins": builtins, "complete_git_parity": compat["complete_git_parity"], "release_eligible": false, "operating_system_libraries": runtimeManifest["operating_system_libraries"], "platform_requirements": runtimeManifest["platform_requirements"]}
	pkg["git_p4"], pkg["python_required"] = p4Receipt, false
	if e = b.store("package.json", encoded(pkg), "product-package-manifest", 0644, 0); e != nil {
		return e
	}
	ch, _ := digest(o.get("core-receipt"))
	rh, _ := digest(filepath.Join(assets, "git-next-posix-runtime.json"))
	sh, _ := digest(filepath.Join(shared, "git-next-shared-runtime.json"))
	report := record{"schema_version": 1, "status": "posix-distribution-assembled-not-runtime-accepted", "product": "git-next", "platform": target, "version": build["product_version"], "source_tree_sha256": build["source_tree_sha256"], "files": b.records, "native_images_inspected": len(b.images), "core_receipt_sha256": ch, "runtime_receipt_sha256": rh, "shared_receipt_sha256": sh, "upstream_git_core_deployed": false, "product_executed": false, "tests_executed": false, "installed": false, "complete_git_parity": compat["complete_git_parity"], "release_eligible": false}
	report["git_p4"], report["python_required"] = p4Receipt, false
	if e = writeNewJSON(filepath.Join(out, "git-next-distribution.json"), report); e != nil {
		return e
	}
	return a.json(record{"status": report["status"], "platform": target, "files": len(b.records), "product_executed": false})
}
func (b *distributionBuilder) addLFSHooks(a *app, prefix string) error {
	for _, name := range []string{"pre-push", "post-checkout", "post-commit", "post-merge"} {
		if e := b.copy(a.path("packaging/lfs-hooks", name), prefix+"/share/git-core/templates/hooks/"+name, "builtin-standard-lfs-hook", 0755, 0); e != nil {
			return e
		}
	}
	for _, name := range []string{"LICENSE.md", "source.json"} {
		if e := b.copy(a.path("packaging/lfs-hooks", name), "LICENSES/git-lfs-hook-templates/"+name, "lfs-hook-source-identity", 0644, 0); e != nil {
			return e
		}
	}
	return nil
}
func (b *distributionBuilder) help(windows bool, prefix string) error {
	for _, v := range [][3]string{{"language", "language.html", "get [--json] | set chinese|english|auto | list"}, {"eol", "line-endings.html", "[--apply]"}, {"update", "updates.html", "check|apply|status|enable|disable [--json]"}, {"doctor", "takeover.html", "[--json]"}, {"compatibility", "compatibility.html", "[--json]"}} {
		if windows && v[0] == "update" {
			continue
		}
		doc := "<!doctype html><meta charset=\"utf-8\"><title>git-next " + v[0] + "</title><a href=\"../../../../../docs/site/" + v[1] + "\">git-next " + v[0] + " manual</a>\n"
		if windows {
			doc = "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><title>git-next " + v[0] + "</title><p><a href=\"../../../../../docs/site/" + v[1] + "\">git-next " + v[0] + " manual</a></p></html>\n"
		}
		if e := b.store(prefix+"/share/doc/git-doc/git-"+v[0]+".html", []byte(doc), "product-help-entrypoint", 0644, 0); e != nil {
			return e
		}
		if !windows {
			manual := ".TH GIT-" + strings.ToUpper(v[0]) + " 1 \"2026\" \"git-next\" \"Git-next Manual\"\n.SH NAME\ngit-" + v[0] + " \\- git-next " + v[0] + " command\n.SH SYNOPSIS\n.B git " + v[0] + "\n" + v[2] + "\n.SH DESCRIPTION\nSee the installed git-next " + v[0] + " guide under docs/site/" + v[1] + ".\n"
			if e := b.store(prefix+"/share/man/man1/git-"+v[0]+".1", []byte(manual), "product-help-entrypoint", 0644, 0); e != nil {
				return e
			}
		}
	}
	return nil
}
func peInfo(p string) (uint16, []string, error) {
	data, e := os.ReadFile(p)
	if e != nil {
		return 0, nil, e
	}
	return peDependencies(data)
}
func bridgeImportsMatch(name string, libraries []string) bool {
	want := map[string]bool{"kernel32.dll": true, "advapi32.dll": true}
	if strings.Contains(strings.ToLower(name), "gui") {
		want["user32.dll"] = true
	}
	got := map[string]bool{}
	for _, name := range libraries {
		got[strings.ToLower(name)] = true
	}
	return equalJSON(want, got)
}
func peNumericVersionStatic(p string) (string, error) {
	f, e := pe.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	section := f.Section(".rsrc")
	if section == nil {
		return "", fmt.Errorf("native core lacks numeric product version")
	}
	data, e := section.Data()
	if e != nil {
		return "", e
	}
	signature := []byte{0xbd, 0x04, 0xef, 0xfe}
	i := bytes.Index(data, signature)
	if i < 0 || i+16 > len(data) {
		return "", fmt.Errorf("native version resource missing")
	}
	majorMinor := binary.LittleEndian.Uint32(data[i+8:])
	buildRevision := binary.LittleEndian.Uint32(data[i+12:])
	return fmt.Sprintf("%d.%d.%d.%d", majorMinor>>16, majorMinor&65535, buildRevision>>16, buildRevision&65535), nil
}
func assembleNativeWindows(a *app, o options) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows native assembly requires Windows")
	}
	if e := o.require("source", "build-dir", "build-receipt", "runtime-assets", "psl", "bridge", "msi-worker", "git-p4", "destination"); e != nil {
		return e
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	build, e := load(o.get("build-receipt"))
	if e != nil {
		return e
	}
	compat, e := nativeCompatibility(a)
	if e != nil {
		return e
	}
	text, e := read(filepath.Join(source, "git-next-identity.h"))
	if e != nil {
		return e
	}
	flag := "0"
	if truth(compat, "complete_git_parity") {
		flag = "1"
	}
	if !strings.Contains(text, "#define GIT_NEXT_COMPLETE_GIT_PARITY "+flag) {
		return fmt.Errorf("native parity declaration differs")
	}
	assets, e := absolute(o.get("runtime-assets"))
	if e != nil {
		return e
	}
	runtimeManifest, e := load(filepath.Join(assets, "git-next-runtime-assets.json"))
	if e != nil {
		return e
	}
	platform := stringValue(runtimeManifest, "platform")
	expected := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if platform == "windows-arm64" {
		expected = pe.IMAGE_FILE_MACHINE_ARM64
	} else if platform != "windows-x64" {
		return fmt.Errorf("unsupported Windows platform")
	}
	checkSource := func(receipt record, key, relative string) bool {
		h, e := digest(a.path(filepath.FromSlash(relative)))
		return e == nil && receipt[key] == h
	}
	worker := o.get("msi-worker")
	wr, e := load(filepath.Join(worker, "receipt.json"))
	if e != nil {
		return e
	}
	wh, e := digest(filepath.Join(worker, "msi-worker.exe"))
	if e != nil {
		return e
	}
	machine, _, e := peInfo(filepath.Join(worker, "msi-worker.exe"))
	if e != nil || machine != expected || wr["platform"] != platform || wr["version"] != build["product_version"] || wr["binary_sha256"] != wh || !checkSource(wr, "source_sha256", "native-core/launcher/windows-msi-worker.c") || !checkSource(wr, "resource_sha256", "native-core/launcher/windows-msi-worker.rc.in") {
		return fmt.Errorf("Windows Installer worker differs from owned sources/receipt")
	}
	bridge := o.get("bridge")
	br, e := load(filepath.Join(bridge, "receipt.json"))
	if e != nil {
		return e
	}
	if br["platform"] != platform || br["product_version"] != build["product_version"] || br["optimizer"] != "O2" || br["ob1_workaround"] != false || !checkSource(br, "source_sha256", "native-core/launcher/windows-bridge.c") || !checkSource(br, "resource_sha256", "native-core/launcher/windows-bridge.rc.in") || !checkSource(br, "memory_source_sha256", "native-core/launcher/freestanding-memory.c") || len(object(br, "imports")) != len(object(br, "binaries")) {
		return fmt.Errorf("owned bridge receipt predates freestanding-memory or differs from sources; rebuild")
	}
	for name, v := range object(br, "binaries") {
		p := filepath.Join(bridge, name)
		if e = shaMatches(p, rec(v)); e != nil {
			return e
		}
		machine, libraries, e := peInfo(p)
		var pinned []string
		for _, v := range array(object(br, "imports"), name) {
			pinned = append(pinned, v.(string))
		}
		if e != nil || machine != expected || !bridgeImportsMatch(name, pinned) || !bridgeImportsMatch(name, libraries) {
			return fmt.Errorf("owned bridge imports or machine differ")
		}
	}
	psl := o.get("psl")
	pr, e := load(filepath.Join(psl, "receipt.json"))
	if e != nil {
		return e
	}
	if pr["platform"] != platform {
		return fmt.Errorf("owned libpsl platform differs")
	}
	for name, v := range object(pr, "files") {
		if e = shaMatches(filepath.Join(psl, "bin", name), rec(v)); e != nil {
			return e
		}
	}
	identity, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	if build["source_tree_sha256"] != identity {
		return fmt.Errorf("core receipt differs from integrated source")
	}
	version := strings.Split(stringValue(build, "product_version"), "-")[0] + ".0"
	for name, v := range object(build, "binaries") {
		p := filepath.Join(o.get("build-dir"), name)
		if e = shaMatches(p, rec(v)); e != nil {
			return e
		}
		machine, _, e := peInfo(p)
		if e != nil || machine != expected {
			return fmt.Errorf("core architecture mismatch")
		}
		numeric, e := peNumericVersion(p)
		if e != nil || numeric != version {
			return fmt.Errorf("numeric product version differs: %s", name)
		}
	}
	for name, v := range object(runtimeManifest, "files") {
		if e = shaMatches(filepath.Join(assets, filepath.FromSlash(name)), rec(v)); e != nil {
			return e
		}
		if upstreamCores[strings.ToLower(path.Base(name))] {
			return fmt.Errorf("upstream Git core remains in runtime inputs")
		}
	}
	var crtFiles []string
	if o.get("crt-dir") != "" {
		crtFiles, e = filepath.Glob(filepath.Join(o.get("crt-dir"), "vcruntime140*.dll"))
		if e != nil {
			return e
		}
	}
	hasCRT := false
	for _, p := range crtFiles {
		hasCRT = hasCRT || strings.EqualFold(filepath.Base(p), "vcruntime140.dll")
		machine, _, e := peInfo(p)
		if e != nil || machine != expected {
			return fmt.Errorf("CRT architecture mismatch")
		}
	}
	if platform == "windows-x64" && !hasCRT {
		return fmt.Errorf("licensed Microsoft vcruntime140.dll required")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	b := distributionBuilder{root: out, target: platform, windows: true, records: record{}}
	var terminal record
	if o.get("terminal") != "" {
		root, e := absolute(o.get("terminal"))
		if e != nil {
			return e
		}
		terminal, e = load(filepath.Join(root, "git-terminal-package.json"))
		if e != nil {
			return e
		}
		if terminal["product"] != "git-terminal" || terminal["platform"] != platform || terminal["renderer"] != "gpui-fast" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(stringValue(terminal, "source_commit")) {
			return fmt.Errorf("matching source-built GPUI Fast terminal required")
		}
		for name, h := range object(terminal, "files") {
			p, e := confined(root, name)
			if e != nil {
				return e
			}
			st, e := os.Lstat(p)
			if e != nil || st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe terminal input")
			}
			if e = shaMatches(p, record{"sha256": h}); e != nil {
				return e
			}
			target := "LICENSES/git-terminal/" + name
			kind := "git-terminal-notice"
			if name == "git-terminal.exe" || name == "git-terminal-shell.exe" || name == "git-terminal-askpass.exe" || name == "libunwind.dll" || strings.HasPrefix(name, "shell-integration/") {
				target = "bin/" + name
			}
			if strings.HasSuffix(name, ".exe") {
				kind = "source-built-git-terminal"
			} else if name == "libunwind.dll" {
				kind = "pinned-git-terminal-runtime"
			}
			if e = b.copy(p, target, kind, 0644, 0); e != nil {
				return e
			}
		}
		if e = b.copy(filepath.Join(root, "git-terminal-package.json"), "LICENSES/git-terminal/git-terminal-package.json", "git-terminal-source-identity", 0644, 0); e != nil {
			return e
		}
		machine, _, e := peInfo(filepath.Join(out, "bin/git-terminal.exe"))
		if e != nil || machine != expected {
			return fmt.Errorf("terminal architecture mismatch")
		}
	}
	var defaults []any
	for _, name := range sortedKeys(object(runtimeManifest, "files")) {
		if pythonRuntimeMember(name) {
			continue
		}
		item := rec(object(runtimeManifest, "files")[name])
		if strings.EqualFold(path.Base(name), "libcurl-openssl-4.dll") || path.Base(name) == "git-update-git-for-windows" {
			continue
		}
		target := "runtime/" + name
		kind := "pinned-third-party-runtime"
		if regexp.MustCompile(`^(?:etc|usr/etc|mingw64/etc|clangarm64/etc)/`).MatchString(name) && name != "etc/gitconfig" {
			target = "runtime/defaults/" + name
			kind = "pinned-runtime-configuration-default"
			defaults = append(defaults, record{"target": "runtime/" + name, "source": target, "sha256": item["sha256"], "attributes": item["windows_attributes"]})
		}
		if e = b.copy(filepath.Join(assets, filepath.FromSlash(name)), target, kind, 0644, number(item, "windows_attributes")); e != nil {
			return e
		}
	}
	if e = b.store("runtime/defaults/manifest.json", encoded(record{"schema_version": 1, "product": "git-next", "version": build["product_version"], "platform": platform, "files": defaults}), "product-runtime-configuration-manifest", 0644, 0); e != nil {
		return e
	}
	native := "runtime/" + stringValue(runtimeManifest, "native_prefix")
	p4Receipt, e := b.addGitP4(a, o.get("git-p4"), platform, native)
	if e != nil {
		return e
	}
	if e = b.addLFSHooks(a, native); e != nil {
		return e
	}
	helper := native + "/libexec/git-core"
	shell, e := read(filepath.Join(source, "git-sh-i18n.sh"))
	if e != nil {
		return e
	}
	if !strings.Contains(shell, "GIT_INTERNAL_GETTEXT_SH_SCHEME=git-next") {
		return fmt.Errorf("native shell language integration missing")
	}
	shell = strings.ReplaceAll(strings.ReplaceAll(shell, "@LOCALEDIR@", native+"/share/locale"), "@USE_GETTEXT_SCHEME@", "")
	if e = b.store(helper+"/git-sh-i18n", []byte(shell), "product-native-shell-language", 0644, 0); e != nil {
		return e
	}
	perl, e := read(filepath.Join(source, "perl/Git/I18N.pm"))
	if e != nil {
		return e
	}
	if !strings.Contains(perl, "__native_translation") {
		return fmt.Errorf("native Perl language integration missing")
	}
	perl = strings.ReplaceAll(strings.ReplaceAll(perl, "@NO_GETTEXT@", "1"), "@LOCALEDIR@", native+"/share/locale")
	if e = b.store(native+"/share/perl5/Git/I18N.pm", []byte(perl), "product-native-perl-language", 0644, 0); e != nil {
		return e
	}
	builtins, e := builtinsAt(source)
	if e != nil {
		return e
	}
	core := filepath.Join(o.get("build-dir"), "git.exe")
	directories := []string{"bin", "cmd", "runtime/bin", "runtime/cmd", native + "/bin", helper}
	for _, dir := range directories {
		if e = b.copy(core, dir+"/git.exe", "source-built-git-next-core", 0644, 0); e != nil {
			return e
		}
	}
	if e = b.copy(core, "bin/git-next.exe", "source-built-git-next-core", 0644, 0); e != nil {
		return e
	}
	copyAlias := func(src, target, kind string) error {
		if b.records[target] != nil {
			delete(b.records, target)
		}
		return b.copy(src, target, kind, 0644, 0)
	}
	for _, name := range builtins {
		if e = copyAlias(core, helper+"/git-"+name+".exe", "source-built-git-next-builtin-alias"); e != nil {
			return e
		}
	}
	for _, name := range []string{"receive-pack", "upload-pack", "upload-archive"} {
		for _, dir := range []string{native + "/bin", helper} {
			if e = copyAlias(core, dir+"/git-"+name+".exe", "source-built-git-next-builtin-alias"); e != nil {
				return e
			}
		}
	}
	for name := range object(build, "binaries") {
		if name == "git.exe" {
			continue
		}
		for _, dir := range []string{native + "/bin", helper} {
			if e = copyAlias(filepath.Join(o.get("build-dir"), name), dir+"/"+name, "source-built-git-next-helper"); e != nil {
				return e
			}
		}
	}
	for _, protocol := range []string{"https", "ftp", "ftps"} {
		if e = copyAlias(filepath.Join(o.get("build-dir"), "git-remote-http.exe"), helper+"/git-remote-"+protocol+".exe", "source-built-git-next-transport-alias"); e != nil {
			return e
		}
	}
	dlls, e := filepath.Glob(filepath.Join(psl, "bin/*.dll"))
	if e != nil {
		return e
	}
	for _, dir := range directories {
		if e = copyAlias(filepath.Join(assets, stringValue(runtimeManifest, "native_prefix"), "bin/libiconv-2.dll"), dir+"/libiconv-2.dll", "pinned-gnu-iconv-runtime"); e != nil {
			return e
		}
		for _, p := range dlls {
			if e = copyAlias(p, dir+"/"+filepath.Base(p), "pinned-public-suffix-runtime"); e != nil {
				return e
			}
		}
		for _, p := range crtFiles {
			if e = copyAlias(p, dir+"/"+filepath.Base(p), "microsoft-vc-redistributable"); e != nil {
				return e
			}
		}
	}
	config, e := read(filepath.Join(assets, "etc/gitconfig"))
	if e != nil {
		return e
	}
	d, e := readDefaults(a.root)
	if e != nil {
		return e
	}
	anchor := regexp.MustCompile(`(?m)^\s*autocrlf\s*=\s*true\s*$`)
	if len(anchor.FindAllString(config, -1)) != 1 {
		return fmt.Errorf("vendor LF anchor differs")
	}
	config = anchor.ReplaceAllStringFunc(config, func(string) string { return d.core() }) + "\n[http]\n\tsslBackend = schannel\n" + d.tail()
	delete(b.records, "runtime/etc/gitconfig")
	if e = b.store("runtime/etc/gitconfig", []byte(config), "managed-product-lf-defaults", 0644, 0); e != nil {
		return e
	}
	if e = b.notices(a); e != nil {
		return e
	}
	if e = b.copy(a.path("native-core/vendor/yyjson/LICENSE"), "LICENSES/yyjson/LICENSE", "third-party-license", 0644, 0); e != nil {
		return e
	}
	if e = b.help(true, native); e != nil {
		return e
	}
	for _, name := range []string{"update.ps1", "Update.Core.psm1", "Scope.Core.ps1", "Scope.Native.cs", "Runtime.Configuration.ps1", "messages.zh-CN.json"} {
		if e = b.copy(a.path("packaging/windows", name), "updater/"+name, "product-updater", 0644, 0); e != nil {
			return e
		}
	}
	for _, pair := range [][2]string{{"packaging/windows/install-tls-provider.ps1", "components/install-tls-provider.ps1"}, {"packaging/tls-providers.lock.json", "components/tls-providers.lock.json"}, {"packaging/windows/takeover.ps1", "migration/takeover.ps1"}, {"packaging/windows/Takeover.Core.psm1", "migration/Takeover.Core.psm1"}} {
		if e = b.copy(a.path(filepath.FromSlash(pair[0])), pair[1], "product-migration", 0644, 0); e != nil {
			return e
		}
	}
	for name := range object(br, "binaries") {
		if e = b.copy(filepath.Join(bridge, name), "migration/"+name, "source-built-git-next-migration-bridge", 0644, 0); e != nil {
			return e
		}
	}
	if e = b.copy(filepath.Join(worker, "msi-worker.exe"), "updater/msi-worker.exe", "source-built-no-console-msi-worker", 0644, 0); e != nil {
		return e
	}
	if e = b.copy(filepath.Join(worker, "receipt.json"), "LICENSES/git-next-msi-worker/source.json", "msi-worker-source-identity", 0644, 0); e != nil {
		return e
	}
	system := map[string]bool{}
	systemPaths, _ := filepath.Glob(filepath.Join(os.Getenv("SystemRoot"), "System32/*.dll"))
	for _, p := range systemPaths {
		system[strings.ToLower(filepath.Base(p))] = true
	}
	seen := map[string]bool{}
	var inspect func(string) error
	inspect = func(p string) error {
		if seen[p] {
			return nil
		}
		seen[p] = true
		machine, names, e := peInfo(p)
		if e != nil {
			return e
		}
		if machine != expected {
			return fmt.Errorf("native dependency architecture mismatch: %s", p)
		}
		for _, name := range names {
			if system[name] || strings.HasPrefix(name, "api-ms-win-") || strings.HasPrefix(name, "ext-ms-win-") {
				continue
			}
			sibling := filepath.Join(filepath.Dir(p), name)
			if e = inspect(sibling); e != nil {
				return e
			}
		}
		return nil
	}
	var aliases []string
	for name, v := range b.records {
		item := rec(v)
		kind := stringValue(item, "kind")
		if kind == "source-built-git-next-core" {
			aliases = append(aliases, name)
		}
		if strings.HasPrefix(kind, "source-built-git-next") || kind == "source-built-git-terminal" || kind == "source-built-git-p4" {
			if e = inspect(filepath.Join(out, filepath.FromSlash(name))); e != nil {
				return e
			}
		}
		if e = shaMatches(filepath.Join(out, filepath.FromSlash(name)), item); e != nil {
			return e
		}
	}
	sort.Strings(aliases)
	identity, e = sourceIdentity(source)
	if e != nil || identity != build["source_tree_sha256"] {
		return fmt.Errorf("core source changed during assembly")
	}
	report := record{"schema_version": 1, "status": "distribution-assembled-not-runtime-accepted", "product": "git-next", "version": build["product_version"], "platform": platform, "engine": "source-built-git-next-native-core", "source_tree_sha256": build["source_tree_sha256"], "upstream_runtime_archive_sha256": runtimeManifest["archive_sha256"], "files": b.records, "core_entrypoints": aliases, "upstream_git_core_deployed": false, "builtins": builtins, "native_import_images_inspected": len(seen), "runtime_configuration_defaults": len(defaults), "initialize_runtime_configuration_before_execution": true, "product_executed": false, "tests_executed": false, "installed": false, "complete_git_parity": compat["complete_git_parity"], "release_eligible": false, "remaining": []string{"D08 consolidated acceptance", "D09 paired performance acceptance", "D10 single release and workstation takeover"}}
	report["git_p4"], report["python_required"] = p4Receipt, false
	if terminal != nil {
		identity := record{}
		for _, key := range []string{"product", "version", "source_commit", "platform", "renderer"} {
			identity[key] = terminal[key]
		}
		report["git_terminal"] = identity
	}
	if e = writeNewJSON(filepath.Join(out, "git-next-distribution.json"), report); e != nil {
		return e
	}
	result := record{}
	for k, v := range report {
		if k != "files" {
			result[k] = v
		}
	}
	return a.json(result)
}
