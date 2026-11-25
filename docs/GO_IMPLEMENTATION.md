# Go client implementation

The independent client is implemented in Go 1.27.3 at `cmd/git-next` and
`internal/**`, with `go-git/v5` providing object storage, refs, index and
transport. It never executes an installed `git` or `ssh` at runtime. The Rust
tree and the native C/MSI distribution remain unchanged rollback paths; this
document describes only the independent client.

## Build

The repository-root `vendor/` directory belongs to the vendored Rust `gix`
crates, not to Go. Never run `go mod vendor`; all Go commands must use
`-mod=mod`:

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
| `git-next-accept --tier l0` | **39 passed / 0 failed / 0 skipped** |
| `git-next-accept --tier full` | **55 passed / 45 failed / 0 skipped** |
| `git-next-accept --tier network` | not run |
| `verify-repo-contract` | 36 rules checked, 0 failed |
| `check-locales` | 9 documents checked |
| `migration-audit --require-complete` | complete, 0 Python entrypoints |
| `check-runtime-defaults` | blocked: requires the manifest-bound bundled LFS helper; the local dev tree only has the historical Rust helper |

The Go binary is **11,302,400 bytes** with `-trimpath -ldflags "-s -w"`. The
historical Rust CLI built with `cargo build --release --locked` was about
**22 MiB** in the same checkout, so the Go client binary is roughly half that
size. This compares the standalone client, not the native C/MSI distribution.

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
  gates. The Go terminal renderer (`mygo`) is accepted by the tooling alongside
  the historical `gpui-fast`; full desktop/MSI lifecycle verification is a
  separate pass.
- The Rust sources are retained as a reference and rollback path until the Go
  client passes the full tier and the consolidated product gates.

## Rollback

Build the Rust CLI into the same output path and continue using the previous
package; no repository format, ref, index, LFS or configuration path changes
are part of this migration.
