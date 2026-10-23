// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func networkBenchmark(a *app, o options) error {
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
	timeout, e := o.integer("timeout", 90)
	if e != nil {
		return e
	}
	minimum := 10
	if o.yes("validation-only") {
		minimum = 1
	}
	if runs < minimum || files < 10 || timeout < 1 {
		return fmt.Errorf("runs>=%d/files>=10/timeout>=1 required", minimum)
	}
	sourceHash, e := benchmarkCheckpoint(a.root, binary, o.get("source-checkpoint"))
	if e != nil {
		return e
	}
	binaryHash := acceptHash(binary)
	root, e := os.MkdirTemp("", "git-next-network-benchmark-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home, empty, source, remote, seed := filepath.Join(root, "home"), filepath.Join(root, "empty"), filepath.Join(root, "source"), filepath.Join(root, "repository.git"), filepath.Join(root, "seed")
	if e = os.MkdirAll(empty, 0755); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_CONFIG_GLOBAL": filepath.Join(home, "config")})
	clients := map[string]string{"git_next": binary, "git": reference}
	run := func(exe, where string, data []byte, args ...string) measuredProcess {
		variables := env
		if exe == binary {
			extra := map[string]string{"PATH": empty}
			if !o.yes("native-distribution") {
				extra["GIT_EXEC_PATH"] = empty
				extra["GIT_SSH"] = filepath.Join(empty, "missing-ssh")
				extra["GIT_SSH_COMMAND"] = filepath.Join(empty, "missing-ssh")
			}
			variables = fixtureEnv(env, extra)
		} else if runtimeOSWindows() && !o.yes("native-distribution") {
			variables = fixtureEnv(env, map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "http.sslBackend", "GIT_CONFIG_VALUE_0": "openssl"})
		}
		value, e := measuredRun(where, variables, data, time.Duration(timeout)*time.Second, exe, args...)
		acceptMust(e)
		acceptCheck(value.code == 0, "network command %s exited %d: %.500s", args[0], value.code, value.stderr)
		return value
	}
	git := func(where string, args ...string) []byte { return run(reference, where, nil, args...).stdout }
	git(root, "init", "-b", "main", source)
	for i := 0; i < files; i++ {
		acceptWrite(filepath.Join(source, fmt.Sprintf("dir-%d/file-%07d.txt", i%16, i)), bytes.Repeat([]byte(fmt.Sprintf("fixture file %d\n", i)), 16))
	}
	acceptWrite(filepath.Join(source, ".gitattributes"), []byte("* text=auto eol=lf\n"))
	git(source, "add", ".")
	git(source, "commit", "-m", "fixture: network baseline")
	oldHead := bytes.TrimSpace(git(source, "rev-parse", "HEAD"))
	oldTree := acceptanceSnapshot(source, true)
	oldBytes := int64(0)
	for name := range oldTree {
		info, e := os.Stat(filepath.Join(source, filepath.FromSlash(name)))
		acceptMust(e)
		oldBytes += info.Size()
	}
	git(root, "clone", "--bare", "--no-hardlinks", source, remote)
	git(remote, "gc", "--prune=now")
	core := strings.TrimSpace(string(git(root, "--exec-path")))
	backend := filepath.Join(core, "git-http-backend")
	if runtimeOSWindows() {
		backend += ".exe"
	}
	service, e := newGitHTTPFixture(root, backend, env, fixtureSecret())
	if e != nil {
		return e
	}
	defer service.close()
	auth := base64.StdEncoding.EncodeToString([]byte("fixture:" + service.password))
	config := "[core]\nautocrlf = input\neol = lf\n[credential]\nhelper =\n[http]\nsslVerify = true\nsslCAInfo = \"" + filepath.ToSlash(service.CA) + "\"\nextraHeader = Authorization: Basic " + auth + "\n"
	if o.yes("native-distribution") && runtimeOSWindows() {
		config += "\n[http]\n sslBackend = schannel\n schannelUseSSLCAInfo = true\n schannelCheckRevoke = false\n"
	}
	acceptMust(write(filepath.Join(home, "config"), []byte(config), 0600))
	versions := record{}
	for label, exe := range clients {
		versions[label] = strings.TrimSpace(string(run(exe, root, nil, "--version").stdout))
	}
	git(root, "clone", "--no-hardlinks", remote, seed)
	git(seed, "remote", "set-url", "origin", service.URL)
	oldStage := git(seed, "ls-files", "--stage", "-z")
	oldObjects := map[string]bool{}
	for _, line := range bytes.Split(git(seed, "rev-list", "--objects", "--all"), []byte("\n")) {
		if len(line) > 0 {
			oldObjects[strings.Fields(string(line))[0]] = true
		}
	}
	rng := rand.New(rand.NewSource(233))
	results, requests := record{}, record{}
	for _, operation := range []string{"clone", "fetch", "pull_ff_only"} {
		if operation == "fetch" {
			for i := 0; i < 10; i++ {
				acceptWrite(filepath.Join(source, fmt.Sprintf("dir-%d/file-%07d.txt", i%16, i)), []byte(fmt.Sprintf("incremental edit %d\n", i)))
			}
			git(source, "add", ".")
			git(source, "commit", "-m", "fixture: incremental network update")
			git(source, "push", remote, "main")
		}
		expectedHead := oldHead
		if operation != "clone" {
			expectedHead = bytes.TrimSpace(git(source, "rev-parse", "HEAD"))
		}
		expectedTree := oldTree
		if operation != "clone" {
			expectedTree = acceptanceSnapshot(source, true)
		}
		expectedStage := git(source, "ls-files", "--stage", "-z")
		remoteObjects := acceptanceSnapshot(filepath.Join(remote, "objects"), false)
		samples := map[string][]float64{}
		memory := map[string][]int64{}
		counts := map[string][]int{}
		for iteration := 0; iteration < runs+3; iteration++ {
			order := []string{"git_next", "git"}
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			for _, label := range order {
				exe := clients[label]
				target := filepath.Join(root, "trial")
				var indexBefore []byte
				if operation != "clone" {
					acceptMust(benchmarkCopy(seed, target))
					acceptEqual(run(exe, target, nil, "status", "--porcelain").stdout, nil, "trial clean")
					indexBefore = acceptBytes(filepath.Join(target, ".git/index"))
				}
				service.mu.Lock()
				before := service.authenticated
				service.mu.Unlock()
				where, args := root, []string{"clone", service.URL, target}
				if operation != "clone" {
					where = target
					if operation == "fetch" {
						args = []string{"fetch"}
					} else {
						args = []string{"pull", "--ff-only"}
					}
				}
				value := run(exe, where, nil, args...)
				service.mu.Lock()
				requestCount := service.authenticated - before
				service.mu.Unlock()
				acceptCheck(requestCount > 0, "network trial made no authenticated requests")
				headExpected := expectedHead
				treeExpected := expectedTree
				stageExpected := expectedStage
				if operation == "fetch" {
					headExpected = oldHead
					treeExpected = oldTree
					stageExpected = oldStage
				}
				acceptEqual(bytes.TrimSpace(git(target, "rev-parse", "HEAD")), headExpected, "trial HEAD")
				acceptEqual(bytes.TrimSpace(git(target, "rev-parse", "refs/remotes/origin/main")), expectedHead, "tracking ref")
				acceptCheck(equalJSON(acceptanceSnapshot(target, true), treeExpected), "trial worktree bytes differ")
				acceptEqual(git(target, "ls-files", "--stage", "-z"), stageExpected, "trial staged IDs")
				if operation == "fetch" {
					acceptEqual(acceptBytes(filepath.Join(target, ".git/index")), indexBefore, "fetch index bytes")
					objects := map[string]bool{}
					for _, line := range bytes.Split(git(target, "rev-list", "--objects", "--all"), []byte("\n")) {
						if len(line) > 0 {
							objects[strings.Fields(string(line))[0]] = true
						}
					}
					for id := range oldObjects {
						acceptCheck(objects[id], "fetch lost existing object")
					}
					acceptCheck(len(objects) > len(oldObjects), "fetch acquired no new objects")
				}
				git(target, "fsck", "--full")
				acceptCheck(equalJSON(acceptanceSnapshot(filepath.Join(remote, "objects"), false), remoteObjects), "fixture source objects changed")
				if iteration >= 3 {
					samples[label] = append(samples[label], value.elapsed)
					if value.peak > 0 {
						memory[label] = append(memory[label], value.peak)
					}
					counts[label] = append(counts[label], requestCount)
				}
				acceptMust(benchmarkRemove(root, target))
			}
		}
		entry := record{}
		for label := range clients {
			entry[label] = benchmarkStats(samples[label], memory[label])
		}
		results[operation] = entry
		requests[operation] = counts
	}
	acceptCheck(acceptHash(binary) == binaryHash, "binary changed during benchmark")
	backends := record{"git_next": "rustls", "git": "reference default"}
	if runtimeOSWindows() {
		if o.yes("native-distribution") {
			backends = record{"git_next": "Schannel", "git": "Schannel"}
		} else {
			backends["git"] = "OpenSSL explicitly selected in the Windows reference process"
		}
	}
	if o.yes("validation-only") {
		results = record{}
	}
	report := record{"schema_version": 1, "status": "pass", "binary_sha256": binaryHash, "source_tree_sha256": sourceHash, "harness_sha256": acceptHash(a.path("tools/devtool/benchmark_network.go")), "http_fixture_sha256": acceptHash(a.path("tools/devtool/fixture_http.go")), "versions": versions, "os": benchmarkOS(), "arch": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "files": files, "initial_worktree_bytes": oldBytes, "incremental_file_edits": 10, "fixture_contents": "16 repeated LF text lines per data file; one attribute policy; next commit edits 10 files", "measured_at_utc": time.Now().UTC().Format(time.RFC3339Nano), "runs_per_client_per_command": runs, "randomized_order_seed": 233, "randomized_order_generator": "Go math/rand Source", "cache": "warm after 3 unmeasured runs; OS cache not flushed", "results": results, "performance_accepted": !o.yes("validation-only"), "authenticated_request_counts": requests, "runtime_git_ssh_on_path": false, "scope": "loopback smart HTTPS, temporary private CA and static fixture authorization; clone/fetch/ff-only pull", "system_configuration": "disabled with isolated profiles and explicit input/LF fixture policy", "tls_backends": backends, "memory_scope": "Windows primary process peak working set; queried after latency interval; transport/child processes excluded", "certificate_verification": "enabled; private fixture CA and loopback IP SAN; no user trust store changes", "http_response_protocol": "HTTP/1.1", "protocol_scope": "fixture default smart HTTP; no v2 packet or object-reuse acceptance", "verification": "each trial verifies HEAD/tracking refs, all worktree bytes, staged IDs, fsck and unchanged source object bytes; fetch preserves HEAD/index/worktree", "packet_bytes_or_object_reuse_proven": false, "real_provider_login_verified": false, "full_git_parity": false}
	if e = writeJSON(o.get("output"), report); e != nil {
		return e
	}
	return a.json(report)
}
func init() {
	register("network-benchmark", "Paired loopback TLS clone/fetch/pull with independent state verification", "reference source-checkpoint runs files timeout validation-only? output native-distribution?", networkBenchmark)
}
