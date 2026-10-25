// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var resourceObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var resourcePackThreads = regexp.MustCompile(`Delta compression using up to ([0-9]+) threads`)

func maintenanceChildStarts(data []byte) (int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	count, events := 0, 0
	for scanner.Scan() {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if e := json.Unmarshal(scanner.Bytes(), &event); e != nil {
			return 0, fmt.Errorf("invalid resource Trace2 event: %w", e)
		}
		events++
		if event.Event != "child_start" {
			continue
		}
		for i := 0; i+1 < len(event.Argv); i++ {
			if event.Argv[i] == "maintenance" && event.Argv[i+1] == "run" {
				count++
				break
			}
		}
	}
	if events == 0 {
		return 0, fmt.Errorf("resource Trace2 event stream is empty")
	}
	return count, scanner.Err()
}

func lfsConcurrentTransfers(data []byte) (int, error) {
	count, matches := 0, 0
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		value, found := strings.CutPrefix(line, "ConcurrentTransfers=")
		if !found {
			continue
		}
		parsed, e := strconv.Atoi(value)
		if e != nil || parsed < 1 || strconv.Itoa(parsed) != value {
			return 0, fmt.Errorf("invalid actual LFS ConcurrentTransfers")
		}
		count, matches = parsed, matches+1
	}
	if matches != 1 {
		return 0, fmt.Errorf("actual LFS env must report ConcurrentTransfers exactly once")
	}
	return count, nil
}

func packProgressThreads(data []byte) (int, error) {
	rows := resourcePackThreads.FindAllSubmatch(data, -1)
	if len(rows) != 1 {
		return 0, fmt.Errorf("real pack must report its delta compression thread budget exactly once")
	}
	return strconv.Atoi(string(rows[0][1]))
}

func packObjectsMatch(revisions, verified []byte) (int, error) {
	wanted := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(revisions)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !resourceObjectID.MatchString(fields[0]) {
			return 0, fmt.Errorf("invalid expected object inventory")
		}
		wanted[fields[0]] = true
	}
	if len(wanted) == 0 {
		return 0, fmt.Errorf("empty expected object inventory")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(verified), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !resourceObjectID.MatchString(fields[0]) {
			continue
		}
		if len(fields) < 5 || !wanted[fields[0]] || seen[fields[0]] {
			return 0, fmt.Errorf("unexpected, duplicate or malformed verified pack object")
		}
		switch fields[1] {
		case "blob", "tree", "commit", "tag":
		default:
			return 0, fmt.Errorf("invalid verified pack object type")
		}
		for _, field := range fields[2:5] {
			if _, e := strconv.ParseUint(field, 10, 64); e != nil {
				return 0, fmt.Errorf("invalid verified pack object dimensions")
			}
		}
		seen[fields[0]] = true
	}
	if len(seen) != len(wanted) {
		return 0, fmt.Errorf("verified pack omitted expected objects")
	}
	return len(seen), nil
}

type resourceLFS struct {
	path, bin, helperDir, sha256 string
}

func ownedResourceLFS(git string) (resourceLFS, error) {
	resolved, e := filepath.EvalSymlinks(git)
	if e != nil {
		return resourceLFS{}, e
	}
	resolved, e = filepath.Abs(resolved)
	if e != nil {
		return resourceLFS{}, e
	}
	root := filepath.Dir(resolved)
	for depth := 0; depth < 8; depth++ {
		manifestPath := filepath.Join(root, "git-next-distribution.json")
		if exists(manifestPath) {
			manifest, e := load(manifestPath)
			if e != nil {
				return resourceLFS{}, e
			}
			if number(manifest, "schema_version") != 1 || manifest["product"] != "git-next" || manifest["engine"] != "source-built-git-next-native-core" {
				return resourceLFS{}, fmt.Errorf("resource probe requires owned native distribution identity")
			}
			files := object(manifest, "files")
			verify := func(p string) error {
				rel, e := filepath.Rel(root, p)
				if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
					return fmt.Errorf("resource probe helper escapes owned native distribution")
				}
				entry := rec(files[filepath.ToSlash(rel)])
				if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(stringValue(entry, "sha256")) {
					return fmt.Errorf("resource probe helper lacks manifest SHA256")
				}
				return requireHash(p, entry["sha256"])
			}
			if e = verify(resolved); e != nil {
				return resourceLFS{}, e
			}
			suffix := ""
			helperMember := "runtime/usr/bin/git-lfs"
			if runtime.GOOS == "windows" {
				suffix = ".exe"
				switch manifest["platform"] {
				case "windows-x64":
					helperMember = "runtime/mingw64/bin/git-lfs.exe"
				case "windows-arm64":
					helperMember = "runtime/clangarm64/bin/git-lfs.exe"
				default:
					return resourceLFS{}, fmt.Errorf("resource probe helper platform differs")
				}
			}
			bin := filepath.Join(root, "bin")
			helperPath := filepath.Join(root, filepath.FromSlash(helperMember))
			for _, name := range []string{filepath.Join(bin, "git"+suffix), helperPath} {
				path, e := filepath.EvalSymlinks(name)
				if e != nil {
					return resourceLFS{}, e
				}
				if e = verify(path); e != nil {
					return resourceLFS{}, e
				}
			}
			hash, e := digest(helperPath)
			return resourceLFS{path: helperPath, bin: bin, helperDir: filepath.Dir(helperPath), sha256: hash}, e
		}
		next := filepath.Dir(root)
		if next == root {
			break
		}
		root = next
	}
	return resourceLFS{}, fmt.Errorf("resource probe requires an exact manifest-bound bundled LFS helper; PATH fallback is refused")
}

