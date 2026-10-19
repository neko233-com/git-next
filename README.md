# git-next

[简体中文](README.zh-CN.md) · [Manual](docs/site/index.html) · [Compatibility](docs/COMPATIBILITY.md)

A self-contained, experimental Git client built in Rust using Gitoxide and Russh. The intended command is **git**. New repositories default to **main**; existing repositories keep their branches and object layout. Runtime code does not launch Git or OpenSSH as a fallback.

**This version is not a complete Git replacement.** Push, updating pull/checkout, merge/rebase, LFS and full ecosystem compatibility are unfinished. The installer blocks system replacement and installs a separate `git-next` preview. Do not use it as an IDE's Git executable or on a production Git server yet. No universal speedup or memory claim has been established.

## Try it

Requires Rust 1.96+ and the platform's native compiler. Source installation resolves an official full commit SHA first, tries GitHub, then pinned GitHub transport mirrors if needed. Official SHA lookup fails closed; credentials and signatures are not obtained from mirrors. Mirror availability is not guaranteed.

Windows PowerShell, after downloading and reviewing `install.ps1` from this repository:

```powershell
./install.ps1
# Open a new terminal
git-next --version
git-next compatibility --json
```

Linux x64/ARM64 or macOS ARM64, after downloading and reviewing `install.sh`:

```sh
sh install.sh
~/.local/bin/git-next --version
```

The preview command is `git-next`; a copy named `git` is stored outside PATH. Existing Git, credentials, SSH configuration and repositories are preserved. `-ReplaceGit` / `--replace-git` deliberately refuse while full parity is false. macOS Intel is not a supported target. [Installation and removal](docs/INSTALLATION.md).

## Current functionality

| Area | Implemented subset | Boundary |
| --- | --- | --- |
| Objects and refs | rev-parse, hash-object, cat-file, show-ref, ls-files, log | Not complete Git flag/output parity |
| Local repositories | init, clone, branch creation, remote/config basics | Default main; SHA-1 local clone; no linked worktrees/reftable parity |
| Native write path | add literal paths / -A, unsigned commit -m | Conflicts, active hooks, signing and in-progress operations refuse |
| Network | HTTPS/SSH clone, shallow clone, fetch, ls-remote | Push absent; refspec/prune/submodule/LFS/partial-clone parity incomplete |
| Pull | Fetch and verify branch relationship | Updating checkout refuses before changing HEAD/worktree; already-current pull works |
| SSH | Embedded Russh, strict known_hosts, key files and agent | Selected OpenSSH config subset; proxy/jump/cert/Match unsupported |
| Safety | Index locks, atomic refs, no runtime Git fallback | Experimental; isolated repositories first |

See [detailed compatibility](docs/COMPATIBILITY.md) for exclusions and [build/acceptance](docs/BUILDING.md) for reproducible gates. Explicit user-configured Git filters may run their configured programs; that is distinct from an automatic Git fallback.

## Platforms

| Platform | CPU | Target |
| --- | --- | --- |
| Windows | x64 | x86_64-pc-windows-msvc |
| Windows | ARM64 | aarch64-pc-windows-gnullvm |
| Linux | x64 | x86_64-unknown-linux-musl |
| Linux | ARM64 | aarch64-unknown-linux-gnu (glibc 2.28+) |
| macOS | ARM64 only | aarch64-apple-darwin |

Compilation is not runtime acceptance. Windows x64 has disposable reference-Git comparisons, HTTPS and loopback SSH coverage. Other platform evidence is recorded separately in the build manual. No GitHub Actions are created or run; validation and packaging are local. No formal binary release is published until its declared gates pass.

## Architecture and performance

Gitoxide supplies Git storage/protocol/index primitives; Russh supplies the built-in SSH transport. Local clones copy missing objects into an isolated temporary repository before publishing the destination, without hard links or source mutation. Fetch reuses existing objects. Worker caps avoid unbounded parallelism. Unsupported operations return errors instead of silently delegating to installed Git.

Performance must be measured per operation, repository, transport and cache state. [Measurement policy](docs/PERFORMANCE.md). Complete Git compatibility is the release prerequisite for replacing a working installation.

MIT license. See [LICENSE](LICENSE), [dependencies](Cargo.toml) and [engineering rules](AGENTS.md).

## Repository terminal

`git-terminal --cwd PATH` opens a native repository shell with the isolated preview. Windows: PowerShell 7 or Windows PowerShell, Start menu shortcut, optional folder context menu. Unix: native shell. See [terminal setup](docs/INSTALLATION.md#git-terminal). Bash/POSIX emulation and full Git replacement remain separate unfinished compatibility goals.
