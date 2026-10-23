// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func init() {
	register("prepare-tls-provider", "Pin independently downloaded TLS DLLs without bundling or loading them", "assets-root private-stage", prepareTLSProvider)
}
func collectWindowsProvider(a *app, platform, assets string) (record, error) {
	receipt, e := load(filepath.Join(assets, "git-next-runtime-assets.json"))
	if e != nil {
		return nil, e
	}
	pin, e := windowsPin(a, platform)
	if e != nil {
		return nil, e
	}
	if receipt["platform"] != platform || receipt["archive_sha256"] != pin["archive_sha256"] {
		return nil, fmt.Errorf("provider assets differ from producer pin")
	}
	prefix := stringValue(receipt, "native_prefix") + "/bin/"
	manifest := object(receipt, "files")
	available := map[string]string{}
	for name := range manifest {
		if strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") && strings.HasSuffix(strings.ToLower(name), ".dll") {
			available[strings.ToLower(path.Base(name))] = name
		}
	}
	pending := []string{"libcurl-openssl-4.dll"}
	selected := record{}
	system := map[string]bool{}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		rel := available[name]
		if rel == "" {
			return nil, fmt.Errorf("missing provider DLL: %s", name)
		}
		if selected[rel] != nil {
			continue
		}
		source, e := confined(assets, rel)
		if e != nil {
			return nil, e
		}
		data, e := os.ReadFile(source)
		if e != nil {
			return nil, e
		}
		hash := digestBytes(data)
		if hash != stringValue(object(manifest, rel), "sha256") {
			return nil, fmt.Errorf("provider asset changed: %s", name)
		}
		arch, deps, e := peDependencies(data)
		if e != nil {
			return nil, e
		}
		expected, e := windowsMachine(platform)
		if e != nil {
			return nil, e
		}
		if arch != expected {
			return nil, fmt.Errorf("provider ISA differs")
		}
		selected[rel] = record{"archive_member": rel, "sha256": hash, "size": len(data)}
		for _, dep := range deps {
			if available[dep] != "" {
				pending = append(pending, dep)
			} else if isSystemDLL(dep) {
				system[dep] = true
			} else {
				return nil, fmt.Errorf("unresolved provider dependency: %s", dep)
			}
		}
	}
	native := stringValue(receipt, "native_prefix")
	for rel, raw := range manifest {
		if rel != native+"/etc/ssl/openssl.cnf" && rel != native+"/etc/ssl/openssl.cnf.dist" && !(strings.HasPrefix(rel, native+"/lib/ossl-modules/") && strings.HasSuffix(rel, ".dll")) {
			continue
		}
		source, e := confined(assets, rel)
		if e != nil {
			return nil, e
		}
		data, e := os.ReadFile(source)
		if e != nil {
			return nil, e
		}
		if digestBytes(data) != stringValue(raw.(map[string]any), "sha256") {
			return nil, fmt.Errorf("provider module/configuration changed")
		}
		selected[rel] = record{"archive_member": rel, "sha256": digestBytes(data), "size": len(data)}
	}
	inventory, e := load(a.path(".evidence/windows-runtime-assets", platform+"-inventory.json"))
	if e != nil {
		return nil, e
	}
	return record{"archive_url": pin["archive_url"], "archive_sha256": pin["archive_sha256"], "archive_size": inventory["archive_bytes"], "provider": "openssl", "curl_version": "8.19.0", "tls_version": "3.5.6", "entrypoint": prefix + "libcurl-openssl-4.dll", "native_prefix": native, "files": selected, "system_dependencies": keys(system)}, nil
}
func tlsPinHeader(platforms record) (string, error) {
	var header strings.Builder
	header.WriteString("/* Generated public provider checksums. SPDX-License-Identifier: GPL-2.0-only */\nstruct git_next_tls_pin { const char *name; const char *sha256; };\n")
	quote := func(v any) (string, error) { b, e := jsonBytes(v); return string(b), e }
	for i, name := range []string{"windows-arm64", "windows-x64"} {
		p := object(platforms, name)
		if p == nil {
			return "", fmt.Errorf("missing TLS platform")
		}
		if i == 0 {
			header.WriteString("#if defined(_M_ARM64) || defined(__aarch64__)\n")
		} else {
			header.WriteString("#else\n")
		}
		for _, pair := range [][2]string{{"PLATFORM", name}, {"ARCHIVE_SHA256", stringValue(p, "archive_sha256")}, {"ENTRYPOINT", stringValue(p, "entrypoint")}, {"NATIVE_PREFIX", stringValue(p, "native_prefix")}} {
			v, e := quote(pair[1])
			if e != nil {
				return "", e
			}
			header.WriteString("#define GIT_NEXT_TLS_" + pair[0] + " " + v + "\n")
		}
		header.WriteString("static const struct git_next_tls_pin git_next_tls_pins[] = {\n")
		for _, filename := range keys(object(p, "files")) {
			file := object(object(p, "files"), filename)
			n, e := quote(filename)
			if e != nil {
				return "", e
			}
			h, e := quote(file["sha256"])
			if e != nil {
				return "", e
			}
			header.WriteString("\t{ " + n + ", " + h + " },\n")
		}
		header.WriteString("};\n")
	}
	header.WriteString("#endif\n")
	return header.String(), nil
}
func prepareTLSProvider(a *app, o options) error {
	if e := o.require("assets-root"); e != nil {
		return e
	}
	if exists(o.get("private-stage")) {
		return fmt.Errorf("private stage must be new")
	}
	platforms := record{}
	for _, name := range []string{"windows-x64", "windows-arm64"} {
		p, e := collectWindowsProvider(a, name, filepath.Join(o.get("assets-root"), name))
		if e != nil {
			return e
		}
		platforms[name] = p
	}
	header, e := tlsPinHeader(platforms)
	if e != nil {
		return e
	}
	if e = writeJSON(a.path("packaging/tls-providers.lock.json"), record{"schema_version": 1, "component": "tls-openssl", "distribution": "independent-producer-download-only", "bundled_in_git_next_release": false, "platforms": platforms}); e != nil {
		return e
	}
	if e = write(a.path("native-core/generated/git-next-tls-pins.h"), []byte(header), 0644); e != nil {
		return e
	}
	counts := record{}
	for _, name := range keys(platforms) {
		p := object(platforms, name)
		counts[name] = len(object(p, "files"))
		if o.get("private-stage") == "" {
			continue
		}
		folder, e := newDestination(filepath.Join(o.get("private-stage"), name))
		if e != nil {
			return e
		}
		for _, filename := range keys(object(p, "files")) {
			item := object(object(p, "files"), filename)
			target, e := confined(folder, filename)
			if e != nil {
				return e
			}
			source, e := confined(filepath.Join(o.get("assets-root"), name), stringValue(item, "archive_member"))
			if e != nil {
				return e
			}
			if e = copyFile(source, target); e != nil {
				return e
			}
		}
		if e = writeNewJSON(filepath.Join(folder, "provider.json"), record{"schema_version": 1, "platform": name, "archive_sha256": p["archive_sha256"], "private_component": true}); e != nil {
			return e
		}
	}
	return a.json(record{"status": "independent-provider-pins-prepared", "dlls": counts, "bundled_in_release": false, "executed": false})
}
