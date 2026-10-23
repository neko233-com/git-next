# Rust acceptance and Go tooling

Rust owns client behavior and CLI conformance. Go owns build/package/docs/release
orchestration, benchmarks, fixture services, desktop automation and the Perforce
helper. Native C, PowerShell and shell remain where their platform APIs require
them. The product and these tools do not need a Python interpreter, venv or pip.

## Entry points

Use Go 1.27.2, matching the pinned independent modules. From the repository root:

```sh
go -C tools/devtool run . help
go -C tools/devtool run . render-docs
go -C tools/devtool run . publish-docs --dry-run
go -C tools/devtool run . check-locales --coverage
go -C tools/devtool run . verify-repo-contract
go -C tools/devtool run . migration-audit --require-complete
go -C tools/devtool test ./...
go -C tools/devtool vet ./...
```

For repeated work build once with `go -C tools/devtool build -o
../../target/devtool/git-next-tool .` (add `.exe` on Windows). `GIT_NEXT_ROOT` or
the current checkout selects the repository. Command paths resolve from that
root even when Go uses `-C`. Unknown options fail; arguments retain their exact
boundaries. Child processes have bounded lifetimes and owned process cleanup.

The former underscore-separated filename maps to a hyphenated subcommand, for
example `prepare-windows-runtime`, `native-product-batch`, `msi-lifecycle` and
`benchmark-clone`. Existing options, layouts, MSI identities and manifest fields
remain compatibility contracts. `help` lists every supported command.
`build-git-p4` builds the independent Go Perforce module for the five targets.

## Acceptance layers

```sh
cargo build --release --locked
cargo run --release --locked --manifest-path accept-runner/Cargo.toml --bin git-next-accept -- target/release/git --tier full --json target/accept-report-full.json
```

The same commands run on Windows and macOS. L0 covers 39 commands and is mandatory
before merge/tag; full covers 100 local scenarios and is mandatory on release
branches. `--tier network` is explicit, with skips recorded. The client runs with
an empty PATH; reference Git constructs fixtures. See
[the exact Rust coverage and gaps](GIT_NEXT_ACCEPTANCE.md).

Native full-package CLI, GCM, HTTP/SSH/LFS, desktop and installer lifecycle checks
use their Go/platform gates. They still require frozen package and harness
identities. A source or runtime change reopens the affected gate. Compilation,
cross-compilation and static package inspection are distinct from actual platform
execution. Existing receipts keep their original hashes and scope.

`native-msi-acceptance --native-packages MATRIX --reference REF --output NEWDIR`
runs the actual user and elevated machine installer lifecycle checks serially,
including injected-failure rollback. It rehashes the frozen inputs and reference
before and after execution, and writes a correctness `batch.json` with
`scope: msi-lifecycle-only`: two installer cases plus frozen-input readback.
The consolidated `native-product-batch` can reuse exactly those two passed cases
with `--reuse-msi-batch NEWDIR/batch.json --reuse-msi-matrix MATRIX` after checking
the package, source, reference and installer harness identities. This partial
report cannot satisfy `prepare-native-release`; all 18 consolidated correctness
cases and the separate performance gate remain required.

The native release also requires `native-mac-runtime-acceptance` on Apple Silicon.
Pass its exact-archive receipt to `freeze-native-batch --mac-runtime-receipt`.
The 51 actual cases cover common CLI, core contracts, LFS/signing and read-thread
budgets; Darwin CPU affinity constraints remain explicitly unsupported. The Linux
static inspection still checks the same Mac archive independently. Release staging
rejects absent, incomplete or changed Mac runtime evidence.

## Migration evidence and rollback

The initial remaining inventory was 114 files. Upstream added the freestanding
bridge verifier during this work, bringing that batch to 115. Earlier migrations
are recorded separately and are not added to this batch's verified count.
Thirty retired CLI suites are preserved at Git commit
`3609553`; removing them does not claim every old assertion moved into Rust.

Equivalent checks use the same pinned inputs: native source integration, Windows
runtime manifests and ABI imports, TLS pins, shared assets, corresponding source
archive members, helper/updater/bridge/worker builds, native CLI and credential/
transport fixtures. Tests also reject archive escapes, foreign PE runtime imports,
incomplete or altered desktop evidence and unsafe MSI upgrade-family reuse.
The corresponding-source archives may contain upstream Python source as opaque
license materials; they are not installed or executed as product tools.

No installed Git, repository object/ref/index layout, LFS bytes, credential stores,
storage paths or database schemas move for this migration. Defaults continue to
come from `packaging/runtime-defaults/gitconfig`. Rollback is the previous source
revision or accepted product binary; user data needs no migration. Generated
fixture secrets stay in disposable profiles and are never written to receipts.

No GitHub Actions are used. The one consolidated native release requires the full
local five-platform build/package/checksum and applicable runtime/lifecycle gates.
Publication and a workstation installation are separate actions.
