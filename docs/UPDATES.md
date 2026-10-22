# MSI and automatic updates

## Delivery contract

The Windows updater supports the built-in Windows PowerShell 5.1 even when launched from PowerShell 7: its child process selects the Windows module directory, MSI property reads suppress COM method return values, and atomic JSON/configuration replacement passes a true null backup path. First-time runtime configuration copies preserve a null destination state rather than converting it to an empty string. Task lookup distinguishes an absent task using its HRESULT, including PowerShell's wrapped file-not-found exception. POSIX directory creation accepts a root-owned sticky ancestor such as `/tmp` while continuing to reject writable or foreign-owned installation leaves and symlink traversal. Candidate refreshes use checksum-pinned archives and matching compiled-updater receipts; they do not rebuild unchanged C cores or reuse earlier acceptance for changed bytes.

The requested final delivery includes Windows x64/ARM64 MSI packages, background automatic updates, official GitHub release discovery and automatic selection of a usable download mirror. These are part of the one consolidated release gate. Source implementation and an MSI that compiles do not establish installation, update, rollback or complete Git compatibility.

`packaging/native_msi.py` and `packaging/windows/` implement the Windows packaging/update path. The consolidated local acceptance has passed in its declared contract scope. v0.3.0 is published and the final native machine MSI has replaced the workstation default; see the final delivery receipt.

The native MSI now defaults to `--scope machine`; `--scope user` retains the original user identities. Machine packages install under Program Files with HKLM/System PATH registration and use `%ProgramData%/git-next` for protected shared state. Their task runs as SYSTEM without a password, including after logout. User packages retain LocalAppData and an interactive user task. Update manifests select matching scope variants and reject scope changes. Machine assets use the ordinary platform filename; explicit user assets append `-user.msi`.

The source CLI exposes `git update check|apply|status|enable|disable [--json]`. `apply` queues the registered worker and reports `queued`, not an installed version. The worker lets the requesting CLI exit before its ordinary busy check. Both scopes accept task requests; machine users can check/read status/request an update, while changing machine update policy requires an administrator. The legacy `git update-git-for-windows` entrypoint supports yes/quiet/GUI prompts and the existing 0/1/2 current/declined/started exit convention, but downloads git-next. The producer updater is excluded. Windows updater contracts and Linux runtime/race checks passed locally. macOS retains its package/Mach-O/shell static acceptance boundary.

## Compatibility, storage and rollback report

The native MSI uses an architecture/scope-specific stable UpgradeCode, a Start menu entry and a scoped PATH entry. The `git-default-v2` layout installs into `%ProgramFiles%/Git` for machine scope and `%LOCALAPPDATA%/Programs/Git` for user scope, so the primary IDE entry point `cmd\git.exe` is the owned git-next payload rather than a bridge forwarder. The existing script installation has a different prefix; migration between installation managers must be accepted before final activation. The historical Rust preview keeps its Git alias outside PATH. Native candidates contain the owned Git entrypoints, but installing a candidate does not authorize takeover or remove the original Git. MSI parameters cannot override the incomplete-parity takeover gate. Existing applications selecting absolute original-Git paths still need migration acceptance.

The package adds client binaries, an updater and a checksummed `package.json`. It does not move, rewrite, repack or renormalize existing repositories, refs, indexes, objects or LFS bytes. It does not rewrite `.gitconfig`, SSH files, known_hosts, credential helpers or credential-vault data. Explicit LF exceptions remain authoritative. Installing an MSI does not remove the original Git.

Windows Installer performs major upgrades after `InstallExecute`, within its rollback transaction. Stable component identities let the new package replace shared files before the old registration is removed; an installation failure retains the previous product. Uninstall removes owned package files, its PATH entry, shortcut and updater task. Repositories, credentials and updater diagnostic state remain. The rollback custom actions restore the prior owned scheduled-task definition. Unrelated tasks with a colliding name cause rejection. An interrupted or failed MSI transaction must pass old-file and old-task recovery tests before release. A successfully installed version is not silently downgraded by the automatic updater; deliberate post-upgrade rollback requires the separately verified migration/recovery procedure.

