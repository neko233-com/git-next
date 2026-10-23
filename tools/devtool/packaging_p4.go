// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

func init() {
	register("build-git-p4", "Build interpreter-free Perforce integration for a supported target", "platform destination go", buildGitP4)
}
func gitP4Sources(a *app) (record, error) {
	out := record{}
	root := a.path("tools/git-p4")
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == "target" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, _ := filepath.Rel(root, p)
		name := filepath.ToSlash(relative)
		if filepath.Ext(name) != ".go" && name != "go.mod" && name != "go.sum" && name != "LICENSE" && name != "README.md" {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("git-p4 source links are not accepted")
		}
		h, e := digest(p)
		if e != nil {
			return e
		}
		out[name] = h
		return nil
	})
	return out, e
}
func buildGitP4(a *app, o options) error {
	if e := o.require("platform", "destination"); e != nil {
		return e
	}
	platform := o.get("platform")
	targets := map[string][2]string{"windows-x64": {"windows", "amd64"}, "windows-arm64": {"windows", "arm64"}, "linux-x64": {"linux", "amd64"}, "linux-arm64": {"linux", "arm64"}, "macos-arm64": {"darwin", "arm64"}}
	target, ok := targets[platform]
	if !ok {
		return fmt.Errorf("unsupported git-p4 platform")
	}
	sources, e := gitP4Sources(a)
	if e != nil {
		return e
	}
	root, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	goTool := o.value("go", "go")
	environment := append(os.Environ(), "GOOS="+target[0], "GOARCH="+target[1], "CGO_ENABLED=0", "GOTOOLCHAIN=local")
	if _, e = commandAt(a.path("tools/git-p4"), environment, goTool, "mod", "verify"); e != nil {
		return e
	}
	name := "git-p4"
	if target[0] == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(root, name)
	if _, e = commandAt(a.path("tools/git-p4"), environment, goTool, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", binary, "."); e != nil {
		return e
	}
	if target[0] == "windows" {
		machine, _, e := peInfo(binary)
		if e != nil || machine != peMachine(platform) {
			return fmt.Errorf("git-p4 PE target differs")
		}
	} else {
		machine, e := imagePlatform(binary)
		if e != nil || machine != platform {
			return fmt.Errorf("git-p4 native target differs")
		}
	}
	after, e := gitP4Sources(a)
	if e != nil {
		return e
	}
	if !equalJSON(sources, after) {
		return fmt.Errorf("git-p4 sources changed during compilation")
	}
	h, e := digest(binary)
	if e != nil {
		return e
	}
	st, e := os.Stat(binary)
	if e != nil {
		return e
	}
	identity := digestBytes(encoded(sources))
	receipt := record{"schema_version": 1, "status": "built-not-runtime-accepted", "product": "git-p4", "platform": platform, "source_files": sources, "source_tree_sha256": identity, "binary": record{"name": name, "sha256": h, "bytes": st.Size()}, "python_required": false, "product_executed": false, "tests_executed": false, "release_eligible": false}
	if e = copyFile(a.path("tools/git-p4/LICENSE"), filepath.Join(root, "LICENSE")); e != nil {
		return e
	}
	if exists(a.path("tools/git-p4/README.md")) {
		if e = copyFile(a.path("tools/git-p4/README.md"), filepath.Join(root, "README.md")); e != nil {
			return e
		}
	}
	dependencies, e := gitP4Notices(a, root, goTool, environment)
	if e != nil {
		return e
	}
	receipt["dependencies"] = dependencies
	if e = writeNewJSON(filepath.Join(root, "DEPENDENCIES.json"), dependencies); e != nil {
		return e
	}
	inventory, e := hashes(root)
	if e != nil {
		return e
	}
	receipt["files"] = inventory
	if e = writeNewJSON(filepath.Join(root, "receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(receipt)
}
func gitP4Notices(a *app, root, goTool string, environment []string) ([]any, error) {
	raw, e := commandAt(a.path("tools/git-p4"), environment, goTool, "list", "-mod=readonly", "-deps", "-json", ".")
	if e != nil {
		return nil, e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var dependencies []any
	seen := map[string]bool{}
	for {
		var pkg record
		e := decoder.Decode(&pkg)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		item := object(pkg, "Module")
		if item == nil || truth(item, "Main") || seen[stringValue(item, "Path")] {
			continue
		}
		seen[stringValue(item, "Path")] = true
		if item["Replace"] != nil || stringValue(item, "Version") == "" || stringValue(item, "Sum") == "" {
			return nil, fmt.Errorf("unpinned Go helper dependency")
		}
		source := stringValue(item, "Dir")
		entries, e := os.ReadDir(source)
		if e != nil {
			return nil, e
		}
		notices := record{}
		for _, entry := range entries {
			upper := strings.ToUpper(entry.Name())
			if entry.IsDir() || !(strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE") || strings.HasPrefix(upper, "PATENTS") || strings.HasPrefix(upper, "AUTHORS")) {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("dependency notice cannot be a link")
			}
			relative := "LICENSES/" + strings.ReplaceAll(stringValue(item, "Path"), "/", "_") + "@" + stringValue(item, "Version") + "/" + entry.Name()
			target, e := confined(root, relative)
			if e != nil {
				return nil, e
			}
			if e = copyFile(filepath.Join(source, entry.Name()), target); e != nil {
				return nil, e
			}
			h, e := digest(target)
			if e != nil {
				return nil, e
			}
			notices[relative] = h
		}
		if len(notices) == 0 {
			return nil, fmt.Errorf("dependency license notice missing: %s", item["Path"])
		}
		dependencies = append(dependencies, record{"module": item["Path"], "version": item["Version"], "sum": item["Sum"], "notices": notices})
	}
	toolchainRaw, e := commandAt(a.path("tools/git-p4"), environment, goTool, "env", "-json", "GOROOT", "GOVERSION")
	if e != nil {
		return nil, e
	}
	var toolchain record
	if e = json.Unmarshal(toolchainRaw, &toolchain); e != nil {
		return nil, e
	}
	notices := record{}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		source := filepath.Join(stringValue(toolchain, "GOROOT"), name)
		if !exists(source) && name == "PATENTS" {
			continue
		}
		relative := "LICENSES/go-standard-library/" + name
		target, e := confined(root, relative)
		if e != nil {
			return nil, e
		}
		if e = copyFile(source, target); e != nil {
			return nil, e
		}
		h, e := digest(target)
		if e != nil {
			return nil, e
		}
		notices[relative] = h
	}
	dependencies = append(dependencies, record{"module": "Go standard library", "version": toolchain["GOVERSION"], "notices": notices})
	return dependencies, nil
}
func checkedGitP4(a *app, root, platform string) (string, record, error) {
	receipt, e := load(filepath.Join(root, "receipt.json"))
	if e != nil {
		return "", nil, e
	}
	if receipt["product"] != "git-p4" || receipt["platform"] != platform || receipt["python_required"] != false {
		return "", nil, fmt.Errorf("interpreter-free matching git-p4 receipt required")
	}
	sources, e := gitP4Sources(a)
	if e != nil {
		return "", nil, e
	}
	if !equalJSON(receipt["source_files"], sources) || receipt["source_tree_sha256"] != digestBytes(encoded(sources)) {
		return "", nil, fmt.Errorf("git-p4 receipt differs from current implementation")
	}
	if len(object(receipt, "files")) == 0 {
		return "", nil, fmt.Errorf("git-p4 receipt lacks dependency notices")
	}
	for name, h := range object(receipt, "files") {
		p, e := confined(root, name)
		if e != nil {
			return "", nil, e
		}
		if e = shaMatches(p, record{"sha256": h}); e != nil {
			return "", nil, e
		}
	}
	name := stringValue(object(receipt, "binary"), "name")
	if name != filepathBase(name) || name != "git-p4" && name != "git-p4.exe" {
		return "", nil, fmt.Errorf("unsafe git-p4 binary name")
	}
	p := filepath.Join(root, name)
	if e = shaMatches(p, object(receipt, "binary")); e != nil {
		return "", nil, e
	}
	if strings.HasPrefix(platform, "windows-") {
		machine, _, e := peInfo(p)
		if e != nil || machine != peMachine(platform) {
			return "", nil, fmt.Errorf("git-p4 binary target differs")
		}
	} else {
		machine, e := imagePlatform(p)
		if e != nil || machine != platform {
			return "", nil, fmt.Errorf("git-p4 binary target differs")
		}
	}
	return p, receipt, nil
}

var pythonRuntimePart = regexp.MustCompile(`(?i)^(?:python(?:w|[0-9.]+)?(?:\.exe|\.dll)?|libpython[0-9.]+(?:\.so(?:\.[0-9.]+)?|\.dylib))$`)

func pythonRuntimeMember(name string) bool {
	for _, part := range strings.Split(strings.ToLower(name), "/") {
		if pythonRuntimePart.MatchString(part) || part == "__pycache__" || part == "site-packages" {
			return true
		}
	}
	base := path.Base(strings.ToLower(name))
	return strings.HasSuffix(base, ".py") || strings.HasSuffix(base, ".pyc") || base == "git-p4" || base == "git-p4.exe"
}
func (b *distributionBuilder) addGitP4(a *app, root, platform, prefix string) (record, error) {
	binary, receipt, e := checkedGitP4(a, root, platform)
	if e != nil {
		return nil, e
	}
	if strings.HasPrefix(platform, "windows-") {
		for _, directory := range []string{"bin", prefix + "/bin", prefix + "/libexec/git-core"} {
			if e = b.copy(binary, directory+"/git-p4.exe", "source-built-git-p4", 0755, 0); e != nil {
				return nil, e
			}
		}
	} else {
		if e = b.copy(binary, prefix+"/libexec/git-core/git-p4", "source-built-git-p4", 0755, 0); e != nil {
			return nil, e
		}
		if e = b.link("bin/git-p4", posixRelative("bin", prefix+"/libexec/git-core/git-p4"), "public-git-p4-alias"); e != nil {
			return nil, e
		}
	}
	for name := range object(receipt, "files") {
		if name == filepath.Base(binary) {
			continue
		}
		if e = b.copy(filepath.Join(root, filepath.FromSlash(name)), "LICENSES/git-p4/"+name, "git-p4-license-and-source-identity", 0644, 0); e != nil {
			return nil, e
		}
	}
	if e = b.copy(filepath.Join(root, "receipt.json"), "LICENSES/git-p4/source.json", "git-p4-build-identity", 0644, 0); e != nil {
		return nil, e
	}
	return receipt, nil
}
