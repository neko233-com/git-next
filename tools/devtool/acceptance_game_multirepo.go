// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type gameMultiRepo struct {
	id, engine, changedPath string
	fixture                 *gameMultiCLI
	files                   map[string][]byte
	ignored                 []string
	initialHead             string
	initialRefs             map[string]string
	verified                bool
}

type gameMultiCLI struct {
	*cliFixture
	timeout time.Duration
	phase   string
	history []record
}

func gameMultiTraceDiagnostic(path string) record {
	data, e := os.ReadFile(path)
	if e != nil {
		return record{"trace2_available": false}
	}
	children := map[string]int{}
	last, exited := "", false
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var event record
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		last = stringValue(event, "event")
		if last == "exit" {
			exited = true
		}
		if last == "child_start" {
			argv := array(event, "argv")
			if len(argv) == 0 {
				continue
			}
			name := filepath.Base(strings.ReplaceAll(fmt.Sprint(argv[0]), "\\", "/"))
			if len(argv) > 1 {
				switch fmt.Sprint(argv[1]) {
				case "maintenance", "filter-process", "clean", "smudge", "pre-push", "post-commit", "commit-graph", "pack-objects", "index-pack", "upload-pack", "receive-pack", "lfs":
					name += " " + fmt.Sprint(argv[1])
				}
			}
			children[name]++
		}
	}
	return record{"trace2_available": true, "trace2_sha256": digestBytes(data), "trace2_last_event": last, "trace2_exit_observed": exited, "trace2_child_commands": children}
}

