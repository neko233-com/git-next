// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestP4FindsGitWithinOwnedPOSIXPackage(t *testing.T) {
	root := t.TempDir()
	write := func(name string) string {
		t.Helper()
		name = filepath.Join(root, filepath.FromSlash(name))
		if e := os.MkdirAll(filepath.Dir(name), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(name, nil, 0755); e != nil {
			t.Fatal(e)
		}
		return name
	}
	helper := write("runtime/usr/libexec/git-core/git-p4")
	git := write("bin/git")
	if _, e := ownedGit(helper); e == nil {
		t.Fatal("accepted an ancestor without package markers")
	}
	write("git-next-distribution.json")
	if _, e := ownedGit(helper); e == nil {
		t.Fatal("accepted an ancestor without runtime configuration")
	}
	write("runtime/etc/gitconfig")
	if got, e := ownedGit(helper); e != nil {
		t.Fatal(e)
	} else if actual, e := os.Stat(got); e != nil {
		t.Fatal(e)
	} else if expected, e := os.Stat(git); e != nil || !os.SameFile(actual, expected) {
		t.Fatal(got, git, e)
	}
	adjacent := write("runtime/usr/libexec/git-core/git")
	if got, e := ownedGit(helper); e != nil {
		t.Fatal(e)
	} else if actual, e := os.Stat(got); e != nil {
		t.Fatal(e)
	} else if expected, e := os.Stat(adjacent); e != nil || !os.SameFile(actual, expected) {
		t.Fatal(got, adjacent, e)
	}
}

func TestP4TimeoutAcceptsPositiveDuration(t *testing.T) {
	t.Setenv("GIT_P4_GIT", filepath.Join(t.TempDir(), "fixture-git"))
	for _, value := range []string{"", "6h", "250ms"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("GIT_P4_TIMEOUT", value)
			d, e := newDriver(io.Discard, io.Discard)
			if e != nil {
				t.Fatal(e)
			}
			want := 30 * time.Minute
			if value != "" {
				want, _ = time.ParseDuration(value)
			}
			ctx, cancel := d.commandContext()
			defer cancel()
			deadline, ok := ctx.Deadline()
			if d.timeout != want || !ok || time.Until(deadline) > want {
				t.Fatal(d.timeout, deadline)
			}
		})
	}
	for _, value := range []string{"0", "-1m", "never", "9999999999999999999h"} {
		t.Setenv("GIT_P4_TIMEOUT", value)
		if _, e := newDriver(io.Discard, io.Discard); e == nil {
			t.Fatalf("accepted invalid timeout %q", value)
		}
	}
}
