# Local build and acceptance

Rust 1.96+; stable rustfmt/clippy. No GitHub Actions or remote CI release gate.

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked
cargo build --release --locked
python tests/acceptance.py target/release/git-next --https
python tests/ssh_acceptance.py target/release/git-next
```

Windows binary suffix: .exe. Acceptance requires reference Git for disposable fixture generation and independent fsck only; the client is executed with an empty PATH, absent Git/OpenSSH commands and isolated HOME/USERPROFILE/config/known_hosts. SSH fixture also needs Python Paramiko. Reference Git is never bundled as runtime fallback.

The fixtures compare clone objects/refs/worktree, native init/add/commit, status, packed incremental local fetch, unsupported operations, writer locks, real public HTTPS shallow clone and loopback embedded SSH. Unknown and changed host keys must reject. This is a scoped suite, not full Git's conformance suite or a promise of ecosystem parity.

## Targets

Windows x64: native MSVC. Windows ARM64: aarch64-pc-windows-gnullvm with Zig's LLVM linker (not an MSVC artifact). Linux x64: x86_64-unknown-linux-musl. Linux ARM64: aarch64-unknown-linux-gnu with glibc 2.28 baseline. macOS: aarch64-apple-darwin with Apple SDK, on a build machine only; no installation or PATH change is required.

Examples for installed rust targets and cargo-zigbuild:

```sh
cargo zigbuild --release --locked --target x86_64-unknown-linux-musl
cargo zigbuild --release --locked --target aarch64-unknown-linux-gnu.2.28
cargo zigbuild --release --locked --target aarch64-pc-windows-gnullvm
```

Cross-compilation proves packaging feasibility, not runtime behavior. Linux/Windows ARM64 need native runtime acceptance before claiming operational support. macOS ARM64 acceptance is recorded separately after native build and isolated tests. Do not publish complete-replacement releases while compatibility.json remains false.

Package git/git-next binaries with README, LICENSE and compatibility.json. Calculate SHA256SUMS locally after the final build; verify unpacked bytes before upload. Configured signatures must also verify locally. Publish only a consolidated release with an exact accepted scope and no private fixture data. Source publication can precede a formal binary Release.
