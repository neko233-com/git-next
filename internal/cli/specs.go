package cli

// Specs maps each command to the flags it accepts.
var Specs = map[string]Spec{
	"language":      {flags: defs(Bool, "json")},
	"diff":          {flags: diffSpec()},
	"show":          {flags: mergeSpecs(diffSpec(), defs(Value, "format", "pretty"))},
	"restore":       {flags: mergeSpecs(defs(Value, "source", "s"), defs(Bool, "staged", "S"), defs(Bool, "worktree", "W"), defs(Bool, "quiet", "q"))},
	"reset":         {flags: mergeSpecs(defs(Bool, "soft"), defs(Bool, "mixed"), defs(Bool, "hard"), defs(Bool, "quiet", "q"))},
	"eol":           {flags: mergeSpecs(defs(Bool, "apply"), defs(Bool, "json"))},
	"lfs":           {flags: mergeSpecs(defs(Bool, "local"), defs(Bool, "skip-smudge"))},
	"push":          {flags: mergeSpecs(defs(Bool, "set-upstream", "u"), defs(Bool, "dry-run", "n"), defs(Bool, "delete"), defs(Bool, "all"), defs(Bool, "tags"), defs(Bool, "force", "f"), defs(Value, "force-with-lease"), defs(Bool, "atomic"), defs(Bool, "porcelain"))},
	"add":           {flags: mergeSpecs(defs(Bool, "all", "A"), defs(Bool, "renormalize"), defs(Bool, "force", "f"))},
	"commit":        {flags: mergeSpecs(defs(Multi, "message", "m"), defs(Bool, "no-edit"), defs(Value, "cleanup"), defs(Bool, "all", "a"), defs(Bool, "allow-empty"), defs(Bool, "amend"))},
	"switch":        {flags: mergeSpecs(defs(Value, "create", "c"), defs(Bool, "detach"), defs(Bool, "discard-changes"))},
	"checkout":      {flags: mergeSpecs(defs(Value, "create", "b"), defs(Value, "create-force", "B"), defs(Bool, "detach"), defs(Bool, "force", "f"))},
	"merge":         {flags: mergeSpecs(defs(Bool, "ff-only"), defs(Bool, "no-ff"), defs(Bool, "ff"), defs(Value, "message", "m"), defs(Bool, "no-edit"), defs(Bool, "quiet", "q"), defs(Value, "cleanup"), defs(Bool, "no-commit"), defs(Bool, "abort"), defs(Bool, "continue"))},
	"stash":         {flags: mergeSpecs(defs(Value, "message", "m"), defs(Bool, "include-untracked", "u"))},
	"cherry-pick":   {flags: mergeSpecs(defs(Bool, "abort"), defs(Bool, "continue"), defs(Bool, "no-commit"))},
	"revert":        {flags: defs(Bool, "no-commit")},
	"rebase":        {flags: mergeSpecs(defs(Bool, "abort"), defs(Bool, "continue"), defs(Value, "onto"))},
	"submodule":     {flags: defs(Bool, "init")},
	"worktree":      {flags: mergeSpecs(defs(Bool, "porcelain"), defs(Bool, "detach"), defs(Bool, "force"))},
	"init":          {flags: mergeSpecs(defs(Value, "initial-branch", "b"), defs(Bool, "bare"))},
	"clone":         {flags: mergeSpecs(defs(Value, "branch", "b"), defs(Bool, "bare"), defs(Value, "depth"), defs(Bool, "quiet", "q"))},
	"fetch":         {flags: mergeSpecs(defs(Bool, "tags"), defs(Value, "depth"), defs(Bool, "all"))},
	"pull":          {flags: mergeSpecs(defs(Bool, "ff-only"), defs(Bool, "no-ff"))},
	"status":        {flags: statusSpec()},
	"log":           {flags: mergeSpecs(defs(Value, "max-count", "n"), defs(Bool, "oneline"), defs(Value, "format", "pretty"), defs(Value, "date"), defs(Bool, "nul", "z"), defs(Bool, "no-decorate"), defs(Bool, "no-color"), defs(Bool, "no-patch", "s"))},
	"rev-parse":     {flags: mergeSpecs(defs(Bool, "show-toplevel"), defs(Bool, "git-dir"), defs(Bool, "absolute-git-dir"), defs(Bool, "is-bare-repository"), defs(Bool, "short"))},
	"cat-file":      {flags: mergeSpecs(defs(Bool, "kind", "t"), defs(Bool, "size", "s"), defs(Bool, "pretty", "p"))},
	"hash-object":   {flags: defs(Bool, "write", "w")},
	"ls-files":      {flags: mergeSpecs(defs(Bool, "stage", "s"), defs(Bool, "nul", "z"))},
	"show-ref":      {flags: mergeSpecs(defs(Bool, "head"), defs(Bool, "tags"), defs(Bool, "heads"))},
	"branch":        {flags: mergeSpecs(defs(Bool, "delete", "d"), defs(Bool, "delete-force", "D"), defs(Bool, "all", "a"))},
	"tag":           {flags: mergeSpecs(defs(Bool, "annotate", "a"), defs(Value, "message", "m"), defs(Bool, "delete", "d"), defs(Bool, "force", "f"))},
	"remote":        {flags: mergeSpecs(defs(Bool, "verbose", "v"), defs(Bool, "force"))},
	"config":        {flags: configSpec()},
	"ls-remote":     {flags: mergeSpecs(defs(Bool, "heads"), defs(Bool, "tags"))},
	"compatibility": {flags: defs(Bool, "json")},
	"version":       {flags: mergeSpecs(defs(Bool, "build-options"), defs(Bool, "json"))},
	"install":       {flags: mergeSpecs(defs(Bool, "json"), defs(Bool, "hash-all"), defs(Value, "root"), defs(Value, "sample"))},
	"doctor":        {flags: defs(Bool, "json")},
}

