// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGameMultiBlobUsesCanonicalGitObjectIdentity(t *testing.T) {
	for input, expected := range map[string]string{"": "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", "hello\n": "ce013625030ba8dba906f756967f9e9ca394464a"} {
		if actual := gameMultiBlob([]byte(input)); actual != expected {
			t.Fatalf("blob identity %q: %s", input, actual)
		}
	}
}

func TestGameMultiEntriesRejectDuplicateAndUnmergedRecords(t *testing.T) {
	const oid = "ce013625030ba8dba906f756967f9e9ca394464a"
	for _, tree := range []bool{false, true} {
		header := "100644 " + oid + " 0\t"
		if tree {
			header = "100644 blob " + oid + "\t"
		}
		valid := header + "Assets/one.cs\x00" + header + "Build/Required.ini\x00"
		if entries := gameMultiParseEntries([]byte(valid), tree); len(entries) != 2 || entries["Build/Required.ini"] != oid {
			t.Fatal("lost canonical game paths")
		}
		duplicate := acceptCapture("duplicate", func() { gameMultiParseEntries([]byte(header+"same\x00"+header+"same\x00"), tree) })
		if duplicate["status"] != "fail" {
			t.Fatal("duplicate member accepted")
		}
	}
	for _, data := range []string{"100644 " + oid + " 2\tfile\x00", "100755 " + oid + " 0\tfile\x00", "broken\x00"} {
		result := acceptCapture("invalid", func() { gameMultiParseEntries([]byte(data), false) })
		if result["status"] != "fail" {
			t.Fatal("noncanonical generated index accepted")
		}
	}
}

