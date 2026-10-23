# Why git-next

The native product and the 1.0.3 candidate use a source-built native C Git engine,
with git-next's identity, LF policy, language support and installation/update behavior.
They include an owned runtime and preserve existing Git repository, configuration
and helper contracts. The independent Rust client is a separate preview; its
implementation choices and compatibility limits do not describe the native installer.
This page separates the current architecture from historical measurements.

Read [the Git guide](GIT-GUIDE.md) first if you want to use it. This page is about the
engineering.

## Native runtime and process boundaries

The native core reads Git objects, refs and indexes using its Git implementation.
It does not dispatch ordinary commands to a separately installed system Git. Its
runtime includes owned OpenSSH, shell and helper components; hooks, filters,
subcommands, transport and user-configured tools can start child processes.
There is no general one-process or shell-free guarantee. Embedded `russh` belongs
to the independent Rust preview, while the native product uses its owned SSH runtime.
Neither product may silently fall back to installed Git or OpenSSH.

Go owns build, packaging, documentation, fixtures, benchmarks and release tooling,
and the product's `git-p4` helper. Necessary PowerShell and POSIX shell scripts
remain. The 115-file Python retirement removes the owned Python toolchain and
product helper dependency; it does not remove Git's supported helper interfaces.
[Tooling](GO_TOOLING.md) and [delivery status](DELIVERY_STATUS.md) define that scope.

Windows uses serial index/grep reads and disables `fscache`, `fsmonitor` and
`preload` through the authoritative defaults fragment. In the
[2026-10-23 same-core control](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-read-safety-20261023.json), the
former automatic profile returned 3,999 of 4,000 files at round 21; the stable
profile completed all 30 grep/status rounds and six contract checks. This supports
the scoped read-safety choice, not an internal root-cause or speed claim. Explicit
user/repository/command-line settings retain their normal priority.
[Defaults](DEFAULTS.md).

## Current measurements · 2026-10-24

Native 1.0.3 completed its exact R6 correctness batch with 16 passed lanes and
two terminal lanes waived by explicit user instruction, in 246.204 seconds.
All five separate performance lanes passed in 691.251 seconds. The waiver does
not cover performance, MSI or other product checks. The
[source checkpoint](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.json)
records staging, workstation update and publication as pending at that point;
later completion is recorded separately. Failed candidates remain preserved.

On Windows x64, ten warm paired samples per operation compare native
`2.54.0.windows.1.git-next.1.0.3` with reference `2.56.0.windows.3`, using matching
declared read-safety settings. Selected medians are:

| Workload | git-next | Reference Git |
| --- | --- | --- |
| 1000-file `version` | 13.244 ms | 23.441 ms |
| 1000-file clean `status` | 35.494 ms | 46.786 ms |
| 100000-file clean `status` | 1656.999 ms | 1666.073 ms |
| 100000-file full `grep` | 9698.981 ms | 10391.285 ms |
| Loopback HTTPS `clone` | 1078.937 ms | 753.106 ms |
| Loopback HTTPS `fetch` | 439.677 ms | 451.569 ms |
| Loopback HTTPS `pull --ff-only` | 622.031 ms | 627.105 ms |

[Performance details](PERFORMANCE.md) retain every selected operation's p95 and
the full artifact-bound reports. These samples show lower startup latency in
this fixture, small differences for large scans and a slower HTTPS clone.
They do not establish universal speed or memory superiority: primary-process
peak working set excludes children and is larger for native status here.
The network fixture uses loopback HTTP/1.1, not a WAN or provider login.
Both complete batches exceeded the 60-second target without skipping checks.

The first 100,000-file attempt failed at native `grep` without a valid latency
report. A later deadline probe established that a timeout can cause exit 1 with
empty stderr and partial output; the original failure's root cause remains
unproved. R6 adds large-scale budget headroom and correct deadline reporting,
then repeats the complete strict measurements. The failed attempt is retained.

## Historical measurements · 2026-10-21

The following table belongs to the historical native artifact in the
[matched-artifact batch of 2026-10-21](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-integrated-performance-20261021.json),
whose source-tree identity starts `fb505db8`. It used ten pairs per operation,
complete output verification and the declared Windows read-safety profile for both
clients; the reference was official Git 2.54. These are not 1.0.3 measurements or a
comparison against Git 2.56. Current candidate performance requires its own final
artifact-bound report, tracked in [delivery status](DELIVERY_STATUS.md).

| Files | Operation | git-next | Git 2.54 |
| --- | --- | --- | --- |
| 1000 | `version` | 11.4 ms | 20.2 ms |
| 1000 | `status` (clean) | 34.9 ms | 42.3 ms |
| 1000 | `diff` (clean) | 29.2 ms | 37.0 ms |
| 1000 | `show` (metadata) | 15.0 ms | 22.7 ms |
| 1000 | `grep` | 78.8 ms | 87.9 ms |
| 10000 | `status` (clean) | 239 ms | 252 ms |
| 100000 | `status` (clean) | 1911 ms | 1920 ms |
| HTTPS | `fetch` | 290 ms | 299 ms |
| HTTPS | `pull --ff-only` | 373 ms | 379 ms |

