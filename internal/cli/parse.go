// Package cli implements the git-compatible command line interface.
package cli

import (
	"fmt"
	"strings"
)

// Invocation is the parsed global context plus the selected command.
type Invocation struct {
	Language      string
	Cwd           []string
	Config        []string
	GitDir        string
	WorkTree      string
	Bare          bool
	NoPager       bool
	SSHIdentity   string
	SSHKnownHosts string
	Help          bool
	Version       bool
	Command       string
	Args          []string
}

// Kind describes whether a flag is a boolean, takes one value, or accumulates.
type Kind int

const (
	Bool Kind = iota
	Value
	Multi
)

type flagDef struct {
	kind      Kind
	canonical string
}

// Spec describes the flags accepted by one command. Keys are flag spellings
// including their leading dash, for example "-m" and "--message".
type Spec struct {
	flags map[string]flagDef
}

func defs(kind Kind, canonical string, aliases ...string) map[string]flagDef {
	out := map[string]flagDef{}
	names := append([]string{canonical}, aliases...)
	for _, name := range names {
		if strings.HasPrefix(name, "-") {
			out[name] = flagDef{kind: kind, canonical: canonical}
		} else if len(name) == 1 {
			out["-"+name] = flagDef{kind: kind, canonical: canonical}
		} else {
			out["--"+name] = flagDef{kind: kind, canonical: canonical}
		}
	}
	return out
}

func mergeSpecs(parts ...map[string]flagDef) map[string]flagDef {
	out := map[string]flagDef{}
	for _, part := range parts {
		for key, value := range part {
			out[key] = value
		}
	}
	return out
}

// Parsed holds the result of parsing a command's arguments.
type Parsed struct {
	bools map[string]bool
	vals  map[string]string
	multi map[string][]string
	args  []string
}

func (p *Parsed) Has(name string) bool { return p.bools[name] }

// BoolValue sets a boolean flag value.
func (p *Parsed) BoolValue(name string, value bool) { p.bools[name] = value }

func (p *Parsed) Get(name string) string {
	if value, ok := p.vals[name]; ok {
		return value
	}
	if values := p.multi[name]; len(values) > 0 {
		return values[len(values)-1]
	}
	return ""
}

func (p *Parsed) All(name string) []string {
	if values := p.multi[name]; len(values) > 0 {
		return values
	}
	if value, ok := p.vals[name]; ok {
		return []string{value}
	}
	return nil
}

func (p *Parsed) Positional() []string { return p.args }

// Empty returns an empty parsed argument set.
func Empty() *Parsed {
	return &Parsed{bools: map[string]bool{}, vals: map[string]string{}, multi: map[string][]string{}, args: []string{}}
}

