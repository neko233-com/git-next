// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseManifestRecordsCGOEnabledFalse(t *testing.T) {
	gitHash := strings.Repeat("a", 64)
	terminalHash := strings.Repeat("b", 64)
	files := map[string]string{"bin/git.exe": strings.Repeat("c", 64)}
	manifest := newReleaseManifest("1.0.5", "windows-x64", "git-next-1.0.5-windows-x64.zip", gitHash, terminalHash, files)

	cgo, ok := manifest["cgo_enabled"].(bool)
	if !ok || cgo {
		t.Fatalf("manifest cgo_enabled = %v, want explicit false", manifest["cgo_enabled"])
	}
	for key, want := range map[string]string{
		"product":         "git-next",
		"version":         "1.0.5",
		"platform":        "windows-x64",
		"git_sha256":      gitHash,
		"terminal_sha256": terminalHash,
	} {
		if got := manifest[key]; got != want {
			t.Errorf("manifest %s = %v, want %s", key, got, want)
		}
	}
	staged, ok := manifest["files"].(map[string]string)
	if !ok || len(staged) != 1 || staged["bin/git.exe"] != files["bin/git.exe"] {
		t.Fatalf("manifest files = %v, want one staged entry", manifest["files"])
	}
}

func TestReleaseSHASumsLineFormat(t *testing.T) {
	hash := strings.Repeat("d", 64)
	line := sha256sumLine(hash, "git-next-1.0.5-linux-arm64.tar.gz")
	want := hash + "  git-next-1.0.5-linux-arm64.tar.gz\n"
	if line != want {
		t.Fatalf("SHA256SUMS line = %q, want %q", line, want)
	}
	if strings.Count(line, "\n") != 1 || strings.Contains(line, " *") {
		t.Fatalf("SHA256SUMS must be one text-mode line, got %q", line)
	}
}

func TestReleasePlatformWhitelist(t *testing.T) {
	for _, platform := range []string{"windows-x64", "linux-x64", "linux-arm64", "macos-arm64"} {
		if !isReleasePlatform(platform) {
			t.Errorf("platform %q must be supported", platform)
		}
		if _, ok := releasePlatformSpecFor(platform); !ok {
			t.Errorf("platform %q has no build spec", platform)
		}
	}
	for _, platform := range []string{"", "WINDOWS-X64", "windows-arm64", "linux-x86", "macos-x64", "darwin-amd64"} {
		if isReleasePlatform(platform) {
			t.Errorf("platform %q must be rejected", platform)
		}
	}
	windows, _ := releasePlatformSpecFor("windows-x64")
	if windows.goos != "windows" || windows.goarch != "amd64" || windows.ext != ".zip" || windows.gitName() != "git.exe" {
		t.Errorf("windows-x64 spec = %+v", windows)
	}
	mac, _ := releasePlatformSpecFor("macos-arm64")
	if mac.goos != "darwin" || mac.goarch != "arm64" || mac.ext != ".tar.gz" || mac.gitName() != "git" {
		t.Errorf("macos-arm64 spec = %+v", mac)
	}
}

func TestDefaultTerminalDirFallback(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "git-next")
	primary := filepath.Join(tmp, "git-terminal")
	fallback := filepath.Join(tmp, "gitsss", "git-terminal")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	// A bare directory is not a usable checkout; fall through to the shared
	// workspace layout without requiring a real Git-Terminal repository.
	if err := os.MkdirAll(primary, 0755); err != nil {
		t.Fatal(err)
	}
	if got := defaultTerminalDir(root); got != fallback {
		t.Fatalf("fallback terminal dir = %q, want %q", got, fallback)
	}

	if err := os.MkdirAll(filepath.Join(primary, "target", "go"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := defaultTerminalDir(root); got != primary {
		t.Fatalf("primary terminal dir = %q, want %q", got, primary)
	}
}
