# Git-Terminal on Windows

Git-Terminal 0.2.0 adds grouped SSH hosts/config import, real remote tabs, native authentication, SFTP browsing, Explorer file-drop upload, right-click download, bounded cancellable transfer queues and agentless read-only Linux monitoring. Passwords are not stored in host configuration. Monitoring is Linux-only; SSH/SFTP also work with other OpenSSH servers. See the [terminal manual](https://github.com/neko233-com/git-terminal/blob/main/README.md).

The [Windows component update](https://github.com/neko233-com/git-terminal/releases/tag/v0.2.0) retains accepted git-next 0.3.0 core bytes and MSI product identity. Existing installations require a matching architecture/scope MSI with `REINSTALL=ALL REINSTALLMODE=vamus`; new installations omit those properties. This recaches the complete repair/uninstall payload. The Git product updater is unchanged; install this separate terminal component update explicitly. No standalone terminal automatic update is claimed.

The askpass helper is installed beside the terminal. Prompt root discovery avoids UNC lookups; PTY writes and resize run off the UI thread, hidden tabs answer protocol queries, and negotiated ConPTY input records preserve emoji. Git protocol implementations remain independent of terminal SSH sessions.

Windows combines tabs and native window buttons in one title bar with an original terminal/branch icon. Nine icon sizes are embedded in the EXE for Explorer, taskbar, Alt+Tab and installed shortcuts; no icon download is needed. Native drag, minimize, maximize/restore, close and Snap hit areas remain available. Tabs scroll in compact windows while caption controls stay visible.

The icon source is `git-terminal/assets/git-terminal.svg` in the peer repository. Desktop acceptance checks icon handles, native caption hit areas, a 560×340 compact window, tabs, highlighting, ConPTY, clipboard and exit. Clipboard formats are copied eagerly in memory and restored without retaining remote OLE proxies; contents are not logged.

The Windows product installs an independent Rust + GPUI Fast Git-Terminal beside the git-next client. Its source repository is https://github.com/neko233-com/git-terminal, a peer submodule of git-next in GitSSS. The historical src/bin/git-terminal.rs launcher is separate and is not the desktop application.

The native terminal owns ConPTY sessions and draws through GPUI Fast. Tabs, Bash/PowerShell/CMD profiles, Unicode and Chinese input, selection, history, copy/paste, bracketed paste, zoom and resizing are implemented. The bundled default is the owned Bash shell, retaining familiar shell commands and HOME files. git-next comes first on each session PATH. No external terminal host or installed official Git is used as a fallback.

The MSI creates a Git-Terminal Start menu shortcut and Open Git-Terminal Here directory/background entries. Legacy Git Bash paths dispatch to the new terminal after takeover. --cd and --cd-to-home compatibility arguments preserve common old shortcuts. Git GUI shortcuts and context entries are retired during the authorized handoff; their original bytes/settings remain in the protected rollback transaction. Internal compatible Git commands remain available without advertising a Git GUI application.

~~~powershell
git-terminal --cwd D:\Code\project
git-terminal --shell powershell
git-terminal --shell cmd
git-terminal --no-profile
git-terminal --print-setup --json
git-terminal language set chinese
git-terminal language set english
git-terminal language set auto
git-terminal language get
~~~

Preferences live in %APPDATA%/git-terminal/settings.json and use atomic replacement. The app follows the system language until manually selected. Output and credentials are not saved to application logs; shells manage their own normal profiles/history. Window/tab cleanup owns only its shell processes and descendants.

The tab-first dark interface uses a 16px monospace terminal and a coordinated ANSI palette. Ctrl+T opens a tab, Ctrl+W closes it, and Ctrl+Tab / Ctrl+Shift+Tab switches. Ctrl+Shift+T/W remains compatible. New tabs inherit the current directory; directory and branch appear in the tab. The dropdown beside + selects a default shell or interface language. Closing another tab preserves the selected session. The footer shows the current directory and shell.

Windows x64 uses Rust/MSVC. ARM64 uses the pinned LLVM MinGW toolchain and a separately verified, licensed unwind runtime. Both binaries are packaged only for their matching architecture. Compilation and PE checks do not substitute for actual x64 desktop/ConPTY acceptance. The latest [delivery ledger](DELIVERY_STATUS.md) records current acceptance.

Windows keeps the Start menu name **Git Terminal** stable across MSI upgrades (the application is branded Git-Terminal). CMD paths use native separators; an owned bundled console helper preserves quoted `--command` scripts and native exit status without writing command text to disk.

The managed Bash prompt uses native Rust coloring for commands, options, strings, variables, comments and unfinished quotes, retaining native readline editing, history and completion. No third-party line editor or first-run plugin download is needed. Only displayed input cells change; shell bytes, command output and alternate-screen applications retain their behavior. PowerShell loads native PSReadLine; CMD keeps its native editor. The Bash prompt shows the directory and branch, or a short OID for detached HEAD. Parent discovery supports worktree/submodule `.git` files. Only HEAD is read; no worktree status scan runs on each prompt. Tab titles follow the active directory/branch.

Profile files are read without being edited. The owned Bash rc file reads `/etc/profile` and the first existing user login profile inside an interactive non-login Bash. `--no-shell-integration` restores normal shell startup; `--no-profile` skips user startup files. `syntax_highlighting=false` disables native coloring; `git_prompt=false` opts into your own prompt. Native Bash coloring follows the managed prompt. Explicit commands retain their normal exit semantics. Consolidated acceptance covers native token colors, ConPTY input, branch discovery, worktree/detached transitions and PSReadLine.

Use `tools/install_bundle.ps1 -Package <MSI path>` from Git-Terminal to preserve the original registered MSI source filename automatically and force replacement of same-version companion files. This installer keeps old component identities and notices.
