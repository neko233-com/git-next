# Acceptance snapshot · 2026-10-19

Experimental preview only. Full Git parity remains false. No system Git replacement and no production Git server integration. No formal binary release.

Windows x64: optimized binary passed local object/ref/worktree comparisons, reference Git fsck, native init/add/commit, packed incremental local fetch, writer lock/state/hook refusal, real HTTPS shallow clone and embedded loopback SSH with unknown/changed-host rejection. The client PATH contained no Git/OpenSSH. macOS ARM64: native optimized build and the same local/HTTPS acceptance passed in a temporary build-only directory; Git was not installed or replaced there. Linux x64/ARM64 and Windows ARM64: cross-compilation and executable architecture checks passed; native runtime acceptance remains outstanding.

All five targets were packaged locally and extracted bytes checked against source binaries. SHA256SUMS verified. Archives are experimental local artifacts, not published Releases. Mirror fallback was implemented but real third-party mirror acceleration was not measured. No broad performance claim.

| Target | git-next SHA-256 | Acceptance |
| --- | --- | --- |
| windows-x64 | `14652c2c83914f6831abbaa4f59bb57b7278289a4cefa691421a786689e62ac4` | Local + HTTPS + embedded SSH |
| windows-arm64 | `07af82c5f5cb6a6ee1c4dfbad6cc03276ac3b814000e2e196d2ee90938c2c552` | Compile / architecture / package only |
| linux-x64 | `01bb1646ce27ecc883562c03e69f808bf450d047c74f22d237dbb3e071b2ff24` | Compile / architecture / package only |
| linux-arm64 | `6ebc2067cae3a52d0bc7db7703b75666fc9d22bfe1e9191cfbeec7287088bc11` | Compile / architecture / package only |
| macos-arm64 | `467953c4fa47a1ba71c99432101a07e724e5142607a1b678dd3ee33208b9f665` | Local + HTTPS; no installation |
