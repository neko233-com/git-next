// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/zip"
	"compress/flate"
	"context"
	"debug/pe"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var allowedImports = map[string]bool{}

func init() {
	for _, name := range strings.Fields("kernel32.dll ntdll.dll advapi32.dll crypt32.dll oleaut32.dll ws2_32.dll userenv.dll user32.dll dwmapi.dll ole32.dll shell32.dll combase.dll gdi32.dll uiautomationcore.dll comctl32.dll dwrite.dll winmm.dll dxgi.dll d3d11.dll imm32.dll dcomp.dll icuuc.dll credui.dll bcryptprimitives.dll vcruntime140.dll vcruntime140_1.dll") {
		allowedImports[name] = true
	}
	register("package-rust", "Package the Rust client/LFS/terminal without interpreter runtimes", "platform git-bin terminal output", packageRust)
	register("fast-verify", "Inspect Rust artifact identity and Windows import closure", "git-bin terminal", fastVerify)
}
func rejectRelative(name string) error {
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(name, "\\", "/")), "/")
	base := parts[len(parts)-1]
	for _, forbidden := range []string{"bash.exe", "sh.exe", "perl.exe", "python.exe", "python3.exe", "git-bash.exe", "ssh.exe"} {
		if base == forbidden {
			return fmt.Errorf("refusing package file %s", name)
		}
	}
	if strings.HasSuffix(base, ".dll") || strings.HasSuffix(base, ".pdb") {
		return fmt.Errorf("refusing package file %s", name)
	}
	for _, part := range parts {
		for _, token := range []string{"mingw", "clangarm64", "perl", "python", "libexec", "libiconv", "libgcc", "libstdc", "libunwind", "git-for-windows"} {
			if strings.Contains(part, token) {
				return fmt.Errorf("refusing package path %s", name)
			}
		}
	}
	return nil
}
func systemImports(path string) (bool, error) {
	file, e := pe.Open(path)
	if e != nil {
		return false, e
	}
	defer file.Close()
	if _, ok := file.OptionalHeader.(*pe.OptionalHeader64); !ok {
		return false, fmt.Errorf("%s is not PE32+", path)
	}
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return false, fmt.Errorf("%s is not a Windows x64 image", path)
	}
	data, e := os.ReadFile(path)
	if e != nil {
		return false, e
	}
	_, imports, e := peDependencies(data)
	if e != nil {
		return false, e
	}
	crt := false
	for _, name := range imports {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "vcruntime") {
			crt = true
		}
		if !allowedImports[lower] && !strings.HasPrefix(lower, "api-ms-win-") {
			return false, fmt.Errorf("%s imports foreign library %s", filepath.Base(path), name)
		}
	}
	return crt, nil
}
func cargoVersion(a *app) (string, error) {
	text, e := read(a.path("Cargo.toml"))
	if e != nil {
		return "", e
	}
	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindStringSubmatch(text)
	if len(m) != 2 {
		return "", fmt.Errorf("Cargo package version missing")
	}
	return m[1], nil
}
func packageRust(a *app, o options) error {
	if e := o.require("platform"); e != nil {
		return e
	}
	if o.get("platform") != "windows-x64" {
		return fmt.Errorf("Rust package supports windows-x64 only, as before")
	}
	version, e := cargoVersion(a)
	if e != nil {
		return e
	}
	gitbin, e := absolute(o.value("git-bin", a.path("target/release")))
	if e != nil {
		return e
	}
	terminal, e := absolute(o.value("terminal", filepath.Join(a.root, "../git-terminal")))
	if e != nil {
		return e
	}
	output, e := absolute(o.value("output", a.path("dist/rust", "git-next-"+version+"-windows-x64.zip")))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(filepath.Dir(output), ".rust-package-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	started := time.Now()
	memberHashes := map[string]string{}
	crt := false
	stageFile := func(src, relative string) error {
		if e := rejectRelative(relative); e != nil {
			return e
		}
		dest, e := confined(stage, relative)
		if e != nil {
			return e
		}
		if e = copyFile(src, dest); e != nil {
			return e
		}
		hash, e := digest(dest)
		if e != nil {
			return e
		}
		memberHashes[relative] = hash
		return nil
	}
	for _, name := range []string{"git.exe", "git-lfs.exe", "git-next-lfs-filter.exe"} {
		path := filepath.Join(gitbin, name)
		needs, e := systemImports(path)
		if e != nil {
			return e
		}
		crt = crt || needs
		if e = stageFile(path, "bin/"+name); e != nil {
			return e
		}
	}
	aliases := map[string]string{}
	alias := filepath.Join(gitbin, "git-next.exe")
	if exists(alias) {
		hash, e := digest(alias)
		if e != nil {
			return e
		}
		needs, e := systemImports(alias)
		if e != nil {
			return e
		}
		crt = crt || needs
		if hash != memberHashes["bin/git.exe"] {
			if e = stageFile(alias, "bin/git-next.exe"); e != nil {
				return e
			}
		} else {
			aliases["git-next.exe"] = "git.exe"
		}
	} else {
		aliases["git-next.exe"] = "git.exe"
	}
	for _, name := range []string{"git-terminal.exe", "git-terminal-shell.exe", "git-terminal-askpass.exe"} {
		path := filepath.Join(terminal, "target/release", name)
		needs, e := systemImports(path)
		if e != nil {
			return e
		}
		crt = crt || needs
		if e = stageFile(path, "bin/"+name); e != nil {
			return e
		}
	}
	entries, e := os.ReadDir(filepath.Join(terminal, "shell-integration"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if e = stageFile(filepath.Join(terminal, "shell-integration", entry.Name()), "bin/shell-integration/"+entry.Name()); e != nil {
				return e
			}
		}
	}
	for _, pair := range [][2]string{{a.path("LICENSE"), "LICENSE"}, {a.path("compatibility.json"), "compatibility.json"}, {filepath.Join(terminal, "LICENSE"), "LICENSE.git-terminal"}, {a.path("packaging/rust/install.ps1"), "install.ps1"}} {
		if e = stageFile(pair[0], pair[1]); e != nil {
			return e
		}
	}
	readme := "# git-next Rust package\n\nRust Git client, LFS entrypoints and Git-Terminal. No MinGW, Bash, Perl, Python or Git for Windows files.\n\nExtract and run install.ps1 to install under %LOCALAPPDATA%\\Programs\\git-next. Registered official Git stays in place. Git-Terminal uses PowerShell/CMD without an owned Bash runtime.\n"
	if e = write(filepath.Join(stage, "README.md"), []byte(readme), 0644); e != nil {
		return e
	}
	memberHashes["README.md"] = digestBytes([]byte(readme))
	crtMode := "static"
	if crt {
		crtMode = "vcruntime140"
	}
	manifest := record{"schema_version": 1, "product": "git-next", "distribution": "rust", "version": version, "platform": o.get("platform"), "official_git": false, "bundled": record{"mingw": false, "bash": false, "perl": false, "python": false}, "microsoft_crt": crtMode, "aliases": aliases, "files": memberHashes}
	if e = writeJSON(filepath.Join(stage, "package.json"), manifest); e != nil {
		return e
	}
	manifestBytes, e := jsonBytes(manifest)
	if e != nil {
		return e
	}
	fingerprint := digestBytes(manifestBytes)
	stampPath := strings.TrimSuffix(output, filepath.Ext(output)) + ".stamp"
	if exists(output) && exists(stampPath) {
		stamp, e := load(stampPath)
		if e == nil && stringValue(stamp, "input_digest") == fingerprint {
			hash, e := digest(output)
			if e == nil && hash == stringValue(stamp, "archive_sha256") {
				if e = verifyRustArchive(output); e != nil {
					return e
				}
				info, e := os.Stat(output)
				if e != nil {
					return e
				}
				return a.json(record{"archive": output, "bytes": info.Size(), "sha256": hash, "cached": true, "seconds": time.Since(started).Seconds()})
			}
		}
	}
	paths, e := files(stage)
	if e != nil {
		return e
	}
	sort.Strings(paths)
	temp, e := os.CreateTemp(filepath.Dir(output), ".rust-archive-*.zip")
	if e != nil {
		return e
	}
	tempName := temp.Name()
	defer temp.Close()
	defer os.Remove(tempName)
	archive := zip.NewWriter(temp)
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestSpeed) })
	for _, p := range paths {
		rel, _ := filepath.Rel(stage, p)
		if e = rejectRelative(rel); e != nil {
			return e
		}
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		writer, e := archive.CreateHeader(header)
		if e != nil {
			return e
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		_, copyErr := io.Copy(writer, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	if e = archive.Close(); e != nil {
		return e
	}
	if e = temp.Close(); e != nil {
		return e
	}
	if e = verifyRustArchive(tempName); e != nil {
		return e
	}
	if e = os.Rename(tempName, output); e != nil {
		return e
	}
	hash, e := digest(output)
	if e != nil {
		return e
	}
	if e = writeJSON(stampPath, record{"input_digest": fingerprint, "archive_sha256": hash}); e != nil {
		return e
	}
	if e = write(filepath.Join(filepath.Dir(output), "SHA256SUMS"), []byte(hash+"  "+filepath.Base(output)+"\n"), 0644); e != nil {
		return e
	}
	info, e := os.Stat(output)
	if e != nil {
		return e
	}
	return a.json(record{"archive": output, "bytes": info.Size(), "sha256": hash, "files": len(paths), "microsoft_crt": crtMode, "official_git": false, "cached": false, "seconds": time.Since(started).Seconds()})
}
func verifyRustArchive(path string) error {
	z, e := zip.OpenReader(path)
	if e != nil {
		return e
	}
	defer z.Close()
	found := false
	actual := map[string]string{}
	var manifest record
	var expanded uint64
	for _, file := range z.File {
		if file.Mode()&os.ModeSymlink != 0 || file.FileInfo().IsDir() || strings.HasPrefix(file.Name, "/") || strings.ContainsAny(file.Name, "\\:") {
			return fmt.Errorf("unsafe package archive member %s", file.Name)
		}
		for _, part := range strings.Split(file.Name, "/") {
			if part == ".." {
				return fmt.Errorf("archive member escapes root")
			}
		}
		if _, duplicate := actual[file.Name]; duplicate {
			return fmt.Errorf("duplicate package member")
		}
		expanded += file.UncompressedSize64
		if expanded > 512<<20 || len(actual) > 20000 {
			return fmt.Errorf("package archive exceeds capacity")
		}
		if e = rejectRelative(file.Name); e != nil {
			return e
		}
		if file.Name == "bin/git.exe" {
			found = true
		}
		r, e := file.Open()
		if e != nil {
			return e
		}
		data, readErr := io.ReadAll(io.LimitReader(r, 512<<20))
		e = readErr
		r.Close()
		if e != nil {
			return e
		}
		actual[file.Name] = digestBytes(data)
		if file.Name == "package.json" {
			if e = decodeJSON(data, &manifest); e != nil {
				return e
			}
		}
	}
	if !found {
		return fmt.Errorf("archive has no bin/git.exe")
	}
	if stringValue(manifest, "product") != "git-next" || stringValue(manifest, "distribution") != "rust" {
		return fmt.Errorf("missing Rust package manifest")
	}
	expected := object(manifest, "files")
	if len(expected)+1 != len(actual) {
		return fmt.Errorf("package manifest/file set differs")
	}
	for name, hash := range expected {
		if actual[name] != hash {
			return fmt.Errorf("package member checksum differs: %s", name)
		}
	}
	return nil
}
func fastVerify(a *app, o options) error {
	for _, p := range []string{a.path("Cargo.toml"), filepath.Join(a.root, "../git-terminal/Cargo.toml")} {
		text, e := read(p)
		if e != nil {
			return e
		}
		if e = validateProfiles(text); e != nil {
			return fmt.Errorf("%s: %w", p, e)
		}
	}
	if rejectRelative("runtime/usr/bin/bash.exe") == nil {
		return fmt.Errorf("package guard accepted Bash")
	}
	started := time.Now()
	bin := o.value("git-bin", a.path("target/release"))
	terminal := o.value("terminal", filepath.Join(a.root, "../git-terminal/target/release/git-terminal.exe"))
	for _, path := range []string{filepath.Join(bin, "git.exe"), filepath.Join(bin, "git-lfs.exe"), terminal} {
		if _, e := systemImports(path); e != nil {
			return e
		}
	}
	smoke := func(program string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		before := time.Now()
		r, e := runProcess(ctx, a.root, nil, nil, program, args...)
		if e != nil {
			return nil, e
		}
		if r.code != 0 || time.Since(before) > 3*time.Second {
			return nil, fmt.Errorf("artifact smoke failed/took over 3 seconds: %s", filepath.Base(program))
		}
		return r.stdout, nil
	}
	out, e := smoke(filepath.Join(bin, "git.exe"), "version", "--json")
	if e != nil {
		return e
	}
	var identity record
	if e = decodeJSON(out, &identity); e != nil {
		return e
	}
	if stringValue(identity, "product") != "git-next" || identity["external_git_fallback"] != false {
		return fmt.Errorf("binary does not identify the standalone Rust client")
	}
	if _, e = smoke(terminal, "--version"); e != nil {
		return e
	}
	return a.json(record{"status": "pass", "compiled": false, "identity": identity, "imports": "system-only", "seconds": time.Since(started).Seconds()})
}
