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

Game-project tools include `native-game-multirepo-acceptance` (independent
repositories and a nested Unity/Unreal monorepository),
`native-game-lfs-stream-acceptance` (disk-streamed assets and failed-transfer
recovery), `benchmark-game-resources` (raw Git status/packing process trees) and
`benchmark-game-lfs-resources` (the complete two-project fixture/Git/LFS process
tree). Use each command's `--help` for bounded fixture sizes and new output paths.
Reports distinguish Windows job committed-memory peaks, Linux samples and
unavailable metrics; process-budget failures and timeouts are failed samples.
`runProcess` returns an identifiable context error on deadline rather than
misclassifying a killed process as an ordinary command exit.

## Acceptance layers

For the already published v1.0.3 delivery, the user's 2026-10-25 instruction accepts
Docker validation, cross-compilation and package/layout/checksum evidence without
physical ARM or complete host lifecycle prerequisites. The existing accepted
receipts satisfy that scope; no runtime bytes, staging checks or old reports are
changed, and no new host/terminal run is required to close this delivery.
[Scope record](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-release-scope-20261025.json).

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

An explicit user instruction to stop terminal testing can be recorded with
`--terminal-waiver PATH` on `native-product-batch` and `prepare-native-release`.
The schema-1 `terminal-test-waiver` JSON must name `authorized_by: user`, quote
the instruction in `authorization` and `reason`, bind the exact matrix SHA-256,
and list only `terminal-rust-contracts` and `terminal-native-desktop` in `cases`.
Its `terminal_binaries` map must identify both `windows-x64` and `windows-arm64`:
each has the exact `source_commit` and a `files` map containing the SHA-256 of
`bin/git-terminal.exe` and `bin/git-terminal-shell.exe`. Matrix, manifest and
actual binary bytes must agree.

With that exact authorization, correctness keeps all 18 rows: 16 passed cases and
two `waived` cases, with overall `pass-with-user-waiver`. The terminal Rust,
desktop and startup tests are not executed. Earlier failed reports remain intact.
Performance requires this same exact correctness report and waiver; all five
performance rows must pass. MSI, native CLI, actual Mac/Linux, package and hash
gates are unchanged. Release proofs and package acceptance metadata retain the
waiver and its hash, and `native-terminal-waiver.json` is included among the
checksummed release assets. A waiver for any other case is rejected.

The native release also requires `native-mac-runtime-acceptance` on Apple Silicon.
Pass its exact-archive receipt to `freeze-native-batch --mac-runtime-receipt`.
The 51 actual cases cover common CLI, core contracts, LFS/signing and read-thread
budgets; Darwin CPU affinity constraints remain explicitly unsupported. The Linux
static inspection still checks the same Mac archive independently. Release staging
rejects absent, incomplete or changed Mac runtime evidence.

## Explicit package-validation release scope

The user's instruction for the current game-project work accepts Docker,
cross-compilation and package verification without complete local host lifecycle
validation: “不需要 arm，docker 验证打包就可，能发布就行。不需要本机完全验证真实环境”.
The additive `--package-validation-policy PATH` option records this scope on
`native-product-batch` and `prepare-native-release`. It never infers authorization
from a missing machine or a failed test.

The schema-1 `package-validation-policy` document names `authorized_by: user`,
the explicit instruction and reason, the exact matrix/source identities, and only
`msi-user` and `msi-machine` in `cases`. It binds all five platform packages,
the four MSI packages and their receipts/manifests by name, size and SHA-256.
`native-package-validation-probes` must actually verify compiled MSI payloads,
run isolated migration-contract fixtures and all six common resource-behavior
probes. The policy pins that successful report and its relevant source identities.
Changed inputs, incomplete probes and any additional waived case are rejected.

All original correctness rows remain present. The two MSI transaction rows are
`waived`, with `installation_runtime_accepted: false`; package checks do not become
installation, upgrade, rollback or uninstall runtime passes. With the separate
existing two-case terminal waiver, the result is 14 passed and four explicitly
waived rows. Other native CLI/transport/read-safety, Windows bootstrap/update,
Docker, updater race and input-readback lanes still run. The five performance
rows still require real passes. Available Mac CLI acceptance stays independent
and requires no GUI or host installation. Without this policy the original
18-case requirements remain unchanged.

Release staging validates the same policy and probes again, records the scope in
proof/update/platform metadata, and includes
`native-package-validation-policy.json` and
`native-package-validation-probes.json` as checksummed assets. These documents
record the frozen candidate inputs. Staging can finalize manuals and
acceptance metadata and rebuild archive/MSI containers; final checksums and static
payload checks identify the published artifacts and their unchanged accepted
core/runtime bytes. Candidate package hashes are not substituted for final asset
hashes. The policy still makes no installer-runtime claim. This adds release
metadata and CLI options only; it changes no Git/LFS protocol, storage path,
installer operation or existing receipt. There is no persistent-state migration.
Rollback uses the previous controller and its original full validation path;
older controllers reject the new option rather than silently accepting the scope.

## Migration evidence and rollback

The initial remaining inventory was 114 files. Upstream added the freestanding
bridge verifier during this work, bringing that batch to 115. Earlier migrations
are recorded separately and are not added to this batch's verified count.
Thirty retired CLI suites are preserved at Git commit
`3609553`; removing them does not claim every old assertion moved into Rust.

The [public retirement inventory and evidence](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.json)
records all 115 old-file hashes, replacement modules, same-input comparisons and
the exact acceptance boundaries. The
[readable evidence report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.md)
also preserves rejected candidates, the R5b missing-Go-PATH failure and the user's
two terminal-case waivers. `waived` remains distinct from `pass`; neither a
partial installer report nor a successful compilation substitutes for a complete
artifact-bound product gate.

Release staging replaces mutable manuals, manifests and integration scripts with
independent files before writing them. Unchanged runtime files may share
hardlinks with the accepted inputs. The stage checks every frozen input before
and after packaging, and checks again on an error; a changed input rejects the
stage. Failed or interrupted directories are diagnostic artifacts, never release
assets.

The final R7 local checkpoint completed correctness with 16 passed / 2 waived
lanes in 249.801 seconds, and all five paired-performance lanes in
668.849 seconds. R6's complete local gates remain in the history;
its later stage hardlink source mutation was rejected, and 25 ARM64 metadata/docs
files were restored from frozen MSI bytes with no runtime binary drift. R7 uses
a new matrix and fresh measurements after the release-only write-isolation fix.
The report's false release/installation/publication flags describe actions still
pending at that source checkpoint; they do not negate completed local gates.
Actual subsequent staging, workstation acceptance and publication are recorded
in a separate public release-completion asset and a normal later documentation
commit, retaining the accepted package's original checkpoint.

The consolidated 100,000-file paired benchmark uses a 120-second per-command
budget; its whole-case limit remains 1,800 seconds. Smaller local scales keep
their 30-second command budget. Context deadlines are reported as deadlines,
separately from an ordinary child exit 1. Sample counts, complete-output checks,
exit checks and fsck are unchanged. The first rejected large-scale attempt is
retained; increasing budget headroom does not establish why that attempt failed.
MSI evidence reuse across this harness repair requires that removing exactly the
added context-error check restores the previous measured-process source hash;
installer scripts, recipe, process bounds and package bytes remain pinned.

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
