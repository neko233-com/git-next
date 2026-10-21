# Native diff, show and history

The primary product uses the complete source-built native Git compatibility core for diff, show, history and status. It does not dispatch these commands to installed official Git. Current acceptance is recorded in [delivery status](DELIVERY_STATUS.md). The Gitoxide implementation details below describe the separate historical Rust preview, not limitations of the primary native product.

## Historical Rust preview

## Status

The source now uses Git-style plain long output by default and accepts `--long`. It groups staged, unmerged, unstaged, untracked and ignored paths, retains ordinary submodule change notes, and shows named/unborn/detached and upstream/ahead-behind information. `status.short`, `status.branch` and `advice.statusHints` are validated; explicit format options override configuration and the last explicit format wins. `--long` with `-z` refuses before optional index publication. Long paths retain spaces and quote control/non-ASCII bytes according to `core.quotePath`; ordinary relative-path settings apply.

Long output reads common merge, cherry-pick, revert, bisect, merge/apply-backend rebase and am state. Rebase output covers conflict/resolution, interactive edit stops and a bounded todo summary. Operation metadata reads are capped at 64 KiB per file; unsupported rebase commands or malformed metadata refuse before output/index publication. This is read-only status support: native rebase/cherry-pick/revert/bisect/am mutation is still unimplemented. Stash counts, color/column/localized output and complete operation/format configuration parity remain open. Repository/index/credential layouts are unchanged; rollback uses the prior binary.

Native status supports porcelain v1 (`--porcelain[=v1|1]`) and v2 (`--porcelain=v2|2`), short output (`-s`/`--short`), NUL output (`-z`), `-u`/`-uno`/`-unormal`/`-uall`, `--untracked-files[=no|normal|all]` and pathspecs after `--`. The bare untracked option means all files; absent options honor `status.showUntrackedFiles`. CLI selection overrides that configuration. Wildcard/exclusion selection does not replace matching children with an unrelated directory; ordinary matching untracked directories remain collapsed in normal mode.

Tracked changes precede untracked rows. Text records honor `core.quotePath` and quote spaces/control bytes; NUL records preserve filename bytes without quoting and put a rename's destination before its source. Porcelain and NUL paths are relative to the repository root; ordinary short output honors the current directory and `status.relativePaths`. Staged executable/type changes, intent-to-add and all seven conflict masks retain their distinct codes. `status.renames`/`status.renameLimit` override diff defaults; merge configuration does not change status. Invalid supported settings refuse before optional index refresh. Canonical LF/CRLF-only saves stay clean; actual edits remain visible.

Porcelain v2 emits ordinary, renamed/copied and unmerged records with HEAD/index/worktree modes and full object IDs. Text v2 paths retain spaces without v1's extra quoting; NUL rename paths retain destination/source ordering. Similarity scores count reusable LF/64-byte spans, including reordered lines; complete rename candidate selection, custom drivers and large-blob parity remain open. Rendering completes before optional index refresh, so an unsupported record/metadata failure does not publish a stat-cache change.

`-b`/`--branch` emits branch/upstream headers in short or porcelain output, including unborn/detached states, missing tracking refs and ahead/behind counts. Counts use the native commit walk; `--no-ahead-behind` avoids that walk and marks unequal tips with unknown counts. Ordinary submodule status distinguishes commit changes, tracked modifications and untracked content. `--ignore-submodules[=none|untracked|dirty|all]` overrides configuration; bare means all, including staged gitlink changes. Native submodule creation/update and complete recursive/format behavior are not accepted.

`--ignored[=traditional|matching|no]` includes ignored paths after untracked paths, using `!!` in v1/short output and `!` in v2. Bare means traditional. Traditional mode normally collapses ignored directories and omits empty ones; `-uall` requests their files. Matching mode reports explicitly ignored directories, including empty directories, and keeps file-level matches separate. It retains Git's common-prefix behavior for directory pathspecs, which can include an explicitly ignored directory even when a file wildcard or exclusion would not select that directory. Global excludes, `.git/info/exclude`, nested `.gitignore`, negative rules and forced tracked files retain their roles. Effective untracked mode `no` suppresses traditional ignored output and rejects matching mode before index locking. Existing tracked edits and staged deletions remain visible. [Git status contract](https://git-scm.com/docs/git-status).

