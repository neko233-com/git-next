# CLI acceptance

The Go `cli-accept` command in `tools/devtool` is the sole CLI conformance gate.
It receives only the built client path and never inspects how the client was
built. The Rust acceptance runner and Python CLI suites are retired, not optional
parallel gates. Build the Go client once from the repository root, then run the
gate:

```sh
go -C tools/devtool run . build-go-client
go -C tools/devtool run . cli-accept --tier l0 --json target/cli-accept-l0.json
go -C tools/devtool run . cli-accept --tier full --json target/cli-accept-full.json
```

These commands are identical on Windows and macOS. The runner resolves `.exe` on
Windows; an explicit `git.exe` path also works. Requirements are Go 1.27.3, the
built client at `target/release/git` and reference Git on PATH for disposable
fixtures. No Python, Rust or Cargo toolchain is needed for this gate.

## Tiers and policy

| Tier | Command option | Required scope |
| --- | --- | --- |
| L0 | default / `--tier l0` | 39 top-level commands, one real scenario each; required before merge/tag. |
| Full | `--tier full` / `--full` | L0 plus local deep/broad cases and SHA-1/SHA-256 restore/reset, porcelain v2, merge and commit-object scenarios; required on release branches. |
| Network | `--tier network` / `--network` | Full plus loopback git-daemon clone/fetch/pull; optional public HTTPS ls-remote. Select explicitly for network acceptance. |
| Native product/layout | separate platform tools | MSI/PowerShell, GUI, native package/runtime, credentials and migration; separate from the independent client. |

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
The fixture template directory is empty, so active hooks bundled with an installed
reference Git cannot silently enter unrelated CLI scenarios. Explicit hook cases
still create and execute their own hooks.

## Current local results

Against the locally built Go binary on Windows x64, L0 is **39 passed / 0 failed /
0 skipped** and full is **57 passed / 43 failed / 0 skipped**. Most full-tier
failures are SHA-256 object-format cases that `go-git/v5` does not implement; the
client refuses those repositories rather than writing them incorrectly. A smaller
set differs in status hooks, nested-worktree paths and reset/diff byte output.
All are recorded as failures, not skips. Full is **not green** and does not
authorize a complete-replacement release. Historical Rust runner reports that
recorded 100/100 describe the old client only and are retained as release
evidence, not current coverage.

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

The 2026-10-25 template-isolation correction was prompted by active LFS hooks in
an installed reference Git. They made ordinary preview cases enter unsupported
LFS hook dispatch, including a recursive post-commit path stopped by the harness's
64-process job limit. Empty fixture templates restore the intended plain CLI
scenario; that correction does not establish preview LFS-hook compatibility.
The released native product uses its separately accepted bundled LFS runtime.

## Python retirement and rollback

The 30 old CLI suites are retained in Git history at commit `3609553` and removed
from the current tree. The aggregate Python runner is removed. Native HTTP/LFS/SSH
fixtures, benchmarks, documentation, build and release tooling use Go; there is no
Python wrapper or archived-suite fallback. Do not add new Python tests. Native
package, platform lifecycle and desktop acceptance retain their independent gates.

`go -C tools/devtool run . verify-repo-contract` checks the Go-only repository
contract (no Rust entrypoints or Cargo files, Go acceptance runner and Taskfile
present) and rejects active Python CLI scripts. `migration-audit --require-complete`
checks owned Python sources and execution entries. A source checkout rollback can
restore a previous harness; no Git/LFS storage, configuration, protocol or
installer migration is involved. Existing historical receipts, including Rust
runner reports, keep their original identity as historical v1.0.x release
evidence and do not become evidence for the current Go cases.
