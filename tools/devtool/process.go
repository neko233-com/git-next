// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type result struct {
	stdout, stderr []byte
	code           int
}

func runProcess(ctx context.Context, cwd string, environment []string, input []byte, program string, args ...string) (result, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = cwd
	if environment != nil {
		cmd.Env = environment
	}
	cmd.Stdin = bytes.NewReader(input)
	var out, errout bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errout
	cmd.WaitDelay = time.Second
	cleanup, e := prepareProcess(cmd)
	if e != nil {
		return result{}, e
	}
	defer cleanup()
	if e = cmd.Start(); e != nil {
		return result{}, e
	}
	if e = attachProcess(cmd); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return result{}, e
	}
	e = cmd.Wait()
	r := result{out.Bytes(), errout.Bytes(), 0}
	if contextErr := ctx.Err(); contextErr != nil {
		r.code = -1
		if cmd.ProcessState != nil {
			r.code = cmd.ProcessState.ExitCode()
		}
		return r, fmt.Errorf("command %s exceeded its execution context: %w", filepathBase(program), contextErr)
	}
	if e != nil {
		r.code = 1
		if exit, ok := e.(*exec.ExitError); ok {
			r.code = exit.ExitCode()
		} else {
			return r, e
		}
	}
	return r, nil
}
func (a *app) run(program string, args ...string) (result, error) {
	ctx, cancel := deadline()
	defer cancel()
	return runProcess(ctx, a.root, nil, nil, program, args...)
}
func toolCommand(args ...string) []string {
	program, e := os.Executable()
	if e != nil {
		panic(e)
	}
	return append([]string{program}, args...)
}
func (a *app) invoke(command string, args ...string) (result, error) {
	argv := toolCommand(append([]string{command}, args...)...)
	return a.run(argv[0], argv[1:]...)
}
func (a *app) mustRun(program string, args ...string) ([]byte, error) {
	r, e := a.run(program, args...)
	if e != nil {
		return nil, e
	}
	if r.code != 0 {
		return nil, fmt.Errorf("%s exited %d: %s", filepathBase(program), r.code, strings.TrimSpace(string(r.stderr)))
	}
	return r.stdout, nil
}
func filepathBase(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return p[strings.LastIndex(p, "/")+1:]
}
func cleanEnvironment(home string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GIT_") || upper == "HOME" || upper == "USERPROFILE" || upper == "XDG_CONFIG_HOME" || upper == "SSH_ROOT_PATH" || upper == "PROGRAMDATA" || upper == "APPDATA" || upper == "LOCALAPPDATA" {
			continue
		}
		env = append(env, item)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home+"/.config", "SSH_ROOT_PATH="+home+"/.ssh", "PROGRAMDATA="+home+"/programdata", "APPDATA="+home+"/AppData/Roaming", "LOCALAPPDATA="+home+"/AppData/Local", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+home+"/config", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANG=C")
}
