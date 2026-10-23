# Rust CLI acceptance

`git-next-accept` is the sole CLI conformance gate. Python CLI suites are retired,
not an optional parallel gate. Build the client once from the repository root:

```sh
cargo build --release --locked
cd accept-runner
cargo build --release --locked
cargo run --release --locked --bin git-next-accept -- ../target/release/git --json ../target/accept-report.json
cargo run --release --locked --bin git-next-accept -- ../target/release/git --tier full --json ../target/accept-full.json
```

These commands are identical on Windows and macOS. The runner resolves `.exe` on
Windows; an explicit `git.exe` path also works. Requirements are Rust/Cargo, the
locked runner dependencies, a built client with its sibling LFS binary, and
reference Git on PATH. Python, venv, Paramiko and Python cryptography are not CLI
gate dependencies. `--offline` may be added once the Cargo dependencies are cached.

## Tiers and policy

| Tier | Command option | Required scope |
| --- | --- | --- |
| L0 | default / `--tier l0` | 39 top-level commands, one real scenario each; required before merge/tag. |
| Full | `--tier full` / `--full` | L0 plus local deep/broad cases and SHA-1/SHA-256 restore/reset, porcelain v2, merge and commit-object scenarios; required on release branches. |
| Network | `--tier network` / `--network` | Full plus loopback git-daemon clone/fetch/pull; optional public HTTPS ls-remote. Select explicitly for network acceptance. |
| Native product/layout | separate platform tools | MSI/PowerShell, GUI, native package/runtime, credentials and migration; separate from the slim Rust CLI. |

Tier selectors are mutually exclusive. JSON records `tier`, `passed`, `failed`,
`skipped`, `cases` and each case result. Missing required components, failed
fixtures, command errors and timeouts fail the gate. Missing LFS is never counted
as a passed scenario. Report parent directories are created as needed.

The client runs with an empty PATH and GIT_EXEC_PATH, absent SSH commands,
isolated HOME/USERPROFILE/XDG/SSH configuration and fixed fixture identities,
timestamps and interface language. Each command has a 120-second deadline.
Windows process trees are limited to 64 processes in a job object; POSIX uses a
separate process group. Process trees, git daemons and owned temporary directories
are reclaimed on exit. Reference Git creates disposable fixtures and supplies
read-only comparisons/fsck; it is never a client fallback or bundled runtime.

## Migrated local coverage

- Existing local cases: stash round trip, hooks, fast-forward merge, clone/fetch/pull,
  plumbing, diff, restore/reset and refusal of an existing writer lock.
- Restore/reset in both object formats: worktree/staged/combined/source operations,
  directory and nested-cwd paths, mixed/soft/hard/path reset. Assertions compare
  stdout, worktree bytes, logical index, HEAD/ORIG_HEAD, reflogs and fsck. Soft reset
  also preserves index bytes. Six invalid commands and four writer locks must
  preserve every repository byte.
- Porcelain v2 in both formats: unborn/tracking states, mixed staged/unstaged
  changes, deletions, renames, intent-to-add, Unicode/spaces, NUL and nested-cwd
  output. Status comparisons require unchanged repository metadata.
- Merge in both formats: clean two-parent merge, no-commit plus continuation,
  conflict plus abort; tree, parent order, index, merge state and fsck assertions.
- Commit cleanup in both formats: raw commit objects for strip/whitespace/verbatim/
  scissors compared with independently created reference commits.

This is scoped conformance. Retirement does not claim that all historical Python
assertions have been migrated. Full config permutations, every cache/index corruption
case, all conflict/submodule/status combinations, HTTPS/SSH/LFS transfer and provider
login remain separate coverage gaps. `complete_git_parity` remains false; L0/full
cannot authorize complete replacement or a native product release by themselves.

## Network configuration

`GIT_NEXT_ACCEPT_PUBLIC_HTTPS=1` enables public HTTPS ls-remote. Otherwise its case
is explicitly skipped. `GIT_NEXT_ACCEPT_SKIP_NETWORK=1` explicitly skips the
network layer and records that fact. A report with these skips is not evidence
that the skipped transport was accepted. Loopback daemon fixtures need reference
Git with `git daemon` available, while the client keeps its empty PATH.

## Python retirement and rollback

The 30 old CLI suites are retained in Git history at commit `3609553` and removed
from the current tree. The aggregate Python runner is removed. Native HTTP/LFS/SSH
fixtures, benchmarks, documentation, build and release tooling use Go; there is no
Python wrapper or archived-suite fallback. Do not add new Python tests. Native
package, platform lifecycle and desktop acceptance retain their independent gates.

`go -C tools/devtool run . verify-repo-contract` requires the Rust runner/lockfile/tier contract and rejects
active Python CLI scripts. `migration-audit --require-complete` checks owned Python
sources and execution entries. A source checkout rollback
can restore the previous harness; no Git/LFS storage, configuration, protocol or
installer migration is involved. Existing historical receipts keep their original
identity and do not become evidence for the new Rust cases.
