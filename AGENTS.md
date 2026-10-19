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
