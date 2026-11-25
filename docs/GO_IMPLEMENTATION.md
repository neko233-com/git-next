# Go client implementation

The independent client is implemented in Go 1.27.3 at `cmd/git-next` and
`internal/**`, with `go-git/v5` providing object storage, refs, index and
transport. It never executes an installed `git` or `ssh` at runtime. This
repository is Go-only: Rust sources, Cargo files, the Rust acceptance runner and
vendored Rust crates have been removed. The native C/MSI distribution is a
separate, retained component with its own gates; this document describes the
independent Go client.

## Build

The historical Rust `vendor/` directory has been deleted. Go commands still use
`-mod=mod` explicitly so no implicit vendoring assumption remains; `go mod
vendor` is not a build step in this repository. Builds are reproducible with
`CGO_ENABLED=0 GOTOOLCHAIN=go1.27.3` and `-trimpath -ldflags "-s -w"`:

```sh
go -C tools/devtool run . build-go-client
# equivalent:
go build -mod=mod -trimpath -ldflags "-s -w" -o target/release/git.exe ./cmd/git-next
go test -mod=mod ./...
go vet -mod=mod ./...
```

The output is `target/release/git` (`git.exe` on Windows). The build command
pins `GOTOOLCHAIN=go1.27.3` and `CGO_ENABLED=0`.

## Command coverage

The CLI accepts the 39 top-level commands used by the acceptance runner:
`language, diff, show, restore, reset, eol, lfs, push, add, commit, switch,
checkout, merge, stash, cherry-pick, revert, rebase, submodule, worktree, init,
clone, fetch, pull, status, log, rev-parse, cat-file, hash-object, ls-files,
show-ref, branch, tag, remote, config, ls-remote, compatibility, version,
install, doctor`. Unknown flags and unsupported semantics fail explicitly.

Interface languages remain data documents in `locales/*.json`, embedded with
`go:embed`. English is the source and a missing message falls back per message.
The explicit selection and device detection use the same locale matching chain,
including region-to-script resolution such as `zh-TW` to `zh-Hant`.

## Measured verification

Run against the locally built Go binary on Windows x64:

| Gate | Result |
| --- | --- |
| `go test -mod=mod ./...` | pass (`internal/cli`, `internal/gitx`, `internal/i18n`) |
| `go vet -mod=mod ./...` | pass |
| `go -C tools/devtool run . cli-accept --tier l0` | **39 passed / 0 failed / 0 skipped** |
| `go -C tools/devtool run . cli-accept --tier full` | **57 passed / 43 failed / 0 skipped** |
| `git-next-accept --tier network` | not run |
| `verify-repo-contract` | 36 rules checked, 0 failed |
| `check-locales` | 9 documents checked |
| `migration-audit --require-complete` | complete, 0 Python entrypoints |
| `check-runtime-defaults` | blocked locally by the LFS manifest: the manifest-bound bundled LFS helper is not present in the dev tree, so the check cannot validate the real binary here |

The Go binary is **11,302,400 bytes** (about **11.3 MB**) with `-trimpath
-ldflags "-s -w"`. The historical Rust CLI built with `cargo build --release
--locked` was about **22 MiB** in the same checkout, so the Go client binary is
roughly half that size. The Rust figure is historical baseline evidence for the
old client and is not a current build input; this compares the standalone client,
not the native C/MSI distribution.

## Known gaps

- The full tier is not green. Most failures come from SHA-256 object-format
  repositories (`core.repositoryformatversion ... objectformat`); `go-git/v5`
  does not implement that format, so the client refuses those repositories
  instead of writing them incorrectly.
- A smaller set of full-tier cases still differs in status hooks, nested worktree
  paths and reset/diff byte output. They remain documented failures, not skips.
- `complete_git_parity` remains `false` and the five replacement blockers are
  unchanged. Do not replace a user's only Git installation based on this
  implementation alone.
- Packaging, MSI lifecycle and the native C distribution keep their existing
  gates. The terminal renderer is `mygo` (Go + MyGo); the historical `gpui-fast`
  renderer and its Rust + GPUI Fast binary are retained only as release evidence.
  Full desktop/MSI lifecycle verification is a separate pass.
- SSH, SFTP, the host monitor and askpass dialogs are not migrated to the Go
  terminal; they remain only in the historical Rust + GPUI Fast binary.

## Rollback

Rust is no longer a build path in this repository. Rollback uses the previous
source revision or an accepted published package; no repository format, ref,
index, LFS or configuration migration is involved. Restore from the matching
release asset rather than rebuilding a Rust client.
