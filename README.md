# git-next

Windows: the [Git-Terminal 0.2.0 complete MSI bundle](https://github.com/neko233-com/git-terminal/releases/tag/v0.2.0) includes the accepted git-next 0.3.0 engine with the updated SSH/SFTP/monitoring terminal and prompt latency fixes. Existing installations should use its `install_bundle.ps1` helper; see the [component installation guide](docs/TERMINAL.md). Linux/macOS core packages are unchanged.

[简体中文](README.zh-CN.md) · [Manual](docs/site/index.html) · [Delivery status](docs/DELIVERY_STATUS.md)

git-next is a complete source-built Git client with its own product identity, cross-platform LF defaults, and native English/Simplified Chinese messages. The primary engine is the full native C command, repository and protocol implementation based on Git 2.54.0; it does not fall back to an installed Git or OpenSSH. Existing repositories, Git configuration and credential helpers keep their normal contracts.

Version 0.3.0 passed the final 18-case local correctness evidence and paired performance batch. Original Git has been uninstalled and the workstation default is git-next. Publication and the completed 10/10 scope are recorded in [delivery status](docs/DELIVERY_STATUS.md). Windows ships tested serial index/grep reads with fscache/fsmonitor/preload disabled; full large-tree scans remain a measured latency limitation, documented in [performance](docs/PERFORMANCE.md).

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

Validation runs locally, with independent correctness lanes in parallel and Windows Installer transactions serialized. Reports retain actual elapsed time and failures. Performance uses a separate paired batch; measured medians, p95 and primary-process memory determine claims. No universal speedup is promised. [Builds](docs/BUILDING.md) · [Performance](docs/PERFORMANCE.md) · [Compatibility](docs/COMPATIBILITY.md).

The native core is GPL-2.0-only with the declared dependency licenses and corresponding source bundles. [Source/license delivery](docs/CORRESPONDING_SOURCES.md). No GitHub Actions are used.

## Windows one-click installation

After obtaining the official release's `install.ps1`, run `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1`. **This automatically replaces registered Git for Windows**, preserves configuration/login/repositories and records a protected rollback transaction. `-Plan` inspects the selection without changes. Offline MSI/TLS material and checksum requirements are documented in [installation](docs/INSTALLATION.md).

Windows MSI installs the independent Rust + GPUI Fast [Git-Terminal](docs/TERMINAL.md) beside Git-next, with tabs, Unicode/IME, copy/paste, scrollback and owned Bash plus PowerShell/CMD profiles. It replaces the Git Bash desktop entry and adds no Git GUI menu. [Built-in LFS and game profiles](docs/GAME_PROJECTS.md) cover Unity/Tuanjie and Unreal; no separate LFS installer is needed.

## Source and historical preview

The native C core is GPL-2.0-only; owned packaging/extensions and the terminal carry their declared licenses. Pinned dependencies, notices and corresponding-source bundles accompany the release. No GitHub Actions are used. The independent Gitoxide/Russh Rust preview remains historical source; [the old entrypoint at its fixed revision](https://github.com/neko233-com/git-next/blob/ecee4ad91367327957d77ff4bdcb9349d8ee0511/README.md) does not describe the current installer. Follow the native installation guide above.
