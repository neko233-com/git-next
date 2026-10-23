// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductAuditPairedPOSIXManifests(t *testing.T) {
	type fixtureMember struct {
		name, text, link string
	}
	for _, test := range []struct {
		name                                                                            string
		key, value                                                                      string
		differentRoot, conflictingFile, missingFile, duplicate, reverse, python, escape bool
	}{
		{name: "same-root"},
		{name: "package-first", reverse: true},
		{name: "platform-conflict", key: "platform", value: "macos-arm64"},
		{name: "version-conflict", key: "version", value: "other"},
		{name: "source-conflict", key: "source_tree_sha256", value: "other"},
		{name: "missing-source", key: "source_tree_sha256"},
		{name: "missing-product", key: "product"},
		{name: "missing-platform", key: "platform"},
		{name: "missing-version", key: "version"},
		{name: "other-product", key: "product", value: "other"},
		{name: "different-roots", differentRoot: true},
		{name: "conflicting-files", conflictingFile: true},
		{name: "missing-files", missingFile: true},
		{name: "duplicate", duplicate: true},
		{name: "unlisted-python", python: true},
		{name: "escaping-link", escape: true},
	} {
		for _, archive := range []bool{false, true} {
			form := "directory"
			if archive {
				form = "tar"
			}
			if test.duplicate && !archive {
				continue
			}
			t.Run(test.name+"/"+form, func(t *testing.T) {
				file := record{"kind": "file", "sha256": digestBytes([]byte("native")), "bytes": 6}
				pack := record{"schema_version": 1, "product": "git-next", "platform": "linux-x64", "version": "fixture", "source_tree_sha256": "fixture-source", "files": record{"bin/git": file}}
				distribution := record{"schema_version": 1, "product": "git-next", "platform": "linux-x64", "version": "fixture", "source_tree_sha256": "fixture-source", "files": record{"bin/git": file, "runtime/data": record{"kind": "file", "sha256": "fixture"}}}
				if test.key != "" {
					distribution[test.key] = test.value
				}
				if test.conflictingFile {
					object(distribution, "files")["bin/git"] = record{"kind": "file", "sha256": "different"}
				}
				if test.missingFile {
					delete(object(distribution, "files"), "bin/git")
				}
				encode := func(value record) string {
					data, err := jsonBytes(value)
					if err != nil {
						t.Fatal(err)
					}
					return string(data)
				}
				distributionName := "git-next-distribution.json"
				if test.differentRoot {
					distributionName = "nested/" + distributionName
				}
				members := []fixtureMember{{distributionName, encode(distribution), ""}, {"package.json", encode(pack), ""}, {"bin/git", "native", ""}}
				if test.reverse {
					members[0], members[1] = members[1], members[0]
				}
				if test.duplicate {
					members = append(members, fixtureMember{"package.json", encode(pack), ""})
				}
				if test.python {
					members = append(members, fixtureMember{"bin/extensionless", "#!/usr/bin/env python3\n", ""})
				}
				if test.escape {
					members = append(members, fixtureMember{"bin/link", "", "../../outside"})
				}
				root := t.TempDir()
				var report record
				var err error
				if archive {
					var buffer bytes.Buffer
					tarball := tar.NewWriter(&buffer)
					for _, member := range members {
						header := &tar.Header{Name: member.name, Mode: 0644, Size: int64(len(member.text))}
						if member.link != "" {
							header.Typeflag, header.Linkname = tar.TypeSymlink, member.link
						}
						if err := tarball.WriteHeader(header); err != nil {
							t.Fatal(err)
						}
						if _, err := tarball.Write([]byte(member.text)); err != nil {
							t.Fatal(err)
						}
					}
					if err := tarball.Close(); err != nil {
						t.Fatal(err)
					}
					name := filepath.Join(root, "fixture.tar")
					if err := os.WriteFile(name, buffer.Bytes(), 0644); err != nil {
						t.Fatal(err)
					}
					report, err = inspectNoPythonProduct("", name)
				} else {
					for _, member := range members {
						name := filepath.Join(root, filepath.FromSlash(member.name))
						if member.link != "" {
							if err := os.Symlink(member.link, name); err != nil {
								t.Skipf("symlinks unavailable: %v", err)
							}
						} else if err := write(name, []byte(member.text), 0644); err != nil {
							t.Fatal(err)
						}
					}
					report, err = inspectNoPythonProduct(root, "")
				}
				bad := test.key != "" || test.differentRoot || test.conflictingFile || test.missingFile || test.duplicate || test.escape
				if bad {
					if err == nil {
						t.Fatalf("conflicting or unsafe product accepted: %+v", report)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(stringValue(object(report, "identity"), "manifest"), "git-next-distribution.json") {
					t.Fatalf("complete distribution manifest was not selected: %+v", report)
				}
				wantPython := 0
				if test.python {
					wantPython = 1
				}
				if report["python_entry_count"] != wantPython || report["complete"] != !test.python {
					t.Fatalf("Python checks changed: %+v", report)
				}
			})
		}
	}
}

func TestProductArchiveAuditChecksLinkTargets(t *testing.T) {
	for _, target := range []string{"../runtime/bin/python3", "../../outside"} {
		var buffer bytes.Buffer
		tarball := tar.NewWriter(&buffer)
		manifest := []byte(`{"schema_version":1,"product":"git-next","version":"fixture","files":{"bin/git":{"sha256":"fixture"}}}`)
		if e := tarball.WriteHeader(&tar.Header{Name: "git-next-distribution.json", Mode: 0644, Size: int64(len(manifest))}); e != nil {
			t.Fatal(e)
		}
		if _, e := tarball.Write(manifest); e != nil {
			t.Fatal(e)
		}
		if e := tarball.WriteHeader(&tar.Header{Name: "bin/helper", Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0755}); e != nil {
			t.Fatal(e)
		}
		if e := tarball.Close(); e != nil {
			t.Fatal(e)
		}
		archive := filepath.Join(t.TempDir(), "product.tar")
		if e := os.WriteFile(archive, buffer.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
		report, e := inspectNoPythonProduct("", archive)
		if target == "../../outside" {
			if e == nil {
				t.Fatal("escaping archive symlink accepted")
			}
		} else if e != nil || report["complete"] != false || report["python_entry_count"] != 1 {
			t.Fatalf("Python symlink target was missed: %v %+v", e, report)
		}
	}
}

func TestMigrationAuditFindsExecutionButIgnoresProtocolAndHistoricalNames(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"tools", "tests", "packaging"} {
		if e := os.MkdirAll(filepath.Join(root, folder), 0755); e != nil {
			t.Fatal(e)
		}
	}
	source := `package main
import("context";"os/exec")
const historical="tests/legacy_fixture.py"
const protocol="python marshal byte tags; not an interpreter invocation"
func sample(){exec.CommandContext(context.Background(),"python3","fixture.py")}
`
	if e := write(filepath.Join(root, "tools/entry.go"), []byte(source), 0644); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "tests/probe.ps1"), []byte("$value=@{python = $false}\n# python legacy.py\n& python3.exe fixture.py --token PRIVATE\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "packaging/Dockerfile"), []byte("FROM python:3.14-slim\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "packaging/source.zip"), []byte("opaque python source archive"), 0644); e != nil {
		t.Fatal(e)
	}
	refs, e := scanPythonEntrypoints(root)
	if e != nil {
		t.Fatal(e)
	}
	if len(refs) != 3 {
		t.Fatalf("expected exactly 3 executable references: %+v", refs)
	}
	for _, ref := range refs {
		if ref.Command == "PRIVATE" || ref.Command == "& python3.exe fixture.py --token PRIVATE" {
			t.Fatal("audit leaked command arguments")
		}
	}
}
func TestProductAuditChecksUnlistedPythonAndExtensionlessShebang(t *testing.T) {
	root := t.TempDir()
	if e := writeJSON(filepath.Join(root, "git-next-distribution.json"), record{"schema_version": 1, "product": "git-next", "version": "fixture", "platform": "linux-x64", "files": record{"bin/git": record{"sha256": "fixture"}}}); e != nil {
		t.Fatal(e)
	}
	if e := write(filepath.Join(root, "bin/git"), []byte("ELF fixture"), 0755); e != nil {
		t.Fatal(e)
	}
	report, e := inspectNoPythonProduct(root, "")
	if e != nil || report["complete"] != true {
		t.Fatalf("clean package rejected: %v %+v", e, report)
	}
	if e = write(filepath.Join(root, "bin/git-p4"), []byte("#!/usr/bin/env python3\nprint('old integration')\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if e = write(filepath.Join(root, "runtime/usr/lib/python3.14/os.py"), []byte("third party runtime"), 0644); e != nil {
		t.Fatal(e)
	}
	report, e = inspectNoPythonProduct(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if report["complete"] != false || report["python_entry_count"] != 2 {
		t.Fatalf("unlisted Python payload was missed: %+v", report)
	}
}

func TestProductAuditRecognizesEmbeddedPythonLibraryArchives(t *testing.T) {
	for _, name := range []string{"runtime/python314.zip", "runtime/lib/libpython3.14.a", "bin/tool.pyz", "Python.framework/Versions/3.14/Python"} {
		if pythonAssetReason(name, nil) == "" {
			t.Fatalf("Python asset was missed: %s", name)
		}
	}
	if pythonAssetReason("runtime/share/vim/syntax/python.vim", nil) != "" {
		t.Fatal("editor syntax data was mistaken for a Python runtime")
	}
}
func TestFixtureErrorRedactsCredentials(t *testing.T) {
	input := "Authorization: Basic YWJjZGVmZw==\npassword=fixture-token\nplain failure"
	got := fixtureCredentialText.ReplaceAllString(input, "<redacted>")
	if got == input || len(got) > 100 {
		t.Fatal("fixture credentials were not redacted")
	}
	if got := acceptCommandDescription([]string{"-c", "http.extraHeader=Basic PRIVATE", "config", "http.extraHeader", "Basic PRIVATE"}); got != "config http.extraHeader" {
		t.Fatal(got)
	}
}
