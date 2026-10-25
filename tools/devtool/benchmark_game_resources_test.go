// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGameResourceSubprocessFixture(t *testing.T) {
	mode := os.Getenv("GIT_NEXT_GAME_RESOURCE_HELPER")
	if mode == "" {
		t.Skip("controlled subprocess fixture")
	}
	switch mode {
	case "exit-one":
		os.Exit(1)
	case "hang":
		time.Sleep(30 * time.Second)
	case "orphan":
		cmd := exec.Command(os.Args[0], "-test.run=^TestGameResourceSubprocessFixture$")
		cmd.Env = fixtureEnv(os.Environ(), map[string]string{"GIT_NEXT_GAME_RESOURCE_HELPER": "hang"})
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if e := cmd.Start(); e != nil {
			os.Exit(3)
		}
		fmt.Fprintf(os.Stdout, "%d\n", cmd.Process.Pid)
	case "memory", "memory-large":
		size := 8 << 20
		if mode == "memory-large" {
			size = 32 << 20
		}
		memory := make([]byte, size)
		for i := 0; i < len(memory); i += 4096 {
			memory[i] = byte(i + 1)
		}
		time.Sleep(220 * time.Millisecond)
		runtime.KeepAlive(memory)
	case "nested-group":
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestGameResourceSubprocessFixture$")
		cmd.Env = fixtureEnv(os.Environ(), map[string]string{"GIT_NEXT_GAME_RESOURCE_HELPER": "memory-large"})
		cleanup, e := prepareProcess(cmd)
		if e != nil {
			os.Exit(4)
		}
		if e = cmd.Start(); e == nil {
			e = attachProcess(cmd)
		}
		if e == nil {
			e = cmd.Wait()
		}
		cleanup()
		if e != nil {
			os.Exit(5)
		}
	case "over-budget":
		memory := make([]byte, 96<<20)
		for i := range memory {
			memory[i] = byte(i + 1)
		}
		time.Sleep(100 * time.Millisecond)
		runtime.KeepAlive(memory)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func gameResourceTestSpec(mode string) gameResourceCommand {
	return gameResourceCommand{name: mode, exe: os.Args[0], args: []string{"-test.run=^TestGameResourceSubprocessFixture$"}, env: fixtureEnv(os.Environ(), map[string]string{"GIT_NEXT_GAME_RESOURCE_HELPER": mode}), prefix: 4096}
}

func gameResourceTestLimits() gameResourceLimits {
	return gameResourceLimits{processes: 128, memory: 2 << 30, timeout: 3 * time.Second}
}

func TestGameResourceCaptureBoundsOutputWithoutLosingDigest(t *testing.T) {
	data := bytes.Repeat([]byte("bounded-output"), 10000)
	c := &gameResourceCapture{limit: 17}
	n, e := c.Write(data)
	if e != nil || n != len(data) || c.bytes != int64(len(data)) || len(c.prefix) != 17 || c.digest() != digestBytes(data) {
		t.Fatal("truncation changed output accounting")
	}
}

func TestGameResourceNormalExitOneIsNotDeadline(t *testing.T) {
	p := gameResourceRunPhase(context.Background(), []gameResourceCommand{gameResourceTestSpec("exit-one")}, false, gameResourceTestLimits())
	if p.Status != "fail" || p.ValidTimingSample || len(p.Results) != 1 || p.Results[0].ExitCode != 1 || p.Results[0].TimedOut {
		t.Fatalf("ordinary exit misclassified: %+v", p)
	}
	if !p.Memory.CleanupOK {
		t.Fatal("failed command cleanup was not verified")
	}
}

func TestGameResourceDeadlineKillsOwnedPhase(t *testing.T) {
	limits := gameResourceTestLimits()
	limits.timeout = 150 * time.Millisecond
	start := time.Now()
	p := gameResourceRunPhase(context.Background(), []gameResourceCommand{gameResourceTestSpec("hang")}, false, limits)
	if p.Status != "fail" || p.ValidTimingSample || len(p.Results) != 1 || !p.Results[0].TimedOut || !p.Memory.CleanupOK {
		t.Fatalf("deadline did not fail/clean the phase: %+v", p)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("deadline cleanup took too long")
	}
}

func TestGameResourceOrphanCannotKeepPipesOrSurvivePhase(t *testing.T) {
	p := gameResourceRunPhase(context.Background(), []gameResourceCommand{gameResourceTestSpec("orphan")}, false, gameResourceTestLimits())
	if len(p.Results) != 1 || p.Status != "fail" || !p.Memory.CleanupOK || p.ValidTimingSample {
		t.Fatalf("orphan was not a real failed sample: %+v", p)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(p.Results[0].StdoutPrefix))
	if e != nil || pid <= 0 {
		t.Fatal("fixture descendant PID missing", e)
	}
	if gameResourceTestAlive(pid) {
		t.Fatal("owned descendant survived job/group cleanup")
	}
}

func TestGameResourceSharedPhaseAccountsConcurrentProcesses(t *testing.T) {
	specs := []gameResourceCommand{}
	for i := 0; i < 4; i++ {
		specs = append(specs, gameResourceTestSpec("memory"))
	}
	p := gameResourceRunPhase(context.Background(), specs, true, gameResourceTestLimits())
	if p.Status != "pass" || !p.ValidTimingSample || !p.Memory.CleanupOK {
		t.Fatalf("bounded concurrent phase failed: %+v", p)
	}
	if runtime.GOOS == "windows" {
		if !p.Memory.MemoryLimitEnforced || !p.Memory.ProcessLimitKernel || p.Memory.Metric != "windows_job_peak_commit_bytes" {
			t.Fatal("Windows whole-job commit policy missing")
		}
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		if !p.Memory.Available || p.Memory.PeakBytes == nil || *p.Memory.PeakBytes < 32<<20 {
			t.Fatalf("only one child was accounted or metric missing: %+v", p.Memory)
		}
	} else if p.Memory.Available || p.Memory.PeakBytes != nil {
		t.Fatal("unavailable platform metric was represented as zero/success")
	}
}

func TestGameResourceUnavailableAndFailedMetricsNeverBecomeZeroSamples(t *testing.T) {
	peak := uint64(123)
	phases := []gameResourcePhase{{Status: "fail", Memory: gameResourceMemory{Available: true, PeakBytes: &peak}}, {Status: "pass", Memory: gameResourceMemory{}}, {Status: "pass", Memory: gameResourceMemory{Available: true, PeakBytes: &peak}}}
	if values := gameResourceAvailablePeaks(phases); len(values) != 1 || values[0] != peak {
		t.Fatal("unavailable/failed sample was included", values)
	}
	data, e := json.Marshal(phases[1].Memory)
	if e != nil || !bytes.Contains(data, []byte(`"peak_bytes":null`)) {
		t.Fatal("unavailable metric serialized as a numerical zero")
	}
}

func TestGameResourceNestedRunnerRemainsInsideWholeTreeMetric(t *testing.T) {
	p := gameResourceRunPhase(context.Background(), []gameResourceCommand{gameResourceTestSpec("nested-group")}, false, gameResourceTestLimits())
	if p.Status != "pass" || !p.Memory.CleanupOK {
		t.Fatalf("nested runner failed: %+v", p)
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		if !p.Memory.Available || p.Memory.PeakBytes == nil || *p.Memory.PeakBytes < 32<<20 {
			t.Fatalf("nested job/process group escaped whole-tree accounting: %+v", p.Memory)
		}
	}
}

func TestGameResourceLFSRequiresExactCompleteFailureRecoveryReport(t *testing.T) {
	valid := record{"schema_version": 1, "status": "pass", "binary_sha256": "binary", "system_gitconfig_sha256": "config", "projects": 2, "assets_per_project": 8, "asset_mib": 16, "terminal_executed": false, "server": record{"objects_buffered_in_memory": false, "active_transfers": 0, "maximum_active_transfers": 16, "transferred_bytes": 123456, "objects": 17}, "results": []record{
		{"case": "two-project-streamed-large-lfs-fixture-setup", "status": "pass"},
		{"case": "parallel-unity-unreal-large-lfs-upload-clone-and-sha", "status": "pass"},
		{"case": "corrupt-large-download-never-publishes-valid-cache-and-retries", "status": "pass"},
		{"case": "rejected-large-upload-preserves-remote-ref-and-recovers", "status": "pass"},
		{"case": "no-active-fixture-transfers-after-commands", "status": "pass"},
	}}
	data, e := json.Marshal(valid)
	if e != nil {
		t.Fatal(e)
	}
	copyReport := func() record {
		var r record
		if e := json.Unmarshal(data, &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	if e = gameResourceValidateLFS(copyReport(), "binary", "config", 8, 16); e != nil {
		t.Fatal(e)
	}
	for name, mutate := range map[string]func(record){
		"missing-recovery": func(r record) { r["results"] = array(r, "results")[:2] },
		"duplicate-case":   func(r record) { r["results"] = append(array(r, "results"), array(r, "results")[0]) },
		"failed-recovery":  func(r record) { rec(array(r, "results")[2])["status"] = "fail" },
		"wrong-config":     func(r record) { r["system_gitconfig_sha256"] = "different" },
		"memory-fixture":   func(r record) { object(r, "server")["objects_buffered_in_memory"] = true },
		"missing-drain":    func(r record) { delete(object(r, "server"), "active_transfers") },
	} {
		t.Run(name, func(t *testing.T) {
			r := copyReport()
			mutate(r)
			if e := gameResourceValidateLFS(r, "binary", "config", 8, 16); e == nil {
				t.Fatal("incomplete acceptance became a timing sample")
			}
		})
	}
}

func TestGameResourceConfigCannotReadHostIncludes(t *testing.T) {
	for _, listing := range []string{"include.path\nprivate-file\x00", "includeif.gitdir:repo/.path\nprivate-file\x00"} {
		if e := gameResourceValidateConfigListing(gameResourceResult{StdoutPrefix: listing, StdoutBytes: int64(len(listing))}); e == nil {
			t.Fatal("host include was accepted")
		}
	}
	if e := gameResourceValidateConfigListing(gameResourceResult{StdoutPrefix: "pack.threads\n1\x00", StdoutBytes: 1000}); e == nil {
		t.Fatal("truncated configuration validation was accepted")
	}
}

func TestGameResourceLFSRejectsUnsupportedTreeCleanupBeforeFixtures(t *testing.T) {
	for _, goos := range []string{"darwin", "freebsd", "plan9"} {
		if e := gameResourceLFSPlatform(goos); e == nil {
			t.Fatal("unsupported nested process-tree cleanup accepted", goos)
		}
	}
	for _, goos := range []string{"windows", "linux"} {
		if e := gameResourceLFSPlatform(goos); e != nil {
			t.Fatal(goos, e)
		}
	}
}

func TestGameResourceFixtureCopiesNeverAliasSourceBytes(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "seed"), filepath.Join(root, "candidate")
	if e := os.Mkdir(from, 0700); e != nil {
		t.Fatal(e)
	}
	asset := filepath.Join(from, "asset.bin")
	if e := gameResourceStreamAsset(asset, 65537, 233); e != nil {
		t.Fatal(e)
	}
	before, e := gameResourceFileDigest(asset)
	if e != nil {
		t.Fatal(e)
	}
	if e = gameResourceCopyTree(from, to); e != nil {
		t.Fatal(e)
	}
	one, _ := os.Stat(asset)
	two, _ := os.Stat(filepath.Join(to, "asset.bin"))
	if os.SameFile(one, two) {
		t.Fatal("paired fixtures share a mutable inode")
	}
	if e = os.WriteFile(filepath.Join(to, "asset.bin"), []byte("candidate change"), 0600); e != nil {
		t.Fatal(e)
	}
	after, e := gameResourceFileDigest(asset)
	if e != nil || before != after {
		t.Fatal("candidate mutation changed baseline seed")
	}
}

func TestGameResourcePackStorageRetainsCompressedAndAuxiliaryByteCost(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, ".git", "objects", "pack")
	if e := os.MkdirAll(packDir, 0700); e != nil {
		t.Fatal(e)
	}
	for name, size := range map[string]int{"pack-test.pack": 101, "pack-test.idx": 17} {
		if e := os.WriteFile(filepath.Join(packDir, name), make([]byte, size), 0600); e != nil {
			t.Fatal(e)
		}
	}
	external := filepath.Join(t.TempDir(), "external")
	if e := os.WriteFile(external+"-abc.pack", make([]byte, 203), 0600); e != nil {
		t.Fatal(e)
	}
	storage, e := gameResourcePackStorage([]gameResourceFixture{{name: "fixture", root: root}}, []string{external})
	if e != nil || number(storage, "repository_pack_bytes") != 101 || number(storage, "repository_auxiliary_bytes") != 17 || number(object(storage, "external_pack_objects"), "pack_bytes") != 203 || storage["measurement_outside_timed_process_phase"] != true || storage["loose_object_bytes_included"] != false {
		t.Fatal("compressed storage cost was lost or misclassified", storage, e)
	}
}
