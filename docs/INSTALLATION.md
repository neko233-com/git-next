# Installation and removal

The current distribution candidate is **1.0.3, unpublished**. R8 local correctness passes 16 lanes with two explicitly waived terminal lanes; paired performance passes 5/5, and fresh Windows x64 user/machine MSI lifecycles pass 27/26 checks. Final staging and workstation acceptance remain pending. The earlier R7 bootstrap failed and required exact-byte recovery; R8 subsequently restored all 9,625 original Git files through verified recovery control and ordinary guarded MSI removal. This legacy recovery has `release_gate=false` and does not claim a normal pre-MSI snapshot or exact historical per-file ACL restoration. [Recovery boundary](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-workstation-r8-legacy-recovery-20261024.json) · [Delivery status](DELIVERY_STATUS.md).

The main native package uses the native C engine. The independent Rust client and its installer are a separate preview; its `git install verify` command is not available in the native C CLI. See [installer evidence and verification scope](MSI-PROOF.md).

Windows MSI lifecycle actions use the bundled native no-console worker. Scheduled checks use the same worker, so installing or updating Git-next does not depend on the configured default terminal opening a PowerShell console.

Windows defaults disable fscache, fsmonitor and index preloading, and set index/grep read threads to one. This tested stability profile prevents the observed intermittent incomplete Windows reads. User/repository settings retain normal precedence. See [performance](PERFORMANCE.md) for evidence and the explicitly matching reference configuration.

## Independent Rust preview package

`go -C tools/devtool run . package-rust --platform windows-x64` packs the Rust `git`, `git-lfs`, `git-next-lfs-filter`, and Git-Terminal programs into one zip. The archive contains no MinGW, Bash, Perl, Python, or Git for Windows file, and nothing in that installer downloads those projects. Extract the zip and run its `install.ps1`. The programs are copied to `%LOCALAPPDATA%\Programs\git-next`, and that `bin` directory is prepended to the user PATH. A registered official Git directory is left unchanged.

Git-Terminal uses PowerShell or CMD when the owned Bash runtime is absent. Its SSH connections use the Windows OpenSSH client when that client is installed. The packaged programs import only Windows system libraries. A build that did not use the static Microsoft C runtime also imports `VCRUNTIME140.dll`; that DLL is the Visual C++ runtime, not a Git component. `RUSTFLAGS=-C target-feature=+crt-static` removes the import.

## Windows: one PowerShell entry

Download the root `install.ps1` from the official git-next repository or the accepted release, then run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

The bootstrap detects registered Git for Windows, chooses native x64/ARM64 and the matching user/machine scope, requests UAC when needed, installs the complete MSI and performs protected takeover. Installation uses Windows PowerShell/.NET and built-in Windows Installer/cabinet APIs; it requires no Go, Python, WiX, Rust/Cargo, existing Git execution or installed OpenSSH. It installs **Git-Terminal** (Rust + GPUI Fast), the owned Bash/runtime and native Git LFS together. It creates no Git GUI shortcut. App-private Git copies such as UGit are not default replacement targets. R8's applicable isolated lifecycle gates pass; final workstation validation is still required before publication.

Native 64-bit PowerShell is used for installation. A normal 64-bit launch needs no extra step. When invoked by 32-bit automation, the bootstrap first starts `%SystemRoot%\Sysnative\WindowsPowerShell\v1.0\powershell.exe` through a non-elevated child process, preserving the original parameters and verifying the script SHA-256 before invocation. The 64-bit child then uses the normal `System32` UAC path for machine scope. It does not attempt `RunAs` directly against `Sysnative` from the 32-bit process.

`-Plan` prints architecture, scope and registered replacement root without downloading, creating a cache/snapshot, installing or requesting UAC; a 32-bit Plan only relays to the native process. `-Scope user|machine` explicitly selects scope; a mismatch with registered Git is refused. `-Version <published-stable-version>` selects an official stable tag; the default queries official `latest`. The last recorded lookup (2026-10-23) returned v1.0.1. Version 1.0.2 is a recorded packaging checkpoint, not an established current GitHub release, and 1.0.3 is not yet published. The older 1.0.0 Linux checkpoint left HTTPS, FTP and FTPS on the 0.3.0 helper and did not include Windows packages; that historical description is not a current download recommendation. GitHub's official API selects the release and supplies the package SHA-256; parallel transport probes select an available official/mirror download, and every payload must match the official digest.

