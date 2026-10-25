// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDefaultByteSizeGitUnitsAndOverflow(t *testing.T) {
	for value, want := range map[string]uint64{
		"0": 0, "17": 17, "32m": 32 << 20, "32M": 32 << 20,
		"8k": 8 << 10, "8K": 8 << 10, "1g": 1 << 30, "1G": 1 << 30,
		"18446744073709551615": ^uint64(0),
	} {
		got, e := parseDefaultByteSize(value)
		if e != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", value, got, e, want)
		}
	}
	for _, value := range []string{"", "m", "32mb", "32MiB", "3.5m", "-32m", "+32m", "32 m", "32m ", "032m", "08m", "0x20", "18446744073709551616", "18014398509481984k", "17592186044416m", "17179869184g"} {
		if got, e := parseDefaultByteSize(value); e == nil {
			t.Errorf("accepted invalid/overflowing byte size %q as %d", value, got)
		}
	}
}

func TestResourceDefaultValidationBoundsAndZeroUnlimited(t *testing.T) {
	for _, key := range []string{"core.deltaBaseCacheLimit", "pack.windowMemory", "pack.deltaCacheSize"} {
		section, name, _ := strings.Cut(key, ".")
		for _, value := range []string{"32m", "32M", "1k", "1g", "33554432", "2147483647"} {
			if e := validateDefaultValue(setting{section, name, value}); e != nil {
				t.Errorf("rejected portable bound %s=%s: %v", key, value, e)
			}
		}
		for _, value := range []string{"0", "0m", "2g", "2147483648", "-1", "32mb", "32MiB", "18446744073709551615g"} {
			if e := validateDefaultValue(setting{section, name, value}); e == nil {
				t.Errorf("accepted unbounded/nonportable candidate %s=%s", key, value)
			}
		}
	}
	for _, key := range []string{"pack.threads", "lfs.concurrenttransfers"} {
		section, name, _ := strings.Cut(key, ".")
		for _, value := range []string{"1", "2", "64"} {
			if e := validateDefaultValue(setting{section, name, value}); e != nil {
				t.Errorf("rejected worker count %s=%s: %v", key, value, e)
			}
		}
		for _, value := range []string{"0", "-1", "65", "02", "010", "2m", "4294967296"} {
			if e := validateDefaultValue(setting{section, name, value}); e == nil {
				t.Errorf("accepted unbounded candidate workers %s=%s", key, value)
			}
		}
	}
	if e := validateDefaultValue(setting{"maintenance", "auto", "true"}); e == nil {
		t.Fatal("shipped defaults accepted automatic per-repository maintenance")
	}
}

