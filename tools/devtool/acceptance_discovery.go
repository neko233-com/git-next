// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func discoveryEnvironment(home string) []string {
	env := []string{}
	for _, item := range acceptEnv(home) {
		key, _, _ := strings.Cut(item, "=")
		if key != "GIT_CONFIG_NOSYSTEM" && !strings.EqualFold(key, "EXEPATH") {
			env = append(env, item)
		}
	}
	return env
}
func nativeDiscovery(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if !runtimeOSWindows() {
		return fmt.Errorf("Windows discovery fixture requires native Windows; use native-discovery-unix-acceptance")
	}
	root, e := os.MkdirTemp("", "git-next-native-discovery-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	r := acceptCapture("filesystem-only-windows-discovery", func() {
		client := filepath.Join(root, "client/git-next.exe")
		acceptMust(copyFile(binary, client))
		home, install := filepath.Join(root, "home"), filepath.Join(root, "git-install")
		command, core := filepath.Join(install, "cmd"), filepath.Join(install, "mingw64/libexec/git-core")
		acceptMust(os.MkdirAll(core, 0755))
		acceptMust(os.MkdirAll(command, 0755))
		system := filepath.Join(install, "etc/gitconfig")
		acceptWrite(system, []byte("[probe]\nsource = filesystem\n[credential]\nhelper = fixture-only\n[include]\npath = relative.inc\n"))
		acceptWrite(filepath.Join(filepath.Dir(system), "relative.inc"), []byte("[probe]\nincluded = yes\n"))
		rustc, e := exec.LookPath("rustc")
		if e != nil {
			home, e := os.UserHomeDir()
			acceptMust(e)
			rustc = filepath.Join(home, ".cargo/bin/rustc.exe")
		}
		acceptRun(a.root, nil, nil, true, rustc, "--edition=2024", "--crate-name", "config_tripwire", "-O", a.path("tests/fixtures/config_tripwire.rs"), "-o", filepath.Join(command, "git.exe"))
		marker := filepath.Join(root, "git-was-executed")
		env := fixtureEnv(discoveryEnvironment(home), map[string]string{"PATH": command, "GIT_NEXT_CONFIG_TRIPWIRE": marker, "GIT_NEXT_FIXTURE_CONFIG": system, "GIT_NEXT_FIXTURE_CORE": core, "ProgramFiles": filepath.Join(root, "programs"), "ProgramW6432": filepath.Join(root, "programs"), "ProgramFiles(x86)": filepath.Join(root, "programs-x86"), "LOCALAPPDATA": filepath.Join(root, "local"), "PROGRAMDATA": filepath.Join(root, "programdata")})
		run := func(where string, extra map[string]string, ok bool, args ...string) []byte {
			v := acceptRun(where, fixtureEnv(env, extra), nil, ok, client, args...)
			acceptCheck(!exists(marker), "native discovery executed Git tripwire")
			return v
		}
		repo := filepath.Join(root, "repo")
		run(root, nil, true, "--version")
		run(root, nil, true, "init", repo)
		acceptEqual(run(repo, nil, true, "config", "--get", "probe.source"), []byte("filesystem\n"), "system source")
		acceptEqual(run(repo, nil, true, "config", "--get", "probe.included"), []byte("yes\n"), "relative include")
		acceptEqual(run(repo, nil, true, "config", "--get-all", "credential.helper"), []byte("fixture-only\n"), "helper once")
		for _, working := range []string{root, repo} {
			for _, args := range [][]string{{"config", "--get", "probe.source"}, {"config", "--no-includes", "--get", "probe.source"}, {"config", "--system", "--get", "probe.source"}} {
				acceptEqual(run(working, nil, true, args...), []byte("filesystem\n"), "filesystem system query")
			}
			acceptEqual(run(working, map[string]string{"GIT_CONFIG_NOSYSTEM": "0"}, true, "config", "--get", "probe.source"), []byte("filesystem\n"), "false NOSYSTEM")
			run(working, map[string]string{"GIT_CONFIG_NOSYSTEM": "1"}, false, "config", "--get", "probe.source")
		}
		earlier := filepath.Join(root, "programdata/Git/config")
		acceptWrite(earlier, []byte("[probe]\nearlier = yes\nsource = earlier\n[credential]\nhelper = earlier-fixture\n"))
		for _, working := range []string{root, repo} {
			acceptEqual(run(working, nil, true, "config", "--get", "probe.earlier"), []byte("yes\n"), "programdata")
			acceptEqual(run(working, nil, true, "config", "--get", "probe.source"), []byte("filesystem\n"), "system precedence")
			for _, args := range [][]string{{"config", "--get-all", "credential.helper"}, {"config", "--no-includes", "--get-all", "credential.helper"}} {
				acceptEqual(run(working, nil, true, args...), []byte("earlier-fixture\nfixture-only\n"), "helper precedence")
			}
		}
		override := filepath.Join(root, "override-config")
		acceptWrite(override, []byte("[probe]\nsource = override\n"))
		for _, working := range []string{root, repo} {
			acceptEqual(run(working, map[string]string{"GIT_CONFIG_SYSTEM": override}, true, "config", "--get-all", "probe.source"), []byte("override\n"), "system override")
			run(working, map[string]string{"GIT_CONFIG_SYSTEM": override}, false, "config", "--get", "probe.earlier")
		}
		empty := filepath.Join(root, "empty")
		acceptMust(os.MkdirAll(empty, 0755))
		for _, extra := range []map[string]string{{"PATH": empty, "EXEPATH": install}, {"PATH": empty, "GIT_EXEC_PATH": core}} {
			acceptEqual(run(repo, extra, true, "config", "--get", "probe.source"), []byte("filesystem\n"), "explicit prefix")
		}
		run(repo, nil, true, "rev-parse", "--show-toplevel")
		run(repo, nil, true, "status", "--porcelain")
		run(repo, map[string]string{"GIT_CONFIG_NOSYSTEM": "1"}, true, "status", "--porcelain")
	})
	return acceptReport(a, o, record{"status": r["status"], "results": []record{r}, "binary_sha256": acceptHash(binary), "git_tripwire_executed": false, "system_configuration_enabled": true, "native_system_config": true, "relative_includes": true, "system_override": true, "helper_not_duplicated": true, "programdata_precedence": true, "explicit_exec_path": true, "no_sibling_git": true, "no_system_boolean": true})
}
func nativeDiscoveryUnix(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if runtimeOSWindows() {
		return fmt.Errorf("Unix discovery requires native Unix or isolated Linux container")
	}
	root, e := os.MkdirTemp("", "git-next-unix-discovery-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	pairs := 0
	r := acceptCapture("filesystem-only-unix-discovery", func() {
		home, marker := filepath.Join(root, "home"), filepath.Join(root, "git-was-executed")
		prefix, brew := filepath.Join(root, "custom"), filepath.Join(root, "brew")
		cellar := filepath.Join(brew, "Cellar/git/2.54.0")
		trap := []byte("#!/bin/sh\nprintf invoked > \"$GIT_NEXT_CONFIG_TRIPWIRE\"\nexit 88\n")
		for _, location := range []string{prefix, cellar} {
			acceptMust(write(filepath.Join(location, "bin/git"), trap, 0755))
			acceptMust(os.MkdirAll(filepath.Join(location, "libexec/git-core"), 0755))
		}
		acceptMust(os.MkdirAll(filepath.Join(brew, "bin"), 0755))
		acceptMust(os.Symlink("../Cellar/git/2.54.0/bin/git", filepath.Join(brew, "bin/git")))
		acceptMust(os.MkdirAll(filepath.Join(brew, "opt"), 0755))
		acceptMust(os.Symlink("../Cellar/git/2.54.0", filepath.Join(brew, "opt/git")))
		shims := filepath.Join(root, "shims")
		acceptMust(os.MkdirAll(shims, 0755))
		acceptMust(os.Symlink(filepath.Join(cellar, "bin/git"), filepath.Join(shims, "git")))
		for location, value := range map[string]string{prefix: "prefix", brew: "homebrew", cellar: "incorrect-cellar"} {
			acceptWrite(filepath.Join(location, "etc/gitconfig"), []byte("[probe]\nsource = "+value+"\n[credential]\nhelper = "+value+"-fixture\n[include]\npath = relative.inc\n"))
			acceptWrite(filepath.Join(location, "etc/relative.inc"), []byte("[probe]\nincluded = "+value+"\n"))
		}
		env := fixtureEnv(discoveryEnvironment(home), map[string]string{"GIT_NEXT_CONFIG_TRIPWIRE": marker})
		run := func(path []string, where string, extra map[string]string, ok bool, args ...string) []byte {
			variables := fixtureEnv(fixtureEnv(env, map[string]string{"PATH": strings.Join(path, string(os.PathListSeparator))}), extra)
			out := acceptRun(where, variables, nil, ok, binary, args...)
			acceptCheck(!exists(marker), "Unix discovery executed Git tripwire")
			return out
		}
		direct := []string{filepath.Join(prefix, "bin"), filepath.Join(brew, "bin")}
		acceptEqual(run(direct, root, nil, true, "config", "--get", "probe.source"), []byte("prefix\n"), "prefix source")
		acceptEqual(run(direct, root, nil, true, "config", "--get", "probe.included"), []byte("prefix\n"), "prefix include")
		repo := filepath.Join(root, "repo")
		run(direct, root, nil, true, "init", repo)
		acceptEqual(run(direct, repo, nil, true, "config", "--get", "probe.source"), []byte("prefix\n"), "prefix repo")
		override := filepath.Join(root, "override-config")
		acceptWrite(override, []byte("[probe]\nsource = override\n"))
		originals := map[string][]byte{}
		acceptMust(filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && strings.Contains(filepath.ToSlash(p), "/etc/") {
				originals[p] = acceptBytes(p)
			}
			return nil
		}))
		for _, directory := range []string{filepath.Join(brew, "bin"), filepath.Join(brew, "opt/git/bin"), filepath.Join(cellar, "bin"), shims} {
			path := []string{directory}
			for _, working := range []string{root, repo} {
				for _, args := range [][]string{{"config", "--get", "probe.source"}, {"config", "--get", "probe.included"}, {"config", "--no-includes", "--get", "probe.source"}, {"config", "--system", "--get", "probe.source"}} {
					acceptEqual(run(path, working, nil, true, args...), []byte("homebrew\n"), "Homebrew query")
				}
				acceptEqual(run(path, working, nil, true, "config", "--show-scope", "--get", "probe.source"), []byte("system\thomebrew\n"), "system scope")
				acceptEqual(run(path, working, nil, true, "config", "--get-all", "credential.helper"), []byte("homebrew-fixture\n"), "helper single")
				acceptEqual(run(path, working, map[string]string{"GIT_CONFIG_NOSYSTEM": "0"}, true, "config", "--get", "probe.source"), []byte("homebrew\n"), "false NOSYSTEM")
				for _, v := range []string{"1", "true"} {
					run(path, working, map[string]string{"GIT_CONFIG_NOSYSTEM": v}, false, "config", "--get", "probe.source")
				}
				for _, args := range [][]string{{"config", "--get-all", "probe.source"}, {"config", "--system", "--get", "probe.source"}} {
					acceptEqual(run(path, working, map[string]string{"GIT_CONFIG_SYSTEM": override}, true, args...), []byte("override\n"), "override")
				}
				run(path, working, map[string]string{"GIT_CONFIG_SYSTEM": ""}, false, "config", "--get", "probe.source")
				run(path, repo, nil, true, "status", "--porcelain")
				pairs++
			}
		}
		for p, data := range originals {
			acceptEqual(acceptBytes(p), data, "read-only discovery")
		}
		run([]string{filepath.Join(brew, "bin")}, root, nil, true, "config", "--system", "probe.written", "native")
		acceptEqual(run([]string{filepath.Join(brew, "bin")}, root, nil, true, "config", "--system", "--get", "probe.written"), []byte("native\n"), "system write")
		acceptEqual(run([]string{filepath.Join(brew, "bin")}, repo, nil, true, "config", "--get", "probe.included"), []byte("homebrew\n"), "include after write")
		for p, data := range originals {
			if p != filepath.Join(brew, "etc/gitconfig") {
				acceptEqual(acceptBytes(p), data, "unselected config unchanged")
			}
		}
	})
	return acceptReport(a, o, record{"status": r["status"], "results": []record{r}, "binary_sha256": acceptHash(binary), "scope": "Unix synthetic filesystem conformance; not native macOS/Homebrew installation acceptance", "layouts": []string{"prefix", "homebrew-bin-symlink", "homebrew-opt-symlink", "homebrew-cellar", "external-symlink"}, "brew_layout_context_pairs": pairs, "git_tripwire_executed": false, "relative_includes": true, "system_scope_overrides_and_writes": true, "inactive_distribution_ignored": true, "real_account_login_verified": false})
}
func init() {
	register("native-discovery-acceptance", "Windows filesystem-only config discovery with executable tripwire", "output", nativeDiscovery)
	register("native-discovery-unix-acceptance", "Unix prefix/Homebrew filesystem discovery with tripwire", "output", nativeDiscoveryUnix)
}
