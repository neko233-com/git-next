// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type measuredProcess struct {
	result
	elapsed float64
	peak    int64
}

func measuredRun(cwd string, env []string, input []byte, timeout time.Duration, exe string, args ...string) (measuredProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := time.Now()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	cleanup, e := prepareProcess(cmd)
	if e != nil {
		return measuredProcess{}, e
	}
	defer cleanup()
	if e = cmd.Start(); e != nil {
		return measuredProcess{}, e
	}
	if e = attachProcess(cmd); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return measuredProcess{}, e
	}
	query, closeMemory := benchmarkMemory(cmd.Process.Pid)
	defer closeMemory()
	e = cmd.Wait()
	elapsed := float64(time.Since(started).Nanoseconds()) / 1e6
	peak := query()
	value := measuredProcess{result{stdout.Bytes(), stderr.Bytes(), 0}, elapsed, peak}
	if contextErr := ctx.Err(); contextErr != nil {
		return value, fmt.Errorf("benchmark process exceeded %s: %w", timeout, contextErr)
	}
	if e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			value.code = exit.ExitCode()
		} else {
			return value, e
		}
	}
	return value, nil
}
func benchmarkStats(values []float64, peaks []int64) record {
	acceptCheck(len(values) > 0, "empty benchmark samples")
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	n := len(ordered)
	median := ordered[n/2]
	if n%2 == 0 {
		median = (ordered[n/2-1] + ordered[n/2]) / 2
	}
	round := func(v float64) float64 { return math.Round(v*1000) / 1000 }
	r := record{"median_ms": round(median), "p95_ms": round(ordered[int(math.Ceil(float64(n)*.95))-1]), "min_ms": round(ordered[0]), "peak_working_set_samples": len(peaks)}
	if len(peaks) > 0 {
		ordered := append([]int64{}, peaks...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
		n := len(ordered)
		median := ordered[n/2]
		if n%2 == 0 {
			median = (ordered[n/2-1] + ordered[n/2]) / 2
		}
		r["peak_working_set_median_bytes"] = median
		r["peak_working_set_p95_bytes"] = ordered[int(math.Ceil(float64(n)*.95))-1]
	}
	return r
}
func benchmarkSourceHash(root string) string {
	paths := []string{filepath.Join(root, "Cargo.toml"), filepath.Join(root, "Cargo.lock"), filepath.Join(root, "compatibility.json"), filepath.Join(root, "docs/COMPATIBILITY.md")}
	for _, directory := range []string{"src", "vendor"} {
		acceptMust(filepath.WalkDir(filepath.Join(root, directory), func(p string, d os.DirEntry, e error) error {
			if os.IsNotExist(e) && p == filepath.Join(root, directory) {
				return nil
			}
			if e != nil {
				return e
			}
			if !d.IsDir() && (strings.HasSuffix(p, ".rs") || (directory == "vendor" && d.Name() == "Cargo.toml")) {
				paths = append(paths, p)
			}
			return nil
		}))
	}
	sort.Slice(paths, func(i, j int) bool {
		left, right := filepath.ToSlash(paths[i]), filepath.ToSlash(paths[j])
		if runtime.GOOS == "windows" {
			left, right = strings.ToLower(left), strings.ToLower(right)
		}
		one, two := strings.Split(left, "/"), strings.Split(right, "/")
		for part := 0; part < min(len(one), len(two)); part++ {
			if one[part] != two[part] {
				return one[part] < two[part]
			}
		}
		return len(one) < len(two)
	})
	h := sha256.New()
	for _, p := range paths {
		rel, e := filepath.Rel(root, p)
		acceptMust(e)
		_, _ = h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		_, _ = h.Write(acceptBytes(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func init() {
	register("benchmark-source-identity", "Hash current Rust benchmark input paths with the historical platform ordering", "", func(a *app, o options) error {
		return a.json(record{"source_tree_sha256": benchmarkSourceHash(a.root)})
	})
}
func benchmarkCheckpoint(root, binary, checkpoint string) (string, error) {
	if checkpoint == "" {
		return benchmarkSourceHash(root), nil
	}
	p, e := load(checkpoint)
	if e != nil {
		return "", e
	}
	hash, e := digest(binary)
	if e != nil {
		return "", e
	}
	if stringValue(p, "binary_sha256") != hash {
		return "", fmt.Errorf("checkpoint does not identify this binary")
	}
	if len(stringValue(p, "source_tree_sha256")) != 64 {
		return "", fmt.Errorf("checkpoint source identity missing")
	}
	return stringValue(p, "source_tree_sha256"), nil
}
func benchmarkCopy(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(p)
			if e != nil {
				return e
			}
			return os.Symlink(link, target)
		}
		return copyFile(p, target)
	})
}
func benchmarkRemove(root, target string) error {
	root, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	target, e = filepath.Abs(target)
	if e != nil {
		return e
	}
	if target == root || !inside(root, target) {
		return fmt.Errorf("refuse removing path outside disposable fixture")
	}
	_ = filepath.WalkDir(target, func(p string, d os.DirEntry, e error) error {
		if e == nil && d.Type()&os.ModeSymlink == 0 {
			_ = os.Chmod(p, 0700)
		}
		return nil
	})
	return os.RemoveAll(target)
}
