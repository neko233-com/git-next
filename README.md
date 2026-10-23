# git-next

The 1.0.3 distribution uses a source-built native C Git engine with git-next's identity, LF policy, language support and installation/update behavior. Native packages include their owned runtime; existing repositories, Git configuration, hooks and credential-helper contracts retain their normal behavior. Local correctness and paired performance gates are complete within the recorded scope. Release staging, workstation activation, push and publication are tracked separately in [delivery status](docs/DELIVERY_STATUS.md).

[简体中文](README.zh-CN.md) · [Git guide](docs/GIT-GUIDE.md) · [Manual](docs/site/index.html) · [Why git-next](docs/WHY-GIT-NEXT.md) · [Delivery status](docs/DELIVERY_STATUS.md) · [Documentation site](https://neko233-com.github.io/git-next/)

Go now owns build, packaging, documentation, fixtures, benchmarks and release tooling, including the product's `git-p4` helper. This migration retires 115 inventoried Python files; owned tooling and product helpers require no Python interpreter. Necessary PowerShell and POSIX shell scripts remain. Thirty historical CLI suites are retained in Git history at `3609553`; the current tree contains no runnable Python CLI wrapper. [Tooling](docs/GO_TOOLING.md).

The independent Rust client is a preview with its own [Rust acceptance runner](docs/GIT_NEXT_ACCEPTANCE.md). Windows x64 and real macOS ARM64 each pass full's 100 scoped scenarios; L0 covers 39 commands. These reports describe the Rust client, while the native C distribution has separate package, runtime and installer gates. `complete_git_parity` remains false, and the Rust suite does not claim a one-for-one port of every historical assertion. [Compatibility](docs/COMPATIBILITY.md).

Current native evidence includes 51 scenarios on real Apple Silicon macOS 15.5 and 52 per Linux Docker target, plus two owned-Git `git-p4` dispatch checks per POSIX target. Windows ARM64 has cross-compilation, PE and static package evidence; hardware runtime acceptance is not claimed. Windows defaults disable fscache/fsmonitor/preload and use serial index/grep reads. R6 correctness records 16 passed lanes and two user-waived terminal lanes in 246.204 seconds; paired performance passes all five lanes in 691.251 seconds. Windows x64 user/machine MSI lifecycles pass 27/25 checks; those exact-input receipts are reused by the final batch. Waived terminal checks and earlier failures remain explicit. [Evidence](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.md) · [Defaults](docs/DEFAULTS.md) · [Performance](docs/PERFORMANCE.md).

## Native packages

Download published packages and checksums from [GitHub Releases](https://github.com/neko233-com/git-next/releases). The planned [v1.0.3 release page](https://github.com/neko233-com/git-next/releases/tag/v1.0.3) does not establish publication; the source-time delivery snapshot records pending delivery actions. Windows packages include the owned runtime and require no Rust compiler.

| Platform | Package | Boundary |
| --- | --- | --- |
| Windows x64 / ARM64 | Machine MSI or user MSI | Machine installation requires administrator rights; x64 has actual lifecycle evidence, ARM64 has build/table/static checks |
| Linux x64 / ARM64 | Complete tar.gz with install.sh | Debian 12 / glibc 2.36 or compatible system |
| macOS Apple Silicon | Complete tar.gz with install.sh | macOS 15.5+; real ARM64 runtime acceptance and independent package/Mach-O/shell checks; CPU-affinity constraints unsupported |

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

Rust CLI conformance uses [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md), with L0/full/network tiers and JSON reports. Windows/macOS use the same Rust runner without Python; release branches require full. Native C product conformance and platform lifecycle acceptance run through Go tooling and necessary platform scripts. Reference Git creates isolated fixtures; owned runtime checks reject fallback to installed Git. Historical Python CLI assertions remain available in Git history.

Validation runs locally, with independent correctness lanes in parallel and Windows Installer transactions serialized. Reports retain actual elapsed time and failures. Performance uses a separate paired batch; measured medians, p95 and primary-process memory determine claims. No universal speedup is promised. [Builds](docs/BUILDING.md) · [Performance](docs/PERFORMANCE.md) · [Compatibility](docs/COMPATIBILITY.md).

The native core is GPL-2.0-only with the declared dependency licenses and corresponding source bundles. [Source/license delivery](docs/CORRESPONDING_SOURCES.md). No GitHub Actions are used.

## Independent Rust preview package

The preview packages the independent Rust client. Go tooling also builds the native C distribution and its installers; these are separate artifacts with separate acceptance scope. The 115-file Python retirement preserves the applicable build, packaging, migration and maintenance capabilities in Go, while necessary platform scripts remain. [Tooling and migration evidence](docs/GO_TOOLING.md).

`go -C tools/devtool run . package-rust --platform windows-x64` writes a zip of the Rust `git`, LFS entrypoints, and Git-Terminal. The zip contains no MinGW, Bash, Perl, Python, or Git for Windows files. Its `install.ps1` copies them to `%LOCALAPPDATA%\Programs\git-next` and does not replace a registered official Git directory. See [installation](docs/INSTALLATION.md).

## Windows one-click installation

After obtaining the official release's `install.ps1`, run `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1`. **This automatically replaces registered Git for Windows**, preserves configuration/login/repositories and records a protected rollback transaction. `-Plan` inspects the selection without changes. Offline MSI/TLS material and checksum requirements are documented in [installation](docs/INSTALLATION.md).

Windows MSI installs the independent Rust + GPUI Fast [Git-Terminal](docs/TERMINAL.md) beside Git-next, with tabs, Unicode/IME, copy/paste, scrollback and owned Bash plus PowerShell/CMD profiles. It replaces the Git Bash desktop entry and adds no Git GUI menu. [Built-in LFS and game profiles](docs/GAME_PROJECTS.md) cover Unity/Tuanjie and Unreal; no separate LFS installer is needed.

## Source and licenses

The native C core is GPL-2.0-only; owned packaging/extensions and the Rust terminal carry their declared licenses. Pinned dependencies, notices and corresponding-source bundles accompany the native release. The independent Rust preview retains its own implementation and scoped acceptance reports. Follow the native installation guide for the released product. No GitHub Actions are used.
