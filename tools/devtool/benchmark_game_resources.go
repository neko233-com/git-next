// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type gameResourceLimits struct {
	processes int
	memory    uint64
	timeout   time.Duration
}

type gameResourceMemory struct {
	Available           bool    `json:"available"`
	Metric              string  `json:"metric"`
	Scope               string  `json:"scope"`
	PeakBytes           *uint64 `json:"peak_bytes"`
	PeakProcesses       uint32  `json:"peak_processes_observed"`
	SampleIntervalMS    int     `json:"sample_interval_ms"`
	Samples             uint64  `json:"samples"`
	MemoryLimitBytes    *uint64 `json:"memory_limit_bytes"`
	MemoryLimitEnforced bool    `json:"memory_limit_enforced"`
	ProcessLimit        int     `json:"process_limit"`
	ProcessLimitKernel  bool    `json:"process_limit_kernel_enforced"`
	Remaining           uint32  `json:"processes_before_cleanup"`
	CleanupOK           bool    `json:"cleanup_verified"`
	CleanupScope        string  `json:"cleanup_scope"`
	Error               string  `json:"error,omitempty"`
	UnavailableReason   string  `json:"unavailable_reason,omitempty"`
}

type gameResourceScope interface {
	start(*exec.Cmd) error
	stop() error
	finish() gameResourceMemory
}

type gameResourceCapture struct {
	hash   hash.Hash
	bytes  int64
	prefix []byte
	limit  int
}

func (c *gameResourceCapture) Write(p []byte) (int, error) {
	if c.hash == nil {
		c.hash = sha256.New()
	}
	_, _ = c.hash.Write(p)
	c.bytes += int64(len(p))
	if len(c.prefix) < c.limit {
		c.prefix = append(c.prefix, p[:min(len(p), c.limit-len(c.prefix))]...)
	}
	return len(p), nil
}

func (c *gameResourceCapture) digest() string {
	if c.hash == nil {
		return digestBytes(nil)
	}
	return hex.EncodeToString(c.hash.Sum(nil))
}

type gameResourceCommand struct {
	name, cwd, exe string
	args, env      []string
	prefix         int
}

type gameResourceResult struct {
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	ExitCode     int     `json:"exit_code"`
	Seconds      float64 `json:"seconds"`
	TimedOut     bool    `json:"timed_out"`
	Error        string  `json:"error,omitempty"`
	StdoutBytes  int64   `json:"stdout_bytes"`
	StdoutSHA256 string  `json:"stdout_sha256"`
	StdoutPrefix string  `json:"stdout_prefix,omitempty"`
	StderrBytes  int64   `json:"stderr_bytes"`
	StderrSHA256 string  `json:"stderr_sha256"`
	StderrPrefix string  `json:"stderr_prefix,omitempty"`
}

type gameResourcePhase struct {
	Profile             string               `json:"profile,omitempty"`
	Workload            string               `json:"workload,omitempty"`
	Mode                string               `json:"mode,omitempty"`
	Sample              int                  `json:"sample,omitempty"`
	Status              string               `json:"status"`
	Seconds             float64              `json:"seconds"`
	ValidTimingSample   bool                 `json:"valid_timing_sample"`
	Memory              gameResourceMemory   `json:"process_tree_memory"`
	Results             []gameResourceResult `json:"results"`
	Integrity           record               `json:"integrity,omitempty"`
	PackStorage         record               `json:"pack_storage,omitempty"`
	TrackedFilesPerSec  *float64             `json:"tracked_files_per_second,omitempty"`
	LogicalMiBPerSecond *float64             `json:"logical_history_mib_per_second,omitempty"`
	Error               string               `json:"error,omitempty"`
}

type gameResourceSetupError struct{ phase gameResourcePhase }

func (e *gameResourceSetupError) Error() string {
	if len(e.phase.Results) == 1 {
		r := e.phase.Results[0]
		return fmt.Sprintf("fixture command %s failed: exit=%d deadline=%t %s", r.Name, r.ExitCode, r.TimedOut, r.Error)
	}
	return "fixture process phase failed: " + e.phase.Error
}

