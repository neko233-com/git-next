// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// releasePlatformSpec describes how a supported release platform is built and
// archived. Cross-compilation always uses GOOS/GOARCH with CGO_ENABLED=0.
type releasePlatformSpec struct {
	goos, goarch, ext string
}

var releasePlatforms = map[string]releasePlatformSpec{
	"windows-x64": {goos: "windows", goarch: "amd64", ext: ".zip"},
	"linux-x64":   {goos: "linux", goarch: "amd64", ext: ".tar.gz"},
	"linux-arm64": {goos: "linux", goarch: "arm64", ext: ".tar.gz"},
	"macos-arm64": {goos: "darwin", goarch: "arm64", ext: ".tar.gz"},
}

func releasePlatformSpecFor(platform string) (releasePlatformSpec, bool) {
	spec, ok := releasePlatforms[platform]
	return spec, ok
}

func isReleasePlatform(platform string) bool {
	_, ok := releasePlatforms[platform]
	return ok
}

// A locally built binary may only be reused for the host platform; otherwise
// a same-named binary from a different GOARCH would be packaged silently.
func isCurrentReleasePlatform(spec releasePlatformSpec) bool {
	return spec.goos == runtime.GOOS && spec.goarch == runtime.GOARCH
}

func supportedReleasePlatforms() string {
	names := make([]string, 0, len(releasePlatforms))
	for name := range releasePlatforms {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (s releasePlatformSpec) gitName() string {
	if s.goos == "windows" {
		return "git.exe"
	}
	return "git"
}

func (s releasePlatformSpec) terminalName() string {
	if s.goos == "windows" {
		return "git-terminal.exe"
	}
	return "git-terminal"
}

func (s releasePlatformSpec) vtName() string {
	if s.goos == "windows" {
		return "ghostty-vt.dll"
	}
	return "libghostty-vt.so"
}

// releaseBuildEnv replaces any inherited Go target settings so a cross build
// cannot silently pick up a foreign GOOS/GOARCH or a cgo-enabled default.
func releaseBuildEnv(spec releasePlatformSpec) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOTOOLCHAIN":
			continue
		}
		env = append(env, item)
	}
	return append(env, "GOOS="+spec.goos, "GOARCH="+spec.goarch, "CGO_ENABLED=0", "GOTOOLCHAIN=go1.27.3")
}

// sha256sumLine follows the GNU coreutils format: digest, two spaces, name.
func sha256sumLine(hash, name string) string {
	return hash + "  " + name + "\n"
}

func newReleaseManifest(version, platform, archiveName, gitHash, terminalHash string, files map[string]string) record {
	return record{
		"schema_version":  1,
		"product":         "git-next",
		"version":         version,
		"platform":        platform,
		"cgo_enabled":     false,
		"git_sha256":      gitHash,
		"terminal_sha256": terminalHash,
		"archive":         archiveName,
		"files":           files,
	}
}

func init() {
	register("package-release", "Build and package a CGO-free distributable release archive", "platform output terminal git-bin", packageRelease)
}