func (f *gameMultiCLI) run(where string, reference, ok bool, input []byte, extra map[string]string, args ...string) []byte {
	acceptCheck(!reference, "game fixture never dispatches a reference client")
	timeout := f.timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	f.commands++
	trace := extra["GIT_TRACE2_EVENT"]
	if trace == "" {
		trace = filepath.Join(f.root, "traces", filepath.Base(f.repo), fmt.Sprintf("command-%04d.jsonl", f.commands))
		acceptMust(os.MkdirAll(filepath.Dir(trace), 0700))
		extra = gameMultiCopyStrings(extra)
		extra["GIT_TRACE2_EVENT"] = filepath.ToSlash(trace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	value, e := runProcess(ctx, where, fixtureEnv(f.env, extra), input, f.binary, args...)
	classification := "success"
	if ctx.Err() != nil {
		classification = "deadline-exceeded"
	} else if e != nil {
		classification = "runner-error"
	} else if value.code != 0 {
		classification = "ordinary-exit"
	}
	row := record{"command": acceptCommandDescription(args), "phase": f.phase, "elapsed_ms": float64(time.Since(started).Nanoseconds()) / 1e6, "timeout_seconds": timeout.Seconds(), "classification": classification, "exit_code": value.code, "expected_nonzero": !ok, "stdout_bytes": len(value.stdout), "stderr_bytes": len(value.stderr)}
	for key, item := range gameMultiTraceDiagnostic(trace) {
		row[key] = item
	}
	f.history = append(f.history, row)
	acceptCheck(ctx.Err() == nil, "game command [%s] exceeded explicit %.0fs deadline", acceptCommandDescription(args), timeout.Seconds())
	acceptMust(e)
	acceptCheck((value.code == 0) == ok, "game command [%s] exited %d: %.700s", acceptCommandDescription(args), value.code, fixtureCredentialText.ReplaceAllString(string(value.stderr), "<redacted>"))
	if !ok {
		acceptCheck(value.code == 1 || value.code == 2 || value.code == 128 || value.code == 129, "game command abnormal exit %d", value.code)
	}
	return value.stdout
}

func gameMultiCopyStrings(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source)+1)
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func (f *gameMultiCLI) G(args ...string) []byte {
	return f.run(f.repo, false, true, nil, nil, args...)
}

func (f *gameMultiCLI) Bad(args ...string) []byte {
	return f.run(f.repo, false, false, nil, nil, args...)
}

func (f *gameMultiCLI) At(where string, args ...string) []byte {
	return f.run(where, false, true, nil, nil, args...)
}

func gameMultiEnvironment(root, binary, gitconfig string) []string {
	home, temporary := filepath.Join(root, "home"), filepath.Join(root, "tmp")
	acceptMust(os.MkdirAll(home, 0700))
	acceptMust(os.MkdirAll(temporary, 0700))
	env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_CONFIG_NOSYSTEM": "0", "GIT_CONFIG_SYSTEM": gitconfig, "GCM_INTERACTIVE": "never", "GIT_NEXT_LANGUAGE": "english", "XDG_DATA_HOME": filepath.Join(home, ".local/share"), "XDG_CACHE_HOME": filepath.Join(home, ".cache"), "XDG_STATE_HOME": filepath.Join(home, ".local/state"), "TMP": temporary, "TEMP": temporary, "TMPDIR": temporary})
	packageRoot := filepath.Dir(filepath.Dir(binary))
	ownedPath := []string{filepath.Dir(binary), filepath.Join(packageRoot, "runtime/usr/bin"), filepath.Join(packageRoot, "runtime/bin")}
	if runtime.GOOS == "windows" {
		ownedPath = append(ownedPath, filepath.Join(os.Getenv("SystemRoot"), "System32"))
	} else {
		ownedPath = append(ownedPath, "/usr/bin", "/bin")
	}
	return fixtureEnv(env, map[string]string{"PATH": strings.Join(ownedPath, string(os.PathListSeparator))})
}

type gameMultiInterval struct {
	start, end time.Time
}

type gameMultiTrace struct {
	sha256              string
	events, maintenance int
	root                gameMultiInterval
}

func gameMultiBlob(data []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func gameMultiParseEntries(data []byte, tree bool) map[string]string {
	entries := map[string]string{}
	for _, entry := range bytes.Split(data, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		header, name, ok := bytes.Cut(entry, []byte{'\t'})
		fields := bytes.Fields(header)
		acceptCheck(ok && len(fields) == 3 && string(fields[0]) == "100644", "invalid game fixture tree/index record")
		acceptCheck(entries[string(name)] == "", "duplicate game fixture tree/index member")
		if tree {
			acceptCheck(string(fields[1]) == "blob", "game fixture tree contains non-blob")
			entries[string(name)] = string(fields[2])
		} else {
			acceptCheck(string(fields[2]) == "0", "game fixture index contains unmerged stage")
			entries[string(name)] = string(fields[1])
		}
	}
	return entries
}

func gameMultiReadTrace(path string) gameMultiTrace {
	data := acceptBytes(path)
	value := gameMultiTrace{sha256: digestBytes(data)}
	rootSID := ""
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event record
		acceptMust(json.Unmarshal(line, &event))
		value.events++
		kind, sid := stringValue(event, "event"), stringValue(event, "sid")
		if kind == "start" && rootSID == "" {
			rootSID = sid
			var e error
			value.root.start, e = time.Parse(time.RFC3339Nano, stringValue(event, "time"))
			acceptMust(e)
		}
		if kind == "exit" && sid == rootSID {
			var e error
			value.root.end, e = time.Parse(time.RFC3339Nano, stringValue(event, "time"))
			acceptMust(e)
		}
		if kind != "child_start" {
			continue
		}
		for _, arg := range array(event, "argv") {
			word := strings.ToLower(filepath.Base(strings.ReplaceAll(fmt.Sprint(arg), "\\", "/")))
			if word == "maintenance" || word == "git-maintenance" || word == "git-maintenance.exe" {
				value.maintenance++
				break
			}
		}
	}
	acceptCheck(!value.root.start.IsZero() && !value.root.end.Before(value.root.start), "missing complete root Trace2 process interval")
	return value
}

func gameMultiPeak(intervals []gameMultiInterval) int {
	type edge struct {
		at    time.Time
		delta int
	}
	edges := make([]edge, 0, len(intervals)*2)
	for _, interval := range intervals {
		edges = append(edges, edge{interval.start, 1}, edge{interval.end, -1})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta
		}
		return edges[i].at.Before(edges[j].at)
	})
	active, peak := 0, 0
	for _, edge := range edges {
		active += edge.delta
		peak = max(peak, active)
	}
	return peak
}

