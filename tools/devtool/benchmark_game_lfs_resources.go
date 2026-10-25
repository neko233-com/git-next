// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func gameResourceLFSPlatform(goos string) error {
	if goos != "windows" && goos != "linux" {
		return fmt.Errorf("complete LFS resource phases require Windows Job containment or Linux /proc owned-session enumeration; this platform cannot safely reclaim nested descendant groups")
	}
	return nil
}

func gameResourceValidateLFS(child record, binaryHash, configHash string, assets, assetMiB int) error {
	server := object(child, "server")
	if number(child, "schema_version") != 1 || child["status"] != "pass" || child["binary_sha256"] != binaryHash || child["system_gitconfig_sha256"] != configHash || number(child, "projects") != 2 || number(child, "assets_per_project") != int64(assets) || number(child, "asset_mib") != int64(assetMiB) || server["objects_buffered_in_memory"] != false || server["active_transfers"] == nil || number(server, "active_transfers") != 0 || number(server, "maximum_active_transfers") < 1 || number(server, "transferred_bytes") < 1 || number(server, "objects") != int64(2*assets+1) || child["terminal_executed"] != false {
		return fmt.Errorf("streamed acceptance identity/complete fixture binding failed")
	}
	expected := map[string]bool{
		"two-project-streamed-large-lfs-fixture-setup":                   false,
		"parallel-unity-unreal-large-lfs-upload-clone-and-sha":           false,
		"corrupt-large-download-never-publishes-valid-cache-and-retries": false,
		"rejected-large-upload-preserves-remote-ref-and-recovers":        false,
		"no-active-fixture-transfers-after-commands":                     false,
	}
	for _, value := range array(child, "results") {
		row := rec(value)
		name := stringValue(row, "case")
		if _, ok := expected[name]; !ok || expected[name] || row["status"] != "pass" {
			return fmt.Errorf("unexpected/duplicate/failed streamed acceptance case")
		}
		expected[name] = true
	}
	for _, seen := range expected {
		if !seen {
			return fmt.Errorf("partial streamed report omitted a required failure/recovery case")
		}
	}
	return nil
}

