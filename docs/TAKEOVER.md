# Complete local Git takeover

> Current delivered state (2026-10-21): the accepted machine MSI has replaced registered Git for Windows. Fresh CMD/PowerShell and legacy entrypoints use git-next, the native terminal is active, and 13 Git/SSH profile files retain identical bytes. v0.3.0 is published. Earlier checkpoints retain their original boundaries; see [delivery status](DELIVERY_STATUS.md).

Takeover preserves ordered legacy credential helpers and profile data. It adds the shipped Windows read-safety system defaults (`core.fscache=false`, `core.fsmonitor=false`, `core.preloadIndex=false`, `index.threads=1`, `grep.threads=1`) after the legacy system include, together with LF defaults. Explicit user/repository values keep their normal priority.

The requested outcome remains: install git-next on this Windows workstation, make the `git` command and its identity belong to git-next, retain repositories/configuration/login state, remove the dependency on the old Git installation, and deliver high performance and low latency. An isolated preview or a passing subset does not complete that outcome.

The [Docker migration checkpoint](benchmarks/linux-x64-docker-migration-0.3.0-dev-20261020.json) verifies private HTTPS/SSH operations with generated credentials in a separate Linux client image containing no official Git/OpenSSH. The temporary old repository, global LF settings, saved helper login and SSH files survive the handoff and tested failures. Its containers, volume, network and temporary images were removed. This closes a bounded isolated migration check while full command compatibility and actual platform/caller acceptance remain required.

## Machine-wide workstation handoff

Read-only metadata identifies this workstation's original Git as a `LocalMachine` installation. Both machine and user MSI flavors now compile. `Apply` rejects removing machine Git through a user MSI. No old installation was changed. The [machine-scope implementation checkpoint](../native-core/receipts/implementation-windows-scopes-20261020.json) records exact packaged and later source bytes separately; neither is runtime acceptance.

The default machine MSI uses a protected Program Files root, HKLM/System PATH registration, a SYSTEM update task without a saved password, and protected ProgramData state. The additive machine UpgradeCode is distinct from the unchanged user identities/paths. Legacy bridges resolve the matching HKCU or HKLM installation and retain builtin command names. Machine configuration/components are shared in ProgramData; private rollback files allow only SYSTEM/Administrators. Atomic creation establishes these permissions before bytes are written. D02 still owns customized system SSH/shell/signing consumers; preserving files alone does not prove their behavior.

Migration keeps Git/LFS repository layouts unchanged. Rollback restores the old registered installation and caller/PATH state from the private verified backup; update/bridge changes must preserve unrelated user edits. Machine payload, backup and Windows Installer cache sizes, startup/legacy-call overhead and update latency remain the consolidated capacity/performance measurements. These scope corrections are required before release or real takeover, not a new deliverable or a test already passed.

## Native MSI migration implementation

The final native MSI contains `migration/takeover.ps1`, `Takeover.Core.psm1` and CRT-free console/GUI `git-next-bridge` binaries. These are implementation inputs; no migration or new product command has been executed during implementation. The original workstation Git remains installed. `Plan` reads software/package metadata without applying a handoff; `Apply` requires both an accepted complete MSI manifest and a matching true parity report embedded in the actual native executable. Invalid language/user configuration rejects before old-Git removal. Build-time packaging never executes these checks.

The supported Windows handoff is the existing Git for Windows installer layout: a matching `Git_is1` uninstall registration, `cmd/git.exe` and a pinned-by-plan `uninsNNN.exe` selected from the matching registration (including `unins000.exe` and `unins001.exe`). Every old executable must have an explicit owned counterpart; unknown mappings, reparse points, busy runtimes and changed input bytes prevent removal. Per-user and machine registrations use their original hive; removing a machine installation needs one elevated run. This OS requirement is applied only to the final authorized handoff.

A user transaction under `%LOCALAPPDATA%/git-next/migrations` is restricted to its user, SYSTEM and Administrators. Machine transactions use `%ProgramData%/git-next/migrations/<platform>` and require an administrator. Each stores verified original installation bytes, file access rules, the selected registry state and related shortcut/Terminal-profile files. It does not export the credential vault, contact a private repository or copy user SSH keys from HOME. Git objects, refs, indexes, reflogs and LFS trees are not rewritten. Backups remain local and must never be committed or uploaded. Capacity is capped at 100000 files, 1 GiB per file and 4 GiB total; actual installed payload and backup sizes remain final measurements.