func packageRelease(a *app, o options) error {
	platform := ""
	if len(o.args) > 0 {
		platform = o.args[0]
	}
	if v := o.value("platform", ""); v != "" {
		platform = v
	}
	if platform == "" {
		return fmt.Errorf("usage: package-release <platform> [output-dir]; supported: %s", supportedReleasePlatforms())
	}
	spec, ok := releasePlatformSpecFor(platform)
	if !ok {
		return fmt.Errorf("unsupported release platform %q; supported: %s", platform, supportedReleasePlatforms())
	}
	version, e := productVersion(a)
	if e != nil {
		return e
	}
	outputArg := ""
	if len(o.args) > 1 {
		outputArg = o.args[1]
	}
	if v := o.value("output", ""); v != "" {
		outputArg = v
	}
	if outputArg == "" {
		outputArg = a.path("target", "release-assets")
	}
	outputDir, e := absolute(outputArg)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(outputDir, 0755); e != nil {
		return e
	}
	archiveName := "git-next-" + version + "-" + platform + spec.ext
	output := filepath.Join(outputDir, archiveName)
	started := time.Now()

	// Reuse the locally built client for the current platform; cross-compile
	// otherwise. Both paths pin CGO_ENABLED=0.
	gitBinDir, e := absolute(o.value("git-bin", a.path("target", "release")))
	if e != nil {
		return e
	}
	gitPath := filepath.Join(gitBinDir, spec.gitName())
	if o.get("git-bin") == "" && (!isCurrentReleasePlatform(spec) || !exists(gitPath)) {
		buildDir := a.path("target", "release-build", platform)
		if e = os.MkdirAll(buildDir, 0755); e != nil {
			return e
		}
		gitPath = filepath.Join(buildDir, spec.gitName())
		if e = buildReleaseBinary(a.root, spec, gitPath, "./cmd/git-next"); e != nil {
			return e
		}
	}
	gitHash, e := digest(gitPath)
	if e != nil {
		return e
	}

	// Locate the Git-Terminal checkout. An explicit --terminal always wins.
	terminalDir := o.value("terminal", defaultTerminalDir(a.root))
	terminalBin := filepath.Join(terminalDir, "target", "go", spec.terminalName())
	if o.get("terminal") == "" && (!isCurrentReleasePlatform(spec) || !exists(terminalBin)) {
		module := filepath.Join(terminalDir, "go")
		if !exists(filepath.Join(module, "go.mod")) || !exists(filepath.Join(module, "cmd", "git-terminal")) {
			return fmt.Errorf("terminal binary is missing: %s (set --terminal)", terminalBin)
		}
		buildDir := a.path("target", "release-build", platform)
		if e = os.MkdirAll(buildDir, 0755); e != nil {
			return e
		}
		terminalBin = filepath.Join(buildDir, spec.terminalName())
		if e = buildReleaseBinary(module, spec, terminalBin, "./cmd/git-terminal"); e != nil {
			return e
		}
	}
	vtCandidates := []string{
		filepath.Join(terminalDir, "target", "go", spec.vtName()),
		filepath.Join(a.path("target", "release-build", platform), spec.vtName()),
		filepath.Join(terminalDir, "go", "resources", spec.goos+"-"+spec.goarch, spec.vtName()),
	}
	vtLib := ""
	for _, candidate := range vtCandidates {
		if exists(candidate) {
			vtLib = candidate
			break
		}
	}
	if vtLib == "" {
		return fmt.Errorf("terminal VT companion %s is missing (set --terminal)", spec.vtName())
	}
	terminalHash, e := digest(terminalBin)
	if e != nil {
		return e
	}

	// Stage a bounded, flat archive tree under the output directory.
	stage, e := os.MkdirTemp(outputDir, ".release-stage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	files := map[string]string{}
	stageFile := func(src, relative string, owned bool) error {
		if !owned {
			if e := rejectRelative(relative); e != nil {
				return e
			}
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
		files[relative] = hash
		return nil
	}
	if e = stageFile(gitPath, "bin/"+spec.gitName(), false); e != nil {
		return e
	}
	if e = stageFile(terminalBin, "bin/"+spec.terminalName(), false); e != nil {
		return e
	}
	// The VT parser is an owned, pinned companion DLL/.so, not a foreign runtime.
	if e = stageFile(vtLib, "bin/"+spec.vtName(), true); e != nil {
		return e
	}
	if e = stageFile(a.path("LICENSE"), "LICENSE", false); e != nil {
		return e
	}
	if e = stageFile(filepath.Join(terminalDir, "LICENSE"), "LICENSE.git-terminal", false); e != nil {
		return e
	}
	notice := filepath.Join(terminalDir, "target", "go", "NOTICE.ghostty-vt.md")
	if !exists(notice) {
		notice = filepath.Join(terminalDir, "go", "resources", "NOTICE.md")
	}
	if exists(notice) {
		if e = stageFile(notice, "NOTICE", false); e != nil {
			return e
		}
	}
	manifest := newReleaseManifest(version, platform, archiveName, gitHash, terminalHash, files)
	if e = writeJSON(filepath.Join(stage, "package.json"), manifest); e != nil {
		return e
	}

	temp, e := os.CreateTemp(outputDir, ".release-archive-*"+spec.ext)
	if e != nil {
		return e
	}
	tempName := temp.Name()
	if e = temp.Close(); e != nil {
		return e
	}
	defer os.Remove(tempName)
	if e = writeReleaseArchive(stage, tempName, spec); e != nil {
		return e
	}
	archiveHash, e := digest(tempName)
	if e != nil {
		return e
	}
	if e = os.Rename(tempName, output); e != nil {
		return e
	}

	// SHA256SUMS intentionally contains only the archive line so a download
	// center can reference a single deterministic asset.
	if e = write(filepath.Join(outputDir, "SHA256SUMS"), []byte(sha256sumLine(archiveHash, archiveName)), 0644); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(outputDir, "package.json"), manifest); e != nil {
		return e
	}
	info, e := os.Stat(output)
	if e != nil {
		return e
	}
	return a.json(record{
		"archive": output, "bytes": info.Size(), "sha256": archiveHash,
		"platform": platform, "version": version, "cgo_enabled": false,
		"git_sha256": gitHash, "terminal_sha256": terminalHash,
		"files": len(files) + 1, "seconds": time.Since(started).Seconds(),
	})
}

func buildReleaseBinary(cwd string, spec releasePlatformSpec, output, pkg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r, e := runProcess(ctx, cwd, releaseBuildEnv(spec), nil, "go", "build", "-mod=mod", "-trimpath", "-ldflags", "-s -w", "-o", output, pkg)
	if e != nil {
		return e
	}
	if r.code != 0 {
		return fmt.Errorf("go build %s/%s exited %d: %s", spec.goos, spec.goarch, r.code, strings.TrimSpace(string(r.stderr)))
	}
	return nil
}

// Fixed metadata keeps repeated builds byte-identical so a download center can
// pin one asset name and one checksum for a given set of inputs.
var releaseArchiveEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

func writeReleaseArchive(stage, output string, spec releasePlatformSpec) error {
	paths, e := files(stage)
	if e != nil {
		return e
	}
	sort.Strings(paths)
	if spec.ext == ".zip" {
		return writeReleaseZip(stage, paths, output)
	}
	return writeReleaseTarGz(stage, paths, output)
}

// releaseMemberName keeps archive entries confined and rejects foreign runtime
// paths, except for the owned VT companion which package-go also pins.
func releaseMemberName(stage, p string) (string, error) {
	rel, e := filepath.Rel(stage, p)
	if e != nil {
		return "", e
	}
	rel = filepath.ToSlash(rel)
	if filepath.Base(rel) == "ghostty-vt.dll" {
		return rel, nil
	}
	if e = rejectRelative(rel); e != nil {
		return "", e
	}
	return rel, nil
}

func writeReleaseZip(stage string, paths []string, output string) error {
	out, e := os.Create(output)
	if e != nil {
		return e
	}
	defer out.Close()
	archive := zip.NewWriter(out)
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	for _, p := range paths {
		rel, e := releaseMemberName(stage, p)
		if e != nil {
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
		header.Name = rel
		header.Method = zip.Deflate
		header.Modified = releaseArchiveEpoch
		header.Extra = nil
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
	return archive.Close()
}

func writeReleaseTarGz(stage string, paths []string, output string) error {
	out, e := os.Create(output)
	if e != nil {
		return e
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		rel, e := releaseMemberName(stage, p)
		if e != nil {
			return e
		}
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		header, e := tar.FileInfoHeader(info, "")
		if e != nil {
			return e
		}
		header.Name = rel
		header.ModTime = releaseArchiveEpoch
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		header.Mode = 0644
		if e = tw.WriteHeader(header); e != nil {
			return e
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		_, copyErr := io.Copy(tw, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	if e = tw.Close(); e != nil {
		return e
	}
	return gz.Close()
}