func gameResourceRun(ctx context.Context, scope gameResourceScope, spec gameResourceCommand) gameResourceResult {
	r := gameResourceResult{Name: spec.name, Status: "fail", ExitCode: -1}
	if e := ctx.Err(); e != nil {
		r.Error, r.TimedOut = e.Error(), errors.Is(e, context.DeadlineExceeded)
		return r
	}
	out := &gameResourceCapture{limit: spec.prefix}
	errout := &gameResourceCapture{limit: 64 << 10}
	cmd := exec.Command(spec.exe, spec.args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = spec.cwd, spec.env, out, errout
	cmd.WaitDelay = 250 * time.Millisecond
	start := time.Now()
	e := scope.start(cmd)
	if e == nil {
		e = cmd.Wait()
	}
	r.Seconds = time.Since(start).Seconds()
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	r.StdoutBytes, r.StdoutSHA256, r.StdoutPrefix = out.bytes, out.digest(), string(out.prefix)
	r.StderrBytes, r.StderrSHA256, r.StderrPrefix = errout.bytes, errout.digest(), string(errout.prefix)
	if ctx.Err() != nil {
		e, r.TimedOut = ctx.Err(), errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	if e != nil {
		r.Error = e.Error()
	} else if r.ExitCode != 0 {
		r.Error = fmt.Sprintf("exit code %d", r.ExitCode)
	} else {
		r.Status = "pass"
	}
	return r
}

func gameResourceRunPhase(parent context.Context, specs []gameResourceCommand, parallel bool, limits gameResourceLimits) gameResourcePhase {
	p := gameResourcePhase{Status: "fail", Results: make([]gameResourceResult, len(specs))}
	scope, e := newGameResourceScope(limits)
	if e != nil {
		p.Error = e.Error()
		return p
	}
	ctx, cancel := context.WithTimeout(parent, limits.timeout)
	defer cancel()
	stopWatch, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = scope.stop()
		case <-stopWatch:
		}
	}()
	start := time.Now()
	run := func(i int) {
		p.Results[i] = gameResourceRun(ctx, scope, specs[i])
		if p.Results[i].Status != "pass" {
			cancel()
		}
	}
	if parallel {
		var group sync.WaitGroup
		for i := range specs {
			group.Go(func() { run(i) })
		}
		group.Wait()
	} else {
		for i := range specs {
			run(i)
		}
	}
	p.Seconds = time.Since(start).Seconds()
	close(stopWatch)
	<-watcherDone
	p.Memory = scope.finish()
	p.Status = "pass"
	for _, r := range p.Results {
		if r.Status != "pass" {
			p.Status = "fail"
		}
	}
	if !p.Memory.CleanupOK || p.Memory.Remaining != 0 || p.Memory.Error != "" {
		p.Status = "fail"
		p.Error = "phase process-tree accounting/cleanup failed; see process_tree_memory"
	}
	p.ValidTimingSample = p.Status == "pass"
	return p
}

type gameResourceFile struct {
	bytes int64
	sha   string
}

type gameResourceAsset struct {
	path string
	old  gameResourceFile
}

type gameResourceFixture struct {
	name, root, engine, head, refs, index string
	assets                                []gameResourceAsset
	files                                 map[string]gameResourceFile
	historyBytes                          int64
}

func gameResourceAssetPaths(engine string, count int) ([]string, error) {
	if count < 1 || count > 16 {
		return nil, fmt.Errorf("--asset-files must be 1..16 per repository")
	}
	directory, extension := "Assets/Art", ".asset"
	if engine == "unreal" {
		directory, extension = "Content", ".uasset"
	} else if engine != "unity" {
		return nil, fmt.Errorf("unknown generated game engine")
	}
	paths := make([]string, count)
	for i := range paths {
		stem := "Large"
		if count != 1 {
			stem = fmt.Sprintf("Large-%02d", i)
		}
		paths[i] = directory + "/" + stem + extension
	}
	return paths, nil
}

func gameResourceGenerateAssets(root string, paths []string, size int64, ordinal int) ([]gameResourceAsset, error) {
	assets := make([]gameResourceAsset, 0, len(paths))
	for i, path := range paths {
		asset := filepath.Join(root, filepath.FromSlash(path))
		if e := os.MkdirAll(filepath.Dir(asset), 0700); e != nil {
			return nil, e
		}
		// Keep the original one-asset seed while making all four repositories' assets distinct.
		if e := gameResourceStreamAsset(asset, size, int64(233+ordinal+4*i)); e != nil {
			return nil, e
		}
		old, e := gameResourceFileDigest(asset)
		if e != nil {
			return nil, e
		}
		assets = append(assets, gameResourceAsset{path: path, old: old})
	}
	return assets, nil
}

