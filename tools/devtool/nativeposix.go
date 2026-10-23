// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	register("build-native-posix", "Compile/stage POSIX native cores without product/test execution", "platform source build-dir dependencies stage receipt cmake shell toolchain generator jobs", buildNativePOSIX)
}
func hostPlatform() string {
	system := runtime.GOOS
	if system == "darwin" {
		system = "macos"
	}
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	return system + "-" + arch
}
func stageInventory(root string) (record, error) {
	result := record{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		relative = filepath.ToSlash(relative)
		info, e := d.Info()
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(p)
			if e != nil {
				return e
			}
			resolved, e := filepath.EvalSymlinks(p)
			if e != nil || !inside(root, resolved) {
				return fmt.Errorf("staging symlink escapes prefix")
			}
			result[relative] = record{"kind": "symlink", "target": target}
			return nil
		}
		hash, e := digest(p)
		if e != nil {
			return e
		}
		result[relative] = record{"kind": "file", "sha256": hash, "bytes": info.Size(), "mode": uint32(info.Mode().Perm())}
		return nil
	})
	return result, e
}
func buildNativePOSIX(a *app, o options) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("run this compiler in a native POSIX environment or owned Linux container")
	}
	if e := o.require("platform", "source", "build-dir", "dependencies", "stage", "receipt"); e != nil {
		return e
	}
	target := o.get("platform")
	if target != "linux-x64" && target != "linux-arm64" && target != "macos-arm64" {
		return fmt.Errorf("unsupported POSIX target")
	}
	if target != hostPlatform() && o.get("toolchain") == "" {
		return fmt.Errorf("different target requires explicit toolchain/SDK")
	}
	parallel, e := jobs(o)
	if e != nil {
		return e
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	dependencies, e := absolute(o.get("dependencies"))
	if e != nil {
		return e
	}
	build, e := absolute(o.get("build-dir"))
	if e != nil {
		return e
	}
	stage, e := absolute(o.get("stage"))
	if e != nil {
		return e
	}
	if exists(stage) || exists(o.get("receipt")) {
		return fmt.Errorf("stage/receipt must be new")
	}
	dependency, e := load(filepath.Join(dependencies, "receipt.json"))
	if e != nil {
		return e
	}
	mac := strings.HasPrefix(target, "macos-")
	if stringValue(dependency, "platform") != target || stringValue(dependency, "tls_backend") != "GnuTLS" || mac && !truth(dependency, "apple_sectrust") {
		return fmt.Errorf("dependency TLS/target contract differs")
	}
	if e = receiptFiles(dependencies, dependency); e != nil {
		return e
	}
	prerequisite := stringValue(dependency, "prerequisite_prefix")
	if e = receiptFiles(prerequisite, object(dependency, "prerequisite")); e != nil {
		return e
	}
	pin, e := load(a.path("packaging/native-core.lock.json"))
	if e != nil {
		return e
	}
	version, e := productVersion(a)
	if e != nil {
		return e
	}
	baseline := strings.SplitN(strings.TrimPrefix(stringValue(pin, "tag"), "v"), ".windows.", 2)[0]
	compatibility := baseline + ".git-next." + version
	before, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	env := append(buildEnvironment(), "GIT_VERSION="+compatibility, "GIT_BUILT_FROM_COMMIT="+stringValue(pin, "commit"), "PKG_CONFIG_PATH="+filepath.Join(dependencies, "lib/pkgconfig")+string(os.PathListSeparator)+filepath.Join(prerequisite, "lib/pkgconfig")+string(os.PathListSeparator)+os.Getenv("PKG_CONFIG_PATH"))
	cmake, e := exec.LookPath(o.value("cmake", "cmake"))
	if e != nil {
		return e
	}
	generator := o.value("generator", "Ninja")
	shell := o.value("shell", "/bin/sh")
	cFlags := "-DPCRE2_STATIC"
	if mac {
		cFlags += " -DHAVE_NS_GET_EXECUTABLE_PATH"
	}
	args := []string{"-S", filepath.Join(source, "contrib/buildsystems"), "-B", build, "-G", generator, "-DCMAKE_BUILD_TYPE=Release", "-DCMAKE_INSTALL_PREFIX=" + stage, "-DCMAKE_PREFIX_PATH=" + dependencies + ";" + prerequisite, "-DSH_EXE=" + shell, "-DBUILD_TESTING=ON", "-DUSE_VCPKG=OFF", "-DCMAKE_C_FLAGS=" + cFlags, "-DMSGFMT_EXE=MSGFMT_EXE-NOTFOUND"}
	if o.get("toolchain") != "" {
		args = append(args, "-DCMAKE_TOOLCHAIN_FILE="+o.get("toolchain"))
	}
	if mac {
		ctx, cancel := deadline()
		defer cancel()
		r, e := runProcess(ctx, a.root, env, nil, "pkg-config", "--libs-only-L", "--static", "libcurl")
		if e != nil {
			return e
		}
		if r.code != 0 {
			return fmt.Errorf("pkg-config libcurl failed")
		}
		args = append(args, "-DCMAKE_OSX_DEPLOYMENT_TARGET=15.5", "-DCMAKE_OSX_ARCHITECTURES=arm64", "-DCMAKE_EXE_LINKER_FLAGS="+strings.TrimSpace(string(r.stdout))+" -L"+shellQuote(filepath.Join(prerequisite, "lib")))
	}
	if e = buildRun(a, a.root, env, cmake, args...); e != nil {
		return e
	}
	programs := append([]string{}, nativePrograms...)
	if mac {
		programs = append(programs, "git-credential-osxkeychain")
	}
	compile := append([]string{"--build", build, "--target"}, programs...)
	compile = append(compile, "shell-gen", "perl-gen", "python-gen", "--parallel", parallel)
	if generator == "Ninja" {
		compile = append(compile, "--", "-k", "0")
	}
	if e = buildRun(a, a.root, env, cmake, compile...); e != nil {
		return e
	}
	header, e := read(filepath.Join(build, "version-def.h"))
	if e != nil {
		return e
	}
	if !strings.Contains(header, `#define GIT_VERSION "`+compatibility+`"`) {
		return fmt.Errorf("generated native version differs")
	}
	for _, program := range programs {
		platform, e := imagePlatform(filepath.Join(build, program))
		if e != nil {
			return e
		}
		if platform != target {
			return fmt.Errorf("native output target differs: %s", program)
		}
	}
	if e = buildRun(a, a.root, env, cmake, "--install", build, "--config", "Release"); e != nil {
		return e
	}
	if strings.HasPrefix(target, "linux-") {
		helper := filepath.Join(stage, "libexec/git-core/git-credential-libsecret")
		ctx, cancel := deadline()
		defer cancel()
		r, e := runProcess(ctx, a.root, env, nil, "pkg-config", "--cflags", "--libs", "libsecret-1", "glib-2.0")
		if e != nil {
			return e
		}
		if r.code != 0 {
			return fmt.Errorf("libsecret/GLib development flags unavailable")
		}
		flags, e := shellWords(string(r.stdout))
		if e != nil {
			return e
		}
		command := []string{"-O2", filepath.Join(source, "contrib/credential/libsecret/git-credential-libsecret.c"), "-o", helper}
		command = append(command, flags...)
		if e = buildRun(a, a.root, env, "cc", command...); e != nil {
			return e
		}
		platform, e := imagePlatform(helper)
		if e != nil {
			return e
		}
		if platform != target {
			return fmt.Errorf("credential helper ISA differs")
		}
		programs = append(programs, "git-credential-libsecret")
	}
	after, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	if after != before {
		return fmt.Errorf("sources changed during native compilation")
	}
	if e = receiptFiles(dependencies, dependency); e != nil {
		return e
	}
	staged, e := stageInventory(stage)
	if e != nil {
		return e
	}
	binaries := record{}
	for _, program := range programs {
		p := filepath.Join(build, program)
		if program == "git-credential-libsecret" {
			p = filepath.Join(stage, "libexec/git-core", program)
		}
		hash, e := digest(p)
		if e != nil {
			return e
		}
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		binaries[program] = record{"sha256": hash, "bytes": info.Size()}
	}
	dependencyHash, e := digest(filepath.Join(dependencies, "receipt.json"))
	if e != nil {
		return e
	}
	headerHash, e := digest(filepath.Join(build, "version-def.h"))
	if e != nil {
		return e
	}
	cmakeHash, e := digest(cmake)
	if e != nil {
		return e
	}
	shellHash, e := digest(shell)
	if e != nil {
		return e
	}
	builderHash, e := digest(a.path("tools/devtool/nativeposix.go"))
	if e != nil {
		return e
	}
	receipt := record{"schema_version": 1, "status": "posix-core-compiled-staged-not-runtime-accepted", "platform": target, "product_version": version, "git_compatibility_version": compatibility, "source_tree_sha256": before, "upstream_commit": pin["commit"], "dependency_receipt_sha256": dependencyHash, "generated_version_header_sha256": headerHash, "builder_source_sha256": builderHash, "binaries": binaries, "staged_files": staged, "configure_command": append([]string{cmake}, args...), "build_command": append([]string{cmake}, compile...), "cmake_sha256": cmakeHash, "build_shell_sha256": shellHash, "utc": time.Now().UTC(), "product_executed": false, "tests_executed": false, "installed": false, "staged": true, "complete_git_parity": false, "release_eligible": false, "remaining": []string{"D02 owned POSIX helper/runtime closure", "D06 updater integration", "D07 complete packages/source-license delivery", "D08 consolidated acceptance", "D09 paired performance batch", "D10 one release and workstation takeover"}}
	if e = writeNewJSON(o.get("receipt"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "platform": target, "staged_files": len(staged), "tests_executed": false})
}

func shellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n'\"$`\\;&|<>()*?[]{}!#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
func shellWords(text string) ([]string, error) {
	var out []string
	var word strings.Builder
	quote := rune(0)
	escaped, active := false, false
	for _, r := range text {
		if escaped {
			if quote == '"' && !strings.ContainsRune("$`\"\\\n", r) {
				word.WriteRune('\\')
			}
			if r != '\n' {
				word.WriteRune(r)
			}
			escaped = false
			active = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			active = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			active = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			active = true
			continue
		}
		if strings.ContainsRune(" \t\r\n", r) {
			if active {
				out = append(out, word.String())
				word.Reset()
				active = false
			}
			continue
		}
		word.WriteRune(r)
		active = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated pkg-config shell quoting")
	}
	if active {
		out = append(out, word.String())
	}
	return out, nil
}
