# Production readiness

This page answers one question: what is still missing before git-next can be the
default `git` on a machine that matters. It is a gap list, not a roadmap of wishes.
Every item names what is missing, what it costs the user today, what "done" means,
and what evidence would close it.

The current release is **1.0.0**. Linux x64 installs that package as the `git`
command. `packaging/native-compatibility.json` records parity and release
eligibility as true. The Rust preview's root `compatibility.json` still reports
`complete_git_parity: false`; the five blockers below belong to that preview
contract, not to the native 1.0.0 Linux package. A Windows MSI built from this
1.0.0 source is not attached to the release.

The MSVC 14.44 CRT-free bridge link is no longer an open choice between `/O2 /Ob1`
and a freestanding `memcpy`. Full `/O2` stays, and `freestanding-memory.c` supplies
`memcpy`, `memset`, `memmove` and `memcmp`. `/Ob1` would also turn off inlining in
the forwarder, so it is not the fix. The next Windows package has to rebuild the
bridge; the assembler rejects a receipt that does not name the current helper
source. The Linux proof of that link is not a substitute for the MSVC packaging
run, and it does not close the list below.

## Where the line is drawn

| Layer | State | Evidence |
| --- | --- | --- |
| Command surface | Common workflow works: init, clone, fetch, pull (ff-only), push, add, commit, status, log, diff, show, branch, switch, checkout, merge, restore, reset, config, remote, rev-parse, cat-file, hash-object, ls-files, show-ref, ls-remote, eol, lfs, language, doctor, compatibility | `docs/GIT-GUIDE.md`, `--help` |
| Repository storage | Reads and writes the real format; existing repositories open unchanged | acceptance batches in `docs/benchmarks/` |
| Machine output | Byte stable across languages; stable porcelain v1/v2 | `LANGUAGE.md`, acceptance |
| Interface languages | 10 languages, complete checker | `LANGUAGE.md`, `tools/check_locales.py` |
| Documentation | 26 pages published, task-oriented guide | <https://neko233-com.github.io/git-next/> |
| Windows packaging | MSI rebuilt and structurally verified (10,601 payload files) | `DELIVERY_STATUS.md` |
| Windows installation | **Not executed on a clean machine** | blocked here by `msiexec` 1619 |
| Linux / macOS | Built and checked; no runtime acceptance on real hardware | `DELIVERY_STATUS.md` |
| Push / server side | Common cases only; no receive-pack server | `doctor` blocker 1 |
| Recovery semantics | No rebase, stash, cherry-pick, revert | `doctor` blocker 3 |

## The five blockers, and what each one means

These are the strings `git doctor` prints. They are generated from
`compatibility.json`, the same file the takeover guard reads, so the printed promise
and the enforced gate cannot drift.

**1. Push refspec, hook and output parity; no receive-pack server.**
Push works for ordinary branch and tag refspecs, force-with-lease, atomic and
dry-run, including local, HTTPS and embedded SSH. What is missing: wildcard refspecs,
multiple push URLs, exact output parity, hook execution on the receiving side, and the
`receive-pack` server command itself. *Cost today:* a CI system or a hook that expects
Git's push output or a hosted server role cannot move yet. *Done means:* a refspec
matrix that matches Git case for case, a hook bridge with the same environment, and a
protocol conformance run against a real server. *Evidence:* a push acceptance report
with exact stdout comparison.

**2. LFS transport and locking; credential-helper/login compatibility.**
Pointer handling, `track`, `install` and the local workflow work. The full transport
(fetch/push of LFS objects against a real LFS server), locking, and arbitrary
credential helpers are not accepted. *Cost today:* a repository with LFS content can
be cloned but not reliably completed, and a custom credential helper may not be
honoured. *Done means:* a real LFS server in the acceptance path, lock/unlock
round trips, and the documented helper protocol implemented over a helper that is not
Git Credential Manager. *Evidence:* LFS end-to-end and helper matrix reports.

**3. Merge and recovery semantics.**
Ordinary two-head merges, conflicts and conservative abort work. Missing: rebase,
stash, cherry-pick, revert, and full diff semantics. *Cost today:* the workflow most
teams use to keep a branch current (rebase) is unavailable, and a mistaken commit
cannot be reverted without a manual `restore`. *Done means:* sequencer state
compatible with Git's, abort/continue for each operation, and byte-identical patch
output for the common diff options. *Evidence:* a recovery acceptance batch,
including interrupted operations.

