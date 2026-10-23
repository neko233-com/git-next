// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

var upstreamCores = wordSet("git.exe scalar.exe git-daemon.exe git-shell.exe git-http-backend.exe git-http-fetch.exe git-http-push.exe git-imap-send.exe git-remote-ftp.exe git-remote-ftps.exe git-remote-http.exe git-remote-https.exe git-receive-pack.exe git-upload-archive.exe git-upload-pack.exe")
var virtualLinks = map[string]string{"dev/fd": "/proc/self/fd", "dev/stderr": "/proc/self/fd/2", "dev/stdin": "/proc/self/fd/0", "dev/stdout": "/proc/self/fd/1", "etc/mtab": "/proc/mounts"}
var providerSystem = wordSet("advapi32.dll bcrypt.dll bcryptprimitives.dll crypt32.dll cryptnet.dll dnsapi.dll iphlpapi.dll kernel32.dll msvcrt.dll ncrypt.dll ntdll.dll normaliz.dll secur32.dll shell32.dll shlwapi.dll user32.dll version.dll winmm.dll wldap32.dll ws2_32.dll wsock32.dll cryptbase.dll")

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Fields(s) {
		m[v] = true
	}
	return m
}
func keys[V any](m map[string]V) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}
func isSystemDLL(s string) bool {
	return providerSystem[s] || strings.HasPrefix(s, "api-ms-win-") || strings.HasPrefix(s, "ext-ms-win-")
}
func peDependencies(data []byte) (uint16, []string, error) {
	f, e := pe.NewFile(bytes.NewReader(data))
	if e != nil {
		return 0, nil, e
	}
	defer f.Close()
	header, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return 0, nil, fmt.Errorf("64-bit PE required")
	}
	readRVA := func(address uint32, n uint64) ([]byte, error) {
		for _, section := range f.Sections {
			if uint64(address) >= uint64(section.VirtualAddress) && uint64(address)+n <= uint64(section.VirtualAddress)+uint64(section.Size) {
				offset := uint64(section.Offset) + uint64(address-section.VirtualAddress)
				if offset+n <= uint64(len(data)) {
					return data[offset : offset+n], nil
				}
			}
		}
		return nil, fmt.Errorf("invalid PE import RVA")
	}
	var libs []string
	addName := func(address uint32) error {
		var name []byte
		for i := uint32(0); i < 256; i++ {
			b, e := readRVA(address+i, 1)
			if e != nil {
				return e
			}
			if b[0] == 0 {
				v := strings.ToLower(string(name))
				if strings.ContainsAny(v, "/\\:") || !strings.HasSuffix(v, ".dll") {
					return fmt.Errorf("unsafe PE import: %s", v)
				}
				libs = append(libs, v)
				return nil
			}
			if b[0] > 127 {
				return fmt.Errorf("non-ASCII PE import")
			}
			name = append(name, b[0])
		}
		return fmt.Errorf("unterminated PE import")
	}
	// debug/pe.ImportedLibraries is a stub in the pinned Go runtime.
	for _, table := range []struct{ index, width, nameOffset uint32 }{{1, 20, 12}, {13, 32, 4}} {
		directory := header.DataDirectory[table.index]
		if directory.VirtualAddress == 0 {
			continue
		}
		terminated := false
		for i := uint32(0); i < 1024; i++ {
			if directory.Size != 0 && uint64(i+1)*uint64(table.width) > uint64(directory.Size) {
				return 0, nil, fmt.Errorf("PE import directory lacks terminator")
			}
			address := uint64(directory.VirtualAddress) + uint64(i)*uint64(table.width)
			if address > 1<<32-1 {
				return 0, nil, fmt.Errorf("PE import RVA overflow")
			}
			d, e := readRVA(uint32(address), uint64(table.width))
			if e != nil {
				return 0, nil, e
			}
			if bytes.Equal(d, make([]byte, table.width)) {
				terminated = true
				break
			}
			nameRVA := binary.LittleEndian.Uint32(d[table.nameOffset:])
			if table.index == 13 && binary.LittleEndian.Uint32(d)&1 == 0 {
				if uint64(nameRVA) < header.ImageBase {
					return 0, nil, fmt.Errorf("invalid delayed import address")
				}
				nameRVA = uint32(uint64(nameRVA) - header.ImageBase)
			}
			if e = addName(nameRVA); e != nil {
				return 0, nil, e
			}
		}
		if !terminated {
			return 0, nil, fmt.Errorf("PE import count exceeds limit")
		}
	}
	return f.Machine, libs, nil
}
func peExports(data []byte) (uint16, map[string]bool, error) {
	f, e := pe.NewFile(bytes.NewReader(data))
	if e != nil {
		return 0, nil, e
	}
	defer f.Close()
	h, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return 0, nil, fmt.Errorf("64-bit export table required")
	}
	rva := func(addr uint32, n uint64) ([]byte, error) {
		for _, s := range f.Sections {
			if uint64(addr) >= uint64(s.VirtualAddress) && uint64(addr)+n <= uint64(s.VirtualAddress)+uint64(s.Size) {
				off := uint64(s.Offset) + uint64(addr-s.VirtualAddress)
				if off+n <= uint64(len(data)) {
					return data[off : off+n], nil
				}
			}
		}
		return nil, fmt.Errorf("invalid PE export RVA")
	}
	d, e := rva(h.DataDirectory[0].VirtualAddress, 40)
	if e != nil {
		return 0, nil, e
	}
	count := binary.LittleEndian.Uint32(d[24:])
	if count > 10000 {
		return 0, nil, fmt.Errorf("export count exceeds limit")
	}
	names, e := rva(binary.LittleEndian.Uint32(d[32:]), uint64(count)*4)
	if e != nil {
		return 0, nil, e
	}
	exports := map[string]bool{}
	for i := uint32(0); i < count; i++ {
		address := binary.LittleEndian.Uint32(names[i*4:])
		var name []byte
		terminated := false
		for j := uint32(0); j < 256; j++ {
			b, e := rva(address+j, 1)
			if e != nil {
				return 0, nil, e
			}
			if b[0] == 0 {
				terminated = true
				break
			}
			if b[0] > 127 {
				return 0, nil, fmt.Errorf("non-ASCII export")
			}
			name = append(name, b[0])
		}
		if !terminated {
			return 0, nil, fmt.Errorf("unterminated export")
		}
		exports[string(name)] = true
	}
	return f.Machine, exports, nil
}
func windowsMachine(platform string) (uint16, error) {
	switch platform {
	case "windows-x64":
		return pe.IMAGE_FILE_MACHINE_AMD64, nil
	case "windows-arm64":
		return pe.IMAGE_FILE_MACHINE_ARM64, nil
	}
	return 0, fmt.Errorf("unsupported Windows platform: %s", platform)
}