func TestGameMultiTraceTracksRealProcessIntervalsAndMaintenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	data := []byte("{\"event\":\"start\",\"sid\":\"root\",\"time\":\"2026-10-25T00:00:00.000001Z\"}\n" +
		"{\"event\":\"child_start\",\"argv\":[\"C:\\\\owned\\\\git.exe\",\"maintenance\",\"run\",\"--auto\"]}\n" +
		"{\"event\":\"child_start\",\"argv\":[\"git-upload-pack\",\"fixture\"]}\n" +
		"{\"event\":\"exit\",\"sid\":\"child\",\"time\":\"2026-10-25T00:00:01Z\"}\n" +
		"{\"event\":\"exit\",\"sid\":\"root\",\"time\":\"2026-10-25T00:00:02Z\"}\n")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	value := gameMultiReadTrace(path)
	if value.events != 5 || value.maintenance != 1 || value.sha256 != digestBytes(data) || value.root.end.Sub(value.root.start) != 2*time.Second-time.Microsecond {
		t.Fatalf("wrong process evidence: %+v", value)
	}
	if e := os.WriteFile(path, []byte("{\"event\":\"start\",\"sid\":\"root\",\"time\":\"2026-10-25T00:00:00Z\"}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	result := acceptCapture("missing-exit", func() { gameMultiReadTrace(path) })
	if result["status"] != "fail" {
		t.Fatal("incomplete process interval accepted")
	}
}

func TestGameMultiOverlapRequiresSimultaneousRootProcesses(t *testing.T) {
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	interval := func(a, b int) gameMultiInterval {
		return gameMultiInterval{start.Add(time.Duration(a) * time.Second), start.Add(time.Duration(b) * time.Second)}
	}
	if gameMultiPeak([]gameMultiInterval{interval(0, 1), interval(1, 2)}) != 1 {
		t.Fatal("adjacent serialized commands counted as parallel")
	}
	if gameMultiPeak([]gameMultiInterval{interval(0, 3), interval(1, 4), interval(2, 5), interval(6, 7)}) != 3 {
		t.Fatal("actual process overlap miscounted")
	}
}

func TestGameMultiWorkerLimitAndFailureRemainObservable(t *testing.T) {
	repos := make([]*gameMultiRepo, 12)
	for i := range repos {
		repos[i] = &gameMultiRepo{id: string(rune('a' + i))}
	}
	var active, peak atomic.Int32
	summary, cases := gameMultiParallel(repos, "fixture", func(repo *gameMultiRepo) gameMultiTrace {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		start := time.Now()
		time.Sleep(10 * time.Millisecond)
		acceptCheck(repo.id != "f", "intentional fixture failure")
		return gameMultiTrace{root: gameMultiInterval{start, time.Now()}}
	})
	if active.Load() != 0 || peak.Load() > 4 || peak.Load() < 2 {
		t.Fatalf("unsafe worker lifetime: active=%d peak=%d", active.Load(), peak.Load())
	}
	if summary["status"] != "fail" || len(cases) != 12 || cases[5]["status"] != "fail" {
		t.Fatal("parallel operation failure lost")
	}
}

func TestGameMultiRefusesUnsafeScaleAndExistingOutputBeforeExecution(t *testing.T) {
	root := t.TempDir()
	binary, output := filepath.Join(root, "git"), filepath.Join(root, "output.json")
	if e := os.WriteFile(binary, []byte("not executable"), 0600); e != nil {
		t.Fatal(e)
	}
	a := &app{root: root, out: io.Discard, err: io.Discard}
	for key, value := range map[string]string{"files": "100001", "projects": "1", "command-timeout": "601"} {
		o := options{args: []string{binary}, strings: map[string]*string{"output": &output, key: &value}}
		if e := nativeGameMultirepo(a, o); e == nil {
			t.Fatalf("unbounded input accepted: %s", key)
		}
	}
	if e := os.WriteFile(output, []byte("retained evidence"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := nativeGameMultirepo(a, options{args: []string{binary}, strings: map[string]*string{"output": &output}}); e == nil {
		t.Fatal("existing evidence accepted")
	}
	if actual, _ := os.ReadFile(output); string(actual) != "retained evidence" {
		t.Fatal("existing evidence was changed")
	}
}

func TestGameMultiRunnerDistinguishesDeadlineAndOrdinaryExit(t *testing.T) {
	root := t.TempDir()
	fixture := &gameMultiCLI{cliFixture: &cliFixture{root: root, repo: root, binary: os.Args[0], env: append(os.Environ(), "GIT_NEXT_GO_FIXTURE=hang")}, timeout: 150 * time.Millisecond}
	result := acceptCapture("deadline", func() { fixture.G("-test.run=^TestProcessFixture$") })
	if result["status"] != "fail" || len(fixture.history) != 1 || fixture.history[0]["classification"] != "deadline-exceeded" {
		t.Fatal("deadline not retained in command diagnostics")
	}
	fixture.env = append(os.Environ(), "GIT_NEXT_MEASURED_PROCESS_FIXTURE=exit-one")
	fixture.timeout = 5 * time.Second
	fixture.Bad("-test.run=^TestMeasuredProcessFixture$")
	if fixture.history[1]["classification"] != "ordinary-exit" || number(fixture.history[1], "exit_code") != 1 {
		t.Fatal("ordinary exit1 confused with timeout")
	}
}

func TestGameMultiLargeMonorepoAddDiagnostic(t *testing.T) {
	binary, config, output := os.Getenv("GIT_NEXT_GAME_ADD_DIAGNOSTIC_BINARY"), os.Getenv("GIT_NEXT_GAME_ADD_DIAGNOSTIC_CONFIG"), os.Getenv("GIT_NEXT_GAME_ADD_DIAGNOSTIC_OUTPUT")
	if binary == "" || config == "" || output == "" {
		t.Skip("explicit large-add diagnostic inputs required")
	}
	if exists(output) || exists(output+".private.json") {
		t.Fatal("diagnostic output must be new")
	}
	checkout, e := findRoot(".")
	if e != nil {
		t.Fatal(e)
	}
	root, e := os.MkdirTemp("", "git-next-game-add-diagnostic-")
	if e != nil {
		t.Fatal(e)
	}
	if e = writeNewJSON(output+".private.json", record{"fixture_root": root, "trace_directory": filepath.Join(root, "traces"), "scope": "owned generated monorepo retained for add diagnosis; no personal data"}); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	profiles := map[string][]byte{}
	for _, name := range []string{"unity.gitignore", "unity.gitattributes", "unreal.gitignore", "unreal.gitattributes"} {
		profiles[name] = acceptBytes(filepath.Join(checkout, "docs/game-profiles", name))
	}
	f := &gameMultiCLI{cliFixture: &cliFixture{root: root, repo: filepath.Join(root, "dual-engine-monorepo"), binary: binary, env: gameMultiEnvironment(root, binary, config)}, timeout: 90 * time.Second, phase: "diagnostic-setup"}
	repo := &gameMultiRepo{id: "dual-engine-monorepo", engine: "monorepo", files: map[string][]byte{}, fixture: f}
	var attempts []record
	var materializeMS float64
	initialObjects := record{}
	binaryHash, configHash := acceptHash(binary), acceptHash(config)
	counts := func() record {
		var loose, packs, size int64
		e := filepath.WalkDir(filepath.Join(f.repo, ".git/objects"), func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			size += info.Size()
			if strings.HasSuffix(path, ".pack") {
				packs++
			}
			if len(entry.Name()) == 38 && len(filepath.Base(filepath.Dir(path))) == 2 {
				loose++
			}
			return nil
		})
		acceptMust(e)
		return record{"loose_objects": loose, "pack_files": packs, "object_file_bytes": size}
	}
	var settings string
	verified := false
	result := acceptCapture("same-shape-monorepo-20000-add", func() {
		f.At(root, "init", "-q", "--object-format=sha1", "-b", "main", f.repo)
		f.G("config", "--local", "fixture.project", repo.id)
		settings = string(f.G("config", "--list", "-z"))
		materializeStart := time.Now()
		repo.project("UnityProject", "unity", 10000, profiles)
		repo.project("UnrealProject", "unreal", 10000, profiles)
		repo.put("Library/keep-outside-unity.txt", []byte("monorepo root data\n"))
		repo.put("Binaries/keep-outside-unreal.txt", []byte("monorepo root data\n"))
		repo.put("UnityProject/Binaries/keep-other-engine.txt", []byte("Unity project data\n"))
		repo.put("UnrealProject/Library/keep-other-engine.txt", []byte("Unreal project data\n"))
		materializeMS = float64(time.Since(materializeStart).Nanoseconds()) / 1e6
		initialObjects = counts()
		f.phase = "diagnostic-add-90"
		first := acceptCapture("first-add", func() { f.G("add", "-A") })
		row := f.history[len(f.history)-1]
		row["objects_after_attempt"] = counts()
		attempts = append(attempts, row)
		if first["status"] != "pass" {
			acceptCheck(row["classification"] == "deadline-exceeded", "nondeadline add failure; no retry: %v", first)
			lock := filepath.Join(f.repo, ".git/index.lock")
			if info, e := os.Lstat(lock); e == nil {
				acceptCheck(info.Mode().IsRegular(), "unexpected nonregular owned diagnostic index lock")
				data := acceptBytes(lock)
				preserved := output + ".interrupted-index.lock"
				file, e := os.OpenFile(preserved, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				acceptMust(e)
				_, e = file.Write(data)
				acceptMust(e)
				acceptMust(file.Close())
				acceptEqual(acceptBytes(lock), data, "owned interrupted lock changed before removal")
				acceptMust(os.Remove(lock))
				row["interrupted_owned_lock_preserved_sha256"] = digestBytes(data)
			} else {
				acceptCheck(os.IsNotExist(e), "cannot inspect owned interrupted index lock")
			}
			f.timeout, f.phase = 300*time.Second, "diagnostic-add-explicit-retry-300"
			second := acceptCapture("explicit-retry-add", func() { f.G("add", "-A") })
			row = f.history[len(f.history)-1]
			row["objects_after_attempt"] = counts()
			attempts = append(attempts, row)
			acceptCheck(second["status"] == "pass", "explicit same-repository retry failed: %v", second)
		}
		repo.verify(false)
		f.G("commit", "-qm", "diagnostic exact generated monorepo")
		repo.verify(true)
		f.G("fsck", "--full")
		acceptCheck(acceptHash(binary) == binaryHash && acceptHash(config) == configHash, "diagnostic input bytes changed")
		verified = true
	})
	selected := record{}
	for _, entry := range bytes.Split([]byte(settings), []byte{0}) {
		key, value, ok := bytes.Cut(entry, []byte{'\n'})
		if !ok {
			continue
		}
		name := strings.ToLower(string(key))
		if strings.HasPrefix(name, "filter.lfs.") || name == "core.fscache" || name == "core.fsmonitor" || name == "core.preloadindex" || name == "maintenance.auto" || name == "index.threads" || name == "grep.threads" {
			selected[name] = string(value)
		}
	}
	if failure, ok := result["error"].(string); ok {
		result["error"] = strings.ReplaceAll(failure, root, "<fixture>")
	}
	status := "fail"
	if verified && result["status"] == "pass" {
		status = "pass"
	}
	var addMS float64
	for _, attempt := range attempts {
		value, _ := attempt["elapsed_ms"].(float64)
		addMS += value
	}
	report := record{"schema_version": 1, "status": status, "acceptance_scope": "one retained generated dual-engine monorepo add diagnosis; not the complete multirepository suite", "binary_sha256": binaryHash, "system_gitconfig_sha256": configHash, "files_per_engine_project": 10000, "engine_project_count": 2, "tracked_paths": len(repo.files), "worktree_materialize_ms": materializeMS, "initial_objects": initialObjects, "elapsed_seconds": time.Since(start).Seconds(), "add_cumulative_elapsed_ms": addMS, "add_attempts": attempts, "effective_selected_configuration": selected, "index_tree_worktree_bytes_verified": verified, "fixture_retained": true, "private_evidence_basename": filepath.Base(output + ".private.json"), "result": result, "command_history": f.history, "memory_or_performance_gate": false, "terminal_functional_tests_executed": false}
	if e = writeNewJSON(output, report); e != nil {
		t.Fatal(e)
	}
	if status != "pass" {
		t.Fatal(fmt.Sprint(result["error"]))
	}
	t.Log("20k generated monorepo add diagnostic complete; fixture and raw Trace2 retained; full suite not implied")
}