func (repo *gameMultiRepo) traced(label string, args ...string) ([]byte, gameMultiTrace) {
	path := filepath.Join(repo.fixture.root, "traces", repo.id+"-"+label+".jsonl")
	acceptMust(os.MkdirAll(filepath.Dir(path), 0700))
	acceptCheck(!exists(path), "Trace2 output must be new")
	repo.fixture.phase = label
	output := repo.fixture.run(repo.fixture.repo, false, true, nil, map[string]string{"GIT_TRACE2_EVENT": filepath.ToSlash(path), "GIT_TRACE2_EVENT_NESTING": "10"}, args...)
	return output, gameMultiReadTrace(path)
}

func (repo *gameMultiRepo) put(name string, data []byte) {
	// Every generated tracked member has canonical LF bytes and a regular-file mode.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	repo.fixture.Write(name, data)
	repo.files[name] = data
	repo.verified = false
}

func (repo *gameMultiRepo) project(prefix, engine string, count int, profiles map[string][]byte) {
	join := func(name string) string { return filepath.ToSlash(filepath.Join(prefix, name)) }
	repo.put(join(".gitignore"), profiles[engine+".gitignore"])
	repo.put(join(".gitattributes"), profiles[engine+".gitattributes"])
	var dataRoot, extension string
	var cache []string
	if engine == "unity" {
		dataRoot, extension = "Assets/Scripts", ".cs"
		cache = []string{"Library", "Temp", "Obj", "Logs", "UserSettings", "Builds", ".vs"}
		repo.put(join("Assets/Scenes/Main.unity"), []byte("%YAML 1.1\nScene: "+repo.id+"\n"))
		repo.put(join("Assets/Scenes/Main.unity.meta"), []byte("fileFormatVersion: 2\nguid: 00000000000000000000000000000001\n"))
		repo.put(join("ProjectSettings/ProjectVersion.txt"), []byte("m_EditorVersion: fixture\n"))
		repo.put(join("Packages/manifest.json"), []byte("{\"dependencies\":{}}\n"))
	} else {
		dataRoot, extension = "Source/Game", ".cpp"
		cache = []string{"Binaries", "DerivedDataCache", "Intermediate", "Saved", ".vs"}
		repo.put(join("Game.uproject"), []byte("{\"FileVersion\":3}\n"))
		repo.put(join("Build/Required.ini"), []byte("[Fixture]\nKeep=true\n"))
		repo.put(join("Config/DefaultEngine.ini"), []byte("[Fixture]\nProject="+repo.id+"\n"))
	}
	for i := 0; i < count; i++ {
		name := join(fmt.Sprintf("%s/d%02d/file-%06d%s", dataRoot, i%16, i, extension))
		repo.put(name, []byte(fmt.Sprintf("// fixture %s %s file %d\n", repo.id, engine, i)))
		if repo.changedPath == "" {
			repo.changedPath = name
		}
	}
	for _, name := range cache {
		name = join(name + "/generated-cache.bin")
		repo.fixture.Write(name, []byte("ignored generated cache\n"))
		repo.ignored = append(repo.ignored, name)
	}
}

func (repo *gameMultiRepo) refs() map[string]string {
	refs := map[string]string{}
	for _, line := range bytes.Split(bytes.TrimSpace(repo.fixture.G("show-ref")), []byte{'\n'}) {
		parts := bytes.Fields(line)
		acceptCheck(len(parts) == 2, "invalid fixture ref record")
		refs[string(parts[1])] = string(parts[0])
	}
	return refs
}

