# Installation and removal

Windows MSI lifecycle actions use the bundled native no-console worker. Scheduled checks use the same worker, so installing or updating Git-next does not depend on the configured default terminal opening a PowerShell console.

Windows defaults disable fscache, fsmonitor and index preloading, and set index/grep read threads to one. This tested stability profile prevents the observed intermittent incomplete Windows reads. User/repository settings retain normal precedence. See [performance](PERFORMANCE.md) for evidence and the explicitly matching reference configuration.

## Windows: one PowerShell entry

Download the root `install.ps1` from the official git-next repository or the accepted release, then run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

The default detects registered Git for Windows, chooses native x64/ARM64 and the matching user/machine scope, requests UAC when needed, installs the complete MSI and automatically performs protected takeover. No Rust, Cargo, compiler, existing Git or installed OpenSSH is required. It installs **Git-Terminal** (Rust + GPUI Fast), the owned Bash/runtime and native Git LFS together. It creates no Git GUI shortcut. App-private Git copies such as UGit are not default replacement targets.

`-Plan` prints architecture, scope and registered replacement root without downloading or changing anything. `-Scope user|machine` explicitly selects scope; a mismatch with registered Git is refused. `-Version 1.0.2` pins the current stable release. The default uses the latest stable release. 1.0.2 includes the Linux x64 package and the Windows x64/ARM64 machine and user MSIs. Install 1.0.2 rather than 1.0.0: the 1.0.0 Linux package left HTTPS, FTP and FTPS on the 0.3.0 helper, and it did not ship Windows packages. GitHub's official API selects the release and supplies the package SHA-256; parallel transport probes select an available official/mirror download, and every payload must match the official digest.

A compatible independent OpenSSL provider is prepared **before installation/removal** to preserve existing `http.sslBackend=openssl` configurations. Its fixed producer archive is about 117 MB on x64, with pinned archive/member checksums and licenses. It is a compatibility component, not an installed-Git fallback. It is not redistributed inside the MSI. No online download is required to fill missing core components after a completed installation.

For an offline installation, obtain the architecture/scope-matching accepted MSI and the pinned TLS archive identified in `packaging/tls-providers.lock.json` beforehand:

```powershell
.\install.ps1 -PackagePath .\git-next-0.3.0-windows-x64.msi -ExpectedSha256 <trusted-release-sha256> -TlsArchive .\Git-2.54.0-64-bit.tar.bz2
```

The TLS archive is needed for takeover of registered Git; a new installation using the default Windows TLS backend does not need it. Offline takeover without the material is refused before MSI installation. `-TlsArchive` accepts a predownloaded, checksum-matching archive. Downloads are staged and verified before the original Git uninstaller can run. Configuration, credentials, SSH files and repositories remain in place; recovery records preserve the selected original installation. Failures during takeover attempt verified rollback rather than leaving an unrecorded partial replacement.

After completion, reopen existing terminals. `git --version` identifies git-next, `git lfs version` uses built-in LFS, `git language set chinese|english|auto` switches language, and `git update check` checks the official release. Machine and user MSI variants provide repair, transactional upgrade, downgrade refusal and scoped scheduled updates. Repeated bootstrap runs use the existing installation scope; accepted MSI/updater transactions govern upgrades. Installation success requires the target MSI version, native identity and all immutable payload bytes to be active. A Windows Installer reboot request is not reported as a completed upgrade when activation cannot be verified; original-Git removal is blocked in that state.

## MSI installation and removal

Direct MSI installation is available for managed deployment:

```powershell
msiexec.exe /i .\git-next-0.3.0-windows-x64.msi
```

The `git-default-v2` layout installs into `%ProgramFiles%\Git` (machine scope) and
`%LOCALAPPDATA%\Programs\Git` (user scope), so the primary IDE entry point `cmd\git.exe`
is the owned git-next payload rather than a bridge forwarder. See
[the default layout](WINDOWS-DEFAULT-LAYOUT.md) for the full contract.

When a registered official Git exists, the MSI refuses to install over it unless the
verified bootstrap passes the explicit `REPLACEGIT=1` authorization on the command line.
That property is deliberately not `Secure`, because a Secure property is neither settable
from the command line during `InstallExecuteSequence` nor carried from the client to the
server for a managed per-machine install. `install.ps1` therefore verifies from the verbose
MSI log that the installer really recorded the authorization, and refuses the installation
when it did not.

A directly installed MSI preserves an existing default Git until the [protected takeover](TAKEOVER.md) is explicitly applied. The PowerShell bootstrap performs that step automatically. Windows Apps or `msiexec.exe /x <product-code>` removes the MSI. An active original-Git handoff must first be rolled back using its recorded transaction; the removal guard prevents breaking callers. Recovery backups, repositories and user credentials are retained. No blind deletion of unrelated Git installations occurs.

Windows machine TLS preparation releases verified child-file handles before its atomic directory rename. The protected ACL and exclusive platform lock remain active. `tests/tls_provider_install_acceptance.ps1` verifies fresh preparation, repeat preparation, pinned component bytes and machine ACLs against an installed MSI; this targeted gate complements the previously exercised MSI lifecycle.

Machine configuration migration converts inherited ACL entries into explicit rules before removing non-administrator write permissions. Existing read and deny restrictions remain; the final protected configuration must pass the same machine-state checks. Updater regression covers inherited full-control input, and actual migration exercises the installed helper against existing system configuration.

The standalone Windows bootstrap captures the GUI-subsystem terminal's version using an explicitly waited child process with redirected output. It checks the installed terminal before beginning original-Git removal. This bootstrap is a separate release asset, not an MSI payload file; a bootstrap-only correction retains already accepted MSI bytes and records its own source/hash alongside runtime provenance.

## Linux and macOS

Extract the accepted architecture-matching tar.gz, enter `git-next-<version>-<platform>` and run `./install.sh --scope user`. Explicitly elevated `--scope machine` selects a machine installation; `--manual-updates` omits service registration. Owned installers verify the accepted package identity and all payload members before writing managed versioned files; unrelated commands are refused. See [updates and removal](UPDATES.md).

Linux packages target Debian 12 / glibc 2.36 or compatible systems. The Apple Silicon package requires macOS 15.5+. Mac acceptance is package/Mach-O/shell static validation in `Dockerfile_mac`; it does not establish native Keychain or OS runtime acceptance. The new GPUI desktop Git-Terminal is currently a Windows deliverable; POSIX packages retain their owned shell entrypoints.

The root `install.sh` remains the historical Rust preview installer. Use the `install.sh` **inside the native release archive** for the complete POSIX product. Historical preview commands/settings are separate from the native MSI installation.

[Desktop terminal](TERMINAL.md) · [Game project profiles and native LFS](GAME_PROJECTS.md) · [Delivery evidence](DELIVERY_STATUS.md)