Native Windows core executables use the git-next product version for both numeric and displayed PE file/product versions. Keeping the upstream numeric version would cause Windows Installer to retain an older core when upgrading git-next; the upstream compatibility baseline remains available in CLI build information.

Capacity overhead is the staged compressed MSI, its installed payload and Windows Installer's managed cache; updater downloads are capped at 1 GiB and removed after use. Downloads are bounded in size and time. The updater runs outside Git commands, so repository discovery, status and version do not incur network checks. Real runtime, peak disk/memory and upgrade latency are final batch measurements, not source-level claims.

## Update trust and mirror policy

The updater obtains `/repos/neko233-com/git-next/releases/latest` from the official GitHub API. It accepts a stable, non-draft release, an official `git-next-update.json` asset with a GitHub SHA-256 digest, a matching version/source commit, and an accepted complete-parity report. Each platform entry must match the official package asset name, byte length, digest and installation UpgradeCode. An unavailable/rate-limited API can fall back to GitHub's official stable-only `/releases/latest/download/git-next-update.json` HTTPS endpoint, whose manifest pins the package hash/size/version and is explicitly reported as `official-release-https` trust. It does not claim an independent API digest in this fallback mode. Corrupt or semantically inconsistent API metadata does not trigger this fallback. Official metadata is never obtained through a mirror. If both official metadata paths fail, installed files are preserved. Public update requests do not use Git credential helpers, stored account tokens, cookies or default Windows credentials.

Official GitHub, `ghfast.top` and `ghproxy.net` download endpoints are probed concurrently. Their initial response times rank download attempts; this does not promise maximum throughput. Mirror payloads may be used only for the already pinned public MSI. Every download must match its official byte length and SHA-256. Corrupt, oversized, stalled or unavailable transports are discarded before installation and the next transport is tried. HTTPS and certificate validation remain enabled; no user authentication is sent to mirrors.

Before upgrade, the updater verifies existing owned-file hashes, downloaded MSI identity and version, and whether an owned client/terminal/LFS process is active. Busy clients defer the update. MSI runs without forced application shutdown or automatic restart. A restart-required result is a failed automatic completion and must be inspected; it is never reported as a successful live upgrade. The installed manifest and file hashes are read back after success.

There is no embedded signing key. Configured release signatures and checksums must be verified locally before publication. GitHub HTTPS/asset digests protect transport identity; they are not described as independent publisher signing.

## Scheduling and controls

The user MSI registers a task for its existing interactive session; the machine MSI registers a protected SYSTEM task. A delayed daily/logon trigger and hourly retry opportunities run outside Git commands. Successful/current-version checks have a 20-hour network throttle; busy or failed attempts wait at least an hour before retry. Neither scope saves a password. User tasks do not elevate and require a logged-in session; machine tasks can update the protected machine package after logout. Scheduled checks are silent; diagnostics record status/timestamps, not exception bodies or credentials. The MSI log remains in the owned update state directory for local diagnosis.

The installed updater supports these modes:

```powershell
$updater = "$env:LOCALAPPDATA/Programs/Git/updater/update.ps1"
& $updater -Mode Check
& $updater -Mode Apply
& $updater -Mode Status
& $updater -Mode Disable
& $updater -Mode Enable
```

`Disable` persists across package upgrades. Scheduled attempts do not overlap. No network requests occur for `Status`, `Enable` or `Disable`. Users must review and trust downloaded installation material before invoking it. Linux/macOS update source and scheduling are described below; current local gates and platform boundaries are recorded in delivery status.

## Local packaging

WiX is a build dependency, not an installed runtime dependency. The build entry requires a pinned native-build receipt, verifies source and executable hashes plus PE architecture, and refuses to overwrite a staged package. Release packaging requires stable native version, immutable source commit and complete parity. `--allow-experimental-local` produces only an unpublished preview package; its receipt has `release_eligible=false` and `installation_runtime_accepted=false`.

