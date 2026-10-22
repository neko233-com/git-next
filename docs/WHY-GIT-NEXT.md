# Why git-next

git-next is not a wrapper around Git and not a user interface on top of it. It is a
Git-compatible client with its own command, repository and protocol implementation,
built from source, that keeps the contracts your existing repositories, configuration
and tools already rely on. This page records what that buys, what was measured, and
what it does not buy.

Read [the Git guide](GIT-GUIDE.md) first if you want to use it. This page is about the
engineering.

## Where the speed comes from

Git's own startup cost is mostly not Git. A single `git status` on Windows runs a
chain of programs - the launcher, the command, a shell for hooks, sometimes a pager
and an editor - and each link pays for process creation, environment setup and path
conversion. On a warm cache that chain is often the largest part of the command.

git-next is one process. Concretely:

- **No shell and no script layer.** The commands are implemented in the binary, so
  nothing is spawned to interpret them and no MSYS path translation happens between
  a shell and a child process.
- **No fallback to an installed Git or OpenSSH.** Both are refused rather than used:
  `git version --json` reports `external_git_fallback: false` and
  `external_ssh_fallback: false`, and SSH is an embedded client (`russh`). There is
  no second program to find, version-check or start.
- **Index and object reads are direct.** Reading status, diff metadata or object
  content does not go through the command surface of another implementation; the
  files are read in the format they are stored in.
- **The stability profile avoids the Windows read race.** On Windows the shipped
  profile reads the index serially and disables `fscache`, `fsmonitor` and
  `preload`. The diagnostic that accompanied the measurements reproduced incomplete
  reads with parallel defaults on both clients; the serial profile does not. Slower
  in theory, correct in practice - and still not slower than the reference.

### Measured

From the [matched-artifact batch of 2026-10-21](benchmarks/native-integrated-performance-20261021.json):
ten pairs per operation, complete output verification, both clients on the same
declared Windows read-safety profile, reference is official Git 2.54.

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

The pattern is the point: the gap is largest where the work is smallest, which is
exactly the process-startup cost that disappears when there is only one process.
`version` is roughly twice as fast; a clean `status` on a thousand files is about a
fifth faster; at a hundred thousand files the two are within noise of each other,
because there the work is real file-system traversal rather than startup.

### What this does not prove

- **Clone is not a client-only number.** A clone includes the server building the pack.
  On a local `gitsss` server this client is 1.24x faster end to end (3379 ms against
  2726 ms), but more than half of both figures is the server: see
  [clone measurement](CLONE-BENCHMARK.md) for the request-by-request split. The earlier
  HTTPS-fixture figure of 1025 ms against 689 ms is historical and is not restated as a
  current clone result.
- **Large full scans are a latency limit, not a win.** A complete `status` over
  100,000 files takes about 1.9 s and a full worktree `grep` about 7.7 s.
- **Memory is reported for the primary process only.** It excludes child processes,
  so it is not comparable to a total for a wrapper that spawns children.
- **These are local fixtures, not your network.** The HTTPS numbers come from a
  loopback fixture, not from a remote provider, and they say nothing about throughput to
  a specific host.

The batch itself took 597 s, over the 60 s target that was set for it. No case was
skipped or relabelled to meet that target; the report keeps the original identity and
the original duration.

## What it does beyond the Git command set

Git answers "what happened in this repository". These answer "is this machine
healthy, is this installation trustworthy, and can a tool depend on the answer".

**A compatibility contract that cannot drift from behaviour.** `git compatibility`
prints the contract, and `git doctor` prints the current blockers for replacing Git
on this machine. Both read the same embedded report that the takeover guard reads,
so the printed promise and the enforced gate are one object, not two documents that
can disagree.

**Refusal instead of silent degradation.** Unsupported semantics fail with a message
instead of doing something approximately right: `git pull` without `--ff-only`,
a checkout over a dirty worktree, a non-fast-forward push, a stale force-lease, a
push refspec form it does not implement. A refusal is a result you can act on; a
half-done merge is not.

**Machine output that does not move.** Human messages are translated; `--porcelain`,
`--porcelain=v2`, `-z`, `--numstat`, `--name-status` and the JSON responses are byte
stable across interface languages. A script that parses Git output keeps working when
a user switches from English to Japanese, which is a class of bug that translated
command-line tools usually ship with.

**A line-ending policy you can inspect.** `git eol` reports the policy in force and
`git eol --json` reports it to a tool. `git add --renormalize` applies it as an
ordinary commit. Existing tracked bytes are never rewritten silently.

**Identity for launchers.** `git version --json` reports the product, version,
engine, SSH implementation, platform and whether fallbacks are enabled. An installer,
an IDE or a terminal can check what it is talking to before it depends on it, instead
of probing for files.

**An installation that can be verified and undone.** The Windows package installs
into the default Git directory, so the IDE entry point is a real binary rather than a
forwarder. Replacement requires an explicit authorization that the bootstrap verifies
from the MSI log, the leftovers of the previous installation are removed by content
hash rather than by guesswork, and rollback is refused while the package that owns the
directory is still installed.

**The terminal is part of the install.** Git-Terminal is the same product family and
the same managed installation: the shell it starts puts the git-next `bin` first on
`PATH`, so the `git` inside the terminal is the one you installed, not whichever one
happens to be on the machine.

**Languages as data.** Every interface language is a document. Adding one is a file,
not a code change, and `tools/check_locales.py` fails on the defects that would
otherwise surface as wrong output in exactly one language: a stale key, a renamed
placeholder, a lost trailing newline, a duplicate entry, an unsorted file.

**Evidence, not adjectives.** Every performance and compatibility claim on this site
points at a report under `docs/benchmarks/` or `docs/DELIVERY_STATUS.md`, with the
batch date and the exact artifact identity. When a target was missed, the report says
so.

## What it deliberately does not do

- It does not claim universal speed or memory superiority, and it does not claim full
  Git parity. `complete_git_parity` is `false` today, and the guard refuses to replace
  your installed Git while it is false.
- It does not fall back to the installed Git when something is unimplemented. That
  would make the compatibility claim untestable.
- It does not provide a Git GUI. The terminal is a terminal.

The measured scope and the open items are in [delivery status](DELIVERY_STATUS.md);
the command-by-command boundary is in [compatibility](COMPATIBILITY.md).
