# Installation and removal

The primary product is the complete source-built native package. A release candidate remains blocked from default takeover until its local correctness/performance gates pass. [Delivery status](DELIVERY_STATUS.md) records the actual stage.

## Windows native MSI

Use the architecture-matching machine MSI for the whole workstation, or the `-user.msi` variant for the current user. The installer contains the owned Git core/runtime, shell, SSH, credential/LFS/signing tools, shortcuts and automatic updater; Rust and an existing Git installation are not prerequisites.

```powershell
msiexec.exe /i .\git-next-0.3.0-windows-x64.msi
```

Machine MSI needs administrator rights. MSI provides repair, transactional upgrade/rollback, downgrade refusal and uninstall. Installing the package separately preserves an existing default Git. The [protected takeover procedure](TAKEOVER.md) performs the separately verified default-command/caller migration and original-Git removal while preserving repository/profile data. Reopen terminals after a completed PATH migration.

After a completed takeover, `git --version` identifies git-next. `git language get` reports the active language; `git language set chinese`, `english` or `auto` persists the choice. `git update check` checks official release metadata; the scheduled updater handles later accepted releases. [Update behavior](UPDATES.md).

Use Windows Apps or `msiexec.exe /x <product-code>` to remove the native MSI. An active original-Git takeover must first be rolled back through its recorded transaction; removal refuses to break active callers. User repositories, credentials and language preferences are retained.

## Linux and macOS native archives

Extract the accepted architecture-matching tar.gz, enter its `git-next-<version>-<platform>` directory and run `./install.sh --scope user`. An explicitly elevated `--scope machine` installs for the machine. `--manual-updates` opts out of service registration. The owned updater verifies package identity, source commit and every payload member before installation. Existing unrelated command files are refused.

Linux packages target Debian 12 / glibc 2.36 or compatible systems. macOS packages require Apple Silicon and macOS 15.5+. Mac acceptance is package/Mach-O/shell validation in `Dockerfile_mac`; it does not establish native Keychain or OS runtime testing. See [managed layout and removal](UPDATES.md).

The root `install.ps1` and `install.sh` below describe the historical Rust preview. Use native release MSI/archive packages for the complete product.

## Historical Rust preview installer

Windows can additionally install the verified standalone [managed GCM support package](CREDENTIALS.md). It reuses an existing named `manager` configuration with native git-next callbacks; it does not reset login or change global configuration. [System configuration discovery](NATIVE_DISCOVERY.md) inspects paths without executing old Git.

The installed command is git-next. A copy named git is stored in the installation prefix's preview/bin directory, outside PATH. This preserves a usable system Git while the compatibility report is experimental. Replacement flags reject and make no change.

Windows requires PowerShell, Rust 1.96+ and compatible native C tools. Default prefix: LOCALAPPDATA/Programs/git-next; preview command: ~/.local/bin/git-next.exe. The installer appends this command directory to user PATH. Open a new terminal. It records SHA-256, source commit, preview location and system_git_replaced=false in installation.json. Existing preview commands receive a timestamped backup.

Unix supports Linux x64/ARM64 and macOS ARM64 only. Requires POSIX sh, curl, Cargo and a native compiler. Prefix defaults to ~/.local/share/git-next, command to ~/.local/bin/git-next. Add that directory to PATH if needed. GIT_NEXT_PREFIX and GIT_NEXT_BIN customize directories.

## Pinned source and mirror fallback

Resolve main through the official GitHub API to a full 40-character commit SHA. Build with Cargo --locked from the official repository. If transport fails, try ghfast.top and ghproxy.net, always using the same pinned official commit. Do not trust a mirror to choose a commit, request credentials for a public mirror or continue when official SHA lookup fails. Mirrors may be unavailable; no measured acceleration is promised. Pin an already-verified commit with -Commit or GIT_NEXT_COMMIT. The installer does not bootstrap Rust silently.

A locally built, trusted native binary may be selected with -SourceBinary on PowerShell or GIT_NEXT_SOURCE_BINARY on sh. Verify its hash before passing it; this is not a download signature check.

Windows validates `version --json` and the embedded product/transport report before installation, then checks the installed checksum. The manifest uses UTF-8 LF JSON and records product/version and `replacement_ready=false`. Existing client, terminal, native LFS helpers and manifest receive timestamped backups on upgrade. The manifest records helper hashes; checksum-aware uninstall preserves modified helpers. `git-next-lfs` and `git-next-lfs-filter` are installed beside the client; a generic `git-lfs` alias exists only inside the isolated preview directory. Local development builds without a pinned source commit leave `source_commit` empty; the executable checksum identifies those bytes. `-CommandDirectory PATH` changes the preview command directory; pass the same option on uninstall. `-NoShortcut -NoPathUpdate` allows isolated installation tests without changing the Start menu or user PATH. Neither option authorizes system replacement. Uninstall preserves modified commands and shortcuts belonging to another executable.

## Removal

Windows: ./install.ps1 -Uninstall. Unix: sh install.sh --uninstall. The installer verifies the installed-command checksum before deleting it; a subsequently changed executable is preserved. It leaves backups, manifest and repositories for inspection. It does not delete original Git, credentials or user configuration. User PATH may retain ~/.local/bin because other applications can use it.

No Git installation on another machine is implied by building or installing the current workstation preview.


## Git Terminal

`git-terminal` opens the current repository in PowerShell 7 (Windows PowerShell fallback), or the native `$SHELL`/`/bin/sh` on Unix. `git-terminal --cwd PATH` opens a specific folder; `--command SCRIPT` runs an explicit shell command and propagates its exit status; `--print-setup` shows the resolved shell and client without launching one. The sibling `git-next` preview is available as a PowerShell alias or `$GIT_NEXT_EXECUTABLE`. The terminal does not prepend the experimental git-named preview to PATH.

Windows installation creates a **Git Terminal** Start menu entry. Use `./install.ps1 -AddContextMenu` for **Open Git Terminal here** on folders and their backgrounds. Existing Git Bash entries, Git configuration, SSH credentials and original Git remain available. Uninstall removes only this installation's checksummed commands and its own shell integration. This is a native-shell terminal entry, not a POSIX/Bash emulator; Bash scripts still require an existing Bash installation.

## POSIX managed package layout

Final accepted POSIX archives include `libexec/git-next/updater`. The managed installer entry is `./libexec/git-next/updater Install --package <absolute-unpacked-package> --scope user` (or root with `--scope machine`). It verifies accepted package identity/parity and every payload member before installing versioned files and owned command links. `--base <absolute-root>` selects an explicit owned prefix; `--manual-updates` explicitly omits initial service registration. Rerunning Install recovers an interrupted initial transaction. Existing ordinary command files are refused rather than deleted. These paths compile but have not been executed; see [update/storage/rollback details](UPDATES.md). The workstation default Git still awaits final Windows MSI acceptance and takeover.

## Native POSIX candidate layout

The complete native package includes `install.sh` beside `bin/` and `libexec/`. After final acceptance, run `./install.sh --scope user` or the explicit elevated `--scope machine`; `--manual-updates` opts out of service registration. The installer verifies the full stable, release-eligible package before writing a versioned installation. A package with `complete_git_parity=false` remains blocked. Linux uses systemd and macOS uses launchd. The current native candidate requires macOS 15.5+ on Apple Silicon; native runtime/Keychain testing is outside this delivery's user-selected Mac acceptance scope.
