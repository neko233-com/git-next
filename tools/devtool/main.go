// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type command struct {
	summary, options string
	run              func(*app, options) error
}

var commands = map[string]command{}

func register(name, summary, spec string, fn func(*app, options) error) {
	if _, exists := commands[name]; exists {
		panic("duplicate development command: " + name)
	}
	commands[name] = command{summary, spec, fn}
}

type app struct {
	root     string
	out, err io.Writer
}
type options struct {
	strings  map[string]*string
	bools    map[string]*bool
	repeated map[string][]string
	args     []string
}

func (o options) all(k string) []string {
	if v, ok := o.repeated[k]; ok {
		return append([]string{}, v...)
	}
	if v := o.get(k); v != "" {
		return []string{v}
	}
	return nil
}

func (o options) get(k string) string {
	if p := o.strings[k]; p != nil {
		return *p
	}
	return ""
}
func (o options) value(k, fallback string) string {
	if v := o.get(k); v != "" {
		return v
	}
	return fallback
}
func (o options) yes(k string) bool { p := o.bools[k]; return p != nil && *p }
func (o options) require(keys ...string) error {
	for _, k := range keys {
		if o.get(k) == "" {
			return fmt.Errorf("--%s is required", k)
		}
	}
	return nil
}
func (o options) integer(k string, fallback int) (int, error) {
	if o.get(k) == "" {
		return fallback, nil
	}
	return strconv.Atoi(o.get(k))
}
func (a *app) path(parts ...string) string {
	return filepath.Join(append([]string{a.root}, parts...)...)
}
func (a *app) json(v any) error {
	encoder := json.NewEncoder(a.out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}
func findRoot(start string) (string, error) {
	p, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(p, "Cargo.toml")) && exists(filepath.Join(p, "compatibility.json")) {
			return p, nil
		}
		parent := filepath.Dir(p)
		if p == parent {
			return "", errors.New("run inside the git-next checkout or set GIT_NEXT_ROOT")
		}
		p = parent
	}
}
func execute(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		names := make([]string, 0, len(commands))
		for name := range commands {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintln(stdout, "git-next-tool <command> [options]\nRun with go -C tools/devtool run . <command>, or build once with go build.")
		for _, name := range names {
			fmt.Fprintf(stdout, "  %-32s %s\n", name, commands[name].summary)
		}
		return nil
	}
	name := strings.ReplaceAll(args[0], "_", "-")
	c, ok := commands[name]
	if !ok {
		return fmt.Errorf("unknown command %q; use help", name)
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := options{strings: map[string]*string{}, bools: map[string]*bool{}, repeated: map[string][]string{}}
	for _, key := range strings.Fields(c.options) {
		if strings.HasSuffix(key, "?") {
			key = strings.TrimSuffix(key, "?")
			o.bools[key] = fs.Bool(key, false, key)
		} else {
			o.strings[key] = fs.String(key, "", key)
		}
	}
	flagArgs := []string{}
	positionals := []string{}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		key := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
		if _, stringFlag := o.strings[key]; stringFlag && strings.Contains(arg, "=") {
			o.repeated[key] = append(o.repeated[key], strings.SplitN(arg, "=", 2)[1])
		}
		if _, stringFlag := o.strings[key]; stringFlag && !strings.Contains(arg, "=") {
			if i+1 >= len(args) {
				return fmt.Errorf("--%s requires a value", key)
			}
			i++
			flagArgs = append(flagArgs, args[i])
			o.repeated[key] = append(o.repeated[key], args[i])
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	o.args = append(fs.Args(), positionals...)
	start := os.Getenv("GIT_NEXT_ROOT")
	if start == "" {
		start = "."
	}
	root, err := findRoot(start)
	if err != nil {
		return err
	}
	previous, err := os.Getwd()
	if err != nil {
		return err
	}
	if err = os.Chdir(root); err != nil {
		return err
	}
	defer os.Chdir(previous)
	return c.run(&app{root, stdout, stderr}, o)
}
func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
func deadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}
