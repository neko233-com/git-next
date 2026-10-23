# Runtime defaults

git-next is a replacement for Git, not a copy of it. The configuration it ships is
therefore a product decision rather than whatever the upstream defaults happen to be:
where a modern option is faster or easier without changing what a command means, it is
turned on.

Every default lives in one file, `packaging/runtime-defaults/gitconfig`, and every one
of them is exercised against the real binary by `go -C tools/devtool run . check-runtime-defaults`. A
default is a promise about behaviour, and the checks are the claims in this document: if
a value stops doing what is written here, the tool fails instead of the change reaching a
workstation.

## Scope of the measurements

Times are from one workstation (Windows, this repository's parent tree, git-next 0.3.0 on
Git 2.54.0.windows.1) with a 5,000-file working tree, median of five runs, taken with no
other build or benchmark running. They are reported as what they are: one machine, one
tree size, one day. The file-count and process-count facts are reproducible anywhere.

The current Windows read defaults prioritize complete results over those historical
timings. On 2026-10-23, the same frozen 1.0.3 native binary and a generated 4,000-file
repository returned only 3,999 files in round 21 with fscache/preloading enabled and
automatic index/grep threads. The five-setting serial control below completed all
30 grep/status rounds. This isolates the configuration effect; it does not establish
the internal cause or a throughput comparison. See the
[bounded read-safety evidence](benchmarks/windows-read-safety-20261023.json).

## Adding or changing a default

1. Add the key to `packaging/runtime-defaults/gitconfig`, nowhere else. The payload
   configuration, the migration override and the evidence reuse check are rendered from
   that file or compared against it.
2. Run `go -C tools/devtool run . check-runtime-defaults`. It fails if the migration's literal has
   drifted from the fragment and if the new key cannot be shown to do something.
3. Add a behavioural probe for it. A key that only has to be accepted needs no probe; a
   key that changes output or behaviour needs one that would fail if the value stopped
   being applied.
4. Record the reason and the cost in this document, with the measurement you relied on.

