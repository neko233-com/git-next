# Windows installer proof

This page separates native MSI lifecycle evidence, workstation recovery and the
independent Rust preview's verification command. A compiled MSI, an isolated lifecycle
pass and a completed workstation takeover are different acceptance layers.

## Current 1.0.3 checkpoint: R7 rejected, R8 pending

R7 local correctness completed with 16 passed lanes and two user-waived terminal
lanes in 249.801 seconds; its paired performance passed 5/5 in 668.849 seconds and
release staging completed. Windows x64 user/machine lifecycle receipts passed 27/25
checks and were reused only after exact-input validation. Windows ARM64 MSI evidence
is compilation/table/static checking, not hardware lifecycle acceptance.
[Fixed R7 evidence](https://github.com/neko233-com/git-next/blob/33269276902e1d3c52fab176516f14db908b7570/docs/benchmarks/native-python-retirement-20261023.json).

The subsequent real workstation bootstrap failed after 1,127.534 seconds: cleanup
marked its own `package.json` stale, and original-Git removal also deleted the shared
`bin/git.exe` entry. Completion and rollback required recovery. Both files were
restored with `CreateNew` from verified original bytes. Readback then matched all
10,696 declared immutable files (1,362,591,995 bytes), found zero Python entries among
10,712 files, and passed both owned P4 entrypoints with empty PATH. Twelve personal
profile files remained unchanged. This recovery has **`release_gate=false`** and does
not establish a completed installation, rollback lifecycle or release.
[Recovery record](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-workstation-r7-recovery-20261024.json)
has SHA-256 `dbee9b0ea14b8bb499cb64c094c82e0feb531f9defe8f0633171344b6c9f1c17`.

R8 source adds a protected complete snapshot before MSI can overwrite original
`cmd`/`bin` bytes, pins the snapshot manifest SHA before MSI, and supplies that pin
to post-MSI Apply. Same-directory cleanup and protected rollback are being repaired.
These source changes require new applicable gates and actual installation before
publication; R7's passes and recovery do not validate them. See [installation](INSTALLATION.md).

## Current native verification entrypoints

The native C CLI does **not** implement `git install verify`; that command belongs
to the independent Rust preview below. Native source tooling can inspect an actual
package directory/archive without executing the product:

```powershell
go -C tools/devtool run . migration-product-audit --package <installed-root-or-archive> --json <new-report.json> --require-complete
```

This checks package identity and Python interpreters/libraries/scripts. It is not an
all-file immutable SHA audit or proof that installation completed. The recovery record
above binds those separate full-hash results. Go is needed to run source verification
tools, not to install the native product.

Exact-input reuse of already completed MSI lifecycle receipts has a separate verifier:

```powershell
go -C tools/devtool run . native-msi-evidence --report <completed-batch.json> --matrix <original-matrix.json> --current <current-matrix.json> --reference-sha256 <reference-git-sha256> --cases msi-user,msi-machine
```

This validates the frozen inputs and allowed changes for receipt reuse. It does not
install, repair or uninstall the current workstation. A new or changed installation
still needs its applicable lifecycle and protected takeover/rollback evidence.

## Historical 2026-10-22 lifecycle evidence

The tables and investigation below describe the original 0.3.0-era checkpoint. Their
versions, limitations and session-specific launch failures are preserved history,
not a description of the current 1.0.3 workstation or a new release gate.

### Full lifecycle, user scope, at the package's own path

`go -C tools/devtool run . msi-lifecycle` installed, repaired and removed the package at
`%LOCALAPPDATA%\Programs\Git`, which is where a user-scope installation belongs:

| Step | Result | Time | What was checked |
| --- | --- | --- | --- |
| install | 0 | 45.1 s | 10,658 files; `cmd\git.exe` 5,248,000 bytes; `bin\git.exe` present; `updater\msi-worker.exe` present; product code registered |
| the installed binary | — | — | `git --version` prints `2.54.0.windows.1.git-next.0.3.0` from the installed tree |
| repair | 0 | 61.8 s | entry points intact, file count unchanged |
| uninstall | 0 | 50.6 s | entry points gone, product code removed from the uninstall registry |

Receipt: `docs/benchmarks/msi-user-lifecycle-20261022.json`.

The historical `cmd\git.exe` size check distinguished that layout's real payload
from an inherited forwarder. The receipt checked the actual IDE entrypoint rather
than inferring it from a file count; this does not claim that every compatibility
forwarder is invalid.

### Machine scope, major upgrade over an existing installation

Installing the machine package over an existing git-next installation:

| Fact | Value |
| --- | --- |
| exit code | 0 (279.3 s) |
| previous product | `{35F9C1DD-2B81-5F01-8735-064BAC6BC396}` removed |
| resulting product | `{36A02B2C-3A80-508B-B2C4-73A84FE31A69}` |
| products named git-next afterwards | one |

The major upgrade path is real: `RemoveExistingProducts` runs after the new product's
files land, and the old registration is gone afterwards.

## Historical fixes and preview-specific work

Every item below was found by trying to install, and each one is a defect that would
have reached a user.

**1. A custom action could destroy a correct installation.** Making the update task
registration fail the install meant a five-minute file copy was rolled back because a
scheduled task could not be created. Registration is an integration, not part of the
file installation: that checkpoint attempted it separately and recorded failures.
The independent Rust preview's `git install verify` can report integration problems;
the native C CLI does not provide that subcommand. Current native MSI results must
be read from its scoped lifecycle/action receipts.

**2. Custom action failures were invisible.** A Windows Installer custom action has no
console, so `msiexec` reported `Error 1722` and nothing else - no reason, no scope, no
stack. Every action now writes its failure to `%ProgramData%\git-next\logs\msi-action-<mode>.log`
(falling back to the local application data root for user installations). The
investigation in this session used that log to find each root cause within one install
attempt instead of guessing.

**3. The entry points were silently inherited.** This is the most damaging one. An
earlier layout leaves a forwarder at `cmd\git.exe` and `bin\git.exe` with the same
version stamp, and Windows Installer keeps a file it considers current. The result
observed here: 10,600 files installed, and an 8,192-byte stub left exactly where the
IDE looks - the failure the whole layout change was meant to remove. The package now
declares `RemoveFile On="install"` for those two paths, so they are removed before they
are written; the rows are present in the built database (`RemoveFile` table, components
`ReplaceEntry_*`). End-to-end confirmation is pending: the last install attempt could
not complete because the running shell holds an open handle on the installed
`cmd\git.exe`, which is also why the file could not be replaced by hand.

**4. The uninstall guard was correct but unexplained.** Uninstalling while a Git
takeover is active is refused on purpose, because removing the files would strand the
machine's Git. The refusal surfaced as a bare `Error 1722`; the reason is now recorded,
and the supported order is documented: roll the takeover back, then uninstall.

**5. Rust preview TLS configuration.** This fix belongs to the Rust client's gix/rustls
transport, not the native C engine. An `http.sslCAInfo` that rustls cannot parse (curl's
`ca-bundle.trust.crt` is the common Windows case) made all HTTP and HTTPS remotes fail.
TLS configuration now applies only to `https://`, and an unparsable bundle falls back to
the default trust store. See [vendor/gix-transport/PATCHES.md](../vendor/gix-transport/PATCHES.md).

## Historical Rust preview verification path

The following command and JSON describe the independent Rust preview implementation.
They must not be used as native C installation instructions or native MSI proof.

```sh
# independent Rust preview only
git install verify --json
```

`git install verify` is the verification half. It takes no locks and opens every file
read-only, so it runs while Git is in use and while the thing being diagnosed is
broken. It reads the manifest the installation itself carries, so it does not trust the
installer that wrote it:

```json
{
  "schema_version": 1,
  "ok": false,
  "version": "0.3.0",
  "install_root": "C:\\Program Files\\Git",
  "checks": [
    {"check": "manifest_identity", "ok": true},
    {"check": "entry_point:cmd/git.exe", "ok": false, "detail": "8192 bytes"},
    {"check": "files", "ok": false, "detail": {"missing": 0, "wrong_hash": 2}}
  ],
  "problems": ["cmd/git.exe is missing or not the real payload"],
  "next_steps": ["repair the installation: msiexec /famus {36A02B2C-...}"]
}
```

- Exit code 0 means verified, 1 means a named problem. Nothing is reported as a bare
  number.
- `problems` is what is wrong; `next_steps` is what to do about it. Both are prose an
  agent can read and a human can act on.
- `--hash-all` hashes every file; by default a deterministic sample is hashed plus
  every entry point, because hashing 1.3 GB takes longer than most callers want to wait.
- The check is deliberately independent of the runtime: it does not start `git`, does
  not use PowerShell, and does not need the update task, which is what makes it usable
  when the runtime is the thing that is broken.

## Limitations of the historical 2026-10-22 checkpoint

- **A clean machine.** This machine already had a git-next installation and an active
  takeover, so the machine-scope test exercised the upgrade and over-install paths, not
  a first install on an empty machine. The user-scope test did start from nothing.
- **Uninstalling a taken-over installation.** Refused by design; exercising the
  rollback needed PowerShell, whose launch failed in that historical session. That
  environment limitation does not describe the later R7 run. R7 did execute takeover
  but its completion/rollback failed as recorded above; R8 must verify the repaired path.
- **Upgrade from a real previous release.** The archived `0.2.999` fixture predates the
  current launch conditions and refuses to install beside an official Git. A synthetic
  0.3.1 built by bumping the version was rejected by the runtime manifest check - which
  is the correct behaviour, since the payload really was 0.3.0. The upgrade path is
  proven only between two builds made by the same generator.
- **Stale files after over-install.** Installing the new layout over a takeover-era
  directory left files from both layouts (19,813 files on disk against a 10,600-file
  table). `install.ps1` owns the stale-file removal; the MSI alone does not clean a
  directory it did not create.

## Historical reproduction examples

```sh
# build the package from a distribution tree
go -C tools/devtool run . native-msi --distribution <tree> --wix .evidence/tools/wix5/wix.exe \
    --output-dir <out> --scope user --allow-experimental-local

# prove install, repair and uninstall at the package's own path
go -C tools/devtool run . msi-lifecycle \
    --package <out>/git-next-0.3.0-windows-x64-user.msi \
    --work "$LOCALAPPDATA/Temp/gn-lifecycle" \
    --natural-folder --install-dir "$LOCALAPPDATA/Programs/Git" \
    --receipt docs/benchmarks/msi-user-lifecycle-20261022.json --skip-upgrade

# independent Rust preview only; unavailable in native C
git install verify --root "C:/Program Files/Git" --json
```

## Historical checklist

- [x] install, repair, uninstall at a real path, with a receipt
- [x] major upgrade over an existing product, old registration removed
- [x] entry points verified as the real payload, not a forwarder
- [x] custom action failures produce a named reason
- [x] Rust preview machine-readable verification that needs no PowerShell (not a native CLI feature)
- [ ] first install on a clean VM
- [ ] uninstall of a taken-over installation, after a real rollback
- [ ] upgrade from the previous release artifact
- [ ] stale-file cleanup proven on a takeover-era directory