### Pre-MSI snapshot and rollback contract

For replacement of registered Git, R8 verifies the selected MSI digest **before MSI writes to the original directory**. Through read-only Windows Installer APIs it reads the embedded cabinet and extracts only `package.json` plus this fixed seven-module allowlist:

- `migration/takeover.ps1`
- `migration/Takeover.Core.psm1`
- `updater/Update.Core.psm1`
- `updater/Scope.Core.ps1`
- `updater/Scope.Native.cs`
- `updater/Runtime.Configuration.ps1`
- `updater/messages.zh-CN.json`

Each module must match its exact `package.files` SHA-256 and MSI File-table size before import. External cabinets, ambiguous paths and mismatched identities/digests are refused. No administrative MSI extraction/install, WiX tool, Python/Go program or old Git process is used to prepare these modules. The private machine cache is created atomically beneath the real Windows common application-data directory; reparse ancestors and untrusted ownership/replacement permissions are refused.

The verified modules expose `New-GnPreinstallSnapshot -OriginalGitRoot <root> -SnapshotRoot <new-private-directory> -CandidatePackagePath <verified-package.json> -CandidateMsiSha256 <verified-digest>`. It records the complete original installation and its access metadata, selected integration/registry/PATH/tasks and original entrypoint identity outside both installation roots. The source is checked again before publishing the snapshot. Actual repository metadata, gitdir pointers and bare repositories within that original installation tree are refused; ordinary `.gitignore`/`.gitmodules` files remain allowed. An MSI that lacks the required snapshot API is refused before installation; a post-MSI copy cannot substitute for the original bytes.

Before MSI starts, bootstrap pins `snapshot.json` SHA-256 in a private reference with the candidate MSI/package digests, roots and owner SID. After MSI, the installed package must match that candidate. Takeover receives `-OriginalSnapshot <snapshot.json> -OriginalSnapshotSha256 <pre-MSI-digest>` and rejects a changed snapshot. On failure the protected snapshot and reference remain, and the error reports its exact path/SHA. They support verified recovery, not blind copying or deletion. Rollback restores the original pre-MSI installation rather than an already overwritten tree; R8's isolated lifecycle gates pass and final workstation proof remains pending. Existing repository, configuration, credential and storage paths retain their contracts.

A compatible independent OpenSSL provider is prepared **before installation/removal** to preserve existing `http.sslBackend=openssl` configurations. Its fixed producer archive is about 117 MB on x64, with pinned archive/member checksums and licenses. It is a compatibility component, not an installed-Git fallback. It is not redistributed inside the MSI. No online download is required to fill missing core components after a completed installation.

For an offline installation, obtain the architecture/scope-matching accepted MSI, its trusted release digest, the matching bootstrap and the pinned TLS archive identified in `packaging/tls-providers.lock.json` beforehand. This candidate filename example does not indicate that 1.0.3 is published:

```powershell
.\install.ps1 -PackagePath .\git-next-1.0.3-windows-x64.msi -ExpectedSha256 <trusted-release-sha256> -TlsArchive .\Git-2.54.0-64-bit.tar.bz2
```

The TLS archive is needed for takeover of registered Git; a new installation using the default Windows TLS backend does not need it. Offline takeover without the material is refused before MSI installation. `-TlsArchive` accepts a predownloaded, checksum-matching archive. Downloads are staged and verified before the original Git uninstaller can run. Configuration, credentials, SSH files and repositories remain in place; recovery records preserve the selected original installation. Failures during takeover attempt verified rollback rather than leaving an unrecorded partial replacement.

After completion, reopen existing terminals. `git --version` identifies git-next, `git lfs version` uses built-in LFS, `git language set chinese|english|auto` switches language, and `git update check` checks the official release. Machine and user MSI variants provide repair, transactional upgrade, downgrade refusal and scoped scheduled updates. Repeated bootstrap runs use the existing installation scope; accepted MSI/updater transactions govern upgrades. Installation success requires the target MSI version, native identity and all immutable payload bytes to be active. A Windows Installer reboot request is not reported as a completed upgrade when activation cannot be verified; original-Git removal is blocked in that state.

## MSI installation and removal

Direct MSI installation is available for managed deployment without a registered original Git to preserve. The filename below describes the unpublished candidate:

```powershell
msiexec.exe /i .\git-next-1.0.3-windows-x64.msi
```