A default is not eligible if it starts a process per repository. See
[the monitor](#the-file-system-monitor-is-not-a-default) for what that costs.

## The defaults

| Key | Value | Why | Measured |
| --- | --- | --- | --- |
| `core.longpaths` | `true` | Paths beyond `MAX_PATH` are ordinary in game and `node_modules` trees. | A 280-character path round-trips through `add`, `status` and `commit`. |
| `core.autocrlf` / `core.eol` | `input` / `lf` | The LF policy; unchanged from before. | - |
| `core.fscache` | `false` | Part of the Windows read-safety profile; preserving complete results takes priority over cached `lstat` throughput. | The combined profile completed 30 rounds; disabling fscache alone was not sufficient in the earlier diagnostic. |
| `core.preloadIndex` | `false` | Keeps worktree stat reads serial in the Windows stability profile. Explicit configuration can still request preloading. | The combined profile completed all 4,000-file grep/status rounds. |
| `core.commitGraph` | `true` | Commit-graph makes `log` and merge-base independent of history length. | - |
| `core.quotePath` | `false` | A non-ASCII name should be readable. `status --porcelain` shows `文档.txt` instead of `"\346\226\207\346\241\243.txt"`. | Verified both ways. |
| `core.fsmonitor` | `false` | See below. | 12.1 MB and one process per repository. |
| `gc.auto` | `256` | The upstream 6,700 lets loose objects pile up until a `gc` big enough to notice. | - |
| `gc.autoDetach` | `true` | A triggered `gc` must not hold the caller. | - |
| `gc.writeCommitGraph` / `fetch.writeCommitGraph` | `true` | Keeps the graph current without a separate `gc`. | - |
| `fetch.prune` | `true` | A branch deleted upstream should stop being listed locally. | Verified: the remote-tracking ref disappears. |
| `fetch.pruneTags` | `true` | The same for tags. **This one deletes things**: a tag removed upstream is removed locally. | Verified. |
| `feature.manyFiles` | `true` | Index version 4 plus the untracked cache: the two single biggest `status` wins. | Verified: index version 4 with the `UNTR` extension. |
| `diff.algorithm` | `histogram` | Better hunks on moved code. | Verified: output matches passing `--histogram`. |
| `merge.conflictStyle` | `zdiff3` | Shows the base between the two sides and drops what all three agree on. | Verified: the base marker is present and the hunk is well formed. |
| `rebase.autoStash` | `true` | A dirty tree should not be a reason a rebase refuses to run. | Verified: rc 0 with it, rc 1 without. |
| `rebase.updateRefs` | `true` | Rebasing a stack updates the branches stacked on it. | - |
| `push.autoSetupRemote` | `true` | `git push` on a new branch works instead of printing three instructions. | Verified: upstream is set after a bare `push`. |
| `init.defaultBranch` | `main` | Already the product behaviour; stated so it survives a user config that says otherwise. | Verified: a new repository is on `main`. |
| `branch.sort` | `-committerdate` | Recently touched branches first. | Verified: `zeta` before `alpha`. |
| `tag.sort` | `version:refname` | `v1.10` after `v1.2`, not before it. | Verified: `v1.0`, `v1.2`, `v1.10`. |
| `index.threads` | `1` | Serial index reads in the shipped Windows profile. Automatic CPU-based budgets remain available with an explicit `0`. | Historical 5,000-file timing was 298 ms at `1`; the current correctness control completed 30 rounds. |
| `grep.threads` | `1` | Serial grep avoids the observed intermittent incomplete Windows results. **`-1` is fatal** (`invalid number of threads specified`). | The former automatic profile omitted one of 4,000 files; the serial control completed 30 rounds. |

These five settings apply to the shipped Windows profile and takeover defaults.
POSIX packages retain their native automatic budgets. User, repository and command-line
settings keep their normal precedence. The read-thread gate verifies the shipped values
against the authoritative fragment and requires complete results in every one of 30
rounds; timing a command that omitted files is not an accepted performance sample.

## The file-system monitor is not a default

`core.fsmonitor = true` makes `status` faster. On 5,000 files it took the median from
292 ms to 241 ms, about 17%, and the cold run that starts the monitor costs 795 ms.

It is still not a default, because of what it leaves behind:

- One daemon per repository, **12.1 MB** each. Fifteen repositories measured 181.6 MB.
- The daemon **outlives the repository**. Six repositories were created, used and
  deleted; six daemons were still alive one second later, and still alive fifteen seconds
  later. Nothing reaps them.

That last point is the one that matters. A population that only ever grows is the shape
of the failure this product must not have, and a cache that survives its own repository is
not a cache anyone can reason about. On this workstation the reaper found eight such
daemons holding 67 MB, from repositories that no longer existed.

Someone who wants the speed can have it:

```
git config --global core.fsmonitor true
go -C tools/devtool run . git-process-watchdog --daemons --apply
```

The second command terminates Git processes that have no parent and have been alive
longer than two minutes, which is what a daemon looks like; a `clone` or a `gc` still has
the process that asked for it and is left alone. It is the reason turning the monitor on
is safe now rather than a slow leak.

`maintenance.auto` is off for the same reason: it runs `git maintenance` after ordinary
commands, which is a process per command per repository.

## Long paths

Three things have to agree before a path past `MAX_PATH` works, and the product controls
two of them:

1. **The forwarder.** A target at or beyond 260 characters is rebuilt with the
   extended-length marker (`\\?\`). The marker has to be added *after* normalisation,
   because an extended-length path is not normalised and a recorded root ending in a
   separator would leave a doubled one.
2. **The configuration.** `core.longpaths = true` lives in the system configuration, so a
   repository operation accepts a long path even where a helper's Win32 manifest does
   not.
3. **The machine.** `HKLM\SYSTEM\CurrentControlSet\Control\FileSystem\LongPathsEnabled`
   is an administrator setting, not a product default. `go -C tools/devtool run . check-runtime-defaults`
   reports it, and a machine where it is 0 needs an administrator to set it.

## The ceiling

Nothing above slows a caller down, and nothing in a single Git process can stop a caller
starting a thousand copies. Three layers cover that:

- The forwarder refuses to start a copy of itself and stops a two-file cycle after four
  hops, so an entry point can never multiply itself.
- `go -C tools/devtool run . verify-windows-bridge-guard` proves it inside a job object limited to 64
  processes: `self` costs 1, `cycle` costs 5, and the previous forwarder hit the limit in
  both.
- `go -C tools/devtool run . git-process-watchdog` is the machine-wide ceiling, and it terminates the
  *newest* excess because the oldest processes are the ones a person is waiting for.
  `--daemons` handles the slower leak described above.

## Verifying and installing

```
go -C tools/devtool run . check-runtime-defaults                  # agreement and behaviour
go -C tools/devtool run . check-runtime-defaults --agreement-only # in a Linux container
go -C tools/devtool run . install-runtime-defaults                # put them on this machine
go -C tools/devtool run . install-runtime-defaults --plan         # show what is still missing
go -C tools/devtool run . verify-repo-contract                    # every rule in AGENTS.md
```

The agreement half runs anywhere; the behavioural half needs a real binary. The last
check in the run is the one that guards the incident this document came out of: after
three repositories are used with the shipped defaults, no Git process is left behind.

`go -C tools/devtool run . install-runtime-defaults` is the other half of shipping a default. It writes the
migrated system configuration, relaunching itself elevated because that file is an
administrator's; if consent is refused it falls back to the user's global configuration
rather than leaving the defaults unapplied. It never overwrites a value a person chose,
and it verifies by asking Git what each key resolves to afterwards, so "installed" means
Git reports the value rather than a file having been written.
