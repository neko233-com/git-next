# Local build and acceptance

```sh
cargo run --release --locked --manifest-path accept-runner/Cargo.toml --bin git-next-accept -- target/release/git --tier full --json target/accept-report.json
```

Build the client once with `cargo build --release --locked`. The command above is
identical on Windows/macOS; Windows resolves `git` to `git.exe`. L0 is required
before merge/tag; release branches require full. Network acceptance is selected
explicitly with `--tier network`. See [Rust CLI acceptance](GIT_NEXT_ACCEPTANCE.md)
for coverage, dependencies, skips and remaining gaps. No Python is used by this gate.

The Python entrypoints are retired. Historical CLI sources remain in Git history
at commit `3609553`; they are not an executable gate. Build, package, documentation,
benchmark, fixture and release tools use the independent Go module in
`tools/devtool`. See [Go tooling](GO_TOOLING.md) for commands and migration evidence.

## Separate native package build tooling

Rust 1.96+; stable rustfmt/clippy. No GitHub Actions or remote CI release gate.

The final primary core uses the [native source integration](NATIVE_CORE.md). Prepare pinned upstream sources, apply `go -C tools/devtool run . integrate-native-core`, then compile with `go -C tools/devtool run . build-native-core`. The Windows adapter requires the complete dependency prefix and `--iconv` imports from `go -C tools/devtool run . prepare-windows-iconv`. It builds production targets only and records source/artifact hashes; it never runs a product command or CTest. All five native cores and their runtime implementation are compiled/staged. Rust CLI conformance follows the gate above; native package acceptance remains independent. Final packages include the [corresponding-source manifest](CORRESPONDING_SOURCES.md); acceptance uses the frozen complete-package matrix.

For this delivery, follow the [fixed implementation and progress ledger](DELIVERY_STATUS.md). Do not execute the individual acceptance/performance commands below during D01–D07; they document available harnesses for the later consolidated batch. Necessary compiler/static syntax checks remain allowed. Native product batches retain their own fixed implementation ledger and frozen-package gates.

## Native POSIX build inputs

`packaging/native-dependencies.lock.json` pins the source bytes for zlib, Expat, PCRE2 with JIT, nghttp2 and curl. `go -C tools/devtool run . build-native-dependencies` accepts all five target IDs and builds only dependency/install targets in new work/prefix directories. Windows uses Schannel plus the separately pinned libpsl imports; POSIX uses recorded GnuTLS/libpsl prerequisites, with Apple SecTrust enabled for macOS. Copied prerequisite hashes and producer versions are build inputs; complete corresponding-source/notice delivery remains a D07 release gate.

`go -C tools/devtool run . collect-posix-prerequisites` records native GnuTLS/libpsl headers, libraries, package metadata and available notices without loading those libraries. `go -C tools/devtool run . build-native-posix` requires the target/dependency receipts, compiles ten production targets plus script generators, stages files into a new prefix, verifies ELF/Mach-O architecture and generated identity, and never invokes the product or CTest. Perl module paths are generated relative to the owned helper layout; the CMake install manifest uses the actual generated executable names. POSIX version strings use `2.54.0.git-next.<version>`; Windows retains its `.windows.1` compatibility marker.

`go -C tools/devtool run . build-native-linux` runs that compiler in an existing digest-pinned Debian Docker image. It copies only declared source/tools/archives, restores source permissions from the pinned original ZIP, isolates HOME/USERPROFILE/SSH paths, exports build artifacts and removes only its label-verified container/volume. No personal profile, repository credentials or deployment configuration is mounted. A failed staging build is not acceptance; failure dependency exports are diagnostics, not a releasable package.

The [POSIX compilation checkpoint](../native-core/receipts/implementation-posix-core-20261020.json) records the completed Linux x64 core build and three-platform primary helper inputs. The core archive is not a complete final product: owned SSH/shell/signing runtime closure, updater, full package/source-license inputs and the consolidated acceptance remain open. macOS native compilation still needs the requested Mac build environment; staged arm64 helpers are not native macOS acceptance.

`packaging/posix-helpers.lock.json` pins Git LFS 3.7.1 and GCM 2.7.3 for Linux x64/ARM64 and macOS ARM64 from official release asset digests. `go -C tools/devtool run . fetch-posix-helpers` verifies size/hash, stages those primary helpers and notices, records logical owned symlinks, and checks native machine type without executing or registering a helper. These producer assets do not supply an upstream Git core.

The separate native product validation uses `go -C tools/devtool run . native-product-batch`: pin the final build receipt/reference Git, collect correctness/auth/LF/language/install failures together, repair them, then run its separate sequential performance phase. It must not be started while implementation, builds or other timing workloads are still running. `--performance` batches 1000/10000/100000-file warm read measurements and loopback network measurements after correctness passes. See [MSI/update contracts](UPDATES.md) and [language contracts](LANGUAGE.md). This runner is being integrated; its subset result alone cannot authorize the consolidated full-product release.

