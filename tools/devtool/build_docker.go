// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func init() {
	register("build-native-linux", "Compile native Linux cores in owned bounded Docker resources", "platform source core-archive dependency-cache base-image destination jobs reuse-dependencies dependency-producer reuse-core-build core-producer go", buildNativeLinux)
	register("native-linux-cache-prepare", "Prepare a confined owned Linux incremental compiler cache", "root", prepareLinuxCache)
}
func scopeID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func dockerJSON(a *app, args ...string) ([]record, error) {
	ctx, cancel := deadline()
	defer cancel()
	r, e := runProcess(ctx, a.root, nil, nil, "docker", args...)
	if e != nil {
		return nil, e
	}
	if r.code != 0 {
		return nil, fmt.Errorf("Docker metadata failed: %s", r.stderr)
	}
	var values []record
	if e = json.Unmarshal(r.stdout, &values); e != nil {
		return nil, e
	}
	return values, nil
}
func dockerAbsent(a *app, kind, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, e := runProcess(ctx, a.root, nil, nil, "docker", kind, "inspect", name)
	return e == nil && r.code != 0 && missingDockerResource(kind, name, string(r.stderr))
}
func missingDockerResource(kind, name, message string) bool {
	message, name = strings.ToLower(message), strings.ToLower(name)
	if strings.Contains(message, "no such "+kind+": "+name) || strings.Contains(message, "no such object: "+name) {
		return true
	}
	if kind == "volume" && strings.Contains(message, "get "+name+": no such volume") {
		return true
	}
	return kind == "network" && strings.Contains(message, "network "+name+" not found")
}
func removeDockerOwned(a *app, kind, name, owner, scope string) error {
	if dockerAbsent(a, kind, name) {
		return nil
	}
	values, e := dockerJSON(a, kind, "inspect", name)
	if e != nil {
		return e
	}
	if len(values) != 1 {
		return fmt.Errorf("unexpected Docker resource count")
	}
	labels := object(values[0], "Labels")
	if kind == "image" || kind == "container" {
		labels = object(object(values[0], "Config"), "Labels")
	}
	if labels["git-next.owner"] != owner || labels["git-next.scope"] != scope {
		return fmt.Errorf("refusing unrelated Docker %s cleanup", kind)
	}
	args := []string{kind, "rm", name}
	if kind == "container" {
		args = []string{"rm", "-f", name}
	}
	return buildRun(a, a.root, nil, "docker", args...)
}
func copyOwnedTree(source, destination string) error {
	return filepath.WalkDir(source, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(source, p)
		if strings.Contains("/"+filepath.ToSlash(rel)+"/", "/.github/workflows/") {
			return fmt.Errorf("workflow source entered build context")
		}
		target := filepath.Join(destination, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("prepared build tree must materialize source symlinks")
		}
		if e = copyFile(p, target); e != nil {
			return e
		}
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
}
func sourceTransfer(source, destination string, modes map[string]os.FileMode) error {
	f, e := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return e
	}
	tw := tar.NewWriter(f)
	paths, e := files(source)
	if e == nil {
		for _, p := range paths {
			rel, _ := filepath.Rel(source, p)
			rel = filepath.ToSlash(rel)
			info, statErr := os.Stat(p)
			if statErr != nil {
				e = statErr
				break
			}
			mode := modes[rel]
			if mode == 0 {
				mode = 0644
			}
			h := &tar.Header{Name: "source/" + rel, Mode: int64(mode.Perm()), Size: info.Size(), ModTime: info.ModTime()}
			if e = tw.WriteHeader(h); e != nil {
				break
			}
			in, openErr := os.Open(p)
			if openErr != nil {
				e = openErr
				break
			}
			_, e = io.Copy(tw, in)
			in.Close()
			if e != nil {
				break
			}
		}
	}
	ce := tw.Close()
	fe := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return fe
}
func prepareLinuxCache(a *app, o options) error {
	root, e := absolute(o.get("root"))
	if e != nil {
		return e
	}
	if root != "/build" {
		return fmt.Errorf("cache cleanup is confined to owned /build volume")
	}
	stage := filepath.Join(root, "core-stage")
	if exists(stage) {
		info, e := os.Lstat(stage)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe cached stage")
		}
		if e = os.RemoveAll(stage); e != nil {
			return e
		}
	}
	for _, name := range nativePrograms {
		p := filepath.Join(root, "core-build", name)
		if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	p := filepath.Join(root, "source/git-next-identity.h")
	now := time.Now()
	return os.Chtimes(p, now, now)
}
func buildToolFor(a *app, target, output, goTool string) error {
	system, arch := "linux", "amd64"
	if target == "linux-arm64" {
		arch = "arm64"
	}
	if target == "windows-x64" {
		system = "windows"
	}
	env := append(os.Environ(), "CGO_ENABLED=0", "GOOS="+system, "GOARCH="+arch, "GOTOOLCHAIN=local")
	return buildRun(a, a.path("tools/devtool"), env, goTool, "build", "-trimpath", "-buildvcs=false", "-o", output, ".")
}
func copiedBuildRecipes(a *app, destination string) (record, error) {
	out := record{}
	entries, e := os.ReadDir(a.path("tools/devtool"))
	if e != nil {
		return nil, e
	}
	for _, d := range entries {
		if d.IsDir() || !(strings.HasSuffix(d.Name(), ".go") || d.Name() == "go.mod" || d.Name() == "go.sum") {
			continue
		}
		source := a.path("tools/devtool", d.Name())
		if e = copyFile(source, filepath.Join(destination, "tools/devtool", d.Name())); e != nil {
			return nil, e
		}
		h, e := digest(source)
		if e != nil {
			return nil, e
		}
		out["tools/devtool/"+d.Name()] = h
	}
	return out, nil
}
func validateLinuxRecipeCache(a *app, producer record, names []string) error {
	for _, name := range names {
		expected := object(producer, "copied_tool_inputs")["tools/devtool/"+name]
		if expected == nil {
			return fmt.Errorf("cached compiler receipt predates the Go recipe; rebuild dependencies once")
		}
		if e := requireHash(a.path("tools/devtool", name), expected); e != nil {
			return fmt.Errorf("Linux compiler recipe changed since cache creation: %s", name)
		}
	}
	return nil
}
func linuxIncrementalCache(a *app, o options, target string, producer record) (record, error) {
	previous := filepath.Join(filepath.Dir(filepath.Dir(o.get("core-producer"))), "inputs/source")
	identity, e := sourceIdentity(previous)
	if e != nil {
		return nil, e
	}
	if identity != producer["source_tree_sha256"] {
		return nil, fmt.Errorf("cache producer frozen source identity differs")
	}
	if e = validateLinuxRecipeCache(a, producer, []string{"build.go", "nativeposix.go", "integrate.go", "posix_prerequisites.go"}); e != nil {
		return nil, e
	}
	if e = requireHash(o.get("reuse-core-build"), object(producer, "artifact")["sha256"]); e != nil {
		return nil, e
	}
	before, e := hashes(previous)
	if e != nil {
		return nil, e
	}
	after, e := hashes(o.get("source"))
	if e != nil {
		return nil, e
	}
	patchBytes, e := os.ReadFile(a.path("native-core/patches/series.json"))
	if e != nil {
		return nil, e
	}
	var patches []record
	if e = json.Unmarshal(patchBytes, &patches); e != nil {
		return nil, e
	}
	changed, added := []string{}, []string{}
	for name, oldHash := range before {
		newHash, ok := after[name]
		if !ok {
			return nil, fmt.Errorf("incremental source removals refused: %s", name)
		}
		if oldHash == newHash {
			continue
		}
		changed = append(changed, name)
		if name == "git-next-identity.h" || name == "git-next-integration-receipt.json" {
			continue
		}
		declaredBefore, declaredAfter := false, false
		for _, p := range patches {
			if p["path"] == name {
				declaredBefore = declaredBefore || p["before_sha256"] == oldHash
				declaredAfter = declaredAfter || p["after_sha256"] == newHash
			}
		}
		if !declaredBefore || !declaredAfter {
			return nil, fmt.Errorf("source change has no declared patch chain: %s", name)
		}
	}
	for name, newHash := range after {
		if _, ok := before[name]; ok {
			continue
		}
		if e = requireHash(a.path("native-core/overlay", name), newHash); e != nil {
			return nil, fmt.Errorf("source addition must match declared overlay: %s", name)
		}
		added = append(added, name)
	}
	if len(changed)+len(added) == 0 {
		return nil, fmt.Errorf("no source delta requires incremental compilation")
	}
	sort.Strings(changed)
	sort.Strings(added)
	producerHash, e := digest(o.get("core-producer"))
	if e != nil {
		return nil, e
	}
	return record{"archive_sha256": object(producer, "artifact")["sha256"], "producer_receipt_sha256": producerHash, "previous_source_tree_sha256": identity, "changed_sources": changed, "added_overlays": added, "cache_kind": "declared-patch-incremental-objects", "unrelated_platform_recipe_changes": []record{}, "final_executables_relinked": true}, nil
}
func buildNativeLinux(a *app, o options) (returned error) {
	if e := o.require("platform", "source", "core-archive", "dependency-cache", "base-image", "destination"); e != nil {
		return e
	}
	target := o.get("platform")
	if target != "linux-x64" && target != "linux-arm64" {
		return fmt.Errorf("invalid Linux target")
	}
	if !regexp.MustCompile(`^debian@sha256:[a-f0-9]{64}$`).MatchString(o.get("base-image")) {
		return fmt.Errorf("official immutable Debian base image required")
	}
	arch := "amd64"
	if target == "linux-arm64" {
		arch = "arm64"
	}
	images, e := dockerJSON(a, "image", "inspect", o.get("base-image"))
	if e != nil {
		return e
	}
	if len(images) != 1 || images[0]["Os"] != "linux" || images[0]["Architecture"] != arch {
		return fmt.Errorf("available base image does not match requested architecture")
	}
	parallel, e := jobs(o)
	if e != nil {
		return e
	}
	if (o.get("reuse-core-build") == "") != (o.get("core-producer") == "") || (o.get("reuse-dependencies") == "") != (o.get("dependency-producer") == "") {
		return fmt.Errorf("cache requires its matching producer receipt")
	}
	if o.get("reuse-core-build") != "" && o.get("reuse-dependencies") != "" {
		return fmt.Errorf("select one cache producer")
	}
	var reused, coreCache record
	for _, kind := range []string{"dependency", "core"} {
		path := o.get(kind + "-producer")
		if path == "" {
			continue
		}
		producer, e := load(path)
		if e != nil {
			return e
		}
		if producer["platform"] != target || producer["base_image"] != o.get("base-image") || !allTrue(object(producer, "cleanup")) {
			return fmt.Errorf("cache producer identity or cleanup differs")
		}
		if kind == "core" {
			coreCache, e = linuxIncrementalCache(a, o, target, producer)
			if e != nil {
				return e
			}
		} else {
			if e = validateLinuxRecipeCache(a, producer, []string{"build.go", "posix_prerequisites.go"}); e != nil {
				return e
			}
			var dependency record
			e = eachArchive(o.get("reuse-dependencies"), func(m archiveMember, r io.Reader) error {
				if !strings.HasPrefix(m.name, "dependencies/") && !strings.HasPrefix(m.name, "tls/") && m.name != "dependencies" && m.name != "tls" {
					return fmt.Errorf("cache may contain only dependencies/prerequisites")
				}
				if m.name == "dependencies/receipt.json" {
					return json.NewDecoder(io.LimitReader(r, 4<<20)).Decode(&dependency)
				}
				return nil
			})
			if e != nil {
				return e
			}
			lockHash, e := digest(a.path("packaging/native-dependencies.lock.json"))
			if e != nil {
				return e
			}
			if dependency["platform"] != target || dependency["lock_sha256"] != lockHash {
				return fmt.Errorf("cache dependency pins differ")
			}
			h, e := digest(o.get("reuse-dependencies"))
			if e != nil {
				return e
			}
			p, e := digest(path)
			if e != nil {
				return e
			}
			reused = record{"archive_sha256": h, "producer_receipt_sha256": p}
		}
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	before, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	pin, e := load(a.path("packaging/native-core.lock.json"))
	if e != nil {
		return e
	}
	if e = requireHash(o.get("core-archive"), pin["archive_sha256"]); e != nil {
		return e
	}
	destination, e := absolute(o.get("destination"))
	if e != nil {
		return e
	}
	if strings.Contains(destination, ",") {
		return fmt.Errorf("Docker bind paths containing commas unsupported")
	}
	destination, e = newDestination(destination)
	if e != nil {
		return e
	}
	inputs, outputs := filepath.Join(destination, "inputs"), filepath.Join(destination, "output")
	for _, p := range []string{inputs, outputs, filepath.Join(inputs, "packaging"), filepath.Join(inputs, "archives")} {
		if e = os.MkdirAll(p, 0755); e != nil {
			return e
		}
	}
	if e = copyOwnedTree(source, filepath.Join(inputs, "source")); e != nil {
		return e
	}
	copied, e := sourceIdentity(filepath.Join(inputs, "source"))
	if e != nil || copied != before {
		return fmt.Errorf("copied core inputs differ from source receipt")
	}
	recipes, e := copiedBuildRecipes(a, inputs)
	if e != nil {
		return e
	}
	for _, name := range []string{"native-core.lock.json", "native-dependencies.lock.json", "product.json"} {
		if e = copyFile(a.path("packaging", name), filepath.Join(inputs, "packaging", name)); e != nil {
			return e
		}
	}
	for _, name := range []string{"Cargo.toml", "compatibility.json"} {
		if e = copyFile(a.path(name), filepath.Join(inputs, name)); e != nil {
			return e
		}
	}
	lock, e := load(a.path("packaging/native-dependencies.lock.json"))
	if e != nil {
		return e
	}
	for _, raw := range object(lock, "components") {
		component, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid dependency pin")
		}
		archive := filepath.Join(o.get("dependency-cache"), stringValue(component, "archive"))
		info, e := os.Stat(archive)
		if e != nil {
			return e
		}
		if info.Size() != number(component, "bytes") {
			return fmt.Errorf("cached dependency size differs")
		}
		if e = requireHash(archive, component["sha256"]); e != nil {
			return e
		}
		if e = copyFile(archive, filepath.Join(inputs, "archives", filepath.Base(archive))); e != nil {
			return e
		}
	}
	modes := map[string]os.FileMode{}
	z, e := zip.OpenReader(o.get("core-archive"))
	if e != nil {
		return e
	}
	for _, m := range z.File {
		if m.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(m.Name, stringValue(pin, "archive_prefix"))
		if name == m.Name {
			continue
		}
		mode := m.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		modes[name] = mode
	}
	z.Close()
	sourceTar := filepath.Join(inputs, "source-input.tar")
	if e = sourceTransfer(filepath.Join(inputs, "source"), sourceTar, modes); e != nil {
		return e
	}
	sourceTarHash, e := digest(sourceTar)
	if e != nil {
		return e
	}
	for _, pair := range []struct {
		input, name string
		value       record
	}{{o.get("reuse-dependencies"), "cached-dependencies.tar.gz", reused}, {o.get("reuse-core-build"), "cached-core-build.tar.gz", coreCache}} {
		if pair.value == nil {
			continue
		}
		p := filepath.Join(inputs, pair.name)
		if e = copyFile(pair.input, p); e != nil {
			return e
		}
		if e = requireHash(p, pair.value["archive_sha256"]); e != nil {
			return e
		}
	}
	if e = buildToolFor(a, target, filepath.Join(inputs, "tool"), o.value("go", "go")); e != nil {
		return e
	}
	toolHash, e := digest(filepath.Join(inputs, "tool"))
	if e != nil {
		return e
	}
	if e = write(filepath.Join(inputs, "compile.sh"), []byte(linuxCompileScript), 0755); e != nil {
		return e
	}
	scope, e := scopeID()
	if e != nil {
		return e
	}
	volume, container := "git-next-native-build-"+scope, "git-next-native-compile-"+scope
	owner := "git-next-native-core-build"
	cleanup := record{}
	status := "failed"
	defer func() {
		for _, resource := range []struct{ kind, name string }{{"container", container}, {"volume", volume}} {
			if e := removeDockerOwned(a, resource.kind, resource.name, owner, scope); e != nil && returned == nil {
				returned = e
			}
			cleanup[resource.kind+"_absent"] = dockerAbsent(a, resource.kind, resource.name)
		}
		record := record{"schema_version": 1, "status": status, "platform": target, "base_image": o.get("base-image"), "base_image_id": images[0]["Id"], "source_tree_sha256": before, "source_transfer_archive_sha256": sourceTarHash, "scope": scope, "cleanup": cleanup, "product_executed": false, "tests_executed": false, "copied_tool_inputs": recipes, "copied_tool_binary_sha256": toolHash, "dependency_cache": reused, "verified_incremental_core_cache": coreCache, "installed": false, "release_eligible": false}
		p := filepath.Join(outputs, "native-build-products.tar.gz")
		if exists(p) {
			f, e := fileRecord(p)
			if e == nil {
				f["name"] = filepath.Base(p)
				record["artifact"] = f
			}
		}
		if e := writeNewJSON(filepath.Join(outputs, "docker-compilation.json"), record); e != nil && returned == nil {
			returned = e
		}
		_ = a.json(record)
	}()
	if e = buildRun(a, a.root, nil, "docker", "volume", "create", "--label", "git-next.owner="+owner, "--label", "git-next.scope="+scope, volume); e != nil {
		return e
	}
	args := []string{"run", "--rm", "--init", "--pull", "never", "--platform", "linux/" + arch, "--name", container, "--label", "git-next.owner=" + owner, "--label", "git-next.scope=" + scope, "--pids-limit", "256", "--memory", "8g", "--cpus", "8", "--mount", "type=bind,source=" + inputs + ",target=/inputs,readonly", "--mount", "type=bind,source=" + outputs + ",target=/output", "--mount", "type=volume,source=" + volume + ",target=/build", "--env", "GIT_NEXT_BUILD_TARGET=" + target, "--env", "GIT_NEXT_BUILD_BASE=" + o.get("base-image"), "--env", "GIT_NEXT_BUILD_JOBS=" + parallel, o.get("base-image"), "/bin/sh", "/inputs/compile.sh"}
	if e = buildRun(a, a.root, nil, "docker", args...); e != nil {
		return e
	}
	after, e := sourceIdentity(filepath.Join(inputs, "source"))
	if e != nil {
		return e
	}
	if after != before {
		return fmt.Errorf("frozen compiler inputs changed while building")
	}
	status = "compiled-not-runtime-accepted"
	return nil
}