func TestDefaultsRejectCaseInsensitiveDuplicatesAndInvalidUnits(t *testing.T) {
	for _, fragment := range []string{
		"[core]\n\tautocrlf = input\n[pack]\n\twindowMemory = 32m\n\tWindowMemory = 16m\n",
		"[core]\n\tautocrlf = input\n[pack]\n\twindowMemory = 32MiB\n",
		"[core]\n\tautocrlf = input\n[pack]\n\tthreads = 0\n",
	} {
		root := t.TempDir()
		if e := write(filepath.Join(root, "packaging/runtime-defaults/gitconfig"), []byte(fragment), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := readDefaults(root); e == nil {
			t.Fatal("accepted ambiguous or invalid resource fragment")
		}
	}
}

func authoritativeDefaults(t *testing.T) (string, defaults) {
	t.Helper()
	root, e := findRoot(".")
	if e != nil {
		t.Fatal(e)
	}
	d, e := readDefaults(root)
	if e != nil {
		t.Fatal(e)
	}
	return root, d
}

func TestResourceDefaultsRenderSharedSubsetAndKeepWindowsReadSafety(t *testing.T) {
	_, d := authoritativeDefaults(t)
	resources, e := d.resources()
	if e != nil {
		t.Fatal(e)
	}
	want := "[core]\n\tdeltaBaseCacheLimit = 32m\n[maintenance]\n\tauto = false\n[pack]\n\tthreads = 2\n\twindowMemory = 32m\n\tdeltaCacheSize = 32m\n[lfs]\n\tconcurrenttransfers = 2\n"
	if resources != want {
		t.Fatalf("shared resource subset differs:\n%s", resources)
	}
	posix, e := posixRuntimeDefaults(d)
	if e != nil || !strings.HasSuffix(posix, resources) || !strings.Contains(posix, "\tprocess = git-lfs filter-process\n\trequired = true\n") || !strings.Contains(posix, "\tdefaultBranch = main\n") {
		t.Fatalf("POSIX assembly changed its existing LF/LFS/init policy: %q, %v", posix, e)
	}
	for _, forbidden := range []string{"fscache", "fsmonitor", "preloadIndex", "[index]", "[grep]", "autocrlf", "defaultBranch"} {
		if strings.Contains(resources, forbidden) || (forbidden != "autocrlf" && forbidden != "defaultBranch" && strings.Contains(posix, forbidden)) {
			t.Errorf("POSIX resource subset imported platform/read policy %s", forbidden)
		}
	}
	values := map[string]string{}
	for _, entry := range d.entries {
		values[strings.ToLower(entry.section+"."+entry.key)] = entry.value
	}
	for key, value := range map[string]string{"core.fscache": "false", "core.fsmonitor": "false", "core.preloadindex": "false", "index.threads": "1", "grep.threads": "1"} {
		if values[key] != value {
			t.Errorf("Windows read-safety %s=%s, want %s", key, values[key], value)
		}
	}
	missing := d
	missing.entries = nil
	for _, entry := range d.entries {
		if entry.section+"."+entry.key != "pack.windowMemory" {
			missing.entries = append(missing.entries, entry)
		}
	}
	if _, e := missing.resources(); e == nil {
		t.Fatal("rendered an incomplete resource budget")
	}
}

func TestResourceDefaultsRenderActualCandidateValues(t *testing.T) {
	_, d := authoritativeDefaults(t)
	for i, entry := range d.entries {
		if entry.section+"."+entry.key == "pack.windowMemory" {
			d.entries[i].value = "48M"
		}
	}
	text, e := d.resources()
	if e != nil || !strings.Contains(text, "\twindowMemory = 48M\n") || strings.Contains(text, "\twindowMemory = 32m\n") {
		t.Fatalf("resource renderer duplicated candidate literals: %q, %v", text, e)
	}
}

func TestResourceDefaultsMigrationAgreement(t *testing.T) {
	root, _ := authoritativeDefaults(t)
	_, checks, e := agreement(&app{root: root})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range checks {
		if !c.OK {
			t.Errorf("%s: %s", c.Name, c.Detail)
		}
	}
}

func TestResourceDefaultsGitRepositoryAndCommandOverride(t *testing.T) {
	reference := os.Getenv("GIT_NEXT_DEFAULTS_REFERENCE_GIT")
	if reference == "" {
		t.Skip("set GIT_NEXT_DEFAULTS_REFERENCE_GIT to an explicit reference Git for isolated config precedence")
	}
	if !filepath.IsAbs(reference) {
		t.Fatal("reference Git must be an explicit absolute path")
	}
	_, d := authoritativeDefaults(t)
	root := t.TempDir()
	system := filepath.Join(root, "system.gitconfig")
	global := filepath.Join(root, "global.gitconfig")
	home := filepath.Join(root, "home")
	if e := os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	env := fixtureEnv(cleanEnvironment(home), map[string]string{"GIT_CONFIG_NOSYSTEM": "0", "GIT_CONFIG_SYSTEM": system, "GIT_CONFIG_GLOBAL": global})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	keys := []string{"maintenance.auto", "core.deltaBaseCacheLimit", "pack.threads", "pack.windowMemory", "pack.deltaCacheSize", "lfs.concurrenttransfers"}
	defaults := []string{"false", "33554432", "2", "33554432", "33554432", "2"}
	repoValues := []string{"true", "16777216", "3", "50331648", "16777216", "3"}
	commandValues := []string{"false", "8388608", "4", "8388608", "8388608", "4"}
	for _, platform := range []string{"windows", "posix"} {
		t.Run(platform, func(t *testing.T) {
			text := d.block()
			if platform == "posix" {
				var e error
				text, e = posixRuntimeDefaults(d)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e := os.WriteFile(system, []byte(text), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(global, nil, 0600); e != nil {
				t.Fatal(e)
			}
			repo := filepath.Join(root, platform)
			for _, dir := range []string{"objects", "refs/heads"} {
				if e := os.MkdirAll(filepath.Join(repo, ".git", dir), 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e := os.WriteFile(filepath.Join(repo, ".git/HEAD"), []byte("ref: refs/heads/main\n"), 0600); e != nil {
				t.Fatal(e)
			}
			config := filepath.Join(repo, ".git/config")
			if e := os.WriteFile(config, []byte("[core]\n\trepositoryformatversion = 0\n\tbare = false\n"), 0600); e != nil {
				t.Fatal(e)
			}
			query := func(key, expected, override string) {
				t.Helper()
				args := []string{}
				if override != "" {
					args = append(args, "-c", key+"="+override)
				}
				kind := "int"
				if key == "maintenance.auto" {
					kind = "bool"
				}
				args = append(args, "config", "--type="+kind, "--get", key)
				cmd := exec.CommandContext(ctx, reference, args...)
				cmd.Dir, cmd.Env = repo, env
				out, e := cmd.CombinedOutput()
				if e != nil || strings.TrimSpace(string(out)) != expected {
					t.Fatalf("%s: expected %s, got %q: %v", key, expected, out, e)
				}
			}
			for i, key := range keys {
				query(key, defaults[i], "")
			}
			for _, args := range [][]string{{"maintenance", "run", "--task=commit-graph"}, {"gc", "--quiet"}} {
				cmd := exec.CommandContext(ctx, reference, args...)
				cmd.Dir, cmd.Env = repo, env
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("maintenance.auto=false blocked manual %s: %q: %v", args[0], out, e)
				}
			}
			local := "[maintenance]\n\tauto = true\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tdeltaBaseCacheLimit = 16m\n[pack]\n\tthreads = 3\n\twindowMemory = 48m\n\tdeltaCacheSize = 16m\n[lfs]\n\tconcurrenttransfers = 3\n"
			if e := os.WriteFile(config, []byte(local), 0600); e != nil {
				t.Fatal(e)
			}
			for i, key := range keys {
				query(key, repoValues[i], "")
				query(key, commandValues[i], commandValues[i])
			}
			actual, e := os.ReadFile(system)
			if e != nil || !bytes.Equal(actual, []byte(text)) {
				t.Fatal("config queries rewrote shipped system defaults")
			}
			if bytes, e := os.ReadFile(config); e != nil || string(bytes) != local {
				t.Fatal("-c wrote into repository configuration")
			}
			for _, key := range []string{"pack.windowMemory", "pack.deltaCacheSize", "core.deltaBaseCacheLimit"} {
				query(key, strconv.Itoa(8<<20), "8M")
			}
		})
	}
}

func TestResourceMaintenanceTrace2PositiveControlAndMalformedInput(t *testing.T) {
	data := []byte("{\"event\":\"version\"}\n{\"event\":\"child_start\",\"argv\":[\"git\",\"maintenance\",\"run\",\"--auto\"]}\n{\"event\":\"child_start\",\"argv\":[\"git-lfs\",\"post-commit\"]}\n{\"event\":\"child_start\",\"argv\":[\"git\",\"maintenance\",\"run\",\"--auto\"]}\n")
	if count, e := maintenanceChildStarts(data); e != nil || count != 2 {
		t.Fatalf("actual child_start counting: %d, %v", count, e)
	}
	if count, e := maintenanceChildStarts([]byte("{\"event\":\"child_start\",\"argv\":[\"git\",\"maintenance-helper\"]}\n")); e != nil || count != 0 {
		t.Fatalf("matched an unrelated helper: %d, %v", count, e)
	}
	for _, invalid := range [][]byte{nil, []byte("not-json\n"), []byte("{\"event\":\"child_start\",\"argv\":false}\n")} {
		if _, e := maintenanceChildStarts(invalid); e == nil {
			t.Fatal("invalid/missing Trace2 evidence was accepted")
		}
	}
}

func TestResourcePackProgressAndFullObjectSet(t *testing.T) {
	if count, e := packProgressThreads([]byte("Counting objects: 100% (4/4)\r\nDelta compression using up to 2 threads\n")); e != nil || count != 2 {
		t.Fatalf("pack worker progress: %d, %v", count, e)
	}
	for _, invalid := range [][]byte{nil, []byte("pack.threads=2\n"), []byte("Delta compression using up to 2 threads\nDelta compression using up to 4 threads\n")} {
		if _, e := packProgressThreads(invalid); e == nil {
			t.Fatal("config text/missing/duplicate progress was accepted as real pack evidence")
		}
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	revisions := []byte(a + "\n" + b + " asset.txt\n")
	verified := []byte(a + " commit 120 97 12\n" + b + " blob 2048 64 109 1 " + a + "\nnon delta: 1 object\nfixture.pack: ok\n")
	if objects, e := packObjectsMatch(revisions, verified); e != nil || objects != 2 {
		t.Fatalf("complete verified pack: %d, %v", objects, e)
	}
	for _, invalid := range [][]byte{
		[]byte(a + " commit 120 97 12\n"),
		append(append([]byte{}, verified...), []byte(a+" commit 120 97 12\n")...),
		[]byte(a + " invalid 120 97 12\n" + b + " blob 2048 64 109\n"),
		[]byte(a + " commit bad 97 12\n" + b + " blob 2048 64 109\n"),
		[]byte(strings.Repeat("c", 40) + " commit 120 97 12\n" + b + " blob 2048 64 109\n"),
	} {
		if _, e := packObjectsMatch(revisions, invalid); e == nil {
			t.Fatal("incomplete/corrupt/foreign/duplicate pack inventory was accepted")
		}
	}
}

func TestResourceLFSEnvAndOwnedHelperBinding(t *testing.T) {
	if count, e := lfsConcurrentTransfers([]byte("git-lfs/3.7.1\r\nConcurrentTransfers=2\r\nBasicTransfersOnly=false\r\n")); e != nil || count != 2 {
		t.Fatalf("actual env parsing: %d, %v", count, e)
	}
	for _, invalid := range []string{"", "lfs.concurrenttransfers=2\n", "ConcurrentTransfers=0\n", "ConcurrentTransfers=02\n", "ConcurrentTransfers=bad\n", "ConcurrentTransfers=2\nConcurrentTransfers=4\n"} {
		if _, e := lfsConcurrentTransfers([]byte(invalid)); e == nil {
			t.Fatal("missing/invalid/ambiguous LFS env was accepted")
		}
	}
	root := t.TempDir()
	suffix := ""
	helperMember, platform := "runtime/usr/bin/git-lfs", "linux-x64"
	if runtime.GOOS == "windows" {
		suffix = ".exe"
		helperMember, platform = "runtime/mingw64/bin/git-lfs.exe", "windows-x64"
	}
	git := filepath.Join(root, "bin", "git"+suffix)
	lfs := filepath.Join(root, filepath.FromSlash(helperMember))
	if e := write(git, []byte("owned-git-fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := write(lfs, []byte("owned-lfs-fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	manifest := record{"schema_version": 1, "product": "git-next", "platform": platform, "engine": "source-built-git-next-native-core", "files": record{"bin/git" + suffix: record{"sha256": digestBytes([]byte("owned-git-fixture"))}, helperMember: record{"sha256": digestBytes([]byte("owned-lfs-fixture"))}}}
	if e := writeJSON(filepath.Join(root, "git-next-distribution.json"), manifest); e != nil {
		t.Fatal(e)
	}
	bound, e := ownedResourceLFS(git)
	if e != nil || bound.sha256 != digestBytes([]byte("owned-lfs-fixture")) {
		t.Fatalf("owned helper binding: %v", e)
	}
	wantedInfo, e := os.Stat(lfs)
	if e != nil {
		t.Fatal(e)
	}
	actualInfo, e := os.Stat(bound.path)
	if e != nil || !os.SameFile(wantedInfo, actualInfo) {
		t.Fatalf("owned helper physical identity differs: %v", e)
	}
	if e := os.WriteFile(lfs, []byte("changed-lfs"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ownedResourceLFS(git); e == nil {
		t.Fatal("changed bundled helper was accepted")
	}
	if e := os.Remove(lfs); e != nil {
		t.Fatal(e)
	}
	if _, e := ownedResourceLFS(git); e == nil {
		t.Fatal("missing bundled helper was allowed to fall back to PATH")
	}
}

func TestResourceDefaultsActualOwnedPackage(t *testing.T) {
	git := os.Getenv("GIT_NEXT_DEFAULTS_NATIVE_GIT")
	if git == "" {
		t.Skip("set GIT_NEXT_DEFAULTS_NATIVE_GIT for the bounded real native package resource probe")
	}
	_, d := authoritativeDefaults(t)
	checks, e := defaultResourceBehavior(git, d)
	if e != nil {
		t.Fatal(e)
	}
	if len(checks) != 6 {
		t.Fatalf("expected six real resource checks, got %d", len(checks))
	}
	for _, c := range checks {
		t.Log(c.Name, c.Detail)
		if !c.OK {
			t.Errorf("real default behavior failed: %s", c.Name)
		}
	}
}