func (repo *gameMultiRepo) verify(checkHead bool) {
	acceptEqual(bytes.TrimSpace(repo.fixture.G("config", "--local", "--get", "fixture.project")), []byte(repo.id), "repo-local identity")
	gitDir := strings.TrimSpace(string(repo.fixture.G("rev-parse", "--absolute-git-dir")))
	actual, e := os.Stat(gitDir)
	acceptMust(e)
	expected, e := os.Stat(filepath.Join(repo.fixture.repo, ".git"))
	acceptMust(e)
	acceptCheck(os.SameFile(actual, expected), "cross-repository Git directory")
	index := gameMultiParseEntries(repo.fixture.G("ls-files", "--stage", "-z"), false)
	acceptCheck(len(index) == len(repo.files), "%s index member count %d != %d", repo.id, len(index), len(repo.files))
	var tree map[string]string
	if checkHead {
		tree = gameMultiParseEntries(repo.fixture.G("ls-tree", "-r", "-z", "--full-tree", "HEAD"), true)
		acceptCheck(len(tree) == len(repo.files), "%s tree member count", repo.id)
	}
	for name, data := range repo.files {
		oid := gameMultiBlob(data)
		acceptCheck(index[name] == oid, "%s index wrong blob for %s", repo.id, name)
		if checkHead {
			acceptCheck(tree[name] == oid, "%s HEAD wrong blob for %s", repo.id, name)
		}
		acceptEqual(acceptBytes(filepath.Join(repo.fixture.repo, filepath.FromSlash(name))), data, "generated worktree member")
	}
	for _, name := range repo.ignored {
		acceptCheck(index[name] == "", "generated engine cache entered index")
		acceptEqual(bytes.TrimSpace(repo.fixture.G("check-ignore", "--", name)), []byte(name), "project-root ignore")
	}
	acceptCheck(!exists(filepath.Join(repo.fixture.repo, ".git/index.lock")), "%s left index lock", repo.id)
	repo.verified = checkHead
}

func gameMultiParallel(repos []*gameMultiRepo, phase string, operation func(*gameMultiRepo) gameMultiTrace) (record, []record) {
	start := make(chan struct{})
	semaphore := make(chan struct{}, 4)
	results, traces := make([]record, len(repos)), make([]gameMultiTrace, len(repos))
	var group sync.WaitGroup
	for i, repo := range repos {
		group.Add(1)
		go func(i int, repo *gameMultiRepo) {
			defer group.Done()
			<-start
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			results[i] = acceptCapture(phase+"/"+repo.id, func() { traces[i] = operation(repo) })
		}(i, repo)
	}
	close(start)
	group.Wait()
	intervals := []gameMultiInterval{}
	maintenance, events := 0, 0
	traceIDs := record{}
	for i, trace := range traces {
		if !trace.root.start.IsZero() && !trace.root.end.IsZero() {
			intervals = append(intervals, trace.root)
		}
		maintenance += trace.maintenance
		events += trace.events
		traceIDs[repos[i].id] = trace.sha256
	}
	return record{"phase": phase, "status": acceptStatus(results), "trace2_root_process_overlap_peak": gameMultiPeak(intervals), "trace2_events": events, "maintenance_child_count": maintenance, "trace2_sha256": traceIDs}, results
}

