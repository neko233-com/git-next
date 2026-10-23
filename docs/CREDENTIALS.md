# Existing credentials and managed GCM

The native core uses Git credential helper protocol semantics, including get/store/erase, and packages include owned GCM runtimes. Current Windows DPAPI and isolated Docker generated-account HTTPS/SSH migration checks pass without contacting the user's private accounts. Existing configuration/store identifiers remain compatibility contracts. Actual workstation profile preservation is recorded separately in [delivery status](DELIVERY_STATUS.md). Earlier preview checks below retain their original identities.

## Existing-account read check · 2026-10-20

Installed Windows x64 client `c425734c0640...` passes a separate [bounded real-account check](benchmarks/windows-x64-existing-login-readonly-0.3.0-dev-20261020.json). The owned Microsoft-signed GCM 2.7.3 helper reads an existing GitHub HTTPS credential with native `git.exe` configured through `GIT_EXEC_PATH`, no old Git/OpenSSH on PATH and client interaction disabled. A separate HTTPS verifier sends that credential only to GitHub's authenticated-user endpoint and receives HTTP 200. It does not read or save the account response body. No token, account identity, key value or provider diagnostic is recorded; no helper Store/Erase command is invoked and the checked profile files stay unchanged.

GCM's [pinned callback selection](https://github.com/git-ecosystem/git-credential-manager/blob/v2.7.3/src/shared/Core/CommandContext.cs#L170-L185) returns the existing executable under `GIT_EXEC_PATH` before searching PATH. The receipt pins the installed client/helper, official source archive and diagnostic scripts. GitHub documents [HTTP 200 for authenticated-user reads](https://docs.github.com/en/rest/users/users#get-the-authenticated-user). This establishes the existing HTTPS helper/account component, with the native client's real private-Git handshake, provider UI/token-renewal branches and other providers still requiring separate acceptance.

The native GitHub SSH read and a direct default-identity OpenSSH reference both reject authentication in this noninteractive check. No SSH configuration or known-host bytes are changed. The reference deliberately bypasses custom SSH configuration, so it is not a full comparison of all existing SSH settings. It does not establish an SSH regression or successful SSH login migration.

Before removing the old distribution, preserve the effective credential-helper selection and any required system-config/runtime assets in native-owned support. A readable standalone helper alone does not replace configuration that could disappear with the old installation. The preview does not silently enable a helper; full takeover remains blocked.

## Isolated Docker migration check · 2026-10-20

The matching Linux x64 client `f35cfe08...`, source `f079bc55...`, passes [13 migration/authentication checks](benchmarks/linux-x64-docker-migration-0.3.0-dev-20261020.json). A reference-server container seeds an old Git worktree, global LF settings, SSH configuration/key/known-hosts and generated GCM credentials. The separate client image physically excludes official Git, Git LFS and OpenSSH. An `execve` audit verifies its Git callbacks use the native branded `git`; standalone upstream GCM 2.7.3 runs independently of the old distribution.

Private HTTPS read/clone/push/fetch/ff-only pull and embedded SSH read/clone/fetch/push succeed. Rejected/missing HTTPS credentials, an untrusted TLS certificate, incomplete helper support and unknown/changed SSH host keys or a rejected identity refuse while retaining repository/profile bytes. The original temporary global configuration, credential files and SSH material remain byte-identical; server and three working-repository fsck checks pass. CRLF-only saves of Unity-style YAML stay clean, real edits remain visible and stage LF, and binary bytes remain exact.

This uses generated accounts with GCM's Linux plaintext store inside a disposable Docker volume, never the user's vault or keys. It does not establish Linux desktop secret-store, macOS Keychain, OAuth/renewal, real private-provider, physical-host performance or full Git compatibility. Runtime networking is internal, ports are unpublished and container root filesystems are read-only. The receipt records the pinned upstream archive, binary/image/harness hashes and successful removal of both containers, their volume/network and both temporary images. No global Docker prune runs.

```powershell
go -C tools/devtool run . migration-host --native-package <linux-x64-package.tar.gz> --output <new-report.json>
```

