# Consistent LF text across platforms

The primary native core integrates shared LF defaults, new-repository/clone configuration, policy-file creation, the eol command and canonical filtered-content comparison after stat/size mismatches. Current Windows x64, Linux x64 and emulated Linux ARM64 native-core checks pass. macOS evidence covers compilation, Mach-O packaging and shell/static checks without executing the product. See [delivery status](DELIVERY_STATUS.md) for the final combined gate. Earlier preview checkpoints below retain their original identities.

## Defaults

New working repositories created with `init` set local `core.autocrlf=input` and `core.eol=lf`. Initialization does not create untracked policy files: the new working tree remains empty, as callers expect. Explicit `eol --apply` creates missing `.gitattributes` and `.editorconfig` files with LF bytes and preserves existing files. New clones set the same local defaults and honor the source repository's tracked attributes; clone also does not introduce policy files into somebody else's project. The earlier Rust preview's automatic policy-file creation on init is retired in the final core.

The shared attribute rule is:

```gitattributes
* text=auto eol=lf
```

EditorConfig sets only `end_of_line=lf`. It does not force indentation, encoding, trailing-space removal or a final newline, avoiding unrelated changes when an editor saves a file.

For an existing repository with no explicit settings, the native runtime defaults to input normalization and LF checkout. Existing `core.autocrlf`, `core.eol`, `.gitattributes`, binary `-text` and LFS rules retain their normal priority. An explicit `eol=crlf` exception remains CRLF on every platform. A personal global setting can still override the unconfigured-repository fallback; `eol --apply` sets a repository's local LF policy explicitly.

Git attributes normalize text into LF in the index and control checkout representation; `text=auto` detects binary contents. Preserve explicit binary rules for formats requiring exact bytes. Do not force `*.asset text`: Unity `.asset` files can be binary or text. Unity YAML `.unity`/`.prefab`/text `.asset`, `.meta` and C# fixtures are covered, alongside binary asset/image preservation. Actual serialization/importer/version changes remain visible. [Git attribute semantics](https://git-scm.com/docs/gitattributes), [EditorConfig properties](https://editorconfig.org/), [Unity Editor serialization settings](https://docs.unity3d.com/6000.0/Documentation/Manual/class-EditorManager.html).

## Existing repositories

```sh
git-next eol --json
git-next eol --apply --json
```

The first command reports the effective configuration and presence of policy files. The second sets local LF configuration and creates missing policy files, preserving existing files and custom rules. It does not rewrite, stage, commit or renormalize existing tracked content. Indirect policy paths and existing config writer locks fail clearly. Ordinary read-only policy files are preserved.

Repositories that historically stored CRLF blobs need one deliberate normalization change. Start from reviewed work, then stage the policy files and run:

```sh
git-next add .gitattributes .editorconfig
git-next add --renormalize .
git-next diff --cached
```

`--renormalize` reapplies clean conversion to existing selected tracked files only. It ignores untracked files and leaves worktree bytes unchanged. Missing tracked files, replaced parent directories and file/directory replacements refuse before object/index writes; review and finish those separate changes first. This matches the tested reference Git behavior. Like Git, normalization also stages real changes in selected tracked files, so review the cached diff and commit normalization separately from business changes. It is never run automatically during installation, opening a repository, status, clone or policy application. Existing custom `.gitattributes`/EditorConfig files may need a project-specific rule update; `--apply` does not silently replace them.

## Preventing false changes

Renormalization preserves unresolved index stages and does not stage their worktree contents. Existing intent-to-add entries follow the tested Git behavior and can become staged content when selected. The staging fixture also checks similar neighbor names and unselected edits; normalizing selected files is still an explicit staging operation.

The earlier Rust preview intentionally refused renormalization through a replaced symbolic-link parent, while the tested Linux reference could read the link target through the old tracked path. That preview limitation is historical, not an accepted claim about the final native core. Final-core path/recovery and renormalization behavior must be checked against the pinned reference in D08.

After a stat mismatch, native status verifies the clean-filtered object hash instead of treating a changed worktree byte count as proof of a content change. LF/CRLF-only saves can therefore remain clean even when their lengths differ. The existing optional index lock can publish verified metadata; locks/read-only indexes retain the previous no-write behavior. Unchanged files still use the stat fast path. Genuine content, executable-bit, file-type and staged changes remain visible. This does not rewrite files or suppress all whitespace changes.

```sh
python tests/eol_acceptance.py target/release/git-next
python tests/status_cache_acceptance.py target/release/git-next
```

Use `.exe` on Windows. Fixtures isolate profiles, remove Git/OpenSSH from the client PATH, compare canonical object IDs and deliberate renormalization with reference Git, and verify Unity-style text, binary bytes, real edits, explicit exceptions and writer locks. No real user login or existing game project is modified by these tests.

## Accepted local scope · 2026-10-20

| Platform | Accepted scope |
| --- | --- |
| Windows x64 | Installed optimized client `c425734c...`; LF/status/real-edit/lock and exact filename-output fixtures; independent 120-call wide-tree stability |
| Linux x64 | Matching musl binary passes 16 isolated local container suites, including LF, status formats and raw filenames |
| Linux ARM64 | Matching GNU binary passes the same 16 suites under CPU emulation; physical ARM64 remains unverified |
| Windows ARM64 | Matching ignored-status source: five executable builds/PE machine checks; actual runtime remains unverified |
| macOS | Shared LF source policy; current native build/install/Unity/keychain acceptance remains open |

This workstation uses global `core.autocrlf=input` and `core.eol=lf`. The latest upgrade verifies original Git/configuration/SSH files and PATH unchanged. No existing game project was rewritten, staged or renormalized. Canonical LF/CRLF-only saves remain clean in the accepted native client, while real content/type/mode changes stay visible. Default `git` still resolves to the original installation until full takeover is accepted.

The [current platform matrix](benchmarks/cross-platform-local-checkpoint-0.3.0-dev-status-ignored-20261020.json) and [installed checkpoint](benchmarks/windows-x64-local-checkpoint-0.3.0-dev-status-ignored-20261020.json) pin exact source/archive/binary/fixture identities. [Acceptance](ACCEPTANCE.md) preserves older checkpoints with their original scope; their latency and runtime results are not reassigned to newer binaries. Actual Unity Editor/device acceptance, current Mac and full Git replacement remain open.