The one-click root `install.ps1` detects registered Git, prepares the pinned independent TLS archive before MSI installation, then invokes this protected takeover automatically. `-PackagePath` plus trusted `-ExpectedSha256` and `-TlsArchive` supports offline takeover. The pinned TLS provider is prepared before removal, so an existing `openssl` selection can continue offline. The old updater's matching interactive-user tasks are retained in the private rollback record and removed before uninstall. The uninstaller runs silently without restart; a live process timeout retains its PID/start identity and defers recovery. It is never killed merely to make migration finish.

After removal, regular non-PE support/configuration/certificate data remain at their compatibility paths. Original PE files are replaced only by the project's own small bridges, which dispatch directly to the matching user or machine native MSI registration. They preserve the raw argument tail, current directory, streams and child exit status; console and GUI subsystems have separate binaries, and 8.3 launch paths are normalized. No original Git/OpenSSH binary is a fallback. The MSI owns the updating runtime, while machine-protected bridge files need no rewriting for ordinary package updates. Existing Git discovery keys identify `git-next`; selected shell associations, shortcuts and Terminal fragments retain their previous paths through these bridges.

### A bridge that names itself

A bridge decides what to run from its own registration, so a registration whose `RelativePath` resolves back to the bridge file makes every invocation start another copy of the same process. Nothing in the child differs from the parent, so the chain only ends when the machine runs out of memory. This is not hypothetical: a machine-scope installation whose `InstallRoot` recorded as `C:\Program Files\Git\` kept `cmd\git.exe` and `bin\git.exe` registrations from an earlier layout. The forwarder built `C:\Program Files\Git\\cmd\git.exe` and compared that string with its own module path; the repeated separator made two spellings of one file look different, the guard that exists to prevent recursion never fired, and one `git` call from an IDE left 54914 processes running.

Three changes stop that class of failure:

- The forwarder compares file identity, not text. A repeated separator, a trailing separator, an 8.3 name and a hard link all resolve to one volume serial number and file index, and the target is refused before it is started. A hop counter in `GIT_NEXT_BRIDGE_DEPTH` stops a cycle between two different files after four hops. A refused forward exits 127 with a message naming the repair tool; it never starts a child.
- `Set-GnBridgeRegistration` refuses to store a mapping whose target resolves to the same file, and the `git-default-v2` apply step deletes any `cmd\git.exe` or `bin\git.exe` registration an earlier layout left behind, because the payload occupies those paths.
- Two tools make the state auditable. `go -C tools/devtool run . repair-windows-entry-points` verifies the payload digest, restores a forwarder entry point from it and deletes every self-referential registration in both hives (`--plan` reports without writing). `go -C tools/devtool run . verify-windows-bridge-guard` proves the guard works: it runs `self`, `cycle` and `forward` scenarios inside a job object with an active-process limit, so a regression is bounded instead of fatal. Against the previous forwarder the first two scenarios reach the 64-process limit; against the current one they cost one and five processes.

An entry point that is still a forwarder after a layout change is a defect in the installation, not in the bridge: the manifest records the payload digest for `cmd/git.exe` and `bin/git.exe`, and any file there smaller than the payload is the forwarder. Repair before running `git` again.

### A ceiling on concurrent invocations

A bridge cannot stop a caller from starting a hundred processes; that decision belongs to the IDE or the build. What the product can guarantee is that each process is a single Git process, that it exits, and that the machine has a ceiling. Three layers enforce that:

- The forwarder refuses to start a copy of itself and stops a two-file cycle after four hops, so no invocation multiplies.
- `go -C tools/devtool run . verify-windows-bridge-guard` runs the `self`, `cycle`, `forward` and `longpath` scenarios inside a job object with an active-process limit of 64. A regression is measured, not suffered: the previous forwarder reached the limit in the first two scenarios, the current one costs one and five processes. A burst of 200 concurrent `git status` through the installed payload peaks at 200 and drains, because each invocation is one process.
- `go -C tools/devtool run . git-process-watchdog` enforces a machine-wide ceiling for callers that ignore both. It terminates the *newest* excess and leaves the oldest alone, because the oldest processes are the ones a person or a build is waiting for. Measured: 80 live invocations with `--limit 20 --apply` leave 20.

Tests follow the same rule. `docker/tests/` builds the suite into an image and runs it with `--pids-limit 128` and `--memory 2g`, so a test that spawns without end hits the container's ceiling and dies there instead of on the workstation; `docker/tests/run.sh stress` runs the concurrent-invocation burst under the same limits. Windows-only behaviour cannot run in a Linux container, which is why the bridge scenarios use the job object instead: both harnesses bound the process count rather than trusting the code.

### Paths beyond MAX_PATH

Three places have to agree before a long path works, and all three now do:

- The forwarder rebuilds its target with the extended-length marker (`\\?\`) when the path reaches 260 characters. The marker cannot be added before normalisation, because an extended-length path is not normalised and a recorded root ending with a separator would leave a doubled one; the test creates a target of more than 300 characters and asserts the child's exit status arrives.
- `core.longpaths = true` is part of the migrated system configuration, so repository operations accept long paths even where the Win32 manifest of a helper does not.
- `HKLM/SYSTEM/CurrentControlSet/Control/FileSystem/LongPathsEnabled` is a machine setting, not a product default. `go -C tools/devtool run . check-runtime-defaults` reports it; enabling it is an administrator action.

Every default the product ships, including these, is written down once in `packaging/runtime-defaults/gitconfig` and justified in [Runtime defaults](DEFAULTS.md).



User migration adds `system.gitconfig` beside the git-next language settings; machine migration uses `%ProgramData%/git-next/config/system.gitconfig`. It includes the retained old system config when present, otherwise managed product defaults, then selects LF software defaults. Native `git_system_config()` uses this file when present, so `git config --system` reads and writes the effective migrated file. Explicit `GIT_CONFIG_SYSTEM`, `GIT_CONFIG_NOSYSTEM`, global/local config, attributes and command-line overrides retain their precedence. Rollback removes only an unchanged migration-owned file. An additive `migration.json` points to the protected transaction; older binaries ignore these files. Runtime consumers of customized system SSH/shell/signing settings still belong to D02 integration and final D08 checks; retaining their bytes is not proof that every consumer has been verified.

The handoff and updater share one lock. Failure attempts an automatic rollback after the uninstaller exits; active processes or independently changed files/registry values are preserved for explicit recovery. Rollback restores original executables and integration from the verified private backup, removes only transaction-owned bridges, and restores old Git PATH entries while preserving unrelated PATH additions. The MSI blocks manual removal of an active handoff until rollback restores its original dependency/caller state; major upgrades retain the current install directory and bypass this removal guard.

After all implementation and acceptance gates pass, the installed entrypoints are:

```powershell
# Read-only plan; use the actual installed MSI root.
& "$nativeRoot/migration/takeover.ps1" -Mode Plan -OriginalGitRoot 'C:/Program Files/Git'
# Final accepted handoff; -TlsArchive optionally supplies the pinned offline archive.
& "$nativeRoot/migration/takeover.ps1" -Mode Apply -OriginalGitRoot 'C:/Program Files/Git'
# Use the exact protected transaction returned by Apply.
& "$nativeRoot/migration/takeover.ps1" -Mode Rollback -Transaction $transaction
```

The current root `install.ps1` automatically selects the registered Git installation and invokes this installed native migration entrypoint after MSI verification; `-Plan` is read-only. The historical Rust preview cannot activate replacement. The declared D08/D09/D10 gates passed; the final actual machine takeover separately verified fresh/legacy entrypoints, the installed native terminal and preservation of all 13 Git/SSH profile files.

Machine `system.gitconfig` lives in `%ProgramData%/git-next/config`; a previously migrated user configuration retains priority. Optional original PATH entries for shell/native tools map to owned runtime directories. Rollback removes migration-added entries and restores pre-existing positions while preserving unrelated additions. Both flavors share the update/migration lock in their own scope. The final release binds accepted runtime bytes and the targeted TLS installer correction to the committed source and actual machine installation receipt.

## Completion gates

### Runtime configuration implementation contract

Owned SSH/shell/signing programs must consume the old system configuration through their ordinary runtime paths. Package defaults therefore live under the additive `runtime/defaults/` tree with a checksummed manifest; active non-Git system settings remain under `runtime/etc`, `runtime/usr/etc` and the native prefix's `etc`. Git's managed LF defaults and effective migrated `system.gitconfig` keep their existing precedence. No repository, key-vault or HOME-key storage path changes.

The installer seeds active configuration, tracks the preceding default hashes in scoped state, and updates only files still equal to those defaults. User changes and extra configuration files survive upgrades. Migration copies the old non-PE system configuration from its private verified backup into those actual consumer paths, retaining access restrictions and MSYS virtual-link attributes. It snapshots the preceding active files before writes. Rollback restores those snapshots only when the migrated bytes are unchanged; independently edited files stop recovery for review. The updater validates immutable defaults separately from intentionally mutable configuration. Windows Installer rollback and upgrade failures remain D08 acceptance requirements.

When the retained original system Git config exists, the migrated file includes that config alone before applying product LF defaults. Managed credential defaults are used only when the original system file is absent. This preserves ordered multi-valued settings such as `credential.helper`; combining old and new helper lists could prompt a different login provider before reaching an existing saved credential. User/global/local settings keep their normal precedence. This correction is included in the accepted native MSI and isolated credential/migration checks.

The final gate includes Windows MSI installation/upgrade/uninstall and [background updates with verified GitHub/mirror downloads](UPDATES.md), plus the supported platform packaging. Implement the requested scope first, run one consolidated correctness/auth/LF/install/performance matrix, repair its failures together, then publish one consolidated release. Performance samples run in a separate sequential batch after correctness work, without concurrent builds or conformance workers.

The [native full-compatibility core integration report](NATIVE_CORE.md) records the proposed engine surface, storage/migration/rollback contract, build gaps and licensing boundary. Source preparation does not switch the installed client or close these gates.

| Requirement | Required evidence | Current state |
| --- | --- | --- |
| Product identity | Default `git`, version/build options and machine-readable identity identify git-next; real callers can detect compatible capabilities | Preview identity implemented; global default remains original Git |
| Native runtime | No installed Git/OpenSSH fallback for repository operations | Isolated suites use an empty client PATH; explicitly configured helpers are tested separately |
| Daily command compatibility | Git CLI/output, index/ref/reflog semantics, config scopes, branches/tags, diff/show/log/blame, reset/restore, merge/rebase/cherry-pick/revert/stash, hooks/signing and error/exit behavior | Full native engine integrated/compiled; final D08 behavior acceptance pending |
| Repository compatibility | Existing branch/object formats, submodules, worktrees, sparse/partial repositories, LFS, locks and interrupted-operation recovery | Full native repository engine integrated; final D08 format/recovery acceptance pending |
| Network and authorization | Git HTTP/HTTPS/SSH protocol, clone/fetch/push, refspecs, permissions/server rejection, host/certificate verification and concurrent update behavior | Native push fixture coverage added; broader protocol/config coverage remains open |
| Existing login preservation | Real helper binaries, Windows credential vault, SSH keys/agent/config continue working after the old distribution is unavailable; no credential reset | Verified standalone GCM installed; isolated DPAPI and native HTTPS callbacks pass; actual vault/provider login remains open |
| Existing applications | PowerShell/cmd, IDEs and desktop Git callers execute git-next correctly, including callers using absolute Git paths | Not accepted |
| Performance | Optimized final bytes, repeated median/p95 comparisons on representative repositories; startup, status, object lookup, clone/fetch/push and memory; correctness verified independently | Enabled-system snapshots cover seven local operations; 1000/10000-file warm reads are faster in their paired runs, while 100000-file status/diff medians remain slower than Git; broad performance remains open |
| Installation and removal | Verified binary/helper packages, default-command activation, original executable/runtime backups, preserved data/config/login and tested rollback; removal does not break Git-dependent tools | Separate preview installed; replacement/uninstall gate remains closed |

## Historical Rust preview implementation

Native push now transfers Git objects directly through Gitoxide and Russh. Local/HTTPS/SSH fixtures cover branch/tag creation, incremental updates, upstream settings, configured tracking mappings, exact leases, dry-run, atomic multi-ref updates, server policy rejection and partial success. A concurrent local tracking update is protected with compare-and-swap. Blob reachability scanning does not load large blob contents just to discover graph membership.

Credential helper invocation is being made independent of the old Git dispatcher; authenticated TLS fixtures preserve saved login on failed authentication and verify private CA handling. This evidence proves the tested protocol paths, not full workstation takeover or production-account acceptance. Original Git remains installed until every completion gate is accepted. No global parity flag, tag, package or release may be used to bypass missing evidence.

Configuration and command-context acceptance now compares the native client with reference Git for scoped/multivar reads and writes, includes/conditional includes, implicit/empty values, origin/scope/NUL output, -c, repeated -C, --git-dir and repository environment overrides. Full configuration/CLI/IDE conformance still remains a completion gate.

Native LFS pointers/cache/process filters, HTTPS batch/basic transfer and upload-before-ref-publication now pass isolated interoperability/failure checks. Full LFS transport/locking and large-object performance remain open.

Next implementation areas include complete LFS, complete configuration/command behavior, merge/rebase/stash, worktrees/submodules/hooks, real caller integration, broader conformance and measured performance. The full objective stays active while these remain incomplete.

Native diff/show and expanded history formatting now have reference-output and repository-no-write acceptance at source level. [The declared subset](READ_COMMANDS.md) does not cover complete diff/history semantics or change the replacement gate.

Status now refreshes verified native index stat data under the original optional lock. A native-created 1000-file workload improves warm median from 95.104 ms to 41.487 ms; reference Git remains faster at 38.118 ms. This removes repeated hashing after native staging, while broad status/performance and full takeover remain open.

Ordinary-file restore/reset, ORIG_HEAD/reflog protection and LF policy now have scoped Windows source acceptance. Native status verifies canonical filtered content after stat mismatches, so LF/CRLF-only saves do not become false changes. New repositories and clones have LF defaults; old tracked bytes are normalized only by an explicit reviewed command. Existing game projects are not rewritten. Full platform/runtime and takeover gates remain open. See [restoration](RESTORATION.md) and [line endings](LINE_ENDINGS.md).

Filesystem-only system configuration discovery now passes an enabled-system executable-tripwire fixture. The verified standalone Windows GCM package passes isolated DPAPI Get and native HTTPS named-helper callbacks, plus installation/upgrade/failure-rollback/removal fixtures. This removes the tested old-Git discovery/callback dependency. It does not yet establish real vault/provider login migration or permit removal of the old installation. See [discovery](NATIVE_DISCOVERY.md) and [credential support](CREDENTIALS.md).

Ordinary two-parent merge, simple conflict staging/continuation/native abort and required repository extension negotiation now have optimized Windows acceptance. The installed preview retains the LF profile and managed GCM callbacks. Full merge/editor/recovery, rebase/stash and remaining completion gates are still open. See [merge scope](MERGE.md) and [current acceptance](ACCEPTANCE.md).

The shared-cache/Unix-discovery source passes nine local LF/configuration/operation suites in Linux x64 Docker and emulated Linux ARM64 containers. This is separate from native Mac/physical ARM64, complete Linux/network performance or provider acceptance; see [acceptance](ACCEPTANCE.md).

Linux ARM64 now passes the same local LF conformance subset under Docker CPU emulation; Windows ARM64 passes compilation/PE machine checks only. These are distinct from physical ARM64/macOS or full takeover acceptance. See the [current platform matrix](benchmarks/cross-platform-local-checkpoint-0.3.0-dev-20261020.json).

The [installed status-v2 checkpoint](benchmarks/windows-x64-local-checkpoint-0.3.0-dev-status-v2-20261020.json) adds ordinary v2 records, branch/upstream headers, submodule read status, same-name tracked/untracked rows and matching-source Linux container conformance. It retains the declared LF policy and verifies original Git/login/profile preservation. Complete IDE/submodule/rename behavior and the takeover gates above remain open.

The [installed ignored-status checkpoint](benchmarks/windows-x64-local-checkpoint-0.3.0-dev-status-ignored-20261020.json) adds the tested ignored modes and preserves the LF policy. Matching Linux x64/emulated ARM64 pass 16 local suites each; matching Windows ARM64 compilation does not establish runtime behavior. Full takeover and real provider/Unity/Mac acceptance remain open.

The [separate existing-account check](benchmarks/windows-x64-existing-login-readonly-0.3.0-dev-20261020.json) accepts the real GitHub HTTPS helper/account component only. A full cutover must also preserve effective helper selection and required system-config/runtime assets after the old distribution is absent. Private Git, provider renewal and SSH acceptance remain separate gates.

Standard legacy cmd receive-pack/upload-pack/upload-archive wrappers map to their owned native libexec targets, and cmd/scalar.exe maps to the owned native scalar. The original updater and its WinToast notification helper are retired together; their bytes remain in the protected recovery backup. Unrecognized executable paths still block removal. [Producer notification helper](https://github.com/git-for-windows/build-extra/blob/main/git-extra/git-update-git-for-windows).
