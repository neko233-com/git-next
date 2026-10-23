# Native two-parent merges

> Python CLI suites referenced below are retired historical sources; they refuse direct execution. The current merge/release CLI gate is [git-next-accept](GIT_NEXT_ACCEPTANCE.md). Historical counts keep their original scope and identity.


The primary product uses the complete source-built native Git compatibility core, including merge and its ordinary conflict/recovery workflows. Current acceptance is recorded in [delivery status](DELIVERY_STATUS.md). The narrower implementation details below describe the separate historical Rust preview.

## Historical Rust preview

## Supported workflow

```sh
git-next merge --no-edit topic
git-next merge --ff-only topic
git-next merge --no-ff -m "Merge topic" topic
git-next merge --no-commit topic
git-next merge --cleanup=strip -m "Merge topic" topic
# After a supported conflict, edit the files, then:
git-next add resolved-file.txt
git-next merge --continue
# Or preserve the merge message without editor additions:
git-next commit --no-edit
# To abandon a merge started by git-next:
git-next merge --abort
```

Ordinary clean-worktree two-parent merges use native merge-base and three-way tree/blob processing. Default/`--ff` permits fast-forward, `--ff-only` rejects divergence and `--no-ff` creates a merge commit when needed. `merge.ff` and destination suppression rules are honored. Locally ahead/already-current merges retain HEAD and update ORIG_HEAD. Ordered parents, tree/index/worktree content, explicit EOL rules, symbolic/detached HEAD and HEAD/branch reflogs are compared with reference Git. SHA-1 and SHA-256 are separate fixtures.

`--no-commit` stops a successful three-way merge with the merged index/worktree and standard merge metadata, keeping HEAD unchanged. `commit --no-edit`, supported `merge --continue` or native `merge --abort` completes it. A plain fast-forward still moves HEAD, like Git; combine `--no-ff --no-commit` to stop that case before a merge commit. The current Windows optimized and installed checkpoint passes 30 pending-merge cases across both object formats; see the exact binary in [acceptance](ACCEPTANCE.md).

Simple both-modified and add/add file conflicts publish standard index stages, conflict markers when appropriate, MERGE_HEAD/MERGE_MSG/MERGE_MODE and AUTO_MERGE, then return exit 1 without moving HEAD. `add` preserves unselected conflict stages; continuing refuses unresolved entries. A preexisting ordinary single-head Git merge may also be resolved and committed. Binary conflicts retain our worktree bytes and the conflict stages. This is a subset, not complete rename/delete/directory/submodule conflict support.

`commit.cleanup` and explicit `--cleanup` support default, whitespace, strip, verbatim and scissors within noninteractive flows. Custom comment strings and conflict scissors are preserved. `merge --continue` uses edited-message cleanup without opening an editor; whitespace/verbatim modes require explicit `commit --no-edit` or `commit -m`, since reference Git retains interactive editor help in those cases. Default/strip/scissors continuation is compared with a no-op reference editor. Explicit editor options, automatic comment-character selection and non-UTF-8 commit encoding remain unsupported. [Git merge/message semantics](https://git-scm.com/docs/git-merge).

## Safety and recovery boundary

Candidate objects stay in memory until validation, worktree staging and index/ref/state locks succeed. Publication checks the original HEAD, refuses tracked/untracked collisions, and joins the existing checkout journal. Index/HEAD/branch/ORIG_HEAD/AUTO_MERGE and merge metadata locks fail before candidate objects or tracked files are published. Active custom hooks/drivers, required signing/signature verification, sparse/skip-worktree indexes, linked worktrees, complex conflicts and unsupported semantics refuse explicitly. No external Git/OpenSSH fallback is used.

Annotated tag merges require tag-message/mergetag/signature semantics which are not yet accepted, so they refuse before writes. Explicitly peeled commit revisions and lightweight tags remain ordinary commit inputs. Required message/signing configuration is validated even before a fast-forward/no-op, and unsupported autostash refuses. Unresolved rename/tree cases are checked by engine conflict type as well as index stage layout.

Conflict state also includes additive `.git/git-next-merge.json`: original HEAD, peer/materialized-tree IDs and touched paths. It contains no credentials. Native abort requires this original snapshot and current matching HEAD/peer. It restores the original index/merge paths, follows reset-merge behavior for post-merge staged changes, retains unrelated unstaged/untracked files, and refuses an unstaged change after a staged resolution or a newly untracked original file. Abort of another tool's preexisting merge, autostash/rerere and full dirty-worktree recovery are not accepted.

State-file locks, original metadata bytes, old index and tracked-file backups join `.git/git-next-checkout-*/`. Interrupted publication preserves these recovery bytes and blocks later writes. There is no automatic crash recovery or multi-file filesystem transaction. Keep the journal and inspect/recover explicitly; do not discard backups. Large-repository merge latency, memory, cross-platform native runtime and complete recovery remain separate acceptance requirements.

## Compatibility, migration and rollback

The existing Git object/ref/index/reflog layout, storage paths, LFS bytes and credential stores remain unchanged. New commits use the repository's current object format. No existing objects or refs are rewritten or repacked. The merge snapshot and journal are additive; ordinary Git can read the standard merge state. Rollback is the previous binary, which refuses active merges until completion or recovery. Supported merges add reachable objects and temporary staging/backup space proportional to selected content; no universal faster-merge claim is made.

```sh
cargo run --release --locked --manifest-path accept-runner/Cargo.toml --bin git-next-accept -- target/release/git --tier full --json target/accept-report-full.json
```

Use `.exe` on Windows. Fixtures isolate HOME/USERPROFILE, remove Git/OpenSSH from the measured client PATH, compare raw objects/state/output with reference Git and run independent fsck. No real game repository or login is modified.

The newer Windows optimized/installed merge checkpoint `e17c7ea65411c443d90c6c2b223470824a867a206d9f17e445165d8b2f77d0f2` passes the scoped LF/merge-message fixtures and retains the existing input/LF profile; see [current acceptance](ACCEPTANCE.md). Native macOS/Linux runtime acceptance for these bytes remains outstanding.
