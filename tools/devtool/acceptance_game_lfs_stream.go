// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

func writeGameAsset(path string, size int64, seed int64) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.CopyN(file, rand.New(rand.NewSource(seed)), size)
	if closeErr := file.Close(); e == nil {
		e = closeErr
	}
	return e
}

func gameLFSStreamAcceptance(a *app, o options) error {
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
	assetMiB, e := strconv.Atoi(o.value("asset-mib", "64"))
	if e != nil || assetMiB < 1 || assetMiB > 256 {
		return fmt.Errorf("asset-mib must be between 1 and 256")
	}
	assetCount, e := strconv.Atoi(o.value("assets", "4"))
	if e != nil || assetCount < 2 || assetCount > 16 {
		return fmt.Errorf("assets must be between 2 and 16 per project")
	}
	root, e := os.MkdirTemp("", "gn-game-lfs-stream-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	service, e := newDiskLFSFixture(root)
	if e != nil {
		return e
	}
	defer service.close()
	packageRoot := filepath.Dir(filepath.Dir(binary))
	config := o.value("gitconfig", filepath.Join(packageRoot, "runtime/etc/gitconfig"))
	config, e = filepath.Abs(config)
	if e != nil || !exists(config) {
		return fmt.Errorf("matching system gitconfig is required")
	}
	home := filepath.Join(root, "home")
	// Resource-only fragments need identical LFS filters for both comparison profiles.
	if e = write(filepath.Join(home, "config"), []byte("[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n"), 0600); e != nil {
		return e
	}
	env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_CONFIG_NOSYSTEM": "0", "GIT_CONFIG_SYSTEM": config, "GCM_INTERACTIVE": "never", "GIT_NEXT_LANGUAGE": "english"})
	env = lfsAuth(env, &lfsFixture{loopbackFixture: service.loopbackFixture, password: service.password})
	started := time.Now()
	fixtures := make([]*cliFixture, 2)
	paths := make([][]string, 2)
	for i := range assetCount {
		paths[0] = append(paths[0], fmt.Sprintf("Assets/Art/asset-%02d.%s", i, []string{"fbx", "psd"}[i%2]))
		paths[1] = append(paths[1], fmt.Sprintf("Content/asset-%02d.%s", i, []string{"uasset", "umap"}[i%2]))
	}
	hashes := make([]map[string]string, 2)
	bares := make([]string, 2)
	results := []record{}
	setup := acceptCapture("two-project-streamed-large-lfs-fixture-setup", func() {
		for i := range fixtures {
			repo := filepath.Join(root, fmt.Sprintf("project-%d", i))
			f := &cliFixture{root: root, repo: repo, binary: binary, env: env}
			fixtures[i] = f
			f.At(root, "init", "-q", "-b", "main", repo)
			bares[i] = filepath.Join(root, fmt.Sprintf("remote-%d.git", i))
			f.At(root, "init", "-q", "--bare", "-b", "main", bares[i])
			f.G("remote", "add", "origin", bares[i])
			f.G("lfs", "install", "--local")
			f.G("config", "lfs.locksverify", "false")
			f.G("config", "lfs.transfer.maxretries", "1")
			f.G("config", "lfs.transfer.maxretrydelay", "0")
			f.Write(".lfsconfig", []byte("[lfs]\n\turl = "+service.URL+"\n"))
			f.Write(".gitattributes", []byte("*.fbx filter=lfs diff=lfs merge=lfs -text\n*.psd filter=lfs diff=lfs merge=lfs -text\n*.uasset filter=lfs diff=lfs merge=lfs -text\n*.umap filter=lfs diff=lfs merge=lfs -text\n"))
			hashes[i] = map[string]string{}
			for j, relative := range paths[i] {
				path := filepath.Join(repo, filepath.FromSlash(relative))
				acceptMust(writeGameAsset(path, int64(assetMiB)<<20, int64(100+i*100+j)))
				hashes[i][relative] = acceptHash(path)
			}
		}
	})
	results = append(results, setup)
	if setup["status"] == "pass" {
		results = append(results, acceptCapture("parallel-unity-unreal-large-lfs-upload-clone-and-sha", func() {
			var wait sync.WaitGroup
			projectResults := make([]record, len(fixtures))
			for i, f := range fixtures {
				wait.Add(1)
				go func(i int, f *cliFixture) {
					defer wait.Done()
					projectResults[i] = acceptCapture(fmt.Sprintf("project-%d", i), func() {
						f.G("add", "-A")
						f.G("commit", "-qm", "large asset fixture")
						for _, relative := range paths[i] {
							acceptCheck(bytes.Contains(f.G("show", "HEAD:"+relative), []byte("oid sha256:"+hashes[i][relative])), "LFS pointer differs")
						}
						f.G("push", "-u", "origin", "main")
						clone := filepath.Join(root, fmt.Sprintf("clone-%d", i))
						f.At(root, "clone", "-q", bares[i], clone)
						for _, relative := range paths[i] {
							acceptCheck(acceptHash(filepath.Join(clone, filepath.FromSlash(relative))) == hashes[i][relative], "cloned asset SHA differs")
						}
						f.At(clone, "lfs", "fsck")
						f.At(clone, "fsck", "--full")
					})
				}(i, f)
			}
			wait.Wait()
			for _, item := range projectResults {
				acceptCheck(item["status"] == "pass", "parallel project failed: %v", item["error"])
			}
		}))
		if results[len(results)-1]["status"] == "pass" {
			results = append(results, acceptCapture("corrupt-large-download-never-publishes-valid-cache-and-retries", func() {
				f := fixtures[0]
				clone := filepath.Join(root, "corrupt-clone")
				acceptRun(root, fixtureEnv(env, map[string]string{"GIT_LFS_SKIP_SMUDGE": "1"}), nil, true, binary, "clone", "-q", bares[0], clone)
				f.At(clone, "config", "lfs.transfer.maxretries", "1")
				f.At(clone, "config", "lfs.transfer.maxretrydelay", "0")
				service.setFailure("corrupt")
				acceptRun(clone, env, nil, false, binary, "lfs", "fetch", "origin", "main")
				for _, oid := range hashes[0] {
					acceptCheck(!exists(filepath.Join(clone, ".git/lfs/objects", oid[:2], oid[2:4], oid)), "corrupt object entered the valid cache")
				}
				service.setFailure("")
				f.At(clone, "lfs", "fetch", "origin", "main")
				f.At(clone, "lfs", "checkout")
				for relative, oid := range hashes[0] {
					acceptCheck(acceptHash(filepath.Join(clone, filepath.FromSlash(relative))) == oid, "retry asset SHA differs")
				}
				f.At(clone, "lfs", "fsck")
			}))
			service.setFailure("")
			results = append(results, acceptCapture("rejected-large-upload-preserves-remote-ref-and-recovers", func() {
				f := fixtures[1]
				before := f.At(bares[1], "rev-parse", "refs/heads/main")
				path := filepath.Join(f.repo, "Content/Rejected.uasset")
				acceptMust(writeGameAsset(path, int64(assetMiB)<<20, 56789))
				f.G("add", "Content/Rejected.uasset")
				f.G("commit", "-qm", "rejected transfer")
				service.setFailure("reject")
				f.Bad("push", "origin", "main")
				acceptEqual(f.At(bares[1], "rev-parse", "refs/heads/main"), before, "rejected upload changed remote ref")
				service.setFailure("")
				f.G("push", "origin", "main")
				acceptEqual(f.At(bares[1], "rev-parse", "refs/heads/main"), f.G("rev-parse", "HEAD"), "retry remote ref")
				f.At(bares[1], "fsck", "--full")
			}))
		}
	}
	stats := service.stats()
	results = append(results, acceptCapture("no-active-fixture-transfers-after-commands", func() {
		acceptCheck(stats["active_transfers"] == 0, "fixture transfer leaked")
	}))
	return acceptReport(a, o, record{"schema_version": 1, "status": acceptStatus(results), "product": "git-next", "scope": "Two disposable Unity/Unreal repositories with disk-streamed LFS objects, parallel transfers, corruption and upload-failure safety", "seconds": time.Since(started).Seconds(), "binary_sha256": acceptHash(binary), "system_gitconfig_sha256": acceptHash(config), "asset_mib": assetMiB, "projects": 2, "assets_per_project": assetCount, "server": stats, "asset_hashes": hashes, "results": results, "personal_repository_accessed": false, "terminal_executed": false, "memory_measurement_scope": "Fixture object bodies stream through fixed buffers; this is not a measurement of total client RSS"})
}

func init() {
	register("native-game-lfs-stream-acceptance", "Parallel large Unity/Unreal LFS assets with disk-streamed failure fixtures", "output asset-mib assets gitconfig", gameLFSStreamAcceptance)
}
