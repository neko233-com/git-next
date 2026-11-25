// Copyright 2026 git-next contributors. MIT license.
package main

import "testing"

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
