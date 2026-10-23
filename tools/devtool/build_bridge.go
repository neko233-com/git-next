// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var msvcCompile = []string{"/nologo", "/O2", "/GS-", "/utf-8", "/TC", "/Zl", "/W4", "/WX"}
var mingwFlags = []string{"-O2", "-fno-stack-protector", "-ffreestanding", "-nostdlib", "-Wall", "-Wextra", "-Werror", "-DUNICODE", "-D_UNICODE", "-DWINVER=0x0A00", "-D_WIN32_WINNT=0x0A00"}

func init() {
	register("build-windows-bridge", "Compile full O2 CRT-free console/GUI bridges without executing them", "compiler sdk sdk-version msvc llvm-root platform destination", buildWindowsBridge)
	register("build-windows-msi-worker", "Compile the CRT-free Installer launcher without executing it", "llvm-root platform version destination", buildWindowsMSIWorker)
	register("verify-freestanding-bridge", "Prove freestanding memory helpers and optional MinGW links", "receipt", verifyFreestandingBridge)
	register("native-product", "Read validated independent native product identity", "compatibility?", func(a *app, o options) error {
		if o.yes("compatibility") {
			v, e := nativeCompatibility(a)
			if e != nil {
				return e
			}
			return a.json(v)
		}
		v, e := productVersion(a)
		if e != nil {
			return e
		}
		return a.json(record{"version": v})
	})
}
func rejectReducedInlining(command []string) error {
	for _, s := range command {
		if strings.Contains(s, "Ob1") {
			return fmt.Errorf("reduced inlining is not the CRT-free link fix")
		}
	}
	return nil
}
func bridgeImports(binary, name string, machine uint16) ([]string, error) {
	f, e := pe.Open(binary)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if f.Machine != machine {
		return nil, fmt.Errorf("bridge architecture differs: %s", name)
	}
	if _, ok := f.OptionalHeader.(*pe.OptionalHeader64); !ok {
		return nil, fmt.Errorf("bridge must use PE32+")
	}
	data, e := os.ReadFile(binary)
	if e != nil {
		return nil, e
	}
	_, libs, e := peDependencies(data)
	if e != nil {
		return nil, e
	}
	want := map[string]bool{"kernel32.dll": true, "advapi32.dll": true}
	if strings.Contains(strings.ToLower(name), "gui") {
		want["user32.dll"] = true
	}
	actual := map[string]bool{}
	for _, lib := range libs {
		actual[strings.ToLower(lib)] = true
	}
	if len(actual) != len(want) {
		return nil, fmt.Errorf("unexpected CRT-free bridge imports: %v", libs)
	}
	result := []string{}
	for lib := range actual {
		if !want[lib] {
			return nil, fmt.Errorf("unexpected bridge import: %s", lib)
		}
		result = append(result, lib)
	}
	sort.Strings(result)
	return result, nil
}
func mingwBridgeCommand(compiler, source, memory, resource, output string, gui bool) []string {
	cmd := append([]string{compiler}, mingwFlags...)
	subsystem := "console"
	if gui {
		cmd = append(cmd, "-DGIT_NEXT_GUI_BRIDGE")
		subsystem = "windows"
	}
	cmd = append(cmd, source, memory)
	if resource != "" {
		cmd = append(cmd, resource)
	}
	return append(cmd, "-o", output, "-Wl,--entry,bridge_entry", "-Wl,--subsystem,"+subsystem, "-Wl,--dynamicbase", "-Wl,--nxcompat", "-Wl,--no-insert-timestamp", "-lkernel32", "-ladvapi32", "-luser32")
}
func msvcMemoryCommand(compiler, memory, output string) []string {
	cmd := append([]string{compiler}, msvcCompile...)
	return append(cmd, "/c", "/Fo"+output, memory)
}
func msvcBridgeCommand(compiler, source, memory, resource, output string, includes, libraries []string, gui bool) []string {
	cmd := []string{compiler}
	for _, arg := range msvcCompile {
		if arg != "/TC" {
			cmd = append(cmd, arg)
		}
	}
	for _, s := range []string{"UNICODE", "_UNICODE", "WINVER=0x0A00", "_WIN32_WINNT=0x0A00"} {
		cmd = append(cmd, "/D"+s)
	}
	for _, s := range includes {
		cmd = append(cmd, "/I"+s)
	}
	cmd = append(cmd, "/Fo"+output+".obj", "/Fe"+output)
	subsystem := "CONSOLE"
	if gui {
		cmd = append(cmd, "/DGIT_NEXT_GUI_BRIDGE")
		subsystem = "WINDOWS"
	}
	cmd = append(cmd, "/Tc"+source, memory, "/link", resource, "/NODEFAULTLIB", "/ENTRY:bridge_entry", "/SUBSYSTEM:"+subsystem, "/DYNAMICBASE", "/NXCOMPAT", "/HIGHENTROPYVA")
	for _, s := range libraries {
		cmd = append(cmd, "/LIBPATH:"+s)
	}
	return append(cmd, "kernel32.lib", "advapi32.lib", "user32.lib")
}
func buildCommand(a *app, env []string, command []string) error {
	if e := rejectReducedInlining(command); e != nil {
		return e
	}
	return buildRun(a, a.root, env, command[0], command[1:]...)
}
func fileRecord(p string) (record, error) {
	h, e := digest(p)
	if e != nil {
		return nil, e
	}
	info, e := os.Stat(p)
	if e != nil {
		return nil, e
	}
	return record{"sha256": h, "bytes": info.Size()}, nil
}
func buildWindowsBridge(a *app, o options) error {
	if e := o.require("destination"); e != nil {
		return e
	}
	target := o.value("platform", "windows-x64")
	machine := uint16(0x8664)
	arch := "x64"
	if target == "windows-arm64" {
		machine = 0xaa64
		arch = "arm64"
	} else if target != "windows-x64" {
		return fmt.Errorf("invalid bridge target")
	}
	source, memory, template := a.path("native-core/launcher/windows-bridge.c"), a.path("native-core/launcher/freestanding-memory.c"), a.path("native-core/launcher/windows-bridge.rc.in")
	before, e := fileRecord(source)
	if e != nil {
		return e
	}
	memoryHash, e := digest(memory)
	if e != nil {
		return e
	}
	resourceHash, e := digest(template)
	if e != nil {
		return e
	}
	version, e := productVersion(a)
	if e != nil {
		return e
	}
	match := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-[A-Za-z0-9.-]+)?$`).FindStringSubmatch(version)
	if match == nil {
		return fmt.Errorf("invalid resource version")
	}
	llvm := o.get("llvm-root")
	compiler := o.get("compiler")
	var includes, libraries []string
	if llvm != "" {
		if target != "windows-arm64" {
			return fmt.Errorf("LLVM bridge adapter targets ARM64")
		}
		llvm, e = absolute(llvm)
		if e != nil {
			return e
		}
		compiler = filepath.Join(llvm, "bin/aarch64-w64-mingw32-clang.exe")
	} else {
		if e = o.require("compiler", "sdk", "sdk-version", "msvc"); e != nil {
			return e
		}
		compiler, e = absolute(compiler)
		if e != nil {
			return e
		}
		sdk, e := absolute(o.get("sdk"))
		if e != nil {
			return e
		}
		msvc, e := absolute(o.get("msvc"))
		if e != nil {
			return e
		}
		includes = []string{filepath.Join(msvc, "include")}
		for _, n := range []string{"um", "shared", "ucrt"} {
			includes = append(includes, filepath.Join(sdk, "Include", o.get("sdk-version"), n))
		}
		libraries = []string{filepath.Join(msvc, "lib", arch), filepath.Join(sdk, "Lib", o.get("sdk-version"), "um", arch)}
		for _, p := range append(append([]string{}, includes...), libraries...) {
			info, e := os.Stat(p)
			if e != nil || !info.IsDir() {
				return fmt.Errorf("matching SDK/MSVC directory missing: %s", p)
			}
		}
	}
	if !exists(compiler) {
		return fmt.Errorf("compiler is missing: %s", compiler)
	}
	env := append(os.Environ(), "PATH="+filepath.Dir(compiler)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if llvm == "" {
		env = append(env, "INCLUDE="+strings.Join(includes, ";")+";"+os.Getenv("INCLUDE"), "LIB="+strings.Join(libraries, ";")+";"+os.Getenv("LIB"))
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	resource := filepath.Join(out, "bridge.rc")
	text, e := read(template)
	if e != nil {
		return e
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "@FILE_VERSION@", strings.Join(match[1:4], ",")+",0"), "@PRODUCT_VERSION@", version)
	if e = write(resource, []byte(text), 0644); e != nil {
		return e
	}
	resourceObject := filepath.Join(out, "bridge.res")
	var resourceCommand []string
	if llvm != "" {
		resourceObject = filepath.Join(out, "bridge.o")
		resourceCommand = []string{filepath.Join(llvm, "bin/aarch64-w64-mingw32-windres.exe"), "-I", filepath.Join(llvm, "aarch64-w64-mingw32/include"), resource, resourceObject}
	} else {
		sdk, _ := absolute(o.get("sdk"))
		resourceCommand = []string{filepath.Join(sdk, "bin", o.get("sdk-version"), "x64/rc.exe"), "/nologo"}
		for _, p := range includes {
			resourceCommand = append(resourceCommand, "/i", p)
		}
		resourceCommand = append(resourceCommand, "/fo", resourceObject, resource)
	}
	if e = buildCommand(a, env, resourceCommand); e != nil {
		return e
	}
	commands := [][]string{}
	files, imports := record{}, record{}
	memoryObject := filepath.Join(out, "freestanding-memory.obj")
	if llvm == "" {
		cmd := msvcMemoryCommand(compiler, memory, memoryObject)
		if e = buildCommand(a, env, cmd); e != nil {
			return e
		}
		commands = append(commands, cmd)
	}
	for _, gui := range []bool{false, true} {
		name := "git-next-bridge.exe"
		if gui {
			name = "git-next-bridge-gui.exe"
		}
		binary := filepath.Join(out, name)
		var cmd []string
		if llvm != "" {
			cmd = mingwBridgeCommand(compiler, source, memory, resourceObject, binary, gui)
		} else {
			cmd = msvcBridgeCommand(compiler, source, memoryObject, resourceObject, binary, includes, libraries, gui)
		}
		if e = buildCommand(a, env, cmd); e != nil {
			return e
		}
		commands = append(commands, cmd)
		libs, e := bridgeImports(binary, name, machine)
		if e != nil {
			return e
		}
		imports[name] = libs
		f, e := fileRecord(binary)
		if e != nil {
			return e
		}
		files[name] = f
	}
	if e = requireHash(source, before["sha256"]); e != nil {
		return e
	}
	if e = requireHash(memory, memoryHash); e != nil {
		return e
	}
	if e = requireHash(template, resourceHash); e != nil {
		return e
	}
	receipt := record{"schema_version": 1, "status": "bridge-compiled-not-runtime-accepted", "platform": target, "source_sha256": before["sha256"], "memory_source_sha256": memoryHash, "binaries": files, "imports": imports, "optimizer": "O2", "ob1_workaround": false, "freestanding_memory": []string{"memcmp", "memcpy", "memmove", "memset"}, "compile_commands": commands, "product_executed": false, "resource_sha256": resourceHash, "product_version": version, "resource_command": resourceCommand, "tests_executed": false, "installed": false, "release_eligible": false}
	if e = writeNewJSON(filepath.Join(out, "receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(receipt)
}
func buildWindowsMSIWorker(a *app, o options) error {
	if e := o.require("llvm-root", "platform", "version", "destination"); e != nil {
		return e
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(o.get("version")) {
		return fmt.Errorf("numeric product version required")
	}
	target := "x86_64-w64-mingw32"
	machine := uint16(0x8664)
	if o.get("platform") == "windows-arm64" {
		target = "aarch64-w64-mingw32"
		machine = 0xaa64
	} else if o.get("platform") != "windows-x64" {
		return fmt.Errorf("invalid worker target")
	}
	sdk, e := absolute(o.get("llvm-root"))
	if e != nil {
		return e
	}
	pin, e := load(a.path("packaging/git-terminal-runtime.lock.json"))
	if e != nil {
		return e
	}
	receipt, e := load(filepath.Join(sdk, "receipt.json"))
	if e != nil {
		return e
	}
	expected := object(pin, "source_archive")["sha256"]
	if object(receipt, "source")["sha256"] != expected {
		return fmt.Errorf("compiler SDK pin mismatch")
	}
	compiler, windres := filepath.Join(sdk, "bin", target+"-clang.exe"), filepath.Join(sdk, "bin", target+"-windres.exe")
	for _, p := range []string{compiler, windres} {
		rel, _ := filepath.Rel(sdk, p)
		if e = requireHash(p, object(receipt, "files")[filepath.ToSlash(rel)]); e != nil {
			return e
		}
	}
	out, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	source, template := a.path("native-core/launcher/windows-msi-worker.c"), a.path("native-core/launcher/windows-msi-worker.rc.in")
	sourceHash, e := digest(source)
	if e != nil {
		return e
	}
	resourceHash, e := digest(template)
	if e != nil {
		return e
	}
	text, e := read(template)
	if e != nil {
		return e
	}
	resource := filepath.Join(out, "worker.rc")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "@FILE_VERSION@", strings.ReplaceAll(o.get("version"), ".", ",")+",0"), "@PRODUCT_VERSION@", o.get("version"))
	if e = write(resource, []byte(text), 0644); e != nil {
		return e
	}
	env := append(os.Environ(), "PATH="+filepath.Join(sdk, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	obj := filepath.Join(out, "worker.o")
	if e = buildRun(a, a.root, env, windres, "-I", filepath.Join(sdk, target, "include"), resource, obj); e != nil {
		return e
	}
	binary := filepath.Join(out, "msi-worker.exe")
	if e = buildRun(a, a.root, env, compiler, "-O2", "-fno-stack-protector", "-ffreestanding", "-fno-builtin", "-nostdlib", "-Wall", "-Wextra", "-Werror", "-DUNICODE", "-D_UNICODE", source, obj, "-o", binary, "-Wl,--entry,worker_entry", "-Wl,--subsystem,windows", "-Wl,--dynamicbase", "-Wl,--nxcompat", "-Wl,--no-insert-timestamp", "-lkernel32"); e != nil {
		return e
	}
	f, e := pe.Open(binary)
	if e != nil {
		return e
	}
	data, e := os.ReadFile(binary)
	if e != nil {
		f.Close()
		return e
	}
	_, libraries, e := peDependencies(data)
	actualMachine := f.Machine
	f.Close()
	if e != nil {
		return e
	}
	if actualMachine != machine || len(libraries) != 1 || strings.ToLower(libraries[0]) != "kernel32.dll" {
		return fmt.Errorf("unexpected worker architecture/imports")
	}
	if e = requireHash(source, sourceHash); e != nil {
		return e
	}
	if e = requireHash(template, resourceHash); e != nil {
		return e
	}
	binaryHash, e := digest(binary)
	if e != nil {
		return e
	}
	record := record{"schema_version": 1, "product": "git-next-msi-worker", "platform": o.get("platform"), "version": o.get("version"), "source_sha256": sourceHash, "resource_sha256": resourceHash, "binary_sha256": binaryHash, "imports": libraries, "compiler_archive_sha256": expected, "executed": false}
	if e = writeNewJSON(filepath.Join(out, "receipt.json"), record); e != nil {
		return e
	}
	return a.json(record)
}

const freestandingLibcall = `
struct block { unsigned char bytes[4096]; };
struct block copy_block(struct block value) { return value; }
int check(void) {
    struct block value, copied;
    unsigned index;
    for (index = 0; index < 4096; index++) value.bytes[index] = (unsigned char)index;
    copied = copy_block(value);
    return copied.bytes[0] == 0 && copied.bytes[1] == 1 && copied.bytes[255] == 255
        && copied.bytes[4095] == (unsigned char)4095;
}
#if defined(__linux__) && defined(__x86_64__)
__attribute__((force_align_arg_pointer, noreturn)) void _start(void) {
    long status = check() ? 0 : 3;
    __asm__ volatile("syscall" : : "a"(60L), "D"(status) : "rcx", "r11", "memory");
    __builtin_unreachable();
}
#endif
`

func proofRun(a *app, work string, cmd ...string) (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, e := runProcess(ctx, work, nil, nil, cmd[0], cmd[1:]...)
	if e != nil {
		return r, e
	}
	if r.code != 0 {
		return r, fmt.Errorf("%s failed: %s%s", cmd[0], r.stdout, r.stderr)
	}
	return r, nil
}
func undefinedSymbols(a *app, work, p string) ([]string, error) {
	r, e := proofRun(a, work, "nm", p)
	if e != nil {
		return nil, e
	}
	names := []string{}
	for _, line := range strings.Split(string(r.stdout), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "U" {
			names = append(names, f[len(f)-1])
		}
	}
	sort.Strings(names)
	return names, nil
}
func verifyFreestandingBridge(a *app, o options) error {
	if o.get("receipt") != "" && exists(o.get("receipt")) {
		return fmt.Errorf("receipt destination must not exist")
	}
	sample := msvcBridgeCommand("cl.exe", "source.c", "memory.obj", "bridge.res", "bridge.exe", nil, nil, false)
	if e := rejectReducedInlining(sample); e != nil {
		return e
	}
	if rejectReducedInlining([]string{"/O2", "/Ob1"}) == nil {
		return fmt.Errorf("inline-limit guard did not fire")
	}
	if _, e := exec.LookPath("gcc"); e != nil {
		return a.json(record{"ok": true, "host_proof": "skipped", "reason": "gcc is not installed"})
	}
	work, e := os.MkdirTemp("", "git-next-freestanding-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(work)
	memory, driver, source := a.path("native-core/launcher/freestanding-memory.c"), a.path("tests/freestanding_memory_driver.c"), a.path("native-core/launcher/windows-bridge.c")
	helper := filepath.Join(work, "memory.o")
	if _, e = proofRun(a, work, "gcc", "-c", "-O2", "-ffreestanding", "-fno-builtin", "-fno-stack-protector", "-Wall", "-Wextra", "-Werror", "-std=c11", memory, "-o", helper); e != nil {
		return e
	}
	undefined, e := undefinedSymbols(a, work, helper)
	if e != nil {
		return e
	}
	for _, name := range undefined {
		if regexp.MustCompile(`^_*(memcpy|memset|memmove|memcmp)$`).MatchString(name) {
			return fmt.Errorf("memory helper calls itself: %s", name)
		}
	}
	driverBin := filepath.Join(work, "driver")
	if _, e = proofRun(a, work, "gcc", "-nostdlib", "-ffreestanding", "-O2", "-fno-stack-protector", "-Wall", "-Wextra", "-Werror", "-std=c11", driver, memory, "-o", driverBin); e != nil {
		return e
	}
	if _, e = proofRun(a, work, driverBin); e != nil {
		return e
	}
	libsource, libobj, libbin := filepath.Join(work, "libcall.c"), filepath.Join(work, "libcall.o"), filepath.Join(work, "libcall")
	if e = write(libsource, []byte(freestandingLibcall), 0644); e != nil {
		return e
	}
	if _, e = proofRun(a, work, "gcc", "-c", "-O2", "-mstringop-strategy=libcall", "-fno-stack-protector", "-std=c11", libsource, "-o", libobj); e != nil {
		return e
	}
	names, e := undefinedSymbols(a, work, libobj)
	if e != nil {
		return e
	}
	emitted := false
	for _, n := range names {
		emitted = emitted || strings.TrimLeft(n, "_") == "memcpy"
	}
	if !emitted {
		return fmt.Errorf("compiler probe did not emit external memcpy")
	}
	if _, e = proofRun(a, work, "gcc", "-nostdlib", "-O2", "-mstringop-strategy=libcall", "-fno-stack-protector", "-std=c11", libsource, memory, "-o", libbin); e != nil {
		return e
	}
	if _, e = proofRun(a, work, libbin); e != nil {
		return e
	}
	gcc, e := proofRun(a, work, "gcc", "--version")
	if e != nil {
		return e
	}
	host := record{"compiler_version": strings.Split(strings.TrimSpace(string(gcc.stdout)), "\n")[0], "helper_undefined": undefined, "driver_exit": 0, "compiler_libcall_exit": 0}
	var mingw any
	if compiler, e := exec.LookPath("x86_64-w64-mingw32-gcc"); e == nil {
		binaries, imports := record{}, record{}
		for _, gui := range []bool{false, true} {
			name := "git-next-bridge.exe"
			if gui {
				name = "git-next-bridge-gui.exe"
			}
			binary := filepath.Join(work, name)
			command := mingwBridgeCommand(compiler, source, memory, "", binary, gui)
			if e = rejectReducedInlining(command); e != nil {
				return e
			}
			if _, e = proofRun(a, a.root, command...); e != nil {
				return e
			}
			libs, e := bridgeImports(binary, name, 0x8664)
			if e != nil {
				return e
			}
			imports[name] = libs
			f, e := fileRecord(binary)
			if e != nil {
				return e
			}
			binaries[name] = f
		}
		v, e := proofRun(a, work, compiler, "--version")
		if e != nil {
			return e
		}
		mingw = record{"compiler": compiler, "compiler_version": strings.Split(strings.TrimSpace(string(v.stdout)), "\n")[0], "resource_linked": false, "binaries": binaries, "imports": imports, "product_executed": false}
	}
	sh, e := digest(source)
	if e != nil {
		return e
	}
	mh, e := digest(memory)
	if e != nil {
		return e
	}
	dh, e := digest(driver)
	if e != nil {
		return e
	}
	proof := record{"schema_version": 1, "status": "freestanding-bridge-proved-not-msvc-packaged", "decision": "full-O2-freestanding-memory", "rejected_alternative": "O2-plus-inline-limit", "optimizer": "O2", "ob1_workaround": false, "msvc_14_44_link_executed": false, "product_executed": false, "release_eligible": false, "bridge_source_sha256": sh, "memory_source_sha256": mh, "driver_source_sha256": dh, "host_proof": host, "mingw_x64": mingw}
	if o.get("receipt") != "" {
		if mingw == nil {
			return fmt.Errorf("MinGW x64 compiler is required before writing the bridge receipt")
		}
		if e = writeNewJSON(o.get("receipt"), proof); e != nil {
			return e
		}
	}
	return a.json(proof)
}
