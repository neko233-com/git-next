# Production readiness

## Published 1.0.4: locally validated package scope · 2026-10-25

The published five-platform distribution has completed fresh builds and package checks, **14 passed / 4 user-waived correctness lanes in 243.565 seconds**, and a separate **five-lane paired-performance pass in 640.668 seconds**, ten pairs per operation. Frozen matrix: 8d8e1cd946e9e0a8963a7e3164aed5eba95cd15a98446712d220eebce949ee8f. Final staging, checksum verification and one consolidated stable/latest publication have completed. This does not claim 1.0.4 installation runtime acceptance.

There are 31 defaults with 24 actual behavior checks; Go tests/vet/race and Rust preview full 100 pass. The final Windows multi-project matrix is 33/33 with 805 tracked paths and 489 Git calls; large-LFS is 5/5. The earlier 60,037-path matrix and memory experiment used the pinned 1.0.3 core with candidate configuration and retain that historical profiling scope. They are not extra final-byte paths or real Editor acceptance.

Real Mac runs 51 native scenarios and two owned P4 dispatches; each Linux Docker target runs 52 and two P4 dispatches. Windows ARM64 has cross-compilation, PE/import and package evidence. Full host lifecycle/physical ARM acceptance is not claimed. Only terminal-native-desktop, terminal-rust-contracts, msi-user and msi-machine are waived. The live MSI waiver requires actual four-MSI static payload/table checks, isolated migration and resource probes; it is not installation runtime acceptance. All four published MSIs are unsigned (`NotSigned`).

Published release evidence: [correctness](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-correctness.json), [performance](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-performance.json), [terminal waiver](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-terminal-waiver.json), [package policy](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-package-validation-policy.json). Exact API asset digests and five anonymously downloaded assets have been verified. The following 1.0.3 MSI/workstation/publication results remain historical evidence for their original bytes.

Published release source: `06b073593a5765ff4db204f7e454584990868881`; built-code checkpoint: `3be06fa5bac1912ec7e77459830de4ebff055355`; native tree: `845e391da2d52fb9cfb580606507e1e9d791fffd591987a2b64abd9ab06d9155`. All **29** remote assets match their exact names, byte counts and SHA-256 digests; **28** are listed in SHA256SUMS. Five assets were independently downloaded over anonymous HTTPS and matched, including the Windows x64 user MSI. All four MSIs are **`NotSigned`**; digest equality is not publisher signature verification. [Publication completion](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-publication-1.0.4.json). Host MSI lifecycles and a 1.0.4 workstation installation remain unexecuted, and the two terminal lanes remain waived.

**The following paragraph retains the pre-publication checkpoint verbatim; its pending flags describe that earlier time.**

