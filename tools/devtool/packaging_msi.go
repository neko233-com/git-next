// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var msiUpgrades = map[string]string{"windows-x64": "{981F8880-E13B-4D22-A36E-A1B166601663}", "windows-arm64": "{D21CC33B-CC02-41AF-95E4-3813F3F8E702}"}

func init() {
	register("native-msi", "Compile source-pinned full native MSI without product execution", "distribution wix output-dir source-commit scope allow-experimental-local?", nativeMSI)
	register("build-msi", "Compile source-pinned Rust/native MSI without publishing", "distribution binary-dir build-receipt wix platform output-dir source-commit scope allow-experimental-local?", buildMSI)
}
func uuid5MSI(name string) string {
	namespace, _ := hex.DecodeString("57e363d45f824cd9bd55b37566f07458")
	h := sha1.New()
	h.Write(namespace)
	h.Write([]byte(name))
	b := h.Sum(nil)[:16]
	b[6] = b[6]&15 | 80
	b[8] = b[8]&63 | 128
	return strings.ToUpper(fmt.Sprintf("{%x-%x-%x-%x-%x}", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
}

type wixNode struct {
	name     string
	attrs    map[string]string
	children []*wixNode
}

func wn(name string, attrs map[string]string, children ...*wixNode) *wixNode {
	return &wixNode{name, attrs, children}
}
func (n *wixNode) add(name string, attrs map[string]string) *wixNode {
	child := wn(name, attrs)
	n.children = append(n.children, child)
	return child
}
func (n *wixNode) encode(e *xml.Encoder) error {
	start := xml.StartElement{Name: xml.Name{Local: n.name}}
	var keys []string
	for k := range n.attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: k}, Value: n.attrs[k]})
	}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, c := range n.children {
		if err := c.encode(e); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}