func init() {
	register("fetch-windows-runtime", "Fetch and inventory pinned independent Windows runtime assets", "platform cache", fetchWindowsRuntime)
	register("prepare-windows-runtime", "Stage pinned Windows helpers, excluding upstream Git engines", "platform cache destination", prepareWindowsRuntime)
	register("prepare-windows-iconv", "Build GNU iconv ABI imports without loading target code", "runtime destination lib", prepareWindowsImport)
	register("prepare-windows-psl", "Build libpsl ABI imports and verified DLL closure", "runtime destination lib", prepareWindowsPSL)
}
func windowsPin(a *app, platform string) (record, error) {
	if _, e := windowsMachine(platform); e != nil {
		return nil, e
	}
	lock, e := load(a.path("packaging/windows-runtime.lock.json"))
	if e != nil {
		return nil, e
	}
	p := object(object(lock, "platforms"), platform)
	if p == nil {
		return nil, fmt.Errorf("missing runtime pin")
	}
	return p, nil
}
func checkedRuntimeArchive(a *app, platform, cache string, fetch bool) (string, record, error) {
	pin, e := windowsPin(a, platform)
	if e != nil {
		return "", nil, e
	}
	name := filepath.Join(cache, platform+"-"+stringValue(pin, "archive_sha256")+".tar.bz2")
	if !exists(name) && fetch {
		if _, e = downloadVariable(stringValue(pin, "archive_url"), stringValue(pin, "archive_sha256"), name, 512<<20); e != nil {
			return "", nil, e
		}
	}
	h, e := digest(name)
	if e != nil {
		return "", nil, e
	}
	if h != stringValue(pin, "archive_sha256") {
		return "", nil, fmt.Errorf("runtime archive checksum mismatch")
	}
	return name, pin, nil
}
func runtimeName(name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	if name == "." {
		return "", nil
	}
	if _, e := confined(".", name); e != nil {
		return "", e
	}
	return path.Clean(name), nil
}
func fetchWindowsRuntime(a *app, o options) error {
	platforms := []string{o.value("platform", "all")}
	if platforms[0] == "all" {
		platforms = []string{"windows-x64", "windows-arm64"}
	}
	cache := o.value("cache", a.path(".evidence/windows-runtime-assets"))
	for _, platform := range platforms {
		archive, pin, e := checkedRuntimeArchive(a, platform, cache, true)
		if e != nil {
			return e
		}
		var members []record
		var excluded []string
		var total int64
		seen := map[string]bool{}
		e = eachArchive(archive, func(m archiveMember, _ io.Reader) error {
			name, e := runtimeName(m.name)
			if e != nil {
				return e
			}
			if name == "" {
				return nil
			}
			if strings.Contains("/"+name+"/", "/.github/workflows/") {
				excluded = append(excluded, name)
				return nil
			}
			if seen[strings.ToLower(name)] {
				return fmt.Errorf("case-colliding runtime member: %s", name)
			}
			seen[strings.ToLower(name)] = true
			total += m.size
			if len(members) >= 100000 || total > 2<<30 || m.size > 128<<20 {
				return fmt.Errorf("runtime archive capacity exceeded")
			}
			kind := "file"
			var link any
			if m.directory {
				kind = "directory"
			} else if m.hardlink {
				kind = "hardlink"
				link = m.link
			} else if m.symlink {
				kind = "symlink"
				link = m.link
			}
			members = append(members, record{"path": name, "bytes": m.size, "mode": uint32(m.mode.Perm()), "type": kind, "link": link})
			return nil
		})
		if e != nil {
			return e
		}
		info, e := os.Stat(archive)
		if e != nil {
			return e
		}
		report := record{"schema_version": 1, "status": "assets-inventoried-not-integrated", "platform": platform, "archive_url": pin["archive_url"], "archive_sha256": pin["archive_sha256"], "archive_bytes": info.Size(), "unpacked_bytes": total, "members": members, "excluded_workflow_members": excluded, "extracted": false, "executed": false, "release_eligible": false, "upstream_git_core_must_be_excluded": true}
		if e = writeJSON(filepath.Join(cache, platform+"-inventory.json"), report); e != nil {
			return e
		}
		delete(report, "members")
		if e = a.json(report); e != nil {
			return e
		}
	}
	return nil
}
func prepareWindowsRuntime(a *app, o options) error {
	if e := o.require("platform", "destination"); e != nil {
		return e
	}
	archive, pin, e := checkedRuntimeArchive(a, o.get("platform"), o.value("cache", a.path(".evidence/windows-runtime-assets")), false)
	if e != nil {
		return e
	}
	dest, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	entries := record{}
	excluded := []record{}
	saved := map[string][]byte{}
	seen := map[string]bool{}
	var pending []archiveMember
	var total int64
	register := func(name string, data []byte, kind string, link any) error {
		target, e := confined(dest, name)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		attributes := 0
		if kind == "msys-virtual-symlink" {
			attributes = 4
			if e = setSystemFileAttribute(target); e != nil {
				return e
			}
		}
		entries[name] = record{"sha256": digestBytes(data), "bytes": len(data), "kind": kind, "link": link, "windows_attributes": attributes}
		return nil
	}
	e = eachArchive(archive, func(m archiveMember, r io.Reader) error {
		name, e := runtimeName(m.name)
		if e != nil {
			return e
		}
		if name == "" {
			return nil
		}
		if strings.Contains("/"+name+"/", "/.github/workflows/") {
			excluded = append(excluded, record{"path": name, "reason": "workflow excluded"})
			return nil
		}
		if m.directory {
			target, e := confined(dest, name)
			if e != nil {
				return e
			}
			return os.MkdirAll(target, 0755)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("case-colliding runtime member: %s", name)
		}
		seen[key] = true
		total += m.size
		if len(seen) > 100000 || total > 2<<30 || m.size > 128<<20 {
			return fmt.Errorf("runtime source capacity exceeded")
		}
		if upstreamCores[strings.ToLower(path.Base(name))] {
			excluded = append(excluded, record{"path": name, "reason": "upstream Git core/entrypoint; replace with git-next build output"})
			if !m.symlink && !m.hardlink {
				data, e := io.ReadAll(io.LimitReader(r, m.size+1))
				if e != nil {
					return e
				}
				saved[name] = data
			}
			return nil
		}
		if m.hardlink {
			link, e := runtimeName(m.link)
			if e != nil {
				return e
			}
			m.name = name
			m.link = link
			pending = append(pending, m)
			return nil
		}
		if m.symlink {
			if virtualLinks[name] != m.link {
				return fmt.Errorf("unexpected MSYS virtual symlink: %s", name)
			}
			data := []byte("!<symlink>\xff\xfe")
			for _, v := range utf16.Encode([]rune(m.link + "\x00")) {
				data = binary.LittleEndian.AppendUint16(data, v)
			}
			return register(name, data, "msys-virtual-symlink", m.link)
		}
		data, e := io.ReadAll(io.LimitReader(r, m.size+1))
		if e != nil {
			return e
		}
		if int64(len(data)) != m.size {
			return fmt.Errorf("runtime member size mismatch")
		}
		return register(name, data, "file", nil)
	})
	if e != nil {
		return e
	}
	for len(pending) > 0 {
		next := []archiveMember{}
		for _, m := range pending {
			var data []byte
			if entries[m.link] != nil {
				target, e := confined(dest, m.link)
				if e != nil {
					return e
				}
				data, e = os.ReadFile(target)
				if e != nil {
					return e
				}
			} else if cached, ok := saved[m.link]; ok {
				if !wordSet("git-gui.exe gitk.exe git-wrapper.exe")[strings.ToLower(path.Base(m.name))] {
					return fmt.Errorf("unapproved helper references excluded Git core")
				}
				data = cached
			} else {
				next = append(next, m)
				continue
			}
			if e = register(m.name, data, "materialized-hardlink", m.link); e != nil {
				return e
			}
		}
		if len(next) == len(pending) {
			return fmt.Errorf("unresolved runtime hardlinks")
		}
		pending = next
	}
	prefix := "mingw64"
	if o.get("platform") == "windows-arm64" {
		prefix = "clangarm64"
	}
	for _, name := range []string{"usr/bin/sh.exe", "usr/bin/ssh.exe", "usr/bin/perl.exe", "usr/bin/gpg.exe", prefix + "/bin/git-lfs.exe", prefix + "/bin/git-credential-manager.exe", prefix + "/bin/libiconv-2.dll", "etc/package-versions.txt"} {
		if entries[name] == nil {
			return fmt.Errorf("required runtime component missing: %s", name)
		}
	}
	report := record{"schema_version": 1, "status": "assets-staged-not-core-integrated", "platform": o.get("platform"), "archive_url": pin["archive_url"], "archive_sha256": pin["archive_sha256"], "native_prefix": prefix, "files": entries, "excluded": excluded, "tests_executed": false, "product_executed": false, "installed": false, "release_eligible": false, "remaining": "Attach own core/helper paths, iconv imports and source/license package; then final consolidated acceptance"}
	if e = writeNewJSON(filepath.Join(dest, "git-next-runtime-assets.json"), report); e != nil {
		return e
	}
	return a.json(record{"platform": o.get("platform"), "status": report["status"], "files": len(entries), "excluded_core_entries": len(excluded), "upstream_core_deployed": false, "product_executed": false})
}
func prepareWindowsImport(a *app, o options) error { return windowsABI(a, o, false) }
func prepareWindowsPSL(a *app, o options) error    { return windowsABI(a, o, true) }
func windowsABI(a *app, o options, psl bool) error {
	if e := o.require("runtime", "destination", "lib"); e != nil {
		return e
	}
	runtimeRoot, e := absolute(o.get("runtime"))
	if e != nil {
		return e
	}
	receipt, e := load(filepath.Join(runtimeRoot, "git-next-runtime-assets.json"))
	if e != nil {
		return e
	}
	platform := stringValue(receipt, "platform")
	expected, e := windowsMachine(platform)
	if e != nil {
		return e
	}
	prefix := stringValue(receipt, "native_prefix") + "/bin/"
	manifest := object(receipt, "files")
	available := map[string]string{}
	for name := range manifest {
		if strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") && strings.HasSuffix(strings.ToLower(name), ".dll") {
			available[strings.ToLower(path.Base(name))] = name
		}
	}
	dll := "libiconv-2.dll"
	stem := "iconv"
	api := wordSet("libiconv libiconv_open libiconv_close")
	header := a.path("native-core/overlay/git-next-iconv.h")
	headerDest := "iconv.h"
	if psl {
		dll = "libpsl-5.dll"
		stem = "psl"
		api = wordSet("psl_builtin psl_latest psl_free psl_is_public_suffix psl_is_cookie_domain_acceptable psl_check_version_number psl_get_version")
		header = a.path("native-core/vendor/libpsl-abi/libpsl.h.in")
		headerDest = "libpsl.h"
	}
	selected := record{}
	pending := []string{dll}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if selected[name] != nil {
			continue
		}
		rel := available[name]
		if rel == "" {
			return fmt.Errorf("missing pinned ABI dependency: %s", name)
		}
		source, e := confined(runtimeRoot, rel)
		if e != nil {
			return e
		}
		data, e := os.ReadFile(source)
		if e != nil {
			return e
		}
		hash := digestBytes(data)
		if hash != stringValue(object(manifest, rel), "sha256") {
			return fmt.Errorf("ABI DLL differs from receipt")
		}
		arch, deps, e := peDependencies(data)
		if e != nil {
			return e
		}
		if arch != expected {
			return fmt.Errorf("ABI architecture differs")
		}
		if name == dll {
			_, exports, e := peExports(data)
			if e != nil {
				return e
			}
			for symbol := range api {
				if !exports[symbol] {
					return fmt.Errorf("missing ABI export: %s", symbol)
				}
			}
		}
		selected[name] = record{"sha256": hash, "archive_member": rel}
		if psl {
			for _, dep := range deps {
				if available[dep] != "" {
					pending = append(pending, dep)
				} else if !isSystemDLL(dep) {
					return fmt.Errorf("unresolved ABI DLL: %s", dep)
				}
			}
		}
	}
	hash, e := digest(header)
	if e != nil {
		return e
	}
	data, e := os.ReadFile(header)
	if e != nil {
		return e
	}
	if psl {
		if hash != "6591fd46b9846403f59d436b11cbb1017f3031801181a7b258a1d3729ad57be9" {
			return fmt.Errorf("pinned libpsl header changed")
		}
		s := string(data)
		for k, v := range map[string]string{"LIBPSL_VERSION": "0.21.5", "LIBPSL_VERSION_MAJOR": "0", "LIBPSL_VERSION_MINOR": "21", "LIBPSL_VERSION_PATCH": "5", "LIBPSL_VERSION_NUMBER": "0x001505"} {
			s = strings.ReplaceAll(s, "@"+k+"@", v)
		}
		if strings.Contains(s, "@LIBPSL_") {
			return fmt.Errorf("unresolved libpsl variables")
		}
		data = []byte(s)
	}
	dest, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	if e = write(filepath.Join(dest, "include", headerDest), data, 0644); e != nil {
		return e
	}
	definition := filepath.Join(dest, "lib", stem+".def")
	if e = write(definition, []byte("LIBRARY "+dll+"\nEXPORTS\n"+strings.Join(keys(api), "\n")+"\n"), 0644); e != nil {
		return e
	}
	library := filepath.Join(dest, "lib", stem+".lib")
	arch := "x64"
	if platform == "windows-arm64" {
		arch = "arm64"
	}
	if _, e = a.mustRun(o.get("lib"), "/nologo", "/def:"+definition, "/machine:"+arch, "/out:"+library); e != nil {
		return e
	}
	for name, raw := range selected {
		item := raw.(map[string]any)
		if e = copyFile(filepath.Join(runtimeRoot, stringValue(item, "archive_member")), filepath.Join(dest, "bin", name)); e != nil {
			return e
		}
	}
	libHash, e := digest(library)
	if e != nil {
		return e
	}
	report := record{"schema_version": 1, "status": "iconv-imports-prepared-not-runtime-accepted", "platform": platform, "upstream_runtime_archive_sha256": receipt["archive_sha256"], "header_sha256": hash, "import_library_sha256": libHash, "exported_api": keys(api), "product_executed": false, "tests_executed": false, "installed": false, "release_eligible": false, "errno_bridge": "native-core/overlay/git-next-iconv.c", "dll_sha256": object(selected, dll)["sha256"]}
	if psl {
		report["status"] = "libpsl-imports-prepared-not-runtime-accepted"
		report["version"] = "0.21.5"
		report["header_source"] = "https://raw.githubusercontent.com/rockdaboot/libpsl/0.21.5/include/libpsl.h.in"
		report["files"] = selected
		delete(report, "dll_sha256")
		delete(report, "errno_bridge")
		delete(report, "installed")
		delete(report, "release_eligible")
	}
	if e = writeNewJSON(filepath.Join(dest, "receipt.json"), report); e != nil {
		return e
	}
	return a.json(report)
}