The `git-default-v2` layout installs into `%ProgramFiles%\Git` (machine scope) and
`%LOCALAPPDATA%\Programs\Git` (user scope), so the primary IDE entry point `cmd\git.exe`
is the owned git-next payload rather than a bridge forwarder. See
[the default layout](WINDOWS-DEFAULT-LAYOUT.md) for the full contract.

When a registered official Git exists, the MSI refuses to install over it unless the
verified bootstrap passes the explicit `REPLACEGIT=1` authorization on the command line.
`install.ps1` checks the verbose MSI log for the complete server-side property value
`REPLACEGIT = 1`; it does not assume the launch argument was received. This authorization
alone does not create an original-installation backup.

Use the matching bootstrap for registered-Git replacement. In `git-default-v2`, MSI writes into the original directory itself; directly supplying `REPLACEGIT=1` can overwrite files before any subsequent takeover script sees them. A bare MSI therefore does not provide the bootstrap's pre-MSI snapshot contract. Windows Apps or `msiexec.exe /x <product-code>` removes the MSI. An active original-Git handoff must first be rolled back using its recorded transaction; the removal guard prevents breaking callers. Recovery backups, repositories and user credentials are retained. No blind deletion of unrelated Git installations occurs. See [protected takeover](TAKEOVER.md).

MSI major upgrades execute the new payload before removing the previous product, within
the same Windows Installer transaction (`afterInstallExecute`). A failed new-product
action leaves the old installed registration available for rollback. Component GUIDs,
key paths, installation paths and UpgradeCodes remain stable across versions; old-only
components are removed normally. Payload components belong to 64 fixed, hidden feature
buckets under the existing `ProductFeature`, each limited to 1,600 components. The buckets
follow their parent's installation state and add no optional installation choices.
This changes MSI feature membership without moving files or user data. The lifecycle
gate must verify an upgrade from the previous single-feature layout and an injected
failure that restores the old installed state, payload bytes, updater task and PATH.
User-scope lifecycle verification runs without elevation; machine-scope verification
runs elevated. Package compilation alone does not establish those rollback results.

Windows machine TLS preparation releases verified child-file handles before its atomic directory rename. The protected ACL and exclusive platform lock remain active. `tests/tls_provider_install_acceptance.ps1` verifies fresh preparation, repeat preparation, pinned component bytes and machine ACLs against an installed MSI; this targeted gate complements the previously exercised MSI lifecycle.

Machine configuration migration converts inherited ACL entries into explicit rules before removing non-administrator write permissions. Existing read and deny restrictions remain; the final protected configuration must pass the same machine-state checks. Updater regression covers inherited full-control input, and actual migration exercises the installed helper against existing system configuration.

The standalone Windows bootstrap captures the GUI-subsystem terminal's version using an explicitly waited child process with redirected output. It checks the installed terminal before beginning original-Git removal. This bootstrap is a separate release asset, not an MSI payload file; a bootstrap-only correction retains already accepted MSI bytes and records its own source/hash alongside runtime provenance.

## Linux and macOS

Extract the accepted architecture-matching tar.gz, enter `git-next-<version>-<platform>` and run `./install.sh --scope user`. Explicitly elevated `--scope machine` selects a machine installation; `--manual-updates` omits service registration. Owned installers verify the accepted package identity and all payload members before writing managed versioned files; unrelated commands are refused. See [updates and removal](UPDATES.md).

Linux packages target Debian 12 / glibc 2.36 or compatible systems. The Apple Silicon package requires macOS 15.5+. R7 evidence includes 51 actual scenarios and both owned P4 dispatches on real Apple Silicon, plus independent package/Mach-O/shell checks; this is not a claim that every OS integration or Keychain workflow was exercised. Darwin CPU-affinity constraints are unsupported. The GPUI desktop Git-Terminal is a Windows deliverable; POSIX packages retain their owned shell entrypoints. [Fixed R7 scope](https://github.com/neko233-com/git-next/blob/33269276902e1d3c52fab176516f14db908b7570/docs/benchmarks/native-python-retirement-20261023.json).

The root `install.sh` remains the historical Rust preview installer. Use the `install.sh` **inside the native release archive** for the complete POSIX product. Historical preview commands/settings are separate from the native MSI installation.

[Desktop terminal](TERMINAL.md) · [Game project profiles and native LFS](GAME_PROJECTS.md) · [Delivery evidence](DELIVERY_STATUS.md)