func gameResourceReviseAssets(root string, assets []gameResourceAsset, ordinal int) error {
	for i, asset := range assets {
		file, e := os.OpenFile(filepath.Join(root, filepath.FromSlash(asset.path)), os.O_WRONLY, 0)
		if e != nil {
			return e
		}
		n, e := file.WriteAt(bytes.Repeat([]byte{byte(ordinal + 1 + 4*i)}, 4096), asset.old.bytes/2)
		if e == nil && n != 4096 {
			e = io.ErrShortWrite
		}
		if e = errors.Join(e, file.Close()); e != nil {
			return e
		}
	}
	return nil
}

func gameResourceStreamAsset(path string, size int64, seed int64) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	rng, buf := rand.New(rand.NewSource(seed)), make([]byte, 1<<20)
	for left := size; left > 0; {
		n := int(min(left, int64(len(buf))))
		_, _ = rng.Read(buf[:n])
		if _, e = f.Write(buf[:n]); e != nil {
			return e
		}
		left -= int64(n)
	}
	return f.Sync()
}

func gameResourceFileDigest(path string) (gameResourceFile, error) {
	f, e := os.Open(path)
	if e != nil {
		return gameResourceFile{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return gameResourceFile{n, hex.EncodeToString(h.Sum(nil))}, e
}

func gameResourceTree(root string) (map[string]gameResourceFile, error) {
	files := map[string]gameResourceFile{}
	e := filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("non-regular fixture member")
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		v, e := gameResourceFileDigest(path)
		if e == nil {
			files[filepath.ToSlash(rel)] = v
		}
		return e
	})
	return files, e
}

func gameResourceCopyTree(from, to string) error {
	if e := os.Mkdir(to, 0700); e != nil {
		return e
	}
	return filepath.WalkDir(from, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(from, path)
		if e != nil || rel == "." {
			return e
		}
		target := filepath.Join(to, rel)
		info, e := entry.Info()
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe fixture source")
		}
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular fixture source")
		}
		input, e := os.Open(path)
		if e != nil {
			return e
		}
		defer input.Close()
		output, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			return e
		}
		_, e = io.Copy(output, input)
		e = errors.Join(e, output.Close())
		if e == nil {
			e = os.Chtimes(target, info.ModTime(), info.ModTime())
		}
		return e
	})
}

