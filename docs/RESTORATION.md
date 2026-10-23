# Native restore and reset

> Python CLI suites referenced below are retired historical sources; they refuse direct execution. The current merge/release CLI gate is [git-next-accept](GIT_NEXT_ACCEPTANCE.md). Historical counts keep their original scope and identity.


The primary product uses the complete source-built native Git compatibility core for restore, reset and recovery. Current acceptance is recorded in [delivery status](DELIVERY_STATUS.md). The narrower implementation details and installation hashes below describe the separate historical Rust preview.

## Historical Rust preview

`restore [-s SOURCE] [-S|--staged] [-W|--worktree] PATHS...` restores selected paths from the index or a tree. With `--staged`, HEAD is the default source. Combined staged/worktree restoration is supported, as are directory-relative paths, globs and exclusions. Unselected files and index entries remain intact. Worktree-only restore preserves staged object IDs while refreshing metadata for restored files. Selected target attributes and unselected current worktree attributes are snapshotted before conversion; filters run only for selected target files.

`reset [--soft|--mixed|--hard] [REVISION]` supports branch and detached HEAD updates, ORIG_HEAD, HEAD/branch reflogs, quiet output and native worktree/index updates. Unchanged-ID reset records the HEAD reflog without adding an unchanged branch entry. Path-limited reset changes only selected index entries. Unborn branches use an empty tree when the default HEAD is implicit, retaining their symbolic branch name. Explicit ambiguous or invalid revision/path combinations fail.

Index/reference locks precede mutation. Worktree updates stage selected files, back up replaced files, publish without clobbering unexpected paths and roll back ordinary file-application errors. Mixed reset and worktree publication retain a journal and prior index on interruption/publication failure. The journal uses the existing `.git/git-next-checkout-*/` path; later writers refuse until explicit recovery. No Git objects or repository trees are migrated. Automatic crash recovery remains open.

Sparse/conflicted/skip-worktree indexes, linked worktrees, symlink/submodule worktree updates, directory/file replacements, `--merge`/`--keep`, interactive/ours/theirs and complete command parity remain open. Worktree restoration refuses customized checkout hooks and transformed `.gitattributes`; ordinary reset does not execute checkout hooks. Index-only restoration can preserve Git link/submodule entry modes. Hard reset can overwrite selected ordinary untracked files when the target tree requires them, retaining unrelated untracked files. Directory/reparse-point collisions fail before application.

```sh
cargo run --release --locked --manifest-path accept-runner/Cargo.toml --bin git-next-accept -- target/release/git --tier full --json target/accept-report-full.json
```

Use `.exe` on Windows. Disposable differential fixtures compare file bytes, index entries, HEAD/branches, ORIG_HEAD, reflog messages, output and errors against reference Git. They cover CRLF attributes, partial selection, missing paths, detached/unborn HEAD, disabled reflog creation and index/HEAD/branch/ORIG_HEAD locks. Client PATH contains no Git/OpenSSH.

The newer installed native-discovery checkpoint `62d7a0cc8ee82e6eaa9254cb16da7ae24dd76e0b95426e2eb3c4acd363a4d586` also passes this suite. Existing LF settings and original profile files were verified unchanged during its upgrade; see [current acceptance](ACCEPTANCE.md).
