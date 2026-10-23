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

func mergeBenchmark(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	reference, e := acceptReference(o)
	if e != nil {
		return e
	}
	if e = o.require("output"); e != nil {
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
	if runs < 10 || files < 3 {
		return fmt.Errorf("runs>=10/files>=3 required")
	}
	binaryHash := acceptHash(binary)
	sourceHash := benchmarkSourceHash(a.root)
	root, e := os.MkdirTemp("", "git-next-merge-benchmark-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home, empty, fixture := filepath.Join(root, "home"), filepath.Join(root, "empty"), filepath.Join(root, "fixture")
	if e = os.MkdirAll(empty, 0755); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_CONFIG_NOSYSTEM": "0", "GIT_MERGE_AUTOEDIT": "no", "GIT_AUTHOR_DATE": "2001-02-03T04:05:06+0000", "GIT_COMMITTER_DATE": "2001-02-03T04:05:06+0000"})
	run := func(client bool, where string, args ...string) measuredProcess {
		exe := reference
		variables := env
		if client {
			exe = binary
			variables = fixtureEnv(env, map[string]string{"PATH": empty, "GIT_EXEC_PATH": empty})
		}
		value, e := measuredRun(where, variables, nil, 60*time.Second, exe, args...)
		acceptMust(e)
		acceptCheck(value.code == 0, "merge benchmark %v exited %d: %.300s", args, value.code, value.stderr)
		return value
	}
	run(false, root, "init", "-b", "main", fixture)
	run(false, fixture, "config", "core.autocrlf", "false")
	acceptWrite(filepath.Join(fixture, ".gitattributes"), []byte("* text=auto eol=lf\n"))
	for i := 0; i < files; i++ {
		content := append([]byte("first line\n"), bytes.Repeat([]byte("stable fixture content\n"), 38)...)
		content = append(content, []byte("last line\n")...)
		acceptWrite(filepath.Join(fixture, fmt.Sprintf("file-%d.txt", i)), content)
	}
	run(false, fixture, "add", ".")
	run(false, fixture, "commit", "-m", "base")
	run(false, fixture, "switch", "-c", "topic")
	path := filepath.Join(fixture, "file-0.txt")
	acceptWrite(path, bytes.ReplaceAll(acceptBytes(path), []byte("last line"), []byte("their final line")))
	acceptWrite(filepath.Join(fixture, "file-1.txt"), []byte("their fixture edit\n"))
	run(false, fixture, "add", ".")
	run(false, fixture, "commit", "-m", "theirs")
	run(false, fixture, "switch", "main")
	acceptWrite(path, bytes.ReplaceAll(acceptBytes(path), []byte("first line"), []byte("our first line")))
	acceptWrite(filepath.Join(fixture, "file-2.txt"), []byte("our fixture edit\n"))
	run(false, fixture, "add", ".")
	run(false, fixture, "commit", "-m", "ours")
	versions := record{"git_next": strings.TrimSpace(string(run(true, root, "--version").stdout)), "git": strings.TrimSpace(string(run(false, root, "--version").stdout))}
	samples := map[string][]float64{}
	rng := rand.New(rand.NewSource(233))
	for i := 0; i < runs+3; i++ {
		order := []bool{false, true}
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		var evidence record
		for _, client := range order {
			destination := filepath.Join(root, fmt.Sprintf("run-%d-%t", i, client))
			acceptMust(benchmarkCopy(fixture, destination))
			acceptEqual(run(false, destination, "status", "--porcelain").stdout, nil, "fixture primed status")
			value := run(client, destination, "merge", "--no-edit", "topic")
			label := "git"
			if client {
				label = "git_next"
			}
			if i >= 3 {
				samples[label] = append(samples[label], value.elapsed)
			}
			worktree := record{}
			entries, e := os.ReadDir(destination)
			acceptMust(e)
			for _, entry := range entries {
				if !entry.IsDir() {
					worktree[entry.Name()] = acceptHash(filepath.Join(destination, entry.Name()))
				}
			}
			observed := record{"commit": string(run(false, destination, "cat-file", "commit", "HEAD").stdout), "index": string(run(false, destination, "ls-files", "--stage").stdout), "status": string(run(false, destination, "status", "--porcelain").stdout), "orig_head": string(acceptBytes(filepath.Join(destination, ".git/ORIG_HEAD"))), "reflog": string(acceptBytes(filepath.Join(destination, ".git/logs/HEAD"))), "worktree": worktree}
			if evidence == nil {
				evidence = observed
			} else {
				acceptCheck(equalJSON(evidence, observed), "measured clients produced different repository state")
			}
			run(false, destination, "fsck", "--full")
			acceptMust(benchmarkRemove(root, destination))
		}
	}
	acceptCheck(acceptHash(binary) == binaryHash, "binary changed during merge benchmark")
	results := record{}
	for name, values := range samples {
		stats := benchmarkStats(values, nil)
		results[name] = record{"median_ms": stats["median_ms"], "p95_ms": stats["p95_ms"], "samples_ms": values}
	}
	report := record{"schema_version": 1, "product": "git-next", "binary_sha256": binaryHash, "source_tree_sha256": sourceHash, "versions": versions, "harness_sha256": acceptHash(a.path("tools/devtool/benchmark_merge.go")), "os": benchmarkOS(), "arch": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "runs": runs, "warmups": 3, "seed": 233, "randomized_order_generator": "Go math/rand Source", "files": files, "merged_paths": 2, "fixture_commits": 3, "system_configuration_enabled": true, "fixture_index_primed_before_measurement": true, "client_git_on_path": false, "scope": "warm clean two-parent merge; process/captured-output included; no memory or cold-cache claim", "verified": []string{"raw commit/parents/tree", "index stages", "worktree file hashes", "ORIG_HEAD", "HEAD reflog", "clean status", "independent fsck"}, "results": results}
	if e = writeJSON(o.get("output"), report); e != nil {
		return e
	}
	return a.json(report)
}
func init() {
	register("merge-benchmark", "Warm clean two-parent merge latency with exact reference Git state verification", "reference runs files output", mergeBenchmark)
}
