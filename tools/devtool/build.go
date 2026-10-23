// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var nativePrograms = []string{"git", "git-remote-http", "git-http-fetch", "git-http-push", "git-http-backend", "git-daemon", "git-shell", "scalar", "git-imap-send", "git-sh-i18n--envsubst"}

func init() {
	register("build-native-dependencies", "Compile pinned core dependencies without running products/tests", "platform work-dir cache prefix cmake generator make-program toolchain psl tls-prefix jobs", buildNativeDependencies)
	register("build-native-core", "Compile Windows native core from pinned integrated inputs", "platform prepared-source? toolchain make-program source build-dir dependencies iconv psl build-shell cmake receipt jobs", buildNativeWindows)
}
func requireHash(path string, want any) error {
	hash, e := digest(path)
	if e != nil {
		return e
	}
	if hash != want {
		return fmt.Errorf("immutable input differs: %s", path)
	}
	return nil
}
func receiptFiles(root string, m record) error {
	entries := object(m, "files")
	if len(entries) == 0 {
		return fmt.Errorf("dependency receipt has no immutable files")
	}
	for name, expected := range entries {
		path, e := confined(root, name)
		if e != nil {
			return e
		}
		if e = requireHash(path, expected); e != nil {
			return e
		}
	}
	return nil
}
func buildRun(a *app, cwd string, env []string, program string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	r, e := runProcess(ctx, cwd, env, nil, program, args...)
	if len(r.stdout) > 0 {
		_, _ = a.out.Write(r.stdout)
	}
	if len(r.stderr) > 0 {
		_, _ = a.err.Write(r.stderr)
	}
	if e != nil {
		return e
	}
	if r.code != 0 {
		return fmt.Errorf("build tool %s exited %d", filepath.Base(program), r.code)
	}
	return nil
}
func yesNo(value bool) string {
	if value {
		return "ON"
	}
	return "OFF"
}
func buildEnvironment() []string {
	return append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "SOURCE_DATE_EPOCH=0", "GIT_DATE=1970-01-01", "MSBUILDDISABLENODEREUSE=1")
}
func jobs(o options) (string, error) {
	n, e := o.integer("jobs", 4)
	if e != nil || n < 1 || n > 16 {
		return "", fmt.Errorf("jobs must be 1..16")
	}
	return fmt.Sprint(n), nil
}
func buildNativeDependencies(a *app, o options) error {
	if e := o.require("platform", "work-dir", "cache", "prefix"); e != nil {
		return e
	}
	target := o.get("platform")
	windows, mac := strings.HasPrefix(target, "windows-"), strings.HasPrefix(target, "macos-")
	if target != "windows-x64" && target != "windows-arm64" && target != "linux-x64" && target != "linux-arm64" && target != "macos-arm64" {
		return fmt.Errorf("unsupported target")
	}
	if windows != (runtime.GOOS == "windows") && o.get("toolchain") == "" {
		return fmt.Errorf("cross-compilation requires an explicit toolchain")
	}
	if mac && runtime.GOOS != "darwin" && o.get("toolchain") == "" {
		return fmt.Errorf("macOS requires Apple SDK/toolchain")
	}
	parallel, e := jobs(o)
	if e != nil {
		return e
	}
	work, e := absolute(o.get("work-dir"))
	if e != nil {
		return e
	}
	prefix, e := absolute(o.get("prefix"))
	if e != nil {
		return e
	}
	if exists(work) || exists(prefix) {
		return fmt.Errorf("build workspace and dependency prefix must be new")
	}
	lock, e := load(a.path("packaging/native-dependencies.lock.json"))
	if e != nil {
		return e
	}
	prerequisite := o.get("tls-prefix")
	if windows {
		prerequisite = o.get("psl")
	}
	if prerequisite == "" {
		return fmt.Errorf("explicit psl (Windows) or tls-prefix (POSIX) required")
	}
	prerequisite, e = absolute(prerequisite)
	if e != nil {
		return e
	}
	tls, e := load(filepath.Join(prerequisite, "receipt.json"))
	if e != nil {
		return e
	}
	if stringValue(tls, "platform") != target {
		return fmt.Errorf("TLS prerequisite target differs")
	}
	if windows {
		if e = requireHash(filepath.Join(prerequisite, "lib/psl.lib"), tls["import_library_sha256"]); e != nil {
			return e
		}
	} else {
		if stringValue(tls, "tls_backend") != "GnuTLS" {
			return fmt.Errorf("POSIX prerequisite must use GnuTLS")
		}
		if e = receiptFiles(prerequisite, tls); e != nil {
			return e
		}
	}
	if _, e = newDestination(work); e != nil {
		return e
	}
	if _, e = newDestination(prefix); e != nil {
		return e
	}
	env := buildEnvironment()
	if !windows {
		env = append(env, "PKG_CONFIG_PATH="+filepath.Join(prefix, "lib/pkgconfig")+string(os.PathListSeparator)+filepath.Join(prerequisite, "lib/pkgconfig")+string(os.PathListSeparator)+os.Getenv("PKG_CONFIG_PATH"))
	}
	recipes := []struct {
		name  string
		flags []string
	}{
		{"zlib", []string{"-DZLIB_BUILD_SHARED=OFF", "-DZLIB_BUILD_STATIC=ON", "-DZLIB_BUILD_TESTING=OFF"}},
		{"expat", []string{"-DEXPAT_BUILD_TOOLS=OFF", "-DEXPAT_BUILD_EXAMPLES=OFF", "-DEXPAT_BUILD_TESTS=OFF", "-DEXPAT_SHARED_LIBS=OFF"}},
		{"pcre2", []string{"-DBUILD_SHARED_LIBS=OFF", "-DPCRE2_BUILD_PCRE2GREP=OFF", "-DPCRE2_BUILD_TESTS=OFF", "-DPCRE2_BUILD_PCRE2_16=OFF", "-DPCRE2_BUILD_PCRE2_32=OFF", "-DPCRE2_SUPPORT_JIT=ON"}},
		{"nghttp2", []string{"-DBUILD_SHARED_LIBS=OFF", "-DBUILD_STATIC_LIBS=ON", "-DENABLE_LIB_ONLY=ON", "-DENABLE_STATIC_CRT=OFF"}},
		{"curl", []string{"-DBUILD_CURL_EXE=OFF", "-DBUILD_SHARED_LIBS=OFF", "-DBUILD_STATIC_LIBS=ON", "-DBUILD_TESTING=OFF", "-DCURL_USE_OPENSSL=OFF", "-DCURL_USE_SCHANNEL=" + yesNo(windows), "-DCURL_USE_GNUTLS=" + yesNo(!windows), "-DCURL_USE_LIBPSL=ON", "-DUSE_NGHTTP2=ON", "-DNGHTTP2_USE_STATIC_LIBS=ON", "-DCURL_USE_LIBSSH2=OFF", "-DCURL_USE_LIBSSH=OFF", "-DUSE_WIN32_IDN=" + yesNo(windows), "-DUSE_LIBIDN2=" + yesNo(!windows), "-DCURL_USE_GSSAPI=" + yesNo(!windows), "-DCURL_ZLIB=ON", "-DZLIB_USE_STATIC_LIBS=ON", "-DUSE_APPLE_SECTRUST=" + yesNo(mac)}},
	}
	var builds []record
	var excluded []string
	cmake := o.value("cmake", "cmake")
	for _, recipe := range recipes {
		component := object(object(lock, "components"), recipe.name)
		pin := record{"asset": component["archive"], "url": component["url"], "bytes": component["bytes"], "sha256": component["sha256"]}
		archive, e := downloadPinned(pin, o.get("cache"))
		if e != nil {
			return e
		}
		sourceRoot := filepath.Join(work, "source-"+recipe.name)
		_, skipped, e := extractArchive(archive, sourceRoot, extractPolicy{roots: map[string]bool{stringValue(component, "source_prefix"): true}, skipWorkflows: true, maxFiles: 50000, maxBytes: 256 << 20})
		if e != nil {
			return e
		}
		excluded = append(excluded, skipped...)
		source := filepath.Join(sourceRoot, stringValue(component, "source_prefix"))
		build := filepath.Join(work, "build-"+recipe.name)
		args := []string{"-S", source, "-B", build, "-DCMAKE_BUILD_TYPE=Release", "-DCMAKE_INSTALL_PREFIX=" + prefix, "-DCMAKE_POSITION_INDEPENDENT_CODE=ON", "-DCMAKE_PREFIX_PATH=" + filepath.ToSlash(prefix) + ";" + filepath.ToSlash(prerequisite)}
		args = append(args, recipe.flags...)
		if o.get("generator") != "" {
			args = append(args, "-G", o.get("generator"))
		} else if windows && o.get("toolchain") == "" {
			arch := "x64"
			if strings.HasSuffix(target, "arm64") {
				arch = "ARM64"
			}
			args = append(args, "-G", "Visual Studio 18 2026", "-A", arch)
		}
		if o.get("toolchain") != "" {
			args = append(args, "-DCMAKE_TOOLCHAIN_FILE="+o.get("toolchain"))
		}
		if o.get("make-program") != "" {
			args = append(args, "-DCMAKE_MAKE_PROGRAM="+o.get("make-program"))
		}
		if recipe.name == "curl" && windows {
			args = append(args, "-DLIBPSL_INCLUDE_DIR="+filepath.Join(prerequisite, "include"), "-DLIBPSL_LIBRARY="+filepath.Join(prerequisite, "lib/psl.lib"))
		}
		if recipe.name == "curl" && mac {
			args = append(args, "-DCMAKE_OSX_DEPLOYMENT_TARGET=14.0", "-DCURL_GSS_FLAVOR=Apple")
		}
		if e = buildRun(a, a.root, env, cmake, args...); e != nil {
			return e
		}
		if e = buildRun(a, a.root, env, cmake, "--build", build, "--config", "Release", "--target", "install", "--parallel", parallel); e != nil {
			return e
		}
		if recipe.name == "curl" {
			config, e := read(filepath.Join(build, "lib/curl_config.h"))
			if e != nil {
				return e
			}
			features := []string{"USE_NGHTTP2", "USE_LIBPSL"}
			if windows {
				features = append(features, "USE_SCHANNEL", "USE_WIN32_IDN")
			} else {
				features = append(features, "USE_GNUTLS", "HAVE_LIBIDN2", "HAVE_GSSAPI")
			}
			if mac {
				features = append(features, "USE_APPLE_SECTRUST", "HAVE_GSSAPPLE")
			}
			for _, feature := range features {
				if !strings.Contains(config, "#define "+feature+" 1") {
					return fmt.Errorf("required curl feature missing: %s", feature)
				}
			}
		}
		builds = append(builds, record{"name": recipe.name, "source": component, "configure": append([]string{cmake}, args...)})
	}
	all, e := hashes(prefix)
	if e != nil {
		return e
	}
	lockHash, e := digest(a.path("packaging/native-dependencies.lock.json"))
	if e != nil {
		return e
	}
	backend := "GnuTLS"
	if windows {
		backend = "Schannel"
	}
	receipt := record{"schema_version": 1, "status": "dependencies-compiled-not-runtime-accepted", "platform": target, "tls_backend": backend, "apple_sectrust": mac, "lock_sha256": lockHash, "recipes": builds, "prerequisite": tls, "files": all, "excluded_workflows": excluded, "prerequisite_prefix": prerequisite, "utc": time.Now().UTC(), "product_executed": false, "tests_executed": false, "installed_into_product": false, "release_eligible": false}
	if e = writeJSON(filepath.Join(prefix, "receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "platform": target, "files": len(all), "tests_executed": false})
}
func buildNativeWindows(a *app, o options) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows native build requires Windows")
	}
	if e := o.require("source", "build-dir", "dependencies", "iconv", "psl", "build-shell", "cmake", "receipt"); e != nil {
		return e
	}
	if exists(o.get("receipt")) {
		return fmt.Errorf("compilation receipt must be new")
	}
	target := o.value("platform", "windows-x64")
	if target != "windows-x64" && target != "windows-arm64" {
		return fmt.Errorf("unsupported Windows target")
	}
	parallel, e := jobs(o)
	if e != nil {
		return e
	}
	source, e := absolute(o.get("source"))
	if e != nil {
		return e
	}
	build, e := absolute(o.get("build-dir"))
	if e != nil {
		return e
	}
	dependencies, e := absolute(o.get("dependencies"))
	if e != nil {
		return e
	}
	psl, e := absolute(o.get("psl"))
	if e != nil {
		return e
	}
	pslReceipt, e := load(filepath.Join(psl, "receipt.json"))
	if e != nil {
		return e
	}
	if stringValue(pslReceipt, "platform") != target {
		return fmt.Errorf("libpsl target differs")
	}
	if e = requireHash(filepath.Join(psl, "lib/psl.lib"), pslReceipt["import_library_sha256"]); e != nil {
		return e
	}
	if !o.yes("prepared-source") {
		if e = integrateNativeCore(a, options{strings: map[string]*string{"source": &source}}); e != nil {
			return e
		}
	}
	version, e := productVersion(a)
	if e != nil {
		return e
	}
	pin, e := load(a.path("packaging/native-core.lock.json"))
	if e != nil {
		return e
	}
	compatibilityVersion := strings.TrimPrefix(stringValue(pin, "tag"), "v") + ".git-next." + version
	before, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	llvm := o.get("toolchain") != ""
	if llvm && target != "windows-arm64" {
		return fmt.Errorf("isolated LLVM adapter targets ARM64")
	}
	shell := o.get("build-shell")
	env := append(buildEnvironment(), "GIT_VERSION="+compatibilityVersion, "GIT_BUILT_FROM_COMMIT="+stringValue(pin, "commit"), "PATH="+filepath.Dir(shell)+string(os.PathListSeparator)+os.Getenv("PATH"))
	zlib, pcre, flags := "lib/zs.lib", "lib/pcre2-8-static.lib", "/DCURL_STATICLIB /DXML_STATIC /DUSE_LIBPCRE2 /DPCRE2_STATIC"
	if llvm {
		zlib, pcre, flags = "lib/libzs.a", "lib/libpcre2-8.a", "-DCURL_STATICLIB -DXML_STATIC -DUSE_LIBPCRE2 -DPCRE2_STATIC"
	}
	args := []string{"-S", filepath.Join(source, "contrib/buildsystems"), "-B", build, "-DUSE_VCPKG=OFF", "-DCMAKE_PREFIX_PATH=" + filepath.ToSlash(dependencies) + ";" + filepath.ToSlash(psl), "-DLIBPSL_INCLUDE_DIR=" + filepath.ToSlash(filepath.Join(psl, "include")), "-DLIBPSL_LIBRARY=" + filepath.ToSlash(filepath.Join(psl, "lib/psl.lib")), "-DNGHTTP2_USE_STATIC_LIBS:BOOL=ON", "-DZLIB_LIBRARY=" + filepath.ToSlash(filepath.Join(dependencies, zlib)), "-DSH_EXE=" + filepath.ToSlash(shell), "-DCMAKE_C_FLAGS=" + flags, "-DBUILD_TESTING=ON", "-DPCRE2_FOUND=ON", "-DPCRE2_LIBRARIES=" + filepath.ToSlash(filepath.Join(dependencies, pcre)), "-DCMAKE_INSTALL_PREFIX=" + filepath.ToSlash(filepath.Join(filepath.Dir(build), "runtime"))}
	if llvm {
		if o.get("make-program") == "" {
			return fmt.Errorf("LLVM build requires explicit Ninja")
		}
		sdk := os.Getenv("GIT_NEXT_LLVM_MINGW_ROOT")
		if sdk == "" {
			return fmt.Errorf("GIT_NEXT_LLVM_MINGW_ROOT is required")
		}
		args = append(args, "-G", "Ninja", "-DCMAKE_BUILD_TYPE=Release", "-DCMAKE_TOOLCHAIN_FILE="+o.get("toolchain"), "-DCMAKE_MAKE_PROGRAM="+o.get("make-program"), "-DWINDRES_EXE="+filepath.Join(sdk, "bin/aarch64-w64-mingw32-windres.exe"))
		// LLVM's pinned UCRT has C99 snprintf and rejects fopen on directories; cross builds cannot run probes.
		args = append(args, "-DSNPRINTF_OK:BOOL=ON", "-DFREAD_READS_DIRECTORIES_NO:BOOL=ON")
		env = append(env, "CPATH="+source+string(os.PathListSeparator)+os.Getenv("CPATH"))
	} else {
		arch := "x64"
		if target == "windows-arm64" {
			arch = "ARM64"
		}
		args = append(args, "-G", "Visual Studio 18 2026", "-A", arch)
	}
	iconv, e := absolute(o.get("iconv"))
	if e != nil {
		return e
	}
	iconvReceipt, e := load(filepath.Join(iconv, "receipt.json"))
	if e != nil {
		return e
	}
	if stringValue(iconvReceipt, "platform") != target {
		return fmt.Errorf("iconv target differs")
	}
	if e = requireHash(filepath.Join(iconv, "lib/iconv.lib"), iconvReceipt["import_library_sha256"]); e != nil {
		return e
	}
	args = append(args, "-DIconv_INCLUDE_DIR="+filepath.ToSlash(filepath.Join(iconv, "include")), "-DIconv_LIBRARY="+filepath.ToSlash(filepath.Join(iconv, "lib/iconv.lib")), "-DIconv_IS_BUILT_IN=OFF", "-DHAVE_NEW_ICONV:BOOL=ON", "-DICONV_DOESNOT_OMIT_BOM:BOOL=ON")
	cmake := o.get("cmake")
	if e = buildRun(a, a.root, env, cmake, args...); e != nil {
		return e
	}
	compile := []string{"--build", build, "--config", "Release", "--target"}
	compile = append(compile, nativePrograms...)
	compile = append(compile, "--parallel", parallel)
	if llvm {
		compile = append(compile, "--", "-k", "0")
	}
	if e = buildRun(a, a.root, env, cmake, compile...); e != nil {
		return e
	}
	header, e := read(filepath.Join(build, "version-def.h"))
	if e != nil {
		return e
	}
	if !strings.Contains(header, `#define GIT_VERSION "`+compatibilityVersion+`"`) {
		return fmt.Errorf("generated version does not identify the native product")
	}
	after, e := sourceIdentity(source)
	if e != nil {
		return e
	}
	if before != after {
		return fmt.Errorf("native sources changed during compilation")
	}
	binaries := record{}
	expected := uint16(0x8664)
	if target == "windows-arm64" {
		expected = 0xaa64
	}
	for _, name := range nativePrograms {
		p := filepath.Join(build, name+".exe")
		image, e := pe.Open(p)
		if e != nil {
			return e
		}
		machine := image.Machine
		image.Close()
		if machine != expected {
			return fmt.Errorf("compiled image architecture differs: %s", name)
		}
		hash, e := digest(p)
		if e != nil {
			return e
		}
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		binaries[name+".exe"] = record{"sha256": hash, "bytes": info.Size()}
	}
	cmakeHash, e := digest(cmake)
	if e != nil {
		return e
	}
	shellHash, e := digest(shell)
	if e != nil {
		return e
	}
	headerHash, e := digest(filepath.Join(build, "version-def.h"))
	if e != nil {
		return e
	}
	libraries := record{}
	entries, e := os.ReadDir(filepath.Join(dependencies, "lib"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".lib") || strings.HasSuffix(entry.Name(), ".a") {
			hash, e := digest(filepath.Join(dependencies, "lib", entry.Name()))
			if e != nil {
				return e
			}
			libraries[entry.Name()] = hash
		}
	}
	receipt := record{"schema_version": 1, "status": "integrated-core-compiled-not-runtime-accepted", "utc": time.Now().UTC(), "source_tree_sha256": before, "upstream_commit": pin["commit"], "platform": target, "product_version": version, "binaries": binaries, "git_compatibility_version": compatibilityVersion, "generated_version_header_sha256": headerHash, "cmake_sha256": cmakeHash, "build_shell_sha256": shellHash, "dependency_libraries": libraries, "public_suffix_dependency": pslReceipt, "configure_command": append([]string{cmake}, args...), "build_command": append([]string{cmake}, compile...), "product_executed": false, "tests_executed": false, "installed": false, "complete_git_parity": false, "release_eligible": false, "remaining": "Complete owned runtime/iconv, installer/update/platform packages and consolidated acceptance"}
	if e = writeNewJSON(o.get("receipt"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "source_tree_sha256": before, "product_executed": false, "tests_executed": false})
}
