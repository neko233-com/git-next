// Copyright 2026 git-next contributors. MIT license.
// Command tripwire is the refuse-style fake Git used by acceptance fixtures.
// Every invocation is recorded so a test can prove that native discovery never
// executed a sibling Git, and the stub answers only the synthetic config queries
// the fixtures need.
package main

import (
	"fmt"
	"os"
)

func hasArgument(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if argument == wanted {
			return true
		}
	}
	return false
}

func main() {
	marker := os.Getenv("GIT_NEXT_CONFIG_TRIPWIRE")
	if marker != "" {
		if file, e := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
			_, _ = file.WriteString("invoked\n")
			_ = file.Close()
		}
	}
	if hasArgument(os.Args[1:], "--exec-path") {
		fmt.Fprintln(os.Stdout, os.Getenv("GIT_NEXT_FIXTURE_CORE"))
		return
	}
	fmt.Fprintf(os.Stdout, "system\x00file:%s\x00probe.source\x00", os.Getenv("GIT_NEXT_FIXTURE_CONFIG"))
}
