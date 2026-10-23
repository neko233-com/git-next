// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func localBenchmark(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	reference, e := acceptReference(o)
	if e != nil {
		return e
	}
	runs, e := o.integer("runs", 40)
	if e != nil {
		return e
	}
	files, e := o.integer("files", 1000)
	if e != nil {
		return e
	}
	directories, e := o.integer("directories", 16)
	if e != nil {
		return e
	}
	timeout, e := o.integer("timeout", 30)
	if e != nil {
		return e
	}
	setupTimeout, e := o.integer("setup-timeout", 180)
	if e != nil {
		return e
	}
	if runs < 10 || files < 1 || directories < 1 || timeout < 1 || setupTimeout < 1 {
		return fmt.Errorf("runs>=10, files/directories/timeouts>=1 required")
	}
	layout := o.value("fixture-layout", "loose")
	if layout != "loose" && layout != "packed" {
		return fmt.Errorf("fixture-layout must be loose or packed")
	}
	cache := o.value("client-fscache", "default")
	if cache != "default" && cache != "on" && cache != "off" {
		return fmt.Errorf("client-fscache must be default/on/off")
	}
	if o.yes("reference-read-safety") && !runtimeOSWindows() {
		return fmt.Errorf("reference-read-safety is Windows-only")
	}
	if (o.get("baseline-binary") == "") != (o.get("baseline-checkpoint") == "") {
		return fmt.Errorf("baseline-binary and baseline-checkpoint must be paired")
	}
	sourceHash, e := benchmarkCheckpoint(a.root, binary, o.get("source-checkpoint"))
	if e != nil {
		return e
	}
	binaryHash := acceptHash(binary)
	root, e := os.MkdirTemp("", "git-next-benchmark-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home, empty, repo := filepath.Join(root, "home"), filepath.Join(root, "empty-path"), filepath.Join(root, "repo")
	if e = os.MkdirAll(empty, 0755); e != nil {
		return e
	}
	env := acceptEnv(home)
	system := "disabled"
	if o.yes("system-config-enabled") {
		env = fixtureEnv(env, map[string]string{"GIT_CONFIG_NOSYSTEM": "0"})
		system = "enabled conventional filesystem paths"
	}
	clients := map[string]string{"git_next": binary, "git": reference}
	baselineIdentity := record{}
	if o.get("baseline-binary") != "" {
		baseline, e := filepath.Abs(o.get("baseline-binary"))
		if e != nil {
			return e
		}
		source, e := benchmarkCheckpoint(a.root, baseline, o.get("baseline-checkpoint"))
		if e != nil {
			return e
		}
		clients["git_next_baseline"] = baseline
		baselineIdentity = record{"binary_sha256": acceptHash(baseline), "source_tree_sha256": source}
	}
	referenceFlags := []string{}
	referenceSettings := []string{}
	if o.yes("reference-read-safety") {
		referenceSettings = []string{"core.fscache=false", "core.fsmonitor=false", "core.preloadIndex=false", "index.threads=1", "grep.threads=1"}
		for _, v := range referenceSettings {
			referenceFlags = append(referenceFlags, "-c", v)
		}
	}
	run := func(exe, where string, setup bool, data []byte, args ...string) measuredProcess {
		variables := env
		if exe != reference {
			extra := map[string]string{"PATH": empty}
			if !o.yes("native-distribution") {
				extra["GIT_EXEC_PATH"] = empty
				extra["GIT_SSH"] = filepath.Join(empty, "missing-ssh")
				extra["GIT_SSH_COMMAND"] = filepath.Join(empty, "missing-ssh")
			}
			if cache != "default" {
				value := "false"
				if cache == "on" {
					value = "true"
				}
				extra["GIT_CONFIG_COUNT"] = "1"
				extra["GIT_CONFIG_KEY_0"] = "core.fscache"
				extra["GIT_CONFIG_VALUE_0"] = value
			}
			variables = fixtureEnv(env, extra)
		} else {
			args = append(append([]string{}, referenceFlags...), args...)
		}
		seconds := timeout
		if setup {
			seconds = setupTimeout
		}
		value, e := measuredRun(where, variables, data, time.Duration(seconds)*time.Second, exe, args...)
		acceptMust(e)
		acceptCheck(value.code == 0, "benchmark command %s exit %d: %.400s", args[0], value.code, value.stderr)
		return value
	}
	run(reference, root, true, nil, "init", "-b", "main", "--object-format=sha1", repo)
	if layout == "packed" {
		var stream bytes.Buffer
		for i := 0; i < files; i++ {
			data := bytes.Repeat([]byte(fmt.Sprintf("benchmark fixture %d\n", i)), 16)
			fmt.Fprintf(&stream, "blob\nmark :%d\ndata %d\n", i+1, len(data))
			stream.Write(data)
			stream.WriteByte('\n')
		}
		message := "fixture: benchmark\n"
		fmt.Fprintf(&stream, "commit refs/heads/main\ncommitter Benchmark <benchmark@example.invalid> 981173106 +0000\ndata %d\n%s\n", len(message), message)
		for i := 0; i < files; i++ {
			fmt.Fprintf(&stream, "M 100644 :%d dir-%02d/file-%06d.txt\n", i+1, i%directories, i)
		}
		stream.WriteString("\ndone\n")
		run(reference, repo, true, stream.Bytes(), "fast-import", "--quiet", "--done")
		run(reference, repo, false, nil, "config", "core.autocrlf", "input")
		run(reference, repo, false, nil, "config", "core.eol", "lf")
		run(reference, repo, true, nil, "reset", "--hard", "HEAD")
	} else {
		for i := 0; i < files; i++ {
			acceptWrite(filepath.Join(repo, fmt.Sprintf("dir-%02d/file-%06d.txt", i%directories, i)), bytes.Repeat([]byte(fmt.Sprintf("benchmark fixture %d\n", i)), 16))
		}
		run(reference, repo, true, nil, "add", ".")
		run(reference, repo, true, nil, "commit", "-m", "fixture: benchmark")
	}
	initialStage := digestBytes(run(reference, repo, false, nil, "ls-files", "--stage", "-z").stdout)
	versions := record{}
	names := []string{"git_next", "git"}
	if clients["git_next_baseline"] != "" {
		names = append(names, "git_next_baseline")
	}
	for _, name := range names {
		versions[name] = strings.TrimSpace(string(run(clients[name], root, false, nil, "--version").stdout))
	}
	commands := []struct {
		name string
		args []string
	}{{"version", []string{"--version"}}, {"rev_parse", []string{"rev-parse", "HEAD"}}, {"status_clean", []string{"status", "--porcelain"}}, {"cat_file", []string{"cat-file", "-p", "HEAD:dir-00/file-000000.txt"}}}
	if o.yes("read-commands") {
		commands = append(commands, struct {
			name string
			args []string
		}{"diff_clean", []string{"diff", "--no-ext-diff", "--no-textconv"}}, struct {
			name string
			args []string
		}{"show_metadata", []string{"show", "--no-patch", "--format=fuller"}}, struct {
			name string
			args []string
		}{"log_metadata", []string{"log", "-n", "20", "--format=%H%x00%s"}}, struct {
			name string
			args []string
		}{"grep_files", []string{"grep", "-l", "-F", "benchmark fixture"}})
	}
	results := record{}
	rng := rand.New(rand.NewSource(233))
	for _, command := range commands {
		expected := run(reference, repo, false, nil, command.args...).stdout
		samples := map[string][]float64{}
		memory := map[string][]int64{}
		validate := func(name string, output []byte) {
			if command.name == "version" || bytes.Equal(output, expected) {
				return
			}
			failure := record{"status": "fail", "stage": "output validation", "operation": command.name, "client": name, "binary_sha256": binaryHash, "source_tree_sha256": sourceHash, "files": files, "directories": directories, "harness_sha256": acceptHash(a.path("tools/devtool/benchmark_local.go")), "actual_sample": string(output[:min(len(output), 2048)]), "expected_sample": string(expected[:min(len(expected), 2048)]), "initial_stage_sha256": initialStage, "valid_latency_report": false}
			if command.name == "status_clean" {
				probes := record{}
				for label, flags := range map[string][]string{"reference_retry": {}, "reference_without_fscache": {"-c", "core.fscache=false"}, "reference_without_fsmonitor": {"-c", "core.fsmonitor=false"}} {
					value, e := measuredRun(repo, fixtureEnv(env, map[string]string{"GIT_OPTIONAL_LOCKS": "0"}), nil, 30*time.Second, reference, append(flags, "status", "--porcelain")...)
					if e != nil {
						probes[label] = record{"error": e.Error()}
					} else {
						probes[label] = record{"exit_code": value.code, "output_sample": string(value.stdout[:min(len(value.stdout), 2048)])}
					}
				}
				failure["probes"] = probes
				failure["current_stage_sha256"] = digestBytes(run(reference, repo, false, nil, "ls-files", "--stage", "-z").stdout)
				live := []record{}
				for _, line := range bytes.Split(output, []byte("\n"))[:min(len(bytes.Split(output, []byte("\n"))), 5)] {
					if len(line) < 3 {
						continue
					}
					relative := string(line[3:])
					path := filepath.Join(repo, relative)
					if !inside(repo, path) {
						continue
					}
					item := record{"path": relative, "exists": exists(path), "is_file": false}
					if info, e := os.Stat(path); e == nil && info.Mode().IsRegular() {
						item["is_file"] = true
						item["size"] = info.Size()
						item["sha256"] = acceptHash(path)
					}
					live = append(live, item)
				}
				failure["live_file_samples"] = live
			}
			destination := a.path(".evidence/benchmark-validation-failure.json")
			if o.get("output") != "" {
				destination = strings.TrimSuffix(o.get("output"), filepath.Ext(o.get("output"))) + ".failure.json"
			}
			acceptMust(writeJSON(destination, failure))
			panic(fmt.Errorf("benchmark %s %s output mismatch; latency report rejected", command.name, name))
		}
		for _, name := range names {
			for i := 0; i < 3; i++ {
				validate(name, run(clients[name], repo, false, nil, command.args...).stdout)
			}
		}
		for i := 0; i < runs; i++ {
			order := append([]string{}, names...)
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			for _, name := range order {
				value := run(clients[name], repo, false, nil, command.args...)
				validate(name, value.stdout)
				samples[name] = append(samples[name], value.elapsed)
				if value.peak > 0 {
					memory[name] = append(memory[name], value.peak)
				}
			}
		}
		entry := record{}
		for _, name := range names {
			entry[name] = benchmarkStats(samples[name], memory[name])
		}
		results[command.name] = entry
	}
	run(reference, repo, false, nil, "fsck", "--full")
	acceptCheck(acceptHash(binary) == binaryHash, "binary changed during benchmark")
	if len(baselineIdentity) > 0 {
		acceptCheck(acceptHash(clients["git_next_baseline"]) == baselineIdentity["binary_sha256"], "baseline changed")
	}
	origin := "current build inputs"
	if o.get("source-checkpoint") != "" {
		origin = "recorded checkpoint verified against binary hash"
	}
	eol := "existing isolated/system settings"
	if layout == "packed" {
		eol = "input/lf"
	}
	report := record{"schema_version": 1, "versions": versions, "harness_sha256": acceptHash(a.path("tools/devtool/benchmark_local.go")), "binary_sha256": binaryHash, "source_tree_sha256": sourceHash, "source_hash_origin": origin, "os": benchmarkOS(), "arch": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "cpu": os.Getenv("PROCESSOR_IDENTIFIER"), "cache": "warm after 3 unmeasured runs; OS cache not flushed", "scope": "process creation + command + captured output; local disposable SHA-1 repository", "files": files, "runs_per_client_per_command": runs, "configured_data_directories": directories, "actual_data_directories": min(files, directories), "command_timeout_seconds": timeout, "fixture_setup_timeout_seconds": setupTimeout, "fixture_layout": layout, "fixture_local_eol": eol, "randomized_order_seed": 233, "randomized_order_generator": "Go math/rand Source", "runtime_git_ssh_on_path": false, "client_core_fscache": cache, "reference_read_safety_overrides": referenceSettings, "system_configuration": system, "results": results, "full_git_parity": false, "memory_scope": "Windows primary process peak working set; queried after latency interval; child processes excluded"}
	if len(baselineIdentity) > 0 {
		report["native_baseline_identity"] = baselineIdentity
	}
	if o.get("output") != "" {
		if e = writeJSON(o.get("output"), report); e != nil {
			return e
		}
	}
	return a.json(report)
}
func init() {
	register("benchmark", "Warm process/command latency, output checks and Windows primary-process peak memory", "reference runs files directories timeout setup-timeout fixture-layout client-fscache reference-read-safety? output read-commands? system-config-enabled? source-checkpoint baseline-binary baseline-checkpoint native-distribution?", localBenchmark)
}
