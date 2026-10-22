# git-next Gitoxide integration patches

Source: gitoxide `gix` 0.89.0 from the crates.io source package. Original MIT and Apache-2.0 licenses are included.

Status rename keys use the `status` section instead of `merge`. This restores `status.renames` / `status.renameLimit` precedence over the ordinary diff defaults and prevents unrelated merge configuration from changing status output. Validate rename enable/disable/fallback precedence with `tests/status_format_acceptance.py`; no configuration file or repository layout is migrated.

`status::iter::Outcome::apply_changes` exposes the existing stat-cache update step without performing I/O. The existing `write_changes` delegates to it and keeps its previous behavior. git-next can therefore acquire an optional index lock before scanning and publish that exact scan's metadata under the same lock, preserving concurrent index writers. It does not reacquire a lock after scanning an older index snapshot.

git-next limits automatic refresh to ordinary v2 indexes with supported cache extensions, honors `GIT_OPTIONAL_LOCKS=0`, and skips refresh if another writer already owns the lock. Other index formats and extension payloads remain untouched. Validate with `tests/status_cache_acceptance.py`.

The worktree-to-object helper recognizes an indexed symlink stored as a regular file when `core.symlinks=false`. Its target bytes bypass text/LFS conversion and retain the symlink mode, matching Git's Windows emulation. Validate with `tests/diff_acceptance.py`, including native staging with CRLF conversion enabled.

The status iterator uses `HashEq` after a stat mismatch, retaining the existing stat fast path for unchanged files. The upstream `FastEq` size shortcut can report a modification when clean/EOL conversion maps different worktree lengths to the same stored bytes. Canonical comparison avoids that false change and allows the existing optional stat-cache update to store the verified new worktree size. Real byte/mode changes still report normally; no file or staged object is rewritten. Validate repeated LF/CRLF editor saves with `tests/eol_acceptance.py` and lock/cache behavior with `tests/status_cache_acceptance.py`.

The clean pipeline has an opt-in `renormalize` mode that skips only the legacy CRLF index-content guard. Normal cleaning is unchanged; Git LFS and other filters still run, binary detection and emulated-link handling remain active. Native `add --renormalize` limits its candidates to tracked paths and does not rewrite the working tree.

Installation configuration is enabled in the default trusted loader now that the vendored `gix-path` resolves paths without executing Git. This preserves earlier Windows ProgramData configuration alongside the discovered installation's system configuration. The existing source loader deduplicates overridden paths and preserves includes and origin metadata. See `docs/NATIVE_DISCOVERY.md`; isolated permissions remain disabled and no on-disk data migration is performed.

Repository opening negotiates required extensions before constructing the reference store and again after configuration includes/overrides load. Unknown version-1 extensions, reftable/unknown reference storage, promisor repositories and invalid precious-object values fail closed. Supported files references and SHA-1/SHA-256 layouts are unchanged, including version-0 optional unknown keys. This prevents native staging from modifying a reftable index before discovering that HEAD uses an unsupported backend. Validate with `tests/repository_format_acceptance.py`; rollback is the previous binary, with no layout migration.