For actual Windows MSI lifecycle checks, pass retained older native fixture bytes with `--msi-base` and the final new package with `--msi`. The harness refuses pre-existing MSI/task/state identities rather than modifying an unrelated/current installation. MSI tests run after the concurrent disposable repository fixtures; timing starts after all correctness and installation activity finishes.

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --offline --locked --lib
cargo fmt --manifest-path accept-runner/Cargo.toml --check
cargo clippy --locked --manifest-path accept-runner/Cargo.toml --all-targets -- -D warnings
cargo test --locked --manifest-path accept-runner/Cargo.toml --bin git-next-accept
```

Release is opt-level 3 with 16 codegen units, incremental, and LTO off. Dev and test profiles use opt-level 0, line tables only, and incremental compilation. Thin LTO and `codegen-units = 1` are not used.

Everyday CLI regression reuses the existing client:

```sh
cargo run --release --locked --manifest-path accept-runner/Cargo.toml --bin git-next-accept -- target/release/git --tier l0
```

Reference Git creates disposable fixtures and supplies independent comparisons/fsck.
The client runs with empty PATH/GIT_EXEC_PATH and isolated profile/SSH paths.
Native Windows platform API, package installation, GUI and credential-provider
checks belong to the separate native product workflow, not this CLI gate.

## Targets

Windows x64: native MSVC. Windows ARM64: aarch64-pc-windows-gnullvm with Zig's LLVM linker (not an MSVC artifact). Linux x64: x86_64-unknown-linux-musl. Linux ARM64: aarch64-unknown-linux-gnu with glibc 2.28 baseline. macOS: aarch64-apple-darwin with Apple SDK, on a build machine only; no installation or PATH change is required.

Examples for installed rust targets and cargo-zigbuild:

```sh
cargo zigbuild --release --locked --target x86_64-unknown-linux-musl --jobs 1
cargo zigbuild --release --locked --target aarch64-unknown-linux-gnu.2.28 --jobs 1
cargo zigbuild --release --locked --target aarch64-pc-windows-gnullvm --jobs 1
```

Cross-compilation proves packaging feasibility, not runtime behavior. Linux/Windows ARM64 need native runtime acceptance before claiming operational support. macOS ARM64 acceptance is recorded separately after native build and isolated tests. Do not publish complete-replacement releases while compatibility.json remains false.

An eight-job Windows ARM64 local build observed Zig's `file_open Unexpected` while checking a MinGW header/cache input. The header remained readable and unchanged; a single-job retry built and verified all five binaries. The command above records that conservative local build setting. This does not establish the initial failure's root cause or Windows ARM64 runtime acceptance.

A later eight-job Linux x64 musl link observed the same diagnostic on `cimag.c`, which remained readable and unchanged. That failed attempt establishes no new cross-platform acceptance. The musl command also selects one Cargo job for the next local validation; subsequent successful target/runtime results must be recorded separately. Neither failure proves its root cause.

The status-v2 checkpoint also observed a two-job musl Zig cache read failure on `arm.zig`. The input remained readable and unchanged; the single-job retry built all five binaries and retained the exact runtime-tested client. Both Linux targets in that checkpoint use one build job and pass 15 local suites. No SDK/cache deletion or security-setting change was needed; the initial diagnostic's cause is not established.

## Historical conformance and separate product checks

The former Python CLI/container runners are retired. Use a target-platform build
of `git-next-accept`; cross-compilation or emulation is not native OS acceptance.
[Historical acceptance](ACCEPTANCE.md) retains original receipts, while the
[current Rust coverage](GIT_NEXT_ACCEPTANCE.md) states the migrated scope and gaps.
Retiring Python does not transfer every historical assertion into the Rust gate.

Native package/container/MSI, generated-account migration, GUI and provider
workflows retain their own frozen-input checks. They cannot be satisfied by an
L0/full report. HTTPS/SSH/LFS transfer is not implied by the loopback git-daemon
network tier. Do not treat a skipped or unavailable transport as passing.

`go -C tools/devtool run . package-rust --platform windows-x64` is an artifact packaging
command, not a CLI conformance gate. It reuses release binaries and rejects MinGW,
Bash, Perl, Python and Git for Windows payloads. Documentation generation and
packaging still use Python; ordinary CLI acceptance needs no Python environment.
## Complete Windows native payload

`go -C tools/devtool run . build-native-core` requires both the pinned `--iconv` and `--psl` import prefixes. The latter restores libpsl cookie public-suffix checks in the native Schannel curl dependency. `go -C tools/devtool run . assemble-native-windows` requires that same `--psl` prefix plus the official Microsoft VC redistributable directory. It verifies current source/build/runtime hashes, excludes the upstream Git engine and optional OpenSSL provider DLL, and writes a new distribution manifest without running any product input. The independent provider pin/header generator and component installer preserve the producer's config/module directory structure. Full-payload MSI compilation uses `go -C tools/devtool run . build-msi --distribution <root>`; this route performs no product version probe. Its unaccepted local candidates remain blocked from takeover and release.

The assembler recursively inspects the PE imports of all source-built entrypoints and their side-by-side native DLLs before emitting a distribution manifest. Missing non-system DLLs or mismatched machine types reject assembly. This is static package validation, not runtime conformance. Nghttp2 is configured with `BUILD_SHARED_LIBS=OFF`, `BUILD_STATIC_LIBS=ON` and curl `NGHTTP2_USE_STATIC_LIBS=ON`; the older similarly named `ENABLE_SHARED_LIB`/`ENABLE_STATIC_LIB` options are not used by the pinned nghttp2 version. Source integration writes generated/overlay files only when their bytes differ, preserving incremental compilation.

Native MSI packaging accepts `--scope machine` (default) or `--scope user`. User UpgradeCodes/ProductCode derivation remain compatible; machine identities are additive. Both x64 flavors compile without installation. `Scope.Native.cs` supplies atomic protected Windows file/directory creation and is compiled for syntax only during implementation. Builtin executable aliases currently contain owned core copies; compressed/installed capacity and alias latency belong to D08/D09. Do not rebuild every MSI after each feature: accumulate remaining implementation, then generate the final D07 packages from exact current inputs.

## Windows legacy entrypoints

The assembler now relocates non-Git system configuration defaults to `runtime/defaults/<original-path>` and emits `runtime/defaults/manifest.json`. It does not initialize active configuration by running product/installer code during compilation. Native MSI includes `Runtime.Configuration.ps1` and initialization/rollback/commit actions. A distribution manifest explicitly records `initialize_runtime_configuration_before_execution=true`; portable installation must honor that contract too. Accumulated changes belong to the final D07 package rebuild, not per-feature MSI trials.

`go -C tools/devtool run . build-windows-bridge` compiles separate console/GUI Windows entrypoints and version resources with the matching MSVC/SDK or, for ARM64, the pinned LLVM-MinGW SDK. They import only `kernel32` and `advapi32`, plus `user32` for the GUI entry, and use no redistributable CRT. MSVC 14.44 at full `/O2` still emits `memcpy`, `memset`, `memmove` and `memcmp`; those symbols are `native-core/launcher/freestanding-memory.c`. The build keeps `/O2` (and the default `/Ob2` inlining) and refuses the `/Ob1` workaround, because that flag hides the helper call by disabling inlining of the forwarder. The helper bodies are compiled with optimization off so the compiler cannot rewrite them into a call to themselves. `go -C tools/devtool run . verify-freestanding-bridge` proves that property on a host compiler and, when MinGW is installed, links the x64 bridge and checks its import table. That host proof is not an MSVC 14.44 packaging run.

`go -C tools/devtool run . assemble-native-windows --bridge <build-root>` verifies source, freestanding-memory source, resource, version, machine, hash, `/O2` and the import table. A receipt written before `freestanding-memory.c` is rejected, so the bridge has to be rebuilt before the next package. `go -C tools/devtool run . build-msi --distribution` requires that complete integration payload. Compiler/resource output, PE inspection and source syntax checks do not count as product regression.

## Owned POSIX runtime assembly

`go -C tools/devtool run . prepare-linux-runtime` collects declared Debian producer runtime files in an isolated, label-scoped Docker container; `go -C tools/devtool run . collect-linux-runtime` records SSH/shell/signing/interpreter/library ownership and notices without executing product commands. `tools/devtool/posix_images.go` inspects imports without loading them. GCM single-file hosts retain their bundle offsets; their owned launcher scopes the library path to the helper. Later source-only notice/ownership fixes must be consumed by the final D07 rebuild.

`go -C tools/devtool run . prepare-git-shared-assets` combines pinned portable GUI/HTML data and official 2.54.0 manuals, excluding native producer executables. HTTPS checksum provenance is recorded separately from publisher-signature verification. `go -C tools/devtool run . assemble-native-posix` requires source-matched core/runtime receipts, these shared assets and a real compiled owned updater; it has no placeholder-updater bypass. Assembly uses static image/hash/relocation checks and rejects uninspected native images. It has not yet run.

The dependency recipe now selects the actual curl options `USE_NGHTTP2`, Windows `USE_WIN32_IDN`, POSIX `USE_LIBIDN2` and `CURL_USE_GSSAPI`. macOS additionally uses Apple GSS and SecTrust. The collector records IDN/Kerberos prerequisites; generated feature macros must match before packaging. These recipe changes are source-only and do not retroactively change earlier dependency artifacts.

## POSIX updater compiler

`go -C tools/devtool run . build-posix-updater --destination <new-directory>` selects the installed Go toolchain satisfying the isolated updater module (Go 1.27.2), builds all three POSIX targets with CGO disabled, inspects ELF/Mach-O headers and writes a source/binary receipt. It never invokes produced executables or services. The full native Windows builder now accepts `--platform windows-x64|windows-arm64` and `--prepared-source` for frozen shared integration inputs; the latter avoids mutating a source tree while target compilers are live. Missing ARM64 MSVC platform tools are build-input failures, not product regressions.

The requested Mac validation uses `tests/containers/Dockerfile_mac` only after all implementation completes. Its Linux container checks package hashes, ARM64 Mach-O headers, owned links/modes and shell syntax without loading Mach-O or mounting host profiles. It provides no macOS kernel, Keychain, GUI or native runtime evidence. Mac host use is limited to build/material preparation. [Docker documents the Linux VM boundary](https://docs.docker.com/desktop/features/vmm/).

## Five-target native packaging

`go -C tools/devtool run . prepare-native-release` stages a consolidated release only after passed correctness and paired performance receipts identify the same frozen five-target matrix. It rechecks every input, requires the immutable project HEAD, finalizes release metadata and canonical POSIX archive roots, compiles both Windows MSI scopes, includes corresponding source bundles, and writes the shared update manifest and checksums. It does not publish. Product runtime bytes remain those accepted by the local gates; release metadata changes are identified in the accompanying acceptance record.

For an identity/resource metadata change, `go -C tools/devtool run . build-native-linux --reuse-core-build ARCHIVE --core-producer RECEIPT` can reuse the owned compiler object cache. It verifies the archive, frozen producer sources, declared resource patch, dependency/compiler recipes and target, rejects functional source changes, refreshes the identity header dependency and relinks all final executables. A fresh target receipt and subsequent package acceptance remain required.

`packaging/product.json` owns the native candidate version independently of the historical Rust preview. All five native cores now compile from the same integrated source. Windows ARM64 can use the pinned isolated LLVM-MinGW SDK via `go -C tools/devtool run . prepare-llvm-mingw` and `packaging/toolchains/windows-arm64-llvm.cmake`; no Visual Studio process is stopped. The bridge compiler supports the same SDK. MSVC builds require licensed redistributable CRT inputs; LLVM ARM64 still verifies every native core import, without adding an unused MSVC runtime.

`go -C tools/devtool run . collect-macos-runtime` collects declared Apple/Homebrew primary runtime files and interpreter standard libraries, thins ARM64 images, records source hashes/notices and resolves non-system Mach-O imports. It does not collect HOME modules, keys or Keychain data. The resulting package declares macOS 15.5+ on Apple Silicon; Apple system frameworks and OS services remain platform requirements. Native credential helpers preserve explicit configuration, including an empty helper reset.

`go -C tools/devtool run . package-native-linux` consumes matching immutable core/runtime receipts and assembles ELF runtime paths in a disposable labelled Docker volume. The Go archive reader needs no producer-side interpreter. All writes are confined and bounded. `packaging/posix/install.sh` is the complete package's local installation entrypoint, using its owned updater and verified package manifest. Product execution still waits for D01-D07 closure.

Final archive staging preserves canonical package roots for long PAX paths and hard links. Windows takeover resolves the exact numbered Inno uninstaller from the matching registry registration and verifies its hash again before execution. `tests/takeover_registration_acceptance.ps1` covers both ordinary numbered layouts and rejects extra arguments or paths outside the selected installation.

## Verified incremental native builds

Windows assembly also requires `go -C tools/devtool run . build-windows-msi-worker --llvm-root <pinned-sdk> --platform windows-x64|windows-arm64 --version <version> --destination <new-directory>` and passes that directory as `--msi-worker`. The freestanding worker imports only Kernel32 and is packaged with a source/binary receipt. Local integration acceptance exercises no-console execution, Unicode paths, exit-code propagation, scheduled request routing and invalid-argument rejection before MSI lifecycle acceptance.

Linux compiler object caches are accepted only with a cleaned producer receipt, matching libraries/tool recipes and source deltas matched to declared patches/overlays. Every final executable is relinked. Frozen source is transferred into the disposable build volume as one checksummed tar to avoid QEMU small-file reads on Windows bind mounts; source identity and original permission metadata remain verified. Build containers and volumes are removed by exact ownership labels.
