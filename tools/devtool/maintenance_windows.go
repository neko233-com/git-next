// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

const bridgeRegistryRoot = `Software\git-next\Bridges`
const msiRegistryRoot = `Software\git-next\MSI`

func recordedInstallRoot(platform string) string {
	k, e := registry.OpenKey(registry.LOCAL_MACHINE, msiRegistryRoot+`\`+platform, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if e != nil {
		return ""
	}
	defer k.Close()
	root, _, _ := k.GetStringValue("InstallRoot")
	return root
}
func listBridgeRegistrations() ([]bridgeRegistration, error) {
	var found []bridgeRegistration
	for _, h := range []struct {
		key  registry.Key
		name string
	}{{registry.LOCAL_MACHINE, "machine"}, {registry.CURRENT_USER, "user"}} {
		var walk func(string) error
		walk = func(path string) error {
			k, e := registry.OpenKey(h.key, path, registry.READ|registry.WOW64_64KEY)
			if e == registry.ErrNotExist {
				return nil
			}
			if e != nil {
				return e
			}
			defer k.Close()
			platform, _, _ := k.GetStringValue("Platform")
			relative, _, _ := k.GetStringValue("RelativePath")
			if platform != "" && relative != "" {
				found = append(found, bridgeRegistration{h.name, path, strings.TrimPrefix(path, bridgeRegistryRoot+`\`), platform, relative, recordedInstallRoot(platform)})
			}
			children, e := k.ReadSubKeyNames(-1)
			if e != nil {
				return e
			}
			for _, name := range children {
				if e = walk(path + `\` + name); e != nil {
					return e
				}
			}
			return nil
		}
		if e := walk(bridgeRegistryRoot); e != nil {
			return nil, e
		}
	}
	return found, nil
}
func removeBridgeRegistration(r bridgeRegistration) error {
	hive := registry.LOCAL_MACHINE
	if r.hive == "user" {
		hive = registry.CURRENT_USER
	}
	parent, leaf := filepath.Split(r.path)
	k, e := registry.OpenKey(hive, strings.TrimSuffix(parent, `\`), registry.ALL_ACCESS|registry.WOW64_64KEY)
	if e != nil {
		return e
	}
	defer k.Close()
	return registry.DeleteKey(k, leaf)
}
func uninstallEntries(hint string) (record, error) {
	found := record{}
	for _, h := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			k, e := registry.OpenKey(h, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.READ|view)
			if e == registry.ErrNotExist {
				continue
			}
			if e != nil {
				return nil, e
			}
			names, e := k.ReadSubKeyNames(-1)
			if e != nil {
				k.Close()
				return nil, e
			}
			for _, name := range names {
				if !strings.HasPrefix(name, "{") {
					continue
				}
				sub, e := registry.OpenKey(k, name, registry.QUERY_VALUE)
				if e != nil {
					continue
				}
				display, _, _ := sub.GetStringValue("DisplayName")
				if strings.Contains(strings.ToLower(display), strings.ToLower(hint)) {
					version, _, _ := sub.GetStringValue("DisplayVersion")
					location, _, _ := sub.GetStringValue("InstallLocation")
					found[strings.ToUpper(name)] = record{"display_name": display, "version": version, "location": location}
				}
				sub.Close()
			}
			k.Close()
		}
	}
	return found, nil
}
func msiProductState(code string) int32 {
	p, e := windows.UTF16PtrFromString(code)
	if e != nil {
		return -2
	}
	state, _, _ := installer.NewProc("MsiQueryProductStateW").Call(uintptr(unsafe.Pointer(p)))
	return int32(state)
}

func msiRelatedProducts(upgrade string) ([]string, error) {
	key, e := windows.UTF16PtrFromString(upgrade)
	if e != nil {
		return nil, e
	}
	proc := installer.NewProc("MsiEnumRelatedProductsW")
	var products []string
	for index := uintptr(0); index < 10000; index++ {
		var code [39]uint16
		status, _, _ := proc.Call(uintptr(unsafe.Pointer(key)), 0, index, uintptr(unsafe.Pointer(&code[0])))
		if status == 259 {
			return products, nil
		}
		if status != 0 {
			return nil, fmt.Errorf("MsiEnumRelatedProductsW failed: %d", status)
		}
		products = append(products, windows.UTF16ToString(code[:]))
	}
	return nil, fmt.Errorf("MSI upgrade family enumeration exceeded bound")
}

type jobAccounting struct {
	User, Kernel, PeriodUser, PeriodKernel int64
	Faults, Total, Active, Terminated      uint32
}

func runBridgeGuard(program string, args ...string) (int, uint32, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cleanup, e := prepareProcess(cmd)
	if e != nil {
		return -1, 0, 0, e
	}
	defer cleanup()
	start := time.Now()
	if e = cmd.Start(); e != nil {
		return -1, 0, 0, e
	}
	if e = attachProcess(cmd); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return -1, 0, 0, e
	}
	e = cmd.Wait()
	code := 0
	if e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			return -1, 0, 0, e
		}
	}
	value, ok := processJobs.Load(cmd)
	if !ok {
		return -1, 0, 0, fmt.Errorf("job accounting unavailable")
	}
	info := jobAccounting{}
	if e = windows.QueryInformationJobObject(value.(windows.Handle), 1, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); e != nil {
		return -1, 0, 0, e
	}
	return code, info.Total, time.Since(start), nil
}
func verifyBridgeScenarios(a *app, o options) (returnErr error) {
	bridge, e := absolute(o.get("bridge"))
	if e != nil {
		return e
	}
	sandbox, e := os.MkdirTemp("", "gn-bridge-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(sandbox)
	ptr, e := windows.UTF16PtrFromString(sandbox)
	if e != nil {
		return e
	}
	buffer := make([]uint16, 32768)
	if n, err := windows.GetLongPathName(ptr, &buffer[0], uint32(len(buffer))); err == nil && n < uint32(len(buffer)) {
		sandbox = windows.UTF16ToString(buffer[:n])
	}
	platform := ""
	for _, candidate := range []string{"windows-x64", "windows-arm64"} {
		existing, err := registry.OpenKey(registry.CURRENT_USER, msiRegistryRoot+`\`+candidate, registry.READ|registry.WOW64_64KEY)
		if err == registry.ErrNotExist {
			platform = candidate
			break
		}
		if err != nil {
			return err
		}
		existing.Close()
	}
	if platform == "" {
		return fmt.Errorf("bridge guard requires an isolated user: both platform registrations already exist")
	}
	msiKey := msiRegistryRoot + `\` + platform
	k, existed, e := registry.CreateKey(registry.CURRENT_USER, msiKey, registry.ALL_ACCESS|registry.WOW64_64KEY)
	if e != nil {
		return e
	}
	defer k.Close()
	if existed {
		return fmt.Errorf("fixture MSI registration became occupied; existing state is preserved")
	}
	n, typ, oldError := k.GetValue("InstallRoot", nil)
	var old []byte
	if oldError == nil {
		old = make([]byte, n)
		_, _, e = k.GetValue("InstallRoot", old)
		if e != nil {
			return e
		}
	} else if oldError != registry.ErrNotExist {
		return oldError
	}
	defer func() {
		var restoreErr error
		if oldError == nil {
			var data uintptr
			if len(old) > 0 {
				data = uintptr(unsafe.Pointer(&old[0]))
			}
			name, _ := windows.UTF16PtrFromString("InstallRoot")
			code, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW").Call(uintptr(k), uintptr(unsafe.Pointer(name)), 0, uintptr(typ), data, uintptr(len(old)))
			if code != 0 {
				restoreErr = fmt.Errorf("registry restore failed: %d", code)
			}
		} else {
			restoreErr = k.DeleteValue("InstallRoot")
		}
		if !existed {
			_ = k.Close()
			if e := registry.DeleteKey(registry.CURRENT_USER, msiKey); e != nil {
				restoreErr = e
			}
		}
		if restoreErr != nil {
			returnErr = restoreErr
		}
	}()
	if e = k.SetStringValue("InstallRoot", sandbox+`\`); e != nil {
		return e
	}
	var created []string
	defer func() {
		for _, name := range created {
			_ = registry.DeleteKey(registry.CURRENT_USER, bridgeRegistryRoot+`\`+name)
		}
	}()
	registerBridge := func(name, relative string) error {
		p := bridgeRegistryRoot + `\` + name
		node, existing, e := registry.CreateKey(registry.CURRENT_USER, p, registry.ALL_ACCESS|registry.WOW64_64KEY)
		if e != nil {
			return e
		}
		defer node.Close()
		if existing && !containsString(created, name) {
			return fmt.Errorf("unexpected preexisting fixture bridge registration")
		}
		if !existing {
			created = append(created, name)
		}
		if e = node.SetStringValue("Platform", platform); e != nil {
			return e
		}
		return node.SetStringValue("RelativePath", relative)
	}
	first := filepath.Join(sandbox, "first/git.exe")
	second := filepath.Join(sandbox, "second/git.exe")
	for _, p := range []string{first, second} {
		if e = copyFile(bridge, p); e != nil {
			return e
		}
		relative, e := filepath.Rel(sandbox, p)
		if e != nil {
			return e
		}
		if e = registerBridge(p, relative); e != nil {
			return e
		}
	}
	var rows []record
	run := func(name string, expected int, max uint32, args ...string) error {
		code, total, duration, e := runBridgeGuard(first, args...)
		if e != nil {
			return e
		}
		rows = append(rows, record{"scenario": name, "exit": code, "seconds": duration.Seconds(), "processes": total, "ok": code == expected && total <= max && total > 0})
		return nil
	}
	if e = run("self", 127, 1, "version"); e != nil {
		return e
	}
	r1, _ := filepath.Rel(sandbox, second)
	r2, _ := filepath.Rel(sandbox, first)
	if e = registerBridge(first, r1); e != nil {
		return e
	}
	if e = registerBridge(second, r2); e != nil {
		return e
	}
	if e = run("cycle", 127, 5, "version"); e != nil {
		return e
	}
	cmd := filepath.Join(os.Getenv("SystemRoot"), "System32/cmd.exe")
	target := filepath.Join(sandbox, "target/cmd.exe")
	if e = copyFile(cmd, target); e != nil {
		return e
	}
	if e = registerBridge(first, `target\cmd.exe`); e != nil {
		return e
	}
	if e = run("forward", 7, 2, "/c", "exit", "7"); e != nil {
		return e
	}
	deep := sandbox
	for len(filepath.Join(deep, "cmd.exe")) < 300 {
		deep = filepath.Join(deep, "directory-with-a-long-name-for-the-limit-")
	}
	if e = copyFile(cmd, filepath.Join(deep, "cmd.exe")); e != nil {
		return e
	}
	relative, e := filepath.Rel(sandbox, filepath.Join(deep, "cmd.exe"))
	if e != nil {
		return e
	}
	if e = registerBridge(first, relative); e != nil {
		return e
	}
	if e = run("longpath", 5, 2, "/c", "exit", "5"); e != nil {
		return e
	}
	ok := true
	for _, r := range rows {
		ok = ok && truth(r, "ok")
	}
	report := record{"ok": ok, "bridge": bridge, "process_limit": 64, "scenarios": rows, "isolated_registry_platform": platform}
	if o.get("json") != "" {
		if e = writeJSON(o.get("json"), report); e != nil {
			return e
		}
	}
	if e = a.json(report); e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("bridge guard scenario failed")
	}
	return nil
}
func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
