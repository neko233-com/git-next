// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	register("verify-native-prefix-layout", "Verify real public/internal Git aliases under colliding package basenames", "distribution output", verifyNativePrefixLayout)
}
func verifyNativePrefixLayout(a *app, o options) error {
	if e := o.require("distribution", "output"); e != nil {
		return e
	}
	source, e := absolute(o.get("distribution"))
	if e != nil {
		return e
	}
	manifest, e := load(filepath.Join(source, "git-next-distribution.json"))
	if e != nil {
		return e
	}
	platform := stringValue(manifest, "platform")
	if !strings.HasPrefix(platform, "windows-") || runtime.GOOS != "windows" {
		return fmt.Errorf("this alias boundary check requires a native Windows distribution")
	}
	for name, raw := range object(manifest, "files") {
		entry, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid frozen package file")
		}
		p, e := confined(source, name)
		if e != nil {
			return e
		}
		if e = requireHash(p, entry["sha256"]); e != nil {
			return e
		}
	}
	temporary, e := os.MkdirTemp(filepath.Dir(source), ".git-next-prefix-layout-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temporary)
	results := []record{}
	prefix := "mingw64"
	if platform == "windows-arm64" {
		prefix = "clangarm64"
	}
	aliases := []string{"bin/git.exe", "cmd/git.exe", "runtime/bin/git.exe", "runtime/cmd/git.exe", "runtime/" + prefix + "/bin/git.exe", "runtime/" + prefix + "/libexec/git-core/git.exe"}
	home := filepath.Join(temporary, "home")
	if e = os.MkdirAll(home, 0755); e != nil {
		return e
	}
	env := cleanEnvironment(home)
	for _, base := range []string{"runtime", "cmd", "git", "中文 runtime"} {
		root := filepath.Join(temporary, base)
		if e = copyRuntimeTree(source, root); e != nil {
			return e
		}
		expected := filepath.Join(root, "runtime", prefix, "libexec/git-core")
		for _, alias := range aliases {
			program := filepath.Join(root, filepath.FromSlash(alias))
			if !exists(program) {
				return fmt.Errorf("required public/internal alias missing: %s", alias)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			r, e := runProcess(ctx, root, env, nil, program, "--exec-path")
			cancel()
			status := "pass"
			actual := strings.TrimSpace(string(r.stdout))
			if e != nil || r.code != 0 || !strings.EqualFold(filepath.Clean(actual), filepath.Clean(expected)) {
				status = "fail"
			}
			item := record{"case": base + ":" + alias, "status": status, "exit_code": r.code}
			if status == "fail" {
				item["reason"] = "alias did not resolve the owned package prefix"
			}
			results = append(results, item)
		}
	}
	status := "pass"
	if len(results) != 24 {
		return fmt.Errorf("package does not provide the expected public/internal aliases")
	}
	for _, item := range results {
		if item["status"] != "pass" {
			status = "fail"
		}
	}
	report := record{"schema_version": 1, "status": status, "platform": platform, "results": results, "personal_profile_accessed": false, "storage_layout_changed": false}
	if e = writeNewJSON(o.get("output"), report); e != nil {
		return e
	}
	if e = a.json(report); e != nil {
		return e
	}
	if status != "pass" {
		return fmt.Errorf("owned runtime-prefix alias boundary failed")
	}
	return nil
}
