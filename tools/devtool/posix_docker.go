// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func init() {
	register("prepare-linux-runtime", "Prepare Linux runtime in owned disposable Docker storage", "core-artifacts primary-helpers destination go", prepareLinuxRuntime)
	register("package-native-linux", "Assemble source-matched Linux distribution in owned Docker storage", "source core-artifacts runtime-artifacts shared-assets updater git-p4 destination go", packageNativeLinux)
}
func packagingAllTrue(m record) bool {
	if len(m) == 0 {
		return false
	}
	for _, v := range m {
		if v != true {
			return false
		}
	}
	return true
}
func producerArtifact(base string, receipt record) (string, error) {
	item := object(receipt, "artifact")
	name := stringValue(item, "name")
	if name == "" || name != filepathBase(name) {
		return "", fmt.Errorf("unsafe producer artifact name")
	}
	p := filepath.Join(base, name)
	return p, shaMatches(p, item)
}
func scaffoldTool(a *app, inputs, target, goTool string) (record, error) {
	recipes, e := copiedBuildRecipes(a, inputs)
	if e != nil {
		return nil, e
	}
	if e = copyOwnedTree(a.path("tools/git-p4"), filepath.Join(inputs, "tools/git-p4")); e != nil {
		return nil, e
	}
	p4Sources, e := gitP4Sources(a)
	if e != nil {
		return nil, e
	}
	for name, h := range p4Sources {
		recipes["tools/git-p4/"+name] = h
	}
	for _, name := range []string{"Cargo.toml", "compatibility.json"} {
		if e = copyFile(a.path(name), filepath.Join(inputs, name)); e != nil {
			return nil, e
		}
	}
	if e = buildToolFor(a, target, filepath.Join(inputs, "tool"), goTool); e != nil {
		return nil, e
	}
	return recipes, nil
}
func dockerOwnedPhase(a *app, owner, target, image, inputs, outputs, script string, env []string) (record, error) {
	if !regexp.MustCompile(`^debian@sha256:[a-f0-9]{64}$`).MatchString(image) {
		return nil, fmt.Errorf("official immutable Debian image required")
	}
	scope, e := scopeID()
	if e != nil {
		return nil, e
	}
	volume, container := "git-next-"+scope, "git-next-"+scope
	cleanup := record{}
	labels := []string{"--label", "git-next.owner=" + owner, "--label", "git-next.scope=" + scope}
	err := buildRun(a, a.root, nil, "docker", append([]string{"volume", "create"}, append(labels, volume)...)...)
	if err == nil {
		arch := "amd64"
		if target == "linux-arm64" {
			arch = "arm64"
		}
		args := []string{"run", "--rm", "--init", "--pull", "never", "--platform", "linux/" + arch, "--name", container, "--pids-limit", "256", "--memory", "8g", "--cpus", "8"}
		args = append(args, labels...)
		args = append(args, "--mount", "type=bind,source="+inputs+",target=/inputs,readonly", "--mount", "type=bind,source="+outputs+",target=/output", "--mount", "type=volume,source="+volume+",target=/build", "--env", "GIT_NEXT_ROOT=/inputs")
		for _, v := range env {
			args = append(args, "--env", v)
		}
		args = append(args, image, "/bin/sh", "/inputs/"+script)
		err = buildRun(a, a.root, nil, "docker", args...)
	}
	for _, v := range [][2]string{{"container", container}, {"volume", volume}} {
		e := removeDockerOwned(a, v[0], v[1], owner, scope)
		if e != nil && err == nil {
			err = e
		}
		cleanup[v[0]+"_absent"] = dockerAbsent(a, v[0], v[1])
	}
	if !packagingAllTrue(cleanup) && err == nil {
		err = fmt.Errorf("Docker cleanup failed")
	}
	return record{"scope": scope, "cleanup": cleanup}, err
}
func prepareLinuxRuntime(a *app, o options) error {
	if e := o.require("core-artifacts", "primary-helpers", "destination"); e != nil {
		return e
	}
	core := o.get("core-artifacts")
	build, e := load(filepath.Join(core, "core-build.json"))
	if e != nil {
		return e
	}
	producer, e := load(filepath.Join(core, "docker-compilation.json"))
	if e != nil {
		return e
	}
	target := stringValue(build, "platform")
	if target != "linux-x64" && target != "linux-arm64" || producer["status"] != "compiled-not-runtime-accepted" || !packagingAllTrue(object(producer, "cleanup")) {
		return fmt.Errorf("completed owned Linux core receipt required")
	}
	archive, e := producerArtifact(core, producer)
	if e != nil {
		return e
	}
	helpersRoot := o.get("primary-helpers")
	helpers, e := load(filepath.Join(helpersRoot, "git-next-posix-helpers.json"))
	if e != nil {
		return e
	}
	if helpers["platform"] != target {
		return fmt.Errorf("helper target differs")
	}
	if strings.Contains(o.get("destination"), ",") {
		return fmt.Errorf("material path cannot contain commas")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	inputs, outputs := filepath.Join(out, "inputs"), filepath.Join(out, "output")
	if e = os.MkdirAll(outputs, 0755); e != nil {
		return e
	}
	recipes, e := scaffoldTool(a, inputs, target, o.value("go", "go"))
	if e != nil {
		return e
	}
	if e = copyFile(archive, filepath.Join(inputs, "core-build.tar.gz")); e != nil {
		return e
	}
	if e = copyFile(filepath.Join(helpersRoot, "git-next-posix-helpers.json"), filepath.Join(inputs, "helpers/git-next-posix-helpers.json")); e != nil {
		return e
	}
	for name, v := range object(helpers, "files") {
		item := rec(v)
		if item["kind"] != "file" {
			continue
		}
		p, e := verifiedFile(helpersRoot, name, item)
		if e != nil {
			return e
		}
		if e = copyFile(p, filepath.Join(inputs, "helpers", filepath.FromSlash(name))); e != nil {
			return e
		}
	}
	script := `#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive HOME=/tmp/git-next-runtime-home USERPROFILE=/tmp/git-next-runtime-home SSH_ROOT_PATH=/tmp/git-next-runtime-ssh
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
mkdir -p "$HOME" "$SSH_ROOT_PATH"
apt-get update
apt-get install -y --no-install-recommends ca-certificates binutils openssh-client gnupg scdaemon pinentry-curses perl tcl tk coreutils findutils diffutils gawk sed grep patch tar gzip less file libicu72 libsecret-1-0 libssl3 libstdc++6
/inputs/tool extract-build-archive /inputs/core-build.tar.gz /build
/inputs/tool collect-linux-runtime --platform "$GIT_NEXT_RUNTIME_PLATFORM" --destination /build/runtime-assets --core-stage /build/core-stage --tls-prefix /build/tls --primary-helpers /inputs/helpers --base-image "$GIT_NEXT_RUNTIME_BASE"
cp /build/runtime-assets/git-next-posix-runtime.json /output/runtime-assets.json
tar -C /build/runtime-assets -czf /output/runtime-assets.tar.gz .
`
	if e = write(filepath.Join(inputs, "prepare.sh"), []byte(script), 0755); e != nil {
		return e
	}
	metadata, runErr := dockerOwnedPhase(a, "git-next-linux-runtime-preparation", target, stringValue(producer, "base_image"), inputs, outputs, "prepare.sh", []string{"GIT_NEXT_RUNTIME_PLATFORM=" + target, "GIT_NEXT_RUNTIME_BASE=" + stringValue(producer, "base_image")})
	status := "runtime-materials-prepared-not-runtime-accepted"
	if runErr != nil {
		status = "failed"
	}
	ah, _ := digest(archive)
	hh, _ := digest(filepath.Join(helpersRoot, "git-next-posix-helpers.json"))
	receipt := record{"schema_version": 1, "status": status, "platform": target, "scope": metadata["scope"], "base_image": producer["base_image"], "input_core_archive_sha256": ah, "helper_manifest_sha256": hh, "copied_tool_inputs": recipes, "cleanup": metadata["cleanup"], "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false}
	artifact := filepath.Join(outputs, "runtime-assets.tar.gz")
	if st, e := os.Stat(artifact); e == nil {
		h, e := digest(artifact)
		if e != nil {
			return e
		}
		receipt["artifact"] = record{"name": filepath.Base(artifact), "sha256": h, "bytes": st.Size()}
	}
	if e = writeNewJSON(filepath.Join(outputs, "docker-runtime-materials.json"), receipt); e != nil {
		return e
	}
	if runErr != nil {
		return runErr
	}
	return a.json(record{"status": status, "platform": target, "cleanup": metadata["cleanup"], "product_executed": false})
}
func packageNativeLinux(a *app, o options) error {
	if e := o.require("source", "core-artifacts", "runtime-artifacts", "shared-assets", "updater", "git-p4", "destination"); e != nil {
		return e
	}
	core, runtimeRoot := o.get("core-artifacts"), o.get("runtime-artifacts")
	producer, e := load(filepath.Join(core, "docker-compilation.json"))
	if e != nil {
		return e
	}
	materials, e := load(filepath.Join(runtimeRoot, "docker-runtime-materials.json"))
	if e != nil {
		return e
	}
	build, e := load(filepath.Join(core, "core-build.json"))
	if e != nil {
		return e
	}
	identity, e := sourceIdentity(o.get("source"))
	if e != nil {
		return e
	}
	if producer["status"] != "compiled-not-runtime-accepted" || !packagingAllTrue(object(producer, "cleanup")) || !packagingAllTrue(object(materials, "cleanup")) || build["source_tree_sha256"] != identity || materials["platform"] != build["platform"] {
		return fmt.Errorf("source-matched cleaned native producers required")
	}
	coreArchive, e := producerArtifact(core, producer)
	if e != nil {
		return e
	}
	runtimeArchive, e := producerArtifact(runtimeRoot, materials)
	if e != nil {
		return e
	}
	if strings.Contains(o.get("destination"), ",") {
		return fmt.Errorf("task path cannot contain commas")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	inputs, outputs := filepath.Join(out, "inputs"), filepath.Join(out, "output")
	if e = os.MkdirAll(outputs, 0755); e != nil {
		return e
	}
	if _, e = scaffoldTool(a, inputs, stringValue(build, "platform"), o.value("go", "go")); e != nil {
		return e
	}
	if e = copyOwnedTree(o.get("source"), filepath.Join(inputs, "source")); e != nil {
		return e
	}
	for _, folder := range []string{"packaging", "docs", "native-core"} {
		if e = copyOwnedTree(a.path(folder), filepath.Join(inputs, folder)); e != nil {
			return e
		}
	}
	for _, name := range []string{"README.md", "README.zh-CN.md", "AGENTS.md"} {
		if e = copyFile(a.path(name), filepath.Join(inputs, name)); e != nil {
			return e
		}
	}
	if e = copyOwnedTree(o.get("shared-assets"), filepath.Join(inputs, "shared")); e != nil {
		return e
	}
	if e = copyOwnedTree(o.get("git-p4"), filepath.Join(inputs, "git-p4")); e != nil {
		return e
	}
	for _, v := range [][2]string{{o.get("updater"), "updater"}, {filepath.Join(core, "core-build.json"), "core-build.json"}, {coreArchive, "core.tar.gz"}, {runtimeArchive, "runtime.tar.gz"}} {
		if e = copyFile(v[0], filepath.Join(inputs, v[1])); e != nil {
			return e
		}
	}
	script := `#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive HOME=/tmp/git-next-package-home USERPROFILE=/tmp/git-next-package-home SSH_ROOT_PATH=/tmp/git-next-package-ssh
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
mkdir -p "$HOME" "$SSH_ROOT_PATH"
apt-get update
apt-get install -y --no-install-recommends binutils patchelf
/inputs/tool extract-build-archive /inputs/core.tar.gz /build
mkdir /build/runtime
/inputs/tool extract-runtime-archive /inputs/runtime.tar.gz /build/runtime
/inputs/tool assemble-native-posix --source /inputs/source --core-stage /build/core-stage --core-receipt /inputs/core-build.json --runtime-assets /build/runtime --shared-assets /inputs/shared --updater /inputs/updater --git-p4 /inputs/git-p4 --destination /build/distribution
cp /build/distribution/package.json /output/package.json
cp /build/distribution/git-next-distribution.json /output/git-next-distribution.json
tar -C /build/distribution -czf /output/distribution.tar.gz .
`
	if e = write(filepath.Join(inputs, "assemble.sh"), []byte(script), 0755); e != nil {
		return e
	}
	metadata, runErr := dockerOwnedPhase(a, "git-next-linux-package-assembly", stringValue(build, "platform"), stringValue(producer, "base_image"), inputs, outputs, "assemble.sh", nil)
	status := "assembled-not-runtime-accepted"
	if runErr != nil {
		status = "failed"
	}
	ch, _ := digest(filepath.Join(inputs, "core.tar.gz"))
	rh, _ := digest(filepath.Join(inputs, "runtime.tar.gz"))
	uh, _ := digest(filepath.Join(inputs, "updater"))
	receipt := record{"schema_version": 1, "status": status, "platform": build["platform"], "source_tree_sha256": build["source_tree_sha256"], "inputs": record{"core": ch, "runtime": rh, "updater": uh}, "cleanup": metadata["cleanup"], "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false}
	artifact := filepath.Join(outputs, "distribution.tar.gz")
	if st, e := os.Stat(artifact); e == nil {
		h, e := digest(artifact)
		if e != nil {
			return e
		}
		receipt["artifact"] = record{"name": filepath.Base(artifact), "sha256": h, "bytes": st.Size()}
	}
	if e = writeNewJSON(filepath.Join(outputs, "assembly.json"), receipt); e != nil {
		return e
	}
	if runErr != nil {
		return runErr
	}
	return a.json(record{"status": status, "platform": build["platform"], "cleanup": metadata["cleanup"]})
}
