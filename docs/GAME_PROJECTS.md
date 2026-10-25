# Game projects and built-in LFS

Git-next includes the pinned Git LFS 3.7.1 runtime on every supported platform. Windows MSI installation needs no separate LFS download or installer. Product defaults register the standard LFS clean/smudge/process filters. New repositories and clones receive recognizable standard pre-push/post-checkout/post-commit/post-merge hooks from the owned templates. Existing custom templates, hooks and explicit filter overrides retain their precedence.

Existing repositories keep their hooks and configuration. If a repository has never enabled LFS, git lfs install --local performs repository configuration using the already bundled helper; it does not install another application. Existing custom hooks are preserved and require deliberate integration rather than forced replacement.

## Unity and Tuanjie

Track Assets, ProjectSettings, Packages and required .meta files. Ignore generated Library, Temp, Obj, Logs and UserSettings directories. Keep text scenes, prefabs, C# and metadata as LF; use text=auto for .asset because that extension can contain binary data. Do not force every .asset to text or to LFS.

The [Unity ignore profile](game-profiles/unity.gitignore) and [attribute profile](game-profiles/unity.gitattributes) are explicit starting points. Merge their rules with existing policies after review. Installation never rewrites an existing game repository or replaces its policy files.

~~~sh
git init
git eol --apply
git lfs track '*.fbx' '*.psd' '*.tga' '*.exr' '*.wav' '*.mp4' '*.unitypackage'
git add .gitattributes .editorconfig
~~~

LFS tracking is a deliberate project policy. Small text and Unity YAML remain diffable. Native status compares canonical cleaned contents after stat/size changes: LF/CRLF-only saves can remain clean; real Unity serialization/importer/content changes remain visible.

## Unreal

Keep Source, Config, the .uproject and required project build configuration. Exclude generated Binaries, DerivedDataCache, Intermediate and Saved data. The profile does not discard the project's Build source/configuration directory.

Use LFS for .uasset/.umap and lockable attributes for assets that cannot be merged. [Unreal profiles](game-profiles/unreal.gitattributes) include these explicit rules.

~~~sh
git lfs track --lockable '*.uasset' '*.umap'
git lfs lock Content/Maps/Main.umap
git push
git lfs unlock Content/Maps/Main.umap
~~~

The server must support the standard LFS locking API. Native pre-push uploads required objects before Git ref publication and verifies supported locks. Upload/verification failures reject the push. Locking is distinct from offline local Git commits.

## Existing histories and large trees

Changing attributes does not rewrite history. Review explicit git add --renormalize changes separately from business work; git lfs migrate rewrites only when the operator deliberately invokes it. Shared branches need ordinary coordination.

The native index/stat fast path, ignored-directory traversal and streaming LFS transfers cover asset trees without treating editor opens as content edits. Windows keeps filesystem caching, index preloading and parallel index/grep reads disabled after an incomplete-result regression; throughput must never depend on omitting files. Large-file cache layout and Git object/ref/index storage keep their standard paths. No speculative repack, history migration or credential reset occurs during installation.

## Multiple projects

Independent projects use independent repositories, with their own configuration, refs, index, locks and LFS cache. No project registry or resident process is required:

~~~sh
git -C UnityA status --short
git -C UnrealB status --short
~~~

A monorepository can contain both engines. Put the Unity profiles in `UnityProject/.gitignore` and `UnityProject/.gitattributes`, and the Unreal profiles in `UnrealProject/`. These ignore patterns are anchored to each profile's directory. A single copy at the monorepository root cannot identify every nested engine project. Both project directories share the enclosing repository's Git configuration, refs and index; an index lock therefore applies to that whole repository.

Keep Unity `Assets`, `.meta`, `Packages` and `ProjectSettings`, and Unreal `Source`, `Config`, `Build` and the `.uproject` under review. Ignore generated caches at their actual project roots. Existing policy files are merged deliberately, never replaced by installation.

## Resource budgets

The common resource settings are rendered from `packaging/runtime-defaults/gitconfig` for Windows and POSIX packages:

| Setting | Default | Scope and tradeoff |
| --- | --- | --- |
| `maintenance.auto` | `false` | Ordinary commit/fetch operations do not start automatic maintenance. Manual maintenance and `gc` remain available; long-lived repositories need deliberate maintenance. |
| `pack.threads` | `2` | Bounds compression workers per packing process. A single large pack may take longer than using every CPU. |
| `pack.windowMemory` | `32m` | Delta-search window budget per packing thread. |
| `pack.deltaCacheSize` | `32m` | Delta-result cache per packing process; smaller caches can require recomputation. |
| `core.deltaBaseCacheLimit` | `32m` | Decoded delta-base cache per process. |
| `lfs.concurrenttransfers` | `2` | Transfer workers per LFS process; several active repositories still multiply the total. |

These are resource knobs, not a hard RSS limit. Indexes, objects, oversized individual assets, helper runtimes and other allocations still consume memory. Repository settings and `git -c` retain ordinary precedence. See [runtime defaults](DEFAULTS.md) for the measured comparison and its limits. Git's memory suffixes are binary (`32m` means 32 MiB), and `pack.windowMemory` is per thread. [Git configuration reference](https://git-scm.com/docs/git-config).

In the [Windows resource fixtures](benchmarks/native-game-resources-20261025.json), first packing across four projects with eight 16 MiB assets each reduces whole-job peak commit from about 1519 to 440 MiB with identical compressed pack byte counts. The separate 60,037-path multi-project integrity matrix passes all 33 cases. A single 64 MiB asset does not show the same memory benefit, and warm repacks can be slower. These are generated-layout results on the profiled native 1.0.3 bytes, not real Editor or later release-byte acceptance.

For example, a project that values transfer throughput over the default budget can explicitly use `git -C UnityA config --local lfs.concurrenttransfers 4`. This changes that repository only. Plan concurrent editor/build/VCS activity across projects; the product does not impose a machine-wide memory limit on arbitrary callers.

Acceptance uses generated repositories/accounts: Unity YAML/.meta and binary bytes, CRLF-only saves and genuine edits, large asset push/clone/checkout, cache corruption, transfer failure before refs, lock/unlock/conflicts, existing hooks and deep/Unicode paths. The [delivery ledger](DELIVERY_STATUS.md) distinguishes implemented behavior from completed final-byte checks.

The Go tools `native-game-multirepo-acceptance`, `native-game-lfs-stream-acceptance`, `benchmark-game-resources` and `benchmark-game-lfs-resources` retain their actual fixture sizes, input hashes, failure cases and measurement scope. The LFS service streams object bodies to disk through fixed buffers. Windows resource benchmarks measure kernel job peak committed memory across the owned process tree, not just the entry process and not RSS. These fixtures do not claim real Unity/Unreal Editor acceptance or absolute stability.