func writeWix(p string, n *wixNode) error {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	e := xml.NewEncoder(&b)
	e.Indent("", "  ")
	if err := n.encode(e); err != nil {
		return err
	}
	if err := e.Flush(); err != nil {
		return err
	}
	b.WriteByte('\n')
	return write(p, b.Bytes(), 0644)
}
func shortID(prefix, value string) string {
	h := sha256.Sum256([]byte(value))
	return prefix + hex.EncodeToString(h[:])[:24]
}
func msiVersion(version string) (string, bool, error) {
	m := regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([A-Za-z0-9.-]+))?$`).FindStringSubmatch(version)
	if m == nil {
		return "", false, fmt.Errorf("invalid native package version")
	}
	for i, limit := range []int{255, 255, 65535} {
		n, e := strconv.Atoi(m[i+1])
		if e != nil || n > limit {
			return "", false, fmt.Errorf("version exceeds Windows Installer limits")
		}
	}
	return strings.Join(m[1:4], "."), m[4] != "", nil
}
func checkedMSIFiles(root string, manifest record) (map[string]string, error) {
	inventory := object(manifest, "files")
	if len(inventory) > 60000 {
		return nil, fmt.Errorf("MSI file count exceeded")
	}
	files := map[string]string{}
	folded := map[string]bool{}
	for name, v := range inventory {
		fold := strings.ToLower(name)
		if folded[fold] {
			return nil, fmt.Errorf("case-colliding distribution file")
		}
		folded[fold] = true
		p, e := confined(root, name)
		if e != nil {
			return nil, e
		}
		st, e := os.Lstat(p)
		if e != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("missing/nonregular MSI payload: %s", name)
		}
		if e = shaMatches(p, rec(v)); e != nil {
			return nil, e
		}
		files[name] = p
	}
	return files, nil
}

func payloadMSIFeatures(fragment, group *wixNode) error {
	const bucketCount, componentLimit = 64, 1600
	buckets := make([][]string, bucketCount)
	for _, component := range group.children {
		id := component.attrs["Id"]
		hash := sha256.Sum256([]byte(id))
		bucket := int(hash[0]) & (bucketCount - 1)
		buckets[bucket] = append(buckets[bucket], id)
	}
	featureGroup := fragment.add("FeatureGroup", map[string]string{"Id": "PayloadFeatures"})
	for bucket, ids := range buckets {
		if len(ids) > componentLimit {
			return fmt.Errorf("MSI payload feature bucket %02x exceeds %d components: %d", bucket, componentLimit, len(ids))
		}
		if len(ids) == 0 {
			continue
		}
		sort.Strings(ids)
		// Fixed buckets preserve feature membership when later releases add files.
		feature := featureGroup.add("Feature", map[string]string{"Id": fmt.Sprintf("PayloadFeature_%02x", bucket), "Level": "1", "Display": "hidden", "InstallDefault": "followParent"})
		for _, id := range ids {
			feature.add("ComponentRef", map[string]string{"Id": id})
		}
	}
	return nil
}

func payloadWix(files map[string]string, manifest record, platform, version, scope, suffix string, legacy bool) (*wixNode, error) {
	root := wn("Wix", map[string]string{"xmlns": "http://wixtoolset.org/schemas/v4/wxs"})
	fragment := root.add("Fragment", nil)
	ref := fragment.add("DirectoryRef", map[string]string{"Id": "INSTALLFOLDER"})
	group := fragment.add("ComponentGroup", map[string]string{"Id": "Payload"})
	dirs := map[string]struct {
		id string
		n  *wixNode
	}{".": {"INSTALLFOLDER", ref}}
	var directory func(string) (string, error)
	directory = func(name string) (string, error) {
		if v, ok := dirs[name]; ok {
			return v.id, nil
		}
		parent, e := directory(path.Dir(name))
		if e != nil {
			return "", e
		}
		_ = parent
		node := dirs[path.Dir(name)].n
		id := shortID("Dir_", name)
		child := node.add("Directory", map[string]string{"Id": id, "Name": path.Base(name)})
		dirs[name] = struct {
			id string
			n  *wixNode
		}{id, child}
		return id, nil
	}
	hive := "HKLM"
	if scope == "user" {
		hive = "HKCU"
	}
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		parent := path.Dir(name)
		dir := ""
		if parent == "bin" {
			dir = "BINDIR"
		} else if parent == "updater" {
			dir = "UPDATERDIR"
		} else if legacy && parent == "preview/bin" {
			dir = "PREVIEWBINDIR"
		} else {
			var e error
			dir, e = directory(parent)
			if e != nil {
				return nil, e
			}
		}
		id := shortID("Payload_", name)
		component := group.add("Component", map[string]string{"Id": id, "Guid": uuid5MSI("component/" + platform + "/" + name + suffix), "Directory": dir})
		component.add("RegistryValue", map[string]string{"Root": hive, "Key": "Software\\git-next\\MSI\\" + platform + "\\Files", "Name": name, "Type": "string", "Value": version, "KeyPath": "yes"})
		attrs := map[string]string{"Id": id + "_File", "Source": files[name], "Name": path.Base(name)}
		if !legacy {
			f, e := os.Open(files[name])
			if e != nil {
				return nil, e
			}
			magic := make([]byte, 2)
			_, _ = f.Read(magic)
			f.Close()
			if string(magic) != "MZ" {
				attrs["CompanionFile"] = shortID("Payload_", "bin/git.exe") + "_File"
			}
			if number(rec(object(manifest, "files")[name]), "windows_attributes")&4 != 0 {
				attrs["System"] = "yes"
			}
		}
		file := component.add("File", attrs)
		if !legacy && scope == "machine" {
			file.add("PermissionEx", map[string]string{"Sddl": "O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;BU)"})
		}
		if !legacy && (name == "cmd/git.exe" || name == "bin/git.exe" || name == "mingw64/bin/git.exe") {
			replacement := group.add("Component", map[string]string{"Id": shortID("ReplaceEntry_", name), "Guid": uuid5MSI("replace/" + platform + "/" + name + suffix), "Directory": dir})
			replacement.add("RegistryValue", map[string]string{"Root": hive, "Key": "Software\\git-next\\MSI\\" + platform + "\\Replace", "Name": name, "Type": "string", "Value": version, "KeyPath": "yes"})
			replacement.add("RemoveFile", map[string]string{"Id": shortID("Remove_", name), "Name": path.Base(name), "On": "install"})
		}
	}
	if !legacy {
		var names []string
		for name := range dirs {
			if name != "." {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			id := shortID("Remove_", name)
			component := group.add("Component", map[string]string{"Id": id, "Directory": dirs[name].id, "Guid": uuid5MSI("directory/" + platform + "/" + name + suffix)})
			component.add("RegistryValue", map[string]string{"Root": hive, "Key": "Software\\git-next\\MSI\\" + platform + "\\Directories", "Name": name, "Type": "string", "Value": version, "KeyPath": "yes"})
			component.add("RemoveFolder", map[string]string{"Id": id + "_Folder", "Directory": dirs[name].id, "On": "uninstall"})
		}
	}
	if e := payloadMSIFeatures(fragment, group); e != nil {
		return nil, e
	}
	return root, nil
}

func checkMSIPayloadTables(files, components, features, membership, sequence [][]string, expectedFiles int) error {
	if len(files) != expectedFiles {
		return fmt.Errorf("compiled MSI File table has %d rows; expected %d payload files", len(files), expectedFiles)
	}
	featureIDs := map[string]bool{}
	for _, row := range features {
		if len(row) < 2 || row[0] == "" || featureIDs[row[0]] || row[0] != "ProductFeature" && (!strings.HasPrefix(row[0], "PayloadFeature_") || row[1] != "ProductFeature") {
			return fmt.Errorf("compiled MSI payload feature hierarchy differs")
		}
		featureIDs[row[0]] = true
	}
	if !featureIDs["ProductFeature"] || len(featureIDs) < 2 || len(featureIDs) > 65 {
		return fmt.Errorf("compiled MSI payload features missing or excessive")
	}
	componentIDs := map[string]bool{}
	for _, row := range components {
		if len(row) == 0 || row[0] == "" || componentIDs[row[0]] {
			return fmt.Errorf("compiled MSI component identity invalid")
		}
		componentIDs[row[0]] = true
	}
	assigned := map[string]bool{}
	counts := map[string]int{}
	for _, row := range membership {
		if len(row) < 2 || !featureIDs[row[0]] || !componentIDs[row[1]] || assigned[row[1]] {
			return fmt.Errorf("compiled MSI component must belong to exactly one feature")
		}
		assigned[row[1]] = true
		counts[row[0]]++
		if counts[row[0]] > 1600 {
			return fmt.Errorf("compiled MSI feature %s exceeds 1600 components", row[0])
		}
	}
	if len(assigned) != len(componentIDs) {
		return fmt.Errorf("compiled MSI has unassigned components")
	}
	actions := map[string]int{}
	for _, row := range sequence {
		if len(row) < 2 {
			return fmt.Errorf("compiled MSI sequence invalid")
		}
		value, e := strconv.Atoi(row[1])
		if e != nil {
			return fmt.Errorf("compiled MSI sequence invalid: %w", e)
		}
		actions[row[0]] = value
	}
	if actions["InstallInitialize"] <= 0 || actions["InstallExecute"] <= actions["InstallInitialize"] || actions["RemoveExistingProducts"] <= actions["InstallExecute"] || actions["InstallFinalize"] <= actions["RemoveExistingProducts"] {
		return fmt.Errorf("compiled MSI must remove the old product after InstallExecute and before InstallFinalize")
	}
	return nil
}

func checkCompiledMSIPayload(path string, expectedFiles int) error {
	database, e := openMSI(path)
	if e != nil {
		return e
	}
	defer closeMSI(database)
	queries := []string{"SELECT `File` FROM `File`", "SELECT `Component` FROM `Component`", "SELECT `Feature`, `Feature_Parent` FROM `Feature`", "SELECT `Feature_`, `Component_` FROM `FeatureComponents`", "SELECT `Action`, `Sequence` FROM `InstallExecuteSequence`"}
	tables := make([][][]string, len(queries))
	for i, query := range queries {
		tables[i], e = queryMSI(database, query)
		if e != nil {
			return e
		}
	}
	return checkMSIPayloadTables(tables[0], tables[1], tables[2], tables[3], tables[4], expectedFiles)
}

func compileMSI(a *app, o options, manifest record, files map[string]string, pkg record, packaging map[string]string, legacy bool) (record, error) {
	platform, version := stringValue(pkg, "platform"), stringValue(pkg, "version")
	scope := stringValue(pkg, "install_scope")
	if scope == "" {
		scope = "user"
	}
	wix, e := absolute(o.get("wix"))
	if e != nil {
		return nil, e
	}
	environment := append(os.Environ(), "DOTNET_ROLL_FORWARD=Major")
	b, e := commandAt(a.root, environment, wix, "--version")
	if e != nil {
		return nil, e
	}
	toolVersion := strings.TrimSpace(string(b))
	if !strings.HasPrefix(toolVersion, "5.0.2+") {
		return nil, fmt.Errorf("pinned WiX 5.0.2 required")
	}
	output := o.value("output-dir", a.path("dist/staged-native"))
	if legacy {
		output = o.value("output-dir", a.path("dist/staged"))
	}
	if e = os.MkdirAll(output, 0755); e != nil {
		return nil, e
	}
	asset := "git-next-" + version + "-" + platform
	if !legacy && scope == "user" {
		asset += "-user"
	}
	asset += ".msi"
	destination := filepath.Join(output, asset)
	receiptPath := strings.TrimSuffix(destination, ".msi") + ".receipt.json"
	if exists(destination) || exists(receiptPath) {
		return nil, fmt.Errorf("refusing MSI/receipt overwrite")
	}
	stage, e := os.MkdirTemp("", "git-next-msi-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(stage)
	if e = writeNewJSON(filepath.Join(stage, "package.json"), pkg); e != nil {
		return nil, e
	}
	inputHashes := map[string]string{}
	for name, p := range files {
		h, e := digest(p)
		if e != nil {
			return nil, e
		}
		inputHashes[name] = h
	}
	files["package.json"] = filepath.Join(stage, "package.json")
	suffix := ""
	if !legacy {
		if scope == "machine" {
			suffix = "/machine"
		}
		suffix += "/git-default-v2"
	}
	node, e := payloadWix(files, manifest, platform, version, scope, suffix, legacy)
	if e != nil {
		return nil, e
	}
	if e = writeWix(filepath.Join(stage, "Payload.wxs"), node); e != nil {
		return nil, e
	}
	terminal := "bin/git-terminal.exe"
	if files[terminal] == "" {
		terminal = "runtime/git-bash.exe"
	}
	if files[terminal] == "" {
		return nil, fmt.Errorf("owned terminal launcher missing")
	}
	arch := "x64"
	if platform == "windows-arm64" {
		arch = "arm64"
	}
	defines := map[string]string{"MsiVersion": stringValue(pkg, "msi_version"), "ProductCode": stringValue(pkg, "product_code"), "UpgradeCode": stringValue(pkg, "upgrade_code"), "InstallScope": scope, "Platform": platform, "TerminalRelativePath": strings.ReplaceAll(terminal, "/", "\\"), "TerminalArguments": "", "TerminalHasArguments": "no"}
	if scope == "machine" {
		defines["PackageScope"] = "perMachine"
		defines["RegistryHive"] = "HKLM"
		defines["PathSystem"] = "yes"
		defines["TaskImpersonate"] = "no"
	} else {
		defines["PackageScope"] = "perUser"
		defines["RegistryHive"] = "HKCU"
		defines["PathSystem"] = "no"
		defines["TaskImpersonate"] = "yes"
	}
	if terminal == "bin/git-terminal.exe" {
		defines["TerminalArguments"] = "--cd-to-home"
		defines["TerminalHasArguments"] = "yes"
	}
	description := "git-next complete native runtime; unpublished candidate; takeover blocked"
	if truth(pkg, "complete_git_parity") {
		description = "git-next complete native runtime; accepted release"
	}
	if legacy {
		description = "git-next native client; experimental preview; Git replacement blocked"
		if truth(pkg, "complete_git_parity") {
			description = "git-next native client; accepted release"
		}
	}
	defines["ProductDescription"] = description
	args := []string{"build", a.path("packaging/windows/Product.wxs"), filepath.Join(stage, "Payload.wxs"), "-arch", arch, "-pdbtype", "none", "-intermediatefolder", filepath.Join(stage, "obj"), "-o", filepath.Join(stage, asset)}
	keys := make([]string, 0, len(defines))
	for key := range defines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-d", key+"="+defines[key])
	}
	if _, e = commandAt(a.root, environment, wix, args...); e != nil {
		return nil, e
	}
	if e = checkCompiledMSIPayload(filepath.Join(stage, asset), len(files)); e != nil {
		return nil, e
	}
	for name, h := range inputHashes {
		actual, e := digest(files[name])
		if e != nil || actual != h {
			return nil, fmt.Errorf("MSI input changed during compilation: %s", name)
		}
	}
	for name, h := range packaging {
		actual, e := digest(a.path(filepath.FromSlash(name)))
		if e != nil || actual != h {
			return nil, fmt.Errorf("MSI recipe changed during compilation")
		}
	}
	if legacy {
		current, e := rustSourceHash(a)
		if e != nil {
			return nil, e
		}
		if current != pkg["source_tree_sha256"] {
			return nil, fmt.Errorf("Rust source changed during MSI compilation")
		}
	}
	if e = copyFile(filepath.Join(stage, asset), destination); e != nil {
		return nil, e
	}
	h, e := digest(destination)
	if e != nil {
		return nil, e
	}
	st, e := os.Stat(destination)
	if e != nil {
		return nil, e
	}
	result := record{"status": "built-not-accepted", "asset": asset, "sha256": h, "size": st.Size(), "version": version, "platform": platform, "product_code": pkg["product_code"], "upgrade_code": pkg["upgrade_code"], "install_scope": scope, "msi_version": pkg["msi_version"], "source_tree_sha256": pkg["source_tree_sha256"], "packaging_files": packaging, "complete_git_parity": pkg["complete_git_parity"], "release_eligible": pkg["release_eligible"], "wix_version": toolVersion, "product_executed": false, "tests_executed": false, "installation_runtime_accepted": false, "payload_files": len(files)}
	if !legacy {
		result["distribution_manifest_sha256"] = inputHashes["git-next-distribution.json"]
	} else {
		h, e := digest(o.get("build-receipt"))
		if e != nil {
			return nil, e
		}
		result["native_build_receipt_sha256"] = h
		result["source_commit"] = pkg["source_commit"]
	}
	if e = writeNewJSON(receiptPath, result); e != nil {
		return nil, e
	}
	return result, nil
}
func nativeMSI(a *app, o options) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("native MSI compilation requires Windows")
	}
	if e := o.require("distribution", "wix"); e != nil {
		return e
	}
	root, e := absolute(o.get("distribution"))
	if e != nil {
		return e
	}
	manifest, e := load(filepath.Join(root, "git-next-distribution.json"))
	if e != nil {
		return e
	}
	if number(manifest, "schema_version") != 1 || manifest["product"] != "git-next" || manifest["engine"] != "source-built-git-next-native-core" || manifest["upstream_git_core_deployed"] != false {
		return fmt.Errorf("complete owned native distribution manifest required")
	}
	platform, version := stringValue(manifest, "platform"), stringValue(manifest, "version")
	if msiUpgrades[platform] == "" {
		return fmt.Errorf("unsupported MSI platform")
	}
	numeric, pre, e := msiVersion(version)
	if e != nil {
		return e
	}
	if !o.yes("allow-experimental-local") && (!truth(manifest, "complete_git_parity") || !truth(manifest, "release_eligible") || pre || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(o.get("source-commit"))) {
		return fmt.Errorf("release MSI requires accepted complete distribution, stable version and immutable commit")
	}
	scope := o.value("scope", "machine")
	if scope != "machine" && scope != "user" {
		return fmt.Errorf("MSI scope must be user or machine")
	}
	files, e := checkedMSIFiles(root, manifest)
	if e != nil {
		return e
	}
	for _, name := range []string{"bin/git.exe", "bin/git-next.exe", "cmd/git.exe", "runtime/etc/gitconfig", "updater/update.ps1", "updater/Update.Core.psm1", "updater/msi-worker.exe", "components/install-tls-provider.ps1", "components/tls-providers.lock.json", "LICENSE", "updater/Runtime.Configuration.ps1", "runtime/defaults/manifest.json", "migration/takeover.ps1", "migration/Takeover.Core.psm1", "migration/git-next-bridge.exe", "migration/git-next-bridge-gui.exe"} {
		if files[name] == "" {
			return fmt.Errorf("complete MSI payload missing: %s", name)
		}
	}
	if !o.yes("allow-experimental-local") && (files["bin/git-terminal.exe"] == "" || object(manifest, "git_terminal")["renderer"] != "gpui-fast") {
		return fmt.Errorf("Windows releases require peer Rust GPUI Fast terminal")
	}
	for _, v := range []struct {
		folder string
		names  []string
	}{{"updater", []string{"update.ps1", "Update.Core.psm1", "Scope.Core.ps1", "Scope.Native.cs", "Runtime.Configuration.ps1", "messages.zh-CN.json"}}, {"migration", []string{"takeover.ps1", "Takeover.Core.psm1"}}, {"components", []string{"install-tls-provider.ps1"}}} {
		for _, name := range v.names {
			h, e := digest(files[v.folder+"/"+name])
			source, se := digest(a.path("packaging/windows", name))
			if e != nil || se != nil || h != source {
				return fmt.Errorf("distribution/MSI integration differs: %s", name)
			}
		}
	}
	expected := uint16(peMachine(platform))
	for _, v := range array(manifest, "core_entrypoints") {
		machine, _, e := peInfo(files[v.(string)])
		if e != nil || machine != expected {
			return fmt.Errorf("native entrypoint machine mismatch")
		}
	}
	suffix := "/git-default-v2"
	upgrade := msiUpgrades[platform]
	if scope == "machine" {
		suffix = "/machine" + suffix
		upgrade = uuid5MSI("upgrade/" + platform + "/machine")
	}
	pkg := record{"schema_version": 1, "product": "git-next", "install_kind": "msi", "install_scope": scope, "engine": manifest["engine"], "version": version, "platform": platform, "msi_version": numeric, "product_code": uuid5MSI(version + "/" + platform + suffix), "upgrade_code": upgrade, "source_commit": o.get("source-commit"), "installation_layout": "git-default-v2", "source_tree_sha256": manifest["source_tree_sha256"], "complete_git_parity": manifest["complete_git_parity"], "builtins": manifest["builtins"], "system_git_replaced": false, "release_eligible": !o.yes("allow-experimental-local")}
	hashes := record{}
	for name, v := range object(manifest, "files") {
		hashes[name] = rec(v)["sha256"]
	}
	mh, e := digest(filepath.Join(root, "git-next-distribution.json"))
	if e != nil {
		return e
	}
	hashes["git-next-distribution.json"] = mh
	pkg["files"] = hashes
	files["git-next-distribution.json"] = filepath.Join(root, "git-next-distribution.json")
	packaging := map[string]string{}
	for _, name := range []string{"tools/devtool/packaging_msi.go", "packaging/windows/Product.wxs"} {
		h, e := digest(a.path(filepath.FromSlash(name)))
		if e != nil {
			return e
		}
		packaging[name] = h
	}
	result, e := compileMSI(a, o, manifest, files, pkg, packaging, false)
	if e != nil {
		return e
	}
	return a.json(result)
}
func peMachine(platform string) uint16 {
	if platform == "windows-arm64" {
		return 0xaa64
	}
	return 0x8664
}
func rustSourceHash(a *app) (string, error) {
	var paths []string
	for _, name := range []string{"Cargo.toml", "Cargo.lock", "compatibility.json", "docs/COMPATIBILITY.md"} {
		paths = append(paths, a.path(filepath.FromSlash(name)))
	}
	for _, folder := range []string{"src", "vendor"} {
		e := filepath.WalkDir(a.path(folder), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() && (filepath.Ext(p) == ".rs" || folder == "vendor" && d.Name() == "Cargo.toml") {
				paths = append(paths, p)
			}
			return nil
		})
		if e != nil {
			return "", e
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		relative, _ := filepath.Rel(a.root, p)
		b, e := os.ReadFile(p)
		if e != nil {
			return "", e
		}
		h.Write([]byte(filepath.ToSlash(relative) + "\x00"))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func buildMSI(a *app, o options) error {
	if o.get("distribution") != "" {
		return nativeMSI(a, o)
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("local MSI compilation requires Windows")
	}
	if e := o.require("binary-dir", "build-receipt", "wix"); e != nil {
		return e
	}
	before, e := rustSourceHash(a)
	if e != nil {
		return e
	}
	receipt, e := load(o.get("build-receipt"))
	if e != nil {
		return e
	}
	if receipt["status"] != "pass" || receipt["source_tree_sha256"] != before {
		return fmt.Errorf("native build receipt differs from current Rust inputs")
	}
	version, e := cargoVersion(a)
	if e != nil {
		return e
	}
	numeric, pre, e := msiVersion(version)
	if e != nil {
		return e
	}
	compat, e := load(a.path("compatibility.json"))
	if e != nil {
		return e
	}
	if !o.yes("allow-experimental-local") && (!truth(compat, "complete_git_parity") || pre || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(o.get("source-commit"))) {
		return fmt.Errorf("release MSI requires complete parity, stable version and immutable commit")
	}
	platform := o.value("platform", "windows-x64")
	if msiUpgrades[platform] == "" {
		return fmt.Errorf("unsupported MSI platform")
	}
	root := o.get("binary-dir")
	for _, name := range []string{"git-next.exe", "git.exe", "git-terminal.exe", "git-lfs.exe", "git-next-lfs-filter.exe"} {
		p := filepath.Join(root, name)
		if e = shaMatches(p, record{"sha256": object(receipt, "binaries")[name]}); e != nil {
			return e
		}
		machine, _, e := peInfo(p)
		if e != nil || machine != peMachine(platform) {
			return fmt.Errorf("binary architecture mismatch")
		}
	}
	if platform == "windows-x64" {
		raw, e := a.mustRun(filepath.Join(root, "git-next.exe"), "version", "--json")
		if e != nil {
			return e
		}
		var identity record
		if e = json.Unmarshal(raw, &identity); e != nil {
			return e
		}
		if identity["product"] != "git-next" || identity["version"] != version || identity["external_git_fallback"] != false || identity["external_ssh_fallback"] != false {
			return fmt.Errorf("unrecognized native Rust client")
		}
	}
	files := map[string]string{"bin/git-next.exe": filepath.Join(root, "git-next.exe"), "bin/git-terminal.exe": filepath.Join(root, "git-terminal.exe"), "bin/git-next-lfs.exe": filepath.Join(root, "git-lfs.exe"), "bin/git-next-lfs-filter.exe": filepath.Join(root, "git-next-lfs-filter.exe"), "preview/bin/git.exe": filepath.Join(root, "git-next.exe"), "preview/bin/git-lfs.exe": filepath.Join(root, "git-lfs.exe"), "preview/bin/git-next-lfs-filter.exe": filepath.Join(root, "git-next-lfs-filter.exe"), "LICENSE": a.path("LICENSE")}
	for _, name := range []string{"update.ps1", "Update.Core.psm1", "Scope.Core.ps1", "Scope.Native.cs", "Runtime.Configuration.ps1", "messages.zh-CN.json"} {
		files["updater/"+name] = a.path("packaging/windows", name)
	}
	hashes := record{}
	for name, p := range files {
		if name == "LICENSE" {
			continue
		}
		h, e := digest(p)
		if e != nil {
			return e
		}
		hashes[name] = h
	}
	pkg := record{"schema_version": 1, "product": "git-next", "install_kind": "msi", "install_scope": "user", "version": version, "platform": platform, "msi_version": numeric, "product_code": uuid5MSI(version + "/" + platform), "upgrade_code": msiUpgrades[platform], "source_commit": o.get("source-commit"), "source_tree_sha256": before, "complete_git_parity": compat["complete_git_parity"], "system_git_replaced": false, "release_eligible": !o.yes("allow-experimental-local"), "files": hashes}
	packaging := map[string]string{}
	e = filepath.WalkDir(a.path("packaging"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(p) {
		case ".ps1", ".psm1", ".wxs", ".json":
			relative, _ := filepath.Rel(a.root, p)
			h, e := digest(p)
			if e != nil {
				return e
			}
			packaging[filepath.ToSlash(relative)] = h
		}
		return nil
	})
	if e != nil {
		return e
	}
	h, e := digest(a.path("tools/devtool/packaging_msi.go"))
	if e != nil {
		return e
	}
	packaging["tools/devtool/packaging_msi.go"] = h
	result, e := compileMSI(a, o, nil, files, pkg, packaging, true)
	if e != nil {
		return e
	}
	after, e := rustSourceHash(a)
	if e != nil || after != before {
		return fmt.Errorf("Rust source changed during MSI compilation")
	}
	return a.json(result)
}
