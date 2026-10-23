// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	register("repair-windows-entry-points", "Restore verified Windows payload entries and remove self registrations", "root payload expected-sha256 plan? report", repairWindowsEntries)
	register("msi-lifecycle", "Exercise install/repair/upgrade/remove in a new isolated root", "package work receipt previous version skip-upgrade? natural-folder? install-dir plan? isolated-machine?", msiLifecycle)
	register("verify-windows-bridge-guard", "Prove self/cycle forwarding guards inside a bounded Windows job", "bridge json", verifyWindowsBridge)
}

type bridgeRegistration struct{ hive, path, bridge, platform, relative, root string }

func repairWindowsEntries(a *app, o options) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows entry-point repair requires Windows")
	}
	root := o.get("root")
	if root == "" {
		root = recordedInstallRoot("windows-x64")
	}
	if root == "" {
		root = filepath.Join(os.Getenv("ProgramFiles"), "Git")
	}
	root, e := absolute(root)
	if e != nil {
		return e
	}
	payload := o.value("payload", filepath.Join(root, "runtime/cmd/git.exe"))
	payload, e = absolute(payload)
	if e != nil {
		return e
	}
	report := record{"ok": false, "install_root": root, "payload": payload, "replaced": []record{}, "removed_registrations": []record{}, "problems": []string{}}
	var problems []string
	var replaced, removed []record
	finish := func() error {
		report["problems"] = problems
		report["replaced"] = replaced
		report["removed_registrations"] = removed
		report["ok"] = len(problems) == 0
		if o.get("report") != "" {
			if e := writeJSON(o.get("report"), report); e != nil {
				return e
			}
		}
		if e := a.json(report); e != nil {
			return e
		}
		if len(problems) > 0 {
			return fmt.Errorf("entry-point repair has %d problem(s)", len(problems))
		}
		return nil
	}
	hash, e := digest(payload)
	if e != nil {
		problems = append(problems, "payload is missing or unreadable")
		return finish()
	}
	report["payload_sha256"] = hash
	if o.get("expected-sha256") != "" && strings.ToLower(o.get("expected-sha256")) != hash {
		problems = append(problems, "payload digest does not match --expected-sha256")
		return finish()
	}
	for _, rel := range []string{"cmd/git.exe", "bin/git.exe"} {
		entry := filepath.Join(root, rel)
		before, e := digest(entry)
		if e != nil {
			problems = append(problems, "entry point is missing or unreadable: "+entry)
			continue
		}
		if before == hash {
			continue
		}
		after := "planned"
		if !o.yes("plan") {
			if e = copyFile(payload, entry); e != nil {
				problems = append(problems, "cannot replace entry; stop running Git processes first: "+entry)
				continue
			}
			actual, e := digest(entry)
			if e != nil || actual != hash {
				problems = append(problems, "entry still differs after copy: "+entry)
				continue
			}
			after = hash
		}
		replaced = append(replaced, record{"path": entry, "before": before, "after": after})
	}
	registrations, e := listBridgeRegistrations()
	if e != nil {
		return e
	}
	report["bridges_scanned"] = len(registrations)
	for _, r := range registrations {
		platformRoot := r.root
		if platformRoot == "" {
			platformRoot = root
		}
		target, e := absolute(filepath.Join(platformRoot, r.relative))
		if e != nil {
			return e
		}
		bridge, e := absolute(r.bridge)
		if e != nil {
			return e
		}
		if !strings.EqualFold(target, bridge) {
			continue
		}
		if !o.yes("plan") {
			if e = removeBridgeRegistration(r); e != nil {
				problems = append(problems, "cannot delete self registration: "+r.hive+" "+r.bridge)
				continue
			}
		}
		removed = append(removed, record{"hive": r.hive, "bridge": r.bridge, "relative": r.relative, "target": target})
	}
	return finish()
}
func msiProperties(packagePath string) (map[string]string, error) {
	db, e := openMSI(packagePath)
	if e != nil {
		return nil, e
	}
	defer closeMSI(db)
	rows, e := queryMSI(db, "SELECT `Property`, `Value` FROM `Property`")
	if e != nil {
		return nil, e
	}
	p := map[string]string{}
	for _, row := range rows {
		if len(row) == 2 {
			p[row[0]] = row[1]
		}
	}
	return p, nil
}
func actionLogs() record {
	root := filepath.Join(os.Getenv("ProgramData"), "git-next/logs")
	names, _ := filepath.Glob(filepath.Join(root, "msi-action-*.log"))
	found := record{}
	for _, name := range names {
		data, e := os.ReadFile(name)
		if e != nil {
			continue
		}
		if len(data) > 4000 {
			data = data[len(data)-4000:]
		}
		found[filepath.Base(name)] = string(data)
	}
	return found
}
func countFiles(root string) int {
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			n++
		}
		return e
	})
	return n
}
func installedState(a *app, root string) record {
	cmd := filepath.Join(root, "cmd/git.exe")
	size := int64(0)
	if st, e := os.Stat(cmd); e == nil {
		size = st.Size()
	}
	version := ""
	if size > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		r, e := runProcess(ctx, a.root, nil, nil, cmd, "--version")
		cancel()
		if e == nil && r.code == 0 {
			version = strings.TrimSpace(string(r.stdout))
		}
	}
	entries, _ := uninstallEntries("git-next")
	return record{"install_dir": root, "files": countFiles(root), "cmd_git_exists": size > 0, "cmd_git_bytes": size, "cmd_git_is_payload": size > 1000000, "bin_git_exists": exists(filepath.Join(root, "bin/git.exe")), "msi_worker_exists": exists(filepath.Join(root, "updater/msi-worker.exe")), "reported_version": version, "arp_entries": entries, "arp_count": len(entries)}
}
func isolatedLifecyclePaths(work, install, programFiles string, machine, natural, isolated bool) error {
	if !filepath.IsAbs(work) || !filepath.IsAbs(install) {
		return fmt.Errorf("lifecycle paths must be absolute")
	}
	if !natural && !inside(work, install) {
		return fmt.Errorf("installation must stay inside its isolated work root")
	}
	if natural && !isolated {
		return fmt.Errorf("--natural-folder requires --isolated-machine in a disposable machine")
	}
	if machine && !inside(programFiles, install) {
		return fmt.Errorf("machine-scope lifecycle root must stay inside Program Files")
	}
	if !isolated && strings.EqualFold(filepath.Clean(install), filepath.Join(programFiles, "Git")) {
		return fmt.Errorf("lifecycle cannot overwrite the workstation Git")
	}
	for _, p := range []string{work, install} {
		if resolved, e := filepath.EvalSymlinks(p); e == nil && !strings.EqualFold(resolved, p) {
			return fmt.Errorf("lifecycle path resolves through a link or alias: %s", p)
		}
	}
	return nil
}
func msiLifecycle(a *app, o options) error {
	if e := o.require("package", "work", "receipt"); e != nil {
		return e
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows Installer lifecycle requires Windows")
	}
	if exists(o.get("receipt")) {
		return fmt.Errorf("receipt must be new")
	}
	packagePath, e := absolute(o.get("package"))
	if e != nil {
		return e
	}
	p, e := msiProperties(packagePath)
	if e != nil {
		return e
	}
	code := strings.ToUpper(p["ProductCode"])
	if code == "" {
		return fmt.Errorf("MSI lacks ProductCode")
	}
	work, e := absolute(o.get("work"))
	if e != nil {
		return e
	}
	install, e := absolute(o.value("install-dir", filepath.Join(work, "install")))
	if e != nil {
		return e
	}
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	machine := p["ALLUSERS"] == "1"
	if e = isolatedLifecyclePaths(work, install, programFiles, machine, o.yes("natural-folder"), o.yes("isolated-machine")); e != nil {
		return e
	}
	if exists(work) {
		return fmt.Errorf("lifecycle work directory must be new; existing state is preserved")
	}
	if msiProductState(code) >= 0 {
		return fmt.Errorf("test MSI product is already registered; preserve existing installation")
	}
	if e = unoccupiedMSIFamily(p["UpgradeCode"], msiRelatedProducts); e != nil {
		return e
	}
	version := o.value("version", p["ProductVersion"])
	previousCode := ""
	if o.get("previous") != "" && !o.yes("skip-upgrade") {
		old, e := msiProperties(o.get("previous"))
		if e != nil {
			return e
		}
		previousCode = strings.ToUpper(old["ProductCode"])
		if previousCode == code || previousCode == "" || old["UpgradeCode"] != p["UpgradeCode"] {
			return fmt.Errorf("upgrade requires a distinct older product of the same family")
		}
		if msiProductState(previousCode) >= 0 {
			return fmt.Errorf("previous test MSI is already installed")
		}
	}
	report := record{"product": "git-next", "package": packagePath, "install_dir": install, "isolated": true, "product_code": code, "steps": []record{}, "ok": true, "plan": o.yes("plan")}
	if info, e := os.Stat(packagePath); e == nil {
		report["package_bytes"] = info.Size()
	} else {
		return e
	}
	if o.yes("plan") {
		if e = writeNewJSON(o.get("receipt"), report); e != nil {
			return e
		}
		return a.json(report)
	}
	if _, e = newDestination(work); e != nil {
		return e
	}
	home := filepath.Join(work, "profile")
	if e = os.Mkdir(home, 0700); e != nil {
		return e
	}
	logs := filepath.Join(work, "logs")
	if e = os.Mkdir(logs, 0700); e != nil {
		return e
	}
	env := cleanEnvironment(home)
	var steps []record
	defer func() {
		report["steps"] = steps
		report["action_logs"] = actionLogs()
		if e := writeNewJSON(o.get("receipt"), report); e != nil {
			fmt.Fprintln(a.err, "cannot write lifecycle receipt:", e)
		}
	}()
	step := func(name string, args []string, checks func() record) error {
		log := filepath.Join(logs, name+".log")
		args = append(args, "/qn", "/norestart", "REBOOT=ReallySuppress", "MSIRESTARTMANAGERCONTROL=Disable", "/l*v", log)
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		r, e := runProcess(ctx, a.root, env, nil, filepath.Join(os.Getenv("SystemRoot"), "System32/msiexec.exe"), args...)
		cancel()
		row := record{"step": name, "result": record{"command": args, "exit_code": r.code, "seconds": time.Since(started).Seconds(), "log": log}}
		if checks != nil {
			row["checks"] = checks()
		}
		steps = append(steps, row)
		good := e == nil && (r.code == 0 || r.code == 3010)
		for _, v := range object(row, "checks") {
			good = good && v == true
		}
		fmt.Fprintf(a.out, "%s exit=%d pass=%t\n", name, r.code, good)
		if !good {
			report["ok"] = false
			return fmt.Errorf("MSI lifecycle step failed: %s (see %s)", name, log)
		}
		return nil
	}
	installArgs := func(packageName string) []string {
		args := []string{"/i", packageName}
		if !o.yes("natural-folder") {
			args = append(args, "INSTALLFOLDER="+install)
		}
		return append(args, "REPLACEGIT=1")
	}
	var installed record
	if e = step("1-install", installArgs(packagePath), func() record {
		installed = installedState(a, install)
		report["installed"] = installed
		return record{"cmd_git_is_payload": truth(installed, "cmd_git_is_payload"), "bin_git_exists": truth(installed, "bin_git_exists"), "msi_worker_exists": truth(installed, "msi_worker_exists"), "reported_version_matches": strings.Contains(stringValue(installed, "reported_version"), version), "package_product_registered": object(installed, "arp_entries")[code] != nil}
	}); e != nil {
		return e
	}
	if e = step("2-repair", []string{"/famus", code}, func() record {
		now := installedState(a, install)
		return record{"cmd_git_is_payload": truth(now, "cmd_git_is_payload"), "files_unchanged": equalJSON(now["files"], installed["files"])}
	}); e != nil {
		return e
	}
	if previousCode != "" {
		if e = step("3a-remove", []string{"/x", code}, nil); e != nil {
			return e
		}
		if e = os.RemoveAll(install); e != nil {
			return e
		}
		if e = step("3b-install-previous", installArgs(o.get("previous")), func() record {
			now := installedState(a, install)
			report["pre_upgrade"] = now
			return record{"previous_product_registered": object(now, "arp_entries")[previousCode] != nil}
		}); e != nil {
			return e
		}
		if e = step("3c-upgrade", installArgs(packagePath), func() record {
			now := installedState(a, install)
			report["after_upgrade"] = now
			return record{"upgraded_product_registered": object(now, "arp_entries")[code] != nil, "previous_product_removed": object(now, "arp_entries")[previousCode] == nil, "reported_version_matches": strings.Contains(stringValue(now, "reported_version"), version), "cmd_git_is_payload": truth(now, "cmd_git_is_payload")}
		}); e != nil {
			return e
		}
	}
	if e = step("4-uninstall", []string{"/x", code}, func() record {
		entries, _ := uninstallEntries("git-next")
		report["after_uninstall"] = record{"install_dir_exists": exists(install), "files_left": countFiles(install), "arp_entries": entries}
		return record{"entry_points_removed": !exists(filepath.Join(install, "cmd/git.exe")) && !exists(filepath.Join(install, "bin/git.exe")), "arp_entry_removed": entries[code] == nil}
	}); e != nil {
		return e
	}
	return nil
}

func unoccupiedMSIFamily(upgrade string, query func(string) ([]string, error)) error {
	if upgrade == "" {
		return fmt.Errorf("MSI lacks UpgradeCode; cannot prove isolated lifecycle")
	}
	products, e := query(upgrade)
	if e != nil {
		return fmt.Errorf("cannot verify MSI upgrade family: %w", e)
	}
	if len(products) != 0 {
		return fmt.Errorf("MSI upgrade family is already registered; use a disposable Windows environment")
	}
	return nil
}
func verifyWindowsBridge(a *app, o options) error {
	if e := o.require("bridge"); e != nil {
		return e
	}
	return verifyBridgeScenarios(a, o)
}
