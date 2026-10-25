# Game-project resource defaults · 2026-10-25

The [resource-profile report](benchmarks/native-game-resources-20261025.json)
compares the same source-built native **1.0.3** binary with its original defaults
and the six resource settings selected for **1.0.4**. It is configuration profiling,
separate from the later release's final-byte gates. Host: Windows
10.0.26300, AMD64 Family 26 Model 68 Stepping 0, 32 logical CPUs. These figures
are not macOS/Linux performance measurements or real Editor acceptance.

Four generated repositories, two Unity and two Unreal, each contain 128 small
files and eight distinct 16 MiB binary assets with two revisions. Every phase
verifies all current and historical asset bytes, worktree files, refs/index and
full `fsck`. The two profile orders are AB then BA. The first sample starts from
loose objects; the second follows earlier repacks. OS caches are not flushed.
Treat these as two distinct reuse states, not a latency distribution.

| First packing of loose-object fixtures | Original / resource profile seconds | Original / resource profile whole-job peak MiB |
| --- | --- | --- |
| Four repositories sequentially, `pack-objects` | 60.169 / 17.999 | 386.47 / 154.89 |
| Four repositories concurrently, `pack-objects` | 29.011 / 6.920 | 1519.21 / 440.49 |
| Four repositories sequentially, `repack` | 62.871 / 19.320 | 388.22 / 139.68 |

The first concurrent pack reduces peak committed memory by about **71%** in this
fixture. All eight pack-objects phases produce exactly **537,104,973 compressed
pack bytes** across the four repositories; the complete object histories remain
valid. This does not attribute the joint change to any individual setting.

The limits matter as much as the wins. A separate one-64-MiB-asset/two-revision
control has virtually identical first-pack peaks, about 307 MiB sequentially and
1227 MiB concurrently. Large individual objects still need memory. Reused packs
have mixed timings: the many-asset sequential repack's second sample is
1.703 seconds originally versus 3.024 seconds with the profile. Small clean-status
memory is essentially unchanged. No universal speed or memory reduction is claimed.

The separate LFS comparison uses two repositories, each with eight 16 MiB assets.
All four AB/BA runs pass upload/clone/SHA, corrupted-download rejection, failed-push
ref preservation and recovery. Maximum observed concurrent transfers decrease
from 16 to 4. Whole-tree peak commit is 195.90/195.82 MiB originally and
183.56/167.38 MiB with the profile, a median of about 196 versus 175 MiB.
End-to-end times are 10.891/13.950 versus 12.677/11.074 seconds; these include
fixture generation, checks, failures, recovery and cleanup, not just successful
network throughput.

Windows metrics are kernel job peak committed memory across all owned processes,
**not RSS**. Raw Git phase metrics exclude the parent controller and fixture
generation; LFS metrics include the fixture/controller/Git/LFS tree. Job caps
protect the experiments, not arbitrary production callers. The report retains
all phases, pack sizes, exact hashes, the single-large-asset counterexample and
the earlier failed large-file fixture attempt. See [defaults](DEFAULTS.md) and
[multi-project usage](GAME_PROJECTS.md).

The functional matrix separately verifies **60,037 tracked paths**, 36 ignored
cache paths and 489 Git calls across four independent repositories plus a nested
dual-engine monorepository: 33/33 cases pass. Actual root-process overlap reaches
four for status/diff/add/commit/fetch, with zero maintenance children. Its large
fixture explicitly selects a 300-second command deadline; the monorepository's
first 20k-file add takes 163.036 seconds in that run. This is an integrity and
isolation result, not a promise of fast first imports or an absolute stability
guarantee.

## Native 1.0.4 final-byte local gates · 2026-10-25

The independent final performance batch completed **5/5 actual passed lanes in 640.668 seconds** after the exact **14 passed / 4 authorized waived correctness lanes in 243.565 seconds**. The five lanes are 1,000, 10,000 and 100,000 files, loopback HTTPS, and frozen-input readback. Every local operation and network operation has **10 paired samples**; performance ran separately from compilation and functional tests. No check was skipped for a timing target, and no universal speed or memory advantage is claimed.

This is the final-byte **1.0.4 local-gate checkpoint**, source 3be06fa5bac1912ec7e77459830de4ebff055355, matrix 8d8e1cd946e9e0a8963a7e3164aed5eba95cd15a98446712d220eebce949ee8f. It is separate from the earlier resource-configuration profiling on the pinned 1.0.3 binary and from historical 1.0.3 release measurements below. Final staging, checksums and publication remain pending; four correctness waivers are not measured passes or installation acceptance.

