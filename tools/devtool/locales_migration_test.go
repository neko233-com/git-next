// Copyright 2026 git-next contributors. MIT license.
package main

import "testing"

func TestCatalogArms(t *testing.T) {
	actual, e := catalogArms(`match template { "simple" => "简单", "brace {key}" => { "引号 \" 与换行\n" }, "unicode" => "\u{6587}\u{6863}", "command" => function(), _ => template }`)
	if e != nil {
		t.Fatal(e)
	}
	expected := map[string]string{"simple": "简单", "brace {key}": "引号 \" 与换行\n", "unicode": "文档"}
	if !equalJSON(actual, expected) {
		t.Fatalf("arms=%#v", actual)
	}
	for _, text := range []string{`"x" => "unterminated`, `"x" => "\u{110000}"`, `"x" => "\xZZ"`} {
		if _, e = catalogArms(text); e == nil {
			t.Fatalf("accepted malformed literal: %s", text)
		}
	}
}
func TestLifecycleIsolation(t *testing.T) {
	// Absolute paths use the host's syntax so these checks run on all tooling targets.
	root := t.TempDir()
	work := root + "/work"
	install := work + "/install"
	if e := isolatedLifecyclePaths(work, install, root, true, false, false); e != nil {
		t.Fatal(e)
	}
	if isolatedLifecyclePaths(work, root+"/other", root, true, false, false) == nil {
		t.Fatal("accepted install outside work")
	}
	if isolatedLifecyclePaths(work, install, root, true, true, false) == nil {
		t.Fatal("accepted natural install on workstation")
	}
}