**Retained r1 checkpoint and archive-layout correction.** The earlier 14-pass/4-waived correctness result (239.890 seconds) and five-pass performance result (618.615 seconds) remain unchanged in [Git history](https://github.com/neko233-com/git-next/blob/4d61669ddf4276c52fd977f048ccb72218cd7805/docs/DELIVERY_STATUS.json) and their original local reports. r1 staging then rejected a Mac candidate whose package.json was under a product-directory prefix; it was not published. The r3 candidate places the same payload at the archive root, retains the same native core source identity, and has newly executed Mac 51/P4/Python-absence validation. The distinct r2 matrix binds that new archive and reruns all fourteen non-waived correctness lanes and all five performance lanes. This changes archive layout, not Git storage or the parser's acceptance rules. Final staging/publication remain pending, and 1.0.4 installation runtime acceptance is still false.

## Published 1.0.3: approved release scope complete · 2026-10-25

[v1.0.3](https://github.com/neko233-com/git-next/releases/tag/v1.0.3) was published
and latest at that historical checkpoint. The user explicitly accepted Docker validation, cross-compilation and
packaging, without physical ARM machines or complete host validation. Those host
checks no longer block this delivery. This closes the requested release scope;
it does not turn unexecuted hardware tests or a blocked Mac GUI preflight into passes.

The accepted native R8 batch has **16 passed correctness lanes and two user-waived
terminal lanes**, with **five passed paired-performance lanes**. Linux x64 and
emulated ARM64 each have 52 native scenarios and two owned P4 dispatch checks;
Docker login migration and frozen-input readback pass. Five platform packages,
four MSI layouts and five product Python-absence scans pass. All **28 published
asset names, sizes and SHA-256 digests** were rechecked against the immutable local
manifest. No runtime or installer source changed after the accepted release, so
these existing receipts were reused without repeating tests or rebuilding packages.
[Scope receipt](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-release-scope-20261025.json).

Windows x64 additionally has fresh 27 user-scope and 26 machine-scope MSI lifecycle
checks, actual workstation installation/readback and a successful SYSTEM update
Check. Real Apple Silicon macOS 15.5 has 51 native scenarios and two owned P4
dispatch checks. Windows ARM64 retains cross-compilation, PE/import and package
evidence. Its physical runtime and full Mac GUI/launchd/upgrade lifecycles remain
unverified and outside the approved gate. Existing ARM packages are retained.
Four MSIs remain `NotSigned`, with no signer configured. Rust remains an independent
preview with `complete_git_parity=false`; native acceptance does not promote it.
[Delivery status](DELIVERY_STATUS.md).

## Historical packaging and Rust preview readiness notes

The remainder preserves earlier packaging observations and preview gap lists.
Their host, version, runtime and installation limitations belong to those
checkpoints, not the current 1.0.3 native evidence above. Historical source or
package identities also do not establish that a GitHub release was published.

The historical gap list asked what was missing before git-next could become the
default `git`. Each item records that checkpoint's missing behavior, user cost,
completion condition and required evidence; it is not the current native release
checklist.

An earlier **1.0.2 packaging checkpoint** recorded a Linux x64 Debian 12 build, maximum glibc
2.34, and `git`, `git-remote-http`, `git-remote-https`, `git-remote-ftp` and
`git-remote-ftps` report `2.54.0.git-next.1.0.2`. Windows x64 and ARM64 machine
and user installers in that checkpoint use the `git-default-v2`
directory: `%ProgramFiles%\Git` and `%LOCALAPPDATA%\Programs\Git`. 1.0.0
replaced `git` and `git-remote-http` only; the other three scheme helpers were
still the 0.3.0 binaries, and that release had no Windows installer.
`packaging/native-compatibility.json` records parity and release eligibility as
true. The Rust preview's root `compatibility.json` still reports
`complete_git_parity: false`; the five blockers below belong to that preview
contract, not to the native package.

The Windows git program at that checkpoint is the accepted 0.3.0 engine with the
product identity rewritten to 1.0.2. This packaging host did not run MSVC 14.44.
The legacy command bridge is a new CRT-free build: full `/O2`,
`freestanding-memory.c` for `memcpy`, `memset`, `memmove` and `memcmp`, and no
`/Ob1`. The x64 bridge imports `kernel32` and `advapi32`; the GUI bridge also
imports `user32`. Cabinets use MSZIP so each MSI stays under the 1 GiB update
limit. The x64 machine package was installed under Wine into `Program Files\Git`:
`git version --json` reported 1.0.2, the HTTPS helper carried
`2.54.0.windows.1.git-next.1.0.2`, and init, commit and `git lfs` worked. Every
recorded payload hash matched. ARM64 packages carry the same identity and an
AArch64 bridge; they were not executed on this host.

## Where the line is drawn

| Layer | State | Evidence |
| --- | --- | --- |
| Command surface | Common workflow works: init, clone, fetch, pull (ff-only), push, add, commit, status, log, diff, show, branch, switch, checkout, merge, restore, reset, config, remote, rev-parse, cat-file, hash-object, ls-files, show-ref, ls-remote, eol, lfs, language, doctor, compatibility | `docs/GIT-GUIDE.md`, `--help` |
| Repository storage | Reads and writes the real format; existing repositories open unchanged | acceptance batches in `docs/benchmarks/` |
| Machine output | Byte stable across languages; stable porcelain v1/v2 | `LANGUAGE.md`, acceptance |
| Interface languages | 10 languages, complete checker | `LANGUAGE.md`, `tools/devtool/locales.go` |
| Documentation | 26 pages published, task-oriented guide | <https://neko233-com.github.io/git-next/> |
| Windows packaging | 1.0.2 x64 and ARM64 machine and user MSIs, `git-default-v2`, MSZIP, each under 1 GiB | release assets |
| Windows installation | x64 machine package installed under Wine into `Program Files\Git`; not a clean Windows VM | this 1.0.2 MSI |
| Linux / macOS | Linux x64 Debian 12 package built and executed; macOS not executed on this host | installed `git` |
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
