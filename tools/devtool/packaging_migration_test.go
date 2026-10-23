// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packagingOptions(values map[string]string, flags ...string) options {
	o := options{strings: map[string]*string{}, bools: map[string]*bool{}}
	for k, v := range values {
		value := v
		o.strings[k] = &value
	}
	for _, key := range flags {
		value := true
		o.bools[key] = &value
	}
	return o
}
func TestMSIIdentitiesMatchOriginalUUID5(t *testing.T) {
	oracle := map[string]string{"upgrade/windows-x64/machine": "{8C7712E8-6254-59D7-9AF9-5E5488927D43}", "1.0.2/windows-x64/machine/git-default-v2": "{DBF3A8C3-9D63-55E1-B8F8-2EA61738C8DC}", "1.0.2/windows-x64/git-default-v2": "{5ADE1057-E334-566F-8C63-CE7935D47483}", "component/windows-arm64/bin/git.exe/machine/git-default-v2": "{B002EBED-9B06-58D1-A765-5E74DC36E7CE}", "replace/windows-x64/cmd/git.exe/machine/git-default-v2": "{67B76F82-6481-508C-A385-61227ECA20CA}", "directory/windows-x64/runtime/etc/machine/git-default-v2": "{63A0DB55-1B48-5B41-B169-B2BB4F00FD98}"}
	for name, want := range oracle {
		if got := uuid5MSI(name); got != want {
			t.Fatalf("%s: %s != %s", name, got, want)
		}
	}
	for _, version := range []string{"256.0.0", "1.256.0", "1.2.65536", "01.2.3", "1.2"} {
		if _, _, e := msiVersion(version); e == nil {
			t.Fatalf("accepted invalid version %s", version)
		}
	}
}
func TestMSIPayloadRetainsRepairAndACLContracts(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	manifest := record{"files": record{}}
	for _, name := range []string{"bin/git.exe", "cmd/git.exe", "mingw64/bin/git.exe", "runtime/etc/gitconfig", "runtime/defaults/manifest.json", "updater/update.ps1"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		data := []byte("data")
		if strings.HasSuffix(name, ".exe") {
			data = []byte("MZcore")
		}
		if e := write(p, data, 0644); e != nil {
			t.Fatal(e)
		}
		files[name] = p
		object(manifest, "files")[name] = record{"windows_attributes": 4}
	}
	node, e := payloadWix(files, manifest, "windows-x64", "1.0.2", "machine", "/machine/git-default-v2", false)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "payload.wxs")
	if e = writeWix(p, node); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	counts := map[string]int{}
	for {
		token, e := decoder.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		counts[start.Name.Local]++
		if start.Name.Local == "RegistryValue" {
			for _, v := range start.Attr {
				if v.Name.Local == "Root" && v.Value != "HKLM" {
					t.Fatal("wrong machine registry hive")
				}
			}
		}
	}
	if counts["RemoveFile"] != 3 || counts["PermissionEx"] != len(files) || counts["RemoveFolder"] < 3 {
		t.Fatalf("MSI repair/ACL contracts changed: %v", counts)
	}
	if bytes.Count(data, []byte("CompanionFile=")) != 3 {
		t.Fatal("non-executable payload lost companion versioning")
	}
	if bytes.Contains(data, []byte(`Directory="Dir_`+shortID("", "bin")+`"`)) {
		t.Fatal("bin should use Product.wxs BINDIR")
	}
}
func TestCorrespondingSourcesRemainOpaqueAndGrouped(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if e := os.Mkdir(cache, 0755); e != nil {
		t.Fatal(e)
	}
	var sources []any
	for _, v := range []struct{ name, group, data string }{{"upstream.zip", "git", "opaque workflow bytes"}, {"dependency.tar.xz", "dependencies", "\xfd7zXZ\x00unmodified"}} {
		if e := write(filepath.Join(cache, v.name), []byte(v.data), 0644); e != nil {
			t.Fatal(e)
		}
		sources = append(sources, record{"file": v.name, "group": v.group, "bytes": len(v.data), "sha256": digestBytes([]byte(v.data)), "url": "https://fixture.invalid/" + v.name})
	}
	lock := filepath.Join(root, "lock.json")
	if e := writeNewJSON(lock, record{"schema_version": 1, "product": "git-next", "sources": sources}); e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(root, "out")
	a := &app{root: root, out: io.Discard, err: io.Discard}
	if e := prepareCorrespondingSources(a, packagingOptions(map[string]string{"cache": cache, "output": out, "lock": lock, "workers": "2"})); e != nil {
		t.Fatal(e)
	}
	for _, v := range sources {
		item := rec(v)
		z, e := zip.OpenReader(filepath.Join(out, "git-next-corresponding-sources-"+stringValue(item, "group")+".zip"))
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, f := range z.File {
			if f.Name == "archives/"+stringValue(item, "file") {
				r, e := f.Open()
				if e != nil {
					t.Fatal(e)
				}
				b, e := io.ReadAll(r)
				r.Close()
				if e != nil || digestBytes(b) != item["sha256"] {
					t.Fatal("source archive was modified")
				}
				found = true
			}
		}
		z.Close()
		if !found {
			t.Fatal("opaque source missing")
		}
	}
	if e := prepareCorrespondingSources(a, packagingOptions(map[string]string{"cache": cache, "output": out, "lock": lock})); e == nil {
		t.Fatal("source bundle overwrite allowed")
	}
}
func packagingTarFixture(t *testing.T, p string, members []struct {
	name, link string
	data       []byte
}) {
	t.Helper()
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		header := &tar.Header{Name: m.name, Size: int64(len(m.data)), Mode: 0755, Typeflag: tar.TypeReg}
		if m.link != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = m.link
			header.Size = 0
		}
		if e = tw.WriteHeader(header); e != nil {
			t.Fatal(e)
		}
		if header.Typeflag == tar.TypeReg {
			if _, e = tw.Write(m.data); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = tw.Close(); e != nil {
		t.Fatal(e)
	}
	if e = gz.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestPOSIXRewritePreservesNativeBytesAndSafeLinks(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "input.tar.gz")
	binary := []byte("\x7fELFimmutable-native-image\x00\xff")
	packagingTarFixture(t, original, []struct {
		name, link string
		data       []byte
	}{{"./bin/git", "", binary}, {"./bin/git-next", "git", nil}, {"./docs/guide.md", "", []byte("old")}})
	target := filepath.Join(root, "out.tar.gz")
	if e := rewriteTar(original, target, "git-next-1.0.2-linux-x64", map[string][]byte{"docs/guide.md": []byte("new"), "docs/new.md": []byte("added")}, 6, true); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	if e := eachArchive(target, func(m archiveMember, r io.Reader) error {
		seen[m.name] = true
		if strings.HasSuffix(m.name, "/bin/git") {
			b, e := io.ReadAll(r)
			if e != nil {
				return e
			}
			if !bytes.Equal(b, binary) {
				t.Fatal("native image changed")
			}
		}
		if strings.HasSuffix(m.name, "/bin/git-next") && (!m.symlink || m.link != "git") {
			t.Fatal("relative core alias changed")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !seen["git-next-1.0.2-linux-x64/docs/new.md"] {
		t.Fatal("new documentation missing")
	}
	if e := rewriteTar(original, target, "", nil, 6, false); e == nil {
		t.Fatal("archive overwrite allowed")
	}
	updater := filepath.Join(root, "updater.tar.gz")
	if e := rewriteTar(original, updater, "", map[string][]byte{"bin/git": []byte("replacement native updater")}, 3, false); e != nil {
		t.Fatal(e)
	}
	if e := eachArchive(updater, func(m archiveMember, r io.Reader) error {
		if strings.HasSuffix(m.name, "bin/git") && m.mode.Perm() != 0755 {
			t.Fatal("repacked native executable lost its executable mode")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	bad := filepath.Join(root, "bad.tar.gz")
	packagingTarFixture(t, bad, []struct {
		name, link string
		data       []byte
	}{{"./bin/git", "../../escape", nil}})
	if e := rewriteTar(bad, filepath.Join(root, "bad-output.tar.gz"), "", nil, 6, true); e == nil {
		t.Fatal("escaping symlink accepted")
	}
}
func TestReleaseEvidenceFailsClosed(t *testing.T) {
	for _, gate := range []record{{"results": []any{record{"case": "a", "status": "pass"}}}, {"results": []any{record{"case": "a", "status": "pass"}, record{"case": "a", "status": "pass"}}}, {"results": []any{record{"case": "a", "status": "fail"}, record{"case": "b", "status": "pass"}}}} {
		if e := resultCases(gate, []string{"a", "b"}); e == nil {
			t.Fatal("incomplete/duplicate/failed release evidence accepted")
		}
	}
	if e := resultCases(record{"results": []any{record{"case": "a", "status": "pass"}, record{"case": "b", "status": "pass"}}}, []string{"a", "b"}); e != nil {
		t.Fatal(e)
	}
}
func TestDistributionFilesRejectCaseCollisionAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if e := write(filepath.Join(root, "a"), []byte("a"), 0644); e != nil {
		t.Fatal(e)
	}
	manifest := record{"files": record{"a": record{"sha256": digestBytes([]byte("a")), "bytes": 1}, "A": record{"sha256": digestBytes([]byte("a")), "bytes": 1}}}
	if _, e := checkedMSIFiles(root, manifest); e == nil {
		t.Fatal("case collision accepted")
	}
	if !bridgeImportsMatch("git-next-bridge.exe", []string{"KERNEL32.dll", "advapi32.dll"}) || bridgeImportsMatch("git-next-bridge.exe", []string{"kernel32.dll"}) || !bridgeImportsMatch("git-next-bridge-gui.exe", []string{"kernel32.dll", "advapi32.dll", "user32.dll"}) {
		t.Fatal("freestanding bridge import contract changed")
	}
}
