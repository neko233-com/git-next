// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMeasuredProcessFixture(t *testing.T) {
	switch os.Getenv("GIT_NEXT_MEASURED_PROCESS_FIXTURE") {
	case "exit-one":
		os.Exit(1)
	case "hang":
		_, _ = os.Stdout.WriteString("measured fixture ready\n")
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		t.Skip("subprocess fixture")
	}
}

func TestMeasuredProcessDistinguishesExitOneFromDeadline(t *testing.T) {
	run := func(mode string, timeout time.Duration) (measuredProcess, error) {
		return measuredRun("", append(os.Environ(), "GIT_NEXT_MEASURED_PROCESS_FIXTURE="+mode), nil, timeout, os.Args[0], "-test.run=^TestMeasuredProcessFixture$")
	}
	ordinary, e := run("exit-one", 5*time.Second)
	if e != nil || ordinary.code != 1 {
		t.Fatalf("ordinary exit1: code=%d error=%v", ordinary.code, e)
	}
	started := time.Now()
	timed, e := run("hang", 300*time.Millisecond)
	if !errors.Is(e, context.DeadlineExceeded) || !strings.Contains(string(timed.stdout), "measured fixture ready") {
		t.Fatalf("real running child deadline: output=%q error=%v", timed.stdout, e)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("timed-out measured child was not reaped promptly")
	}
}

func TestMSIReuseAcceptsOnlyExactMeasuredDeadlineBlock(t *testing.T) {
	a := &app{root: t.TempDir(), out: io.Discard, err: io.Discard}
	before := "package main\nfunc measuredFixture() {\n\treturn value, nil\n}\n"
	after := strings.Replace(before, "\treturn value, nil", benchmarkContextDeadlineBlock+"\treturn value, nil", 1)
	previous := record{"source_tree_sha256": "source", "files": record{"package.msi": "same"}, "platforms": record{"windows-x64": record{"msi": "same"}}, "harnesses": record{"tools/devtool/benchmark_process.go": digestBytes([]byte(before))}}
	report := record{"phase": "correctness", "source_tree_sha256": "source", "reference_sha256": "reference", "results": []any{record{"case": "msi-user", "status": "pass", "exit_code": 0}, record{"case": "msi-machine", "status": "pass", "exit_code": 0}}}
	for _, mode := range []string{"exact deadline check", "extra implementation edit", "double deadline block", "wrong current hash", "changed process helper", "changed MSI script", "changed MSI recipe", "new timeout test"} {
		t.Run(mode, func(t *testing.T) {
			current := cloneRecord(t, previous)
			data := after
			switch mode {
			case "extra implementation edit":
				data += "// unrelated change\n"
			case "double deadline block":
				data = strings.Replace(data, benchmarkContextDeadlineBlock, benchmarkContextDeadlineBlock+benchmarkContextDeadlineBlock, 1)
			case "changed process helper":
				object(current, "harnesses")["tools/devtool/process.go"] = "changed"
			case "changed MSI script":
				object(current, "harnesses")["tests/msi_acceptance.ps1"] = "changed"
			case "changed MSI recipe":
				object(current, "harnesses")["tools/devtool/packaging_msi.go"] = "changed"
			case "new timeout test":
				object(current, "harnesses")["tools/devtool/benchmark_process_timeout_test.go"] = "new non-production test source"
			}
			if e := write(a.path("tools/devtool/benchmark_process.go"), []byte(data), 0644); e != nil {
				t.Fatal(e)
			}
			object(current, "harnesses")["tools/devtool/benchmark_process.go"] = digestBytes([]byte(data))
			if mode == "wrong current hash" {
				object(current, "harnesses")["tools/devtool/benchmark_process.go"] = "unmatched"
			}
			_, e := validateReuseAt(a, report, previous, current, "reference", []string{"msi-user", "msi-machine"})
			wantPass := mode == "exact deadline check" || mode == "new timeout test"
			if (e == nil) != wantPass {
				t.Fatalf("mode=%s error=%v", mode, e)
			}
		})
	}
}
