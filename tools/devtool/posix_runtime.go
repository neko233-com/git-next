// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func init() {
	register("collect-linux-runtime", "Collect bounded Linux runtime and its static ELF closure", "platform destination core-stage tls-prefix primary-helpers base-image", collectLinuxRuntime)
	register("collect-macos-runtime", "Collect declared Apple Silicon runtime and native dependency closure", "destination core-stage primary-helpers", collectMacRuntime)
}

type runtimeCollector struct {
	root, platform               string
	inventory, origins, mappings record
	pending                      [][2]string
	total                        int64
	mac                          bool
}

func (c *runtimeCollector) copy(source, name, component string, mode uint32) error {
	actual, e := filepath.EvalSymlinks(source)
	if e != nil {
		return e
	}
	actual, e = absolute(actual)
	if e != nil {
		return e
	}
	st, e := os.Stat(actual)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("runtime producer is not regular file: %s", source)
	}
	h, e := digest(actual)
	if e != nil {
		return e
	}
	if item := rec(c.inventory[name]); item != nil {
		expected := item["sha256"]
		if c.mac {
			expected = rec(c.origins[name])["sha256"]
		}
		if expected != h {
			return fmt.Errorf("conflicting runtime asset: %s", name)
		}
		return nil
	}
	p, e := confined(c.root, name)
	if e != nil {
		return e
	}
	c.total += st.Size()
	limit := int64(1024 << 20)
	count := 30000
	if c.mac {
		limit = 1536 << 20
		count = 40000
	}
	if c.total > limit || len(c.inventory) >= count {
		return fmt.Errorf("runtime SDK bounds exceeded")
	}
	if e = copyFile(actual, p); e != nil {
		return e
	}
	if c.mac {
		if component == "producer-source-recipe" && filepath.Base(actual) == "INSTALL_RECEIPT.json" {
			value, e := load(p)
			if e != nil {
				return e
			}
			if src := object(value, "source"); src != nil && src["path"] != nil {
				src["path"] = "producer/Homebrew/" + filepath.Base(filepath.Dir(filepath.Dir(actual))) + "/" + filepath.Base(filepath.Dir(actual))
				if e = writeJSON(p, value); e != nil {
					return e
				}
			}
		}
		if e = thinMacho(p); e != nil {
			return e
		}
	}
	machine, e := imagePlatform(p)
	if e != nil {
		return e
	}
	if machine != "" {
		if machine != c.platform {
			return fmt.Errorf("foreign runtime image: %s", name)
		}
		c.pending = append(c.pending, [2]string{actual, p})
	}
	if mode == 0 {
		mode = 0644
		if st.Mode()&0111 != 0 {
			mode = 0755
		}
	}
	if e = os.Chmod(p, os.FileMode(mode)); e != nil {
		return e
	}
	outHash, e := digest(p)
	if e != nil {
		return e
	}
	outStat, e := os.Stat(p)
	if e != nil {
		return e
	}
	c.inventory[name] = record{"kind": "file", "sha256": outHash, "bytes": outStat.Size(), "mode": mode, "component": component}
	if c.mac {
		origin := actual
		if home, e := os.UserHomeDir(); e == nil && inside(home, actual) {
			relative, _ := filepath.Rel(home, actual)
			origin = "producer-home/" + filepath.ToSlash(relative)
		}
		c.origins[name] = record{"source": origin, "sha256": h}
	} else {
		c.origins[name] = source
	}
	return nil
}
func (c *runtimeCollector) link(name, target, component string) error {
	if _, e := confined(c.root, name); e != nil {
		return e
	}
	if c.inventory[name] != nil {
		return fmt.Errorf("duplicate runtime link")
	}
	if path.IsAbs(target) || strings.ContainsAny(target, "\\:") {
		return fmt.Errorf("runtime link must remain relative")
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if _, e := confined(c.root, resolved); e != nil {
		return e
	}
	c.inventory[name] = record{"kind": "symlink", "target": target, "component": component}
	return nil
}
func (c *runtimeCollector) tree(source, target, component string) error {
	return filepath.WalkDir(source, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" || d.Name() == ".github" || c.mac && d.Name() == "site-packages" {
				return filepath.SkipDir
			}
			return nil
		}
		if pythonRuntimeMember(filepath.ToSlash(p)) {
			return nil
		}
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		relative, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		return c.copy(p, target+"/"+filepath.ToSlash(relative), component, 0)
	})
}
func (c *runtimeCollector) helpers(root string, manifest record) error {
	for _, name := range sortedKeys(object(manifest, "files")) {
		item := rec(object(manifest, "files")[name])
		if item["kind"] == "file" {
			p, e := verifiedFile(root, name, item)
			if e != nil {
				return e
			}
			if e = c.copy(p, name, "primary-"+stringValue(item, "component"), uint32(number(item, "mode"))); e != nil {
				return e
			}
		} else if item["kind"] == "symlink" {
			if e := c.link(name, stringValue(item, "target"), "primary-"+stringValue(item, "component")); e != nil {
				return e
			}
		} else {
			return fmt.Errorf("unknown primary helper member")
		}
	}
	return nil
}
func (c *runtimeCollector) addCore(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		machine, e := imagePlatform(p)
		if e != nil {
			return e
		}
		if machine != "" {
			c.pending = append(c.pending, [2]string{p, p})
		}
		return nil
	})
}
func collectLinuxRuntime(a *app, o options) error {
	if e := o.require("platform", "destination", "core-stage", "tls-prefix", "primary-helpers", "base-image"); e != nil {
		return e
	}
	platform := o.get("platform")
	if hostPlatform() != platform || !strings.HasPrefix(platform, "linux-") {
		return fmt.Errorf("collect on declared Linux producer architecture")
	}
	if !regexp.MustCompile(`^debian@sha256:[a-f0-9]{64}$`).MatchString(o.get("base-image")) {
		return fmt.Errorf("immutable official Debian producer digest required")
	}
	helpers, e := load(filepath.Join(o.get("primary-helpers"), "git-next-posix-helpers.json"))
	if e != nil {
		return e
	}
	if helpers["platform"] != platform {
		return fmt.Errorf("primary helper architecture differs")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	c := runtimeCollector{root: out, platform: platform, inventory: record{}, origins: record{}}
	programs := map[string]string{"bash": "/bin/bash", "ssh": "/usr/bin/ssh", "ssh-add": "/usr/bin/ssh-add", "ssh-agent": "/usr/bin/ssh-agent", "ssh-keygen": "/usr/bin/ssh-keygen", "scp": "/usr/bin/scp", "sftp": "/usr/bin/sftp", "gpg": "/usr/bin/gpg", "gpgv": "/usr/bin/gpgv", "gpgsm": "/usr/bin/gpgsm", "gpg-agent": "/usr/bin/gpg-agent", "gpgconf": "/usr/bin/gpgconf", "dirmngr": "/usr/bin/dirmngr", "pinentry-curses": "/usr/bin/pinentry-curses", "perl": "/usr/bin/perl", "tclsh": "/usr/bin/tclsh", "wish": "/usr/bin/wish"}
	for _, name := range strings.Fields("sed grep cut dirname basename find sort uniq head tail tr cat wc ls cp mv rm mkdir rmdir mktemp env xargs readlink realpath touch chmod date expr uname id whoami tee install du od sleep timeout yes diff patch tar gzip less file awk") {
		p, e := exec.LookPath(name)
		if e != nil {
			return e
		}
		programs[name] = p
	}
	for name, p := range programs {
		if e = c.copy(p, "usr/bin/"+name, "debian-native-runtime", 0755); e != nil {
			return e
		}
	}
	if e = c.link("usr/bin/sh", "bash", "owned-command-shell"); e != nil {
		return e
	}
	if e = c.link("usr/bin/pinentry", "pinentry-curses", "native-signing"); e != nil {
		return e
	}
	entries, e := os.ReadDir("/usr/lib/gnupg")
	if e != nil {
		return e
	}
	for _, v := range entries {
		if !v.IsDir() {
			if e = c.copy(filepath.Join("/usr/lib/gnupg", v.Name()), "usr/lib/gnupg/"+v.Name(), "debian-native-signing", 0); e != nil {
				return e
			}
		}
	}
	roots := []string{"/usr/share/perl5", "/usr/share/perl", "/usr/share/tcltk", "/usr/share/terminfo", "/lib/terminfo"}
	triplets, _ := filepath.Glob("/usr/lib/*-linux-gnu")
	for _, triplet := range triplets {
		for _, name := range []string{"perl", "perl5"} {
			if exists(filepath.Join(triplet, name)) {
				roots = append(roots, filepath.Join(triplet, name))
			}
		}
	}
	for _, root := range roots {
		if !exists(root) {
			continue
		}
		target := strings.TrimPrefix(filepath.ToSlash(root), "/")
		if e = c.tree(root, target, "debian-interpreter-data"); e != nil {
			return e
		}
	}
	if e = c.helpers(o.get("primary-helpers"), helpers); e != nil {
		return e
	}
	raw, e := a.mustRun("ldconfig", "-p")
	if e != nil {
		return e
	}
	libraries := map[string]string{}
	for _, v := range regexp.MustCompile(`(?m)^\s*(\S+)\s+\([^)]*\)\s+=>\s+(/\S+)`).FindAllStringSubmatch(string(raw), -1) {
		if libraries[v[1]] == "" {
			libraries[v[1]] = v[2]
		}
	}
	tls := o.get("tls-prefix")
	tlsLibs, _ := filepath.Glob(filepath.Join(tls, "lib/*.so*"))
	for _, p := range tlsLibs {
		libraries[filepath.Base(p)] = p
	}
	for _, name := range []string{"libssl.so.3", "libcrypto.so.3", "libicuuc.so.72", "libicui18n.so.72", "libicudata.so.72", "libsecret-1.so.0", "libkrb5.so.3", "libgssapi_krb5.so.2"} {
		if libraries[name] == "" {
			return fmt.Errorf("managed/provider runtime library missing: %s", name)
		}
		if e = c.copy(libraries[name], "lib/"+name, "native-runtime-library", 0); e != nil {
			return e
		}
	}
	if e = c.addCore(o.get("core-stage")); e != nil {
		return e
	}
	seen := map[string]bool{}
	for i := 0; i < len(c.pending); i++ {
		source := c.pending[i][0]
		actual, e := filepath.EvalSymlinks(source)
		if e != nil {
			return e
		}
		if seen[actual] {
			continue
		}
		seen[actual] = true
		names, e := elfNeeded(source)
		if e != nil {
			return e
		}
		for _, name := range names {
			if elfSystemLibraries[name] {
				continue
			}
			if strings.Contains(name, "/") || libraries[name] == "" {
				return fmt.Errorf("missing explicit ELF dependency: %s", name)
			}
			if e = c.copy(libraries[name], "lib/"+name, "native-runtime-library", 0); e != nil {
				return e
			}
		}
	}
	raw, e = a.mustRun("dpkg-query", "-W", "-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n")
	if e != nil {
		return e
	}
	packages := strings.Split(strings.TrimSpace(string(raw)), "\n")
	queries := map[string]bool{}
	for _, v := range c.origins {
		origin, _ := v.(string)
		if !strings.HasPrefix(origin, "/usr/") && !strings.HasPrefix(origin, "/bin/") && !strings.HasPrefix(origin, "/lib/") && !strings.HasPrefix(origin, "/sbin/") {
			continue
		}
		queries[origin] = true
		if strings.HasPrefix(origin, "/usr/") {
			queries[strings.TrimPrefix(origin, "/usr")] = true
		} else {
			queries["/usr"+origin] = true
		}
	}
	args := []string{"-S"}
	for q := range queries {
		args = append(args, q)
	}
	sort.Strings(args[1:])
	ownership, e := a.run("dpkg-query", args...)
	if e != nil {
		return e
	}
	owners := map[string]bool{}
	for _, line := range strings.Split(string(ownership.stdout), "\n") {
		name, _, ok := strings.Cut(line, ": ")
		if ok && regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(?::(?:amd64|arm64|all))?$`).MatchString(name) {
			owners[strings.Split(name, ":")[0]] = true
		}
	}
	for name := range owners {
		p := filepath.Join("/usr/share/doc", name, "copyright")
		if exists(p) {
			if e = c.copy(p, "share/licenses/debian/"+name+".copyright", "producer-notice", 0644); e != nil {
				return e
			}
		}
	}
	notices, _ := filepath.Glob(filepath.Join(tls, "licenses/*"))
	for _, p := range notices {
		if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() {
			if e = c.copy(p, "share/licenses/native-tls/"+filepath.Base(p), "native-tls-producer-notice", 0644); e != nil {
				return e
			}
		}
	}
	launcher := []byte("#!/bin/sh\nset -eu\nself=$0\ncase \"$self\" in */*) ;; *) self=$(command -v \"$self\");; esac\nbase=${self%/*}\nruntime=$(CDPATH= cd -- \"$base/../..\" && pwd -P)\nLD_LIBRARY_PATH=\"$runtime/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}\"\nexport LD_LIBRARY_PATH\nexec \"$runtime/usr/lib/git-next/gcm/git-credential-manager\" \"$@\"\n")
	if e = write(filepath.Join(out, "usr/bin/git-credential-manager"), launcher, 0755); e != nil {
		return e
	}
	c.inventory["usr/bin/git-credential-manager"] = record{"kind": "file", "sha256": digestBytes(launcher), "bytes": len(launcher), "mode": 0755, "component": "owned-managed-helper-launcher"}
	var system []string
	for name := range elfSystemLibraries {
		system = append(system, name)
	}
	sort.Strings(system)
	receipt := record{"schema_version": 1, "status": "linux-runtime-collected-not-runtime-accepted", "platform": platform, "producer": record{"base_image": o.get("base-image"), "packages": packages, "source_file_ownership": splitLines(ownership.stdout), "unresolved_file_ownership": splitLines(ownership.stderr)}, "components": helpers["components"], "files": c.inventory, "source_paths": c.origins, "operating_system_libraries": system, "platform_requirements": []string{"glibc 2.36 or newer", "normal POSIX kernel/loader and system services", "existing SSH/GnuPG agents and system configuration keep their OS paths"}, "elf_images_inspected": len(seen), "physical_symlinks_created": false, "corresponding_sources": "required-before-release", "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false}
	if e = writeNewJSON(filepath.Join(out, "git-next-posix-runtime.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"platform": platform, "files": len(c.inventory), "native_images": len(seen), "bytes": c.total, "product_executed": false})
}
func splitLines(b []byte) []string {
	s := strings.TrimSuffix(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}
func collectMacRuntime(a *app, o options) error {
	if e := o.require("destination", "core-stage", "primary-helpers"); e != nil {
		return e
	}
	if hostPlatform() != "macos-arm64" {
		return fmt.Errorf("collect on native Apple Silicon producer")
	}
	helpers, e := load(filepath.Join(o.get("primary-helpers"), "git-next-posix-helpers.json"))
	if e != nil {
		return e
	}
	if helpers["platform"] != "macos-arm64" {
		return fmt.Errorf("wrong primary helper architecture")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	c := runtimeCollector{root: out, platform: "macos-arm64", inventory: record{}, origins: record{}, mappings: record{}, mac: true}
	formulae := []string{"bash", "tcl-tk@8", "gnupg"}
	prefixes := map[string]string{}
	for _, name := range formulae {
		b, e := a.mustRun("brew", "--prefix", name)
		if e != nil {
			return e
		}
		p, e := filepath.EvalSymlinks(strings.TrimSpace(string(b)))
		if e != nil {
			return e
		}
		prefixes[name] = p
	}
	programs := map[string]string{}
	for _, name := range []string{"ssh", "ssh-add", "ssh-agent", "ssh-keygen", "scp", "sftp", "perl"} {
		programs[name] = "/usr/bin/" + name
	}
	programs["bash"] = filepath.Join(prefixes["bash"], "bin/bash")
	for _, name := range []string{"tclsh", "wish"} {
		matches, _ := filepath.Glob(filepath.Join(prefixes["tcl-tk@8"], "bin", name+"*"))
		if len(matches) == 0 {
			return fmt.Errorf("Tcl/Tk producer program missing")
		}
		programs[name] = matches[0]
	}
	for _, name := range []string{"gpg", "gpgv", "gpgsm", "gpg-agent", "gpgconf", "dirmngr"} {
		programs[name] = filepath.Join(prefixes["gnupg"], "bin", name)
	}
	for name, p := range programs {
		if e = c.copy(p, "usr/bin/"+name, "declared-native-producer", 0755); e != nil {
			return e
		}
	}
	if e = c.link("usr/bin/sh", "bash", "owned-command-shell"); e != nil {
		return e
	}
	signing, _ := filepath.Glob(filepath.Join(prefixes["gnupg"], "libexec/*"))
	for _, p := range signing {
		if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() {
			if e = c.copy(p, "usr/lib/gnupg/"+filepath.Base(p), "native-signing", 0); e != nil {
				return e
			}
		}
	}
	if p, e := exec.LookPath("pinentry"); e == nil {
		if e = c.copy(p, "usr/bin/pinentry", "native-signing", 0755); e != nil {
			return e
		}
	}
	if e = c.tree("/System/Library/Perl", "usr/lib/perl", "apple-perl-standard-library"); e != nil {
		return e
	}
	for _, pattern := range []string{"tcl8.*", "tk8.*"} {
		matches, _ := filepath.Glob(filepath.Join(prefixes["tcl-tk@8"], "lib", pattern))
		var roots []string
		for _, p := range matches {
			if st, e := os.Stat(p); e == nil && st.IsDir() {
				roots = append(roots, p)
			}
		}
		if len(roots) != 1 {
			return fmt.Errorf("ambiguous Tcl/Tk standard library")
		}
		if e = c.tree(roots[0], "usr/lib/"+filepath.Base(roots[0]), "tcl-tk-standard-library"); e != nil {
			return e
		}
	}
	if e = c.helpers(o.get("primary-helpers"), helpers); e != nil {
		return e
	}
	if e = c.addCore(o.get("core-stage")); e != nil {
		return e
	}
	seen, system := map[string]bool{}, map[string]bool{}
	for i := 0; i < len(c.pending); i++ {
		source, image := c.pending[i][0], c.pending[i][1]
		if seen[source] {
			continue
		}
		seen[source] = true
		rpaths, e := machoRpaths(image)
		if e != nil {
			return e
		}
		needed, e := machoNeeded(image)
		if e != nil {
			return e
		}
		for _, name := range needed {
			if machoSystem(name) {
				system[name] = true
				continue
			}
			candidate := ""
			if path.IsAbs(name) {
				candidate = name
			} else if strings.HasPrefix(name, "@loader_path/") || strings.HasPrefix(name, "@executable_path/") {
				_, suffix, _ := strings.Cut(name, "/")
				candidate = filepath.Join(filepath.Dir(source), suffix)
			} else if strings.HasPrefix(name, "@rpath/") {
				for _, prefix := range rpaths {
					prefix = strings.ReplaceAll(strings.ReplaceAll(prefix, "@loader_path", filepath.Dir(source)), "@executable_path", filepath.Dir(source))
					trial := filepath.Join(prefix, strings.TrimPrefix(name, "@rpath/"))
					if exists(trial) {
						candidate = trial
						break
					}
				}
			}
			if candidate == "" || !exists(candidate) {
				return fmt.Errorf("unresolved native producer dependency: %s", name)
			}
			candidate, e = filepath.EvalSymlinks(candidate)
			if e != nil {
				return e
			}
			library := "lib/" + filepath.Base(candidate)
			if e = c.copy(candidate, library, "native-runtime-library", 0); e != nil {
				return e
			}
			if prev := c.mappings[name]; prev != nil && prev != library {
				return fmt.Errorf("ambiguous native dependency mapping")
			}
			c.mappings[name] = library
		}
	}
	noticeRoots := map[string]bool{}
	for _, prefix := range prefixes {
		noticeRoots[prefix] = true
	}
	for _, v := range c.origins {
		source := stringValue(rec(v), "source")
		parts := strings.Split(filepath.ToSlash(source), "/")
		for i, part := range parts {
			if part == "Cellar" && len(parts) > i+2 {
				noticeRoots[strings.Join(parts[:i+3], "/")] = true
				break
			}
		}
	}
	for root := range noticeRoots {
		recipes := []string{filepath.Join(root, "INSTALL_RECEIPT.json")}
		rb, _ := filepath.Glob(filepath.Join(root, ".brew/*.rb"))
		recipes = append(recipes, rb...)
		for _, p := range recipes {
			if exists(p) {
				if e = c.copy(p, "share/source-recipes/homebrew/"+filepath.Base(filepath.Dir(root))+"/"+filepath.Base(root)+"/"+filepath.Base(p), "producer-source-recipe", 0644); e != nil {
					return e
				}
			}
		}
		e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			lower := strings.ToLower(d.Name())
			if strings.HasPrefix(lower, "copying") || strings.HasPrefix(lower, "license") || strings.HasPrefix(lower, "notice") || strings.HasPrefix(lower, "authors") {
				relative, _ := filepath.Rel(root, p)
				return c.copy(p, "share/licenses/homebrew/"+filepath.Base(filepath.Dir(root))+"/"+filepath.Base(root)+"/"+filepath.ToSlash(relative), "producer-notice", 0644)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	raw, e := a.mustRun("brew", append([]string{"info", "--json=v2"}, formulae...)...)
	if e != nil {
		return e
	}
	var info any
	if e = json.Unmarshal(raw, &info); e != nil {
		return e
	}
	raw, e = a.mustRun("/usr/bin/sw_vers", "-productVersion")
	if e != nil {
		return e
	}
	var systemNames []string
	for name := range system {
		systemNames = append(systemNames, name)
	}
	sort.Strings(systemNames)
	receipt := record{"schema_version": 1, "status": "macos-runtime-collected-not-runtime-accepted", "platform": "macos-arm64", "producer": record{"os_version": strings.TrimSpace(string(raw)), "formulae": info}, "files": c.inventory, "source_paths": c.origins, "macho_library_map": c.mappings, "components": helpers["components"], "operating_system_libraries": systemNames, "platform_requirements": []string{"macOS 15.5 or newer on Apple Silicon", "Apple system frameworks, POSIX utilities and OS-managed SSH/Keychain services", "existing SSH/GnuPG profiles and agents retain their OS paths"}, "native_images_inspected": len(seen), "physical_symlinks_created": false, "corresponding_sources": "required-before-release", "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false}
	if e = writeNewJSON(filepath.Join(out, "git-next-posix-runtime.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "files": len(c.inventory), "native_images": len(seen), "product_executed": false})
}