const linuxCompileScript = `#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive HOME=/tmp/git-next-build-home USERPROFILE=/tmp/git-next-build-home SSH_ROOT_PATH=/tmp/git-next-build-ssh
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 GIT_NEXT_ROOT=/inputs
mkdir -p "$HOME" "$SSH_ROOT_PATH"
apt-get update
apt-get install -y --no-install-recommends ca-certificates build-essential cmake ninja-build pkg-config libgnutls28-dev libpsl-dev libidn2-dev libkrb5-dev libsecret-1-dev binutils
trap 'result=$?; if [ "$result" -ne 0 ] && [ -f /build/dependencies/receipt.json ] && [ -f /build/tls/receipt.json ]; then tar -C /build -czf /output/failed-build-dependencies.tar.gz dependencies tls; fi' EXIT
tar -xf /inputs/source-input.tar -C /build
if [ -f /inputs/cached-core-build.tar.gz ]; then
    /inputs/tool extract-build-archive /inputs/cached-core-build.tar.gz /build
    /inputs/tool native-linux-cache-prepare --root /build
elif [ -f /inputs/cached-dependencies.tar.gz ]; then
    /inputs/tool extract-build-archive /inputs/cached-dependencies.tar.gz /build
else
    /inputs/tool collect-posix-prerequisites --platform "$GIT_NEXT_BUILD_TARGET" --destination /build/tls --base-image "$GIT_NEXT_BUILD_BASE"
    /inputs/tool build-native-dependencies --platform "$GIT_NEXT_BUILD_TARGET" --work-dir /build/dependency-work --cache /inputs/archives --prefix /build/dependencies --tls-prefix /build/tls --jobs "$GIT_NEXT_BUILD_JOBS"
fi
/inputs/tool build-native-posix --platform "$GIT_NEXT_BUILD_TARGET" --source /build/source --build-dir /build/core-build --dependencies /build/dependencies --stage /build/core-stage --receipt /output/core-build.json --jobs "$GIT_NEXT_BUILD_JOBS"
cp /build/dependencies/receipt.json /output/dependency-build.json
cp /build/tls/receipt.json /output/tls-prerequisites.json
dpkg-query -W '-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n' > /output/build-environment-packages.tsv
tar -C /build -czf /output/native-build-products.tar.gz core-stage dependencies tls core-build
`
