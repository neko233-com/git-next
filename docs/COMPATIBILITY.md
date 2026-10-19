# Compatibility contract

Status: experimental, full_git_parity=false. Intended command: git; installed preview: git-next. No runtime Git/OpenSSH fallback.

## Implemented subset

- `init [directory] [--bare] [-b branch]`: main default, existing repositories are not reformatted.
- `clone URL [directory] [--bare] [-b branch] [--depth n]`: native HTTPS/SSH, shallow network clone, SHA-1 local clone. Refuse an existing destination. Local sources with detached HEAD require an explicit branch; local shallow clones refuse.
- `fetch [remote]`: native network transfer and common configured ref updates; local default wildcard branch refspec. Existing objects reused. No complete prune/tag/custom-refspec parity.
- `pull [--ff-only] [remote]`: fetch, inspect ancestry; updating the worktree refuses before changing HEAD/worktree. Fetched remote refs and objects may have advanced. Already-current pull succeeds.
- `add [-A] [literal paths...]`, `commit -m message`: native index/tree/object/ref writing for ordinary unsigned commits. Root-relative paths only; no magic pathspec, conflict resolution, sparse index, active hooks, signing, merge/rebase/sequencer state.
- `status [--porcelain|-s]`, `log [-n count] [--oneline]`, `branch [name]`, `remote`, `config`, `rev-parse`, `hash-object`, `cat-file`, `ls-files`, `show-ref`, `ls-remote`: implemented flags are visible in `--help`; do not assume general Git CLI/output parity.

## Not implemented / not accepted

Push, receive-pack service, updating checkout/switch/reset/restore, merge/rebase/cherry-pick/bisect, stash, full diff/blame, LFS, submodules, sparse/partial clone, complete credential helpers, reftable, linked worktrees, complete hooks/signature/config/pathspec/rename parity and IDE integrations. SHA-256 reading primitives exist but full SHA-256 repository workflows have not passed acceptance. Unsupported commands/options must fail clearly.

## Embedded SSH

Strict known_hosts verification is mandatory; unknown/changed host keys reject. Supported subset includes Host/Include, HostName, User, Port, IdentityFile, IdentityAgent and UserKnownHostsFile, with first-match semantics. Keys and the native agent are attempted with bounded authentication attempts. Match, ProxyCommand/ProxyJump, agent commands, certificates/security keys and unsupported authorization modes refuse. An explicitly configured encrypted-key passphrase or password may be supplied through private process environment; never place it in a command, repository or report. The project does not alter system SSH settings or known_hosts automatically.

## Data and rollback

Use disposable repositories first. Index locks use exclusive creation; ref updates use Gitoxide compare-and-swap semantics. Local clone publishes a complete temporary destination, never hard-links source objects. No repacking or source repository migration is performed. Fetch and clone storage formats remain Git-readable within the tested subset.

A preview uninstall removes only the installed command if its checksum still matches; repositories, global Git configuration and original Git remain. Full system replacement requires a separately validated compatibility matrix, executable backup, atomic activation and verified rollback. That gate is currently closed.
