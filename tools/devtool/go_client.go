// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	register("build-go-client", "Build the Go 1.27 CLI into target/release", "output?", buildGoClient)
}

func buildGoClient(a *app, o options) error {
	if !exists(a.path("go.mod")) || !exists(a.path("cmd/git-next/main.go")) {
		return fmt.Errorf("Go client module or entrypoint is missing")
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	output, err := absolute(o.value("output", a.path("target/release/git"+suffix)))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	env := append([]string{}, os.Environ()...)
	env = append(env, "GOTOOLCHAIN=go1.27.3", "CGO_ENABLED=0")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r, err := runProcess(ctx, a.root, env, nil, "go", "build", "-mod=mod", "-trimpath", "-ldflags", "-s -w", "-o", output, "./cmd/git-next")
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fmt.Errorf("go build exited %d: %s", r.code, strings.TrimSpace(string(r.stderr)))
	}
	info, err := os.Stat(output)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "built %s (%d bytes)\n", filepath.ToSlash(output), info.Size())
	return nil
}
