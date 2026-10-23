// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func lfsAuth(env []string, f *lfsFixture) []string {
	return fixtureEnv(env, map[string]string{"GIT_CONFIG_COUNT": "5", "GIT_CONFIG_KEY_0": "http.extraheader", "GIT_CONFIG_VALUE_0": "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("fixture:"+f.password)), "GIT_CONFIG_KEY_1": "http.sslCAInfo", "GIT_CONFIG_VALUE_1": f.CA, "GIT_CONFIG_KEY_2": "http.schannelUseSSLCAInfo", "GIT_CONFIG_VALUE_2": "true", "GIT_CONFIG_KEY_3": "http.schannelCheckRevoke", "GIT_CONFIG_VALUE_3": "false", "GIT_CONFIG_KEY_4": "credential.helper", "GIT_CONFIG_VALUE_4": ""})
}
func nativeIntegrations(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if e = o.require("output"); e != nil {
		return e
	}
	if exists(o.get("output")) {
		return fmt.Errorf("result output must be new")
	}
	root, e := os.MkdirTemp("", "gn-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	home, repo, keys := filepath.Join(root, "home"), filepath.Join(root, "repo"), filepath.Join(root, "signing")
	if e = os.MkdirAll(keys, 0700); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"GNUPGHOME": keys, "GIT_NEXT_LANGUAGE": "english", "GIT_LFS_SKIP_SMUDGE": "1"})
	f := &cliFixture{root: root, repo: repo, binary: binary, env: env}
	f.At(root, "init", "-q", "-b", "main", repo)
	f.G("config", "user.name", "Fixture")
	f.G("config", "user.email", "fixture@example.invalid")
	results := []record{}
	results = append(results, acceptCapture("D02-primary-lfs-filter-https-upload-download-cache-integrity", func() {
		acceptCheck(bytes.Contains(f.G("lfs", "version"), []byte("git-lfs/")), "LFS identity")
		f.G("lfs", "install", "--local", "--skip-smudge")
		f.G("lfs", "track", "*.asset")
		data := bytes.Repeat([]byte("\x00\r\nfixture LFS content\n"), 4096)
		asset := f.Write("scene.asset", data)
		f.G("add", "-A")
		f.G("commit", "-qm", "LFS fixture")
		pointer := f.G("show", "HEAD:scene.asset")
		oid := digestBytes(data)
		acceptCheck(bytes.Contains(pointer, []byte("oid sha256:"+oid)), "LFS pointer")
		service, e := newLFSFixture(root)
		acceptMust(e)
		defer service.close()
		f.G("remote", "add", "origin", filepath.Join(root, "remote.git"))
		f.G("config", "lfs.url", service.URL)
		f.G("config", "http.sslCAInfo", service.CA)
		f.G("config", "http.extraHeader", "Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:"+service.password)))
		f.G("lfs", "push", "--all", "origin")
		service.mu.Lock()
		uploaded := append([]byte{}, service.objects[oid]...)
		verified := strings.Contains(strings.Join(service.verified, " "), oid)
		service.mu.Unlock()
		acceptEqual(uploaded, data, "uploaded bytes")
		acceptCheck(verified, "upload verify missing")
		cache := filepath.Join(repo, ".git/lfs/objects", oid[:2], oid[2:4], oid)
		acceptMust(os.Remove(cache))
		f.G("lfs", "fetch", "origin", "HEAD")
		acceptEqual(acceptBytes(cache), data, "fetch bytes")
		acceptWrite(asset, pointer)
		f.G("lfs", "checkout")
		acceptEqual(acceptBytes(asset), data, "checkout bytes")
		acceptMust(os.Remove(cache))
		service.mu.Lock()
		service.failure = "corrupt"
		service.mu.Unlock()
		f.Bad("lfs", "fetch", "origin", "HEAD")
		acceptCheck(!exists(cache), "corrupt cache published")
		service.mu.Lock()
		service.failure = ""
		service.mu.Unlock()
		f.G("lfs", "fetch", "origin", "HEAD")
	}))
	results = append(results, acceptCapture("D02-owned-gpg-generated-key-commit-and-tag-verification", func() {
		packageRoot := filepath.Dir(filepath.Dir(binary))
		gpg := ""
		for _, p := range []string{"runtime/usr/bin/gpg", "runtime/usr/bin/gpg.exe", "runtime/mingw64/bin/gpg.exe"} {
			candidate := filepath.Join(packageRoot, filepath.FromSlash(p))
			if exists(candidate) {
				gpg = candidate
				break
			}
		}
		acceptCheck(gpg != "", "owned signing entrypoint missing")
		if runtimeOSWindows() {
			converted := acceptRun(root, env, nil, true, filepath.Join(filepath.Dir(gpg), "cygpath.exe"), "-u", keys)
			env = fixtureEnv(env, map[string]string{"GNUPGHOME": strings.TrimSpace(string(converted))})
			f.env = env
		}
		acceptRun(root, env, nil, true, gpg, "--batch", "--pinentry-mode", "loopback", "--passphrase", "", "--quick-generate-key", "Fixture <fixture@example.invalid>", "ed25519", "sign", "0")
		gpgconf := filepath.Join(filepath.Dir(gpg), "gpgconf"+filepath.Ext(gpg))
		if exists(gpgconf) {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, _ = runProcess(ctx, root, env, nil, gpgconf, "--kill", "all")
			}()
		}
		f.G("config", "gpg.program", filepath.ToSlash(gpg))
		f.G("config", "user.signingkey", "fixture@example.invalid")
		f.G("commit", "--allow-empty", "-S", "-qm", "signed fixture")
		f.G("verify-commit", "HEAD")
		f.G("tag", "-s", "signed-fixture", "-m", "signed tag")
		f.G("verify-tag", "signed-fixture")
	}))
	if runtimeOSWindows() {
		results = append(results, acceptCapture("D05-native-no-console-msi-worker-unicode-quoting-exit-and-argument-guards", func() {
			checkMSIWorker(filepath.Join(filepath.Dir(filepath.Dir(binary)), "updater/msi-worker.exe"), root, env)
		}))
	}
	return acceptReport(a, o, record{"schema_version": 1, "status": acceptStatus(results), "cases": results, "personal_credentials_accessed": false, "fixture_keys_deleted": true})
}

