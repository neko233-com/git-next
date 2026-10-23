// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type pythonReference struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
}

var pythonProgram = regexp.MustCompile(`(?i)^(?:python(?:w)?(?:[0-9]+(?:\.[0-9]+)*)?|pypy(?:[0-9]+)?|py)(?:\.exe)?$`)
var pythonAsset = regexp.MustCompile(`(?i)^(?:libpython[^/]*|python(?:w)?(?:[0-9]+(?:\.[0-9]+)*)?(?:-config|\.framework|\.exe|\.dll|\.dylib|\.so(?:\.[0-9]+)*|\.zip|\.a|\.lib)?|python@[^/]+|pypy(?:[0-9]+)?(?:\.exe)?|site-packages|__pycache__)$`)
var pythonShellCommand = regexp.MustCompile(`(?i)(?:^|&&|\|\||;|\|)\s*(?:(?:&|call|exec|sudo|command)\s+)?["']?(?:[^\s"']*[/\\])?(pythonw?(?:[0-9]+(?:\.[0-9]+)*)?|pypy[0-9]*|py)(?:\.exe)?["']?(?:\s+|$)(.*)$`)

func pythonExecutable(value string) bool { return pythonProgram.MatchString(filepathBase(value)) }
func shellPythonReferences(name, text string) []pythonReference {
	refs := []pythonReference{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	line := 0
	docker := strings.Contains(strings.ToLower(filepath.Base(name)), "dockerfile")
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(value, "#") {
			if strings.HasPrefix(value, "#!") && regexp.MustCompile(`(?i)(?:^|[/\s])(?:pythonw?[0-9.]*|pypy[0-9]*)(?:\s|$)`).MatchString(strings.TrimPrefix(value, "#!")) {
				refs = append(refs, pythonReference{name, line, "python-shebang", "python"})
			}
			continue
		}
		lower := strings.ToLower(value)
		if value == "" || strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "rem ") {
			continue
		}
		if docker && strings.HasPrefix(strings.ToUpper(value), "FROM ") {
			parts := strings.Fields(value)
			if len(parts) > 1 {
				image := strings.Split(strings.Split(parts[1], "@")[0], ":")[0]
				if pythonExecutable(path.Base(image)) {
					refs = append(refs, pythonReference{name, line, "python-container-base", parts[1]})
				}
			}
			continue
		}
		if docker {
			for _, prefix := range []string{"RUN ", "CMD ", "ENTRYPOINT "} {
				if strings.HasPrefix(strings.ToUpper(value), prefix) {
					value = strings.TrimSpace(value[len(prefix):])
					break
				}
			}
			if strings.HasPrefix(value, "[") {
				var first string
				parts := strings.SplitN(strings.TrimPrefix(value, "["), ",", 2)
				if len(parts) > 0 {
					first = strings.Trim(parts[0], " \t\"'")
				}
				if pythonExecutable(first) {
					refs = append(refs, pythonReference{name, line, "python-container-entrypoint", first})
					continue
				}
			}
		}
		if match := pythonShellCommand.FindStringSubmatch(value); match != nil {
			tail := strings.TrimSpace(match[2])
			if !strings.HasPrefix(tail, "=") && !strings.HasPrefix(tail, ":") {
				refs = append(refs, pythonReference{name, line, "python-command", match[1]})
			}
		}
	}
	return refs
}
func goPythonReferences(name string, data []byte) ([]pythonReference, error) {
	fset := token.NewFileSet()
	file, e := parser.ParseFile(fset, name, data, 0)
	if e != nil {
		return nil, e
	}
	refs := []pythonReference{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		function := ""
		switch expr := call.Fun.(type) {
		case *ast.Ident:
			function = expr.Name
		case *ast.SelectorExpr:
			function = expr.Sel.Name
		}
		index := -1
		switch function {
		case "Command", "run", "mustRun":
			index = 0
		case "CommandContext":
			index = 1
		case "runProcess", "measuredRun":
			index = 4
		}
		if index < 0 || len(call.Args) <= index {
			return true
		}
		value, ok := call.Args[index].(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return true
		}
		program, e := strconv.Unquote(value.Value)
		if e != nil || !pythonExecutable(program) {
			return true
		}
		refs = append(refs, pythonReference{name, fset.Position(call.Pos()).Line, "go-python-process", function + "(" + value.Value + ")"})
		return true
	})
	return refs, nil
}
func scanPythonEntrypoints(root string) ([]pythonReference, error) {
	refs := []pythonReference{}
	scan := func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == "target" || d.Name() == "__pycache__" || d.Name() == ".git" || d.Name() == ".evidence" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		relative = filepath.ToSlash(relative)
		ext := strings.ToLower(filepath.Ext(p))
		docker := strings.Contains(strings.ToLower(d.Name()), "dockerfile")
		if ext != ".go" && ext != ".ps1" && ext != ".sh" && ext != ".cmd" && ext != ".bat" && !docker {
			return nil
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if ext == ".go" {
			found, e := goPythonReferences(relative, data)
			if e != nil {
				return e
			}
			refs = append(refs, found...)
		} else {
			refs = append(refs, shellPythonReferences(relative, string(data))...)
		}
		return nil
	}
	for _, folder := range []string{"tools", "tests", "packaging"} {
		if e := filepath.WalkDir(filepath.Join(root, folder), scan); e != nil {
			return nil, e
		}
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if e = scan(filepath.Join(root, entry.Name()), entry, nil); e != nil {
				return nil, e
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Path == refs[j].Path {
			return refs[i].Line < refs[j].Line
		}
		return refs[i].Path < refs[j].Path
	})
	return refs, nil
}
func pythonAssetReason(name string, prefix []byte) string {
	normalized := strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
	for _, part := range strings.Split(normalized, "/") {
		if pythonAsset.MatchString(part) {
			return "python-runtime-path"
		}
	}
	switch strings.ToLower(path.Ext(normalized)) {
	case ".py", ".pyc", ".pyo", ".pyd", ".pyi", ".pyw", ".pyz", ".pyzw", ".whl", ".egg":
		return "python-script-or-module"
	}
	trimmed := bytes.TrimPrefix(prefix, []byte{0xef, 0xbb, 0xbf})
	if bytes.HasPrefix(trimmed, []byte("#!")) {
		line := string(bytes.SplitN(trimmed, []byte("\n"), 2)[0])
		if len(shellPythonReferences(name, line)) > 0 {
			return "python-shebang"
		}
	}
	ext := strings.ToLower(path.Ext(name))
	if ext == ".ps1" || ext == ".sh" || ext == ".cmd" || ext == ".bat" {
		if len(shellPythonReferences(name, string(prefix))) > 0 {
			return "python-wrapper"
		}
	}
	return ""
}
func inspectNoPythonProduct(directory, archive string) (record, error) {
	if (directory == "") == (archive == "") {
		return nil, fmt.Errorf("exactly one --package or --archive is required")
	}
	refs := []pythonReference{}
	count := 0
	var total int64
	type productManifest struct {
		name  string
		value record
	}
	manifests := map[string]productManifest{}
	manifestRoot := ""
	scan := func(name string, size int64, reader io.Reader, isManifest bool) error {
		count++
		total += size
		if count > 75000 || size < 0 || total > 4<<30 {
			return fmt.Errorf("product exceeds 75000 files/4 GiB inspection bound")
		}
		prefix, e := io.ReadAll(io.LimitReader(reader, 8192))
		if e != nil {
			return e
		}
		if reason := pythonAssetReason(name, prefix); reason != "" {
			refs = append(refs, pythonReference{Path: name, Kind: reason})
		}
		if isManifest {
			if size > 32<<20 {
				return fmt.Errorf("product manifest too large")
			}
			rest, e := io.ReadAll(io.LimitReader(reader, (32<<20)-int64(len(prefix))+1))
			if e != nil {
				return e
			}
			if len(prefix)+len(rest) > 32<<20 {
				return fmt.Errorf("product manifest too large")
			}
			candidate := record{}
			if e := decodeJSON(append(prefix, rest...), &candidate); e != nil {
				return e
			}
			product := stringValue(candidate, "product")
			if product == "git-next" || number(candidate, "schema_version") == 1 && len(object(candidate, "files")) > 0 {
				if product != "git-next" || number(candidate, "schema_version") != 1 || len(object(candidate, "files")) == 0 {
					return fmt.Errorf("invalid or conflicting product manifest: %s", name)
				}
				base, root := path.Base(name), path.Dir(name)
				if len(manifests) > 0 && root != manifestRoot {
					return fmt.Errorf("product manifests belong to different roots")
				}
				if _, found := manifests[base]; found {
					return fmt.Errorf("duplicate product manifest: %s", name)
				}
				manifestRoot = root
				manifests[base] = productManifest{name, candidate}
			}
		}
		return nil
	}
	input := directory
	if archive != "" {
		input = archive
		seen := map[string]bool{}
		e := eachArchive(archive, func(member archiveMember, reader io.Reader) error {
			name := strings.TrimPrefix(member.name, "./")
			if member.directory {
				return nil
			}
			if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:") {
				return fmt.Errorf("unsafe product archive member")
			}
			for _, part := range strings.Split(name, "/") {
				if part == ".." {
					return fmt.Errorf("unsafe product archive member")
				}
			}
			if seen[name] {
				return fmt.Errorf("duplicate product archive member")
			}
			seen[name] = true
			if member.symlink || member.hardlink {
				if member.link == "" || path.IsAbs(member.link) || strings.ContainsAny(member.link, "\\:\x00\r\n") {
					return fmt.Errorf("unsafe product archive link")
				}
				target := path.Clean(member.link)
				if member.symlink {
					target = path.Clean(path.Join(path.Dir(name), member.link))
				}
				if target == ".." || strings.HasPrefix(target, "../") {
					return fmt.Errorf("product archive link escaped package root")
				}
				if pythonAssetReason(target, nil) != "" {
					refs = append(refs, pythonReference{Path: name, Kind: "python-link-target"})
				}
				reader = strings.NewReader("")
			}
			base := path.Base(name)
			return scan(name, member.size, reader, base == "package.json" || base == "git-next-distribution.json")
		})
		if e != nil {
			return nil, e
		}
	} else {
		root, e := filepath.Abs(directory)
		if e != nil {
			return nil, e
		}
		e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			info, e := os.Lstat(p)
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, e := filepath.EvalSymlinks(p)
				if e != nil || !inside(root, resolved) {
					return fmt.Errorf("product symlink escaped package root")
				}
				return scan(filepath.ToSlash(rel), 0, strings.NewReader(""), false)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported product entry type")
			}
			file, e := os.Open(p)
			if e != nil {
				return e
			}
			defer file.Close()
			base := filepath.Base(p)
			return scan(filepath.ToSlash(rel), info.Size(), file, base == "package.json" || base == "git-next-distribution.json")
		})
		if e != nil {
			return nil, e
		}
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("git-next product file manifest is required")
	}
	selected := manifests["git-next-distribution.json"]
	if selected.name == "" {
		selected = manifests["package.json"]
	}
	if len(manifests) == 2 {
		pack := manifests["package.json"].value
		distribution := manifests["git-next-distribution.json"].value
		for _, key := range []string{"product", "platform", "version", "source_tree_sha256"} {
			if stringValue(pack, key) == "" || stringValue(pack, key) != stringValue(distribution, key) {
				return nil, fmt.Errorf("product manifests disagree on %s", key)
			}
		}
		for name, entry := range object(pack, "files") {
			other, found := object(distribution, "files")[name]
			if !found || !equalJSON(entry, other) {
				return nil, fmt.Errorf("product manifest file records disagree: %s", name)
			}
		}
	}
	identity := record{"product": selected.value["product"], "version": selected.value["version"], "platform": selected.value["platform"], "source_tree_sha256": selected.value["source_tree_sha256"], "manifest": selected.name}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	return record{"schema_version": 1, "scope": "actual final product paths, script headers and direct platform wrappers; opaque source archives and historical receipts are excluded", "input": input, "identity": identity, "files_inspected": count, "declared_bytes_inspected": total, "python_entries": refs, "python_entry_count": len(refs), "complete": len(refs) == 0}, nil
}
func productPythonAudit(a *app, o options) error {
	report, e := inspectNoPythonProduct(o.get("package"), o.get("archive"))
	if e != nil {
		return e
	}
	if o.get("json") != "" {
		if e = writeJSON(o.get("json"), report); e != nil {
			return e
		}
	}
	if e = a.json(report); e != nil {
		return e
	}
	if o.yes("require-complete") && report["complete"] != true {
		return fmt.Errorf("product still contains %d Python runtime/script entries", report["python_entry_count"])
	}
	return nil
}
func init() {
	register("migration-product-audit", "Inspect actual product directory/archive for Python interpreters, libraries and scripts", "package archive json require-complete?", productPythonAudit)
}