These selected historical medians show a larger relative difference for short
operations and a small difference for the large clean scan. They do not isolate
process startup as the cause, prove a one-process architecture, or establish an
advantage for other machines, workloads or versions.

### What this does not prove

- **Clone is not a client-only number.** A separate historical
  [2026-10-22 local gitsss fixture](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/clone-gitsss-20261022.json), against
  Git 2.55.0.windows.3, recorded 3379 ms for the reference and 2726 ms for git-next
  (1.24x). Clone includes server pack generation; this fixture does not establish
  current 1.0.3 performance or throughput to a remote provider. See the historical
  [clone measurement](CLONE-BENCHMARK.md) for its method and limits.
- **Large full scans remain a measured latency limit.** The 2026-10-21 artifact's
  complete `status` over 100,000 files took about 1.9 s and full worktree `grep`
  about 7.7 s; these are dated measurements, not current-version guarantees.
- **Memory is reported for the primary process only.** It excludes child processes,
  including owned helpers, so it does not establish total process-tree memory.
- **These are local fixtures, not your network.** The HTTPS numbers come from a
  loopback fixture, not from a remote provider, and they say nothing about throughput to
  a specific host.

The 2026-10-21 batch took 596.63 s, over its 60 s target. No case was skipped or
relabelled to meet that target; the report retains its original identity and duration.

## What it does beyond the Git command set

Git answers "what happened in this repository". These answer "is this machine
healthy, is this installation trustworthy, and can a tool depend on the answer".

**Separate product and preview contracts.** The native distribution retains Git's
command and repository semantics, with the product extensions and platform scope
described in [compatibility](COMPATIBILITY.md) and [delivery status](DELIVERY_STATUS.md).
The independent Rust preview has a smaller declared command subset and its own
`git compatibility`/`git doctor` contract. Preview refusals, such as its documented
`pull --ff-only` boundary, must not be presented as native product limitations.

**Machine interfaces separate from translation.** Git's `--porcelain`,
`--porcelain=v2`, `-z`, `--numstat` and `--name-status` output and the product's
declared JSON schemas remain machine interfaces. Human English/Chinese translation
is separate; the independent Rust preview's accepted output subset is documented
under its own compatibility scope.

**A line-ending policy you can inspect.** `git eol` reports the policy in force and
`git eol --json` reports it to a tool. An explicit `git add --renormalize` stages
normalization for review and a normal commit. Existing tracked bytes are never
rewritten silently.

**Identity for launchers.** `git version --json` identifies the executing product
and its declared compatibility information. Check the actual artifact's report;
Rust preview implementation fields must not be assumed to describe a native binary.

**An installation that can be verified and undone.** Windows MSI packages install
the owned CLI/runtime with scoped registration and guarded compatibility bridges.
Takeover and recovery preserve configuration and verify owned content; lifecycle
guards protect the installed package and its rollback transaction. Actual final-byte
installation, repair and rollback results are recorded in
[delivery status](DELIVERY_STATUS.md); candidate build output alone is insufficient.

**The terminal is part of the install.** Git-Terminal is the same product family and
the same managed installation: the shell it starts puts the git-next `bin` first on
`PATH`, so the `git` inside the terminal is the one you installed, not whichever one
happens to be on the machine.

**Declared language implementations.** The native product provides owned
English/Chinese catalogs and language selection. The independent Rust preview
embeds locale documents; `tools/devtool/locales.go` checks those documents for
stale keys, placeholders, trailing newlines, duplicates and ordering. That Rust
locale workflow is separate from the native catalog implementation.

**Evidence with a declared scope.** Performance and compatibility claims require
dated reports and an artifact binding. Historical fixture reports keep their original
inputs and limitations; they cannot establish the current candidate's acceptance.
Missed targets and failed checks remain recorded in the reports and
[delivery status](DELIVERY_STATUS.md).

## What it deliberately does not do

- It does not claim universal speed or memory superiority. Native release eligibility
  depends on its actual package, platform, lifecycle and performance evidence.
- The independent Rust preview keeps `complete_git_parity: false`; its 39-command
  L0 and 100-scenario full suite are scoped conformance, not complete Git parity or
  authorization to replace an installed native Git product.
- It does not fall back to the installed Git when something is unimplemented. That
  would make the compatibility claim untestable.
- It does not provide a Git GUI. The terminal is a terminal.

The native product's measured scope and open items are in
[delivery status](DELIVERY_STATUS.md); the independent Rust preview's command subset
and historical receipts are in [compatibility](COMPATIBILITY.md).