func gameResourceArgs(empty string, args ...string) []string {
	base := []string{"-c", "core.hooksPath=" + empty, "-c", "init.templateDir=" + empty, "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "core.longpaths=true", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}
	return append(base, args...)
}

func gameResourceSetupRun(binary, cwd, empty string, env []string, limits gameResourceLimits, args ...string) (gameResourceResult, error) {
	p := gameResourceRunPhase(context.Background(), []gameResourceCommand{{name: args[0], exe: binary, cwd: cwd, env: env, args: gameResourceArgs(empty, args...), prefix: 64 << 10}}, false, limits)
	if p.Status != "pass" || len(p.Results) != 1 {
		return gameResourceResult{}, &gameResourceSetupError{p}
	}
	return p.Results[0], nil
}

func gameResourceValidateConfigListing(parsed gameResourceResult) error {
	if parsed.StdoutBytes > int64(len(parsed.StdoutPrefix)) {
		return fmt.Errorf("configuration listing exceeds the bounded validation buffer")
	}
	for _, item := range strings.Split(parsed.StdoutPrefix, "\x00") {
		key, _, _ := strings.Cut(item, "\n")
		key = strings.ToLower(key)
		if key == "include.path" || strings.HasPrefix(key, "includeif.") {
			return fmt.Errorf("configuration includes are forbidden in isolated benchmarks")
		}
	}
	return nil
}

func gameResourceCreateFixture(binary, root, empty string, env []string, limits gameResourceLimits, files, assetMiB, assetFiles, ordinal int) (gameResourceFixture, error) {
	f := gameResourceFixture{root: root, name: fmt.Sprintf("project-%d", ordinal), engine: "unity"}
	if ordinal >= 2 {
		f.engine = "unreal"
	}
	run := func(args ...string) (gameResourceResult, error) {
		return gameResourceSetupRun(binary, root, empty, env, limits, args...)
	}
	if _, e := gameResourceSetupRun(binary, filepath.Dir(root), empty, env, limits, "init", "-q", "--object-format=sha1", "-b", "main", root); e != nil {
		return f, e
	}
	put := func(name, body string) error {
		return write(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0600)
	}
	if e := put(".gitattributes", "# Raw Git object stress; LFS transfer is not measured.\n* -text -filter\n"); e != nil {
		return f, e
	}
	textRoot := "Assets/Scripts"
	metadata := map[string]string{".gitignore": "/Library/\n/Temp/\n", "ProjectSettings/ProjectVersion.txt": "m_EditorVersion: generated-resource-fixture\n", "Packages/manifest.json": "{\"dependencies\":{}}\n"}
	if f.engine == "unreal" {
		textRoot = "Source/Game"
		metadata = map[string]string{".gitignore": "/Binaries/\n/Intermediate/\n/DerivedDataCache/\n/Saved/\n", "Game.uproject": "{\"FileVersion\":3}\n", "Config/DefaultEngine.ini": "; generated resource fixture\n", "Build/Keep.txt": "required build configuration is preserved\n"}
	}
	assetPaths, e := gameResourceAssetPaths(f.engine, assetFiles)
	if e != nil {
		return f, e
	}
	if f.engine == "unity" {
		for i, path := range assetPaths {
			metadata[path+".meta"] = fmt.Sprintf("fileFormatVersion: 2\nguid: %032x\n", i+1)
		}
	}
	for name, body := range metadata {
		if e := put(name, body); e != nil {
			return f, e
		}
	}
	for i := 0; i < files; i++ {
		name := fmt.Sprintf("%s/dir-%02d/file-%06d.txt", textRoot, i%16, i)
		if e := put(name, fmt.Sprintf("generated %s project %d text %d\n", f.engine, ordinal, i)); e != nil {
			return f, e
		}
	}
	if f.assets, e = gameResourceGenerateAssets(root, assetPaths, int64(assetMiB)<<20, ordinal); e != nil {
		return f, e
	}
	if _, e = run("add", "--all"); e != nil {
		return f, e
	}
	if _, e = run("commit", "-qm", "generated resource fixture first revision"); e != nil {
		return f, e
	}
	if e = gameResourceReviseAssets(root, f.assets, ordinal); e != nil {
		return f, e
	}
	if _, e = run("add", "--all"); e != nil {
		return f, e
	}
	if _, e = run("commit", "-qm", "generated resource fixture second revision"); e != nil {
		return f, e
	}
	if f.files, e = gameResourceTree(root); e != nil {
		return f, e
	}
	for _, asset := range f.assets {
		f.historyBytes += asset.old.bytes
	}
	for _, v := range f.files {
		f.historyBytes += v.bytes
	}
	head, e := run("rev-parse", "HEAD")
	if e != nil {
		return f, e
	}
	f.head = strings.TrimSpace(head.StdoutPrefix)
	refs, e := run("show-ref", "--head")
	if e != nil {
		return f, e
	}
	f.refs = refs.StdoutSHA256
	index, e := run("ls-files", "--stage", "-z")
	f.index = index.StdoutSHA256
	return f, e
}

func gameResourceValidateAssetHistory(f gameResourceFixture, rows []gameResourceResult) ([]string, int) {
	problems, verified := []string{}, 0
	if len(f.assets) == 0 || len(rows) != 2*len(f.assets) {
		return []string{f.name + ": incomplete current/historical asset validation"}, 0
	}
	seen := map[string]bool{}
	for i, asset := range f.assets {
		current, found := f.files[asset.path]
		one, two := rows[2*i], rows[2*i+1]
		if seen[asset.path] || !found || current.bytes <= 0 || asset.old.bytes <= 0 || len(current.sha) != 64 || len(asset.old.sha) != 64 {
			problems = append(problems, f.name+": invalid or duplicate asset validation member: "+asset.path)
		} else if one.Status != "pass" || two.Status != "pass" || one.ExitCode != 0 || two.ExitCode != 0 || one.TimedOut || two.TimedOut || one.StdoutSHA256 != current.sha || one.StdoutBytes != current.bytes || two.StdoutSHA256 != asset.old.sha || two.StdoutBytes != asset.old.bytes {
			problems = append(problems, f.name+": complete current/historical asset SHA mismatch: "+asset.path)
		} else {
			verified += 2
		}
		seen[asset.path] = true
	}
	return problems, verified
}

func gameResourceIntegrity(binary, empty string, env []string, limits gameResourceLimits, fixtures []gameResourceFixture, extra []gameResourceCommand) record {
	start := time.Now()
	specs := []gameResourceCommand{}
	for _, f := range fixtures {
		for _, v := range []struct {
			name string
			args []string
		}{{"fsck", []string{"fsck", "--full", "--strict"}}, {"refs", []string{"show-ref", "--head"}}, {"index", []string{"ls-files", "--stage", "-z"}}, {"status", []string{"status", "--porcelain=v1"}}} {
			specs = append(specs, gameResourceCommand{name: f.name + "/" + v.name, cwd: f.root, exe: binary, env: env, args: gameResourceArgs(empty, v.args...)})
		}
		for _, asset := range f.assets {
			for _, revision := range []string{"HEAD", "HEAD^"} {
				specs = append(specs, gameResourceCommand{name: f.name + "/" + revision + ":" + asset.path, cwd: f.root, exe: binary, env: env, args: gameResourceArgs(empty, "cat-file", "blob", revision+":"+asset.path)})
			}
		}
	}
	specs = append(specs, extra...)
	p := gameResourceRunPhase(context.Background(), specs, false, limits)
	problems := []string{}
	offset, revisionsExpected, revisionsVerified := 0, 0, 0
	for _, f := range fixtures {
		width := 4 + 2*len(f.assets)
		revisionsExpected += 2 * len(f.assets)
		if offset+width > len(p.Results) {
			problems = append(problems, f.name+": validation process unavailable")
			offset += width
			continue
		}
		rows := p.Results[offset : offset+width]
		offset += width
		if rows[1].StdoutSHA256 != f.refs || rows[2].StdoutSHA256 != f.index || rows[3].StdoutBytes != 0 {
			problems = append(problems, f.name+": ref/index/status mismatch")
		}
		assetProblems, verified := gameResourceValidateAssetHistory(f, rows[4:])
		problems = append(problems, assetProblems...)
		revisionsVerified += verified
		actual, e := gameResourceTree(f.root)
		if e != nil || len(actual) != len(f.files) {
			problems = append(problems, f.name+": fixture tree membership changed")
		}
		for name, expected := range f.files {
			if actual[name] != expected {
				problems = append(problems, f.name+": file SHA mismatch: "+name)
			}
		}
	}
	status := "pass"
	if p.Status != "pass" || len(problems) != 0 {
		status = "fail"
	}
	return record{"status": status, "seconds": time.Since(start).Seconds(), "full_fsck": true, "full_worktree_file_sha256": true, "complete_current_and_historical_asset_sha256": revisionsExpected > 0 && revisionsVerified == revisionsExpected, "asset_revision_sha256_checks_expected": revisionsExpected, "asset_revision_sha256_checks_passed": revisionsVerified, "refs_and_index_entries_unchanged": len(problems) == 0 && p.Status == "pass", "problems": problems, "validation_processes": p}
}

func gameResourcePackFiles(paths []string) (record, error) {
	rows := []record{}
	var packBytes, auxiliaryBytes int64
	for _, path := range paths {
		info, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("pack storage contains a non-regular member")
		}
		if strings.HasSuffix(info.Name(), ".pack") {
			packBytes += info.Size()
		} else {
			auxiliaryBytes += info.Size()
		}
		rows = append(rows, record{"name": info.Name(), "bytes": info.Size()})
	}
	return record{"files": rows, "pack_bytes": packBytes, "auxiliary_bytes": auxiliaryBytes, "total_bytes": packBytes + auxiliaryBytes}, nil
}

