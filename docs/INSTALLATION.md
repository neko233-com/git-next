# Installation and removal

## Preview only

The installed command is git-next. A copy named git is stored in the installation prefix's preview/bin directory, outside PATH. This preserves a usable system Git while the compatibility report is experimental. Replacement flags reject and make no change.

Windows requires PowerShell, Rust 1.96+ and compatible native C tools. Default prefix: LOCALAPPDATA/Programs/git-next; preview command: ~/.local/bin/git-next.exe. The installer appends this command directory to user PATH. Open a new terminal. It records SHA-256, source commit, preview location and system_git_replaced=false in installation.json. Existing preview commands receive a timestamped backup.

Unix supports Linux x64/ARM64 and macOS ARM64 only. Requires POSIX sh, curl, Cargo and a native compiler. Prefix defaults to ~/.local/share/git-next, command to ~/.local/bin/git-next. Add that directory to PATH if needed. GIT_NEXT_PREFIX and GIT_NEXT_BIN customize directories.

## Pinned source and mirror fallback

Resolve main through the official GitHub API to a full 40-character commit SHA. Build with Cargo --locked from the official repository. If transport fails, try ghfast.top and ghproxy.net, always using the same pinned official commit. Do not trust a mirror to choose a commit, request credentials for a public mirror or continue when official SHA lookup fails. Mirrors may be unavailable; no measured acceleration is promised. Pin an already-verified commit with -Commit or GIT_NEXT_COMMIT. The installer does not bootstrap Rust silently.

A locally built, trusted native binary may be selected with -SourceBinary on PowerShell or GIT_NEXT_SOURCE_BINARY on sh. Verify its hash before passing it; this is not a download signature check.

## Removal

Windows: ./install.ps1 -Uninstall. Unix: sh install.sh --uninstall. The installer verifies the installed-command checksum before deleting it; a subsequently changed executable is preserved. It leaves backups, manifest and repositories for inspection. It does not delete original Git, credentials or user configuration. User PATH may retain ~/.local/bin because other applications can use it.

No Git installation on another machine is implied by building or installing the current workstation preview.


## Git Terminal

`git-terminal` opens the current repository in PowerShell 7 (Windows PowerShell fallback), or the native `$SHELL`/`/bin/sh` on Unix. `git-terminal --cwd PATH` opens a specific folder; `--command SCRIPT` runs an explicit shell command and propagates its exit status; `--print-setup` shows the resolved shell and client without launching one. The sibling `git-next` preview is available as a PowerShell alias or `$GIT_NEXT_EXECUTABLE`. The terminal does not prepend the experimental git-named preview to PATH.

Windows installation creates a **Git Terminal** Start menu entry. Use `./install.ps1 -AddContextMenu` for **Open Git Terminal here** on folders and their backgrounds. Existing Git Bash entries, Git configuration, SSH credentials and original Git remain available. Uninstall removes only this installation's checksummed commands and its own shell integration. This is a native-shell terminal entry, not a POSIX/Bash emulator; Bash scripts still require an existing Bash installation.
