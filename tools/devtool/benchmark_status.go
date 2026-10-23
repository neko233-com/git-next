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

func statusBenchmark(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if e = o.require("baseline"); e != nil {
		return e
	}
	baseline, e := filepath.Abs(o.get("baseline"))
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
	if runs < 10 || files < 1 {
		return fmt.Errorf("runs>=10/files>=1 required")
	}
	clients := map[string]string{"git_next": binary, "before_refresh": baseline, "git": reference}
	hashes := record{}
	for name, exe := range clients {
		hashes[name] = acceptHash(exe)
	}
	root, e := os.MkdirTemp("", "git-next-status-benchmark-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home, empty := filepath.Join(root, "home"), filepath.Join(root, "empty")
	if e = os.MkdirAll(empty, 0755); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_AUTHOR_DATE": "2026-10-19T00:00:00Z", "GIT_COMMITTER_DATE": "2026-10-19T00:00:00Z"})
	run := func(exe, where string, args ...string) measuredProcess {
		variables := env
		if exe != reference {
			variables = fixtureEnv(env, map[string]string{"PATH": empty, "GIT_EXEC_PATH": empty})
		}
		value, e := measuredRun(where, variables, nil, 30*time.Second, exe, args...)
		acceptMust(e)
		acceptCheck(value.code == 0, "status command %v failed %.300s", args, value.stderr)
		return value
	}
	repos := map[string]string{}
	names := []string{"git_next", "before_refresh", "git"}
	for _, name := range names {
		repo := filepath.Join(root, name)
		run(binary, root, "init", repo)
		for i := 0; i < files; i++ {
			acceptWrite(filepath.Join(repo, fmt.Sprintf("dir-%02d/file-%06d.txt", i%16, i)), bytes.Repeat([]byte(fmt.Sprintf("fixture %d\n", i)), 32))
		}
		run(binary, repo, "add", "-A")
		run(binary, repo, "commit", "-m", "fixture: native index")
		repos[name] = repo
	}
	versions, first := record{}, record{}
	samples := map[string][]float64{}
	for name, exe := range clients {
		versions[name] = strings.TrimSpace(string(run(exe, root, "--version").stdout))
	}
	rng := rand.New(rand.NewSource(233))
	order := append([]string{}, names...)
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	for _, name := range order {
		value := run(clients[name], repos[name], "status", "--porcelain")
		acceptEqual(value.stdout, nil, "first clean status")
		first[name] = value.elapsed
		for i := 0; i < 3; i++ {
			acceptEqual(run(clients[name], repos[name], "status", "--porcelain").stdout, nil, "warm clean status")
		}
	}
	for i := 0; i < runs; i++ {
		order = append([]string{}, names...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, name := range order {
			value := run(clients[name], repos[name], "status", "--porcelain")
			acceptEqual(value.stdout, nil, "measured status")
			samples[name] = append(samples[name], value.elapsed)
		}
	}
	head := []byte{}
	for _, repo := range repos {
		run(reference, repo, "fsck", "--full")
		value := run(reference, repo, "rev-parse", "HEAD").stdout
		if len(head) == 0 {
			head = value
		} else {
			acceptEqual(value, head, "equivalent fixture commits")
		}
	}
	for name, exe := range clients {
		acceptCheck(acceptHash(exe) == hashes[name], "benchmark executable changed")
	}
	results := record{}
	for name, values := range samples {
		stats := benchmarkStats(values, nil)
		results[name] = record{"median_ms": stats["median_ms"], "p95_ms": stats["p95_ms"]}
	}
	report := record{"schema_version": 1, "scope": "warm status on separate equivalent native-created zero-stat indexes; process creation and output included", "versions": versions, "binary_sha256": hashes, "os": benchmarkOS(), "cpu_count": runtime.NumCPU(), "files": files, "runs_per_client": runs, "randomized_order_seed": 233, "randomized_order_generator": "Go math/rand Source", "runtime_git_on_native_path": false, "first_status_ms": first, "results": results, "full_git_parity": false}
	if o.get("output") != "" {
		if e = writeJSON(o.get("output"), report); e != nil {
			return e
		}
	}
	return a.json(report)
}
func referenceWide(a *app, o options) error {
	reference, e := acceptReference(o)
	if e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "git-next-reference-wide-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	repo := filepath.Join(root, "repo")
	env := fixtureEnv(acceptEnv(filepath.Join(root, "home")), map[string]string{"GIT_CONFIG_NOSYSTEM": "0"})
	run := func(data []byte, args ...string) []byte {
		where := root
		if exists(repo) {
			where = repo
		}
		value, e := measuredRun(where, env, data, 180*time.Second, reference, args...)
		acceptMust(e)
		acceptCheck(value.code == 0, "reference-only command failed %v", args)
		return value.stdout
	}
	run(nil, "init", "-b", "main", repo)
	var stream bytes.Buffer
	for i := 0; i < 10000; i++ {
		data := bytes.Repeat([]byte(fmt.Sprintf("benchmark fixture %d\n", i)), 16)
		fmt.Fprintf(&stream, "blob\nmark :%d\ndata %d\n", i+1, len(data))
		stream.Write(data)
		stream.WriteByte('\n')
	}
	message := "fixture: reference-only diagnostic\n"
	fmt.Fprintf(&stream, "commit refs/heads/main\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\ndata %d\n%s\n", len(message), message)
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&stream, "M 100644 :%d dir-%02d/file-%06d.txt\n", i+1, i%1000, i)
	}
	stream.WriteString("\ndone\n")
	run(stream.Bytes(), "fast-import", "--quiet", "--done")
	run(nil, "config", "core.autocrlf", "input")
	run(nil, "config", "core.eol", "lf")
	run(nil, "reset", "--hard", "HEAD")
	stage := digestBytes(run(nil, "ls-files", "--stage", "-z"))
	results := []record{}
	for _, configuration := range []struct {
		name  string
		flags []string
	}{{"default", nil}, {"without_fscache", []string{"-c", "core.fscache=false"}}, {"without_preload", []string{"-c", "core.preloadIndex=false"}}} {
		failures := []record{}
		for iteration := 0; iteration < 60; iteration++ {
			out := run(nil, append(append([]string{}, configuration.flags...), "status", "--porcelain")...)
			if len(out) > 0 {
				line := bytes.SplitN(out, []byte("\n"), 2)[0]
				acceptCheck(len(line) >= 3, "short status output")
				path := filepath.Join(repo, string(line[3:]))
				acceptCheck(inside(repo, path), "reference sample escaped fixture")
				sample := record{"iteration": iteration, "sample": string(out[:min(len(out), 2048)]), "sample_file_exists": exists(path), "sample_file_sha256": nil}
				if exists(path) {
					sample["sample_file_sha256"] = acceptHash(path)
				}
				failures = append(failures, sample)
			}
		}
		results = append(results, record{"configuration": configuration.name, "runs": 60, "nonempty_status_count": len(failures), "failures": failures})
	}
	stageAfter := digestBytes(run(nil, "ls-files", "--stage", "-z"))
	run(nil, "fsck", "--full")
	report := record{"scope": "Reference Git-only stability diagnostic; no native client executed; no performance acceptance", "reference_version": strings.TrimSpace(string(run(nil, "--version"))), "files": 10000, "directories": 1000, "index_stage_entries_unchanged": stage == stageAfter, "stage_sha256": stage, "results": results, "harness_sha256": acceptHash(a.path("tools/devtool/benchmark_status.go"))}
	output := o.value("output", a.path(".evidence/reference-wide-stability.json"))
	if e = writeJSON(output, report); e != nil {
		return e
	}
	return a.json(report)
}
func init() {
	register("status-benchmark", "Warm status comparison on equivalent native-created zero-stat indexes", "baseline reference runs files output", statusBenchmark)
	register("reference-wide-stability", "Reference-only wide-tree stability diagnostic; never launches candidate", "reference output", referenceWide)
}
