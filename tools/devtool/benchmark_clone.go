// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func cloneBenchmark(a *app, o options) error {
	targets := o.all("target")
	if len(targets) == 0 {
		return fmt.Errorf("at least one --target LABEL=URL is required")
	}
	reps, e := o.integer("reps", 5)
	if e != nil || reps < 1 {
		return fmt.Errorf("reps must be >=1")
	}
	binary := o.get("binary")
	if binary == "" {
		suffix := ""
		if runtimeOSWindows() {
			suffix = ".exe"
		}
		binary = a.path("target/release/git-next" + suffix)
	}
	binary, e = filepath.Abs(binary)
	if e != nil {
		return e
	}
	if !exists(binary) {
		return fmt.Errorf("build the client first: %s", binary)
	}
	reference, e := acceptReference(o)
	if e != nil {
		return e
	}
	config := map[string]string{}
	for _, entry := range o.all("config") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return fmt.Errorf("--config needs KEY=VALUE")
		}
		config[key] = value
	}
	parsedTargets := []struct{ label, url string }{}
	for _, entry := range targets {
		label, raw, ok := strings.Cut(entry, "=")
		if !ok || label == "" || raw == "" {
			return fmt.Errorf("--target needs LABEL=URL")
		}
		parsed, e := url.Parse(raw)
		if e != nil || parsed.User != nil {
			return fmt.Errorf("benchmark URLs must not contain credentials")
		}
		parsedTargets = append(parsedTargets, struct{ label, url string }{label, raw})
	}
	root, e := os.MkdirTemp("", "git-next-bench-clone-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	env := fixtureEnv(acceptEnv(filepath.Join(root, "home")), map[string]string{"GIT_NEXT_TRACE_TIMING": "1"})
	keys := []string{}
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		extra := map[string]string{"GIT_CONFIG_COUNT": fmt.Sprint(len(keys))}
		for i, key := range keys {
			extra[fmt.Sprintf("GIT_CONFIG_KEY_%d", i)] = key
			extra[fmt.Sprintf("GIT_CONFIG_VALUE_%d", i)] = config[key]
		}
		env = fixtureEnv(env, extra)
	}
	clients := []struct{ name, exe string }{{"git", reference}, {"git-next", binary}}
	reportTargets := []record{}
	for _, target := range parsedTargets {
		samples := map[string][]float64{}
		traces := map[string]string{}
		sizes := map[string]int64{}
		for round := 0; round < reps+1; round++ {
			order := clients
			if round%2 != 0 {
				order = []struct{ name, exe string }{clients[1], clients[0]}
			}
			for _, client := range order {
				destination := filepath.Join(root, "out-"+client.name)
				acceptMust(benchmarkRemove(root, destination))
				value, e := measuredRun(root, env, nil, 10*time.Minute, client.exe, "clone", target.url, destination)
				if e != nil {
					return e
				}
				if value.code != 0 {
					return fmt.Errorf("%s clone failed: %.300s", client.name, value.stderr)
				}
				if round == 0 {
					continue
				}
				samples[client.name] = append(samples[client.name], value.elapsed)
				for _, line := range strings.Split(string(value.stderr), "\n") {
					if strings.HasPrefix(line, "trace:") {
						traces[client.name] = line
					}
				}
				total := int64(0)
				acceptMust(filepath.WalkDir(destination, func(p string, d os.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if !d.IsDir() {
						info, e := d.Info()
						if e != nil {
							return e
						}
						if info.Mode().IsRegular() {
							total += info.Size()
						}
					}
					return nil
				}))
				sizes[client.name] = total
			}
		}
		results := []record{}
		medians := map[string]float64{}
		for _, client := range clients {
			values := samples[client.name]
			stats := benchmarkStats(values, nil)
			round1 := func(v float64) float64 { return math.Round(v*10) / 10 }
			rounded := []float64{}
			maximum := 0.0
			for _, value := range values {
				rounded = append(rounded, round1(value))
				maximum = math.Max(maximum, value)
			}
			median := stats["median_ms"].(float64)
			medians[client.name] = median
			results = append(results, record{"client": client.name, "runs_ms": rounded, "median_ms": round1(median), "min_ms": round1(stats["min_ms"].(float64)), "max_ms": round1(maximum), "checkout_bytes": sizes[client.name], "phase_trace": traces[client.name]})
			acceptMust(benchmarkRemove(root, filepath.Join(root, "out-"+client.name)))
		}
		entry := record{"label": target.label, "url": target.url, "results": results}
		if medians["git-next"] > 0 {
			entry["speedup_vs_reference"] = math.Round(medians["git"]/medians["git-next"]*1000) / 1000
		}
		reportTargets = append(reportTargets, entry)
	}
	configuration := record{}
	for key, value := range config {
		if strings.Contains(strings.ToLower(key), "credential") || strings.Contains(strings.ToLower(key), "extraheader") {
			configuration[key] = "<redacted>"
		} else {
			configuration[key] = value
		}
	}
	report := record{"product": "git-next", "measured_at": time.Now().UTC().Format(time.RFC3339), "platform": runtime.GOOS + " " + runtime.GOARCH, "reference": strings.TrimSpace(string(acceptRun(root, env, nil, true, reference, "--version"))), "reps": reps, "configuration": configuration, "note": "The first run of each client is discarded. A clone includes identical server-side pack generation; the client phase trace separates transfer+index from checkout but the server share is included in both wall-clock figures.", "targets": reportTargets}
	if o.get("json") != "" {
		if e = writeJSON(o.get("json"), report); e != nil {
			return e
		}
	}
	return a.json(report)
}
func init() {
	register("benchmark-clone", "Alternating clone wall-clock benchmark with discarded warmup and phase traces", "target reps config json binary reference", cloneBenchmark)
}
