// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVersionAndURLContracts(t *testing.T) {
	for _, item := range []struct {
		next, old string
		newer     bool
	}{
		{"0.3.0", "0.2.9", true}, {"0.3.0", "0.3.0-dev", true},
		{"0.3.0", "0.3.0", false}, {"0.3.0", "1.0.0", false},
	} {
		got, err := newerVersion(item.next, item.old)
		if err != nil || got != item.newer {
			t.Fatalf("version contract: %v %v", got, err)
		}
	}
	for _, value := range []string{"01.2.3", "1.2", "1.2.3-dev", "4294967296.0.0"} {
		if _, err := newerVersion(value, "0.0.0"); err == nil {
			t.Fatal("invalid update version accepted")
		}
	}
	for _, value := range []string{"http://github.com/file", "https://user:secret@github.com/file", "https://github.com:444/file", "https://untrusted.invalid/file"} {
		if publicURL(value, true) {
			t.Fatal("untrusted metadata URL accepted")
		}
	}
	if !publicURL("https://github.com/neko233-com/git-next/releases/latest", true) {
		t.Fatal("official URL refused")
	}
}

func TestOfficialMetadataDigestAndAnonymousTransport(t *testing.T) {
	entry := updateEntry{Asset: "git-next-0.4.0-" + platformID() + ".tar.gz", SHA: strings.Repeat("a", 64), Size: 4096, Kind: "tar"}
	manifest := updateManifest{Schema: 1, Product: "git-next", Version: "0.4.0", Commit: strings.Repeat("b", 40), Parity: true, Platforms: map[string]updateEntry{platformID(): entry}}
	data, _ := json.Marshal(manifest)
	sum := sha256.Sum256(data)
	release := githubRelease{Tag: "v0.4.0", Assets: []releaseAsset{
		{Name: "git-next-update.json", URL: officialURL("0.4.0", "git-next-update.json"), Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(data))},
		{Name: entry.Asset, URL: officialURL("0.4.0", entry.Asset), Digest: "sha256:" + entry.SHA, Size: entry.Size},
	}}
	prior := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = prior })
	install := func(badDigest bool, unavailable bool) {
		http.DefaultTransport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.URL.User != nil {
				t.Error("credential supplied to public update transport")
			}
			body, status := data, 200
			if request.URL.Host == "api.github.com" {
				copy := release
				copy.Assets = append([]releaseAsset(nil), release.Assets...)
				if badDigest {
					copy.Assets[0].Digest = "sha256:" + strings.Repeat("c", 64)
				}
				body, _ = json.Marshal(copy)
				if unavailable {
					status = 429
				}
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Header: make(http.Header), Request: request}, nil
		})
	}
	installed := packageManifest{Version: "0.3.0", Platform: platformID()}
	install(false, false)
	plan, err := officialPlan(installed)
	if err != nil || !plan.Newer || plan.Trust != "official-api-digest" {
		t.Fatalf("valid official plan refused: %v", err)
	}
	install(true, false)
	if _, err := officialPlan(installed); err == nil {
		t.Fatal("mismatched digest accepted")
	}
	install(false, true)
	plan, err = officialPlan(installed)
	if err != nil || plan.Trust != "official-release-https" {
		t.Fatalf("official fallback refused: %v", err)
	}
}

func TestConfinedArchiveAndCurrentActivation(t *testing.T) {
	for _, item := range []struct {
		name, target string
		kind         byte
		mode         int64
	}{
		{"fixture/../../escape", "", tar.TypeReg, 0644},
		{"fixture/link", "../../escape", tar.TypeSymlink, 0777},
		{"fixture/special", "", tar.TypeFifo, 0600},
		{"fixture/writable", "", tar.TypeReg, 0666},
	} {
		dir := t.TempDir()
		archive := filepath.Join(dir, "input.tar.gz")
		file, err := os.Create(archive)
		if err != nil {
			t.Fatal(err)
		}
		compressed := gzip.NewWriter(file)
		writer := tar.NewWriter(compressed)
		if err := writer.WriteHeader(&tar.Header{Name: item.name, Linkname: item.target, Typeflag: item.kind, Mode: item.mode}); err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		_ = compressed.Close()
		_ = file.Close()
		stage := filepath.Join(dir, "stage")
		if err := extractPackage(archive, stage, "fixture"); err == nil {
			t.Fatal("hostile archive accepted")
		}
	}
	base := t.TempDir()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	c := contextState{base: base, root: root}
	target := "releases/0.3.0-" + strings.Repeat("a", 16)
	if err := os.MkdirAll(filepath.Join(base, target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.switchCurrent(target); err != nil {
		t.Fatal(err)
	}
	if value, err := root.Readlink("current"); err != nil || value != target {
		t.Fatal("atomic activation did not select owned release")
	}
	if err := c.switchCurrent("../outside"); err == nil {
		t.Fatal("activation traversal accepted")
	}
}

func TestConfigurationRollbackPreservesConcurrentChanges(t *testing.T) {
	for _, edited := range []bool{false, true} {
		base := t.TempDir()
		root, err := os.OpenRoot(base)
		if err != nil {
			t.Fatal(err)
		}
		data := []byte("[include]\n path = fixture.gitconfig\n")
		sum := sha256.Sum256(data)
		name := filepath.Join(base, "config/system.gitconfig")
		c := contextState{base: base, root: root, receipt: installation{Scope: "user", UID: uint32(os.Geteuid()),
			Configuration: name, ConfigTemporary: filepath.Join(base, "config/.fixture"), ConfigSHA: hex.EncodeToString(sum[:]), ConfigCreated: true}}
		if err := c.installConfiguration(data); err != nil {
			root.Close()
			t.Fatal(err)
		}
		if edited {
			if err := os.WriteFile(name, []byte("user concurrent change\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.rollbackConfiguration(); err != nil {
			root.Close()
			t.Fatal(err)
		}
		actual, err := os.ReadFile(name)
		if edited && (err != nil || string(actual) != "user concurrent change\n") {
			t.Fatal("rollback removed user changes")
		}
		if !edited && !os.IsNotExist(err) {
			t.Fatal("rollback retained owned unchanged configuration")
		}
		_ = root.Close()
	}
}
