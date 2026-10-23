// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func nativeReadThreads(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if e = o.require("output"); e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "git-next-read-threads-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	started := time.Now()
	results := []record{}
	r := acceptCapture("read-thread-matrix", func() {
		home, repo := filepath.Join(root, "home"), filepath.Join(root, "repo")
		env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_OPTIONAL_LOCKS": "0", "GIT_TRACE2_EVENT_NESTING": "10"})
		run := func(command []string, data []byte, trace string) []byte {
			where := root
			if exists(repo) {
				where = repo
			}
			variables := env
			if trace != "" {
				variables = fixtureEnv(env, map[string]string{"GIT_TRACE2_EVENT": trace})
			}
			return acceptRun(where, variables, data, true, binary, command...)
		}
		values := func(name string, command ...string) ([]byte, map[string]int) {
			p := filepath.Join(root, name+".jsonl")
			out := run(command, nil, p)
			events := map[string]int{}
			for _, line := range bytes.Split(acceptBytes(p), []byte("\n")) {
				if len(line) == 0 {
					continue
				}
				var item record
				acceptMust(json.Unmarshal(line, &item))
				if item["event"] == "data" && item["category"] == "git-next" {
					v, e := strconv.Atoi(fmt.Sprint(item["value"]))
					acceptMust(e)
					events[stringValue(item, "key")] = v
				}
			}
			return out, events
		}
		run([]string{"init", "-b", "main", repo}, nil, "")
		data := []byte("read-fixture\n")
		var stream bytes.Buffer
		fmt.Fprintf(&stream, "blob\nmark :1\ndata %d\n%s\ncommit refs/heads/main\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\ndata 7\nfixture\n", len(data), data)
		count := 32000
		for i := 0; i < count; i++ {
			fmt.Fprintf(&stream, "M 100644 :1 d%02d/f%06d.txt\n", i%32, i)
		}
		stream.WriteString("\ndone\n")
		run([]string{"fast-import", "--quiet", "--done"}, stream.Bytes(), "")
		run([]string{"read-tree", "HEAD"}, nil, "")
		blob := strings.TrimSpace(string(run([]string{"rev-parse", "HEAD:d00/f000000.txt"}, nil, "")))
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		cpus := runtime.NumCPU()
		var restore, restrict func() error
		if runtime.GOOS != "darwin" {
			var e error
			cpus, restore, restrict, e = acceptanceAffinity()
			acceptMust(e)
		}
		budget := cpus * 2
		defaultOut, events := values("default", "ls-files", "--stage", "-z")
		acceptCheck(events["read-thread-budget"] == budget, "default budget %v != %d", events, budget)
		serial, one := values("serial", "-c", "index.threads=1", "ls-files", "--stage", "-z")
		acceptEqual(serial, defaultOut, "serial index")
		acceptCheck(one["read-thread-budget"] == 1 && one["index-entry-workers"] == 1, "serial worker count")
		minimum := 1
		if budget >= 3 {
			minimum = 2
		}
		acceptCheck(events["index-entry-workers"] >= minimum, "default workers")
		results = append(results, record{"case": "native-index-default-cpu-times-two-and-explicit-single-thread", "status": "pass", "available_cpus": cpus, "default_budget": budget, "actual_entry_workers": events["index-entry-workers"]})
		count = 4000
		stream.Reset()
		stream.WriteString("commit refs/heads/read-small\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\ndata 7\nfixture\nfrom refs/heads/main\ndeleteall\n")
		for i := 0; i < count; i++ {
			fmt.Fprintf(&stream, "M 100644 %s d%02d/f%06d.txt\n", blob, i%32, i)
		}
		stream.WriteString("\ndone\n")
		run([]string{"fast-import", "--quiet", "--done"}, stream.Bytes(), "")
		run([]string{"symbolic-ref", "HEAD", "refs/heads/read-small"}, nil, "")
		run([]string{"read-tree", "HEAD"}, nil, "")
		status, preload := values("preload", "status", "--porcelain=v1", "-uno")
		acceptCheck(preload["preload-thread-budget"] == budget && preload["preload-workers"] >= 2 && preload["preload-workers"] <= min(budget, count/500), "preload workers %v", preload)
		overridden, three := values("three", "-c", "index.threads=3", "status", "--porcelain=v1", "-uno")
		acceptEqual(overridden, status, "preload override output")
		acceptCheck(three["preload-thread-budget"] == 3 && three["preload-workers"] == 3, "preload override")
		run([]string{"config", "index.threads", "2"}, nil, "")
		_, override := values("command-priority", "-c", "index.threads=3", "status", "--porcelain=v1", "-uno")
		acceptCheck(override["preload-thread-budget"] == 3, "command precedence")
		run([]string{"config", "--unset", "index.threads"}, nil, "")
		results = append(results, record{"case": "preload-default-and-repository-command-line-precedence", "status": "pass", "actual_workers": preload["preload-workers"]})
		if runtime.GOOS != "darwin" {
			func() {
				defer func() { acceptMust(restore()) }()
				acceptMust(restrict())
				_, limited := values("affinity", "ls-files", "--stage", "-z")
				acceptCheck(limited["read-thread-budget"] == 2, "inherited affinity budget %v", limited)
			}()
			results = append(results, record{"case": "current-process-affinity-is-inherited-and-honored", "status": "pass"})
		}
		acceptWrite(filepath.Join(repo, "d00/f000000.txt"), data)
		acceptWrite(filepath.Join(repo, "new-read-file.txt"), data)
		indexHash := acceptHash(filepath.Join(repo, ".git/index"))
		_, huge := values("large-budget", "-c", "core.ignorecase=true", "-c", "index.threads=2147483647", "status", "--porcelain=v1", "--untracked-files=all")
		acceptCheck(huge["name-hash-thread-budget"] == 2147483647 && huge["name-hash-workers"] > 1 && huge["name-hash-workers"] <= count/2000, "huge budget overflow/bounds %v", huge)
		acceptCheck(acceptHash(filepath.Join(repo, ".git/index")) == indexHash, "readonly index mutated")
		results = append(results, record{"case": "large-explicit-index-budget-bounded-without-integer-overflow", "status": "pass", "actual_name_workers": huge["name-hash-workers"]})
		grep, ge := values("grep", "grep", "--cached", "-l", "-F", "read-fixture")
		single, _ := values("grep-single", "grep", "--threads=1", "--cached", "-l", "-F", "read-fixture")
		explicit, goverride := values("grep-override", "-c", "grep.threads=3", "grep", "--threads=2", "--cached", "-l", "-F", "read-fixture")
		acceptEqual(grep, single, "grep serial")
		acceptEqual(grep, explicit, "grep override")
		acceptCheck(ge["grep-workers"] == budget && goverride["grep-workers"] == 2, "grep budgets")
		results = append(results, record{"case": "readonly-grep-default-budget-and-explicit-override-consistency", "status": "pass", "default_workers": ge["grep-workers"]})
		if runtimeOSWindows() {
			stable := map[string]string{"core.fscache": "false", "core.fsmonitor": "false", "core.preloadindex": "false", "index.threads": "1", "grep.threads": "1"}
			configuration := o.value("system-config", filepath.Join(filepath.Dir(filepath.Dir(binary)), "runtime/etc/gitconfig"))
			acceptCheck(exists(configuration), "owned shipped runtime configuration missing: %s", configuration)
			shipped := fixtureEnv(env, map[string]string{"GIT_CONFIG_NOSYSTEM": "0", "GIT_CONFIG_SYSTEM": configuration})
			shipRun := func(args ...string) []byte { return acceptRun(repo, shipped, nil, true, binary, args...) }
			for key, value := range stable {
				acceptEqual(bytes.TrimSpace(shipRun("config", "--get", key)), []byte(value), "shipped default "+key)
			}
			acceptMust(os.Remove(filepath.Join(repo, "new-read-file.txt")))
			shipRun("reset", "--hard", "HEAD")
			expected := shipRun("ls-files", "-z")
			for i := 0; i < 30; i++ {
				acceptEqual(shipRun("grep", "-z", "-l", "-F", "read-fixture"), expected, "shipped complete grep")
				acceptEqual(shipRun("status", "--porcelain=v1"), nil, "shipped status")
			}
			results = append(results, record{"case": "windows-shipped-read-safety-and-working-tree-completeness", "status": "pass", "defaults": stable, "files": count, "rounds": 30, "configuration_scope": "explicit owned packaged system configuration; ambient machine overrides excluded", "configuration_sha256": acceptHash(configuration)})
		}
	})
	if r["status"] != "pass" {
		results = append(results, r)
	}
	return acceptReport(a, o, record{"status": acceptStatus(results), "scope": "native index decode/preload/name-hash and read-only grep; no compression or checkout worker changes", "cpu_affinity_constraint_supported": runtime.GOOS != "darwin", "seconds": time.Since(started).Seconds(), "personal_profile_accessed": false, "results": results})
}
func init() {
	register("native-read-threads-acceptance", "CPU budget, bounded workers, inherited affinity and read consistency", "output system-config", nativeReadThreads)
}
