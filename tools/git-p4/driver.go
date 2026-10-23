// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/neko233-com/git-next/tools/git-p4/internal/marshal"
)

type dictionary = map[string]any
type driver struct {
	git, p4, cwd   string
	out, err       io.Writer
	in             io.Reader
	config         map[string][]string
	users          map[string]string
	verbose        bool
	largeFiles     map[string]bool
	baseAttributes []byte
	metadataError  error
	usersLoaded    bool
	promptReader   *bufio.Reader
	commitUsers    map[string]string
}

func newDriver(out, errout io.Writer) (*driver, error) {
	cwd, e := os.Getwd()
	if e != nil {
		return nil, e
	}
	git := os.Getenv("GIT_P4_GIT")
	if git == "" {
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		for _, name := range []string{"git.exe", "git"} {
			candidate := filepath.Join(filepath.Dir(exe), name)
			if st, e := os.Stat(candidate); e == nil && !st.IsDir() {
				git = candidate
				break
			}
		}
	}
	if git == "" {
		return nil, fmt.Errorf("owned Git executable is missing beside git-p4; set GIT_P4_GIT only for an explicit development fixture")
	}
	p4 := os.Getenv("GIT_P4_P4")
	if p4 == "" {
		p4 = "p4"
	}
	return &driver{git: git, p4: p4, cwd: cwd, out: out, err: errout, in: os.Stdin, config: map[string][]string{}, users: map[string]string{}}, nil
}
func (d *driver) process(input []byte, environment []string, program string, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = d.cwd
	if environment != nil {
		cmd.Env = environment
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	e := runChild(cmd)
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			return stdout.Bytes(), exit.ExitCode(), fmt.Errorf("%s: %s", filepath.Base(program), strings.TrimSpace(string(stderr.Bytes())))
		}
		return stdout.Bytes(), -1, e
	}
	return stdout.Bytes(), 0, nil
}
func (d *driver) gitCall(input []byte, environment []string, args ...string) ([]byte, error) {
	b, _, e := d.process(input, environment, d.git, args...)
	return b, e
}
func (d *driver) gitText(args ...string) (string, error) {
	b, e := d.gitCall(nil, nil, args...)
	return strings.TrimSpace(string(b)), e
}
func (d *driver) cfgAll(key string) []string {
	if v, ok := d.config[key]; ok {
		return v
	}
	b, code, e := d.process(nil, nil, d.git, "config", "--null", "--get-all", key)
	if e != nil && code != 1 {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\x00")
	var result []string
	if s != "" {
		result = strings.Split(s, "\x00")
	}
	d.config[key] = result
	return result
}
func (d *driver) cfg(key string) string {
	values := d.cfgAll(key)
	if len(values) > 0 {
		return values[len(values)-1]
	}
	return ""
}
func (d *driver) cfgBool(key string) bool {
	v := strings.ToLower(d.cfg(key))
	return v == "true" || v == "yes" || v == "on" || v == "1"
}
func (d *driver) p4Args(args []string) ([]string, []string) {
	global := []string{}
	for _, v := range [][2]string{{"user", "-u"}, {"port", "-p"}, {"host", "-H"}, {"client", "-c"}} {
		if value := d.cfg("git-p4." + v[0]); value != "" {
			global = append(global, v[1], value)
		}
	}
	retries := d.cfg("git-p4.retries")
	if retries == "" {
		retries = "3"
	}
	if count, e := strconv.ParseInt(retries, 10, 32); e == nil && count > 0 {
		global = append(global, "-r", retries)
	}
	environment := append([]string{}, os.Environ()...)
	if password := d.cfg("git-p4.password"); password != "" {
		environment = append(environment, "P4PASSWD="+password)
	}
	return append(global, args...), environment
}
func (d *driver) p4Each(args []string, input dictionary, visit func(dictionary) error) error {
	args, environment := d.p4Args(append([]string{"-G"}, args...))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.p4, args...)
	cmd.Dir = d.cwd
	cmd.Env = environment
	var encoded bytes.Buffer
	if input != nil {
		if e := marshal.Encode(&encoded, input); e != nil {
			return e
		}
		cmd.Stdin = &encoded
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	attach, cleanup, e := childProcess(cmd)
	if e != nil {
		return e
	}
	defer cleanup()
	cmd.WaitDelay = time.Second
	if e = cmd.Start(); e != nil {
		return e
	}
	if e = attach(); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return e
	}
	decoder := marshal.NewDecoder(stdout)
	var failure error
	for {
		value, e := decoder.Decode()
		if e == io.EOF {
			break
		}
		if e != nil {
			failure = e
			break
		}
		item, ok := value.(map[string]any)
		if !ok {
			failure = fmt.Errorf("P4 emitted a non-dictionary record")
			break
		}
		if e = visit(item); e != nil {
			failure = e
			break
		}
	}
	if failure != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if failure != nil {
		return failure
	}
	if waitErr != nil {
		return fmt.Errorf("p4 request failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}
func runChild(cmd *exec.Cmd) error {
	attach, cleanup, e := childProcess(cmd)
	if e != nil {
		return e
	}
	defer cleanup()
	cmd.WaitDelay = time.Second
	if e = cmd.Start(); e != nil {
		return e
	}
	if e = attach(); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return e
	}
	return cmd.Wait()
}
func (d *driver) p4List(args ...string) ([]dictionary, error) { return d.p4ListInput(nil, args...) }
func (d *driver) p4ListInput(input dictionary, args ...string) ([]dictionary, error) {
	var result []dictionary
	var failure error
	e := d.p4Each(args, input, func(item dictionary) error {
		if text(item["code"]) == "error" && integer(item["severity"]) >= 3 {
			failure = fmt.Errorf("p4: %s", strings.TrimSpace(d.metadata(item["data"])))
		}
		result = append(result, item)
		return nil
	})
	if e != nil {
		if failure != nil {
			return result, failure
		}
		for _, item := range result {
			if text(item["code"]) == "error" {
				return result, fmt.Errorf("p4: %s", strings.TrimSpace(d.metadata(item["data"])))
			}
		}
		return result, e
	}
	return result, failure
}
func (d *driver) p4Single(args ...string) (dictionary, error) {
	values, e := d.p4List(args...)
	if e != nil {
		return nil, e
	}
	for _, v := range values {
		if text(v["code"]) == "stat" {
			return v, nil
		}
	}
	return nil, fmt.Errorf("p4 returned no data record")
}
func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	}
	return ""
}
func integer(v any) int64 { n, _ := strconv.ParseInt(text(v), 10, 64); return n }
func (d *driver) metadata(v any) string {
	s := text(v)
	if utf8.ValidString(s) || d.cfg("git-p4.metadataDecodingStrategy") == "passthrough" {
		return s
	}
	strategy := d.cfg("git-p4.metadataDecodingStrategy")
	if strategy != "" && strategy != "fallback" {
		d.metadataError = fmt.Errorf("decoding Perforce metadata failed; configure git-p4.metadataDecodingStrategy=fallback and metadataFallbackEncoding")
		return ""
	}
	encoding := strings.ToLower(d.cfg("git-p4.metadataFallbackEncoding"))
	if encoding == "" {
		encoding = "cp1252"
	}
	out, e := decodeEncoding(s, encoding, false)
	if e != nil {
		return escapeMetadata(s)
	}
	return out
}
func (d *driver) author(user string) (string, error) {
	for _, value := range d.cfgAll("git-p4.mapUser") {
		name, author, ok := strings.Cut(value, "=")
		if ok && strings.TrimSpace(name) == user {
			return strings.TrimSpace(author), nil
		}
	}
	if author := d.users[user]; author != "" {
		return author, nil
	}
	if !d.usersLoaded {
		items, e := d.p4List("users")
		if e != nil {
			return "", e
		}
		d.usersLoaded = true
		for _, item := range items {
			id := text(item["User"])
			name, email := d.metadata(item["FullName"]), d.metadata(item["Email"])
			if d.metadataError != nil {
				return "", d.metadataError
			}
			if name == "" {
				name = id
			}
			if email == "" {
				email = "a@b"
			}
			d.users[id] = strings.NewReplacer("\n", " ", "\r", " ", "<", "", ">", "").Replace(name) + " <" + strings.NewReplacer("\n", "", "\r", "", "<", "", ">", "").Replace(email) + ">"
		}
		if author := d.users[user]; author != "" {
			return author, nil
		}
		return d.metadata(user) + " <a@b>", d.metadataError
	}
	return d.metadata(user) + " <a@b>", d.metadataError
}
