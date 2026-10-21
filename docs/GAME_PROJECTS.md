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

The native index/stat fast path, Windows filesystem cache, ignored-directory traversal and streaming LFS transfers cover large asset trees without treating editor opens as content edits. Large-file cache layout and Git object/ref/index storage keep their standard paths. No speculative repack, history migration or credential reset occurs during installation.

Acceptance uses generated repositories/accounts: Unity YAML/.meta and binary bytes, CRLF-only saves and genuine edits, large asset push/clone/checkout, cache corruption, transfer failure before refs, lock/unlock/conflicts, existing hooks and deep/Unicode paths. The [delivery ledger](DELIVERY_STATUS.md) distinguishes implemented behavior from completed final-byte checks.
