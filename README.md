# git-next

[简体中文](README.zh-CN.md) · [Manual](docs/site/index.html) · [Delivery status](docs/DELIVERY_STATUS.md)

git-next is a complete source-built Git client with its own product identity, cross-platform LF defaults, and native English/Simplified Chinese messages. The primary engine is the full native C command, repository and protocol implementation based on Git 2.54.0; it does not fall back to an installed Git or OpenSSH. Existing repositories, Git configuration and credential helpers keep their normal contracts.

Version 0.3.0 passed the consolidated local correctness and paired performance gates. Final publication and workstation takeover are tracked separately. [Current acceptance](docs/DELIVERY_STATUS.md) distinguishes implementation, testing, publication and installation.

## Native packages

Download accepted packages from [GitHub Releases](https://github.com/neko233-com/git-next/releases). Candidate build output is not an installation recommendation. Released Windows packages include the owned runtime and require no Rust compiler.

| Platform | Package | Boundary |
| --- | --- | --- |
| Windows x64 / ARM64 | Machine MSI or user MSI | Machine installation requires administrator rights |
| Linux x64 / ARM64 | Complete tar.gz with install.sh | Debian 12 / glibc 2.36 or compatible system |
| macOS Apple Silicon | Complete tar.gz with install.sh | macOS 15.5+; package/Mach-O/shell validation uses Dockerfile_mac |

Use the [native installation and removal guide](docs/INSTALLATION.md). Windows default-Git takeover is a separate protected migration after MSI acceptance; it preserves repository/profile data and provides a verified recovery backup. [Takeover procedure](docs/TAKEOVER.md).

## Commands and defaults

```sh
git --version
git version --json
git language get
git language set chinese
git language set english
git language set auto
git --language english status
git update check
```

`git --version` identifies git-next and its compatibility baseline. Normal Git commands, plumbing, hooks, signing, LFS, credential helpers, submodules and linked worktrees use the full native engine. User-configured hooks, filters and helper programs retain their normal Git behavior.

Unconfigured text uses LF consistently across platforms. Explicit repository attributes, binary/LFS rules and configuration retain their priority. New repositories remain empty after init. `git eol --apply` explicitly installs missing shared LF/editor policy; it does not silently rewrite every existing project. Pure line-ending changes are compared canonically, while content/serialization changes remain visible. [LF policy](docs/LINE_ENDINGS.md).

Language follows system preferences and supports persistent and per-command overrides. Machine formats remain stable. Automatic updates select official GitHub metadata, probe official/mirror download routes concurrently and verify downloaded bytes; mirrors cannot choose the release or provide its trusted identity. [Languages](docs/LANGUAGE.md) · [Updates](docs/UPDATES.md).

## Evidence and source

Validation runs locally, with independent correctness lanes in parallel and Windows Installer transactions serialized. Reports retain actual elapsed time and failures. Performance uses a separate paired batch; measured medians, p95 and primary-process memory determine claims. No universal speedup is promised. [Builds](docs/BUILDING.md) · [Performance](docs/PERFORMANCE.md) · [Compatibility](docs/COMPATIBILITY.md).

The native core is GPL-2.0-only with the declared dependency licenses and corresponding source bundles. [Source/license delivery](docs/CORRESPONDING_SOURCES.md). No GitHub Actions are used.

## Historical Rust preview

The currently installed preview is a self-contained, experimental Git client built in Rust using Gitoxide and Russh. The intended command is **git**. New repositories default to **main**; existing repositories keep their branches and object layout. Runtime code does not launch Git or OpenSSH as a fallback.

**This version is not a complete Git replacement.** Native push, clean-worktree switch/checkout and two-parent merges are implemented in the declared subset. Native LFS pointers/cache/filters and HTTPS batch/basic transfer are available in the [declared subset](docs/LFS.md). Complete merge/recovery, rebase, LFS/CLI/IDE behavior and existing-login migration remain open. The installer blocks system replacement and installs a separate `git-next` preview. Do not use it as an IDE's Git executable or on a production Git server yet. No universal speedup or memory claim has been established. [Full workstation takeover requirements](docs/TAKEOVER.md).

## Try it

Requires Rust 1.96+ and the platform's native compiler. Source installation resolves an official full commit SHA first, tries GitHub, then pinned GitHub transport mirrors if needed. Official SHA lookup fails closed; credentials and signatures are not obtained from mirrors. Mirror availability is not guaranteed.

Windows PowerShell, after downloading and reviewing `install.ps1` from this repository:

```powershell
./install.ps1
# Open a new terminal
git-next --version
git-next version --build-options
git-next doctor --json
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
| Read commands | Native diff, show and formatted log; patches, names, statistics and pathspecs | [Declared read-command subset](docs/READ_COMMANDS.md); full diff/history parity remains open |
| Status output | Default long format, porcelain v1/v2, branch/upstream headers, short/NUL, rename paths, untracked/ignored modes, pathspecs and ordinary submodule status | [Status subset](docs/READ_COMMANDS.md); full IDE/submodule/rename parity remains open |
| Restoration | Path restore, soft/mixed/hard/path reset, ORIG_HEAD and reflogs | [Ordinary-file subset](docs/RESTORATION.md); complete reset/restore parity remains open |
| Cross-platform text | LF defaults, shared Git/EditorConfig rules, canonical status and explicit renormalization | [LF policy](docs/LINE_ENDINGS.md); explicit project exceptions and binary bytes preserved |
| Local repositories | init, clone, branch creation, remote, scoped/multivar config and command context | Default main; SHA-1 local clone; no complete config/worktree/reftable parity |
| Native write path | add literal paths / -A, unsigned commit -m, declared message cleanup | Simple merge conflicts can be staged and committed; active hooks/signing and other operation states refuse |
| Network | HTTPS/SSH clone, fetch, push, tags, atomic batch, exact leases, ls-remote | Full refspec/hook/output/prune/submodule/LFS/partial-clone parity incomplete |
| Worktree | switch/checkout, branch creation, detached HEAD, pull --ff-only | Clean ordinary-file worktrees only; changes, collisions, hooks and linked worktrees refuse |
| Merge | Two-parent merges, fast-forward modes, simple conflict resolution/continue/native abort | [Declared merge subset](docs/MERGE.md); editor, complex conflicts and full recovery remain open |
| LFS | Native pointer/cache, clean/smudge/process, HTTPS upload/download and push integration | SSH discovery, locking and full LFS semantics remain open; [scope](docs/LFS.md) |
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

`--version`, `-v`, `version`, and `version --build-options` identify **git-next**; `--json` provides machine-readable product/engine identity. `doctor --json` lists the current replacement blockers. Version queries use a direct startup path without repository discovery or SSH setup.

Performance must be measured per operation, repository, transport and cache state. The status scanner uses one worker per 2000 index entries, capped at eight, to avoid starting one worker per logical CPU on small repositories. [Measurement policy and local benchmark](docs/PERFORMANCE.md). Complete Git compatibility is the release prerequisite for replacing a working installation.

MIT license. See [LICENSE](LICENSE), [dependencies](Cargo.toml) and [engineering rules](AGENTS.md).

## Repository terminal

`git-terminal --cwd PATH` opens a native repository shell with the isolated preview. Windows: PowerShell 7 or Windows PowerShell, Start menu shortcut, optional folder context menu. Unix: native shell. See [terminal setup](docs/INSTALLATION.md#git-terminal). Bash/POSIX emulation and full Git replacement remain separate unfinished compatibility goals.

System configuration uses filesystem-only discovery. Verified standalone Windows GCM support retains an existing named helper and uses native git-next callbacks. See [native discovery](docs/NATIVE_DISCOVERY.md) and [credentials](docs/CREDENTIALS.md); real-account migration and full takeover remain open.

Local LF/configuration/operation suites run in Linux x64 Docker and emulated Linux ARM64 containers. Exact accepted suite counts and source/binary identities are recorded in [acceptance](docs/ACCEPTANCE.md). Native Mac/physical ARM64, complete Linux/network performance and provider acceptance remain separate requirements.