Use the accepted Linux x64 package and matching cross-build receipt; the runner refuses mismatched binaries. Obtain the standalone helper from the [official GCM release](https://github.com/git-ecosystem/git-credential-manager/releases/tag/v2.7.3); its accepted Linux archive SHA-256 is `c7935c3e1b22681d56011b580086aeb6d60f3f0aaf6ef9c581480356988ff804`. The report's archived harness is authoritative for this checkpoint; later source changes need separate acceptance.

## Windows x64 support package

`install-credential-support.ps1` installs the official standalone Git Credential Manager 2.7.3 package separately from Git. It requires an existing matching git-next preview and accepts only the pinned official `gcm-win-x64-2.7.3.zip` SHA-256:

```text
529ea8aa1379b3a11bd8aafe4705a839cf253a41e14908fca113abeefa30657f
```

Obtain the archive from the [official GCM 2.7.3 release](https://github.com/git-ecosystem/git-credential-manager/releases/tag/v2.7.3). The installer verifies the archive digest, the executable's valid Microsoft signature and the exact version before publication. It preserves the entire upstream package, NOTICE and GCM license; no Git or OpenSSH runtime is included. The package uses Windows .NET Framework, matching the existing workstation helper. Other OS/architecture packages require their own acceptance.

```powershell
./install-credential-support.ps1 -Archive <path-to-gcm-win-x64-2.7.3.zip>
```

Custom `-Prefix` and `-CommandDirectory` must match the original preview installation. The owned files live under `<prefix>/support/gcm`; `credential-support.json` records package/file hashes and support locators. LF JSON locators next to `git-next` and the isolated `git` preview identify the same owned helper and native callback directory.

When an existing configuration selects `credential.helper=manager`, git-next resolves the owned helper and sets `GIT_EXEC_PATH` for that helper process to the native preview directory. This overrides an inherited old-Git callback path without changing the parent environment or system PATH. Other named/path/script helpers retain their configured resolution and semantics. An incomplete managed locator fails before authentication instead of falling back to old Git. The installer does not enable a helper in Git configuration or run GCM configure, unconfigure, diagnostics, login, logout or credential deletion.

The native CLI implements the helper's `rev-parse --absolute-git-dir`, `remote -v show` and common configuration reads. Metadata-directory discovery retains its directory name rather than misclassifying a literal `.`. Native HTTPS/SSH `ls-remote` requests every advertised namespace, including HEAD, without deriving a server-side prefix filter from its wildcard refspec; fixtures compare nonempty refs with reference Git. Version output continues to identify git-next. GCM's numeric official-Git version parser does not recognize this product identity; generic-provider fixture acceptance does not prove all GitHub/Azure/Bitbucket/GitLab UI, OAuth, token renewal or provider capability branches.

## Upgrade, rollback and removal

An upgrade preserves the previous support directory, locators and manifest as timestamped backups. If publication fails, newly published unchanged files are removed and previous directory/locator bytes are restored. An existing install lock rejects another installer. Locally modified files are preserved. No repositories, global configuration, Windows vault records, SSH material or LFS objects are migrated.

```powershell
./install-credential-support.ps1 -Uninstall
./install.ps1 -Uninstall
```

Support removal deletes only files and locators matching the recorded hashes, retaining modified files and backups. Use the same custom paths when applicable. Rollback may restore the recorded previous owned directory and locator backups; the original credential store does not need migration. Removing the original Git distribution remains blocked while full parity and actual caller/login acceptance are incomplete.

## Reproducible acceptance

```powershell
go -C tools/devtool run . native-discovery-acceptance target/release/git-next.exe
go -C tools/devtool run . gcm-acceptance target/release/git-next.exe --helper <standalone-gcm-executable>
./tests/credential_support_install_acceptance.ps1 -Archive <official-gcm-zip>
```

Fixtures generate temporary credentials and store them under an isolated DPAPI directory. They check Get inside/outside repositories, reference CLI output, HTTPS authentication through the configured named managed helper, native callbacks despite an old-Git tripwire on PATH, unchanged saved fixture credentials and failure on missing callback support. The installer fixture verifies hashes/signature, LF locators, failed-upgrade rollback, upgrade and conservative uninstall. Real Windows vault credentials are never read or written by these fixtures. A real existing-account acceptance, provider UI renewal, macOS Keychain and Linux stores remain outstanding.

See [native discovery](NATIVE_DISCOVERY.md), [installation](INSTALLATION.md) and [complete takeover gates](TAKEOVER.md). GCM store configuration and isolation variables follow the [pinned upstream environment documentation](https://github.com/git-ecosystem/git-credential-manager/blob/v2.7.3/docs/environment.md).
## Native POSIX credential helper inputs

The final native route now has pinned GCM 2.7.3 assets for Linux x64/ARM64 and macOS ARM64 in `packaging/posix-helpers.lock.json`. They are primary owned helper inputs, with producer digests/notices and logical `git-credential-manager`/legacy `git-credential-manager-core` aliases. `go -C tools/devtool run . fetch-posix-helpers` only downloads and stages bytes; it does not configure a credential store, contact an account, execute the helper or read a personal profile. The [POSIX build checkpoint](../native-core/receipts/implementation-posix-core-20261020.json) is input evidence only.

Windows system-config migration now selects the retained original system config when it exists, then applies product LF defaults. It does not prepend another credential helper list; ordered helper behavior and existing saved-login providers must remain intact. Native generated-account and real caller/store behavior remain in the consolidated D08 acceptance.

## Owned POSIX helper material

The Linux runtime collector includes the complete pinned primary GCM/LFS inputs and their required library material. It preserves .NET single-file host offsets and uses an owned launcher with a helper-scoped library path. Assembly must not patch that host as an ordinary ELF or prepend a new credential.helper ahead of a retained ordered system configuration. These are preparation/source changes; no real account, desktop secret store or Keychain acceptance has run for the new full core.

## Native desktop integration

POSIX installation preserves existing global settings and includes the prior system configuration at its original path through a managed wrapper; it does not export credential contents. Explicit credential helper choices, order and empty resets remain authoritative. With no configured helper, macOS uses the source-built Keychain helper; Linux uses the owned libsecret helper when an active D-Bus session exists. Primary LFS/GCM and SSH producer files are packaged dependencies, never fallbacks to an old installed Git/OpenSSH. Password rejection continues to follow Git/helper behavior and may invalidate rejected credentials; preservation during migration does not suppress an explicit helper erase operation.

The complete Windows package sets Schannel in its system defaults, preserving higher-priority user/repository choices. Tests that deliberately disable system configuration select Schannel explicitly when checking its CA and revocation options. Generated GnuPG test homes stay short enough for the bundled MSYS agent socket; this does not change users' existing key paths.
