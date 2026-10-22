# Windows installer proof

This page records what the Windows package has actually been made to do, on a real
machine, with the evidence each claim rests on. It exists because "the MSI builds"
and "the MSI installs" are different statements, and only the second one matters.

## What is proven

### Full lifecycle, user scope, at the package's own path

`msi_lifecycle.py` installed, repaired and removed the package at
`%LOCALAPPDATA%\Programs\Git`, which is where a user-scope installation belongs:

| Step | Result | Time | What was checked |
| --- | --- | --- | --- |
| install | 0 | 45.1 s | 10,658 files; `cmd\git.exe` 5,248,000 bytes; `bin\git.exe` present; `updater\msi-worker.exe` present; product code registered |
| the installed binary | — | — | `git --version` prints `2.54.0.windows.1.git-next.0.3.0` from the installed tree |
| repair | 0 | 61.8 s | entry points intact, file count unchanged |
| uninstall | 0 | 50.6 s | entry points gone, product code removed from the uninstall registry |

Receipt: `docs/benchmarks/msi-user-lifecycle-20261022.json`.

The `cmd\git.exe` size check is the point of the whole exercise: a bridge forwarder
is a few kilobytes, the payload is 5.2 MB, and an IDE that starts a forwarder cannot
work. The installer is verified on the file the IDE will call, not on a file count.

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

## What this work fixed

Every item below was found by trying to install, and each one is a defect that would
have reached a user.

**1. A custom action could destroy a correct installation.** Making the update task
registration fail the install meant a five-minute file copy was rolled back because a
scheduled task could not be created. Registration is an integration, not part of the
file installation: it is now attempted, recorded if it fails, and reported by
`git install verify` with the command that repairs it. An install cannot be broken by
an optional integration any more.

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

**5. A TLS setting could disable every remote.** Unrelated to the installer, found while
building the clone benchmark: an `http.sslCAInfo` that rustls cannot parse (curl's
`ca-bundle.trust.crt` is the common Windows case) made all HTTP and HTTPS remotes fail.
TLS configuration now applies only to `https://`, and an unparsable bundle falls back to
the default trust store. See [vendor/gix-transport/PATCHES.md](../vendor/gix-transport/PATCHES.md).

## The agent path

An installation is something an automated agent has to drive: install, verify, decide,
repair. That path must not require a shell with the right language, a GUI, or an
elevated desktop session.

```sh
msiexec /i git-next.msi /qn /norestart REPLACEGIT=1     # install, no interaction
git install verify --json                                # verify, machine readable
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

## What is not proven here

- **A clean machine.** This machine already had a git-next installation and an active
  takeover, so the machine-scope test exercised the upgrade and over-install paths, not
  a first install on an empty machine. The user-scope test did start from nothing.
- **Uninstalling a taken-over installation.** Refused by design; exercising the
  rollback needs PowerShell, and this session cannot start it (the PowerShell tool
  fails with a sandbox injection error, and the shell refuses to launch it). The
  documented order is rollback, then uninstall; that whole sequence needs a session
  with a working PowerShell.
- **Upgrade from a real previous release.** The archived `0.2.999` fixture predates the
  current launch conditions and refuses to install beside an official Git. A synthetic
  0.3.1 built by bumping the version was rejected by the runtime manifest check - which
  is the correct behaviour, since the payload really was 0.3.0. The upgrade path is
  proven only between two builds made by the same generator.
- **Stale files after over-install.** Installing the new layout over a takeover-era
  directory left files from both layouts (19,813 files on disk against a 10,600-file
  table). `install.ps1` owns the stale-file removal; the MSI alone does not clean a
  directory it did not create.

## Reproduce

```sh
# build the package from a distribution tree
python packaging/native_msi.py --distribution <tree> --wix .evidence/tools/wix5/wix.exe \
    --output-dir <out> --scope user --allow-experimental-local

# prove install, repair and uninstall at the package's own path
python tools/msi_lifecycle.py \
    --package <out>/git-next-0.3.0-windows-x64-user.msi \
    --work "$LOCALAPPDATA/Temp/gn-lifecycle" \
    --natural-folder --install-dir "$LOCALAPPDATA/Programs/Git" \
    --receipt docs/benchmarks/msi-user-lifecycle-20261022.json --skip-upgrade

# verify any installation, including a broken one
git install verify --root "C:/Program Files/Git" --json
```

## The checklist this leaves

- [x] install, repair, uninstall at a real path, with a receipt
- [x] major upgrade over an existing product, old registration removed
- [x] entry points verified as the real payload, not a forwarder
- [x] custom action failures produce a named reason
- [x] machine-readable verification that needs no PowerShell
- [ ] first install on a clean VM
- [ ] uninstall of a taken-over installation, after a real rollback
- [ ] upgrade from the previous release artifact
- [ ] stale-file cleanup proven on a takeover-era directory
