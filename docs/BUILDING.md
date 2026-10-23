# Local build and acceptance

Rust 1.96+; stable rustfmt/clippy. No GitHub Actions or remote CI release gate.

The final primary core uses the [native source integration](NATIVE_CORE.md). Prepare pinned upstream sources, apply `tools/integrate_native_core.py`, then compile with `tools/build_native_core.py`. The Windows adapter requires the complete dependency prefix and `--iconv` imports from `tools/prepare_windows_iconv.py`. It builds production targets only and records source/artifact hashes; it never runs a product command or CTest. All five native cores and their runtime implementation are compiled/staged. Historical Rust commands below remain preview tooling. Final packages include the [corresponding-source manifest](CORRESPONDING_SOURCES.md); acceptance uses the frozen complete-package matrix.

For this delivery, follow the [fixed implementation and progress ledger](DELIVERY_STATUS.md). Do not execute the individual acceptance/performance commands below during D01–D07; they document available harnesses for the later consolidated batch. Necessary compiler/static syntax checks remain allowed. `tools/product_batch.py` checks the implementation ledger before resolving binaries, creating result directories or launching any checks.

## Native POSIX build inputs

`packaging/native-dependencies.lock.json` pins the source bytes for zlib, Expat, PCRE2 with JIT, nghttp2 and curl. `tools/build_native_dependencies.py` accepts all five target IDs and builds only dependency/install targets in new work/prefix directories. Windows uses Schannel plus the separately pinned libpsl imports; POSIX uses recorded GnuTLS/libpsl prerequisites, with Apple SecTrust enabled for macOS. Copied prerequisite hashes and producer versions are build inputs; complete corresponding-source/notice delivery remains a D07 release gate.

`tools/collect_posix_prerequisites.py` records native GnuTLS/libpsl headers, libraries, package metadata and available notices without loading those libraries. `tools/build_native_posix.py` requires the target/dependency receipts, compiles ten production targets plus script generators, stages files into a new prefix, verifies ELF/Mach-O architecture and generated identity, and never invokes the product or CTest. Perl module paths are generated relative to the owned helper layout; the CMake install manifest uses the actual generated executable names. POSIX version strings use `2.54.0.git-next.<version>`; Windows retains its `.windows.1` compatibility marker.

`tools/build_native_linux.py` runs that compiler in an existing digest-pinned Debian Docker image. It copies only declared source/tools/archives, restores source permissions from the pinned original ZIP, isolates HOME/USERPROFILE/SSH paths, exports build artifacts and removes only its label-verified container/volume. No personal profile, repository credentials or deployment configuration is mounted. A failed staging build is not acceptance; failure dependency exports are diagnostics, not a releasable package.

The [POSIX compilation checkpoint](../native-core/receipts/implementation-posix-core-20261020.json) records the completed Linux x64 core build and three-platform primary helper inputs. The core archive is not a complete final product: owned SSH/shell/signing runtime closure, updater, full package/source-license inputs and the consolidated acceptance remain open. macOS native compilation still needs the requested Mac build environment; staged arm64 helpers are not native macOS acceptance.

`packaging/posix-helpers.lock.json` pins Git LFS 3.7.1 and GCM 2.7.3 for Linux x64/ARM64 and macOS ARM64 from official release asset digests. `tools/fetch_posix_helpers.py` verifies size/hash, stages those primary helpers and notices, records logical owned symlinks, and checks native machine type without executing or registering a helper. These producer assets do not supply an upstream Git core.

The requested final validation uses the single `tools/product_batch.py` entry: pin the final build receipt/reference Git, collect correctness/auth/LF/language/install failures together, repair them, then run its separate sequential performance phase. It must not be started while implementation, builds or other timing workloads are still running. `--performance` batches 1000/10000/100000-file warm read measurements and loopback network measurements after correctness passes. See [MSI/update contracts](UPDATES.md) and [language contracts](LANGUAGE.md). This runner is being integrated; its subset result alone cannot authorize the consolidated full-product release.

