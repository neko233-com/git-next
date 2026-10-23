// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeCase struct {
	name    string
	command []string
	timeout time.Duration
}

func init() {
	register("native-product-batch", "Run consolidated native correctness or paired timing batch", "native-packages reference output workers wall-clock-target performance? performance-runs network-runs correctness-receipt reuse-msi-batch reuse-msi-matrix reuse-desktop-batch reuse-desktop-matrix terminal-waiver", nativeProductBatch)
	register("product-batch", "Compatibility entrypoint for the native product batch", "native-packages reference output workers wall-clock-target performance? performance-runs network-runs correctness-receipt reuse-msi-batch reuse-msi-matrix reuse-desktop-batch reuse-desktop-matrix terminal-waiver", func(a *app, o options) error {
		if o.get("native-packages") == "" {
			return fmt.Errorf("Rust CLI conformance uses accept-runner/git-next-accept --tier full; native packages require --native-packages")
		}
		return nativeProductBatch(a, o)
	})
	register("native-docker-batch", "Run one complete-package Docker acceptance and clean exact owned resources", "platform archive sha256 base-image output go", nativeDockerBatch)
}
func matrixHarnessPath(a *app, m record, name string) (string, error) {
	if peer := stringValue(m, "terminal_source"); peer != "" && strings.HasPrefix(name, "../git-terminal/") {
		return confined(peer, strings.TrimPrefix(name, "../git-terminal/"))
	}
	return a.path(filepath.FromSlash(name)), nil
}
func nativeMatrixReadback(a *app, m record) error {
	for path, want := range object(m, "files") {
		if e := requireHash(path, want); e != nil {
			return e
		}
	}
	for name, want := range object(m, "harnesses") {
		path, e := matrixHarnessPath(a, m, name)
		if e != nil {
			return e
		}
		if e := requireHash(path, want); e != nil {
			return e
		}
	}
	mac := object(object(m, "platforms"), "macos-arm64")
	if mac["native_executed"] == true && stringValue(mac, "runtime_receipt") == "" {
		return fmt.Errorf("Mac native execution flag lacks its exact runtime receipt")
	}
	if path := stringValue(mac, "runtime_receipt"); path != "" {
		if mac["native_executed"] != true {
			return fmt.Errorf("Mac actual runtime evidence flag is inconsistent")
		}
		if e := requireHash(path, object(m, "files")[path]); e != nil {
			return e
		}
		r, e := checkedMacRuntimeReceipt(a, path, stringValue(mac, "archive"), stringValue(m, "source_tree_sha256"), stringValue(m, "version"))
		if e != nil {
			return e
		}
		if r["archive_sha256"] != mac["runtime_archive_sha256"] {
			return fmt.Errorf("Mac runtime archive hash is inconsistent")
		}
	}
	return nil
}
func roundedSeconds(d time.Duration) float64 { return math.Round(d.Seconds()*1000) / 1000 }
func nativeProductBatch(a *app, o options) error {
	if e := o.require("native-packages", "reference", "output"); e != nil {
		return e
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("consolidated Windows/MSI batch requires Windows; platform items run independently")
	}
	if e := requireImplementationComplete(a); e != nil {
		return e
	}
	workers, e := o.integer("workers", 8)
	if e != nil || workers < 1 || workers > 16 {
		return fmt.Errorf("workers must be 1..16")
	}
	runs, e := o.integer("performance-runs", 40)
	if e != nil || runs < 10 || runs > 100 {
		return fmt.Errorf("performance-runs must be 10..100")
	}
	networkRuns, e := o.integer("network-runs", 20)
	if e != nil || networkRuns < 10 || networkRuns > 100 {
		return fmt.Errorf("network-runs must be 10..100")
	}
	target, e := strconv.ParseFloat(o.value("wall-clock-target", "60"), 64)
	if e != nil || target <= 0 || math.IsInf(target, 0) || math.IsNaN(target) {
		return fmt.Errorf("positive finite wall-clock target required")
	}
	startedBatch := time.Now()
	matrix, e := load(o.get("native-packages"))
	if e != nil {
		return e
	}
	platforms := object(matrix, "platforms")
	if number(matrix, "schema_version") != 1 || len(platforms) != 5 {
		return fmt.Errorf("five complete native package inputs required")
	}
	for _, target := range nativePlatforms {
		if object(platforms, target) == nil {
			return fmt.Errorf("missing native platform %s", target)
		}
	}
	if e = nativeMatrixReadback(a, matrix); e != nil {
		return e
	}
	reference, e := absolute(o.get("reference"))
	if e != nil {
		return e
	}
	referenceSHA, e := digest(reference)
	if e != nil {
		return e
	}
	identity, e := digest(o.get("native-packages"))
	if e != nil {
		return e
	}
	waiver, e := readNativeWaiver(a, o.get("terminal-waiver"), matrix, identity)
	if e != nil {
		return e
	}
	if waiver != nil && (o.get("reuse-desktop-batch") != "" || o.get("reuse-desktop-matrix") != "") {
		return fmt.Errorf("terminal waiver cannot be combined with desktop acceptance reuse")
	}
	reusedMSI, reusedDesktop := record{}, record{}
	for _, kind := range []string{"msi", "desktop"} {
		report, matrixPath := o.get("reuse-"+kind+"-batch"), o.get("reuse-"+kind+"-matrix")
		if (report == "") != (matrixPath == "") || o.yes("performance") && report != "" {
			return fmt.Errorf("evidence reuse requires both correctness receipt and frozen matrix")
		}
		if report != "" {
			cases := []string{"msi-user", "msi-machine"}
			if kind == "desktop" {
				cases = []string{"terminal-native-desktop"}
			}
			values, e := loadReuse(a, report, matrixPath, matrix, referenceSHA, cases)
			if e != nil {
				return e
			}
			if kind == "msi" {
				reusedMSI = values
			} else {
				if len(values) == 0 {
					return fmt.Errorf("exact desktop case did not pass supplied evidence")
				}
				reusedDesktop = values
			}
		}
	}
	out, e := newDestination(o.get("output"))
	if e != nil {
		return e
	}
	win := object(platforms, "windows-x64")
	binary := filepath.Join(stringValue(win, "directory"), "bin/git.exe")
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LANG=C", "LC_ALL=C", "PATH="+filepath.Dir(reference)+string(os.PathListSeparator)+os.Getenv("PATH"))
	results := []record{}
	var printMu sync.Mutex
	run := func(c nativeCase) record {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()
		r, e := runProcess(ctx, a.root, env, nil, c.command[0], c.command[1:]...)
		log := append(append(append([]byte{}, r.stdout...), '\n'), r.stderr...)
		result := record{"case": c.name, "status": "pass", "exit_code": r.code}
		if e != nil {
			result["status"] = "fail"
			result["reason"] = e.Error()
			delete(result, "exit_code")
		} else if r.code != 0 {
			result["status"] = "fail"
		}
		if e = write(filepath.Join(out, c.name+".log"), log, 0644); e != nil {
			result["status"] = "fail"
			result["reason"] = e.Error()
		}
		result["seconds"] = roundedSeconds(time.Since(started))
		result["started_seconds_from_batch"] = roundedSeconds(started.Sub(startedBatch))
		result["finished_seconds_from_batch"] = roundedSeconds(time.Since(startedBatch))
		printMu.Lock()
		fmt.Fprintf(a.out, "%s => %s\n", c.name, result["status"])
		printMu.Unlock()
		return result
	}
	makeTool := func(name string, timeout int, args ...string) nativeCase {
		return nativeCase{name, toolCommand(args...), time.Duration(timeout) * time.Second}
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32/WindowsPowerShell/v1.0/powershell.exe")
	ps := func(name, script string, timeout int, args ...string) nativeCase {
		command := append([]string{shell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}, args...)
		return nativeCase{name, command, time.Duration(timeout) * time.Second}
	}
	var raceContainer, raceScope, correctnessSHA string
	if o.yes("performance") {
		if e = o.require("correctness-receipt"); e != nil {
			return e
		}
		prior, e := load(o.get("correctness-receipt"))
		if e != nil {
			return e
		}
		if e = validateNativeCorrectness(prior, matrix, identity, referenceSHA, waiver); e != nil {
			return e
		}
		correctnessSHA, e = digest(o.get("correctness-receipt"))
		if e != nil {
			return e
		}
		binarySHA, e := digest(binary)
		if e != nil {
			return e
		}
		checkpoint := filepath.Join(out, "native-identity.json")
		if e = writeNewJSON(checkpoint, record{"binary_sha256": binarySHA, "source_tree_sha256": matrix["source_tree_sha256"]}); e != nil {
			return e
		}
		for _, shape := range [][2]int{{1000, 16}, {10000, 1000}, {100000, 1000}} {
			name := "performance-" + strconv.Itoa(shape[0])
			results = append(results, run(makeTool(name, 1800, "benchmark", binary, "--reference", reference, "--runs", strconv.Itoa(runs), "--files", strconv.Itoa(shape[0]), "--directories", strconv.Itoa(shape[1]), "--read-commands", "--system-config-enabled", "--native-distribution", "--reference-read-safety", "--fixture-layout", "packed", "--source-checkpoint", checkpoint, "--output", filepath.Join(out, name+".json"))))
		}
		results = append(results, run(makeTool("performance-network", 1800, "network-benchmark", binary, "--reference", reference, "--runs", strconv.Itoa(networkRuns), "--native-distribution", "--source-checkpoint", checkpoint, "--output", filepath.Join(out, "performance-network.json"))))
	} else {
		terminalSource := stringValue(matrix, "terminal_source")
		if terminalSource == "" {
			terminalSource = filepath.Join(filepath.Dir(a.root), "git-terminal")
		}
		runtimeRoot := ""
		if waiver == nil {
			runtimeRoot, e = prepareTerminalRuntime(a, stringValue(win, "directory"), filepath.Join(out, "terminal-runtime"))
			if e != nil {
				return e
			}
		}
		cases := []nativeCase{
			makeTool("windows-core-contracts", 1200, "native-contract-acceptance", binary, "--reference", reference, "--output", filepath.Join(out, "windows-core.json")),
			makeTool("windows-common-cli-contracts", 180, "native-common-cli-acceptance", binary, "--reference", reference, "--output", filepath.Join(out, "windows-common-cli.json")),
			makeTool("windows-owned-integrations", 900, "native-integrations-acceptance", binary, "--output", filepath.Join(out, "windows-integrations.json")),
			makeTool("windows-dpapi-migration", 900, "gcm-acceptance", binary, "--helper", stringValue(win, "gcm"), "--native-distribution"),
			ps("windows-update-contracts", "tests/updater_acceptance.ps1", 900),
			makeTool("windows-read-thread-contracts", 240, "native-read-threads-acceptance", binary, "--output", filepath.Join(out, "windows-read-threads.json")),
			ps("windows-bootstrap-contracts", "tests/bootstrap_acceptance.ps1", 120, "-PackagePath", stringValue(win, "msi_machine"), "-Output", filepath.Join(out, "windows-bootstrap.json")),
			makeTool("windows-game-lfs-contracts", 900, "native-game-project-acceptance", binary, "--output", filepath.Join(out, "windows-game-lfs.json")),
		}
		if waiver == nil {
			cases = append(cases, makeTool("terminal-rust-contracts", 3600, "terminal-product-acceptance", "--source", terminalSource, "--distribution", runtimeRoot, "--output", filepath.Join(out, "terminal-rust.json")))
		}
		desktop := makeTool("terminal-native-desktop", 900, "terminal-desktop-acceptance", "--binary", filepath.Join(runtimeRoot, "bin/git-terminal.exe"), "--git", filepath.Join(runtimeRoot, "bin/git.exe"), "--output", filepath.Join(out, "terminal-desktop.json"))
		for _, target := range []string{"linux-x64", "linux-arm64", "macos-arm64"} {
			item := object(platforms, target)
			archive := stringValue(item, "archive")
			cases = append(cases, makeTool(target, 4500, "native-docker-batch", "--platform", target, "--archive", archive, "--sha256", stringValue(object(matrix, "files"), archive), "--base-image", stringValue(item, "base_image"), "--output", filepath.Join(out, target)))
		}
		cases = append(cases, makeTool("docker-login-migration", 2400, "migration-host", "--native-package", stringValue(object(platforms, "linux-x64"), "archive"), "--output", filepath.Join(out, "docker-login-migration.json")))
		stampReuse := func(values record) {
			for _, raw := range values {
				item := raw.(map[string]any)
				stamp := roundedSeconds(time.Since(startedBatch))
				item["started_seconds_from_batch"], item["finished_seconds_from_batch"] = stamp, stamp
				results = append(results, item)
			}
		}
		if waiver != nil {
			for _, name := range keys(terminalWaiverCases) {
				item := waiver.result(name)
				stamp := roundedSeconds(time.Since(startedBatch))
				item["started_seconds_from_batch"], item["finished_seconds_from_batch"] = stamp, stamp
				results = append(results, item)
			}
		} else if len(reusedDesktop) > 0 {
			stampReuse(reusedDesktop)
		} else {
			results = append(results, run(desktop))
		}
		raceScope, e = scopeID()
		if e != nil {
			return e
		}
		raceContainer = "git-next-updater-race-" + raceScope
		raceArgs := []string{"docker", "run", "--rm", "--init", "--name", raceContainer, "--label", "git-next.owner=git-next-native-product-batch", "--label", "git-next.scope=" + raceScope, "--network", "none", "--read-only", "--pids-limit", "64", "--memory", "2g", "--cpus", "4", "--tmpfs", "/tmp:rw,exec,size=1024m", "--mount", "type=bind,source=" + stringValue(matrix, "race_binary") + ",target=/updater-tests,readonly", "-e", "HOME=/tmp/home", "-e", "GOCACHE=/tmp/go-cache", "-e", "GOTOOLCHAIN=local", "-e", "GOPATH=/tmp/go", stringValue(matrix, "go_race_image"), "/updater-tests", "-test.v", "-test.count=1"}
		cases = append(cases, nativeCase{"posix-updater-race-contracts", raceArgs, 900 * time.Second})
		defer removeDockerOwned(a, "container", raceContainer, "git-next-native-product-batch", raceScope)
		jobs := make(chan nativeCase)
		done := make(chan record, len(cases))
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for c := range jobs {
					done <- run(c)
				}
			}()
		}
		for _, c := range cases {
			jobs <- c
		}
		close(jobs)
		wg.Wait()
		close(done)
		for item := range done {
			results = append(results, item)
		}
		if cleanupErr := removeDockerOwned(a, "container", raceContainer, "git-next-native-product-batch", raceScope); cleanupErr != nil {
			for _, item := range results {
				if item["case"] == "posix-updater-race-contracts" {
					item["status"] = "fail"
					item["reason"] = "owned race fixture cleanup failed"
				}
			}
		}
		for _, scope := range []string{"user", "machine"} {
			name := "msi-" + scope
			if raw := reusedMSI[name]; raw != nil {
				stampReuse(record{name: raw})
				continue
			}
			script := "tests/msi_acceptance.ps1"
			if scope == "machine" {
				script = "tests/elevated_msi_acceptance.ps1"
			}
			args := []string{"-Package", stringValue(win, "base_msi_"+scope), "-UpgradePackage", stringValue(win, "msi_"+scope)}
			if scope == "machine" {
				args = append(args, "-Output", filepath.Join(out, "msi-machine-elevated.log"))
			}
			results = append(results, run(ps(name, script, 1800, args...)))
		}
	}
	unchanged := nativeMatrixReadback(a, matrix) == nil
	hash, e := digest(o.get("native-packages"))
	unchanged = unchanged && e == nil && hash == identity
	hash, e = digest(reference)
	unchanged = unchanged && e == nil && hash == referenceSHA
	unchanged = unchanged && waiver.unchanged() == nil
	if correctnessSHA != "" {
		unchanged = unchanged && requireHash(o.get("correctness-receipt"), correctnessSHA) == nil
	}
	readbackStatus := "fail"
	if unchanged {
		readbackStatus = "pass"
	}
	results = append(results, record{"case": "frozen-input-readback", "status": readbackStatus})
	status := nativeBatchStatus(results, waiver, o.yes("performance"))
	elapsed := roundedSeconds(time.Since(startedBatch))
	phase := "correctness"
	effectiveWorkers := workers
	var localPairs, networkPairs any
	if o.yes("performance") {
		phase = "performance"
		effectiveWorkers = 1
		localPairs = runs
		networkPairs = networkRuns
	}
	reusedScopes := []string{}
	for s := range reusedMSI {
		reusedScopes = append(reusedScopes, s)
	}
	sort.Strings(reusedScopes)
	report := record{"schema_version": 1, "status": status, "phase": phase, "matrix_sha256": identity, "reference_sha256": referenceSHA, "source_tree_sha256": matrix["source_tree_sha256"], "results": results, "release_eligible": false, "native_mac_product_executed": object(platforms, "macos-arm64")["native_executed"] == true, "personal_account_accessed": false, "elapsed_seconds": elapsed, "wall_clock_target_seconds": target, "wall_clock_target_met": elapsed < target, "workers": effectiveWorkers, "msi_transactions_serialized": true, "local_pairs_per_command": localPairs, "network_pairs_per_operation": networkPairs, "checks_skipped_to_meet_time_target": false, "msi_scopes_reusing_unchanged_passed_evidence": reusedScopes, "desktop_reusing_unchanged_passed_evidence": len(reusedDesktop) > 0, "msi_transactions_after_parallel_functional_lanes": !o.yes("performance")}
	if waiver != nil {
		report["terminal_waiver"] = waiver.proof()
	}
	if correctnessSHA != "" {
		report["correctness_sha256"] = correctnessSHA
	}
	if !o.yes("performance") {
		first, last := math.MaxFloat64, 0.0
		msiSeconds := 0.0
		for _, item := range results {
			switch item["case"] {
			case "msi-user", "msi-machine":
				msiSeconds += floatValue(item, "seconds")
			case "frozen-input-readback":
			default:
				first = math.Min(first, floatValue(item, "started_seconds_from_batch"))
				last = math.Max(last, floatValue(item, "finished_seconds_from_batch"))
			}
		}
		report["parallel_functional_elapsed_seconds"] = math.Round((last-first)*1000) / 1000
		report["parallel_functional_target_met"] = last-first < target
		report["serialized_msi_lifecycle_seconds"] = msiSeconds
	}
	if e = writeNewJSON(filepath.Join(out, "batch.json"), report); e != nil {
		return e
	}
	if e = a.json(report); e != nil {
		return e
	}
	if status != "pass" && status != "pass-with-user-waiver" {
		return fmt.Errorf("native product batch failed; see batch.json")
	}
	return nil
}
func floatValue(m record, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func nativeDockerBatch(a *app, o options) (returned error) {
	if e := o.require("platform", "archive", "sha256", "base-image", "output"); e != nil {
		return e
	}
	target := o.get("platform")
	if target != "linux-x64" && target != "linux-arm64" && target != "macos-arm64" {
		return fmt.Errorf("invalid package target")
	}
	archive, e := absolute(o.get("archive"))
	if e != nil {
		return e
	}
	if e = requireHash(archive, o.get("sha256")); e != nil {
		return e
	}
	out, e := newDestination(o.get("output"))
	if e != nil {
		return e
	}
	scope, e := scopeID()
	if e != nil {
		return e
	}
	image, container := "git-next-native-batch-"+scope, "git-next-native-check-"+scope
	owner := "git-next-native-product-batch"
	mac := target == "macos-arm64"
	dockerfile := a.path("tests/containers/native.Dockerfile")
	arch, toolTarget := "amd64", "linux-x64"
	if mac {
		dockerfile = a.path("tests/containers/Dockerfile_mac")
	}
	if target == "linux-arm64" {
		arch, toolTarget = "arm64", "linux-arm64"
	}
	tool := filepath.Join(out, "harness-tool")
	if e = buildToolFor(a, toolTarget, tool, o.value("go", "go")); e != nil {
		return e
	}
	status := "failed"
	cleanup := record{}
	defer func() {
		for _, resource := range []struct{ kind, name string }{{"container", container}, {"image", image}} {
			if e := removeDockerOwned(a, resource.kind, resource.name, owner, scope); e != nil && returned == nil {
				returned = e
			}
			cleanup[resource.kind+"_absent"] = dockerAbsent(a, resource.kind, resource.name)
		}
		packageRoot := filepath.Join(out, "package")
		if exists(packageRoot) {
			info, e := os.Lstat(packageRoot)
			if e != nil || !inside(out, packageRoot) || info.Mode()&os.ModeSymlink != 0 {
				if returned == nil {
					returned = fmt.Errorf("unsafe package review cleanup")
				}
			} else if e = os.RemoveAll(packageRoot); e != nil && returned == nil {
				returned = e
			}
		}
		var macExecuted any
		if mac {
			macExecuted = false
		}
		report := record{"schema_version": 1, "status": status, "platform": target, "archive_sha256": o.get("sha256"), "cleanup": cleanup, "macos_product_executed": macExecuted, "personal_profiles_mounted": false}
		if e := writeNewJSON(filepath.Join(out, "docker.json"), report); e != nil && returned == nil {
			returned = e
		}
		_ = a.json(report)
	}()
	labels := []string{"--label", "git-next.owner=" + owner, "--label", "git-next.scope=" + scope}
	if !dockerAbsent(a, "image", image) {
		return fmt.Errorf("Docker image already exists")
	}
	build := append([]string{"build", "--platform", "linux/" + arch}, labels...)
	build = append(build, "--build-arg", "BASE="+o.get("base-image"), "-f", dockerfile, "-t", image, filepath.Dir(dockerfile))
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Second)
	r, e := runProcess(ctx, a.root, nil, nil, "docker", build...)
	cancel()
	_ = write(filepath.Join(out, "image-build.log"), append(append(r.stdout, '\n'), r.stderr...), 0644)
	if e != nil {
		return e
	}
	if r.code != 0 {
		return fmt.Errorf("Docker acceptance image build failed")
	}
	args := append([]string{"run", "--rm", "--init", "--name", container}, labels...)
	args = append(args, "--platform", "linux/"+arch, "--network", "none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit", "128", "--memory", "2g", "--cpus", "4", "--tmpfs", "/tmp:rw,exec,size=1536m", "--env", "GIT_NEXT_ROOT=/repo", "--mount", "type=bind,source="+out+",target=/results", "--mount", "type=bind,source="+a.root+",target=/repo,readonly", "--mount", "type=bind,source="+a.path("tests")+",target=/tests,readonly", "--mount", "type=bind,source="+a.path("tests/containers")+",target=/harness,readonly", "--mount", "type=bind,source="+tool+",target=/usr/local/bin/git-next-tool,readonly", "--mount", "type=bind,source="+archive+",target=/inputs/package.tar.gz,readonly", image)
	if mac {
		args = append(args, "--archive", "/inputs/package.tar.gz", "--output", "/results/macos-package-static.json")
	}
	timeout := 1800 * time.Second
	if arch == "arm64" {
		timeout = 3600 * time.Second
	}
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	r, e = runProcess(ctx, a.root, nil, nil, "docker", args...)
	cancel()
	_ = write(filepath.Join(out, "container.log"), append(append(r.stdout, '\n'), r.stderr...), 0644)
	if e != nil {
		return e
	}
	if r.code != 0 {
		return fmt.Errorf("native Docker acceptance exited %d", r.code)
	}
	status = "pass"
	return nil
}