func benchmarkGameLFSResources(a *app, o options) (returned error) {
	if e := o.require("output", "baseline-config", "candidate-config"); e != nil {
		return e
	}
	if e := gameResourceLFSHostPreflight(); e != nil {
		return e
	}
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	samples, e := o.integer("samples", 2)
	if e != nil || samples < 2 || samples > 10 {
		return fmt.Errorf("--samples must be 2..10 (AB then BA at minimum)")
	}
	assets, e := o.integer("assets", 8)
	if e != nil || assets < 2 || assets > 16 {
		return fmt.Errorf("--assets must be 2..16 per project")
	}
	assetMiB, e := o.integer("asset-mib", 16)
	if e != nil || assetMiB < 1 || assetMiB > 256 {
		return fmt.Errorf("--asset-mib must be 1..256")
	}
	seconds, e := o.integer("phase-timeout", 900)
	if e != nil || seconds < 1 || seconds > 7200 {
		return fmt.Errorf("--phase-timeout must be 1..7200")
	}
	memoryMiB, e := o.integer("job-memory-mib", 2048)
	if e != nil || memoryMiB < 32 || memoryMiB > 32768 {
		return fmt.Errorf("--job-memory-mib must be 32..32768")
	}
	output, e := filepath.Abs(o.get("output"))
	if e != nil {
		return e
	}
	if _, e = os.Lstat(output); !os.IsNotExist(e) {
		return fmt.Errorf("output must be a new file")
	}
	if e = os.MkdirAll(filepath.Dir(output), 0700); e != nil {
		return e
	}
	work := output + ".fixtures"
	if e = os.Mkdir(work, 0700); e != nil {
		return e
	}
	start := time.Now()
	report := record{"schema_version": 1, "status": "running", "scope": "Whole-controller plus fixture plus Git/LFS resource comparison of complete two-project streamed acceptance, including corrupt-download and rejected-upload detection/recovery", "projects": 2, "assets_per_project": assets, "asset_mib": assetMiB, "samples_per_profile": samples, "phase_timeout_seconds": seconds, "fixtures_retained_on_failure": true, "fixture_directory_basename": filepath.Base(work), "terminal_executed": false, "personal_configuration_accessed": false, "memory_scope": "Windows whole-job peak commit, not RSS; Linux sampled owned-process-group sum RSS; macOS unavailable; no total-RSS or universal-speedup promise"}
	phases := []gameResourcePhase{}
	defer func() {
		report["status"], report["comparison_eligible"] = "pass", returned == nil
		report["timing_comparison_eligible"] = returned == nil
		memoryEligible := returned == nil && len(phases) > 0
		for _, phase := range phases {
			memoryEligible = memoryEligible && phase.Memory.Available && phase.Memory.PeakBytes != nil
		}
		report["memory_comparison_eligible"] = memoryEligible
		report["parent_benchmark_controller_accounted"] = false
		report["child_controller_fixture_git_and_lfs_in_workload_peak"] = true
		if returned != nil {
			report["status"], report["error"] = "fail", returned.Error()
		}
		report["seconds"], report["phases"] = time.Since(start).Seconds(), phases
		returned = errors.Join(returned, writeNewJSON(output, report))
	}()
	argv := toolCommand("native-game-lfs-stream-acceptance")
	controller := argv[0]
	identity, inputs := record{}, map[string]string{"binary": binary, "controller": controller}
	for key, value := range map[string]string{"binary": binary, "controller": controller, "baseline": o.get("baseline-config"), "candidate": o.get("candidate-config")} {
		absolute, e := filepath.Abs(value)
		if e != nil {
			return e
		}
		h, e := digest(absolute)
		if e != nil {
			return e
		}
		identity[key+"_sha256"], inputs[key] = h, absolute
	}
	report["input_identities"] = identity
	home := filepath.Join(work, "home")
	if e = os.Mkdir(home, 0700); e != nil {
		return e
	}
	empty := filepath.Join(work, "empty-path-and-hooks")
	if e = os.Mkdir(empty, 0700); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"PATH": empty, "TMPDIR": home, "TMP": home, "TEMP": home, "GIT_NEXT_ROOT": a.root})
	snapshots := map[string]string{}
	for _, name := range []string{"baseline", "candidate"} {
		info, e := os.Lstat(inputs[name])
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("configuration input must be a regular file no larger than 1 MiB")
		}
		data, e := os.ReadFile(inputs[name])
		if e != nil {
			return e
		}
		if digestBytes(data) != identity[name+"_sha256"] {
			return fmt.Errorf("%s config changed before snapshot", name)
		}
		snapshots[name] = filepath.Join(home, name+".gitconfig")
		if e = write(snapshots[name], data, 0600); e != nil {
			return e
		}
		parsed, e := gameResourceSetupRun(binary, work, empty, env, gameResourceLimits{processes: 128, memory: uint64(memoryMiB) << 20, timeout: time.Duration(seconds) * time.Second}, "config", "--file", snapshots[name], "--no-includes", "--list", "-z")
		if e != nil {
			return e
		}
		if e = gameResourceValidateConfigListing(parsed); e != nil {
			return e
		}
		inputs[name+"_snapshot"], identity[name+"_snapshot_sha256"] = snapshots[name], identity[name+"_sha256"]
	}
	failed := false
	for sample := 1; sample <= samples; sample++ {
		order := []string{"baseline", "candidate"}
		if sample%2 == 0 {
			order[0], order[1] = order[1], order[0]
		}
		for _, name := range order {
			childOutput := filepath.Join(work, fmt.Sprintf("%s-%02d.json", name, sample))
			command := toolCommand("native-game-lfs-stream-acceptance", binary, "--output", childOutput, "--gitconfig", snapshots[name], "--asset-mib", strconv.Itoa(assetMiB), "--assets", strconv.Itoa(assets))
			spec := gameResourceCommand{name: "complete-two-project-lfs-acceptance", exe: command[0], args: command[1:], cwd: a.root, env: env, prefix: 16 << 10}
			p := gameResourceRunPhase(context.Background(), []gameResourceCommand{spec}, false, gameResourceLimits{processes: 128, memory: uint64(memoryMiB) << 20, timeout: time.Duration(seconds) * time.Second})
			p.Profile, p.Workload, p.Mode, p.Sample = name, "complete-lfs-stream-acceptance", "two-project-parallel-with-failure-recovery", sample
			child, e := load(childOutput)
			if e != nil {
				p.Status, p.Error = "fail", "child acceptance report unavailable; real failure retained"
			} else {
				server := object(child, "server")
				if e := gameResourceValidateLFS(child, fmt.Sprint(identity["binary_sha256"]), fmt.Sprint(identity[name+"_sha256"]), assets, assetMiB); e != nil {
					p.Status, p.Error = "fail", e.Error()
				}
				h, _ := digest(childOutput)
				p.Integrity = record{"status": child["status"], "acceptance_basename": filepath.Base(childOutput), "acceptance_sha256": h, "complete_child_results": child["results"], "server": server, "includes_failed_transfer_detection_and_recovery": true, "transfer_throughput_is_not_success_only": true}
				if p.Status == "pass" && p.Seconds > 0 {
					throughput := float64(number(server, "transferred_bytes")) / (1 << 20) / p.Seconds
					p.LogicalMiBPerSecond = &throughput
				}
			}
			p.ValidTimingSample = p.Status == "pass"
			failed = failed || !p.ValidTimingSample
			phases = append(phases, p)
			fmt.Fprintf(a.out, "LFS resources %-9s sample=%d %s %.3fs\n", name, sample, p.Status, p.Seconds)
		}
	}
	for name, path := range inputs {
		h, e := digest(path)
		if e != nil || h != identity[name+"_sha256"] {
			return fmt.Errorf("%s input changed during comparison", name)
		}
	}
	report["input_before_after_readback"] = "pass"
	if failed {
		return fmt.Errorf("LFS resource phases failed; failed/budget-limited/deadline phases are not valid timing samples")
	}
	return nil
}

func init() {
	register("benchmark-game-lfs-resources", "Measure full streamed LFS acceptance process trees in AB/BA order", "output baseline-config candidate-config samples assets asset-mib phase-timeout job-memory-mib", benchmarkGameLFSResources)
}
