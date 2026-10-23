// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func unpackAcceptancePackage(archive, destination string) (string, error) {
	if e := os.Mkdir(destination, 0755); e != nil {
		return "", e
	}
	if _, _, e := extractArchive(archive, destination, extractPolicy{links: true, maxFiles: 75000, maxBytes: 4 << 30}); e != nil {
		return "", e
	}
	if exists(filepath.Join(destination, "package.json")) {
		return destination, nil
	}
	candidates, e := filepath.Glob(filepath.Join(destination, "*/package.json"))
	if e != nil {
		return "", e
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("missing/ambiguous package root")
	}
	return filepath.Dir(candidates[0]), nil
}
func verifyAcceptancePackage(root string, manifest record) error {
	for name, value := range object(manifest, "files") {
		item, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid file manifest %s", name)
		}
		target, e := confined(root, name)
		if e != nil {
			return e
		}
		resolved, e := filepath.EvalSymlinks(target)
		if e != nil || !inside(root, resolved) {
			return fmt.Errorf("manifest file escapes root %s", name)
		}
		info, e := os.Lstat(target)
		if e != nil {
			return e
		}
		if stringValue(item, "kind") == "symlink" {
			link, e := os.Readlink(target)
			if e != nil || link != stringValue(item, "target") {
				return fmt.Errorf("package link differs %s", name)
			}
		} else {
			if !info.Mode().IsRegular() || info.Size() != number(item, "bytes") {
				return fmt.Errorf("package bytes/type differ %s", name)
			}
			hash, e := digest(target)
			if e != nil {
				return e
			}
			if hash != stringValue(item, "sha256") {
				return fmt.Errorf("package hash differs %s", name)
			}
			if int64(info.Mode().Perm()) != number(item, "mode") {
				return fmt.Errorf("package mode differs %s", name)
			}
		}
	}
	return nil
}
func managedAssembly(path string) bool {
	image, e := os.Open(path)
	if e != nil {
		return false
	}
	defer image.Close()
	header := make([]byte, 64)
	if _, e = io.ReadFull(image, header); e != nil || string(header[:2]) != "MZ" {
		return false
	}
	offset := int64(binary.LittleEndian.Uint32(header[60:]))
	if offset < 64 || offset > 1<<20 {
		return false
	}
	if _, e = image.Seek(offset, io.SeekStart); e != nil {
		return false
	}
	header = make([]byte, 264)
	n, _ := io.ReadFull(image, header)
	header = header[:n]
	if len(header) < 248 || string(header[:4]) != "PE\x00\x00" {
		return false
	}
	directory := 0
	switch binary.LittleEndian.Uint16(header[24:]) {
	case 0x20b:
		directory = 24 + 112
	case 0x10b:
		directory = 24 + 96
	default:
		return false
	}
	if len(header) < directory+15*8 || binary.LittleEndian.Uint32(header[directory-4:]) < 15 {
		return false
	}
	return binary.LittleEndian.Uint32(header[directory+14*8:]) != 0 && binary.LittleEndian.Uint32(header[directory+14*8+4:]) >= 72
}
func macPackageStatic(a *app, o options) error {
	if e := o.require("output"); e != nil {
		return e
	}
	if (o.get("package") == "") == (o.get("archive") == "") {
		return fmt.Errorf("exactly one --package or --archive is required")
	}
	root := o.get("package")
	if root != "" {
		var e error
		root, e = filepath.Abs(root)
		if e != nil {
			return e
		}
	} else {
		temp, e := os.MkdirTemp("", "git-next-mac-static-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(temp)
		root, e = unpackAcceptancePackage(o.get("archive"), filepath.Join(temp, "package"))
		if e != nil {
			return e
		}
	}
	manifest, e := load(filepath.Join(root, "package.json"))
	if e != nil {
		return e
	}
	if number(manifest, "schema_version") != 1 || manifest["product"] != "git-next" || manifest["platform"] != "macos-arm64" {
		return fmt.Errorf("unexpected package identity")
	}
	images, scripts, failures := []string{}, []string{}, []string{}
	hybrid := regexp.MustCompile(`exec\s+['"]?(wish|tclsh)(?:[0-9]+(?:\.[0-9]+)*)?['"]?\s`)
	for name, raw := range object(manifest, "files") {
		item := raw.(map[string]any)
		r := acceptCapture(name, func() {
			acceptMust(verifyAcceptancePackage(root, record{"files": record{name: item}}))
			if item["kind"] == "symlink" {
				return
			}
			p := filepath.Join(root, filepath.FromSlash(name))
			data := acceptBytes(p)
			header := data
			if len(header) > 64 {
				header = header[:64]
			}
			if len(header) >= 4 && bytes.Equal(header[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) {
				acceptCheck(len(header) >= 32 && binary.LittleEndian.Uint32(header[4:]) == 0x100000c, "native image not ARM64 Mach-O")
				images = append(images, name)
			} else if bytes.HasPrefix(header, []byte("MZ")) {
				acceptCheck(strings.HasPrefix(name, "runtime/usr/lib/git-next/gcm/") && strings.HasSuffix(name, ".dll") && managedAssembly(p), "unexpected native Windows image")
			} else if bytes.HasPrefix(header, []byte{0x7f, 'E', 'L', 'F'}) || (len(header) >= 4 && (bytes.Equal(header[:4], []byte{0xca, 0xfe, 0xba, 0xbe}) || bytes.Equal(header[:4], []byte{0xbe, 0xba, 0xfe, 0xca}) || bytes.Equal(header[:4], []byte{0xca, 0xfe, 0xba, 0xbf}))) {
				panic(fmt.Errorf("unexpected/uninspected native image"))
			} else {
				shell := false
				for _, prefix := range []string{"#!/bin/sh", "#!/bin/bash", "#!/usr/bin/env bash", "#!/usr/bin/env sh"} {
					if bytes.HasPrefix(data, []byte(prefix)) {
						shell = true
					}
				}
				if shell {
					prefix := data[:min(len(data), 8192)]
					if hybrid.Match(prefix) {
						lines := bytes.SplitAfter(data, []byte("\n"))
						var preamble bytes.Buffer
						for _, line := range lines {
							preamble.Write(line)
							if hybrid.Match(line) {
								break
							}
						}
						data = preamble.Bytes()
					}
					acceptRun(root, nil, data, true, "/bin/bash", "-n")
					scripts = append(scripts, name)
				}
			}
		})
		if r["status"] != "pass" {
			failures = append(failures, name)
		}
	}
	if len(images) == 0 || len(scripts) == 0 {
		failures = append(failures, "missing-native-or-shell-inputs")
	}
	status := "passed"
	if len(failures) > 0 {
		status = "failed"
	}
	return acceptReport(a, o, record{"schema_version": 1, "status": status, "platform": "macos-arm64", "acceptance_scope": "linux-docker-package-hashes-arm64-mach-o-and-shell-syntax", "package_manifest_sha256": acceptHash(filepath.Join(root, "package.json")), "files_inspected": len(object(manifest, "files")), "native_images": images, "shell_entrypoints": scripts, "failures": failures, "macos_product_executed": false, "macos_kernel_keychain_runtime_verified": false, "host_profiles_mounted": false})
}
func nativeDockerRun(a *app, o options) error {
	archive := o.value("archive", "/inputs/package.tar.gz")
	destination := o.value("destination", "/tmp/package")
	root, e := unpackAcceptancePackage(archive, destination)
	if e != nil {
		return e
	}
	manifest, e := load(filepath.Join(root, "package.json"))
	if e != nil {
		return e
	}
	if e = verifyAcceptancePackage(root, manifest); e != nil {
		return e
	}
	resultDir := o.value("results", "/results")
	if e = os.MkdirAll(resultDir, 0755); e != nil {
		return e
	}
	cases := []struct{ name, command string }{{"native-contracts", "native-contract-acceptance"}, {"native-common-cli", "native-common-cli-acceptance"}, {"native-integrations", "native-integrations-acceptance"}, {"native-read-threads", "native-read-threads-acceptance"}}
	checks := make([]record, len(cases))
	for i, c := range cases {
		func() {
			args := []string{c.command, filepath.Join(root, "bin/git"), "--output", filepath.Join(resultDir, c.name+".json")}
			if c.name == "native-contracts" || c.name == "native-common-cli" {
				args = append(args, "--reference", "/usr/bin/git")
			}
			if c.name == "native-common-cli" {
				args = append(args, "--jobs", "1")
			}
			argv := toolCommand(args...)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			value, e := runProcess(ctx, a.root, nil, nil, argv[0], argv[1:]...)
			if e != nil {
				value.stderr = append(value.stderr, []byte(e.Error())...)
				value.code = 1
			}
			_ = write(filepath.Join(resultDir, c.name+".log"), append(append(value.stdout, byte('\n')), value.stderr...), 0644)
			status := "pass"
			if value.code != 0 {
				status = "fail"
			}
			checks[i] = record{"case": c.name, "status": status, "exit_code": value.code}
		}()
	}
	report := record{"schema_version": 1, "status": acceptStatus(checks), "platform": manifest["platform"], "package_manifest_sha256": acceptHash(filepath.Join(root, "package.json")), "files_verified": len(object(manifest, "files")), "cases": checks, "personal_profile_mounted": false}
	if e = writeNewJSON(filepath.Join(resultDir, "linux-native.json"), report); e != nil {
		return e
	}
	return acceptReport(a, options{}, report)
}
func init() {
	register("mac-package-static", "Linux static inspection of macOS package hashes, ARM64 headers and shell syntax", "package archive output", macPackageStatic)
	register("native-docker-run", "Verify and exercise complete Linux package with isolated profiles", "archive destination results", nativeDockerRun)
	register("package-input", "Bounded package archive extraction without executing target code", "archive destination", func(a *app, o options) error {
		if e := o.require("archive", "destination"); e != nil {
			return e
		}
		root, e := unpackAcceptancePackage(o.get("archive"), o.get("destination"))
		if e != nil {
			return e
		}
		return a.json(record{"package": root})
	})
}
