// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func acceptanceSnapshot(root string, worktree bool) map[string]string {
	files := map[string]string{}
	acceptMust(filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		if worktree && d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			if info.Mode().IsRegular() {
				files[filepath.ToSlash(rel)] = acceptHash(p)
			}
		}
		return nil
	}))
	return files
}
func nativeGCM(a *app, o options) error {
	if !runtimeOSWindows() {
		return fmt.Errorf("GCM DPAPI fixture requires native Windows")
	}
	binary, e := acceptBinary(o)
	if e != nil {
		return e
	}
	if e = o.require("helper"); e != nil {
		return e
	}
	helper, e := filepath.Abs(o.get("helper"))
	if e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "git-next-gcm-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	helperHash := acceptHash(helper)
	r := acceptCapture("gcm-isolated-dpapi-and-native-https-callback", func() {
		rustc := os.Getenv("GIT_NEXT_FIXTURE_RUSTC")
		if rustc == "" {
			rustc, e = exec.LookPath("rustc")
			if e != nil {
				home, e := os.UserHomeDir()
				acceptMust(e)
				rustc = filepath.Join(home, ".cargo/bin/rustc.exe")
			}
		}
		reference, e := acceptReference(o)
		acceptMust(e)
		native := filepath.Join(root, "native")
		acceptMust(os.MkdirAll(native, 0755))
		client := filepath.Join(native, "git.exe")
		acceptMust(copyFile(binary, client))
		if o.yes("installed-support") || o.yes("native-distribution") {
			client = binary
		}
		trap := filepath.Join(root, "old-git-tripwire")
		acceptMust(os.MkdirAll(trap, 0755))
		acceptRun(a.root, nil, nil, true, rustc, "--edition=2024", "--crate-name", "gcm_tripwire", "-O", a.path("tests/fixtures/config_tripwire.rs"), "-o", filepath.Join(trap, "git.exe"))
		home, marker, store := filepath.Join(root, "home"), filepath.Join(root, "git-was-executed"), filepath.Join(root, "dpapi-store")
		config := filepath.Join(home, ".gitconfig")
		acceptWrite(config, []byte("[test]\nsource = native-fixture\n"))
		env := fixtureEnv(acceptEnv(home), map[string]string{"GIT_CONFIG_GLOBAL": config, "GIT_EXEC_PATH": native, "PATH": trap, "GIT_NEXT_CONFIG_TRIPWIRE": marker, "GCM_PROVIDER": "generic", "GCM_INTERACTIVE": "never", "GCM_ALLOW_WINDOWSAUTH": "0", "GCM_AUTODETECT_TIMEOUT": "-1", "GCM_CREDENTIAL_STORE": "dpapi", "GCM_DPAPI_STORE_PATH": store})
		if o.yes("native-distribution") {
			env = fixtureEnv(env, map[string]string{"GIT_EXEC_PATH": filepath.Dir(binary)})
		}
		run := func(program, where string, data []byte, code int, extra map[string]string, args ...string) []byte {
			variables := fixtureEnv(env, extra)
			if o.yes("native-distribution") && program == client {
				filtered := []string{}
				for _, v := range variables {
					if !strings.HasPrefix(v, "GIT_EXEC_PATH=") {
						filtered = append(filtered, v)
					}
				}
				variables = filtered
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			p, e := runProcess(ctx, where, variables, data, program, args...)
			acceptMust(e)
			acceptCheck(!exists(marker), "GCM executed old Git tripwire")
			if program == helper {
				acceptCheck(p.code == code, "credential helper %s exited %d (output redacted)", args[0], p.code)
			} else {
				acceptCheck(p.code == code, "%s %v exit %d: %.400s", filepath.Base(program), args, p.code, p.stderr)
			}
			return p.stdout
		}
		repo := filepath.Join(root, "repo")
		run(client, root, nil, 0, nil, "init", repo)
		acceptWrite(filepath.Join(repo, "fixture.txt"), []byte("generated DPAPI migration fixture\n"))
		for _, name := range []string{"origin", "second"} {
			run(client, repo, nil, 0, nil, "remote", "add", name, "https://"+name+".example.invalid/repo")
		}
		for _, working := range []string{repo, filepath.Join(repo, ".git")} {
			expected := acceptRun(working, env, nil, true, reference, "rev-parse", "--absolute-git-dir")
			acceptEqual(run(client, working, nil, 0, nil, "rev-parse", "--absolute-git-dir"), expected, "absolute Git dir")
		}
		run(client, root, nil, 128, nil, "rev-parse", "--absolute-git-dir")
		expected := acceptRun(repo, env, nil, true, reference, "remote", "-v", "show")
		acceptEqual(run(client, repo, nil, 0, nil, "remote", "-v", "show"), expected, "remote -v show")
		token := fixtureSecret()
		credential := []byte("protocol=https\nhost=fixture.example.invalid\nusername=fixture-user\n")
		run(helper, root, append(append([]byte{}, credential...), []byte("password="+token+"\n\n")...), 0, nil, "store")
		saved := acceptanceSnapshot(store, false)
		found := false
		for name := range saved {
			if strings.HasSuffix(name, ".credential") {
				found = true
			}
		}
		acceptCheck(found, "fixture did not use isolated DPAPI store")
		before := acceptanceSnapshot(root, false)
		for _, working := range []string{root, repo} {
			output := run(helper, working, append(append([]byte{}, credential...), byte('\n')), 0, nil, "get")
			values := map[string]string{}
			for _, line := range strings.Split(string(output), "\n") {
				k, v, ok := strings.Cut(line, "=")
				if ok {
					values[k] = v
				}
			}
			acceptCheck(values["username"] == "fixture-user" && values["password"] == token, "credential get mismatch (redacted)")
		}
		acceptCheck(equalJSON(acceptanceSnapshot(root, false), before), "credential lookup modified fixture files")
		backendEnv := fixtureEnv(env, map[string]string{"PATH": os.Getenv("PATH")})
		filtered := []string{}
		for _, item := range backendEnv {
			if !strings.HasPrefix(item, "GIT_EXEC_PATH=") {
				filtered = append(filtered, item)
			}
		}
		backendEnv = filtered
		core := strings.TrimSpace(string(acceptRun(root, backendEnv, nil, true, reference, "--exec-path")))
		backendEnv = fixtureEnv(backendEnv, map[string]string{"GIT_EXEC_PATH": core})
		acceptRun(root, backendEnv, nil, true, reference, "init", "--bare", "-b", "main", filepath.Join(root, "repository.git"))
		run(client, repo, nil, 0, nil, "add", "-A")
		run(client, repo, nil, 0, nil, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
		acceptRun(repo, backendEnv, nil, true, reference, "push", filepath.Join(root, "repository.git"), "main")
		refs := strings.Fields(string(acceptRun(root, backendEnv, nil, true, reference, "ls-remote", filepath.Join(root, "repository.git"))))
		service, e := newGitHTTPFixture(root, filepath.Join(core, "git-http-backend.exe"), backendEnv, token)
		acceptMust(e)
		defer service.close()
		locator := record{"schema_version": 1, "product": "git-next", "credential_helper": helper, "native_callback_directory": native}
		locatorPath := filepath.Join(filepath.Dir(client), "git-next-support.json")
		var locatorBefore []byte
		if o.yes("native-distribution") {
		} else if o.yes("installed-support") {
			locatorBefore = acceptBytes(locatorPath)
			actual := acceptJSON(locatorBefore)
			fi, e := os.Stat(stringValue(actual, "credential_helper"))
			acceptMust(e)
			other, e := os.Stat(helper)
			acceptMust(e)
			acceptCheck(os.SameFile(fi, other), "installed helper locator differs")
		} else {
			acceptMust(writeJSON(locatorPath, locator))
		}
		configAppend := "\n[credential]\nhelper = manager\nusername = fixture\n[http]\nsslCAInfo = \"" + filepath.ToSlash(service.CA) + "\"\n"
		if o.yes("native-distribution") {
			configAppend += "sslBackend = schannel\nschannelUseSSLCAInfo = true\nschannelCheckRevoke = false\n"
		}
		acceptWrite(config, append(acceptBytes(config), []byte(configAppend)...))
		parsed, e := url.Parse(service.URL)
		acceptMust(e)
		httpContext := []byte("protocol=https\nhost=" + parsed.Host + "\nusername=fixture\n")
		run(helper, root, append(httpContext, []byte("password="+token+"\n\n")...), 0, nil, "store")
		savedCredentials := acceptanceSnapshot(store, false)
		extra := map[string]string{}
		if !o.yes("native-distribution") {
			extra["GIT_EXEC_PATH"] = trap
		}
		advertised := strings.Fields(string(run(client, root, nil, 0, extra, "ls-remote", service.URL)))
		sort.Strings(advertised)
		sort.Strings(refs)
		acceptCheck(len(refs) > 0 && equalJSON(advertised, refs), "authenticated advertisement differs")
		service.mu.Lock()
		authenticated := service.authenticated
		service.mu.Unlock()
		acceptCheck(authenticated > 0, "no authenticated HTTPS request")
		acceptCheck(equalJSON(acceptanceSnapshot(store, false), savedCredentials), "native authentication changed saved credentials")
		if !o.yes("installed-support") && !o.yes("native-distribution") {
			locator["native_callback_directory"] = filepath.Join(root, "missing-callback")
			acceptMust(writeJSON(locatorPath, locator))
			run(client, root, nil, 128, extra, "ls-remote", service.URL)
		} else if !o.yes("native-distribution") {
			acceptEqual(acceptBytes(locatorPath), locatorBefore, "installed locator unchanged")
		}
		acceptCheck(equalJSON(acceptanceSnapshot(store, false), savedCredentials), "saved credentials changed")
		acceptCheck(acceptHash(helper) == helperHash, "helper bytes changed")
	})
	return acceptReport(a, o, record{"status": r["status"], "results": []record{r}, "binary_sha256": acceptHash(binary), "helper_sha256": helperHash, "existing_helper_get": true, "native_git_callbacks": true, "original_git_tripwire_executed": false, "managed_named_helper_https_authentication": true, "incomplete_support_fails_without_old_git": !o.yes("installed-support"), "temporary_dpapi_store_only": true, "get_did_not_modify_files": true, "real_account_login_verified": false, "standalone_helper_packaged": o.yes("installed-support")})
}
func init() {
	register("gcm-acceptance", "Isolated Windows DPAPI and managed named helper HTTPS authentication", "helper reference output installed-support? native-distribution?", nativeGCM)
}