For actual Windows MSI lifecycle checks, pass retained older native fixture bytes with `--msi-base` and the final new package with `--msi`. The harness refuses pre-existing MSI/task/state identities rather than modifying an unrelated/current installation. MSI tests run after the concurrent disposable repository fixtures; timing starts after all correctness and installation activity finishes.

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked
cargo build --release --locked
## Windows metadata-cache unit coverage, in addition to the app tests:
cargo test --manifest-path vendor/gix-status/Cargo.toml --target-dir target/vendor-status-tests --lib --features sha1,sha256,worktree-rewrites fscache
python tests/acceptance.py target/release/git-next --https
python tests/worktree_acceptance.py target/release/git-next
python tests/push_acceptance.py target/release/git-next
python tests/config_acceptance.py target/release/git-next
python tests/status_cache_acceptance.py target/release/git-next
python tests/status_head_acceptance.py target/release/git-next
python tests/status_head_paths_acceptance.py target/release/git-next
python tests/status_format_acceptance.py target/release/git-next
python tests/status_v2_acceptance.py target/release/git-next
python tests/status_directory_acceptance.py target/release/git-next
python tests/add_directory_acceptance.py target/release/git-next
## Loopback TLS conformance smoke; no latency statistics are accepted:
python tests/network_benchmark.py target/release/git-next --runs 1 --files 16 --validation-only --output network-smoke.json
python tests/status_wide_acceptance.py target/release/git-next --files 1000 --directories 128 --runs 6
python tests/diff_acceptance.py target/release/git-next
python tests/restore_acceptance.py target/release/git-next
python tests/eol_acceptance.py target/release/git-next
python tests/merge_acceptance.py target/release/git-next --object-format sha1
python tests/merge_acceptance.py target/release/git-next --object-format sha256
python tests/merge_message_acceptance.py target/release/git-next
python tests/merge_pending_acceptance.py target/release/git-next
python tests/commit_acceptance.py target/release/git-next
python tests/repository_format_acceptance.py target/release/git-next
python tests/native_discovery_acceptance.py target/release/git-next.exe
python tests/windows_metadata_api_acceptance.py
# Unix only, including isolated Linux containers:
python tests/native_discovery_unix_acceptance.py target/release/git-next
python tests/lfs_acceptance.py target/release/git-next
python tests/lfs_transfer_acceptance.py target/release/git-next
python tests/ssh_acceptance.py target/release/git-next
python tests/terminal_acceptance.py target/release/git-terminal
python tests/benchmark.py target/release/git-next --runs 40 --files 1000
```

Windows binary suffix: .exe. Acceptance requires reference Git for disposable fixture generation and independent fsck only; the client is executed with an empty PATH, absent Git/OpenSSH commands and isolated HOME/USERPROFILE/config/known_hosts. SSH fixture also needs Python Paramiko. Reference Git is never bundled as runtime fallback.

The Windows metadata API fixture compares directory-only and prior MFT-query fields with live filesystem metadata, including a real junction, hardlink, Unicode/empty/ordinary files and directory. It records whether a real symlink could be created; missing symlink privilege is an explicit untested category. This API contract check is separate from optimized product conformance and performance.

The fixtures compare clone objects/refs/worktree, native init/add/commit, status, packed incremental local fetch and fast-forward pull, clean checkout/switch/fast-forward merge, CRLF attributes, dirty/staged/untracked conflicts, ref/index locks, mid-apply rollback, interrupted journals, unsupported operations, real public HTTPS shallow clone, loopback embedded SSH and native terminal behavior. Unknown and changed host keys must reject. On Windows also run `./tests/install_acceptance.ps1` to test product identity, separate preview installation, blocked takeover, checksum-aware uninstall and original-Git/PATH preservation in a disposable directory. This is a scoped suite, not full Git's conformance suite or a promise of ecosystem parity.

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

## Linux container LF/local conformance

With the corresponding optimized cross-build already present, the tracked fixture uses read-only binary/test mounts, no container network, a read-only root filesystem and disposable tmpfs working/profile storage. Reference Git/Git LFS are fixture dependencies inside the image; the native client PATH still excludes them. The pinned Python base is shared across architectures, and each report records the resulting image and binary identity.

```sh
docker build --platform linux/amd64 -t git-next-local-acceptance:20261020 tests/containers
python tests/containers/host.py
docker build --platform linux/arm64 -t git-next-local-acceptance-arm64:20261020 tests/containers
python tests/containers/host.py --arm64
```

The ARM64 path uses `target/aarch64-unknown-linux-gnu/release`; on this x64 workstation it is emulated conformance, not physical ARM64 or performance evidence. Image construction needs package network access; fixture execution has no network. These local suites deliberately do not establish complete merge semantics against a different reference-Git version, network/provider login, macOS, Windows ARM64 runtime or native-host latency. Preserve each exact report under `.evidence/` before promoting a sanitized checkpoint.

Push acceptance runs a disposable bare target, an authenticated TLS HTTP backend and an embedded loopback SSH target. Reference Git supplies only fixture servers and independent inspection. It verifies upstream/tracking refspec mapping, exact leases, annotated tags, dry-run, deletion policies, atomic rejection, non-atomic partial success and a simulated concurrent tracking update. A tiny Rust fixture helper requires rustc; credentials, private CA and SSH keys are generated inside the isolated temporary directory, never copied from the user. The HTTP fixture retains certificate verification and never calls helper store/erase. Rust dependency patches live in `vendor/` with upstream licenses and PATCHES.md explanations; they are required source inputs for reproducible builds.

LFS acceptance additionally requires official Git LFS as a fixture reference and Python cryptography for a generated TLS CA. The client uses an empty PATH and the native sibling process filter; no original Git/LFS runtime is bundled. See [LFS scope](LFS.md).

Config acceptance compares stdout and exit status directly with reference Git for isolated global/local configuration, includes/conditional includes, implicit and empty values, multivars, typed reads/writes, regex selection, NUL/origin/scope output, command overrides and repository context. It also checks comment preservation and refusal to overwrite an existing config writer lock. No real user profile or credentials are used.

The HEAD/index status fixture covers both object formats, sorted directory/file boundaries, missing/invalid/wrong-count cache trees, stale matching cache IDs over genuine staged content/mode/path changes, intent-to-add and unchanged index/HEAD bytes under optional-lock refusal. Canonical LF/CRLF-only saves use normalized object hashes as truth, since reference Git's size shortcut can report a false modification. Explicit rename settings retain the full comparison path, and invalid rename/algorithm configuration refuses before an index lock is acquired. This is scoped status conformance, not complete porcelain/quoting parity.

The separate protected-path fixture intentionally creates malformed temporary HEAD/tree/index/cache combinations for `.git` and symlink `.gitmodules` in SHA-1/SHA-256. The native command must refuse without changing any repository file. These are intentionally invalid fixture objects; ordinary fixture fsck and malformed-input refusal are distinct checks. They never use user repositories or credentials.

`tests/status_wide_acceptance.py` defaults to 10000 files in 1000 directories and 60 native status runs per object format. It independently verifies all worktree bytes, staged IDs, HEAD, optional-lock no-write behavior and fsck, using the known fixture contents as truth. It does not rely on reference status when that implementation transiently reports false deletions. The smaller command above is a smoke check; full stability and timing remain separate scopes. `python tests/reference_wide_stability.py` is the Windows reference-only diagnostic for the installed Git 2.54 path; it never launches git-next and does not change global settings.

[Merge acceptance](MERGE.md) compares raw commits, ordered parents, index stages, worktree, standard merge state, ORIG_HEAD/AUTO_MERGE and reflogs, including native continuation/abort, partial conflict resolution and refused writes. Message cleanup and SHA-1/SHA-256 commit objects have separate comparisons. Required repository extensions fail before unsupported storage is touched. The client PATH contains no reference Git; all mutations use temporary fixtures.

Windows filesystem-only system configuration discovery and managed standalone GCM have separate [discovery](NATIVE_DISCOVERY.md) and [credential support](CREDENTIALS.md) fixtures. They enable system configuration or use explicitly isolated DPAPI test credentials, respectively. These checks distinguish native callbacks from fixture/reference Git and do not establish real vault or native macOS/Linux acceptance. Package the optional support installer with `third_party/gcm/LICENSE`; preserve upstream notices when installing its verified archive.

Package git/git-next, git-terminal, git-lfs and git-next-lfs-filter binaries with README, LICENSE and compatibility.json. Calculate SHA256SUMS locally after the final build; verify unpacked bytes before upload. Configured signatures must also verify locally. Publish only a consolidated release with an exact accepted scope and no private fixture data. Source publication can precede a formal binary Release.

Linux x64 Docker and emulated Linux ARM64 run local LF/configuration/operation suites. [Acceptance](ACCEPTANCE.md) records the exact passing suite count and source/binary identity for each checkpoint. Native Mac/physical ARM64, complete Linux/network performance and provider acceptance remain separate requirements.

Long-status conformance adds `python tests/status_long_acceptance.py <binary>` and `python tests/status_operation_acceptance.py <binary>`. Both isolate SHA-1/SHA-256 repositories and require exact reference output plus unchanged metadata. Use `--include-long` with the filename/v2 fixtures to compare long paths, conflicts, tracking and ordinary submodule notes. Reference Git creates operation fixtures; the native client never launches Git/OpenSSH. The Docker runtime runner includes these tests; its private migration check additionally uses separate client/server images and cleans its owned resources, as described in [credential acceptance](CREDENTIALS.md).

## Complete Windows native payload

`tools/build_native_core.py` requires both the pinned `--iconv` and `--psl` import prefixes. The latter restores libpsl cookie public-suffix checks in the native Schannel curl dependency. `tools/assemble_native_windows.py` requires that same `--psl` prefix plus the official Microsoft VC redistributable directory. It verifies current source/build/runtime hashes, excludes the upstream Git engine and optional OpenSSL provider DLL, and writes a new distribution manifest without running any product input. The independent provider pin/header generator and component installer preserve the producer's config/module directory structure. Full-payload MSI compilation uses `packaging/build_msi.py --distribution <root>`; this route performs no product version probe. Its unaccepted local candidates remain blocked from takeover and release.

The assembler recursively inspects the PE imports of all source-built entrypoints and their side-by-side native DLLs before emitting a distribution manifest. Missing non-system DLLs or mismatched machine types reject assembly. This is static package validation, not runtime conformance. Nghttp2 is configured with `BUILD_SHARED_LIBS=OFF`, `BUILD_STATIC_LIBS=ON` and curl `NGHTTP2_USE_STATIC_LIBS=ON`; the older similarly named `ENABLE_SHARED_LIB`/`ENABLE_STATIC_LIB` options are not used by the pinned nghttp2 version. Source integration writes generated/overlay files only when their bytes differ, preserving incremental compilation.

Native MSI packaging accepts `--scope machine` (default) or `--scope user`. User UpgradeCodes/ProductCode derivation remain compatible; machine identities are additive. Both x64 flavors compile without installation. `Scope.Native.cs` supplies atomic protected Windows file/directory creation and is compiled for syntax only during implementation. Builtin executable aliases currently contain owned core copies; compressed/installed capacity and alias latency belong to D08/D09. Do not rebuild every MSI after each feature: accumulate remaining implementation, then generate the final D07 packages from exact current inputs.

## Windows legacy entrypoints

The assembler now relocates non-Git system configuration defaults to `runtime/defaults/<original-path>` and emits `runtime/defaults/manifest.json`. It does not initialize active configuration by running product/installer code during compilation. Native MSI includes `Runtime.Configuration.ps1` and initialization/rollback/commit actions. A distribution manifest explicitly records `initialize_runtime_configuration_before_execution=true`; portable installation must honor that contract too. Accumulated changes belong to the final D07 package rebuild, not per-feature MSI trials.

`tools/build_windows_bridge.py` compiles separate console/GUI Windows entrypoints and version resources with the matching MSVC/SDK or, for ARM64, the pinned LLVM-MinGW SDK. They import only `kernel32` and `advapi32`, plus `user32` for the GUI entry, and use no redistributable CRT. MSVC 14.44 at full `/O2` still emits `memcpy`, `memset`, `memmove` and `memcmp`; those symbols are `native-core/launcher/freestanding-memory.c`. The build keeps `/O2` (and the default `/Ob2` inlining) and refuses the `/Ob1` workaround, because that flag hides the helper call by disabling inlining of the forwarder. The helper bodies are compiled with optimization off so the compiler cannot rewrite them into a call to themselves. `python tools/verify_freestanding_bridge.py` proves that property on a host compiler and, when MinGW is installed, links the x64 bridge and checks its import table. That host proof is not an MSVC 14.44 packaging run.

`tools/assemble_native_windows.py --bridge <build-root>` verifies source, freestanding-memory source, resource, version, machine, hash, `/O2` and the import table. A receipt written before `freestanding-memory.c` is rejected, so the bridge has to be rebuilt before the next package. `packaging/build_msi.py --distribution` requires that complete integration payload. Compiler/resource output, PE inspection and source syntax checks do not count as product regression.

## Owned POSIX runtime assembly

`tools/prepare_linux_runtime.py` collects declared Debian producer runtime files in an isolated, label-scoped Docker container; `tools/collect_linux_runtime.py` records SSH/shell/signing/interpreter/library ownership and notices without executing product commands. `tools/posix_images.py` inspects imports without loading them. GCM single-file hosts retain their bundle offsets; their owned launcher scopes the library path to the helper. Later source-only notice/ownership fixes must be consumed by the final D07 rebuild.

`tools/prepare_git_shared_assets.py` combines pinned portable GUI/HTML data and official 2.54.0 manuals, excluding native producer executables. HTTPS checksum provenance is recorded separately from publisher-signature verification. `tools/assemble_native_posix.py` requires source-matched core/runtime receipts, these shared assets and a real compiled owned updater; it has no placeholder-updater bypass. Assembly uses static image/hash/relocation checks and rejects uninspected native images. It has not yet run.

The dependency recipe now selects the actual curl options `USE_NGHTTP2`, Windows `USE_WIN32_IDN`, POSIX `USE_LIBIDN2` and `CURL_USE_GSSAPI`. macOS additionally uses Apple GSS and SecTrust. The collector records IDN/Kerberos prerequisites; generated feature macros must match before packaging. These recipe changes are source-only and do not retroactively change earlier dependency artifacts.

## POSIX updater compiler

`tools/build_posix_updater.py --destination <new-directory>` selects the installed Go toolchain satisfying the isolated updater module (Go 1.27.2), builds all three POSIX targets with CGO disabled, inspects ELF/Mach-O headers and writes a source/binary receipt. It never invokes produced executables or services. The full native Windows builder now accepts `--platform windows-x64|windows-arm64` and `--prepared-source` for frozen shared integration inputs; the latter avoids mutating a source tree while target compilers are live. Missing ARM64 MSVC platform tools are build-input failures, not product regressions.

The requested Mac validation uses `tests/containers/Dockerfile_mac` only after all implementation completes. Its Linux container checks package hashes, ARM64 Mach-O headers, owned links/modes and shell syntax without loading Mach-O or mounting host profiles. It provides no macOS kernel, Keychain, GUI or native runtime evidence. Mac host use is limited to build/material preparation. [Docker documents the Linux VM boundary](https://docs.docker.com/desktop/features/vmm/).

## Five-target native packaging

`tools/prepare_native_release.py` stages a consolidated release only after passed correctness and paired performance receipts identify the same frozen five-target matrix. It rechecks every input, requires the immutable project HEAD, finalizes release metadata and canonical POSIX archive roots, compiles both Windows MSI scopes, includes corresponding source bundles, and writes the shared update manifest and checksums. It does not publish. Product runtime bytes remain those accepted by the local gates; release metadata changes are identified in the accompanying acceptance record.

For an identity/resource metadata change, `tools/build_native_linux.py --reuse-core-build ARCHIVE --core-producer RECEIPT` can reuse the owned compiler object cache. It verifies the archive, frozen producer sources, declared resource patch, dependency/compiler recipes and target, rejects functional source changes, refreshes the identity header dependency and relinks all final executables. A fresh target receipt and subsequent package acceptance remain required.

`packaging/product.json` owns the native candidate version independently of the historical Rust preview. All five native cores now compile from the same integrated source. Windows ARM64 can use the pinned isolated LLVM-MinGW SDK via `tools/prepare_llvm_mingw.py` and `packaging/toolchains/windows-arm64-llvm.cmake`; no Visual Studio process is stopped. The bridge compiler supports the same SDK. MSVC builds require licensed redistributable CRT inputs; LLVM ARM64 still verifies every native core import, without adding an unused MSVC runtime.

`tools/collect_macos_runtime.py` collects declared Apple/Homebrew primary runtime files and interpreter standard libraries, thins ARM64 images, records source hashes/notices and resolves non-system Mach-O imports. It does not collect HOME modules, keys or Keychain data. The resulting package declares macOS 15.5+ on Apple Silicon; Apple system frameworks and OS services remain platform requirements. Native credential helpers preserve explicit configuration, including an empty helper reset.

`tools/package_native_linux.py` consumes matching immutable core/runtime receipts and assembles ELF runtime paths in a disposable labelled Docker volume. `tools/extract_runtime_archive.py` supports the producer's Python 3.11 without relying on newer tar APIs. All writes are confined and bounded. `packaging/posix/install.sh` is the complete package's local installation entrypoint, using its owned updater and verified package manifest. Product execution still waits for D01-D07 closure.

Final archive staging preserves canonical package roots for long PAX paths and hard links. Windows takeover resolves the exact numbered Inno uninstaller from the matching registry registration and verifies its hash again before execution. `tests/takeover_registration_acceptance.ps1` covers both ordinary numbered layouts and rejects extra arguments or paths outside the selected installation.

## Verified incremental native builds

Windows assembly also requires `tools/build_windows_msi_worker.py --llvm-root <pinned-sdk> --platform windows-x64|windows-arm64 --version <version> --destination <new-directory>` and passes that directory as `--msi-worker`. The freestanding worker imports only Kernel32 and is packaged with a source/binary receipt. Local integration acceptance exercises no-console execution, Unicode paths, exit-code propagation, scheduled request routing and invalid-argument rejection before MSI lifecycle acceptance.

Linux compiler object caches are accepted only with a cleaned producer receipt, matching libraries/tool recipes and source deltas matched to declared patches/overlays. Every final executable is relinked. Frozen source is transferred into the disposable build volume as one checksummed tar to avoid QEMU small-file reads on Windows bind mounts; source identity and original permission metadata remain verified. Build containers and volumes are removed by exact ownership labels.