func diffSpec() map[string]flagDef {
	return mergeSpecs(
		defs(Bool, "cached", "staged"),
		defs(Bool, "name-only"), defs(Bool, "name-status"), defs(Bool, "numstat"),
		defs(Bool, "stat"), defs(Bool, "shortstat"),
		defs(Bool, "patch", "p"), defs(Bool, "no-patch", "s"),
		defs(Value, "unified", "U"), defs(Bool, "nul", "z"), defs(Bool, "reverse", "R"),
		defs(Bool, "exit-code"), defs(Bool, "quiet"), defs(Bool, "no-renames"),
		defs(Bool, "no-ext-diff"), defs(Bool, "no-textconv"), defs(Bool, "no-color"),
		defs(Value, "color"), defs(Bool, "full-index"), defs(Bool, "text"),
		defs(Value, "diff-algorithm"), defs(Bool, "oneline"), defs(Bool, "abbrev-commit"),
		defs(Bool, "first-parent"), defs(Value, "date"),
	)
}

func statusSpec() map[string]flagDef {
	return mergeSpecs(
		defs(Value, "porcelain"), defs(Bool, "short", "s"), defs(Bool, "long"),
		defs(Bool, "branch", "b"), defs(Bool, "ahead-behind"), defs(Bool, "no-ahead-behind"),
		defs(Value, "ignore-submodules"), defs(Bool, "nul", "z"),
		defs(Value, "untracked-files", "u"), defs(Value, "ignored"),
	)
}

func configSpec() map[string]flagDef {
	return mergeSpecs(
		defs(Bool, "global"), defs(Bool, "local"), defs(Bool, "system"), defs(Bool, "worktree"),
		defs(Value, "file", "f"), defs(Bool, "get"), defs(Bool, "get-all"),
		defs(Bool, "get-regexp"), defs(Bool, "list", "l"), defs(Bool, "add"),
		defs(Bool, "replace-all"), defs(Bool, "unset"),
	)
}

// OptionalValue describes a flag whose value may be omitted. When omitted,
// the default is used without consuming the following argument unless that
// argument is one of the accepted values.
type OptionalValue struct {
	Default string
	Allowed []string
}

// OptionalValues covers the flags Git permits with or without a value.
var OptionalValues = map[string]OptionalValue{
	"porcelain":         {Default: "v1", Allowed: []string{"v1", "1", "v2", "2"}},
	"untracked-files":   {Default: "all", Allowed: []string{"no", "normal", "all"}},
	"ignored":           {Default: "traditional", Allowed: []string{"traditional", "matching", "no"}},
	"ignore-submodules": {Default: "all", Allowed: []string{"none", "untracked", "dirty", "all"}},
	"color":             {Default: "always", Allowed: []string{"always", "never", "auto"}},
	"force-with-lease":  {Default: "", Allowed: nil},
}
