// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const migrationGCMURL = "https://github.com/git-ecosystem/git-credential-manager/releases/download/v2.7.3/gcm-linux-x64-2.7.3.tar.gz"
const migrationGCMSHA = "c7935c3e1b22681d56011b580086aeb6d60f3f0aaf6ef9c581480356988ff804"

func traceExecutables(trace string) []string {
	set := map[string]bool{}
	pattern := regexp.MustCompile(`execve\("([^"\n]+)"`)
	for _, m := range pattern.FindAllStringSubmatch(trace, -1) {
		set[m[1]] = true
	}
	return keys(set)
}
func migrationHost(a *app, o options) (returnErr error) {
	if e := o.require("output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("migration report must be new")
	}
	native := o.get("native-package")
	accepted := record{}
	if native != "" {
		var e error
		native, e = absolute(native)
		if e != nil {
			return e
		}
		assembly, e := load(filepath.Join(filepath.Dir(native), "assembly.json"))
		if e != nil {
			return e
		}
		if stringValue(assembly, "status") != "assembled-not-runtime-accepted" {
			return fmt.Errorf("native package lacks assembled receipt")
		}
		for _, v := range object(assembly, "cleanup") {
			if v != true {
				return fmt.Errorf("native assembly did not clean its owned resources")
			}
		}
		if e = requireHash(native, object(assembly, "artifact")["sha256"]); e != nil {
			return e
		}
		var manifest record
		e = eachArchive(native, func(m archiveMember, r io.Reader) error {
			if strings.TrimPrefix(m.name, "./") != "package.json" {
				return nil
			}
			if m.directory || m.symlink || m.hardlink || m.size > 32<<20 {
				return fmt.Errorf("invalid package manifest")
			}
			if manifest != nil {
				return fmt.Errorf("duplicate package manifest")
			}
			data, e := io.ReadAll(io.LimitReader(r, 32<<20))
			if e != nil {
				return e
			}
			return decodeJSON(data, &manifest)
		})
		if e != nil {
			return e
		}
		if stringValue(manifest, "platform") != "linux-x64" || stringValue(manifest, "engine") != "source-built-git-next-native-core" {
			return fmt.Errorf("migration requires Linux x64 native core package")
		}
		accepted = record{"source_tree_sha256": manifest["source_tree_sha256"], "binaries": record{"git-next": object(object(manifest, "files"), "bin/git")["sha256"]}}
	} else {
		native = a.path("target/x86_64-unknown-linux-musl/release")
		var e error
		accepted, e = load(a.path(".evidence/cross-x86_64-unknown-linux-musl.json"))
		if e != nil {
			return e
		}
		for name, want := range object(accepted, "binaries") {
			if e = requireHash(filepath.Join(native, name), want); e != nil {
				return e
			}
		}
	}
	archive := a.path(".evidence/gcm-linux-x64-2.7.3.tar.gz")
	if !exists(archive) {
		if _, e := downloadVariable(migrationGCMURL, migrationGCMSHA, archive, 100<<20); e != nil {
			return e
		}
	}
	if e := requireHash(archive, migrationGCMSHA); e != nil {
		return e
	}
	fixture, e := os.MkdirTemp("", "git-next-migration-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(fixture)
	gcm := filepath.Join(fixture, "gcm")
	if _, _, e = extractArchive(archive, gcm, extractPolicy{maxFiles: 4, maxBytes: 100 << 20}); e != nil {
		return e
	}
	entries, e := os.ReadDir(gcm)
	if e != nil {
		return e
	}
	expected := wordSet("NOTICE git-credential-manager libHarfBuzzSharp.so libSkiaSharp.so")
	if len(entries) != len(expected) {
		return fmt.Errorf("unexpected GCM package members")
	}
	for _, d := range entries {
		if !expected[d.Name()] || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected GCM package member")
		}
	}
	tool := filepath.Join(fixture, "git-next-tool")
	if e = buildToolFor(a, "linux-x64", tool, o.value("go", "go")); e != nil {
		return e
	}
	identity, e := scopeID()
	if e != nil {
		return e
	}
	prefix := "git-next-migration-" + identity
	owner := "git-next-migration-acceptance"
	network, volume, server, client := prefix+"-network", prefix+"-volume", prefix+"-server", prefix+"-client"
	tags := []string{prefix + ":server", prefix + ":client"}
	labels := []string{"--label", "git-next.owner=" + owner, "--label", "git-next.scope=" + identity}
	started := time.Now()
	var created []struct{ kind, name string }
	report := record{"status": "fail", "scope": "Docker Linux x64 fixture accounts; no personal profiles or accounts", "source_tree_sha256": accepted["source_tree_sha256"], "binary_sha256": object(accepted, "binaries")["git-next"], "gcm_archive_sha256": migrationGCMSHA, "gcm_archive_url": migrationGCMURL, "images": record{}, "cleanup": []record{}, "isolation": record{"runtime_network_internal": true, "published_ports": false, "real_profile_mounted": false, "read_only_rootfs": true, "fixture_data_in_disposable_volume_and_tmpfs": true}, "process_trace": "strace -f execve with seccomp-bpf filtering; assertions preserved"}
	harnesses := record{}
	for _, name := range []string{"tools/devtool/migration_host.go", "tools/devtool/migration_fixture.go", "tools/devtool/fixture_http.go", "tools/devtool/fixture_ssh.go", "tests/containers/migration.Dockerfile"} {
		h, e := digest(a.path(name))
		if e != nil {
			return e
		}
		harnesses[name] = h
	}
	toolHash, e := digest(tool)
	if e != nil {
		return e
	}
	harnesses["compiled-tool"] = toolHash
	report["harnesses"] = harnesses
	docker := func(timeout time.Duration, check bool, args ...string) (result, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		r, e := runProcess(ctx, a.root, nil, nil, "docker", args...)
		if e != nil {
			return r, e
		}
		if check && r.code != 0 {
			return r, fmt.Errorf("Docker %s failed: exit=%d", args[0], r.code)
		}
		return r, nil
	}
	defer func() {
		var cleanup []record
		for i := len(created) - 1; i >= 0; i-- {
			item := created[i]
			err := migrationRemoveResource(a, item.kind, item.name, owner, identity)
			absent := dockerAbsent(a, item.kind, item.name)
			cleanup = append(cleanup, record{"kind": item.kind, "name": item.name, "absent_after_cleanup": absent})
			if err != nil || !absent {
				if returnErr == nil {
					returnErr = fmt.Errorf("owned migration %s cleanup failed", item.kind)
				}
			}
		}
		report["cleanup"] = cleanup
		report["all_owned_resources_removed"] = true
		for _, row := range cleanup {
			if !truth(row, "absent_after_cleanup") {
				report["all_owned_resources_removed"] = false
			}
		}
		if returnErr != nil {
			report["status"] = "fail"
			report["failure"] = returnErr.Error()
		}
		report["elapsed_seconds"] = roundedSeconds(time.Since(started))
		if e := writeNewJSON(o.get("output"), report); e != nil {
			returnErr = e
		}
		if e := a.json(record{"status": report["status"], "report": o.get("output"), "all_owned_resources_removed": report["all_owned_resources_removed"]}); e != nil {
			returnErr = e
		}
	}()
	base := nativeBases["linux-x64"]
	if dockerAbsent(a, "image", base) {
		if _, e = docker(5*time.Minute, true, "pull", base); e != nil {
			return e
		}
	}
	baseInfo, e := dockerJSON(a, "image", "inspect", base)
	if e != nil {
		return e
	}
	if len(baseInfo) != 1 {
		return fmt.Errorf("unexpected base image metadata")
	}
	baseID := baseInfo[0]["Id"]
	report["reference_base_image"] = baseID
	for i, stage := range []string{"server", "client"} {
		tag := tags[i]
		if !dockerAbsent(a, "image", tag) {
			return fmt.Errorf("migration image name collision")
		}
		created = append(created, struct{ kind, name string }{"image", tag})
		args := append([]string{"build", "--platform", "linux/amd64"}, labels...)
		args = append(args, "--target", stage, "--build-arg", "BASE="+base, "--build-arg", "SERVER_BASE="+base, "-t", tag, "-f", a.path("tests/containers/migration.Dockerfile"), a.path("tests/containers"))
		r, e := docker(10*time.Minute, false, args...)
		if err := write(o.get("output")+"."+stage+"-build.log", append(append(r.stdout, '\n'), r.stderr...), 0600); err != nil {
			return err
		}
		if e != nil || r.code != 0 {
			return fmt.Errorf("migration %s image build failed", stage)
		}
		info, e := dockerJSON(a, "image", "inspect", tag)
		if e != nil {
			return e
		}
		object(report, "images")[stage] = info[0]["Id"]
	}
	readback, e := dockerJSON(a, "image", "inspect", base)
	if e != nil {
		return e
	}
	if len(readback) != 1 || readback[0]["Id"] != baseID {
		return fmt.Errorf("reference base image changed")
	}
	for _, item := range []struct{ kind, name string }{{"network", network}, {"volume", volume}} {
		if !dockerAbsent(a, item.kind, item.name) {
			return fmt.Errorf("migration resource name collision")
		}
		args := append([]string{item.kind, "create"}, labels...)
		if item.kind == "network" {
			args = append(args, "--internal")
		}
		args = append(args, item.name)
		if _, e = docker(time.Minute, true, args...); e != nil {
			return e
		}
		created = append(created, item)
	}
	mount := func(source, target string) []string {
		return []string{"--mount", "type=bind,source=" + source + ",target=" + target + ",readonly"}
	}
	common := []string{"--init", "--platform", "linux/amd64", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit", "128", "--memory", "2g", "--cpus", "4", "--tmpfs", "/tmp:rw,exec,size=512m", "--mount", "type=volume,source=" + volume + ",target=/migration"}
	common = append(common, labels...)
	common = append(common, mount(gcm, "/inputs/gcm")...)
	common = append(common, mount(tool, "/harness/git-next-tool")...)
	created = append(created, struct{ kind, name string }{"container", server})
	args := []string{"run", "-d", "--name", server, "--network", network}
	args = append(args, common...)
	args = append(args, tags[0], "/harness/git-next-tool", "migration-server")
	if _, e = docker(time.Minute, true, args...); e != nil {
		return e
	}
	limit := time.Now().Add(75 * time.Second)
	for {
		r, e := docker(time.Minute, false, "exec", server, "test", "-f", "/migration/ready.json")
		if e != nil {
			return e
		}
		if r.code == 0 {
			break
		}
		state, e := dockerJSON(a, "container", "inspect", server)
		if e != nil {
			return e
		}
		if len(state) != 1 || !truth(object(state[0], "State"), "Running") {
			return fmt.Errorf("reference migration fixture stopped before readiness")
		}
		if time.Now().After(limit) {
			return fmt.Errorf("reference fixture readiness deadline exceeded")
		}
		time.Sleep(200 * time.Millisecond)
	}
	created = append(created, struct{ kind, name string }{"container", client})
	args = []string{"run", "--name", client, "--network", "container:" + server}
	args = append(args, common...)
	target := "/inputs/native"
	flag := "0"
	if o.get("native-package") != "" {
		target = "/inputs/native-package.tar.gz"
		flag = "1"
	}
	args = append(args, mount(native, target)...)
	args = append(args, "--env", "GIT_NEXT_NATIVE_PACKAGE="+flag, tags[1], "strace", "--seccomp-bpf", "-f", "-qq", "-e", "trace=execve", "-o", "/tmp/client-exec.trace", "/harness/git-next-tool", "migration-client")
	r, e := docker(7*time.Minute, false, args...)
	if e != nil {
		return e
	}
	if r.code != 0 {
		return fmt.Errorf("native migration client acceptance failed: exit=%d", r.code)
	}
	var acceptance record
	if e = decodeJSON(r.stdout, &acceptance); e != nil {
		return e
	}
	report["acceptance"] = acceptance
	if _, e = docker(time.Minute, true, "wait", server); e != nil {
		return e
	}
	state, e := dockerJSON(a, "container", "inspect", server)
	if e != nil {
		return e
	}
	if len(state) != 1 || number(object(state[0], "State"), "ExitCode") != 0 {
		return fmt.Errorf("migration reference server failed")
	}
	report["status"] = "pass"
	return nil
}
func migrationRemoveResource(a *app, kind, name, owner, scope string) error {
	if dockerAbsent(a, kind, name) {
		return nil
	}
	rows, e := dockerJSON(a, kind, "inspect", name)
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return fmt.Errorf("unexpected resource metadata")
	}
	labels := object(rows[0], "Labels")
	if kind == "container" || kind == "image" {
		labels = object(object(rows[0], "Config"), "Labels")
	}
	if labels["git-next.owner"] != owner || labels["git-next.scope"] != scope {
		return fmt.Errorf("refusing unrelated Docker resource cleanup")
	}
	args := []string{kind, "rm", name}
	if kind == "container" {
		args = []string{"rm", "-f", name}
	}
	_, e = a.mustRun("docker", args...)
	return e
}
