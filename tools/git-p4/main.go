// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type cliOptions struct {
	command string
	values  map[string][]string
	flags   map[string]bool
	args    []string
}
type usageError struct{ error }

func (o cliOptions) value(key string) string {
	v := o.values[key]
	if len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

var syncFlags = "detect-branches silent detect-labels import-labels import-local keep-path use-client-spec"
var syncValues = "branch changesfile max-changes changes-block-size /"

func parseCLI(args []string) (cliOptions, error) {
	o := cliOptions{values: map[string][]string{}, flags: map[string]bool{}}
	if len(args) == 0 {
		return o, fmt.Errorf("a git p4 command is required")
	}
	o.command = args[0]
	flags, values := "verbose help", "git-dir"
	switch o.command {
	case "clone":
		values = ""
		flags += " " + syncFlags + " bare"
		values += " " + syncValues + " destination"
	case "sync":
		flags += " " + syncFlags
		values += " " + syncValues
	case "submit", "commit":
		flags += " M preserve-user export-labels dry-run prepare-p4-only shelve disable-rebase disable-p4sync no-verify"
		values += " origin conflict branch update-shelve commit"
	case "rebase":
		flags += " import-labels"
	case "unshelve":
		values += " origin"
	case "branches":
	case "help", "--help", "-h":
		o.flags["help"] = true
		return o, nil
	default:
		return o, fmt.Errorf("unknown git p4 command %s", o.command)
	}
	flagSet, valueSet := map[string]bool{}, map[string]bool{}
	for _, k := range strings.Fields(flags) {
		flagSet[k] = true
	}
	for _, k := range strings.Fields(values) {
		valueSet[k] = true
	}
	aliases := map[string]string{"v": "verbose", "h": "help", "n": "dry-run"}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.args = append(o.args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			o.args = append(o.args, arg)
			continue
		}
		if !strings.HasPrefix(arg, "--") && !strings.HasPrefix(arg, "-/") && len(arg) > 2 {
			keys := []string{}
			for _, ch := range arg[1:] {
				key := string(ch)
				if alias := aliases[key]; alias != "" {
					key = alias
				}
				if !flagSet[key] {
					keys = nil
					break
				}
				keys = append(keys, key)
			}
			if len(keys) > 0 {
				for _, key := range keys {
					o.flags[key] = true
				}
				continue
			}
		}
		key := strings.TrimLeft(arg, "-")
		key, value, assigned := strings.Cut(key, "=")
		if strings.HasPrefix(arg, "-/") && len(arg) > 2 {
			key = "/"
			value = arg[2:]
			assigned = true
		}
		if alias := aliases[key]; alias != "" {
			key = alias
		}
		if flagSet[key] {
			if assigned {
				return o, fmt.Errorf("flag --%s takes no value", key)
			}
			o.flags[key] = true
			continue
		}
		if !valueSet[key] {
			matches := []string{}
			for candidate := range flagSet {
				if strings.HasPrefix(candidate, key) {
					matches = append(matches, candidate)
				}
			}
			for candidate := range valueSet {
				if strings.HasPrefix(candidate, key) {
					matches = append(matches, candidate)
				}
			}
			if len(matches) != 1 {
				return o, fmt.Errorf("unknown or ambiguous option %s", arg)
			}
			key = matches[0]
			if flagSet[key] {
				if assigned {
					return o, fmt.Errorf("flag --%s takes no value", key)
				}
				o.flags[key] = true
				continue
			}
		}
		if !assigned {
			i++
			if i >= len(args) {
				return o, fmt.Errorf("option --%s needs a value", key)
			}
			value = args[i]
		}
		if key == "/" {
			value = "/" + value
		}
		o.values[key] = append(o.values[key], value)
	}
	if value := o.value("conflict"); value != "" && value != "ask" && value != "skip" && value != "quit" {
		return o, fmt.Errorf("invalid conflict behavior %s", value)
	}
	return o, nil
}
func usage(w io.Writer, command string) {
	fmt.Fprintln(w, "usage: git p4 <clone|sync|rebase|submit|commit|unshelve|branches> [options]")
	fmt.Fprintln(w, "Common options: --verbose (-v), --help (-h); --git-dir DIR except clone")
	switch command {
	case "clone", "sync":
		fmt.Fprintf(w, "%s flags: --%s\n", command, strings.ReplaceAll(syncFlags, " ", ", --"))
		fmt.Fprintln(w, "Values: --branch REF, --changesfile FILE, --max-changes N, --changes-block-size N, -/DEPOT")
		if command == "clone" {
			fmt.Fprintln(w, "Clone: --destination DIR, --bare; DEPOT[@RANGE|#REV] ...")
		}
	case "submit", "commit":
		fmt.Fprintln(w, "Flags: -M, --preserve-user, --export-labels, --dry-run (-n), --prepare-p4-only, --shelve, --disable-rebase, --disable-p4sync, --no-verify")
		fmt.Fprintln(w, "Values: --origin REF, --conflict ask|skip|quit, --branch REF, --update-shelve CHANGE, --commit COMMIT|RANGE; optional Git branch")
	case "unshelve":
		fmt.Fprintln(w, "--origin REF; one shelved changelist number")
	case "rebase":
		fmt.Fprintln(w, "--import-labels")
	}
}
func executeP4(args []string, out, errout io.Writer) error {
	o, e := parseCLI(args)
	if e != nil {
		return usageError{e}
	}
	if o.flags["help"] {
		usage(out, o.command)
		return nil
	}
	d, e := newDriver(out, errout)
	if e != nil {
		return e
	}
	d.verbose = o.flags["verbose"]
	dir := o.value("git-dir")
	if dir == "" {
		dir = os.Getenv("GIT_DIR")
	}
	if dir != "" && o.command != "clone" {
		if info, e := os.Stat(filepath.Join(dir, ".git")); e == nil && (info.IsDir() || info.Mode().IsRegular()) {
			dir = filepath.Join(dir, ".git")
		}
		dir, e = filepath.Abs(dir)
		if e != nil {
			return e
		}
		if e = os.Setenv("GIT_DIR", dir); e != nil {
			return e
		}
	}
	switch o.command {
	case "clone", "sync":
		return d.sync(o)
	case "branches":
		return d.branches()
	case "rebase":
		if e = d.sync(cliOptions{command: "sync", values: map[string][]string{}, flags: map[string]bool{"import-labels": o.flags["import-labels"]}}); e != nil {
			return e
		}
		return d.rebase()
	case "submit", "commit":
		return d.submit(o)
	case "unshelve":
		return d.unshelve(o)
	}
	return fmt.Errorf("unhandled git p4 command")
}
func main() {
	if e := executeP4(os.Args[1:], os.Stdout, os.Stderr); e != nil {
		fmt.Fprintln(os.Stderr, "git-p4:", e)
		var usage usageError
		if errors.As(e, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
