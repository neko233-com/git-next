# git-next credential helper patch

Source: gitoxide `gix-credentials` 0.42.0, copied from the crates.io source package. Original MIT and Apache-2.0 license texts are retained beside the source.

The `ExternalPath` credential helper branch uses the existing native argument-splitting command builder. A simple absolute executable path is launched directly instead of requiring an installed POSIX shell. Named helpers use `git-credential-NAME` directly, without an external Git dispatcher. Explicit shell-script helpers retain their shell semantics. git-next additionally resolves configured helper executable paths and reuses credentials without automatically storing or erasing saved logins on network authentication.

Validate via `tests/push_acceptance.py --transport http`, with an absolute fixture helper executable, empty client PATH and no Git/OpenSSH programs. The fixture secret is generated at runtime in an isolated directory and is never included in source or reports.

`Program::set_environment` applies per-helper environment overrides without mutating the parent process. Managed GCM uses this to direct its Git configuration/repository callbacks to the native git-next preview, even when the parent inherited an old `GIT_EXEC_PATH`. Other helpers retain their environment. No protocol/store format changes; rollback uses the previous binary and preserves the credential store. Validate `tests/gcm_acceptance.py` with an old-Git executable tripwire and isolated DPAPI fixtures.
