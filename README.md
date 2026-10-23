# git-next

Version 1.0.2 is the current release. Linux x64 installs a Debian 12 build as the `git` command. Windows x64 and ARM64 machine and user MSIs install into the official Git directory. Their git program is the accepted 0.3.0 engine with the 1.0.2 identity, and the legacy command bridge is the rebuilt CRT-free binary. The 1.0.1 Linux package already corrected HTTPS, FTP and FTPS; 1.0.2 keeps that correction and adds the Windows packages.

[简体中文](README.zh-CN.md) · [Git guide](docs/GIT-GUIDE.md) · [Manual](docs/site/index.html) · [Why git-next](docs/WHY-GIT-NEXT.md) · [Delivery status](docs/DELIVERY_STATUS.md) · [Documentation site](https://neko233-com.github.io/git-next/)

The git-next client implements commands, repository access and transport in Rust. Behavior follows [Git's documentation](https://git-scm.com/docs), while objects, refs and the index remain Git's on-disk format. The program does not link or execute a native Git binary. Implemented commands are listed in [compatibility](docs/COMPATIBILITY.md). `complete_git_parity` stays false until those documented commands match. Existing repositories, Git configuration and credential helpers keep their normal contracts.

Version 1.0.1 is the release that replaces the `git` command. The 1.0.0 Linux package copied a new `git` but left `git-remote-https`, `git-remote-ftp` and `git-remote-ftps` as the 0.3.0 binaries, so those transports did not run the 1.0.0 core. 1.0.1 installs one core for HTTP and those three schemes. On Linux x64 the command was checked against Git 2.43.0: the same init, branch and merge produced the same commit hash. Version 0.3.0 passed the earlier 18-case local correctness evidence and paired performance batch; that record stays in [delivery status](docs/DELIVERY_STATUS.md). Windows ships tested serial index/grep reads with fscache/fsmonitor/preload disabled; full large-tree scans remain a measured latency limitation, documented in [performance](docs/PERFORMANCE.md).

## Native packages

Download accepted packages from [GitHub Releases](https://github.com/neko233-com/git-next/releases). Candidate build output is not an installation recommendation. Released Windows packages include the owned runtime and require no Rust compiler.

| Platform | Package | Boundary |
| --- | --- | --- |
| Windows x64 / ARM64 | Machine MSI or user MSI | Machine installation requires administrator rights |
| Linux x64 / ARM64 | Complete tar.gz with install.sh | Debian 12 / glibc 2.36 or compatible system |
| macOS Apple Silicon | Complete tar.gz with install.sh | macOS 15.5+; package/Mach-O/shell validation uses Dockerfile_mac |

Use the [native installation and removal guide](docs/INSTALLATION.md). The root `install.ps1` installs the accepted native MSI and automatically takes over registered Git for Windows (with UAC for machine installations); it preserves repository/profile data and provides a verified recovery backup. [Takeover procedure](docs/TAKEOVER.md).

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

Rust CLI conformance has one local merge/tag gate: [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md), with L0/full/network tiers and JSON reports. It runs on Windows/macOS without Python; release branches require full. Python CLI suites are retired immediately under `tests/legacy_cli/`, with no runnable wrapper. Native package/installer/GUI acceptance remains a separate scope.

Validation runs locally, with independent correctness lanes in parallel and Windows Installer transactions serialized. Reports retain actual elapsed time and failures. Performance uses a separate paired batch; measured medians, p95 and primary-process memory determine claims. No universal speedup is promised. [Builds](docs/BUILDING.md) · [Performance](docs/PERFORMANCE.md) · [Compatibility](docs/COMPATIBILITY.md).

The native core is GPL-2.0-only with the declared dependency licenses and corresponding source bundles. [Source/license delivery](docs/CORRESPONDING_SOURCES.md). No GitHub Actions are used.

## Rust package

Development tooling follows [Rust acceptance + Go tooling](docs/GO_TOOLING.md). Go replaces the owned Python build/package/docs controllers; native build/MSI/migration capabilities are retained until their replacements have equivalent evidence. The migration audit reports remaining Python files explicitly.

`go -C tools/devtool run . package-rust --platform windows-x64` writes a zip of the Rust `git`, LFS entrypoints, and Git-Terminal. The zip contains no MinGW, Bash, Perl, Python, or Git for Windows files. Its `install.ps1` copies them to `%LOCALAPPDATA%\Programs\git-next` and does not replace a registered official Git directory. See [installation](docs/INSTALLATION.md).

## Windows one-click installation

After obtaining the official release's `install.ps1`, run `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1`. **This automatically replaces registered Git for Windows**, preserves configuration/login/repositories and records a protected rollback transaction. `-Plan` inspects the selection without changes. Offline MSI/TLS material and checksum requirements are documented in [installation](docs/INSTALLATION.md).

Windows MSI installs the independent Rust + GPUI Fast [Git-Terminal](docs/TERMINAL.md) beside Git-next, with tabs, Unicode/IME, copy/paste, scrollback and owned Bash plus PowerShell/CMD profiles. It replaces the Git Bash desktop entry and adds no Git GUI menu. [Built-in LFS and game profiles](docs/GAME_PROJECTS.md) cover Unity/Tuanjie and Unreal; no separate LFS installer is needed.

## Source and historical preview

The native C core is GPL-2.0-only; owned packaging/extensions and the terminal carry their declared licenses. Pinned dependencies, notices and corresponding-source bundles accompany the release. No GitHub Actions are used. The independent Gitoxide/Russh Rust preview remains historical source; [the old entrypoint at its fixed revision](https://github.com/neko233-com/git-next/blob/ecee4ad91367327957d77ff4bdcb9349d8ee0511/README.md) does not describe the current installer. Follow the native installation guide above.
