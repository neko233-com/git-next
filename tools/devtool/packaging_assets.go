// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

func init() {
	register("prepare-corresponding-sources", "Fetch pinned opaque sources and package source groups", "lock cache output workers", prepareCorrespondingSources)
	register("prepare-git-shared-assets", "Stage pinned portable Git help and GUI data", "windows-runtime-assets cache destination", prepareSharedAssets)
	register("prepare-git-terminal", "Build committed peer terminal and collect dependency notices", "source destination platform cargo git llvm-mingw", prepareGitTerminal)
}
func encoded(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func sortedKeys(m record) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func rec(v any) record { r, _ := v.(map[string]any); return r }
func shaMatches(p string, item record) error {
	h, e := digest(p)
	if e != nil {
		return e
	}
	info, e := os.Stat(p)
	if e != nil {
		return e
	}
	if h != stringValue(item, "sha256") || (item["bytes"] != nil && info.Size() != number(item, "bytes")) {
		return fmt.Errorf("input differs from receipt: %s", p)
	}
	return nil
}
func verifiedFile(root, name string, item record) (string, error) {
	p, e := confined(root, name)
	if e != nil {
		return "", e
	}
	actual, e := filepath.EvalSymlinks(p)
	if e != nil || !inside(root, actual) {
		return "", fmt.Errorf("input escapes producer: %s", name)
	}
	return p, shaMatches(p, item)
}
func prepareCorrespondingSources(a *app, o options) error {
	if e := o.require("cache", "output"); e != nil {
		return e
	}
	lockPath := o.value("lock", a.path("packaging/corresponding-sources.lock.json"))
	lock, e := load(lockPath)
	if e != nil {
		return e
	}
	if number(lock, "schema_version") != 1 || stringValue(lock, "product") != "git-next" {
		return fmt.Errorf("unsupported source lock")
	}
	workers, e := o.integer("workers", 8)
	if e != nil || workers < 1 || workers > 16 {
		return fmt.Errorf("workers must be 1..16")
	}
	cache, e := absolute(o.get("cache"))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(cache, 0755); e != nil {
		return e
	}
	out, e := newDestination(o.get("output"))
	if e != nil {
		return e
	}
	sources := array(lock, "sources")
	seen := map[string]bool{}
	groups := map[string][]any{}
	for _, v := range sources {
		item := rec(v)
		name := stringValue(item, "file")
		if name != filepathBase(name) || strings.ContainsAny(name, "\\:") || seen[name] || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(stringValue(item, "sha256")) || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(stringValue(item, "group")) {
			return fmt.Errorf("duplicate or unsafe source entry")
		}
		seen[name] = true
		groups[stringValue(item, "group")] = append(groups[stringValue(item, "group")], item)
	}
	jobs := make(chan record)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failure error
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				p := filepath.Join(cache, stringValue(item, "file"))
				var err error
				if exists(p) {
					err = shaMatches(p, item)
				} else {
					for attempt := 0; attempt < 3; attempt++ {
						_, err = downloadVariable(stringValue(item, "url"), stringValue(item, "sha256"), p, number(item, "bytes"))
						if err == nil {
							err = shaMatches(p, item)
							break
						}
						if attempt < 2 {
							time.Sleep(time.Duration(attempt+1) * time.Second)
						}
					}
				}
				if err != nil {
					mu.Lock()
					if failure == nil {
						failure = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, v := range sources {
		jobs <- rec(v)
	}
	close(jobs)
	wg.Wait()
	if failure != nil {
		return failure
	}
	var bundles []any
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		name := "git-next-corresponding-sources-" + g + ".zip"
		p := filepath.Join(out, name)
		f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return e
		}
		z := zip.NewWriter(f)
		writeEntry := func(name string, b []byte) error {
			w, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if e == nil {
				_, e = w.Write(b)
			}
			return e
		}
		e = writeEntry("SOURCES.json", encoded(groups[g]))
		if e == nil {
			e = writeEntry("README.txt", []byte("Original source archives and producer recipes are opaque, unmodified files.\nGit-next patches, overlay, build scripts and locks are in the matching product source release.\nNo GitHub workflow is extracted, enabled or executed by this tool.\n"))
		}
		for _, v := range groups[g] {
			if e != nil {
				break
			}
			item := rec(v)
			r, err := os.Open(filepath.Join(cache, stringValue(item, "file")))
			if err != nil {
				e = err
				break
			}
			w, err := z.CreateHeader(&zip.FileHeader{Name: "archives/" + stringValue(item, "file"), Method: zip.Store})
			if err == nil {
				_, err = io.Copy(w, r)
			}
			r.Close()
			e = err
		}
		ce := z.Close()
		fe := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if fe != nil {
			return fe
		}
		h, _ := digest(p)
		st, _ := os.Stat(p)
		bundles = append(bundles, record{"name": name, "bytes": st.Size(), "sha256": h, "components": len(groups[g])})
	}
	h, _ := digest(lockPath)
	receipt := record{"schema_version": 1, "status": "source-materials-packaged", "lock_sha256": h, "components": len(sources), "bundles": bundles, "upstream_archives_extracted": false}
	if e = writeNewJSON(filepath.Join(out, "sources.json"), receipt); e != nil {
		return e
	}
	return a.json(receipt)
}
func prepareSharedAssets(a *app, o options) error {
	if e := o.require("windows-runtime-assets", "cache", "destination"); e != nil {
		return e
	}
	lock, e := load(a.path("packaging/git-shared-runtime.lock.json"))
	if e != nil {
		return e
	}
	source, e := absolute(o.get("windows-runtime-assets"))
	if e != nil {
		return e
	}
	runtime, e := load(filepath.Join(source, "git-next-runtime-assets.json"))
	if e != nil {
		return e
	}
	if runtime["archive_sha256"] != object(lock, "portable_ui_and_html")["archive_sha256"] {
		return fmt.Errorf("portable producer differs from pin")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	inventory := record{}
	var total int64
	store := func(name string, data []byte, mode int, origin string) error {
		p, e := confined(out, name)
		if e != nil {
			return e
		}
		if inventory[name] != nil {
			return fmt.Errorf("duplicate shared asset")
		}
		total += int64(len(data))
		if total > 256<<20 || len(inventory) >= 20000 {
			return fmt.Errorf("shared asset bounds exceeded")
		}
		if len(data) >= 4 && (string(data[:2]) == "MZ" || string(data[:4]) == "\x7fELF" || string(data[:4]) == "\xcf\xfa\xed\xfe") {
			return fmt.Errorf("native executable in portable assets")
		}
		if e = write(p, data, os.FileMode(mode)); e != nil {
			return e
		}
		inventory[name] = record{"kind": "file", "sha256": digestBytes(data), "bytes": len(data), "mode": mode, "component": origin}
		return nil
	}
	native := stringValue(runtime, "native_prefix")
	exact := map[string]string{native + "/libexec/git-core/git-gui": "usr/libexec/git-core/git-gui", native + "/libexec/git-core/git-gui.tcl": "usr/libexec/git-core/git-gui.tcl", native + "/bin/gitk": "usr/bin/gitk"}
	prefixes := map[string]string{native + "/share/git-gui/": "usr/share/git-gui/", native + "/share/doc/git-doc/": "usr/share/doc/git-doc/", native + "/share/locale/": "usr/share/locale/"}
	for _, name := range sortedKeys(object(runtime, "files")) {
		target := exact[name]
		mode := 0644
		if target != "" {
			mode = 0755
		} else {
			for prefix, v := range prefixes {
				if strings.HasPrefix(name, prefix) {
					target = v + strings.TrimPrefix(name, prefix)
					break
				}
			}
		}
		if target == "" {
			continue
		}
		p, e := verifiedFile(source, name, rec(object(runtime, "files")[name]))
		if e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if e = store(target, b, mode, "pinned-portable-git-data"); e != nil {
			return e
		}
	}
	pin := object(lock, "manuals")
	archive := filepath.Join(o.get("cache"), stringValue(pin, "asset"))
	if !exists(archive) {
		if _, e = downloadVariable(stringValue(pin, "url"), stringValue(pin, "sha256"), archive, number(pin, "bytes")); e != nil {
			return e
		}
	}
	if e = shaMatches(archive, pin); e != nil {
		return e
	}
	e = eachArchive(archive, func(m archiveMember, r io.Reader) error {
		m.name = path.Clean(m.name)
		if _, e := confined(out, m.name); e != nil {
			return e
		}
		root := strings.Split(m.name, "/")[0]
		if m.directory || root != "man1" && root != "man5" && root != "man7" {
			return nil
		}
		if m.symlink || m.hardlink {
			return fmt.Errorf("unexpected manual link")
		}
		if m.size > 256<<20 {
			return fmt.Errorf("oversized manual")
		}
		b, e := io.ReadAll(io.LimitReader(r, (256<<20)+1))
		if e != nil {
			return e
		}
		return store("usr/share/man/"+m.name, b, 0644, "pinned-git-manual")
	})
	if e != nil {
		return e
	}
	for _, name := range []string{"usr/libexec/git-core/git-gui", "usr/libexec/git-core/git-gui.tcl", "usr/bin/gitk", "usr/share/man/man1/git.1", "usr/share/doc/git-doc/git.html"} {
		if inventory[name] == nil {
			return fmt.Errorf("required shared asset missing: %s", name)
		}
	}
	h, _ := digest(filepath.Join(source, "git-next-runtime-assets.json"))
	receipt := record{"schema_version": 1, "status": "shared-git-assets-staged-not-runtime-accepted", "version": lock["version"], "files": inventory, "source": lock, "producer_receipt_sha256": h, "platform_requirements": []string{"Tcl/Tk 8.6 or newer for Git GUI/gitk", "normal platform manual/browser viewers"}, "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false}
	if e = writeNewJSON(filepath.Join(out, "git-next-shared-runtime.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"files": len(inventory), "bytes": total, "product_executed": false, "tests_executed": false})
}
func commandAt(cwd string, environment []string, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	r, e := runProcess(ctx, cwd, environment, nil, program, args...)
	if e != nil {
		return nil, e
	}
	if r.code != 0 {
		return nil, fmt.Errorf("%s exited %d: %s", filepathBase(program), r.code, strings.TrimSpace(string(r.stderr)))
	}
	return r.stdout, nil
}
func prepareGitTerminal(a *app, o options) error {
	if e := o.require("source", "destination", "platform", "cargo", "git"); e != nil {
		return e
	}
	platform := o.get("platform")
	target := map[string]string{"windows-x64": "x86_64-pc-windows-msvc", "windows-arm64": "aarch64-pc-windows-gnullvm"}[platform]
	if target == "" {
		return fmt.Errorf("unsupported terminal platform")
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	if exists(o.get("destination")) {
		return fmt.Errorf("terminal destination must be new")
	}
	git, e := absolute(o.get("git"))
	if e != nil {
		return e
	}
	cargo, e := absolute(o.get("cargo"))
	if e != nil {
		return e
	}
	commit, e := commandAt(source, nil, git, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	dirty, e := commandAt(source, nil, git, "status", "--porcelain", "--untracked-files=no")
	if e != nil {
		return e
	}
	if len(strings.TrimSpace(string(dirty))) != 0 {
		return fmt.Errorf("commit complete terminal before packaging")
	}
	manifest, e := read(filepath.Join(source, "Cargo.toml"))
	if e != nil {
		return e
	}
	field := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(manifest)
		if len(m) > 1 {
			return m[1]
		}
		return ""
	}
	if field("name") != "git-terminal" {
		return fmt.Errorf("independent Git-Terminal project required")
	}
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+filepath.Dir(cargo)+string(os.PathListSeparator)+os.Getenv("PATH"))
	var runtimePin record
	var sdk string
	if platform == "windows-arm64" {
		if e = o.require("llvm-mingw"); e != nil {
			return e
		}
		sdk, e = absolute(o.get("llvm-mingw"))
		if e != nil {
			return e
		}
		catalog, e := load(a.path("packaging/git-terminal-runtime.lock.json"))
		if e != nil {
			return e
		}
		runtimePin = object(object(catalog, "platforms"), platform)
		receipt, e := load(filepath.Join(sdk, "receipt.json"))
		if e != nil {
			return e
		}
		if object(receipt, "source")["sha256"] != object(catalog, "source_archive")["sha256"] {
			return fmt.Errorf("compiler SDK pin mismatch")
		}
		for _, name := range []string{stringValue(runtimePin, "path"), stringValue(runtimePin, "license_path"), "bin/aarch64-w64-mingw32-clang.exe", "bin/llvm-ar.exe"} {
			h, e := digest(filepath.Join(sdk, filepath.FromSlash(name)))
			if e != nil || h != object(receipt, "files")[name] {
				return fmt.Errorf("SDK bytes changed: %s", name)
			}
		}
		for _, pair := range [][2]string{{"path", "sha256"}, {"license_path", "license_sha256"}} {
			h, e := digest(filepath.Join(sdk, filepath.FromSlash(stringValue(runtimePin, pair[0]))))
			if e != nil || h != runtimePin[pair[1]] {
				return fmt.Errorf("pinned unwind runtime changed")
			}
		}
		compiler := filepath.Join(sdk, "bin/aarch64-w64-mingw32-clang.exe")
		env = append(env, "PATH="+filepath.Join(sdk, "bin")+string(os.PathListSeparator)+filepath.Dir(cargo)+string(os.PathListSeparator)+os.Getenv("PATH"), "CARGO_TARGET_AARCH64_PC_WINDOWS_GNULLVM_LINKER="+compiler, "CC_aarch64_pc_windows_gnullvm="+compiler, "AR_aarch64_pc_windows_gnullvm="+filepath.Join(sdk, "bin/llvm-ar.exe"))
	}
	if _, e = commandAt(source, env, cargo, "build", "--locked", "--release", "--target", target, "--target-dir", filepath.Join(source, "target")); e != nil {
		return e
	}
	raw, e := commandAt(source, env, cargo, "metadata", "--locked", "--format-version", "1", "--filter-platform", target)
	if e != nil {
		return e
	}
	var metadata record
	if e = json.Unmarshal(raw, &metadata); e != nil {
		return e
	}
	nodes := map[string]record{}
	for _, v := range array(object(metadata, "resolve"), "nodes") {
		item := rec(v)
		nodes[stringValue(item, "id")] = item
	}
	var pending []string
	if root := stringValue(object(metadata, "resolve"), "root"); root != "" {
		pending = append(pending, root)
	} else {
		for _, v := range array(metadata, "workspace_default_members") {
			pending = append(pending, v.(string))
		}
	}
	reached := map[string]bool{}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reached[id] {
			continue
		}
		reached[id] = true
		for _, v := range array(nodes[id], "dependencies") {
			pending = append(pending, v.(string))
		}
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	for _, name := range []string{"git-terminal.exe", "git-terminal-shell.exe", "git-terminal-askpass.exe"} {
		if e = copyFile(filepath.Join(source, "target", target, "release", name), filepath.Join(out, name)); e != nil {
			return e
		}
	}
	if runtimePin != nil {
		if e = copyFile(filepath.Join(sdk, filepath.FromSlash(stringValue(runtimePin, "path"))), filepath.Join(out, "libunwind.dll")); e != nil {
			return e
		}
		if e = copyFile(filepath.Join(sdk, filepath.FromSlash(stringValue(runtimePin, "license_path"))), filepath.Join(out, "licenses/llvm-unwind/LICENSE.TXT")); e != nil {
			return e
		}
	}
	entries, e := os.ReadDir(filepath.Join(source, "shell-integration"))
	if e != nil {
		return e
	}
	for _, v := range entries {
		if !v.IsDir() {
			if e = copyFile(filepath.Join(source, "shell-integration", v.Name()), filepath.Join(out, "shell-integration", v.Name())); e != nil {
				return e
			}
		}
	}
	var dependencies []any
	for _, v := range array(metadata, "packages") {
		item := rec(v)
		if !reached[stringValue(item, "id")] {
			continue
		}
		root := filepath.Dir(stringValue(item, "manifest_path"))
		entries, e := os.ReadDir(root)
		if e != nil {
			return e
		}
		notices := map[string]bool{}
		for _, v := range entries {
			upper := strings.ToUpper(v.Name())
			if !v.IsDir() && (strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE") || strings.HasPrefix(upper, "AUTHORS")) {
				notices[filepath.Join(root, v.Name())] = true
			}
		}
		if name := stringValue(item, "license_file"); name != "" {
			if !filepath.IsAbs(name) {
				name = filepath.Join(root, name)
			}
			notices[name] = true
		}
		var names []string
		for p := range notices {
			st, e := os.Lstat(p)
			actual, ae := filepath.EvalSymlinks(p)
			if e != nil || ae != nil || st.Mode()&os.ModeSymlink != 0 || !inside(root, actual) {
				return fmt.Errorf("unsafe dependency notice")
			}
			relative := "licenses/" + stringValue(item, "name") + "-" + stringValue(item, "version") + "/" + filepath.Base(p)
			if _, e = confined(out, relative); e != nil {
				return e
			}
			if e = copyFile(p, filepath.Join(out, filepath.FromSlash(relative))); e != nil {
				return e
			}
			names = append(names, relative)
		}
		sort.Strings(names)
		dependencies = append(dependencies, record{"name": item["name"], "version": item["version"], "license": item["license"], "notices": names})
	}
	for _, name := range []string{"README.md", "AGENTS.md", "LICENSE", "docs/site/index.html", "vendor/gpui-terminal/MODIFICATIONS.md", "vendor/portable-pty/MODIFICATIONS.md"} {
		if e = copyFile(filepath.Join(source, filepath.FromSlash(name)), filepath.Join(out, "manuals", filepath.FromSlash(name))); e != nil {
			return e
		}
	}
	if e = writeNewJSON(filepath.Join(out, "DEPENDENCIES.json"), dependencies); e != nil {
		return e
	}
	inventory, e := hashes(out)
	if e != nil {
		return e
	}
	receipt := record{"schema_version": 1, "product": "git-terminal", "version": field("version"), "platform": platform, "renderer": "gpui-fast", "renderer_version": "0.1.10", "source_repository": field("repository"), "source_commit": strings.TrimSpace(string(commit)), "source_built": true, "profile": "release", "tests_executed": false, "rust_target": target, "additional_runtime": runtimePin, "files": inventory}
	if e = writeNewJSON(filepath.Join(out, "git-terminal-package.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": "built-not-runtime-accepted", "product": "git-terminal", "platform": platform, "source_commit": receipt["source_commit"], "files": len(inventory)})
}