The fixtures `tests/status_format_acceptance.py`, `tests/status_v2_acceptance.py` and `tests/status_ignored_acceptance.py` use isolated SHA-1/SHA-256 repositories, raw-byte reference comparisons, unchanged repository files and no Git/OpenSSH on the client PATH. `--include-long` extends the first two matrices to long output; `tests/status_long_acceptance.py` and `tests/status_operation_acceptance.py` cover default/explicit/configured formats, tracking/detached states and operation summaries. Windows covers Unicode/space names and emulated symlinks; Unix additionally exercises quote/control/backslash and non-UTF8 names. The ignored fixture also checks forced tracked ignored files, staged deletions, ignored empty/nested directories and ordinary nested repository boundaries. See [acceptance](ACCEPTANCE.md) for exact accepted binaries; source support alone does not establish current Mac or full IDE acceptance. Stash headers, complete long-output configuration and complete submodule behavior remain open; unsupported options refuse.

## Diff

`diff` compares index/worktree, `--cached` compares HEAD/index, and one or two revisions compare worktree or trees. Two-dot ranges and pathspecs after `--` are supported, including exclusions, globs and directory-relative selection. Intent-to-add entries retain their distinct index/worktree meaning.

Supported output includes unified patches (`-U`), binary markers, missing-newline markers, exact/edited renames, executable and file-type changes, `--name-only`, `--name-status`, `--numstat`, `--stat`, `--shortstat`, `-z`, `-R`, `--exit-code`, `--quiet`, `--no-patch`, `--full-index`, `--text`, `--no-renames` and Myers/minimal/histogram algorithms. Configured object-ID abbreviation checks collisions even when the worktree blob has never been stored. Path quoting and `core.quotePath` follow the tested Git output.

Working files pass through the native clean-filter pipeline. With `core.symlinks=false`, emulated link target bytes bypass text/LFS conversion and retain the link mode. Temporary trees and converted worktree objects live only in memory: diff/show do not write objects or refresh the index. Identical snapshots avoid tree reconstruction, and binary output skips text-line diff computation.

The current Windows optimized and installed checkpoint validates this scalability change: unchanged index/worktree scans can avoid building full snapshot maps, while dirty/intent-to-add and revision comparisons keep the existing content path. Diff status scanning uses one worker per 2000 entries, capped at eight. See the exact bytes and declared conformance/performance scope in [acceptance](ACCEPTANCE.md) and [performance](PERFORMANCE.md).

Windows workers also share directory listings within that one scan, retaining local lookup maps and live metadata fallback. Each new scan starts fresh. The original cache, stat/hash checks and `core.fscache` setting remain active; disabling the cache is not the selected default. See the per-scan lifetime/rollback notes in `vendor/gix-status/PATCHES.md` and the exact retained experiments in [performance](PERFORMANCE.md).

Three-dot merge-base comparisons, conflicted/sparse indexes, combined diffs, full whitespace/color/driver/textconv/binary-patch/submodule behavior and complete statistics scaling remain open. External drivers and text conversions refuse unless explicitly disabled with `--no-ext-diff` / `--no-textconv`. Blobs above 256 MiB refuse before output; memory and large-file performance need further work.

## Show and log

`show [object]` prints commit metadata/patches, raw blob bytes, tree entry names or annotated tags. Common medium/short/full/fuller/raw/oneline formats, `--abbrev-commit`, ISO/RFC/short/unix/raw dates, metadata templates and NUL output are supported. Templates include object/tree/parent IDs, subject/body/message, author/committer identity and dates, `%n`, `%%` and `%xHH`. Unsupported placeholders refuse.

`log` shares the metadata formatter and keeps one repository open for the walk. It accepts a single starting commit or tag, `-n` / `--max-count`, numeric counts such as `-2`, `--oneline`, common pretty/date formats and explicit template NUL records. The default no longer truncates history to 20 commits. Range/set selection, path history, complete traversal ordering, decorations, graph output and full encoding/mailmap/format behavior are not accepted. Merge patches in show require `--first-parent`; combined merge output is not implemented.

## Verification

`tests/status_directory_acceptance.py` additionally compares same-name file/directory replacements, staged deletion plus an untracked file or directory, empty directories and nested repositories in both object formats. Status row identity retains tracked deletions independently of same-name untracked results. Ordinary and `-uall` output follow their distinct tested Git behavior. Full IDE acceptance remains separate.

```sh
python tests/diff_acceptance.py target/release/git-next
python tests/benchmark.py target/release/git-next --read-commands --runs 40 --files 1000
```

On Windows use `.exe`. The acceptance fixture compares bytes and exit status against reference Git, then checks all `.git` files remain unchanged. It covers unstaged/staged/tree comparisons, renames, pathspecs, binary/CRLF/Unicode paths, ID collisions, emulated links, type changes, show formats and log records. Unsupported options must fail without output or writes. The client PATH contains no Git/OpenSSH. This is scoped compatibility evidence; full Git parity and global takeover remain false.