func gameResourcePackStorage(fixtures []gameResourceFixture, externalPrefixes []string) (record, error) {
	repositories := []record{}
	var repositoryPackBytes, repositoryAuxiliaryBytes int64
	for _, f := range fixtures {
		directory := filepath.Join(f.root, ".git", "objects", "pack")
		entries, e := os.ReadDir(directory)
		if e != nil {
			return nil, e
		}
		paths := []string{}
		for _, entry := range entries {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
		storage, e := gameResourcePackFiles(paths)
		if e != nil {
			return nil, e
		}
		storage["project"] = f.name
		repositories = append(repositories, storage)
		repositoryPackBytes += number(storage, "pack_bytes")
		repositoryAuxiliaryBytes += number(storage, "auxiliary_bytes")
	}
	externalPaths := []string{}
	for _, prefix := range externalPrefixes {
		if prefix == "" {
			continue
		}
		paths, e := filepath.Glob(prefix + "-*")
		if e != nil {
			return nil, e
		}
		externalPaths = append(externalPaths, paths...)
	}
	external, e := gameResourcePackFiles(externalPaths)
	if e != nil {
		return nil, e
	}
	return record{"repositories": repositories, "repository_pack_bytes": repositoryPackBytes, "repository_auxiliary_bytes": repositoryAuxiliaryBytes, "external_pack_objects": external, "measurement_outside_timed_process_phase": true, "pack_sizes_are_compressed_bytes": true, "loose_object_bytes_included": false}, nil
}

func benchmarkGameResources(a *app, o options) (returned error) {
	if e := o.require("output", "baseline-config", "candidate-config"); e != nil {
		return e
	}
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	projects, e := o.integer("projects", 4)
	if e != nil || projects != 4 {
		return fmt.Errorf("--projects must be 4 (two Unity and two Unreal repositories)")
	}
	files, e := o.integer("files", 128)
	if e != nil || files < 1 || files > 1000000 {
		return fmt.Errorf("--files must be 1..1000000 per repository")
	}
	assetMiB, e := o.integer("asset-mib", 2)
	if e != nil || assetMiB < 1 || assetMiB > 4096 {
		return fmt.Errorf("--asset-mib must be 1..4096 per asset revision")
	}
	assetFiles, e := o.integer("asset-files", 1)
	if e != nil || assetFiles < 1 || assetFiles > 16 {
		return fmt.Errorf("--asset-files must be 1..16 per repository")
	}
	samples, e := o.integer("samples", 1)
	if e != nil || samples < 1 || samples > 20 {
		return fmt.Errorf("--samples must be 1..20")
	}
	seconds, e := o.integer("phase-timeout", 180)
	if e != nil || seconds < 1 || seconds > 3600 {
		return fmt.Errorf("--phase-timeout must be 1..3600 seconds")
	}
	processes, e := o.integer("process-limit", 128)
	if e != nil || processes < 8 || processes > 128 {
		return fmt.Errorf("--process-limit must be 8..128")
	}
	memoryMiB, e := o.integer("job-memory-mib", 2048)
	if e != nil || memoryMiB < 32 || memoryMiB > 32768 {
		return fmt.Errorf("--job-memory-mib must be 32..32768")
	}
	limits := gameResourceLimits{processes: processes, memory: uint64(memoryMiB) << 20, timeout: time.Duration(seconds) * time.Second}
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
	started := time.Now()
	report := record{"schema_version": 1, "status": "running", "scope": "Same native Git bytes; isolated two Unity/two Unreal generated repositories per configuration; sequential and four-way concurrent raw Git object/status resource comparison", "os": runtime.GOOS, "arch": runtime.GOARCH, "projects_per_profile": projects, "text_files_per_repository": files, "binary_asset_mib_per_revision": assetMiB, "asset_files_per_repository": assetFiles, "asset_revisions": 2, "logical_asset_history_bytes_per_repository": int64(assetFiles) * int64(assetMiB) << 21, "samples": samples, "phase_timeout_seconds": seconds, "fixtures_retained": true, "fixture_directory_basename": filepath.Base(work), "personal_repositories_accessed": false, "personal_configuration_accessed": false, "terminal_executed": false, "lfs_transfer_executed": false, "common_safety_overrides": []string{"isolated hooks/templates", "gc.auto=0", "maintenance.auto=false", "core.longpaths=true", "commit.gpgsign=false", "tag.gpgsign=false"}, "memory_boundary": "Windows kernel whole-job peak commit is not RSS; Linux sampled sum of owned-session RSS counts shared pages per process and may miss short-lived peaks; macOS RSS unavailable with root-group-only cleanup; no cross-platform total-RSS or universal speedup claim", "cache_boundary": "Equivalent independent seed copies; normal per-profile index/object caches retained between samples; OS cache is not flushed and paired repacks may produce different equivalent pack bytes"}
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
		report["fixture_generation_and_integrity_checks_in_workload_peak"] = false
		if returned != nil {
			report["status"], report["error"] = "fail", returned.Error()
			var setupFailure *gameResourceSetupError
			if errors.As(returned, &setupFailure) {
				report["setup_failure"] = setupFailure.phase
			}
		}
		report["seconds"], report["phases"] = time.Since(started).Seconds(), phases
		if e := writeNewJSON(output, report); e != nil {
			returned = errors.Join(returned, e)
		}
	}()
	inputs := map[string]string{"binary": binary}
	identities := record{}
	for name, path := range map[string]string{"binary": binary, "baseline": o.get("baseline-config"), "candidate": o.get("candidate-config")} {
		path, e = filepath.Abs(path)
		if e != nil {
			return e
		}
		h, e := digest(path)
		if e != nil {
			return e
		}
		inputs[name], identities[name+"_sha256"] = path, h
	}
	report["input_identities"] = identities
	home, empty := filepath.Join(work, "home"), filepath.Join(work, "empty-path-and-hooks")
	for _, dir := range []string{home, empty, filepath.Join(work, "seeds"), filepath.Join(work, "baseline"), filepath.Join(work, "candidate"), filepath.Join(work, "packs")} {
		if e = os.Mkdir(dir, 0700); e != nil {
			return e
		}
	}
	emptyConfig := filepath.Join(home, "empty.gitconfig")
	if e = write(emptyConfig, nil, 0600); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"PATH": empty, "GIT_CONFIG_NOSYSTEM": "0", "GIT_CONFIG_SYSTEM": emptyConfig, "GIT_CONFIG_GLOBAL": emptyConfig, "TMPDIR": home, "TMP": home, "TEMP": home, "GIT_TERMINAL_PROMPT": "0", "GIT_LFS_SKIP_SMUDGE": "1"})
	profileEnv := map[string][]string{}
	for _, name := range []string{"baseline", "candidate"} {
		info, e := os.Lstat(inputs[name])
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("configuration input must be a regular file no larger than 1 MiB")
		}
		data, e := os.ReadFile(inputs[name])
		if e != nil {
			return e
		}
		if digestBytes(data) != identities[name+"_sha256"] {
			return fmt.Errorf("%s config changed before snapshot", name)
		}
		snapshot := filepath.Join(home, name+".gitconfig")
		if e = write(snapshot, data, 0600); e != nil {
			return e
		}
		parsed, e := gameResourceSetupRun(binary, work, empty, env, limits, "config", "--file", snapshot, "--no-includes", "--list", "-z")
		if e != nil {
			return e
		}
		if e = gameResourceValidateConfigListing(parsed); e != nil {
			return e
		}
		profileEnv[name] = fixtureEnv(env, map[string]string{"GIT_CONFIG_SYSTEM": snapshot})
		inputs[name+"_snapshot"], identities[name+"_snapshot_sha256"] = snapshot, identities[name+"_sha256"]
	}
	seeds := make([]gameResourceFixture, 0, projects)
	for i := 0; i < projects; i++ {
		f, e := gameResourceCreateFixture(binary, filepath.Join(work, "seeds", fmt.Sprintf("project-%d", i)), empty, env, limits, files, assetMiB, assetFiles, i)
		if e != nil {
			return e
		}
		seeds = append(seeds, f)
	}
	history, historyBytes := []record{}, int64(0)
	for _, seed := range seeds {
		history = append(history, record{"project": seed.name, "engine": seed.engine, "asset_files": len(seed.assets), "asset_revisions": 2, "tracked_files": len(seed.files), "logical_history_bytes": seed.historyBytes})
		historyBytes += seed.historyBytes
	}
	report["fixture_history"], report["logical_history_bytes_per_profile"] = history, historyBytes
	fixtures := map[string][]gameResourceFixture{}
	for _, name := range []string{"baseline", "candidate"} {
		for _, seed := range seeds {
			f := seed
			f.root = filepath.Join(work, name, f.name)
			if e = gameResourceCopyTree(seed.root, f.root); e != nil {
				return e
			}
			if _, e = gameResourceSetupRun(binary, f.root, empty, profileEnv[name], limits, "status", "--porcelain=v1"); e != nil {
				return e
			}
			fixtures[name] = append(fixtures[name], f)
		}
		if integrity := gameResourceIntegrity(binary, empty, profileEnv[name], limits, fixtures[name], nil); integrity["status"] != "pass" {
			report["setup_integrity_failure"] = integrity
			return fmt.Errorf("%s seed copy integrity failed", name)
		}
	}
	anyFailed := false
	for sample := 1; sample <= samples; sample++ {
		for _, workload := range []string{"status", "pack-objects", "repack"} {
			for _, mode := range []string{"sequential", "concurrent-four"} {
				names := []string{"baseline", "candidate"}
				if sample%2 == 0 {
					names[0], names[1] = names[1], names[0]
				}
				for _, name := range names {
					specs := []gameResourceCommand{}
					packPaths := []string{}
					var historyBytes int64
					tracked := 0
					for _, f := range fixtures[name] {
						args := []string{"status", "--porcelain=v1"}
						if workload == "pack-objects" {
							pack := filepath.Join(work, "packs", fmt.Sprintf("%s-%s-%d-%s", name, mode, sample, f.name))
							args = []string{"pack-objects", "--all", pack}
							packPaths = append(packPaths, pack)
						} else if workload == "repack" {
							args = []string{"repack", "-a", "-d"}
							packPaths = append(packPaths, "")
						}
						specs = append(specs, gameResourceCommand{name: f.name, exe: binary, cwd: f.root, args: gameResourceArgs(empty, args...), env: profileEnv[name], prefix: 64 << 10})
						historyBytes += f.historyBytes
						tracked += len(f.files)
					}
					beforeStorage, e := gameResourcePackStorage(fixtures[name], packPaths)
					if e != nil {
						return e
					}
					p := gameResourceRunPhase(context.Background(), specs, mode == "concurrent-four", limits)
					p.Profile, p.Workload, p.Mode, p.Sample = name, workload, mode, sample
					extra := []gameResourceCommand{}
					for i, result := range p.Results {
						if workload == "status" && result.StdoutBytes != 0 {
							p.Status, p.Error = "fail", "status omitted the expected clean-tree result"
						}
						if workload == "pack-objects" && result.Status == "pass" {
							hash := strings.TrimSpace(result.StdoutPrefix)
							if len(hash) != 40 || strings.Trim(hash, "0123456789abcdef") != "" {
								p.Status, p.Error = "fail", "pack-objects did not return a canonical SHA-1 pack identity"
								continue
							}
							extra = append(extra, gameResourceCommand{name: result.Name + "/verify-external-pack", cwd: fixtures[name][i].root, exe: binary, env: profileEnv[name], args: gameResourceArgs(empty, "verify-pack", "--", packPaths[i]+"-"+hash+".idx")})
						}
					}
					p.Integrity = gameResourceIntegrity(binary, empty, profileEnv[name], limits, fixtures[name], extra)
					if p.Integrity["status"] != "pass" {
						p.Status = "fail"
					}
					afterStorage, storageError := gameResourcePackStorage(fixtures[name], packPaths)
					p.PackStorage = record{"before": beforeStorage, "after": afterStorage, "logical_history_bytes": historyBytes, "comparison_requires_memory_time_and_compressed_pack_bytes": true}
					if storageError != nil {
						p.Status, p.Error = "fail", "pack storage readback failed: "+storageError.Error()
					}
					p.ValidTimingSample = p.Status == "pass"
					if p.ValidTimingSample && p.Seconds > 0 {
						if workload == "status" {
							v := float64(tracked) / p.Seconds
							p.TrackedFilesPerSec = &v
						} else {
							v := float64(historyBytes) / (1 << 20) / p.Seconds
							p.LogicalMiBPerSecond = &v
						}
					} else {
						anyFailed = true
					}
					phases = append(phases, p)
					fmt.Fprintf(a.out, "game resources %-9s %-12s %-15s sample=%d %s %.3fs\n", name, workload, mode, sample, p.Status, p.Seconds)
				}
			}
		}
	}
	for name, path := range inputs {
		h, e := digest(path)
		if e != nil || h != identities[name+"_sha256"] {
			return fmt.Errorf("%s input bytes changed during benchmark", name)
		}
	}
	report["input_before_after_readback"] = "pass"
	if anyFailed {
		return fmt.Errorf("one or more real resource phases/integrity checks failed; failed phases are excluded from timing samples")
	}
	return nil
}

func init() {
	register("benchmark-game-resources", "Compare isolated four-project Git resource phases with full process-tree accounting", "output baseline-config candidate-config projects files asset-mib asset-files samples phase-timeout process-limit job-memory-mib", benchmarkGameResources)
}

// Sort metric records without implying that unavailable measurements are zero.
func gameResourceAvailablePeaks(phases []gameResourcePhase) []uint64 {
	values := []uint64{}
	for _, p := range phases {
		if p.Status == "pass" && p.Memory.Available && p.Memory.PeakBytes != nil {
			values = append(values, *p.Memory.PeakBytes)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}
