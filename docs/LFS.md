# Native Git LFS

The current native product bundles Git LFS 3.7.1, registers standard filters and supplies standard LFS hooks in new-repository templates. No separate LFS application installation is needed. Existing repositories/custom hooks remain untouched. [Game-project behavior and current scope](GAME_PROJECTS.md) supersede historical Rust preview notes below; final new-byte acceptance remains in the delivery ledger.

The final native route uses the pinned owned Git LFS 3.7.1 primary helper, with Windows runtime inputs and Linux x64/ARM64/macOS ARM64 assets prepared. It preserves the standard pointer/cache/filter/transport interfaces; complete runtime assembly and D08 acceptance remain open. Historical Rust pointer/filter/basic-transfer implementation and subset evidence are retained below and do not establish final native-product compatibility.

## Local use

Build all binaries with `cargo build --release --locked --bins`. Keep `git-next-lfs-filter` beside `git-next` and the git-named preview. The installer supplies the native standalone LFS command as `git-next-lfs` and puts a `git-lfs` alias only inside the isolated preview directory.

```sh
git-next lfs install --local
git-next lfs track '*.bin' 'asset with spaces.dat'
git-next add .gitattributes asset.bin
git-next commit -m 'Track asset with LFS'
git-next push origin main
git-next lfs fetch origin HEAD
```

Installation is explicitly repository-local; global installation is not implemented. It preserves recognized standard LFS hooks, refuses custom hooks before configuration changes, and installs missing LFS hooks without replacing another writer's files. Track appends attribute rules under an exclusive `.gitattributes.lock`, preserving existing content and permissions. It does not rewrite existing Git history.

Standard `filter.lfs` commands are routed to the sibling native filter executable for add/status/checkout. Customized filter commands retain their explicit program dependencies. Root `-c` options reach filter children through standard configuration environment variables. A configured `--skip` or `GIT_LFS_SKIP_SMUDGE` leaves pointers in the worktree.

## Objects and transfer

Pointers use SHA-256 and the canonical v1 encoding; legacy version aliases are readable. Empty files pass through unchanged. The cache remains `COMMON_GIT_DIR/lfs/objects/aa/bb/OID`, or the existing `lfs.storage` location. Relative storage paths are resolved under the common Git directory. Clean streams content into a temporary file; object publication never overwrites an existing cache object. Smudge verifies size and SHA-256 before output. Corrupt existing cache files are preserved and cause failure. These paths follow the [Git LFS pointer and cache specification](https://github.com/git-lfs/git-lfs/blob/main/docs/spec.md).

The batch/basic adapter implements authenticated HTTPS POST batch, PUT upload, optional POST verify and GET download with reusable connections. It supports configured `lfs.url`, remote LFS URLs and `.lfsconfig`, or HTTP(S) remote endpoint discovery. Existing credential helpers are queried on authentication challenges; saved credentials are neither stored nor erased. Configured CA bundles retain certificate validation. Action URLs reject embedded credentials and HTTPS downgrade; origin credentials are not automatically forwarded to an action URL. The protocol is described by the [batch API](https://github.com/git-lfs/git-lfs/blob/main/docs/api/batch.md) and [basic transfers](https://github.com/git-lfs/git-lfs/blob/main/docs/api/basic-transfers.md).

Git push completes required LFS uploads before sending Git reference updates. A batch/upload/verify failure prevents those updates. Dry-run performs no LFS upload. Uploads already accepted by the LFS server can remain if a later Git receive rejects the push; Git refs and LFS object storage are separate protocols. `GIT_LFS_SKIP_PUSH` explicitly skips upload, matching the operator's requested override.

The process filter negotiates clean/smudge only, spools request/result streams and sends success after processing finishes. It can continue after a per-file error. Delay is not advertised. See the [filter-process protocol](https://git-scm.com/docs/gitattributes#_long_running_filter_process).

## Verified scope and remaining work

`tests/lfs_acceptance.py` compares pointers and clean/smudge with official Git LFS, checks corruption refusal, multi-request filter framing and native add/status/switch with an empty client PATH. It also checks local install/tracking and command-scoped storage overrides. `tests/lfs_transfer_acceptance.py` runs an authenticated HTTPS fixture with a generated CA, verifies official-tool interoperability, upload deduplication, response validation and failure before Git ref publication. Fixtures use temporary profiles and generated secrets; real user credentials are not read.

Recognized current standard pre-push/post-commit/post-checkout/post-merge hooks are handled by the native path only when their behavior is supported. Lockable-file permission management and lock verification refuse before writes rather than being silently skipped. Customized or unrecognized hook bodies still refuse.

Remaining acceptance includes SSH authentication discovery/pure SSH LFS, locking, extension transformations, complete include/exclude/config/command semantics, resumable transfers/retries, redirects and full URL-specific HTTP configuration. Native LFS transfer performance and large-object memory/capacity behavior still need representative measurements. The workstation's original Git distribution is still required until the broader takeover gates pass.
## Native POSIX primary LFS inputs

The native product route now prepares official Git LFS 3.7.1 for Linux x64/ARM64 and macOS ARM64 using pinned asset size/SHA-256 values in `packaging/posix-helpers.lock.json`. The Windows producer runtime already supplies this primary helper generation. Staging copies the producer's helper and notices without calling `git lfs install`, executing the helper or changing a repository/global configuration. The [POSIX checkpoint](../native-core/receipts/implementation-posix-core-20261020.json) records byte/machine identity; transfer, locking/filter, failure and preserved configuration acceptance wait for D08. Historical Rust subset results below are not final native-product acceptance.
