// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"debug/elf"
	"debug/macho"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var elfSystemLibraries = map[string]bool{"libc.so.6": true, "libm.so.6": true, "libpthread.so.0": true, "libdl.so.2": true, "librt.so.1": true, "libutil.so.1": true, "libresolv.so.2": true, "ld-linux-x86-64.so.2": true, "ld-linux-aarch64.so.1": true}

func elfNeeded(p string) ([]string, error) {
	f, e := elf.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.ImportedLibraries()
}
func machoNeeded(p string) ([]string, error) {
	f, e := macho.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.ImportedLibraries()
}
func machoSystem(name string) bool {
	return strings.HasPrefix(name, "/usr/lib/") || strings.HasPrefix(name, "/System/Library/")
}
func imageNeeded(p string) ([]string, error) {
	target, e := imagePlatform(p)
	if e != nil {
		return nil, e
	}
	if strings.HasPrefix(target, "linux-") {
		return elfNeeded(p)
	}
	if target == "macos-arm64" {
		return machoNeeded(p)
	}
	return nil, nil
}
func relocateELF(a *app, p, libraries string) error {
	f, e := elf.Open(p)
	if e != nil {
		return e
	}
	dynamic := false
	for _, v := range f.Progs {
		dynamic = dynamic || v.Type == elf.PT_DYNAMIC
	}
	f.Close()
	if !dynamic {
		return nil
	}
	relative, e := filepath.Rel(filepath.Dir(p), libraries)
	if e != nil {
		return e
	}
	_, e = a.mustRun("patchelf", "--set-rpath", "$ORIGIN/"+filepath.ToSlash(relative), p)
	return e
}
func relocateMacho(a *app, p, root string, mapping map[string]string) error {
	names, e := machoNeeded(p)
	if e != nil {
		return e
	}
	changed := false
	for _, name := range names {
		if machoSystem(name) {
			continue
		}
		if strings.HasPrefix(name, "@loader_path/") {
			candidate, e := filepath.EvalSymlinks(filepath.Join(filepath.Dir(p), strings.TrimPrefix(name, "@loader_path/")))
			if e == nil && inside(root, candidate) {
				continue
			}
		}
		target, ok := mapping[name]
		if !ok {
			return fmt.Errorf("missing explicit owned Mach-O library mapping: %s", name)
		}
		candidate, e := confined(root, target)
		if e != nil {
			return e
		}
		candidate, e = filepath.EvalSymlinks(candidate)
		if e != nil || !inside(root, candidate) {
			return fmt.Errorf("Mach-O library mapping escapes package")
		}
		relative, e := filepath.Rel(filepath.Dir(p), candidate)
		if e != nil {
			return e
		}
		if _, e = a.mustRun("install_name_tool", "-change", name, "@loader_path/"+filepath.ToSlash(relative), p); e != nil {
			return e
		}
		changed = true
	}
	if changed {
		_, e = a.mustRun("codesign", "--force", "--sign", "-", "--timestamp=none", "--preserve-metadata=entitlements,flags,runtime", p)
	}
	return e
}
func machoRpaths(p string) ([]string, error) {
	f, e := macho.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var paths []string
	for _, load := range f.Loads {
		raw := load.Raw()
		if len(raw) >= 12 && f.ByteOrder.Uint32(raw) == 0x8000001c {
			offset := int(f.ByteOrder.Uint32(raw[8:]))
			if offset < 12 || offset >= len(raw) {
				return nil, fmt.Errorf("invalid LC_RPATH")
			}
			paths = append(paths, strings.TrimRight(string(raw[offset:]), "\x00"))
		}
	}
	return paths, nil
}
func thinMacho(p string) error {
	f, e := macho.OpenFat(p)
	if e != nil {
		return nil
	}
	defer f.Close()
	for _, v := range f.Arches {
		if v.Cpu == macho.CpuArm64 {
			in, e := os.Open(p)
			if e != nil {
				return e
			}
			defer in.Close()
			data := make([]byte, v.Size)
			if _, e = in.ReadAt(data, int64(v.Offset)); e != nil {
				return e
			}
			return os.WriteFile(p, data, 0755)
		}
	}
	return fmt.Errorf("producer has no ARM64 image")
}
