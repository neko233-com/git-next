// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	register("collect-posix-prerequisites", "Snapshot native GnuTLS/libpsl/GSS build inputs without loading libraries", "platform destination base-image", collectPOSIXPrerequisites)
}
func collectPOSIXPrerequisites(a *app, o options) error {
	if e := o.require("platform", "destination"); e != nil {
		return e
	}
	platform := o.get("platform")
	if hostPlatform() != platform || platform != "linux-x64" && platform != "linux-arm64" && platform != "macos-arm64" {
		return fmt.Errorf("collect prerequisites on target OS/architecture")
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	components, provenance := record{}, record{}
	requested := []struct {
		name, library string
		headers       []string
	}{{"gnutls", "gnutls", []string{"gnutls"}}, {"libpsl", "psl", []string{"libpsl.h"}}, {"libidn2", "idn2", []string{"idn2.h"}}}
	if strings.HasPrefix(platform, "linux-") {
		requested = append(requested, struct {
			name, library string
			headers       []string
		}{"mit-krb5-gssapi", "gssapi_krb5", []string{"gssapi", "gssapi.h", "krb5", "krb5.h"}})
	}
	text := func(program string, args ...string) (string, error) {
		b, e := a.mustRun(program, args...)
		return strings.TrimSpace(string(b)), e
	}
	for _, item := range requested {
		libdir, e := text("pkg-config", "--variable=libdir", item.name)
		if e != nil {
			return e
		}
		include, e := text("pkg-config", "--variable=includedir", item.name)
		if e != nil {
			return e
		}
		pcdir, e := text("pkg-config", "--variable=pcfiledir", item.name)
		if e != nil {
			return e
		}
		version, e := text("pkg-config", "--modversion", item.name)
		if e != nil {
			return e
		}
		libraries := map[string]bool{}
		for _, pattern := range []string{"lib" + item.library + ".so*", "lib" + item.library + "*.dylib", "lib" + item.library + ".a"} {
			matches, e := filepath.Glob(filepath.Join(libdir, pattern))
			if e != nil {
				return e
			}
			for _, p := range matches {
				if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() {
					libraries[p] = true
				}
			}
		}
		if len(libraries) == 0 {
			return fmt.Errorf("native prerequisite libraries missing: %s", item.name)
		}
		for p := range libraries {
			name := "lib/" + filepath.Base(p)
			if e = copyFile(p, filepath.Join(out, filepath.FromSlash(name))); e != nil {
				return e
			}
			actual, e := filepath.EvalSymlinks(p)
			if e != nil {
				return e
			}
			h, e := digest(p)
			if e != nil {
				return e
			}
			provenance[name] = record{"source": actual, "sha256": h}
		}
		for _, header := range item.headers {
			p := filepath.Join(include, header)
			st, e := os.Stat(p)
			if e != nil {
				return e
			}
			if !st.IsDir() {
				if e = copyFile(p, filepath.Join(out, "include", header)); e != nil {
					return e
				}
				continue
			}
			e = filepath.WalkDir(p, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					if d.Name() == ".github" {
						return filepath.SkipDir
					}
					return nil
				}
				relative, e := filepath.Rel(include, p)
				if e != nil {
					return e
				}
				return copyFile(p, filepath.Join(out, "include", relative))
			})
			if e != nil {
				return e
			}
		}
		pcPath := filepath.Join(pcdir, item.name+".pc")
		pc, e := read(pcPath)
		if e != nil {
			return e
		}
		var lines []string
		for _, line := range strings.Split(strings.TrimSuffix(strings.ReplaceAll(pc, "\r\n", "\n"), "\n"), "\n") {
			key, _, _ := strings.Cut(line, "=")
			switch key {
			case "prefix":
				line = "prefix=" + filepath.ToSlash(out)
			case "exec_prefix":
				line = "exec_prefix=${prefix}"
			case "libdir":
				line = "libdir=${prefix}/lib"
			case "includedir":
				line = "includedir=${prefix}/include"
			}
			lines = append(lines, line)
		}
		if e = write(filepath.Join(out, "lib/pkgconfig", item.name+".pc"), []byte(strings.Join(lines, "\n")+"\n"), 0644); e != nil {
			return e
		}
		h, e := digest(pcPath)
		if e != nil {
			return e
		}
		components[item.name] = record{"version": version, "pkg_config_sha256": h}
	}
	var producer record
	if strings.HasPrefix(platform, "linux-") {
		packages := []string{"libgnutls28-dev", "libgnutls30", "libpsl-dev", "libpsl5", "libidn2-dev", "libidn2-0", "libkrb5-dev", "libgssapi-krb5-2"}
		b, e := a.mustRun("dpkg-query", append([]string{"-W", "-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n"}, packages...)...)
		if e != nil {
			return e
		}
		for _, name := range packages {
			p := filepath.Join("/usr/share/doc", name, "copyright")
			if exists(p) {
				if e = copyFile(p, filepath.Join(out, "licenses", name+".copyright")); e != nil {
					return e
				}
			}
		}
		producer = record{"kind": "debian-installed-build-prerequisites", "base_image": o.get("base-image"), "packages": strings.Split(strings.TrimSpace(string(b)), "\n"), "corresponding_source_status": "required-before-release", "source_service": "https://deb.debian.org/debian/"}
	} else {
		b, e := a.mustRun("brew", "info", "--json=v2", "gnutls", "libpsl", "libidn2")
		if e != nil {
			return e
		}
		var info any
		if e = json.Unmarshal(b, &info); e != nil {
			return e
		}
		components["gssapi"] = record{"provider": "Apple system GSS.framework"}
		producer = record{"kind": "homebrew-native-build-prerequisites", "formulae": info, "corresponding_source_status": "required-before-release"}
	}
	inventory, e := hashes(out)
	if e != nil {
		return e
	}
	receipt := record{"schema_version": 1, "status": "native-prerequisite-inputs-collected-not-runtime-accepted", "platform": platform, "tls_backend": "GnuTLS", "components": components, "producer": producer, "library_provenance": provenance, "files": inventory, "product_executed": false, "tests_executed": false, "release_eligible": false}
	if e = writeNewJSON(filepath.Join(out, "receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"platform": platform, "files": len(inventory), "tls_backend": "GnuTLS", "product_executed": false})
}
