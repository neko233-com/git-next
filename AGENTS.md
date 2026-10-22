# git-next engineering contracts

- This is an independent public repository. Do not edit the parent GitSSS implementation or production configuration.
- Default new repositories to `main`; preserve existing branch names and object formats.
- Never spawn Git or OpenSSH as a runtime fallback. Explicit user-configured hooks/filters and Git-based acceptance fixtures must be distinguished from runtime fallbacks.
- Unknown commands and unsupported semantics must fail clearly, without silently ignoring options or modifying a repository.
- Preserve `.git` compatibility, atomic ref/index updates, host-key verification and existing user credentials. Test mutations in temporary repositories.
- Never write credentials or private deployment identifiers into source, fixtures, benchmarks or reports.
- Keep README, Markdown guides and `docs/site/` HTML accurate. Every compatibility/performance claim needs reproducible evidence and a declared scope.
- No `.github/workflows/`: formatting, clippy, tests, packaging and checksums run locally. Do not enable GitHub Actions.
- The intended installed command is `git`. Replacement of the user's Windows Git is authorized, but must remain blocked while the embedded compatibility report says full parity is false. Keep the preview separate, preserve rollback metadata, and do not integrate it into a production Git server.
- Use Conventional Commits and an `Assisted-by: Codex:GPT-6` trailer. No force push, amend, squash or automated sign-off.

## Interface languages

- A language is a document: `locales/<code>.json`. `build.rs` discovers the directory and embeds every document, so adding a language never touches Rust code, a registry, an alias table or a test fixture.
- English is the source language. Code strings are the keys; a document lists only the messages it overrides, and anything missing falls back to English per message. A partial document is valid and preferred over a wrong one.
- Verify a language once, when its document is added. Do not add per-language regression cases to the test matrix: the test matrix verifies behaviour, not wording. A wrong or awkward string is a defect of that one document and is fixed by editing the JSON.
- Every locale change must pass `python tools/check_locales.py`. It fails on an unreadable document, a duplicate key, a key the source no longer produces, a named placeholder that does not survive translation, a translation that disagrees with the source on a trailing newline, and unsorted keys. `--fix` sorts, `--coverage` reports what each language still leaves to English.
- The checker validates configuration, not prose. Do not add a translation service, a network lookup or a golden-text comparison.
- Never translate product identity, command and option names, machine-readable output, or user data (paths, refs, hashes, commit messages).

## Windows installer

- The package's job is declarative: files, registry values, shortcuts. Anything stateful - runtime configuration, update registration, takeover - is an *integration* that must be idempotent and must never abort a file installation. A custom action that fails after five minutes of copying destroys a correct installation to report an unrelated problem.
- Every custom action records its own failure before signalling it (`%ProgramData%\git-next\logs\msi-action-<mode>.log`, falling back to the local application data root). A bare `Error 1722` is not an acceptable failure mode.
- Stateful integrations report through `git install verify`, not through the installer's exit code alone. The verifier is read-only and takes no locks, so it works while the product is in use and while something else is broken.
- `/x` on a taken-over installation is refused on purpose: roll back the takeover first. Do not weaken that guard to make a test pass.
- Test the lifecycle with `tools/msi_lifecycle.py`, never by installing over a working toolchain. The machine scope only accepts a root inside Program Files, so the isolated root lives there.


## Documentation

- `docs/GIT-GUIDE.md` is the plain-language manual that replaces the official Git documentation for our users. It is task-first: a reader who has never used Git must be able to follow it end to end. Keep every command it shows one the binary actually accepts, and check `--help` when in doubt.
- `docs/WHY-GIT-NEXT.md` records the measured speed work and the checks beyond the Git command set. Numbers must come from a benchmark report under `docs/benchmarks/`, with the report date, and must state the limits as plainly as the wins.
- `docs/LANGUAGE.md` describes the language documents and the device-language resolution. `docs/DOCUMENTATION_SITE.md` describes how the site is rendered and published.
- Regenerate `docs/site/` with `python tools/render_docs.py` in the same commit as the Markdown change; the HTML is tracked and a stale page is a defect.
- Publish with `python tools/publish_docs.py`. It builds the `gh-pages` branch through a temporary index and never touches the working tree; the push is a fast forward.