**4. CLI, IDE, hooks, signing, submodule and linked-worktree compatibility.**
The command line is a subset, hooks are not executed, commits are not signed, and
submodules and linked worktrees are refused rather than handled. *Cost today:* a team
that signs commits or uses submodules cannot switch, and an IDE that shells out to
`git` with uncommon flags may hit a refusal. *Done means:* hook execution with the
documented environment, signing via the configured program or key, submodule
init/update, linked worktrees, and the IDE call patterns recorded as fixtures.
*Evidence:* IDE call-log fixtures plus a signed-commit and submodule acceptance batch.

**5. Checkout requires a clean ordinary-file worktree.**
Refusing a dirty checkout is safer than doing half of it, but it is not parity: Git
carries staged and unstaged changes across a branch switch when it can, and it
recovers from an interrupted checkout. *Cost today:* switching branches with work in
progress requires commit or restore first. *Done means:* the merge-in-index algorithm
for carried changes, plus crash recovery with a journal. *Evidence:* a checkout
matrix over dirty/staged/conflicted states and an interrupted-checkout test.

## Beyond the blockers

**The installer now installs, and it took four defects to get there.** `msiexec`
returns 1619 from a non-elevated shell here, but from an elevated one the package
installs, repairs, upgrades over an existing product and uninstalls, with receipts in
[the installer proof](MSI-PROOF.md). Getting to that state found four real defects: a
custom action could roll back a correct installation over an optional integration;
custom action failures produced a bare `Error 1722` with no reason; the entry points
silently stayed as 8 KB forwarders because Windows Installer keeps a same-version file;
and the uninstall guard refused a taken-over installation without saying so.

What is still unproven: a first install on a **clean** machine, the uninstall of a
**taken-over** installation (it requires a PowerShell session, which this environment
cannot provide), and an upgrade from a **real previous release** artifact rather than
from a second build of the same generator. *Done means:* those three on a clean Windows
VM, plus the IDE finding `cmd\git.exe` as the 5.2 MB payload afterwards.

**Two platforms have no runtime evidence.** Linux x64/ARM64 and macOS Apple Silicon
build and pass static checks; runtime acceptance on real hardware or a container that
matches it has not been recorded. *Done means:* the acceptance batch executed on each
platform, with the same report format as Windows.

**The performance batch misses its own 60-second target.** The measured batches took
597 s and 1389 s. That target was set for the batch, not for a command, but a batch
that cannot finish cannot gate a release cheaply. *Done means:* either the target is
restated as a per-command budget, or the matrix is split so each part fits.

**The HTTP clone path had a fatal configuration interaction.** A `http.sslcainfo`
pointing at a bundle that rustls cannot parse (curl's `ca-bundle.trust.crt` is the
common case on Windows) made *every* HTTP and HTTPS remote fail, including plain
`http://`. This is fixed in this batch: TLS settings are only applied to TLS URLs, and
an unparsable bundle no longer aborts the connection. It is listed here because a
fault of this class - one configuration value disabling all network operations - is
what "production" means you must not have, and the fix needs a regression test with a
`trust.crt` bundle.

**Update delivery is local only.** The MSI and the bundle scripts work, but there is
no signed update channel and no rollback rehearsal beyond the documented procedure.
*Done means:* a signed update manifest, an upgrade from N-1 and a rollback rehearsal,
each with evidence.

**A production checklist**

- [ ] Five `doctor` blockers cleared, `compatible` contract updated in the same commit
- [ ] `release_eligible: true` in `packaging/native-compatibility.json`, produced by a real batch
- [ ] MSI install → upgrade → repair → uninstall on a clean Windows VM
- [ ] Takeover and rollback rehearsed on that VM, IDE check included
- [ ] Runtime acceptance on Linux x64, Linux ARM64 and macOS Apple Silicon
- [ ] TLS regression test with a `ca-bundle.trust.crt` configured
- [ ] Push refspec/hook matrix and LFS end-to-end against a real server
- [ ] Signed update channel with rollback evidence
- [ ] Performance target restated as a per-command budget and enforced in the batch

## What is already production-grade

It is worth being explicit, because the list above is long enough to read as "nothing
works":

- **Repository safety.** Atomic ref and index updates, writer locks, refusal over
  speculative writes, no silent fallback to an installed Git or OpenSSH.
- **Determinism.** Machine formats do not change with the interface language; the
  contract printed by `compatibility` is the contract enforced by the guard.
- **Performance.** Measured faster than the reference on local operations and on a
  local `gitsss` clone; see [clone measurement](CLONE-BENCHMARK.md).
- **Language and documentation.** A new language is a file with an automated checker;
  the manual is written for people who do not know Git yet.
