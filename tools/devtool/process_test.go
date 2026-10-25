// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProcessFixture(t *testing.T) {
	mode := os.Getenv("GIT_NEXT_GO_FIXTURE")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	switch mode {
	case "exit-one":
		os.Exit(1)
	case "pipes":
		os.Stdout.Write(bytes.Repeat([]byte("x"), 100000))
		os.Stderr.Write(bytes.Repeat([]byte("y"), 100000))
	case "hang":
		time.Sleep(30 * time.Second)
	case "descendant":
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessFixture$")
		cmd.Env = append(os.Environ(), "GIT_NEXT_GO_FIXTURE=hang")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if e := cmd.Start(); e != nil {
			os.Exit(3)
		}
		os.Stdout.WriteString("descendant spawned\n")
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
func fixtureRun(t *testing.T, mode string, timeout time.Duration) (result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runProcess(ctx, "", append(os.Environ(), "GIT_NEXT_GO_FIXTURE="+mode), nil, os.Args[0], "-test.run=^TestProcessFixture$")
}
func TestProcessCapturesBothPipes(t *testing.T) {
	r, e := fixtureRun(t, "pipes", 5*time.Second)
	if e != nil || r.code != 0 || len(r.stdout) < 100000 || len(r.stderr) < 100000 {
		t.Fatal(r.code, e)
	}
}
func TestProcessTimeoutStopsTree(t *testing.T) {
	start := time.Now()
	_, e := fixtureRun(t, "hang", 150*time.Millisecond)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline was reported as an ordinary command result: %v", e)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("timeout cleanup hung")
	}
}
func TestProcessOrdinaryFailureIsNotDeadline(t *testing.T) {
	r, e := fixtureRun(t, "exit-one", 5*time.Second)
	if e != nil || r.code != 1 {
		t.Fatalf("ordinary exit 1 was misclassified: code=%d error=%v", r.code, e)
	}
}
func TestProcessReapsOrphanedPipeOwner(t *testing.T) {
	start := time.Now()
	r, e := fixtureRun(t, "descendant", 5*time.Second)
	if e != nil && time.Since(start) > 4*time.Second {
		t.Fatal(e)
	}
	if !strings.Contains(string(r.stdout), "descendant spawned") {
		t.Fatal("parent did not complete")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("orphan held pipe open")
	}
}
func TestUnknownFlagsAndCommandsRefuse(t *testing.T) {
	for _, args := range [][]string{{"not-a-command"}, {"render-docs", "--bogus"}, {"package-rust", "--platform"}} {
		if e := execute(args, &bytes.Buffer{}, &bytes.Buffer{}); e == nil {
			t.Fatal("accepted", args)
		}
	}
}