func resolveValue(flag flagDef, args []string, index int, hasValue bool, attached string) (string, int, error) {
	if hasValue {
		return attached, index, nil
	}
	if optional, ok := OptionalValues[flag.canonical]; ok {
		if len(optional.Allowed) == 0 {
			return optional.Default, index, nil
		}
		if index+1 < len(args) && containsString(optional.Allowed, args[index+1]) {
			return args[index+1], index + 1, nil
		}
		return optional.Default, index, nil
	}
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("option %s requires a value", flag.canonical)
	}
	return args[index+1], index + 1, nil
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// ParseCommand parses args according to spec. Unknown flags and flags with a
// missing value are errors; nothing is silently ignored.
func ParseCommand(spec Spec, args []string) (*Parsed, error) {
	parsed := &Parsed{
		bools: map[string]bool{}, vals: map[string]string{},
		multi: map[string][]string{}, args: []string{},
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			parsed.args = append(parsed.args, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			name, value, hasValue := strings.Cut(arg, "=")
			flag, ok := spec.flags[name]
			if !ok {
				return nil, fmt.Errorf("unknown option %q", name)
			}
			switch flag.kind {
			case Bool:
				if hasValue {
					return nil, fmt.Errorf("option %s does not accept a value", name)
				}
				parsed.bools[flag.canonical] = true
			case Value:
				value, next, err := resolveValue(flag, args, i, hasValue, value)
				if err != nil {
					return nil, err
				}
				i = next
				parsed.vals[flag.canonical] = value
			case Multi:
				value, next, err := resolveValue(flag, args, i, hasValue, value)
				if err != nil {
					return nil, err
				}
				i = next
				parsed.multi[flag.canonical] = append(parsed.multi[flag.canonical], value)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 && !allDigits(arg[1:]) {
			runes := []rune(arg[1:])
		shortFlags:
			for offset, r := range runes {
				name := "-" + string(r)
				flag, ok := spec.flags[name]
				if !ok {
					return nil, fmt.Errorf("unknown option %q", name)
				}
				switch flag.kind {
				case Bool:
					parsed.bools[flag.canonical] = true
				case Value, Multi:
					attached := string(runes[offset+1:])
					value, next, err := resolveValue(flag, args, i, attached != "", attached)
					if err != nil {
						return nil, err
					}
					i = next
					if flag.kind == Value {
						parsed.vals[flag.canonical] = value
					} else {
						parsed.multi[flag.canonical] = append(parsed.multi[flag.canonical], value)
					}
					break shortFlags
				}
			}
			continue
		}
		parsed.args = append(parsed.args, arg)
	}
	return parsed, nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Commands is the set of top level commands exposed by this client.
var Commands = []string{
	"language", "diff", "show", "restore", "reset", "eol", "lfs", "push",
	"add", "commit", "switch", "checkout", "merge", "stash", "cherry-pick",
	"revert", "rebase", "submodule", "worktree", "init", "clone", "fetch",
	"pull", "status", "log", "rev-parse", "cat-file", "hash-object", "ls-files",
	"show-ref", "branch", "tag", "remote", "config", "ls-remote",
	"compatibility", "version", "install", "doctor",
}

var commandSet = func() map[string]bool {
	set := map[string]bool{}
	for _, name := range Commands {
		set[name] = true
	}
	return set
}()

// Parse parses global options and splits off the command and its arguments.
func Parse(argv []string) (*Invocation, error) {
	inv := &Invocation{}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" && i+1 < len(argv) {
			inv.Command = argv[i+1]
			inv.Args = argv[i+2:]
			break
		}
		if arg == "--help" || arg == "-h" {
			inv.Help = true
			continue
		}
		if arg == "--version" || arg == "-v" {
			inv.Version = true
			continue
		}
		takeValue := func(name string) (string, error) {
			if i+1 >= len(argv) {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return argv[i], nil
		}
		switch {
		case arg == "--language":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.Language = value
		case strings.HasPrefix(arg, "--language="):
			inv.Language = strings.TrimPrefix(arg, "--language=")
		case arg == "--git-dir":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.GitDir = value
		case strings.HasPrefix(arg, "--git-dir="):
			inv.GitDir = strings.TrimPrefix(arg, "--git-dir=")
		case arg == "--work-tree":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.WorkTree = value
		case strings.HasPrefix(arg, "--work-tree="):
			inv.WorkTree = strings.TrimPrefix(arg, "--work-tree=")
		case arg == "--ssh-identity":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.SSHIdentity = value
		case strings.HasPrefix(arg, "--ssh-identity="):
			inv.SSHIdentity = strings.TrimPrefix(arg, "--ssh-identity=")
		case arg == "--ssh-known-hosts":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.SSHKnownHosts = value
		case strings.HasPrefix(arg, "--ssh-known-hosts="):
			inv.SSHKnownHosts = strings.TrimPrefix(arg, "--ssh-known-hosts=")
		case arg == "--bare":
			inv.Bare = true
		case arg == "--no-pager":
			inv.NoPager = true
		case arg == "-C":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.Cwd = append(inv.Cwd, value)
		case strings.HasPrefix(arg, "-C") && len(arg) > 2:
			inv.Cwd = append(inv.Cwd, arg[2:])
		case arg == "-c":
			value, err := takeValue(arg)
			if err != nil {
				return nil, err
			}
			inv.Config = append(inv.Config, value)
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			inv.Config = append(inv.Config, arg[2:])
		case commandSet[arg]:
			inv.Command = arg
			inv.Args = argv[i+1:]
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("unknown global option %q", arg)
			}
			return nil, fmt.Errorf("unknown command %q; use --help for a list", arg)
		}
		if inv.Command != "" {
			break
		}
	}
	return inv, nil
}

// HelpText returns a short help for a command.
func HelpText(command string) string {
	descriptions := map[string]string{
		"language":      "Manage the interface language (get|set|list)",
		"diff":          "Show changes between revisions or the worktree",
		"show":          "Show an object or a commit",
		"restore":       "Restore worktree or index files",
		"reset":         "Reset the index or the current HEAD",
		"eol":           "Check or apply the LF line-ending policy",
		"lfs":           "Manage native Git LFS",
		"push":          "Publish refs and objects",
		"add":           "Stage file changes",
		"commit":        "Create a commit",
		"switch":        "Switch branches",
		"checkout":      "Check out a branch or revision",
		"merge":         "Merge revisions",
		"stash":         "Manage the stash",
		"cherry-pick":   "Apply commits on top of the current HEAD",
		"revert":        "Revert a commit",
		"rebase":        "Replay commits onto another base",
		"submodule":     "Manage submodules (status|init|update)",
		"worktree":      "Manage linked worktrees (list|add|remove)",
		"init":          "Create a repository",
		"clone":         "Clone a repository",
		"fetch":         "Fetch refs and objects",
		"pull":          "Fetch and integrate changes",
		"status":        "Show the worktree status",
		"log":           "Show the commit history",
		"rev-parse":     "Resolve a revision or repository information",
		"cat-file":      "Inspect an object",
		"hash-object":   "Compute or store an object",
		"ls-files":      "List index files",
		"show-ref":      "List refs",
		"branch":        "List or create branches",
		"tag":           "List or create tags",
		"remote":        "Manage remotes (add|get-url|show)",
		"config":        "Read or write Git configuration",
		"ls-remote":     "List remote refs",
		"compatibility": "Show the compatibility report",
		"version":       "Show version and build information",
		"install":       "Manage the installation",
		"doctor":        "Check installation and takeover state",
	}
	if command == "" {
		return "git-next: native Git-compatible client (experimental)\n\nCommands:\n  " +
			strings.Join(Commands, "\n  ") + "\n"
	}
	return fmt.Sprintf("git %s — %s\n", command, descriptions[command])
}
