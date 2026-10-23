// Copyright 2026 git-next contributors. MIT license.
package main

import "testing"

func TestPythonRuntimeMemberSDKGenerator(t *testing.T) {
	for _, name := range []string{"mingw64/bin/psl-make-dafsa", "clangarm64/bin/psl-make-dafsa", "usr/bin/psl-make-dafsa", "USR/BIN/PSL-MAKE-DAFSA"} {
		if !pythonRuntimeMember(name) {
			t.Errorf("SDK Python generator was retained: %s", name)
		}
	}
	for _, name := range []string{"mingw64/bin/libpsl-5.dll", "clangarm64/bin/libpsl-5.dll", "usr/share/publicsuffix/public_suffix_list.dat", "usr/share/libpsl/public_suffix_list.dafsa", "usr/share/licenses/libpsl/LICENSE", "sources/libpsl-0.21.5.tar.gz"} {
		if pythonRuntimeMember(name) {
			t.Errorf("libpsl runtime data/license/source was excluded: %s", name)
		}
	}
}
