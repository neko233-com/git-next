// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	register("migration-audit", "Check owned Python files, current execution entrypoints and optional final product", "require-complete? json package archive", migrationAudit)
}
func migrationAudit(a *app, o options) error {
	var remaining []string
	for _, folder := range []string{"tools", "tests", "packaging"} {
		e := filepath.WalkDir(a.path(folder), func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() && (d.Name() == "__pycache__" || d.Name() == "target") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(strings.ToLower(path), ".py") {
				relative, e := filepath.Rel(a.root, path)
				if e != nil {
					return e
				}
				remaining = append(remaining, filepath.ToSlash(relative))
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	sort.Strings(remaining)
	entrypoints, e := scanPythonEntrypoints(a.root)
	if e != nil {
		return e
	}
	complete := len(remaining) == 0 && len(entrypoints) == 0
	report := record{"schema_version": 1, "scope": "owned tools/tests/packaging and root executable scripts; current direct Go/process, platform-script and Docker entrypoints; historical receipts and opaque source archives are not executable tooling", "remaining_python_files": len(remaining), "remaining": remaining, "python_execution_entrypoints": entrypoints, "python_execution_entrypoint_count": len(entrypoints), "complete": complete, "rust_cli_gate": "accept-runner/git-next-accept", "go_tool": "tools/devtool"}
	if o.get("package") != "" || o.get("archive") != "" {
		product, e := inspectNoPythonProduct(o.get("package"), o.get("archive"))
		if e != nil {
			return e
		}
		report["product"] = product
		complete = complete && product["complete"] == true
		report["complete"] = complete
	}
	if o.get("json") != "" {
		if e := writeJSON(o.get("json"), report); e != nil {
			return e
		}
	}
	if e := a.json(report); e != nil {
		return e
	}
	if o.yes("require-complete") && !complete {
		return fmt.Errorf("migration incomplete: %d owned Python files and %d direct execution entrypoints remain; optional product must also contain no Python", len(remaining), len(entrypoints))
	}
	return nil
}
