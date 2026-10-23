// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

func init() {
	register("prepare-native-core", "Prepare pinned native source without builds or product execution", "archive destination", prepareNativeCore)
	register("fetch-posix-helpers", "Stage pinned primary LFS/GCM helper assets", "platform cache destination", fetchPOSIXHelpers)
	register("build-posix-updater", "Cross-compile the owned POSIX updater without executing it", "destination go", buildPOSIXUpdater)
}
func productVersion(a *app) (string, error) {
	m, e := load(a.path("packaging/product.json"))
	if e != nil {
		return "", e
	}
	version := stringValue(m, "version")
	if number(m, "schema_version") != 1 || stringValue(m, "product") != "git-next" || stringValue(m, "engine") != "source-built-git-next-native-core" || !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[A-Za-z0-9.-]+)?$`).MatchString(version) {
		return "", fmt.Errorf("invalid native product identity")
	}
	return version, nil
}
func prepareNativeCore(a *app, o options) error {
	if e := o.require("destination"); e != nil {
		return e
	}
	pin, e := load(a.path("packaging/native-core.lock.json"))
	if e != nil {
		return e
	}
	archive := o.get("archive")
	if archive == "" {
		cache := a.path(".evidence/native-core-download")
		archive, e = downloadBounded(stringValue(pin, "archive_url"), stringValue(pin, "archive_sha256"), cache, 64<<20)
		if e != nil {
			return e
		}
	}
	hash, e := digest(archive)
	if e != nil {
		return e
	}
	if hash != stringValue(pin, "archive_sha256") {
		return fmt.Errorf("native source checksum mismatch")
	}
	z, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	if z.Comment != stringValue(pin, "commit") {
		z.Close()
		return fmt.Errorf("source archive commit differs from pin")
	}
	z.Close()
	dest, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	h, skipped, e := extractArchive(archive, dest, extractPolicy{strip: stringValue(pin, "archive_prefix"), links: true, materialize: true, skipWorkflows: true, maxFiles: 20000, maxBytes: 256 << 20})
	if e != nil {
		return e
	}
	materialized := []record{}
	if e = eachArchive(archive, func(m archiveMember, _ io.Reader) error {
		if m.symlink {
			name := strings.TrimPrefix(m.name, stringValue(pin, "archive_prefix"))
			materialized = append(materialized, record{"path": name, "target": path.Clean(path.Join(path.Dir(name), m.link))})
		}
		return nil
	}); e != nil {
		return e
	}
	if skipped == nil {
		skipped = []string{}
	}
	receipt := record{"status": "source-prepared-not-accepted", "upstream": pin["upstream"], "commit": pin["commit"], "archive_sha256": hash, "license": pin["license"], "files": h, "excluded_workflow_members": skipped, "modified_upstream_files": []string{}, "runtime_accepted": false, "materialized_symlinks": materialized}
	if e = writeJSON(filepath.Join(dest, "git-next-source-receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "files": len(h), "excluded_workflow_members": skipped, "runtime_accepted": false})
}
func downloadBounded(raw, expected, cache string, maximum int64) (string, error) {
	if e := os.MkdirAll(cache, 0755); e != nil {
		return "", e
	}
	target := filepath.Join(cache, expected+".archive")
	if exists(target) {
		hash, e := digest(target)
		if e != nil {
			return "", e
		}
		if hash != expected {
			return "", fmt.Errorf("cached asset checksum mismatch")
		}
		return target, nil
	}
	return downloadVariable(raw, expected, target, maximum)
}
func imagePlatform(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	data := make([]byte, 64)
	n, e := io.ReadFull(f, data)
	if e != nil && e != io.ErrUnexpectedEOF && e != io.EOF {
		return "", e
	}
	if n < 4 {
		return "", nil
	}
	if string(data[:4]) == "\x7fELF" && n < 20 {
		return "", fmt.Errorf("truncated ELF header")
	}
	if string(data[:4]) == "\xcf\xfa\xed\xfe" && n < 8 {
		return "", fmt.Errorf("truncated Mach-O header")
	}
	if string(data[:4]) == "\x7fELF" && data[4] == 2 && data[5] == 1 {
		switch binary.LittleEndian.Uint16(data[18:20]) {
		case 62:
			return "linux-x64", nil
		case 183:
			return "linux-arm64", nil
		}
	}
	if string(data[:4]) == "\xcf\xfa\xed\xfe" && binary.LittleEndian.Uint32(data[4:8]) == 0x100000c {
		return "macos-arm64", nil
	}
	return "", nil
}
func fetchPOSIXHelpers(a *app, o options) error {
	if e := o.require("platform", "cache", "destination"); e != nil {
		return e
	}
	lock, e := load(a.path("packaging/posix-helpers.lock.json"))
	if e != nil {
		return e
	}
	pins := object(object(lock, "platforms"), o.get("platform"))
	if pins == nil {
		return fmt.Errorf("unsupported platform")
	}
	dest, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	records := record{}
	components := record{}
	var total int64
	for _, component := range []string{"lfs", "gcm"} {
		pin := object(pins, component)
		u, e := url.Parse(stringValue(pin, "url"))
		if e != nil || u.Scheme != "https" || u.Hostname() != "github.com" || u.User != nil {
			return fmt.Errorf("unexpected primary helper release URL")
		}
		archive, e := downloadPinned(pin, o.get("cache"))
		if e != nil {
			return e
		}
		program := "git-lfs"
		if component == "gcm" {
			program = "git-credential-manager"
		}
		var main string
		e = eachArchive(archive, func(m archiveMember, _ io.Reader) error {
			if !m.directory && filepath.Base(m.name) == program {
				if main != "" {
					return fmt.Errorf("ambiguous primary helper")
				}
				main = m.name
			}
			return nil
		})
		if e != nil {
			return e
		}
		if main == "" {
			return fmt.Errorf("missing primary %s", program)
		}
		parent := filepath.ToSlash(filepath.Dir(main))
		e = eachArchive(archive, func(m archiveMember, r io.Reader) error {
			if m.directory {
				return nil
			}
			if m.symlink || m.hardlink {
				return fmt.Errorf("unexpected helper link")
			}
			if strings.Contains("/"+m.name+"/", "/.github/workflows/") {
				return nil
			}
			if _, e := confined(dest, m.name); e != nil {
				return e
			}
			relative := ""
			mode := m.mode.Perm()
			if component == "lfs" {
				if m.name == main {
					relative = "usr/bin/git-lfs"
					mode = 0755
				} else {
					base := filepath.Base(m.name)
					upper := strings.ToUpper(base)
					if strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE") {
						relative = "share/licenses/git-lfs/" + base
					}
				}
			} else {
				local := strings.TrimPrefix(m.name, parent+"/")
				if parent == "." {
					local = m.name
				}
				if local == m.name && parent != "." {
					return fmt.Errorf("unrelated GCM archive payload")
				}
				relative = "usr/lib/git-next/gcm/" + local
				if m.name == main {
					mode = 0755
				}
			}
			if relative == "" {
				return nil
			}
			if records[relative] != nil {
				return fmt.Errorf("duplicate helper path")
			}
			total += m.size
			if total > 768<<20 || len(records) >= 20000 {
				return fmt.Errorf("helper extraction limit exceeded")
			}
			target, e := confined(dest, relative)
			if e != nil {
				return e
			}
			b, e := io.ReadAll(io.LimitReader(r, m.size+1))
			if e != nil {
				return e
			}
			if int64(len(b)) != m.size {
				return fmt.Errorf("helper size mismatch")
			}
			if mode == 0 {
				mode = 0644
			}
			if e = write(target, b, mode); e != nil {
				return e
			}
			records[relative] = record{"kind": "file", "sha256": digestBytes(b), "bytes": len(b), "mode": uint32(mode), "component": component}
			return nil
		})
		if e != nil {
			return e
		}
		binary := filepath.Join(dest, "usr/bin/git-lfs")
		if component == "gcm" {
			binary = filepath.Join(dest, "usr/lib/git-next/gcm/git-credential-manager")
			records["usr/bin/git-credential-manager"] = record{"kind": "symlink", "target": "../lib/git-next/gcm/git-credential-manager", "component": "gcm"}
			records["usr/bin/git-credential-manager-core"] = record{"kind": "symlink", "target": "git-credential-manager", "component": "gcm"}
		}
		platform, e := imagePlatform(binary)
		if e != nil {
			return e
		}
		if platform != o.get("platform") {
			return fmt.Errorf("helper architecture differs from target")
		}
		components[component] = pin
	}
	hash, e := digest(a.path("packaging/posix-helpers.lock.json"))
	if e != nil {
		return e
	}
	receipt := record{"schema_version": 1, "status": "primary-posix-helpers-staged-not-runtime-accepted", "platform": o.get("platform"), "components": components, "files": records, "physical_symlinks_created": false, "lock_sha256": hash, "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false}
	if e = writeJSON(filepath.Join(dest, "git-next-posix-helpers.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"platform": o.get("platform"), "files": len(records), "bytes": total, "product_executed": false})
}
func buildPOSIXUpdater(a *app, o options) error {
	if e := o.require("destination"); e != nil {
		return e
	}
	dest, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	module := a.path("packaging/posix/updater")
	sources, e := hashes(module)
	if e != nil {
		return e
	}
	ctx, cancel := deadline()
	defer cancel()
	goexe := o.value("go", "go")
	r, e := runProcess(ctx, module, nil, nil, goexe, "env", "GOROOT")
	if e != nil {
		return e
	}
	if r.code != 0 {
		return fmt.Errorf("select Go toolchain: %s", r.stderr)
	}
	goexe = filepath.Join(strings.TrimSpace(string(r.stdout)), "bin/go")
	if strings.HasPrefix(o.value("go", "go"), "C:") || exists(goexe+".exe") {
		goexe += ".exe"
	}
	compiler, e := a.mustRun(goexe, "version")
	if e != nil {
		return e
	}
	binaries := record{}
	for _, target := range []struct{ platform, os, arch string }{{"linux-x64", "linux", "amd64"}, {"linux-arm64", "linux", "arm64"}, {"macos-arm64", "darwin", "arm64"}} {
		output := filepath.Join(dest, target.platform, "updater")
		if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
			return e
		}
		env := append(os.Environ(), "GOOS="+target.os, "GOARCH="+target.arch, "CGO_ENABLED=0", "GOTOOLCHAIN=local")
		r, e := runProcess(ctx, module, env, nil, goexe, "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", output, ".")
		if e != nil {
			return e
		}
		if r.code != 0 {
			return fmt.Errorf("updater build failed: %s", r.stderr)
		}
		platform, e := imagePlatform(output)
		if e != nil {
			return e
		}
		if platform != target.platform {
			return fmt.Errorf("updater architecture differs")
		}
		hash, e := digest(output)
		if e != nil {
			return e
		}
		info, e := os.Stat(output)
		if e != nil {
			return e
		}
		binaries[target.platform] = record{"sha256": hash, "bytes": info.Size()}
	}
	after, e := hashes(module)
	if e != nil {
		return e
	}
	if !equalJSON(sources, after) {
		return fmt.Errorf("updater sources changed during compilation")
	}
	receipt := record{"schema_version": 1, "status": "posix-updaters-compiled-not-runtime-accepted", "compiler": strings.TrimSpace(string(compiler)), "sources": sources, "binaries": binaries, "product_executed": false, "tests_executed": false, "services_installed": false, "installed": false, "release_eligible": false}
	if e = writeJSON(filepath.Join(dest, "receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(receipt)
}
