# Windows default installation layout

The `git-default-v2` MSI layout installs in `%ProgramFiles%\Git` for machine scope
and `%LOCALAPPDATA%\Programs\Git` for user scope. The primary IDE entry point is
`cmd\git.exe`; `bin\git.exe` and Git-Terminal belong to the same owned installation.
The Git engine, repositories, LFS storage and user credential stores do not change.

Changing the directory is a major MSI upgrade, with new product/component identities
and the existing UpgradeCode. The old product is removed inside the MSI transaction
before installing the relocated components. Small-update repair cannot relocate an
existing product. Upgrades after layout v2 retain a custom installation directory.

An official Git installation is replaced in place. The MSI requires the explicit
`REPLACEGIT=1` authorization, which only `install.ps1` passes after it has recorded a
protected backup target. The property is deliberately not marked `Secure`: a Secure
property cannot be set from the command line during `InstallExecuteSequence` and is not
carried from the client to the server for a managed per-machine install, so the value
would silently stay `0` and the launch condition would block every install on a machine
that has an official Git registered. Because the authorization now travels as an ordinary
public property, `install.ps1` verifies from the verbose MSI log that the installer really
recorded `REPLACEGIT=1` before it continues, and refuses the installation when it did not.
The takeover then recognises the identical directory
(`same_directory`), removes the official uninstaller's leftovers instead of covering them
with bridge forwarders, and points the migrated `system.gitconfig` include at the payload's
own `runtime\etc\gitconfig`. Because the payload now occupies the official directory,
rolling back to official Git requires removing the v2 MSI before the protected takeover
backup can be replayed. The layout migration retains its original recovery journal and user
data. On failure, MSI rollback restores the previous product; the migration driver restores
the prior pointer. Existing application-owned configuration outside the MSI tree
is preserved.

Relocation temporarily needs space for both installations and the MSI cache. The
driver verifies immutable payload hashes and configuration preservation before
reporting success. It must not delete backups, user repositories or credential files.