func defaultResourceBehavior(git string, d defaults) ([]check, error) {
	if _, e := d.resources(); e != nil {
		return nil, e
	}
	var e error
	if !strings.ContainsAny(git, `/\`) {
		git, e = exec.LookPath(git)
	} else {
		git, e = absolute(git)
	}
	if e != nil {
		return nil, e
	}
	helper, e := ownedResourceLFS(git)
	if e != nil {
		return nil, e
	}
	root, e := os.MkdirTemp("", "gn-resource-defaults-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	if e = os.Mkdir(home, 0700); e != nil {
		return nil, e
	}
	env := append(cleanEnvironment(home), "GIT_AUTHOR_NAME=Resource Fixture", "GIT_COMMITTER_NAME=Resource Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_NEXT_LANGUAGE=english")
	values := map[string]string{}
	for i, entry := range d.entries {
		key := entry.section + "." + entry.key
		values[strings.ToLower(key)] = entry.value
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, key), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, entry.value))
	}
	env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(d.entries)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run := func(dir string, extra map[string]string, args ...string) (result, error) {
		// Keep the explicit-enabled control synchronous; it must not leave a daemon.
		args = append([]string{"-c", "maintenance.autoDetach=false"}, args...)
		r, e := runProcess(ctx, dir, fixtureEnv(env, extra), nil, git, args...)
		if e != nil {
			return r, e
		}
		if r.code != 0 {
			return r, fmt.Errorf("isolated resource default command failed (exit %d)", r.code)
		}
		return r, nil
	}
	repo := filepath.Join(root, "repository")
	if _, e = run(root, nil, "init", "-q", repo); e != nil {
		return nil, e
	}
	traced := func(name string, args ...string) (int, error) {
		path := filepath.Join(root, name+".trace.json")
		if _, e := run(repo, map[string]string{"GIT_TRACE2_EVENT": path}, args...); e != nil {
			return 0, e
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return 0, e
		}
		return maintenanceChildStarts(data)
	}
	if e = write(filepath.Join(repo, "probe.txt"), []byte("first\n"), 0600); e != nil {
		return nil, e
	}
	if _, e = run(repo, nil, "add", "probe.txt"); e != nil {
		return nil, e
	}
	commitOff, e := traced("commit-disabled", "commit", "-q", "-m", "resource baseline")
	if e != nil {
		return nil, e
	}
	remote := filepath.Join(root, "remote.git")
	if _, e = run(root, nil, "clone", "--bare", "-q", repo, remote); e != nil {
		return nil, e
	}
	fetchOff, e := traced("fetch-disabled", "fetch", "-q", remote)
	if e != nil {
		return nil, e
	}
	if e = write(filepath.Join(repo, "probe.txt"), []byte("second\n"), 0600); e != nil {
		return nil, e
	}
	if _, e = run(repo, nil, "add", "probe.txt"); e != nil {
		return nil, e
	}
	commitOn, e := traced("commit-enabled", "-c", "maintenance.auto=true", "commit", "-q", "-m", "resource explicit enable")
	if e != nil {
		return nil, e
	}
	fetchOn, e := traced("fetch-enabled", "-c", "maintenance.auto=true", "fetch", "-q", remote)
	if e != nil {
		return nil, e
	}
	if _, e = run(repo, nil, "maintenance", "run", "--task=commit-graph"); e != nil {
		return nil, e
	}
	if _, e = run(repo, nil, "commit-graph", "verify"); e != nil {
		return nil, e
	}
	checks := []check{{"maintenance.auto", commitOff == 0 && fetchOff == 0 && commitOn > 0 && fetchOn > 0, fmt.Sprintf("Trace2 maintenance children commit/fetch default=%d/%d explicit-true=%d/%d; manual commit-graph maintenance verified", commitOff, fetchOff, commitOn, fetchOn)}}
	for i := range 96 {
		data := strings.Repeat("shared Unity/UE resource fixture line\n", 128) + strconv.Itoa(i) + "\n"
		if e = write(filepath.Join(repo, fmt.Sprintf("asset-%03d.txt", i)), []byte(data), 0600); e != nil {
			return nil, e
		}
	}
	if _, e = run(repo, nil, "add", "-A"); e != nil {
		return nil, e
	}
	if _, e = run(repo, nil, "commit", "-q", "-m", "small real pack resource fixture"); e != nil {
		return nil, e
	}
	revisions, e := run(repo, nil, "rev-list", "--objects", "--all")
	if e != nil {
		return nil, e
	}
	pack := func(name string, overrides []string) (int, int, error) {
		prefix := filepath.Join(root, name)
		args := append(append([]string{}, overrides...), "pack-objects", "--all", "--progress", prefix)
		r, e := run(repo, nil, args...)
		if e != nil {
			return 0, 0, e
		}
		workers, e := packProgressThreads(r.stderr)
		if e != nil {
			return 0, 0, e
		}
		oid := strings.TrimSpace(string(r.stdout))
		if !resourceObjectID.MatchString(oid) {
			return 0, 0, fmt.Errorf("real resource pack did not return an object identity")
		}
		verified, e := run(repo, nil, "verify-pack", "-v", prefix+"-"+oid+".idx")
		if e != nil {
			return 0, 0, e
		}
		objects, e := packObjectsMatch(revisions.stdout, verified.stdout)
		return workers, objects, e
	}
	workers, objects, e := pack("default-pack", nil)
	if e != nil {
		return nil, e
	}
	overrides := []string{"-c", "pack.threads=4", "-c", "pack.windowMemory=8m", "-c", "pack.deltaCacheSize=8m", "-c", "core.deltaBaseCacheLimit=8m"}
	overridden, overrideObjects, e := pack("override-pack", overrides)
	if e != nil {
		return nil, e
	}
	expectedWorkers, e := strconv.Atoi(values["pack.threads"])
	if e != nil {
		return nil, e
	}
	checks = append(checks, check{"pack.threads", workers == expectedWorkers && overridden == 4 && objects == overrideObjects, fmt.Sprintf("real pack progress budget=%d, explicit=4; verify-pack validated the same %d complete objects", workers, objects)})
	for _, key := range []string{"pack.windowMemory", "pack.deltaCacheSize", "core.deltaBaseCacheLimit"} {
		expected, e := parseDefaultByteSize(values[strings.ToLower(key)])
		if e != nil {
			return nil, e
		}
		readInt := func(args []string) (uint64, error) {
			r, e := run(repo, nil, append(append([]string{}, args...), "config", "--type=int", "--get", key)...)
			if e != nil {
				return 0, e
			}
			return strconv.ParseUint(strings.TrimSpace(string(r.stdout)), 10, 64)
		}
		actual, e := readInt(nil)
		if e != nil {
			return nil, e
		}
		control, e := readInt(overrides)
		if e != nil {
			return nil, e
		}
		checks = append(checks, check{key, actual == expected && control == 8<<20, fmt.Sprintf("soft window/cache knob resolved to %d bytes; explicit control=%d bytes; both real packs verified; not a process RSS ceiling", actual, control)})
	}
	if _, e = run(repo, nil, "gc", "--quiet"); e != nil {
		return nil, e
	}
	if _, e = run(repo, nil, "fsck", "--full"); e != nil {
		return nil, e
	}
	checks[0].Detail += "; manual gc and full fsck passed"
	lfsEnv := fixtureEnv(env, map[string]string{"PATH": helper.bin + string(os.PathListSeparator) + helper.helperDir, "GIT_EXEC_PATH": helper.helperDir})
	actual, e := runProcess(ctx, repo, lfsEnv, nil, helper.path, "env")
	if e != nil || actual.code != 0 {
		return nil, fmt.Errorf("exact bundled LFS env failed")
	}
	defaultLFS, e := lfsConcurrentTransfers(actual.stdout)
	if e != nil {
		return nil, e
	}
	control, e := runProcess(ctx, repo, lfsEnv, nil, git, "-c", "lfs.concurrenttransfers=4", "lfs", "env")
	if e != nil || control.code != 0 {
		return nil, fmt.Errorf("owned Git LFS explicit concurrency control failed")
	}
	controlLFS, e := lfsConcurrentTransfers(control.stdout)
	if e != nil {
		return nil, e
	}
	if e = requireHash(helper.path, helper.sha256); e != nil {
		return nil, fmt.Errorf("owned LFS helper changed during resource probe")
	}
	wantedLFS, e := strconv.Atoi(values["lfs.concurrenttransfers"])
	if e != nil {
		return nil, e
	}
	checks = append(checks, check{"lfs.concurrenttransfers", defaultLFS == wantedLFS && controlLFS == 4, fmt.Sprintf("exact bundled LFS env ConcurrentTransfers=%d; Git -c control=%d; helper sha256=%s", defaultLFS, controlLFS, helper.sha256)})
	return checks, nil
}