Planned release assets, available only after publication: [batch performance](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-performance.json), [1,000 files](https://github.com/neko233-com/git-next/releases/download/v1.0.4/performance-1000.json), [10,000 files](https://github.com/neko233-com/git-next/releases/download/v1.0.4/performance-10000.json), [100,000 files](https://github.com/neko233-com/git-next/releases/download/v1.0.4/performance-100000.json), [loopback HTTPS](https://github.com/neko233-com/git-next/releases/download/v1.0.4/performance-network.json), and [correctness](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-correctness.json). Full reports retain binary/source/harness identity, output/object verification, paired medians/p95 and their memory scope. The following 1.0.3 measurements keep their original dates, sample counts and bytes.

**Retained r1 checkpoint and archive-layout correction.** The earlier 14-pass/4-waived correctness result (239.890 seconds) and five-pass performance result (618.615 seconds) remain unchanged in [Git history](https://github.com/neko233-com/git-next/blob/4d61669ddf4276c52fd977f048ccb72218cd7805/docs/DELIVERY_STATUS.json) and their original local reports. r1 staging then rejected a Mac candidate whose package.json was under a product-directory prefix; it was not published. The r3 candidate places the same payload at the archive root, retains the same native core source identity, and has newly executed Mac 51/P4/Python-absence validation. The distinct r2 matrix binds that new archive and reruns all fourteen non-waived correctness lanes and all five performance lanes. This changes archive layout, not Git storage or the parser's acceptance rules. Final staging/publication remain pending, and 1.0.4 installation runtime acceptance is still false.

## Native 1.0.3 measurements · 2026-10-24

The exact R7 correctness batch completed **16 passed / 2 user-waived lanes** in
**249.801 seconds**. All **five performance lanes passed** in
**668.849 seconds**: three file scales, loopback HTTPS and frozen-input
readback. Neither batch met the 60-second orchestration target; no checks were
skipped for that target. The terminal waiver covers only its two named terminal
test lanes. MSI, native CLI, transport, package and performance checks retain
their full requirements. The [artifact-bound source checkpoint](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.json)
records local gates as complete, with staging, workstation update and publication
pending at that checkpoint. Later completion is recorded separately.

These Windows x64 measurements compare native
2.54.0.windows.1.git-next.1.0.3 with reference 2.56.0.windows.3 on
Windows 10.0.26300, AMD64 Family 26 Model 68 Stepping 0, 32 logical CPUs.
Each command has **10 randomized pairs**, three warm-ups and warm OS cache.
Both clients use the declared five-setting Windows read-safety profile. Full
reports pin binary/source/harness hashes and verify complete output, refs,
worktree bytes and fsck. A ten-pair p95 is exploratory.

| Files | Operation | git-next median / p95 ms | Reference median / p95 ms |
| --- | --- | --- | --- |
| 1000 | version | 14.602 / 16.039 | 26.691 / 29.983 |
| 1000 | status_clean | 37.869 / 41.136 | 49.842 / 52.153 |
| 1000 | diff_clean | 30.702 / 31.945 | 42.154 / 44.618 |
| 1000 | show_metadata | 19.036 / 20.050 | 28.528 / 31.243 |
| 1000 | grep_files | 90.055 / 101.335 | 99.871 / 120.687 |
| 10000 | version | 16.038 / 16.635 | 27.765 / 31.816 |
| 10000 | status_clean | 208.153 / 267.964 | 232.690 / 253.850 |
| 10000 | diff_clean | 178.620 / 215.503 | 190.149 / 212.941 |
| 10000 | show_metadata | 23.725 / 27.424 | 33.867 / 43.516 |
| 10000 | grep_files | 977.826 / 1371.733 | 930.960 / 1243.645 |
| 100000 | version | 13.664 / 15.109 | 23.996 / 27.564 |
| 100000 | status_clean | 1576.423 / 1750.447 | 1547.554 / 1667.810 |
| 100000 | diff_clean | 1411.979 / 1485.115 | 1437.367 / 1496.629 |
| 100000 | show_metadata | 66.174 / 70.203 | 74.498 / 81.349 |
| 100000 | grep_files | 8092.692 / 8975.921 | 8524.933 / 9109.385 |

Full reports: [1000 files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-1000-20261023.json),
[10000 files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-10000-20261023.json),
[100000 files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-100000-20261023.json).

| Loopback HTTPS | git-next median / p95 ms | Reference median / p95 ms |
| --- | --- | --- |
| clone | 1051.060 / 1181.805 | 728.930 / 771.395 |
| fetch | 410.538 / 444.398 | 432.197 / 451.336 |
| pull_ff_only | 585.100 / 601.374 | 597.053 / 608.425 |

The [network report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-network-20261023.json)
uses HTTP/1.1, Schannel on both clients, a private fixture CA and static fixture
authorization. This is not WAN/provider authentication or GitSSS throughput
evidence. Native clone and 100,000-file clean status have higher medians here;
the complete 100,000-file grep still takes about 8.1 seconds. Primary-process
peak working set excludes children; the 100,000-file
status median is 33413120 bytes for git-next versus 6946816 for reference
Git. These results establish no universal speed or memory advantage.

R6 remains a historical complete local checkpoint: correctness 246.204 seconds,
performance 691.251 seconds. Its later release stage was stopped after hardlink
aliasing changed 25 ARM64 metadata/document files. Strict r6b validation rejected
the changed source. Original bytes were recovered from the frozen MSI; all 9,005
ARM files and 896 executable/DLL paths match, with no runtime binary writes.
The release-only fresh-inode replacement fix has its own R7 matrix and new full
measurements; R6 performance is not rebound to that changed harness.

The rejected R5c 100,000-file attempt is also retained: native grep exited 1
without stderr, with no valid timing for that failed operation. A separate forced
one-second probe established a real deadline can cause exit 1 and partial output;
the original attempt's root cause remains unproved. The large scale now has
120 seconds per command within the unchanged 1,800-second case limit, with the
same complete-output, exit, fsck and sample checks.

The sections below retain their historical dates, binaries and sample counts.

# Historical matched-artifact Windows measurements · 2026-10-21

The [dated matched-artifact batch](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-integrated-performance-20261021.json) passes all three file scales and local HTTPS workloads with **10 pairs per operation**, complete output/fixture verification and core identity `fb505db8` / binary `3bc2edf0`. Both clients use the same declared Windows read-safety profile; the reference is complete official Git 2.54. The actual batch took **596.63 seconds**, exceeding the 60-second target. No checks were skipped. A 10-sample p95 is exploratory; the previous larger-sample reports below retain their original identities.

Primary-process memory excludes children. These measurements do not imply universal speed or memory superiority. In particular, HTTPS clone remains slower than the reference; large full scans remain a latency limitation. Fetch and fast-forward pull are comparable in this generated local TLS fixture. This does not measure WAN providers or GitSSS same-branch push throughput.

| Files | Operation | Native median ms | Reference median ms |
| --- | --- | --- | --- |
| 1000 | version | 11.375 | 20.16 |
| 1000 | status_clean | 34.903 | 42.282 |
| 1000 | diff_clean | 29.24 | 36.968 |
| 1000 | show_metadata | 15.031 | 22.703 |
| 1000 | grep_files | 78.82 | 87.898 |
| 10000 | version | 11.941 | 19.273 |
| 10000 | status_clean | 239.229 | 251.528 |
| 10000 | diff_clean | 145.419 | 154.264 |
| 10000 | show_metadata | 20.931 | 29.202 |
| 10000 | grep_files | 700.734 | 703.387 |
| 100000 | version | 11.887 | 20.512 |
| 100000 | status_clean | 1911.129 | 1919.913 |
| 100000 | diff_clean | 1364.5 | 1353.605 |
| 100000 | show_metadata | 68.707 | 55.394 |
| 100000 | grep_files | 7738.232 | 7787.639 |
| HTTPS | clone | 1024.611 | 688.87 |
| HTTPS | fetch | 290.339 | 298.652 |
| HTTPS | pull_ff_only | 372.506 | 379.133 |

## Previous larger-sample checkpoint

# Final Windows measurements · 2026-10-21

The [final paired batch](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-final-20261021.json) passes all three file scales and loopback HTTPS workloads. Forty local pairs use the shipped Windows stability profile for the native client and the same five explicitly recorded reference overrides; twenty network pairs use the report's declared TLS settings. Every warm-up and sample must return the complete expected output. Primary-process peak memory is recorded separately; it excludes child processes. These measurements do not establish full Git compatibility or universal speed/memory superiority.

Both the 1071.771-second correctness batch and 1388.824-second performance batch exceeded the 60-second target. Cases were not skipped or relabeled to meet that target. At 100000 files, complete status takes about 1.89 seconds and full worktree grep about 7.15 seconds; this is a current full-scan latency limitation. The stable profile prevents the reproduced incomplete reads; it is not a claim that the underlying Windows race is eliminated under explicit parallel-read overrides.

| Files | Operation | Native median / p95 ms | Reference median / p95 ms |
| --- | --- | --- | --- |
| 1000 | version | 10.812 / 11.769 | 18.491 / 19.657 |
| 1000 | status_clean | 34.778 / 36.592 | 42.195 / 44.25 |
| 1000 | diff_clean | 26.278 / 27.629 | 34.03 / 35.714 |
| 1000 | show_metadata | 15.443 / 17.002 | 22.594 / 23.952 |
| 1000 | grep_files | 75.13 / 83.002 | 83.683 / 92.171 |
| 10000 | version | 11.241 / 12.462 | 18.889 / 20.126 |
| 10000 | status_clean | 214.638 / 228.737 | 224.052 / 236.867 |
| 10000 | diff_clean | 147.799 / 161.302 | 155.066 / 176.385 |
| 10000 | show_metadata | 19.95 / 22.875 | 26.489 / 28.842 |
| 10000 | grep_files | 702.804 / 766.934 | 712.344 / 790.701 |
| 100000 | version | 11.673 / 12.965 | 19.751 / 22.159 |
| 100000 | status_clean | 1893.537 / 1950.727 | 1905.992 / 1990.433 |
| 100000 | diff_clean | 1272.526 / 1332.905 | 1287.109 / 1338.518 |
| 100000 | show_metadata | 61.536 / 66.172 | 51.684 / 54.532 |
| 100000 | grep_files | 7148.149 / 7465.653 | 7190.098 / 7662.198 |

Full local reports: [1000](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-1000-20261021.json), [10000](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-10000-20261021.json), [100000](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-100000-20261021.json). The [read-stability diagnostic](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-windows-read-stability-20261021.json) retains both clients' failures under parallel defaults and successful stable-profile rounds.

| Loopback HTTPS | Native median / p95 ms | Reference median / p95 ms |
| --- | --- | --- |
| clone | 888.9 / 927.696 | 585.136 / 618.876 |
| fetch | 295.248 / 325.771 | 287.641 / 333.007 |
| pull_ff_only | 382.529 / 403.871 | 366.613 / 397.414 |

[Network report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-network-20261021.json). Native clone is slower in this fixture; this is not a WAN/provider or GitSSS server-throughput measurement. Earlier checkpoints below retain their original identities and are not reassigned to these defaults.

# Native read thread defaults

The core's unconfigured automatic budget is twice the current process-available logical CPU count. Windows and Linux honor inherited process affinity; macOS uses the OS online CPU count. Shipped Windows packages override read concurrency with the stability profile below; the automatic budget remains available through explicit configuration and on POSIX.

Windows ships `core.fscache=false`, `core.fsmonitor=false`, `core.preloadIndex=false`, `index.threads=1` and `grep.threads=1`. Takeover applies the same system defaults after legacy system configuration while preserving higher-priority user/repository settings. A 10000-file/1000-directory diagnostic reproduced omitted grep results in both the native client and reference Git with parallel reads; the exact stable profile completed 30 rounds per client without omissions and with unchanged file bytes. Disabling fscache alone did not fix grep. These defaults prioritize complete reads over speculative throughput; they do not establish the internal root cause or universal correctness.

The final paired Windows batch uses shipped native defaults and explicitly applies these same five settings to the reference with `--reference-read-safety`. Reports include `reference_read_safety_overrides`; this is a comparison under matching stable settings, not stock reference defaults. Every warm-up/sample must return complete expected results before its timing is accepted.

Index entry decoding, worktree stat preloading and case-insensitive index name-table loading use this budget. `index.threads` retains explicit user/repository/command-line priority; `1` requests serial work and `0`/`true` selects the automatic budget. Index optional EOIE/IEOT offset metadata allows compatible parallel decoding; object, ref and pack bytes are unaffected. Work is bounded by useful index blocks/entries. The old fixed preload cap of 20 is removed, with worker state allocated by the actual worker count. Read-only `git grep` uses the same automatic CPU*2 default; `grep.threads` and `--threads` override it normally. Compression, pack construction, checkout and fetch worker policies are unchanged.

`GIT_TRACE2_EVENT` reports the chosen read budget and actual index/preload/name-hash/grep workers. Acceptance checks default arithmetic, process affinity, explicit precedence, serial/parallel output equality and a very large explicit budget bounded by useful work. Paired benchmark runs include startup, index/status/read commands and full-file grep, reporting median, p95 and primary-process peak memory. Historical measurements below retain their original identities and must not be reassigned to the new defaults.

# Performance measurement policy

No blanket faster-than-Git, low-memory or low-latency claim is established for this experimental version. Correctness and compatibility precede replacement.

A Windows `FindFirstFileExW` large-fetch experiment was rejected before installation. In an eight-pair, instrumented 100000-file diagnostic, directory-walk median was 60.955 ms versus 57.781 ms for the instrumented baseline. Both scanned the same files and verified unchanged stage IDs/HEAD plus independent fsck. These are internal-phase diagnostics, not accepted process latency measurements. The uninstalled candidate was `94d37ceb...`; its new directory backend and dependency patch were removed. Existing accepted binaries and their timings remain unchanged. The diagnostic/source archive is retained locally under `.evidence/rejected-large-fetch-*`. [Microsoft API contract](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-findfirstfileexw).

Use optimized final binaries. Record client/reference versions, source SHA, CPU/OS, repository object and worktree counts, transport, cache state, network and concurrency. Separate cold clone, warm clone, incremental fetch, already-current pull, status, object lookup and commit. Report median and tail latency across repeated trials, not a single fastest run. Compare identical results with independent fsck and refs/worktree checks.

Track process-tree resident/peak memory and child processes separately from language allocator counters. A file cache or OS page cache is not a client memory leak. Do not manufacture zero RSS from an unsupported measurement API. Capture protocol bytes/object reuse for incremental transfer; wall time alone does not establish less disk/network work.

No external Git/OpenSSH runtime fallback is permitted in performance runs. Explicit reference Git fixture generation is outside measured client operations. Benchmark only disposable repositories and public or authorized data; sanitize paths/hostnames from published reports.

Current implementation caps checkout/pack workers, shares a two-worker SSH runtime and copies only missing local objects. These are design choices, not evidence of a universal speedup. Missing operations cannot be benchmarked as faster successes.

## Reproduce local latency

```sh
go -C tools/devtool run . benchmark target/release/git-next --runs 40 --files 1000 --output benchmark.json
```

On Windows append `.exe`. The harness creates an isolated HOME/USERPROFILE and a disposable 1000-file SHA-1 repository. It warms each operation three times, randomizes client order with seed 233, checks successful output against reference Git, and runs independent `fsck`. It records source-tree and binary SHA-256, versions, OS/CPU, median and nearest-rank p95, and refuses a binary that changes during measurement. The measured client PATH contains no Git/OpenSSH. `--client-fscache on|off` supports explicitly labeled experiments without changing installed configuration; default measurements retain the native default.

`--directories` selects the data layout (default 16); `--files 100000 --directories 10000 --fixture-layout packed --timeout 180` measures many small directories. Reports retain the configured/actual directory counts. Optional `--baseline-binary` plus `--baseline-checkpoint` measures a retained native client in the same randomized fixture with its verified binary/source identity. Both native clients receive the Git/OpenSSH-free runtime environment and unchanged-byte checks. Each layout remains a separate workload; a dense-directory improvement does not establish wide-tree performance.

Benchmark and validation-failure JSON files are written as UTF-8/LF on every platform. Repeated Windows generation must preserve that shared text representation.

## Reproduce loopback HTTPS latency

```sh
go -C tools/devtool run . network-benchmark target/release/git-next --runs 40 --files 1000 --output network-benchmark.json
```

Use `.exe` on Windows. This harness measures full clone, incremental fetch and fast-forward-only pull against a temporary local smart-HTTPS server. A private fixture CA, IP hostname validation and a generated static authorization header stay inside disposable profiles; no real provider login, user trust-store or global configuration changes occur. The reference server uses Git's HTTP backend only as fixture infrastructure. Native client PATH contains no Git/OpenSSH. On Windows the reference process explicitly selects OpenSSL so the short-lived private CA can be validated without a nonexistent fixture revocation service. The native C distribution uses its pinned owned TLS dependencies; rustls belongs to the independent Rust preview. Certificate verification remains enabled. The current fixture server is implemented in Go. The historical Python fixture used HTTP/1.0; neither those historical results nor a loopback fixture establish HTTP/2/v2-protocol, WAN, provider, SSH, packet-byte or object-reuse performance.

Each trial independently verifies HEAD/tracking refs, all worktree bytes, staged IDs, fsck and unchanged source object bytes. Fetch must preserve HEAD/index/worktree and retain the previous reachable objects. Fixture copies, clean-index priming, verification and cleanup are outside timing; process creation, TLS/HTTP/backend work, transfer and client writes are included. Three warm-ups precede randomized paired samples with seed 233. Reports use UTF-8/LF and pin the native binary/source, benchmark/helper hashes, versions and request counts. `--validation-only --runs 1 --files 16` runs a conformance smoke and emits no latency statistics. Such a smoke can overlap builds; accepted latency runs must wait for build/test/install activity to finish.

## Rejected serial HEAD/index experiment · 2026-10-20

The uninstalled `5a1fbe43...` candidate passed 23 optimized fixtures, including corrected protected-path validation, but its serial streaming proof delayed the existing parallel scan. In the [same-fixture 40-round comparison](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-status-head-dense-20261020.json), 100000 files across 16 directories measure clean status at 98.239/103.171 ms median/p95 versus the installed `d9a17139...` baseline at 91.827/101.450 and Git at 78.021/85.348. This shortcut was removed from the default implementation. Tracked/untracked ordering, configuration validation and regression fixtures are retained with the full comparison. Neither the earlier missed-validation prototype nor this slower candidate was installed.

Wide-directory timing failed output validation: reference Git briefly reported existing files as deleted. A separate reference-only 10000-file/1000-directory diagnostic, with no native client executed, reproduced two nonempty status results in 60 default runs. The staged-object digest stayed unchanged and sampled files still existed. Explicit reference-only experiments disabling fscache or preload each had zero occurrences in 60 runs; this implicates that path but does not prove a root cause or a universal fix. The original Git/global configuration is unchanged. [Failure and reference-only diagnostic](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-reference-wide-validation-20261020.json). These failed runs have no valid wide-tree latency report and are not used as speed evidence.

The [installed native d9 checkpoint](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-installed-wide-stability-0.3.0-dev-d9-20261020.json) independently passes 60 clean status calls in each object format on 10000 files/1000 directories. Every worktree byte, staged ID, HEAD and fsck is checked; optional-lock-disabled calls leave the index unchanged. This establishes scoped stability, not wide-tree latency or complete Git parity.

## Windows ignored-status checkpoint · 2026-10-20

Installed `c425734c0640...`, 40 paired warm samples, retained ecf1 baseline, isolated profiles and system config enabled. Native PATH contains no Git/OpenSSH. No own build/test/install overlaps measurement; output, stage IDs/HEAD and fsck checks pass. These are plain status/read measurements, not dirty ignored-mode or production/Unity performance proof.

[1000 data files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status-ignored-1000-20261020.json):

| Operation | Current median / p95 ms | Prior ecf1 median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| version | 7.063 / 8.442 | 7.184 / 7.761 | 19.115 / 19.965 |
| rev_parse | 10.936 / 12.078 | 10.862 / 11.955 | 20.370 / 21.988 |
| status_clean | 16.334 / 17.243 | 16.503 / 17.928 | 25.846 / 27.611 |
| cat_file | 11.185 / 12.280 | 11.180 / 12.644 | 21.016 / 23.743 |
| diff_clean | 15.386 / 17.106 | 15.305 / 17.358 | 22.278 / 24.883 |
| show_metadata | 11.535 / 13.581 | 11.778 / 13.889 | 23.179 / 26.501 |
| log_metadata | 12.119 / 13.591 | 12.201 / 13.316 | 23.358 / 25.318 |

[100000 data files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status-ignored-100000-20261020.json):

| Operation | Current median / p95 ms | Prior ecf1 median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| version | 7.208 / 8.246 | 7.093 / 8.319 | 19.255 / 22.817 |
| rev_parse | 11.844 / 16.567 | 11.699 / 12.960 | 21.514 / 25.362 |
| status_clean | 94.205 / 100.813 | 92.986 / 104.870 | 77.456 / 84.167 |
| cat_file | 12.550 / 13.596 | 12.385 / 13.486 | 23.650 / 25.136 |
| diff_clean | 59.844 / 63.304 | 60.127 / 64.315 | 59.554 / 64.556 |
| show_metadata | 12.143 / 13.306 | 12.257 / 13.939 | 46.995 / 48.854 |
| log_metadata | 12.165 / 13.553 | 12.173 / 13.404 | 22.817 / 25.495 |

## Windows status-v2 checkpoint · 2026-10-20

Installed `ecf1b66b6dd1...` has two 40-round paired reports, with retained a4ba in each randomized fixture. System configuration is enabled, profiles isolated, native PATH contains no Git/OpenSSH, and output/fsck checks pass. No own build/test/install overlaps timing; caches remain warm. These reports measure the existing plain status/read commands, not dirty-v2/submodule/history scaling.

[1000 data files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status-v2-1000-20261020.json):

| Operation | Current median / p95 ms | Previous a4ba median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| version | 6.854 / 7.947 | 6.980 / 7.625 | 18.250 / 19.560 |
| rev_parse | 10.820 / 12.657 | 10.783 / 12.682 | 20.312 / 23.744 |
| status_clean | 16.332 / 18.289 | 16.285 / 17.864 | 26.007 / 28.137 |
| cat_file | 11.006 / 12.414 | 11.037 / 12.660 | 21.083 / 23.620 |
| diff_clean | 15.084 / 16.609 | 14.924 / 16.159 | 21.886 / 24.299 |
| show_metadata | 11.107 / 12.489 | 10.965 / 12.181 | 22.401 / 23.617 |
| log_metadata | 12.211 / 13.384 | 11.966 / 13.153 | 22.956 / 24.746 |

[100000 data files](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status-v2-100000-20261020.json):

| Operation | Current median / p95 ms | Previous a4ba median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| version | 6.771 / 7.640 | 6.564 / 7.723 | 18.458 / 20.522 |
| rev_parse | 11.731 / 13.031 | 11.835 / 12.836 | 21.754 / 22.980 |
| status_clean | 87.212 / 89.747 | 87.383 / 91.492 | 71.041 / 74.962 |
| cat_file | 11.513 / 12.788 | 11.734 / 12.673 | 21.789 / 23.815 |
| diff_clean | 57.819 / 60.004 | 56.813 / 59.607 | 55.671 / 60.087 |
| show_metadata | 11.895 / 13.003 | 11.611 / 12.569 | 45.975 / 47.892 |
| log_metadata | 11.038 / 12.304 | 11.442 / 12.128 | 21.550 / 23.698 |

Earlier network and large-layout reports below keep their original bytes/scope. Broad speedup, memory, cold-cache, Mac and physical ARM acceptance remain open.

## Windows installed staging-correction checkpoint · 2026-10-20

Installed `a4ba7c7b...` has a [40-round 1000-file paired read/startup report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-staging-final-system-20261020.json), with retained b414 in the same randomized fixture. System configuration is enabled, profiles are isolated, native PATH contains no Git/OpenSSH and output/fsck checks pass. Builds, tests and installation finish before timing. OS caches stay warm.

| Operation | Current median / p95 ms | Previous b414 median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| version | 7.437 / 9.156 | 7.213 / 8.732 | 19.702 / 21.944 |
| rev_parse | 11.805 / 12.913 | 11.625 / 13.128 | 21.137 / 23.217 |
| status_clean | 17.348 / 18.599 | 17.202 / 17.931 | 27.172 / 28.687 |
| cat_file | 12.235 / 13.709 | 12.298 / 14.049 | 23.114 / 26.368 |
| diff_clean | 16.597 / 17.905 | 16.715 / 18.559 | 24.198 / 26.747 |
| show_metadata | 12.172 / 13.321 | 12.193 / 13.003 | 23.788 / 26.056 |
| log_metadata | 11.805 / 12.822 | 12.074 / 12.867 | 22.540 / 24.311 |

The earlier 100000-file and loopback HTTPS results below retain b414 bytes and their original scope. They do not establish these corrective bytes' large-repository or network latency. Broad performance, memory, cold-cache, dirty/conflicted, Mac and physical ARM acceptance remain open.

## Windows loopback HTTPS checkpoint · 2026-10-20

The retained/installed-at-measurement `b41410ed...` client has [40-round paired measurements](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-network-https-status-format-20261020.json) and an [identity checkpoint](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-network-local-checkpoint-0.3.0-dev-status-format-20261020.json). The fixture has 1000 data files with repeated LF text lines, one attribute file and 270259 initial worktree bytes; its next commit edits ten data files. Three warm-ups precede randomized pairs with seed 233. System configuration is disabled and profiles are isolated; Windows reference TLS explicitly uses OpenSSL, native TLS uses rustls, and both validate the private fixture CA/IP. Static fixture authorization avoids provider/vault interaction. The local Python server responds with HTTP/1.0. Build/test/install activity finished before measurement.

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| full clone | 286.028 / 313.837 | 600.055 / 650.665 |
| incremental fetch | 175.167 / 187.368 | 279.447 / 310.341 |
| pull --ff-only | 247.101 / 261.362 | 358.906 / 383.719 |

Each trial verifies HEAD/tracking refs, every worktree byte, staged IDs, fsck and unchanged source object bytes. Fetch additionally preserves HEAD/index/worktree and retains old reachable objects. Copying, priming, verification and cleanup are outside timing; client startup, TLS/HTTP/backend work, transfer and writes are included. These small warm loopback results are faster than the paired reference; they do not establish WAN, large/binary repositories, cold-cache, SSH, HTTP/2/v2, protocol-byte reuse, memory or real-account performance. The later directory-output defect in these bytes is recorded separately; clean read/network measurements are not relabeled as the corrective build.

## Windows native status-format checkpoint · 2026-10-20

The installed `b41410ed...` bytes have [1000-file loose](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status-format-system-20261020.json) and [100000-file packed](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-status-format-dense-20261020.json) measurements. Each has 40 randomized samples per client/operation after three warm-ups, with the accepted `e5a1e9f8...` native client in the same fixture. Enabled system configuration, isolated profiles and native PATH without Git/OpenSSH are retained. Successful output and fsck pass; build/test/install activity finished before measurement. OS caches stay warm.

| Data files | Operation | Current median / p95 ms | Previous e5a median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- | --- |
| 1000 | version | 7.926 / 9.009 | 7.752 / 8.923 | 20.714 / 22.755 |
| 1000 | rev-parse HEAD | 12.138 / 13.412 | 12.045 / 13.304 | 22.211 / 23.854 |
| 1000 | status --porcelain (clean) | 17.426 / 18.762 | 17.474 / 19.116 | 27.875 / 30.190 |
| 1000 | cat-file (blob) | 11.677 / 12.783 | 11.248 / 12.799 | 22.347 / 24.081 |
| 1000 | diff (clean) | 16.780 / 18.331 | 16.482 / 17.940 | 24.403 / 25.797 |
| 1000 | show metadata | 12.173 / 13.488 | 11.723 / 12.697 | 23.679 / 26.973 |
| 1000 | log metadata | 12.840 / 13.993 | 12.490 / 13.581 | 23.941 / 26.069 |
| 100000 | status --porcelain (clean) | 92.109 / 95.833 | 92.877 / 101.530 | 76.995 / 83.450 |
| 100000 | diff (clean) | 61.447 / 65.924 | 60.757 / 63.594 | 60.540 / 64.770 |

Distinct layouts/settings are separate workloads. These correctness/output changes do not establish universal improvement; large clean status remains an unfinished performance requirement. Full protocol, cold-cache, memory, dirty/conflicted, Mac and physical ARM performance remain open. Invalid reference-wide timing reports remain excluded from speed claims.

## Windows status consistency checkpoint · 2026-10-20

The installed `e5a1e9f8...` bytes have [1000-file loose](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status-consistency-system-20261020.json) and [100000-file packed](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-status-consistency-dense-20261020.json) measurements. Each has 40 randomized samples per client/operation after three warm-ups, with the retained `d9a17139...` client in the same fixture. System configuration is enabled, profiles are isolated and native PATH contains no Git/OpenSSH. Successful outputs and fsck pass. Builds/tests/installation finished before timing; OS caches stay warm.

| Data files | Operation | Current median / p95 ms | Previous d9 median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- | --- |
| 1000 | version | 6.582 / 7.872 | 6.675 / 7.455 | 17.582 / 19.387 |
| 1000 | rev-parse HEAD | 10.773 / 11.767 | 10.921 / 11.826 | 19.931 / 21.319 |
| 1000 | status --porcelain (clean) | 15.651 / 17.982 | 15.709 / 17.223 | 24.881 / 27.252 |
| 1000 | cat-file (blob) | 10.675 / 11.745 | 10.650 / 12.082 | 20.851 / 23.597 |
| 1000 | diff (clean) | 14.486 / 16.508 | 14.562 / 15.706 | 20.988 / 23.475 |
| 1000 | show metadata | 11.080 / 12.630 | 11.150 / 12.447 | 22.037 / 23.867 |
| 1000 | log metadata | 10.720 / 12.031 | 11.084 / 11.784 | 20.615 / 24.294 |
| 100000 | status --porcelain (clean) | 88.570 / 94.355 | 88.779 / 95.999 | 70.960 / 76.161 |
| 100000 | diff (clean) | 58.121 / 60.929 | 58.206 / 61.869 | 56.308 / 60.362 |

These two layouts have distinct settings/object layouts and do not form a controlled scaling curve. Large clean status remains slower than Git. The correctness fixes are retained without claiming universal performance improvement. Wide-tree timing remains invalid because of the separately recorded reference-output failures; native wide-tree stability is accepted independently. Cold-cache, memory, dirty/conflicted workload, network, Mac and physical ARM performance remain open.

## Windows directory-only metadata checkpoint · 2026-10-20 · 0.3.0-dev

The current [installed bytes](ACCEPTANCE.md) use the required `FILE_FULL_DIR_INFO` subset instead of unused file-ID/short-name/MFT information. Five cache units and an independent Windows metadata API check pass, including a real junction; that API fixture explicitly records unavailable ordinary-symlink privilege. Optimized product conformance and installed LF/filter/credential checks also pass. [Microsoft's query contract](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_full_dir_info) explains the directory-only information source; measured results below determine actual performance.

All three workloads have 40 randomized samples per client/operation after three warm-ups, enabled conventional system configuration, isolated profiles and no Git/OpenSSH on the client PATH. Successful output matches reference Git and independent fsck passes. Builds, tests and installation do not overlap measurement. OS caches stay warm; no cold-cache, memory, network or broad-platform claim follows.

The [1000-file loose workload](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-full-directory-system-20261020.json):

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 7.441 / 8.909 | 19.959 / 21.494 |
| rev-parse HEAD | 11.209 / 12.473 | 20.524 / 22.954 |
| status --porcelain (clean) | 17.771 / 19.547 | 28.549 / 30.209 |
| cat-file (tracked blob) | 12.180 / 13.430 | 23.371 / 25.586 |
| diff (clean) | 16.881 / 18.066 | 24.917 / 29.376 |
| show --no-patch --format=fuller | 11.645 / 13.197 | 22.986 / 28.067 |
| log -n 20 (one fixture commit) | 11.730 / 12.876 | 22.176 / 24.800 |

| Data files / layout | Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| 10000 / loose | status --porcelain (clean) | 24.463 / 31.165 | 32.484 / 40.059 |
| 10000 / loose | diff (clean) | 18.508 / 20.994 | 27.981 / 31.424 |
| 100000 / packed | status --porcelain (clean) | 90.626 / 94.233 | 76.979 / 82.548 |
| 100000 / packed | diff (clean) | 60.456 / 63.182 | 57.660 / 64.905 |

[10000-file report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-10000-full-directory-20261020.json), [100000-file report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-packed-full-directory-20261020.json). The packed fixture has local input/LF settings and a distinct object layout. All seven median results are faster than the paired reference in the 1000/10000-file runs. At 100000 files, clean status remains near the prior shared-cache result, while diff moves from 84.942 to 60.456 ms median across the retained separate snapshots. Each report retains its own paired Git baseline and bytes. Status and diff medians remain slower than reference Git; the broad performance goal is incomplete.

## Windows shared-cache/lazy-diff checkpoint · 2026-10-20 · 0.3.0-dev

The newer directory-only metadata checkpoint is recorded above. This shared-cache snapshot retains its earlier bytes and timing results.

These previously installed bytes are pinned by [acceptance](ACCEPTANCE.md). Each paired workload has 40 samples per client/operation after three warm-ups, randomized order seed 233, enabled conventional system configuration and isolated profiles. The client PATH excludes Git/OpenSSH. Successful outputs match reference Git and independent fsck passes. Build/test/install activity does not overlap measurement. Cache snapshots live for one scan; no persistent daemon or repository layout migration is involved.

The [1000-file loose workload](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-shared-cache-system-20261020.json):

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 7.097 / 7.857 | 18.333 / 19.719 |
| rev-parse HEAD | 11.410 / 12.356 | 20.583 / 21.939 |
| status --porcelain (clean) | 21.090 / 22.641 | 25.991 / 28.278 |
| cat-file (tracked blob) | 10.691 / 11.968 | 20.623 / 22.519 |
| diff (clean) | 19.917 / 21.349 | 21.651 / 23.647 |
| show --no-patch --format=fuller | 11.303 / 11.928 | 22.158 / 25.208 |
| log -n 20 (one fixture commit) | 10.550 / 11.374 | 20.720 / 22.389 |

| Data files / layout | Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| 10000 / loose | status --porcelain (clean) | 24.261 / 26.466 | 31.575 / 33.421 |
| 10000 / loose | diff (clean) | 19.685 / 20.950 | 26.726 / 28.511 |
| 100000 / packed | status --porcelain (clean) | 91.116 / 93.629 | 71.142 / 75.057 |
| 100000 / packed | diff (clean) | 84.942 / 88.588 | 55.608 / 58.563 |

[10000-file report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-10000-shared-cache-20261020.json), [100000-file report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-packed-shared-cache-20261020.json). The packed fixture uses local input/LF settings; it is not the same object layout/settings as the loose fixtures. All seven operations are faster at median in the 1000/10000-file paired runs. At 100000 files, status/diff remain slower than Git despite improving from the retained 206.106/180.535 ms pre-optimization medians to 91.116/84.942 ms. These before/after runs occurred separately; each retains its own paired reference and exact bytes. The lazy-map-only experiment remains [recorded](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-packed-lazy-candidate-20261020.json).

Disabling core.fscache was rejected: the retained [explicit off experiment](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-packed-fscache-off-20261020.json) measures 2208.278/5236.688 ms clean status/diff medians. It did not alter installed configuration. The selected default keeps the cache enabled and shares per-scan listings. Large blobs, dirty/conflicted repositories, cold cache, memory, network, physical ARM64 and macOS performance remain separate requirements.

## Larger Windows worktrees: retained pre-optimization baseline · 2026-10-20

These reports pin the accepted/installed `264214cf...` bytes and their original source hash. Forty measured samples per client/operation, three warm-ups, seed 233, enabled system configuration and isolated profiles; all successful outputs match reference Git and independent fsck passes. No Git/OpenSSH is present on the measured client PATH.

| Data files / layout | Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- | --- |
| 10000 / loose | status --porcelain | 25.004 / 26.385 | 31.247 / 34.899 |
| 10000 / loose | diff (clean) | 32.818 / 35.130 | 25.255 / 28.048 |
| 100000 / packed | status --porcelain | 206.106 / 230.855 | 77.922 / 88.311 |
| 100000 / packed | diff (clean) | 180.535 / 195.507 | 57.700 / 62.514 |

The [10000-file loose sample](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-10000-files-20261020.json) retains the earlier fixture's isolated/system EOL settings. The [100000-file packed sample](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-packed-baseline-20261020.json) uses fixture-only fast-import to create a pack and LF checkout with local autocrlf=input/eol=lf. Each is an independent paired comparison; different object layouts/settings must not be treated as one controlled scaling curve. The larger clean status/diff results are slower than Git and remain an unfinished performance requirement.

The earlier [100000-file loose attempt](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-100000-loose-setup-failure-20261020.json) timed out in reference Git add after 180 seconds, before measurements. It has no valid latency result. Packed preparation avoids that loose-object setup path and is distinct from client runtime behavior. [Git fast-import input](https://git-scm.com/docs/git-fast-import) is used only by the fixture, never as a native fallback.

Use `--fixture-layout packed --files 100000 --timeout 180` for the packed workload. The timeout also covers fixture preparation; progress is printed outside command timing. Use `--source-checkpoint` when measuring a retained historical binary so current source is not assigned to old bytes. The new installed shared-cache/lazy-diff checkpoint above records matching optimized acceptance and paired measurements. Large blobs, dirty/conflicted repositories, cold cache, memory, network and platform-wide performance remain separate scopes.

## Windows x64 pending-merge snapshot · 2026-10-20 · 0.3.0-dev

The [previous pending-merge optimized bytes](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-merge-pending-system-20261020.json) have 40 samples per client/operation after three warm-ups, randomized order seed 233, 1000 tracked files and one commit. Conventional system configuration is enabled, the profile is isolated and the client PATH excludes Git/OpenSSH. All Windows/Linux build, installation and acceptance activity finished before measurement.

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 7.041 / 7.854 | 18.161 / 19.999 |
| rev-parse HEAD | 11.043 / 12.030 | 20.454 / 22.184 |
| status --porcelain (clean) | 21.634 / 22.525 | 26.229 / 28.522 |
| cat-file (tracked blob) | 10.625 / 11.863 | 20.563 / 23.183 |
| diff (clean) | 20.328 / 21.811 | 22.083 / 24.222 |
| show --no-patch --format=fuller | 10.582 / 11.768 | 21.165 / 22.873 |
| log -n 20 (one fixture commit) | 11.495 / 13.044 | 21.762 / 23.589 |

The previous binary's clean merge measurement below retains its original hash; it is not relabeled as this build. Linux container runtime conformance does not establish host performance. No universal speed, cold-cache, memory, network, large-repository, ARM64 or macOS claim is made.

## Windows x64 native-merge snapshot · 2026-10-20 · 0.3.0-dev

The historical native-merge optimized bytes have a [40-round startup/read comparison](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-merge-system-20261020.json). Same 1000-file/one-commit workload, three warm-ups, seed 233, enabled system configuration, isolated profile and no Git/OpenSSH on the client PATH. Build, installation and acceptance finished before measurement.

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 6.883 / 8.103 | 17.863 / 20.483 |
| rev-parse HEAD | 11.113 / 12.045 | 20.564 / 21.883 |
| status --porcelain (clean) | 20.708 / 22.129 | 25.173 / 26.559 |
| cat-file (tracked blob) | 10.755 / 11.546 | 20.805 / 22.850 |
| diff (clean) | 19.548 / 20.863 | 21.330 / 23.006 |
| show --no-patch --format=fuller | 10.552 / 11.588 | 20.728 / 22.335 |
| log -n 20 (one fixture commit) | 11.297 / 12.242 | 21.554 / 23.972 |
| merge --no-edit topic | 110.294 / 118.245 | 105.835 / 109.239 |

The merge row is a separate [clean two-parent workload](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-merge-operation-20261020.json): 1000 text data files plus one attribute file, three initial commits, nonoverlapping same-file changes and one incoming separate-file change. Each operation starts from a fresh fixture copy with a primed clean index. Copying, priming and verification are outside measurement; process creation and captured output are included. Forty measured pairs follow three warm-up pairs in randomized order. Raw commit/parents/tree, index stages, all worktree file hashes, ORIG_HEAD, HEAD reflog, clean status and independent fsck match after each pair. Windows read-only object fixture cleanup is outside timing.

Reproduce with `go -C tools/devtool run . merge-benchmark target/release/git-next.exe --runs 40 --files 1000 --output merge-benchmark.json`. These results do not establish conflict, recovery, large-repository, cold-cache, memory, network or native macOS/Linux performance. Full takeover and the broad high-performance goal remain open.

The seven read/startup operations are faster in this run. Clean merge is slightly slower than reference Git at median and p95; the journal/backup safety path has not been justified as a faster merge implementation.

## Windows x64 native-discovery snapshot · 2026-10-20 · 0.3.0-dev

The installed optimized bytes are pinned by the [enabled-system report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-discovery-system-20261020.json). Forty samples per client/operation, three warm-ups, randomized order, 1000 files and one commit; successful output matches reference Git. The profile is isolated and client PATH contains no Git/OpenSSH. Conventional system configuration stays enabled. Build/install/acceptance activity finished before the final benchmark sequence.

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 6.844 / 7.977 | 18.330 / 19.937 |
| rev-parse HEAD | 11.292 / 12.290 | 21.007 / 23.673 |
| status --porcelain (clean) | 20.575 / 21.863 | 25.571 / 28.327 |
| cat-file (tracked blob) | 10.694 / 12.170 | 20.950 / 22.354 |
| diff (clean) | 19.597 / 20.984 | 21.440 / 23.097 |
| show --no-patch --format=fuller | 10.708 / 11.652 | 21.339 / 22.552 |
| log -n 20 (one fixture commit) | 10.825 / 12.001 | 21.868 / 24.139 |

These seven operations are faster in this workload. A fresh [previous-binary baseline](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-discovery-baseline-20261020.json) records clean status at 58.771/60.687 ms and clean diff at 58.054/62.714 ms with system configuration enabled. Removing Git subprocess configuration/auxiliary discovery reduces this startup cost; the enabled-system tripwire independently verifies no fallback. The baseline's source-tree hash is taken from its earlier accepted checkpoint after matching the binary hash, rather than assigning current source to old bytes.

The separate [system-disabled run](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-discovery-nosystem-20261020.json) records native clean status at 20.472/22.260 ms and clean diff at 19.607/21.097 ms. Earlier slow snapshots below retain their original accepted bytes and measurements. Configuration modes and fixtures can change performance; no cross-platform, large-repository, cold-cache, memory, network throughput or universal ultra-low-latency conclusion follows.

Use `--system-config-enabled` for the additional mode. `--source-checkpoint <accepted-benchmark.json>` identifies historical binaries only after verifying the recorded binary hash. Reports pin the harness hash as well as build inputs. No real user credentials are used or reported.

## Windows x64 LF/restoration snapshot · 2026-10-20 · 0.3.0-dev

Warm cache, 40 samples per client/operation, 1000 tracked files and one fixture commit. Process creation and captured output are included; the client runs without Git/OpenSSH on PATH. [Pinned installed binary and results](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-lf-20261020.json).

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 7.161 / 8.410 | 18.141 / 20.413 |
| rev-parse HEAD | 10.891 / 12.437 | 20.476 / 22.425 |
| status --porcelain (clean) | 40.408 / 44.413 | 36.940 / 39.395 |
| cat-file (tracked blob) | 10.787 / 11.700 | 20.889 / 22.538 |
| diff (clean) | 39.747 / 42.607 | 28.292 / 30.860 |
| show --no-patch --format=fuller | 10.936 / 12.438 | 22.359 / 27.358 |
| log -n 20 (one fixture commit) | 10.715 / 11.819 | 21.002 / 23.224 |

Version/ref/object/metadata reads are faster in this run. Clean status and diff remain slower. The history measurement contains one commit; no long-history throughput, all-operations or ultra-low-latency completion claim is established. Earlier snapshots ran at different times; compare each client pair within its own run. Canonical status retains the unchanged-file stat fast path and hashes after a mismatch so conversion-equivalent saves do not become false content changes.

## Windows x64 read-command snapshot · 2026-10-19 · 0.3.0-dev

Warm cache, 40 samples per client/operation, 1000 tracked files and one fixture commit. Process creation and captured output are included; the client runs without Git/OpenSSH on PATH. [Pinned optimized binary and results](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-read.json).

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 6.812 / 7.801 | 18.864 / 21.596 |
| rev-parse HEAD | 10.469 / 11.226 | 19.704 / 21.286 |
| status --porcelain (clean) | 40.775 / 44.109 | 37.490 / 39.105 |
| cat-file (tracked blob) | 10.389 / 10.908 | 20.508 / 21.700 |
| diff (clean) | 38.879 / 42.002 | 28.219 / 29.526 |
| show --no-patch --format=fuller | 10.558 / 11.660 | 21.611 / 23.357 |
| log -n 20 (one fixture commit) | 10.801 / 11.652 | 21.514 / 22.905 |

Version/ref/object/commit metadata reads are faster in this run. Clean status and diff remain slower. Log measures one commit; it provides no evidence about long-history traversal throughput. These results do not satisfy the universal high-performance goal. Reproduce with `go -C tools/devtool run . benchmark target/release/git-next --read-commands --runs 40 --files 1000`.

## Windows x64 local snapshot · 2026-10-19 · 0.3.0-dev

Warm cache, 40 samples per client/operation, 1000 tracked files; process creation and captured output included. Reference: Git for Windows 2.54.0.windows.1. [Exact optimized bytes and measurements](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.3.0-dev-status.json).

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 6.201 / 7.081 | 17.218 / 18.609 |
| rev-parse HEAD | 9.915 / 11.099 | 19.698 / 21.727 |
| status --porcelain (clean) | 39.973 / 41.997 | 36.767 / 39.298 |
| cat-file (tracked blob) | 9.964 / 10.888 | 20.482 / 22.878 |

The measured version/ref/object reads are faster in this run. Clean status remains slower than Git; this is not completion of the ultra-low-latency goal.

### Native-created index workload

A separate [40-round comparison](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-status-cache-0.3.0-dev.json) uses equivalent 1000-file repositories created/staged/committed by the native client. It compares the previous optimized binary with the stat-cache refresh binary, pinning both executable hashes. Warm status median falls from 95.104 ms to 41.487 ms (about 56%); p95 falls from 99.516 ms to 45.621 ms. Reference Git is 38.118 / 43.911 ms. This result applies to the native zero-stat index workload, not every repository.

The first status also publishes verified metadata, costing 119.841 ms in one unreplicated observation versus 96.716 ms before refresh. Subsequent scans reuse the refreshed stat cache. The first observation is not a repeated cold-start benchmark. Optional lock ownership predates scanning; read-only, v4, split/sparse and unfamiliar-extension indexes are not rewritten automatically.

Reproduce with `go -C tools/devtool run . status-benchmark CURRENT --baseline PREVIOUS --runs 40 --files 1000`. Preserve both optimized executable sets; debug binaries are not valid performance baselines. Other commands, transport throughput, large-object memory and large production repositories remain unmeasured.

## Windows x64 local snapshot · 2026-10-19 · 0.2.0

Warm cache, 40 samples per client/operation, 1000 tracked files; process creation and captured output included. Reference: Git for Windows 2.54.0.windows.1, standard installed command. [Machine-readable result](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-x64-0.2.0.json).

| Operation | git-next median / p95 ms | Git median / p95 ms |
| --- | --- | --- |
| version | 7.713 / 9.375 | 21.134 / 24.614 |
| rev-parse HEAD | 11.232 / 12.482 | 21.865 / 25.042 |
| status --porcelain (clean) | 46.131 / 51.507 | 41.812 / 48.588 |
| cat-file (tracked blob) | 11.790 / 13.062 | 23.760 / 25.590 |

Startup and the measured object/ref reads were faster in this run. Clean status was approximately 10% slower; ultra-low latency for all Git operations is not achieved. The adaptive status worker limit (one per 2000 entries, capped at eight) avoids excess worker creation, but does not establish an overall status speedup. Cold starts, large production repositories, network push, memory usage and other platforms were not measured here. Full-target checkout staging still adds I/O and temporary disk capacity; it is not a performance win claim.

The complete native batch uses the same Schannel backend for both Windows HTTPS clients. Local and network reports also record Windows primary-process peak working-set median/p95 and sample count, queried after the latency interval. Child and transport processes are excluded from this memory metric. Correctness lanes run concurrently; paired timings remain isolated from builds, emulation and regression workers.


Windows native packages enable Git's per-process core.fscache by default, matching the reference installation's directory/stat cache. Explicit user/repository configuration retains priority. The first consolidated candidate omitted this default and showed a material status regression; its measured results are diagnostic, not release acceptance. The corrected package is remeasured in the consolidated paired batch. Primary-process memory excludes child processes, so the reference cmd wrapper's memory alone is not a total-memory comparison.

The consolidated native batch uses the packed fixture layout for all three sizes: fast-import creates one pack and checkout materializes the same tracked LF files for both clients. Fixture commands have a separate setup deadline; measured commands retain their own deadline. A timed-out measured process and its owned descendants are terminated before fixture cleanup. The initial loose 100,000-file setup failed in the reference Git add operation and is retained as a harness failure, not a native latency result.

## Accepted native measurement scope

The user clarified that ultra-low latency is a GitSSS server requirement; git-next needs practical high performance without extreme tuning. The final native batch improves startup and status at 1k/10k/100k files. One measured boundary remains: 100k-file show metadata is 70.424ms versus 57.774ms for the reference (p95 78.754ms versus 63.701ms), so that operation does not match the reference. Network is comparable. These results do not establish universal speedups or lower total process-tree memory. See the [consolidated report](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-performance-consolidated-20261020.json).
