# Rust parity roadmap

Goal: replace installed Git for all documented user-facing behavior. Implementation stays in Rust (`goxide` / `russh` / this repository). Behavior follows [Git's documentation](https://git-scm.com/docs). On-disk repositories stay Git-compatible. `complete_git_parity` in `compatibility.json` becomes `true` only when every row below is **accepted** (implemented + covered by local acceptance).

Fast loop while closing gaps:

```sh
go -C tools/devtool run . fast-verify
cargo test --offline --locked --lib
cargo test --offline --locked --test tag_acceptance   # when present
go -C tools/devtool run . package-rust --platform windows-x64
```

## Legend

| Status | Meaning |
| --- | --- |
| accepted | Matches Git docs for the scoped flags; acceptance recorded |
| partial | Usable subset; documented gaps |
| planned | Not started in Rust client |
| native-only | Exists only in the separate C/MSI tree; not part of the Rust product |

## Porcelain commands

| Command | Status | Notes |
| --- | --- | --- |
| add | partial | Root paths, `-A`, `--renormalize`; no magic pathspec / sparse |
| branch | partial | List, create; no `-d`, tracking, `-u` |
| tag | partial | List, lightweight, annotated `-a -m`, `-d`, `-f` |
| checkout / switch | partial | Clean ordinary-file worktrees; see COMPATIBILITY |
| clone | partial | HTTPS/SSH, shallow; local/bare rules in COMPATIBILITY |
| commit | partial | `-m`, `--no-edit`, cleanup; no `-a`, signing, `-S` |
| config | partial | Scopes and multivar subset |
| diff / show | partial | Read-command subset |
| fetch | partial | Default remote refspec |
| init | accepted | `main` default, `-b`, bare |
| log | partial | Subset of formats |
| merge | partial | Two-parent subset, continue/abort |
| pull | partial | `--ff-only` |
| push | partial | Refspec/lease subset |
| restore / reset | partial | Ordinary-file subset |
| status | partial | Porcelain v1/v2 subsets |
| remote | partial | add, get-url, show |
| lfs | partial | Pointer, batch HTTPS; not full locking |
| rebase | partial | Linear `rebase --onto` only; no interactive continue/abort |
| stash | partial | push/pop/list/apply/drop; no `-u` |
| cherry-pick | partial | Single commit, linear; no conflict continue |
| revert | partial | Single-parent linear; tip revert fast path |
| submodule | partial | status/init/update; local `file://` URLs |
| worktree | partial | add/list/remove `--force`; main checkout unchanged |
| bisect | planned | |
| grep | planned | |
| blame | planned | |
| archive | planned | |
| bundle | planned | |
| notes | planned | |
| replace | planned | |
| send-email | planned | |
| svn | planned | Out of scope unless explicitly requested |

## Plumbing and services

| Area | Status | Notes |
| --- | --- | --- |
| rev-parse, cat-file, hash-object, ls-files, show-ref, ls-remote | partial | As documented in COMPATIBILITY |
| receive-pack / upload-pack server | planned | For hosting; client transport exists |
| hooks | partial | pre-commit/post-* execution; Windows batch via temp `.cmd` |
| signing (GPG/SSH commit-sig) | planned | |
| credential helpers | partial | No native Git dispatcher |
| filters / clean-smudge | partial | LFS filter process |
| sparse checkout / partial clone | planned | |
| reftable | planned | Refuse unknown required extensions today |

## Release gate

1. Every **porcelain** row that Git documents for normal workflows is **accepted** or explicitly marked out-of-scope in `compatibility.json`.
2. `complete_git_parity: true` and `replacement_blockers: []`.
3. `go -C tools/devtool run . product-batch` (or successor) passes on optimized release binaries.
4. Rust zip/MSI path contains no MinGW, Bash, Perl, Python, or Git for Windows files.
5. Human sign-off on takeover (`install.ps1` / migration) after the above.

Until then, do not replace a user's only Git installation on production machines.