const msiWorkerScript = `param([string]$Mode,[string]$Request)
$path=Join-Path $PSScriptRoot 'result.json'
$count=1
if(Test-Path -LiteralPath $path){$count=(Get-Content -LiteralPath $path -Raw | ConvertFrom-Json).count+1}
$data=@{mode=$Mode;request=$Request;count=$count;input_redirected=[Console]::IsInputRedirected;output_redirected=[Console]::IsOutputRedirected}
Add-Type -TypeDefinition 'using System;using System.Runtime.InteropServices;public static class GnWorkerConsoleProbe{[DllImport("kernel32.dll")]public static extern IntPtr GetConsoleWindow();}'
$data.console_window=[GnWorkerConsoleProbe]::GetConsoleWindow().ToInt64()
[IO.File]::WriteAllText($path,($data | ConvertTo-Json),[Text.UTF8Encoding]::new($false))
exit 17
`

func checkMSIWorker(binary, root string, env []string) {
	acceptCheck(runtimeOSWindows(), "MSI worker is Windows-only")
	updater := filepath.Join(root, "worker 中文 fixture/updater")
	helper := filepath.Join(updater, "msi-worker.exe")
	acceptMust(copyFile(binary, helper))
	acceptWrite(filepath.Join(updater, "update.ps1"), []byte(msiWorkerScript))
	run := func(args ...string) int {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, e := runProcess(ctx, root, env, nil, helper, args...)
		acceptMust(e)
		return r.code
	}
	acceptCheck(run("-Mode", "Register") == 17, "worker exit status")
	result := acceptJSON(acceptBytes(filepath.Join(updater, "result.json")))
	acceptCheck(result["mode"] == "Register" && number(result, "count") == 1 && number(result, "console_window") == 0, "worker console/register")
	for _, request := range []string{"Auto", "Check", "Apply", "Enable", "Disable", ""} {
		acceptCheck(run("-Mode", "Scheduled", "-Request", request) == 17, "scheduled request rejected")
		result = acceptJSON(acceptBytes(filepath.Join(updater, "result.json")))
		expected := request
		if expected == "" {
			expected = "Auto"
		}
		acceptCheck(result["mode"] == "Scheduled" && result["request"] == expected && number(result, "console_window") == 0, "worker scheduled/no-console")
	}
	for _, args := range [][]string{{}, {"-Mode", "Check"}, {"-Mode", "Register", "extra"}, {"-Mode", "Register;&exit 0"}, {"-Mode", "Scheduled", "-Request", "Apply;exit 0"}, {"-Mode", "Register", "-Request", "Apply"}} {
		acceptCheck(run(args...) == 87, "worker accepted unsupported argument %v", args)
	}
	result = acceptJSON(acceptBytes(filepath.Join(updater, "result.json")))
	acceptCheck(number(result, "count") == 7, "worker retried or invalid arguments executed")
}
func workerAcceptance(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "git-next-msi-worker-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	r := acceptCapture("windows-msi-worker", func() { checkMSIWorker(binary, root, acceptEnv(filepath.Join(root, "home"))) })
	return acceptReport(a, o, record{"status": r["status"], "cases": []record{r}})
}
func gameProject(a *app, o options) error {
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if e = o.require("output"); e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "gn-game-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	started := time.Now()
	repo, bare, clone := filepath.Join(root, "game"), filepath.Join(root, "remote.git"), filepath.Join(root, "clone")
	packageRoot := filepath.Dir(filepath.Dir(binary))
	service, e := newLFSFixture(root)
	if e != nil {
		return e
	}
	defer service.close()
	service.locksSupported = true
	env := fixtureEnv(acceptEnv(filepath.Join(root, "home")), map[string]string{"GIT_CONFIG_NOSYSTEM": "0", "GIT_CONFIG_SYSTEM": filepath.Join(packageRoot, "runtime/etc/gitconfig"), "GCM_INTERACTIVE": "never", "GIT_NEXT_LANGUAGE": "english"})
	env = lfsAuth(env, service)
	f := &cliFixture{root: root, repo: repo, binary: binary, env: env}
	f.At(root, "init", "-q", "-b", "main", repo)
	f.At(root, "init", "-q", "--bare", "-b", "main", bare)
	f.G("remote", "add", "origin", bare)
	results := []record{}
	add := func(name string, fn func()) { results = append(results, acceptCapture(name, fn)) }
	add("builtin-lfs-without-separate-install-and-empty-init-worktree", func() {
		acceptCheck(bytes.Contains(f.G("lfs", "version"), []byte("git-lfs/")), "builtin LFS")
		acceptEqual(bytes.TrimSpace(f.G("config", "--get", "filter.lfs.process")), []byte("git-lfs filter-process"), "filter default")
		for _, name := range []string{"pre-push", "post-checkout", "post-commit", "post-merge"} {
			acceptCheck(bytes.Contains(acceptBytes(filepath.Join(repo, ".git/hooks", name)), []byte("git lfs "+name)), "default hook %s", name)
		}
		acceptEqual(f.G("status", "--porcelain"), nil, "empty init")
	})
	attrs := append(acceptBytes(filepath.Join(packageRoot, "docs/game-profiles/unity.gitattributes")), []byte("*.uasset filter=lfs diff=lfs merge=lfs -text lockable\n")...)
	f.Write(".gitattributes", attrs)
	f.Write(".gitignore", acceptBytes(filepath.Join(packageRoot, "docs/game-profiles/unity.gitignore")))
	f.Write(".lfsconfig", []byte("[lfs]\n\turl = "+service.URL+"\n"))
	scene := f.Write("Assets/Scenes/中文场景.unity", []byte("%YAML 1.1\nScene: 1\n"))
	f.Write("Assets/Scenes/中文场景.unity.meta", []byte("fileFormatVersion: 2\nguid: 00000000000000000000000000000001\n"))
	binaryAsset := bytes.Repeat([]byte("\x00\r\nraw asset\n"), 1024)
	f.Write("Assets/Binary/object.asset", binaryAsset)
	media := bytes.Repeat([]byte("\x00\r\nFBX-LFS-fixture\n"), 250000)[:4*1024*1024]
	fbx := f.Write("Assets/Art/model.fbx", media)
	unreal := f.Write("Content/Hero.uasset", bytes.Repeat([]byte("\x00UE-ASSET\r\n"), 1024))
	f.Write("Library/generated-cache", []byte("ignored"))
	f.G("add", "-A")
	f.G("commit", "-qm", "game fixture")
	add("unity-yaml-meta-binary-auto-detection-lfs-pointer-and-real-vs-newline-edits", func() {
		acceptEqual(f.G("show", "HEAD:Assets/Binary/object.asset"), binaryAsset, "binary asset")
		acceptCheck(bytes.Contains(f.G("show", "HEAD:Assets/Art/model.fbx"), []byte("version https://git-lfs.github.com/spec/v1")), "FBX pointer")
		acceptCheck(!bytes.Contains(f.G("ls-files"), []byte("Library/")), "ignored Library")
		acceptWrite(scene, bytes.ReplaceAll(acceptBytes(scene), []byte("\n"), []byte("\r\n")))
		acceptEqual(f.G("status", "--porcelain", "--", "Assets/Scenes"), nil, "game newline only")
		acceptWrite(scene, []byte("%YAML 1.1\nScene: 2\n"))
		acceptCheck(len(f.G("status", "--porcelain", "--", "Assets/Scenes")) > 0, "real edit missing")
		f.G("add", scene)
		f.G("commit", "-qm", "real scene edit")
	})
	add("automatic-lfs-prepush-upload-and-clone-smudge-without-lfs-install", func() {
		f.G("push", "-u", "origin", "main")
		service.mu.Lock()
		_, uploaded := service.objects[digestBytes(media)]
		service.mu.Unlock()
		acceptCheck(uploaded, "automatic upload missing")
		f.At(root, "clone", "-q", bare, clone)
		acceptEqual(acceptBytes(filepath.Join(clone, "Assets/Art/model.fbx")), media, "clone smudge")
		acceptEqual(acceptBytes(filepath.Join(clone, "Assets/Binary/object.asset")), binaryAsset, "clone binary")
		acceptEqual(acceptBytes(filepath.Join(clone, "Assets/Scenes/中文场景.unity")), acceptBytes(scene), "clone scene")
	})
	add("lfs-lock-unlock-duplicate-and-foreign-lock-blocks-ref-publication", func() {
		f.G("lfs", "lock", "Content/Hero.uasset")
		service.mu.Lock()
		locked := len(service.locks) > 0
		service.mu.Unlock()
		acceptCheck(locked, "lock missing")
		f.Bad("lfs", "lock", "Content/Hero.uasset")
		acceptWrite(unreal, bytes.Repeat([]byte("\x00UE-EDIT\r\n"), 1024))
		f.G("add", unreal)
		f.G("commit", "-qm", "locked game edit")
		current := f.At(bare, "rev-parse", "refs/heads/main")
		service.mu.Lock()
		for _, item := range service.locks {
			item["owner"].(map[string]any)["name"] = "other-fixture"
		}
		service.mu.Unlock()
		f.G("config", "lfs.locksverify", "true")
		f.Bad("push", "origin", "main")
		acceptEqual(f.At(bare, "rev-parse", "refs/heads/main"), current, "foreign lock ref")
		service.mu.Lock()
		for _, item := range service.locks {
			item["owner"].(map[string]any)["name"] = "fixture"
		}
		service.mu.Unlock()
		f.G("push", "origin", "main")
		f.G("lfs", "unlock", "Content/Hero.uasset")
		service.mu.Lock()
		unlocked := len(service.locks) == 0
		service.mu.Unlock()
		acceptCheck(unlocked, "unlock retained lock")
	})
	add("failed-game-asset-transfer-retains-remote-refs-and-recovers", func() {
		acceptWrite(fbx, append(append([]byte{}, media...), []byte("changed")...))
		f.G("add", fbx)
		f.G("commit", "-qm", "upload failure fixture")
		current := f.At(bare, "rev-parse", "refs/heads/main")
		service.mu.Lock()
		service.failure = "reject"
		service.mu.Unlock()
		f.Bad("push", "origin", "main")
		acceptEqual(f.At(bare, "rev-parse", "refs/heads/main"), current, "failed upload ref")
		service.mu.Lock()
		service.failure = ""
		service.mu.Unlock()
		f.G("push", "origin", "main")
	})
	add("deep-unicode-game-paths-and-existing-custom-hook-preservation", func() {
		directory := "Assets"
		for i := 0; i < 12; i++ {
			directory += "/deep-asset-folder"
		}
		p := f.Write(directory+"/角色.txt", []byte("long-path\n"))
		f.G("add", p)
		hook := f.Write(".git/hooks/pre-commit", []byte("#!/bin/sh\nexit 42\n"))
		acceptMust(os.Chmod(hook, 0755))
		f.Bad("commit", "-qm", "must reject custom hook")
		acceptEqual(acceptBytes(hook), []byte("#!/bin/sh\nexit 42\n"), "custom hook")
		acceptMust(os.Remove(hook))
		f.G("commit", "-qm", "deep unicode path")
	})
	return acceptReport(a, o, record{"status": acceptStatus(results), "product": "git-next", "seconds": time.Since(started).Seconds(), "separate_lfs_installer_used": false, "personal_repository_accessed": false, "results": results})
}
func init() {
	register("native-integrations-acceptance", "Owned LFS HTTPS, shell, signing and MSI worker fixtures", "output", nativeIntegrations)
	register("native-game-project-acceptance", "Generated Unity/Unreal assets, LFS and lock enforcement", "output", gameProject)
	register("windows-msi-worker-acceptance", "No-console MSI worker argument and exit guards", "output", workerAcceptance)
}