```powershell
dotnet tool install wix --version 5.0.2 --tool-path .evidence/tools/wix5
python packaging/native_msi.py --distribution .evidence/native-core-integration/distribution-owned-native-20261020 `
  --wix .evidence/tools/wix5/wix.exe --allow-experimental-local
```

The final native route uses `--distribution`: it verifies every manifest file, native entrypoint ISA, owned-core identity and source fingerprints, then generates stable MSI file/directory components for the full runtime tree. MSYS virtual-link System attributes are retained. Final x64 package receipts enumerate 10,600 payload files; the accepted machine package is installed and published in v0.3.0. Packaging does not call `git --version` or any product command. The old `--binary-dir` Rust subset route is retained for historical preview fixtures and is not a final product package.

All installation, upgrade, injected-failure rollback, uninstall, scheduling, transport-corruption, offline, version-replay and busy-client checks belong to the consolidated local product matrix. Its MSI receipts, artifact verification and platform results must pass together before one tag/package release. No GitHub workflow is used.

`tests/updater_acceptance.ps1` prepares offline deterministic transport/version/checksum contracts without contacting real accounts. `tests/msi_acceptance.ps1` exercises actual Windows Installer with isolated Git HOME/USERPROFILE/SSH variables and a temporary install prefix; it requires absent MSI/task/state identities and checks owned user PATH/Start menu/task rollback. It refuses to overwrite an existing MSI installation or updater state. A separately built older MSI is passed as `--msi-base` to the product batch and the final MSI as `--msi`, so successful version upgrade, downgrade rejection and injected-failure rollback use real native package bytes. Test packages are local fixtures, not additional published releases. The lifecycle fixture changes only owned installation integration; repositories/login stores remain untouched.

The transport and installer contracts follow [GitHub's Releases API](https://docs.github.com/en/rest/releases/releases), [Windows Task Scheduler](https://learn.microsoft.com/en-us/windows/win32/taskschd/task-scheduler-start-page), and [WiX major upgrades](https://docs.firegiant.com/wix/schema/wxs/majorupgrade/).

## Native migration integration

The immutable package, distribution and stock-default manifests are MSI companions of the versioned native core. They upgrade with that core even when a previous rollback changed file timestamps. Mutable active configuration remains outside the immutable payload and keeps its transactional merge rules. Lifecycle acceptance deliberately varies manifest timestamps before upgrade and checks rollback and final package hashes. This follows the [WiX companion-file contract](https://docs.firegiant.com/wix/schema/wxs/file/).

Windows Installer actions and scheduled update requests launch the owned `updater/msi-worker.exe`. This native GUI-subsystem worker starts the system PowerShell with `CREATE_NO_WINDOW`, avoiding console-host handoff from an impersonated user MSI. It accepts only fixed lifecycle modes or scheduled `Auto`, `Check`, `Apply`, `Enable` and `Disable` requests, returns the script's exit code, and does not retry script failures. A process-initialization DLL failure can be retried once before any script executes. The worker has no CRT or third-party DLL imports; package receipts pin its source and executable hashes.

Active non-Git runtime settings are mutable configuration. The package stores immutable stock files and their manifest under `runtime/defaults/`; `InitializeRuntimeConfiguration` seeds the ordinary SSH/shell/signing paths after files/registry installation. It updates an existing file only when it still equals the preceding default, preserving edits and extra files. Its scoped default-hash state and private transaction snapshots support installer rollback; a commit action removes only the transaction's own snapshots. Uninstall retains active configuration, repositories, login data and diagnostic state. `runtime/etc/gitconfig` remains the checksummed managed LF default, with migrated user/system configuration retaining its documented precedence.

`Runtime.Configuration.ps1` supplies this lifecycle and the takeover snapshot/restore path. Immutable-file verification does not treat intended active configuration edits as corrupt binaries. The installer actions and updated package layout are source changes awaiting the final D07 rebuild and D08 lifecycle batch; earlier MSI compilation receipts do not cover them.

The full native payload contains console/GUI legacy bridges and `migration/takeover.ps1`. MSI upgrades reuse the installed user root unless an explicit override is supplied. A read-only `GuardRemoval` check runs before uninstall initialization, so an active Git handoff must be rolled back before its runtime is removed; major upgrades keep the handoff. The updater now accepts the complete manifest tree, validates each canonical path/hash and case identity, and considers all installed runtime executable names when deferring a busy update. Migration and updates share the same exclusive lock. Packaging verifies that its update/migration scripts match the current MSI integration source; an old subset distribution cannot silently acquire a newer incompatible MSI action table.

The old producer's `git-update-git-for-windows` script is excluded from the owned payload. The final D06 update entrypoint must use Git-next's verified release flow; a vendor updater must never install official Git over Git-next. These are implementation changes; no installer or update task has been executed during this phase.

Machine and user native MSI flavors have compiled locally; [the scope checkpoint](../native-core/receipts/implementation-windows-scopes-20261020.json) distinguishes their packaged bytes from subsequent source edits. The machine flavor implements protected executable/state paths, HKLM/System PATH ownership and the SYSTEM worker. No task or installer has run. User-scope update success must not be reported as global Git replacement; final lifecycle checks remain D08.

## POSIX updater implementation

The owned stdlib-only module in `packaging/posix/updater/` compiles with Go 1.27.2 for Linux x64/ARM64 and macOS ARM64. Native `git update` delegates only to this executable and passes its resolved language. It implements Check, RequestApply, Status, Enable and Disable, plus managed Install, worker and explicit Rollback operations. Compilation is [recorded separately from acceptance](../native-core/receipts/implementation-cross-platform-updates-20261020.json); none of these binaries or services has run.

Managed installation uses `releases/<version>-<manifest-id>` and an atomic relative `current` symlink. Each new payload is size/hash checked, boundedly unpacked into a new owned tree, verified against every manifest file/link/mode and activated only after journal persistence. Recovery completes a valid interrupted activation or restores its verified prior tree. Previous versions remain available for running clients and deliberate rollback. No repository or credential bytes are migrated by an update. An interrupted initial install is recovered on the next Install invocation; existing command links are restored only while they still point to this installation.

The user prefix is `$XDG_DATA_HOME/git-next/<platform>` or `~/.local/share/git-next/<platform>`. Machine prefixes are `/opt/git-next/<platform>` on Linux and `/Library/git-next/<platform>` on macOS. Protected machine update state uses `/var/lib/git-next/<platform>` or `/Library/Application Support/git-next/<platform>`. User installs require a non-root owner; machine policy/install changes require root. Existing regular files in command-link locations are refused. Initial installation can retain an explicitly selected manual-update policy.

systemd or launchd runs the owned worker outside Git commands. Successful scheduled checks wait 20 hours; retry opportunities are hourly. A protected Unix socket accepts only the fixed apply request, without caller paths, URLs or credentials. Requests report queued; the worker verifies and activates the release. Disable persists policy while preserving explicit apply. No password is saved. The worker restarts onto the newly activated updater through its ordinary supervisor.

Official GitHub API digests or the official stable HTTPS manifest anchor metadata; only public payloads use ranked mirrors, followed by full pinned size/SHA-256 validation. Downgrades and prereleases are rejected by automatic apply. The shared JSON fields are `schema_version`, `status`, `version`, `installed_version`, `install_scope`, with `newer`/`metadata_trust` for check and `enabled` for policy/status. Platform diagnostic extensions may add fields.

Final archives use `git-next-<version>-<platform>.tar.gz` with that single top-level directory. Each POSIX update manifest platform entry has `install_kind: tar`, `asset`, `sha256`, and `size`; global schema/version/source_commit/complete_git_parity match the Windows manifest. D07 generates the final complete manifest; D08 owns transport, socket/permission, replay, interruption, rollback and scheduling acceptance. No release or automatic service is currently installed.

Machine updater removal runs before Windows Installer removes its registration values, so protected-root validation and rollback still have their ownership record. Native Program Files is read from the protected 64-bit registry view, including from a 32-bit PowerShell host. [Windows Installer action sequencing](https://learn.microsoft.com/en-us/windows/win32/msi/removeregistryvalues-action).