func nativeGameMultirepo(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if e = o.require("output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("result output must be new")
	}
	files, e := o.integer("files", 64)
	if e != nil || files < 1 || files > 100000 {
		return fmt.Errorf("files must be 1..100000 per engine project")
	}
	projects, e := o.integer("projects", 4)
	if e != nil || projects < 2 || projects > 16 {
		return fmt.Errorf("projects must be 2..16 independent repositories")
	}
	seconds, e := o.integer("command-timeout", 90)
	if e != nil || seconds < 1 || seconds > 600 {
		return fmt.Errorf("command-timeout must be 1..600 seconds")
	}
	gitconfig := o.get("gitconfig")
	if gitconfig == "" {
		gitconfig = filepath.Join(filepath.Dir(filepath.Dir(binary)), "runtime/etc/gitconfig")
	}
	gitconfig, e = filepath.Abs(gitconfig)
	if e != nil {
		return e
	}
	configHash, e := digest(gitconfig)
	if e != nil {
		return e
	}
	profiles := map[string][]byte{}
	profileHashes := record{}
	for _, name := range []string{"unity.gitignore", "unity.gitattributes", "unreal.gitignore", "unreal.gitattributes"} {
		data, e := os.ReadFile(a.path("docs/game-profiles", name))
		if e != nil {
			return e
		}
		profiles[name], profileHashes[name] = data, digestBytes(data)
	}
	root, e := os.MkdirTemp("", "git-next-game-multirepo-")
	if e != nil {
		return e
	}
	retained := false
	defer func() {
		if !retained {
			_ = os.RemoveAll(root)
		}
	}()
	started, binaryHash := time.Now(), acceptHash(binary)
	env := gameMultiEnvironment(root, binary, gitconfig)
	results, phases, repos := []record{}, []record{}, []*gameMultiRepo{}
	add := func(name string, fn func()) bool {
		value := acceptCapture(name, fn)
		results = append(results, value)
		return value["status"] == "pass"
	}
	ready := add("isolated-game-project-fixtures", func() {
		for i := 0; i <= projects; i++ {
			engine, id := "unity", fmt.Sprintf("project-%02d", i)
			if i%2 != 0 {
				engine = "unreal"
			}
			if i == projects {
				engine, id = "monorepo", "dual-engine-monorepo"
			}
			repo := &gameMultiRepo{id: id, engine: engine, files: map[string][]byte{}, fixture: &gameMultiCLI{cliFixture: &cliFixture{root: root, repo: filepath.Join(root, id), binary: binary, env: env}, timeout: time.Duration(seconds) * time.Second, phase: "setup"}}
			repos = append(repos, repo)
			f := repo.fixture
			f.At(root, "init", "-q", "--object-format=sha1", "-b", "main", f.repo)
			f.G("config", "--local", "fixture.project", id)
			if engine == "monorepo" {
				repo.project("UnityProject", "unity", files, profiles)
				repo.project("UnrealProject", "unreal", files, profiles)
				// These are outside either project root and must not inherit its anchored ignores.
				repo.put("Library/keep-outside-unity.txt", []byte("monorepo root data\n"))
				repo.put("Binaries/keep-outside-unreal.txt", []byte("monorepo root data\n"))
				repo.put("UnityProject/Binaries/keep-other-engine.txt", []byte("Unity project data\n"))
				repo.put("UnrealProject/Library/keep-other-engine.txt", []byte("Unreal project data\n"))
			} else {
				repo.project("", engine, files, profiles)
			}
			f.G("add", "-A")
			f.G("commit", "-qm", "initial "+id)
			repo.initialHead = strings.TrimSpace(string(f.G("rev-parse", "HEAD")))
			f.G("branch", "isolation/"+id)
			bare := filepath.Join(root, id+"-remote.git")
			f.At(root, "init", "-q", "--bare", "-b", "main", bare)
			f.G("remote", "add", "origin", bare)
			f.G("push", "-q", "-u", "origin", "main")
			// Modern fetch may create origin/HEAD; include that ordinary state in the baseline.
			f.G("fetch", "--no-tags", "origin")
			repo.initialRefs = repo.refs()
			repo.verify(true)
		}
	})
	if ready {
		add("candidate-defaults-and-explicit-resource-overrides", func() {
			defaults := map[string]string{"maintenance.auto": "false", "pack.threads": "2", "pack.windowmemory": "32m", "pack.deltacachesize": "32m", "core.deltabasecachelimit": "32m", "lfs.concurrenttransfers": "2", "core.fscache": "false", "core.fsmonitor": "false", "core.preloadindex": "false", "index.threads": "1", "grep.threads": "1"}
			for _, repo := range repos {
				for key, expected := range defaults {
					acceptEqual(bytes.TrimSpace(repo.fixture.G("config", "--get", key)), []byte(expected), "candidate default "+key)
				}
				for key, override := range map[string]string{"pack.threads": "3", "lfs.concurrenttransfers": "4"} {
					repo.fixture.G("config", "--local", key, "1")
					acceptEqual(bytes.TrimSpace(repo.fixture.G("config", "--get", key)), []byte("1"), "repository resource override")
					acceptEqual(bytes.TrimSpace(repo.fixture.G("-c", key+"="+override, "config", "--get", key)), []byte(override), "explicit resource override")
					acceptEqual(bytes.TrimSpace(repo.fixture.G("config", "--get", key)), []byte("1"), "explicit override leaked into repository")
				}
				if repo.engine == "monorepo" {
					for _, subdir := range []string{"UnityProject", "UnrealProject"} {
						acceptEqual(bytes.TrimSpace(repo.fixture.At(filepath.Join(repo.fixture.repo, subdir), "config", "--local", "--get", "fixture.project")), []byte(repo.id), "monorepo shares one Git config")
					}
				}
			}
		})
		for _, phase := range []string{"status", "diff", "add", "commit", "fetch"} {
			phase := phase
			if phase == "status" {
				for _, repo := range repos {
					repo.put(repo.changedPath, []byte("// parallel edit owned by "+repo.id+"\n"))
				}
			}
			summary, cases := gameMultiParallel(repos, phase, func(repo *gameMultiRepo) gameMultiTrace {
				var output []byte
				var trace gameMultiTrace
				switch phase {
				case "status":
					output, trace = repo.traced(phase, "status", "--porcelain=v1", "-z", "--untracked-files=all")
					acceptEqual(output, []byte(" M "+repo.changedPath+"\x00"), "parallel status")
				case "diff":
					output, trace = repo.traced(phase, "diff", "--no-ext-diff", "--no-textconv", "--text", "--", repo.changedPath)
					acceptCheck(bytes.Contains(output, []byte("+// parallel edit owned by "+repo.id+"\n")), "parallel diff lost repository edit")
					acceptEqual(repo.fixture.G("diff", "--name-only", "-z"), []byte(repo.changedPath+"\x00"), "parallel diff path isolation")
				case "add":
					_, trace = repo.traced(phase, "add", "--", repo.changedPath)
					repo.verify(false)
				case "commit":
					_, trace = repo.traced(phase, "commit", "-qm", "parallel "+repo.id)
					acceptCheck(trace.maintenance == 0, "ordinary commit spawned maintenance")
					repo.verify(true)
				case "fetch":
					_, trace = repo.traced(phase, "fetch", "--no-tags", "origin")
					acceptCheck(trace.maintenance == 0, "ordinary fetch spawned maintenance")
					repo.verify(true)
				}
				return trace
			})
			phases, results = append(phases, summary), append(results, cases...)
		}
		add("actual-multirepository-process-overlap", func() {
			for _, phase := range phases {
				peak := int(number(phase, "trace2_root_process_overlap_peak"))
				acceptCheck(peak >= 2 && peak <= 4, "%s actual Trace2 process overlap %d outside 2..4", phase["phase"], peak)
			}
		})
		add("repository-refs-index-and-config-stay-independent", func() {
			for _, repo := range repos {
				current := repo.refs()
				acceptCheck(len(current) == len(repo.initialRefs), "cross-repository ref appeared")
				for name, old := range repo.initialRefs {
					if name == "refs/heads/main" {
						acceptCheck(current[name] != old, "parallel commit did not advance own main")
					} else {
						acceptCheck(current[name] == old, "unrelated ref changed")
					}
				}
				acceptEqual(repo.fixture.G("status", "--porcelain"), nil, "clean parallel repository")
			}
		})
		add("one-index-lock-does-not-block-another-repository", func() {
			locked, other := repos[0], repos[1]
			before := acceptHash(filepath.Join(locked.fixture.repo, ".git/index"))
			head := locked.fixture.G("rev-parse", "HEAD")
			lockPath := filepath.Join(locked.fixture.repo, ".git/index.lock")
			lock, e := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			acceptMust(e)
			_, e = lock.WriteString("owned game fixture index lock\n")
			acceptMust(e)
			acceptMust(lock.Close())
			defer os.Remove(lockPath)
			_, checks := gameMultiParallel([]*gameMultiRepo{locked, other}, "lock-isolation", func(repo *gameMultiRepo) gameMultiTrace {
				if repo == locked {
					repo.fixture.Bad("add", "--", repo.changedPath)
					return gameMultiTrace{}
				}
				repo.put(repo.changedPath, []byte("// independent commit while another index is locked\n"))
				repo.fixture.G("add", "--", repo.changedPath)
				_, trace := repo.traced("lock-independent-commit", "commit", "-qm", "independent index lock")
				acceptCheck(trace.maintenance == 0, "ordinary lock fixture commit spawned maintenance")
				repo.verify(true)
				return trace
			})
			acceptCheck(acceptStatus(checks) == "pass", "index lock isolation operation failed: %v", checks)
			acceptCheck(acceptHash(filepath.Join(locked.fixture.repo, ".git/index")) == before, "failed add changed locked index")
			acceptEqual(locked.fixture.G("rev-parse", "HEAD"), head, "failed add changed locked HEAD")
			acceptEqual(acceptBytes(lockPath), []byte("owned game fixture index lock\n"), "foreign index lock changed")
			acceptMust(os.Remove(lockPath))
		})
		add("manual-maintenance-remains-available", func() {
			repo := repos[0]
			head, index := repo.fixture.G("rev-parse", "HEAD"), acceptHash(filepath.Join(repo.fixture.repo, ".git/index"))
			_, trace := repo.traced("manual-maintenance", "maintenance", "run", "--task=commit-graph")
			acceptCheck(trace.events > 0, "manual maintenance did not execute")
			acceptEqual(repo.fixture.G("rev-parse", "HEAD"), head, "manual maintenance changed HEAD")
			acceptCheck(acceptHash(filepath.Join(repo.fixture.repo, ".git/index")) == index, "manual maintenance changed index")
		})
		add("final-exact-game-trees-and-no-index-locks", func() {
			for _, repo := range repos {
				repo.verify(true)
				repo.fixture.G("fsck", "--full")
			}
		})
	}
	add("input-binary-system-config-and-profiles-unchanged", func() {
		acceptCheck(acceptHash(binary) == binaryHash && acceptHash(gitconfig) == configHash, "acceptance input bytes changed")
		for name, expected := range profileHashes {
			acceptCheck(acceptHash(a.path("docs/game-profiles", name)) == expected, "game profile changed during acceptance")
		}
	})
	for _, value := range results {
		if failure, ok := value["error"].(string); ok {
			failure = strings.ReplaceAll(failure, root, "<fixture>")
			value["error"] = strings.ReplaceAll(failure, gitconfig, "<system-config>")
		}
	}
	tracked, ignored, commandCount := 0, 0, 0
	commandHistory := record{}
	for _, repo := range repos {
		if repo.verified {
			tracked += len(repo.files)
			ignored += len(repo.ignored)
		}
		commandCount += repo.fixture.commands
		commandHistory[repo.id] = repo.fixture.history
	}
	privateEvidence := ""
	privateBasename := ""
	if acceptStatus(results) != "pass" {
		retained = true
		privateEvidence = o.get("output") + ".private.json"
		privateBasename = filepath.Base(privateEvidence)
		if e := writeNewJSON(privateEvidence, record{"fixture_root": root, "trace_directory": filepath.Join(root, "traces"), "scope": "owned generated fixture retained after failure; contains no personal repository or credentials"}); e != nil {
			return e
		}
	}
	return acceptReport(a, o, record{"schema_version": 1, "status": acceptStatus(results), "product": "git-next", "binary_sha256": binaryHash, "system_gitconfig_sha256": configHash, "harness_sha256": acceptHash(a.path("tools/devtool/acceptance_game_multirepo.go")), "game_profile_sha256": profileHashes, "independent_repository_count": projects, "dual_engine_monorepository_count": 1, "engine_project_count": projects + 2, "files_per_engine_project": files, "tracked_files_verified": tracked, "ignored_cache_paths_verified": ignored, "parallel_command_limit": 4, "subprocess_timeout_seconds": seconds, "commands_executed": commandCount, "command_history": commandHistory, "elapsed_seconds": time.Since(started).Seconds(), "phases": phases, "cases": results, "failed_fixture_retained": retained, "private_evidence_basename": privateBasename, "configuration_scope": "one isolated shared fixture HOME, distinct repository configs; both monorepo project roots share one Git config", "resource_scope": "functional configuration precedence only; no memory limit or low-memory claim", "large_asset_memory_measured": false, "terminal_functional_tests_executed": false, "personal_repository_accessed": false, "personal_profiles_accessed": false, "system_installation_changed": false})
}

func init() {
	register("native-game-multirepo-acceptance", "Isolated concurrent Unity/Unreal repositories, monorepo ignores, resource overrides and maintenance policy", "output files projects gitconfig command-timeout", nativeGameMultirepo)
}
