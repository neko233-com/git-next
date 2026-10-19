# Performance measurement policy

No blanket faster-than-Git, low-memory or low-latency claim is established for this experimental version. Correctness and compatibility precede replacement.

Use optimized final binaries. Record client/reference versions, source SHA, CPU/OS, repository object and worktree counts, transport, cache state, network and concurrency. Separate cold clone, warm clone, incremental fetch, already-current pull, status, object lookup and commit. Report median and tail latency across repeated trials, not a single fastest run. Compare identical results with independent fsck and refs/worktree checks.

Track process-tree resident/peak memory and child processes separately from language allocator counters. A file cache or OS page cache is not a client memory leak. Do not manufacture zero RSS from an unsupported measurement API. Capture protocol bytes/object reuse for incremental transfer; wall time alone does not establish less disk/network work.

No external Git/OpenSSH runtime fallback is permitted in performance runs. Explicit reference Git fixture generation is outside measured client operations. Benchmark only disposable repositories and public or authorized data; sanitize paths/hostnames from published reports.

Current implementation caps checkout/pack workers, shares a two-worker SSH runtime and copies only missing local objects. These are design choices, not evidence of a universal speedup. Missing operations cannot be benchmarked as faster successes.
