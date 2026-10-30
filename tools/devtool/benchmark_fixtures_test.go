// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureCleanupSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		fail, keep, wantRetained bool
	}{
		{"success", false, false, false}, {"failed", true, false, true}, {"explicit-keep", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "report.json")
			work := output + ".fixtures"
			if err := os.Mkdir(work, 0700); err != nil {
				t.Fatal(err)
			}
			info, err := ownedDirectory(work)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(work, "owned"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			var runErr error
			if tc.fail {
				runErr = errors.New("sample failed")
			}
			if err = finishBenchmarkReport(output, work, info, record{"status": "test"}, runErr, tc.keep); (err != nil) != tc.fail {
				t.Fatalf("unexpected result: %v", err)
			}
			report, err := load(output)
			if err != nil {
				t.Fatal(err)
			}
			if report["fixtures_retained"] != tc.wantRetained {
				t.Fatalf("wrong report: %v", report)
			}
			_, err = os.Stat(work)
			if (err == nil) != tc.wantRetained {
				t.Fatalf("wrong directory state: %v", err)
			}
		})
	}
}

func TestFixtureCleanupRefusesReplacement(t *testing.T) {
	parent := t.TempDir()
	output := filepath.Join(parent, "report.json")
	work := output + ".fixtures"
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	owned, err := ownedDirectory(work)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(work, work+".original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err = finishBenchmarkReport(output, work, owned, record{}, nil, false); err == nil {
		t.Fatal("replacement must be retained")
	}
	if _, err = os.Stat(work); err != nil {
		t.Fatal(err)
	}
}
