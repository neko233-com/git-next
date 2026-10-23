// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeIdentityPreservesWireOrder(t *testing.T) {
	input := []byte(`{"product": "git-next", "version":"1.0.2","features":{"z":true,"a":"中文 <value> 😀"},"values":[false,null,1]}`)
	got, e := orderedCompactJSON(input)
	if e != nil {
		t.Fatal(e)
	}
	want := `{"product":"git-next","version":"1.0.2","features":{"z":true,"a":"\u4e2d\u6587 <value> \ud83d\ude00"},"values":[false,null,1]}`
	if got != want {
		t.Fatalf("identity wire bytes differ:\n%s", got)
	}
	if _, e = orderedCompactJSON([]byte(`{} {}`)); e == nil {
		t.Fatal("trailing JSON accepted")
	}
	if got := string(canonicalPythonJSON(map[string]string{"z": "<>&", "a": "中文"})); got != `{"a": "\u4e2d\u6587", "z": "<>&"}` {
		t.Fatalf("source identity encoding drift: %s", got)
	}
	if got := cLiteral("中文\u2028<PRIu64>"); got != "\"中文\u2028\" PRIu64 \"\"" {
		t.Fatalf("C catalog UTF-8 encoding drift: %q", got)
	}
}

func TestImplementationEvidenceRejectsEmptyValues(t *testing.T) {
	for _, v := range []any{nil, false, "", []any{}, record{}, float64(0)} {
		if completionEvidencePresent(v) {
			t.Fatalf("accepted empty implementation evidence: %#v", v)
		}
	}
	for _, v := range []any{"receipt.json", []any{"receipt.json"}, record{"source": "receipt.json"}} {
		if !completionEvidencePresent(v) {
			t.Fatalf("refused concrete implementation evidence: %#v", v)
		}
	}
}
func TestFreestandingCompileContract(t *testing.T) {
	for _, gui := range []bool{false, true} {
		mingw := mingwBridgeCommand("clang", "source.c", "memory.c", "bridge.o", "bridge.exe", gui)
		msvc := msvcBridgeCommand("cl.exe", "source.c", "memory.obj", "bridge.res", "bridge.exe", []string{"SDK include"}, []string{"SDK lib"}, gui)
		for _, cmd := range [][]string{mingw, msvc, msvcMemoryCommand("cl.exe", "memory.c", "memory.obj")} {
			if e := rejectReducedInlining(cmd); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(strings.Join(cmd, " "), "Ob1") {
				t.Fatal("O2 workaround returned")
			}
		}
		if !strings.Contains(strings.Join(msvc, " "), "/O2") || !strings.Contains(strings.Join(msvc, " "), "/NODEFAULTLIB") {
			t.Fatal("MSVC bridge lost O2/CRT guard")
		}
		if !strings.Contains(strings.Join(mingw, " "), "source.c memory.c bridge.o") {
			t.Fatal("MinGW bridge lost helper translation unit")
		}
	}
	if rejectReducedInlining([]string{"/O2", "/Ob1"}) == nil {
		t.Fatal("reduced-inline guard missing")
	}
}
func TestNativePkgConfigQuoting(t *testing.T) {
	got, e := shellWords(`-I'/SDK path/include' -L/one\ two -lsecret "a\"b" ''`)
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"-I/SDK path/include", "-L/one two", "-lsecret", `a"b`, ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if _, e = shellWords(`-I'unterminated`); e == nil {
		t.Fatal("malformed pkg-config accepted")
	}
	quoted := shellQuote("/one'two lib")
	roundtrip, e := shellWords(quoted)
	if e != nil || !reflect.DeepEqual(roundtrip, []string{"/one'two lib"}) {
		t.Fatal(quoted, roundtrip, e)
	}
}
func TestCodeloadZeroModeIsReadable(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "source.zip")
	f, e := os.Create(archive)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "source/a.c", Method: zip.Store}
	h.CreatorVersion = 3 << 8
	h.ExternalAttrs = 0
	w, e := z.CreateHeader(h)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = w.Write([]byte("source\n"))
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	destination := filepath.Join(root, "output")
	_, _, e = extractArchive(archive, destination, extractPolicy{strip: "source/", maxFiles: 5, maxBytes: 100})
	if e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(destination, "a.c"))
	if e != nil || !bytes.Equal(got, []byte("source\n")) {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(destination, "a.c"))
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm()&0400 == 0 {
		t.Fatal("zero mode produced unreadable source")
	}
}
func TestImagePlatformTreatsShortDataAsData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "NOTICE")
	if e := os.WriteFile(p, []byte("MIT"), 0644); e != nil {
		t.Fatal(e)
	}
	got, e := imagePlatform(p)
	if e != nil || got != "" {
		t.Fatal(got, e)
	}
}
func TestNativeBuildContainerHasNoInterpreterDependency(t *testing.T) {
	if strings.Contains(linuxCompileScript, "python3") || strings.Contains(linuxCompileScript, ".py ") {
		t.Fatal("compiler container still invokes Python")
	}
	for _, required := range []string{"/inputs/tool collect-posix-prerequisites", "/inputs/tool build-native-dependencies", "/inputs/tool build-native-posix", "tar -xf /inputs/source-input.tar"} {
		if !strings.Contains(linuxCompileScript, required) {
			t.Fatalf("missing compiler stage %s", required)
		}
	}
}

func TestNativeMatrixReadsExplicitTerminalWorktree(t *testing.T) {
	root, clean := t.TempDir(), t.TempDir()
	p := filepath.Join(clean, "src/main.rs")
	if e := write(p, []byte("committed terminal source\n"), 0644); e != nil {
		t.Fatal(e)
	}
	hash, e := digest(p)
	if e != nil {
		t.Fatal(e)
	}
	m := record{"terminal_source": clean, "harnesses": record{"../git-terminal/src/main.rs": hash}}
	if e = nativeMatrixReadback(&app{root: root}, m); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, []byte("modified terminal source\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e = nativeMatrixReadback(&app{root: root}, m); e == nil {
		t.Fatal("modified explicit worktree source accepted")
	}
}
