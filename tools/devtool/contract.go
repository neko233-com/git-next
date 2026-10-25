// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func init() {
	register("verify-repo-contract", "Check repository defaults, safety, docs, acceptance and commit rules", "full? json", verifyRepoContract)
}
func tomlSetting(text, section, key string) string {
	active := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			active = line == "["+section+"]"
			continue
		}
		if active {
			left, right, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(left) == key {
				return strings.TrimSpace(strings.SplitN(right, "#", 2)[0])
			}
		}
	}
	return ""
}
func validateProfiles(text string) error {
	if tomlSetting(text, "profile.release", "opt-level") != "3" || tomlSetting(text, "profile.release", "lto") != "false" || tomlSetting(text, "profile.release", "codegen-units") != "16" || tomlSetting(text, "profile.dev", "incremental") != "true" || tomlSetting(text, "profile.dev", "opt-level") != "0" {
		return fmt.Errorf("profiles must retain release opt-level 3, no LTO, 16 codegen units and incremental dev opt-level 0")
	}
	return nil
}
func verifyRepoContract(a *app, o options) error {
	var checks []check
	add := func(name string, ok bool, detail string) { checks = append(checks, check{name, ok, detail}) }
	text := func(relative string) string {
		value, _ := read(a.path(relative))
		return strings.ReplaceAll(strings.TrimPrefix(value, "\ufeff"), "\r\n", "\n")
	}
	d, agreementChecks, e := agreement(a)
	if e != nil {
		return e
	}
	add("defaults-fragment-exists", exists(a.path("packaging/runtime-defaults/gitconfig")), "authoritative defaults fragment")
	add("migration-literal-rendered", strings.Contains(text("packaging/windows/Takeover.Core.psm1"), `"`+d.powershell()+`"`), "migration literal matches rendering")
	assembly := text("tools/devtool/packaging_assemble.go")
	add("payload-config-rendered", strings.Contains(assembly, "d.core()") && strings.Contains(assembly, "d.tail()"), "native payload consumes the authoritative fragment")
	add("evidence-check-rendered", strings.Contains(text("tools/devtool/evidence.go"), "readDefaults(a.root)") && (strings.Contains(text("tools/devtool/evidence.go"), "d.core()+d.tail()") || strings.Contains(text("tools/devtool/evidence.go"), "d.core() + d.tail()")), "native evidence renders defaults")
	var missing []string
	document := text("docs/DEFAULTS.md")
	for _, entry := range d.entries {
		if !strings.Contains(document, entry.key) {
			missing = append(missing, entry.key)
		}
	}
	add("defaults-documented", len(missing) == 0, strings.Join(missing, ", "))
	bridge := text("native-core/launcher/windows-bridge.c")
	add("bridge-compares-file-identity", strings.Contains(bridge, "GetFileInformationByHandle"), "volume serial and file index")
	add("bridge-limits-forwarding-depth", strings.Contains(bridge, "DEPTH_LIMIT"), "bounded bridge forwarding")
	memory := text("native-core/launcher/freestanding-memory.c")
	helperPresent := true
	for _, token := range []string{"void *memcpy(", "void *memset(", "void *memmove(", "int memcmp(", "#pragma function(memcpy)", "GN_NOOPT"} {
		helperPresent = helperPresent && strings.Contains(memory, token)
	}
	add("bridge-provides-freestanding-memory", helperPresent, "helpers remain optimization-off to prevent self recursion")
	builder := text("tools/devtool/build_bridge.go")
	add("bridge-keeps-full-o2", strings.Contains(builder, `"O2"`) && strings.Contains(builder, `"ob1_workaround": false`) && strings.Contains(builder, `"/NODEFAULTLIB"`), "full /O2 with CRT-free memory support")
	add("packager-requires-rebuilt-bridge", strings.Contains(builder, "memory_source_sha256") && strings.Contains(assembly, "memory_source_sha256") && strings.Contains(assembly, "freestanding-memory.c") && strings.Contains(assembly, "bridgeImportsMatch"), "stale bridge receipts and foreign imports are refused")
	watch := text("tools/devtool/watchdog.go") + text("tools/devtool/watchdog_windows.go")
	add("watchdog-has-ceiling", strings.Contains(watch, `"limit"`) && strings.Contains(watch, "PROCESS_TERMINATE"), "Go supervisor enforces a process ceiling")
	add("watchdog-reaps-daemons", strings.Contains(watch, `"daemons"`), "Go supervisor reclaims orphan daemons")
	container := text("docker/tests/run.sh")
	add("containers-have-pids-limit", strings.Contains(container, "--pids-limit") && strings.Contains(container, "--memory"), "container process/memory bounds")
	var absent, unlinked []string
	index := text("docs/site/index.html")
	for _, m := range manuals {
		if !exists(a.path("docs/site", m.page)) {
			absent = append(absent, m.page)
		}
		if m.page != "index.html" && !strings.Contains(index, `href="`+m.page+`"`) {
			unlinked = append(unlinked, m.page)
		}
	}
	add("site-pages-present", len(absent) == 0, fmt.Sprintf("%d pages; missing %v", len(manuals), absent))
	add("site-pages-linked", len(unlinked) == 0, fmt.Sprint(unlinked))
	tracked, gitError := a.run("git", "ls-files", "-z")
	if gitError == nil && tracked.code == 0 {
		var offenders []string
		for _, name := range strings.Split(string(tracked.stdout), "\x00") {
			if name == "" || strings.HasPrefix(name, "docs/benchmarks/") {
				continue
			}
			switch filepath.Ext(name) {
			case ".ps1", ".psm1", ".bat", ".cmd":
				continue
			}
			data, e := os.ReadFile(a.path(name))
			if e != nil || bytes.ContainsRune(data, 0) {
				continue
			}
			if bytes.Contains(data, []byte("\r\n")) {
				offenders = append(offenders, name)
			}
		}
		add("tracked-files-use-lf", len(offenders) == 0, strings.Join(offenders, ", "))
	} else {
		add("tracked-files-use-lf", true, "skipped: Git unavailable")
	}
	add("no-github-workflows", !exists(a.path(".github/workflows")), "GitHub Actions remain absent")
	_, userMSI := packageValidationCases["msi-user"]
	_, machineMSI := packageValidationCases["msi-machine"]
	add("package-policy-bounded", len(packageValidationCases) == 2 && userMSI && machineMSI, "explicit package policy can waive only the two MSI lifecycle rows")
	_, packageProbes := commands["native-package-validation-probes"]
	add("package-policy-probes-present", packageProbes, "compiled package, isolated migration and actual resource probes remain required")
	add("package-policy-documented", strings.Contains(text("AGENTS.md"), "--package-validation-policy") && strings.Contains(text("docs/GO_TOOLING.md"), "installation_runtime_accepted: false"), "authorization, exact inputs, waived scope and rollback are documented")
	commit, e := a.run("git", "log", "-1", "--format=%B")
	if e == nil && commit.code == 0 {
		message := strings.TrimSpace(string(commit.stdout))
		subject := strings.SplitN(message, "\n", 2)[0]
		pattern := regexp.MustCompile(`^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([^)]+\))?!?: .+`)
		add("commit-conventional-subject", pattern.MatchString(subject), subject)
		add("commit-assistance-trailer", strings.Contains(message, "Assisted-by:"), "Assisted-by trailer")
		add("commit-no-signoff", !strings.Contains(message, "Signed-off-by:"), "human-only sign-off")
	} else {
		add("commit-contract", true, "skipped: Git unavailable")
	}
	var diagnostics bytes.Buffer
	localeApp := &app{a.root, io.Discard, &diagnostics}
	localeError := checkLocales(localeApp, options{})
	add("locale-documents-valid", localeError == nil, strings.TrimSpace(diagnostics.String()))
	cargo := text("Cargo.toml")
	add("release-without-lto", tomlSetting(cargo, "profile.release", "opt-level") == "3" && tomlSetting(cargo, "profile.release", "lto") == "false" && tomlSetting(cargo, "profile.release", "codegen-units") == "16", "release profile")
	add("dev-profile-incremental", tomlSetting(cargo, "profile.dev", "incremental") == "true", "incremental dev profile")
	_, fastPresent := commands["fast-verify"]
	add("fast-verify-present", fastPresent, "Go artifact diagnostic")
	main := text("accept-runner/src/main.rs")
	add("git-next-rust-accept-runner", main != "", "Rust acceptance gate")
	active := []string{}
	paths, e := filepath.Glob(a.path("tests/*.py"))
	if e != nil {
		return e
	}
	for _, p := range paths {
		name := filepath.Base(p)
		if name != "acceptance.py" && !strings.HasSuffix(name, "_acceptance.py") {
			continue
		}
		if strings.HasPrefix(name, "native_") || strings.HasPrefix(name, "terminal_") || strings.HasPrefix(name, "windows_") || strings.HasPrefix(name, "gcm_") {
			continue
		}
		active = append(active, name)
	}
	add("git-next-no-python-cli-gate", !exists(a.path("tests/run_git_next_feature_suite.py")) && len(active) == 0, strings.Join(active, ", "))
	add("git-next-accept-tiers", strings.Contains(main, "TierArg"), "l0/full/network")
	add("git-next-accept-lockfile", exists(a.path("accept-runner/Cargo.lock")), "locked acceptance dependencies")
	archived, e := filepath.Glob(a.path("tests/legacy_cli/*.py"))
	if e != nil {
		return e
	}
	add("git-next-legacy-cli-removed", len(archived) == 0, "historical Python CLI suites exist only in Git history")
	if o.yes("full") {
		checks = append(checks, agreementChecks...)
	}
	failed := 0
	for _, c := range checks {
		mark := "ok"
		if !c.OK {
			mark = "FAIL"
			failed++
		}
		fmt.Fprintf(a.out, "%-36s %-4s %s\n", c.Name, mark, c.Detail)
	}
	fmt.Fprintf(a.out, "%d rules checked, %d failed\n", len(checks), failed)
	if o.get("json") != "" {
		if e = writeJSON(o.get("json"), record{"checks": checks, "failed": failed, "ok": failed == 0, "utc": time.Now().UTC()}); e != nil {
			return e
		}
	}
	if failed > 0 {
		return fmt.Errorf("repository contract has %d failed check(s)", failed)
	}
	return nil
}
