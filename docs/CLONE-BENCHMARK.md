# Clone measurement

The question this page answers is narrow: when a user runs `clone` against a real
remote, is this client faster than the Git it replaces, and where does the time
actually go. It is measured, not argued.

## Method

Everything is reproducible with the tools in this repository.

```sh
# a remote that behaves like a real host, served by the reference implementation
python tools/git_http_fixture.py --root <dir-of-bare-repos> --port 8123

# the comparison, alternating clients round by round
python tools/benchmark_clone.py --reps 9 \
    --target gitsss=http://127.0.0.1:3000/root/bench-big.git \
    --json docs/benchmarks/clone-gitsss-20261022.json
```

- **Same remote, same content, same machine.** Both clients clone the same URL, into
  the same destination path, with the destination removed before each run.
- **Interleaved rounds.** Running one client to completion and then the other lets the
  machine and the remote warm up for whoever goes second; that effect is larger than
  the difference being measured. Each round runs both clients and the order is swapped
  every round.
- **The first round is discarded.** It pays for the file-system cache and the first
  connection.
- **Median, not mean.** A single stalled run should not decide the result; the minimum
  and maximum are reported so the spread is visible.
- **Identical configuration.** Configuration is exported through `GIT_CONFIG_*` so both
  clients receive exactly the same settings.

Fixture: 3,000 files, ~100 MB of text, one commit, 12.7 MiB pack. Server: `gitsss`
running on the same machine, serving the repository over HTTP.

## Result

| Target | Client | Median | Min | Max | Against the reference |
| --- | --- | --- | --- | --- | --- |
| gitsss (local HTTP) | Git 2.55.0 | 3379 ms | 3182 | 3667 | — |
| gitsss (local HTTP) | git-next | **2726 ms** | 2179 | 3364 | **1.24x faster (-19.3%)** |

Raw report: [clone-gitsss-20261022.json](benchmarks/clone-gitsss-20261022.json).

The same comparison before the pack on the server was rebuilt measured 1.23x, so the
result does not depend on that repack.

## Where the time goes

The interesting part is not the ratio, it is the split. The server's own access log
records every request, and for one clone of this repository it shows:

| Request | Server time |
| --- | --- |
| `GET /info/refs?service=git-upload-pack` | 370-720 ms |
| `POST /git-upload-pack` (ref advertisement / negotiation) | 370-640 ms |
| `POST /git-upload-pack` (pack transfer) | 1000-2000 ms |

That is **2.0-2.9 s of the 2.7-3.4 s wall time spent inside the server**, before the
client does anything with the bytes. Two things are being paid for:

1. **Process startup.** `gitsss` serves each request by starting a `git upload-pack`
   process. On this Windows machine a bare `git --version` costs about 360 ms, so three
   requests cost more than a second before any work happens.
2. **Pack generation.** Building a 12.7 MiB pack from 100 MB of content is real CPU
   work, and it is identical for both clients.

The client side of the same clone is the remainder: about 0.5-1.0 s end to end,
including the pack index and the checkout, of which the checkout is ~130 ms by this
client's own trace.

Two conclusions follow, and both matter more than the headline ratio:

- **On a Windows host, clone speed is dominated by the server's process startup.** The
  same server on Linux starts a process in single-digit milliseconds, so the server
  share collapses and the client share becomes the whole number. A comparison run on a
  Windows workstation therefore *understates* the client's contribution.
- **No client can beat the server's pack generation.** Claims about clone speed that do
  not separate the two are measuring the server.

## GitHub

The intention was a second target: the same repository cloned from
`https://github.com/neko233-com/git-next`, to compare a public host with a self-hosted
one. **It could not be measured reliably in this environment.** The sandbox routes
HTTPS through an intercepting proxy that intermittently fails TLS with
`unexpected eof while reading` / `tls handshake eof`, and the reference client needs a
different TLS backend to reach GitHub at all. Runs that did complete produced medians
that moved by more than the difference between the clients.

What was measurable under the same conditions is the single-request path, which is the
one that is not dominated by the server:

| Operation against `https://github.com/neko233-com/git-next` | Git 2.55.0 | git-next |
| --- | --- | --- |
| `ls-remote` (one request, one TLS handshake) | ~1990 ms | **~1020 ms** |

That is exploratory: three runs per client, taken while the proxy was stable. It is
consistent with the architecture - one process instead of a launcher chain - and it is
*not* a clone result. To produce a real one, run the command above from a normal
network:

```sh
python tools/benchmark_clone.py --reps 9 \
    --target github=https://github.com/neko233-com/git-next \
    --json docs/benchmarks/clone-github-<date>.json
```

## What changed in this batch

The comparison exposed a defect that mattered more than any optimisation:

- **A TLS setting could disable every remote.** `http.sslcaInfo` pointing at a bundle
  rustls cannot parse - curl's `ca-bundle.trust.crt` is the common Windows case - made
  **all** HTTP and HTTPS remotes fail, plain `http://` included, because the HTTP client
  was built with the TLS configuration before the URL scheme was considered. TLS
  settings now apply only to `https://`, and an unparsable bundle falls back to the
  default trust store instead of aborting the connection.
- **The repository was opened twice.** After the fetch, the code reopened the freshly
  cloned repository just to check it out. The fetch already returns an open repository;
  the second open re-read configuration and re-scanned the pack directory for nothing.
- **A phase trace was added**, behind `GIT_NEXT_TRACE_TIMING=1`, so a slow clone can be
  attributed to transfer+index or to checkout instead of guessed at.

## Limits

- This measures one machine, one server, one repository shape. A repository with deep
  history and heavy deltas shifts cost from transfer to index; a repository with many
  small files shifts it to checkout.
- The Windows process-startup cost described above is specific to this host.
- The GitHub target is unmeasured here, as described.
- The `gitsss` server was run as-is from the parent repository's configuration; its
  pre-receive hook could not be used for the fixture repository in this environment, so
  the repository was populated on disk and cloned over HTTP. The clone path is the one
  measured and it is the unmodified server path.
