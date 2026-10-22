# git-next literal filter executable patch

Source: gitoxide `gix-filter` 0.36.0 from the crates.io source package. The original MIT and Apache-2.0 licenses are included.

A filter command that is exactly an existing executable file is launched directly, retaining the repository environment context and piped streams. This lets the native git-next LFS filter work from installation directories containing spaces or `~` without a POSIX shell. Other explicitly configured commands retain their existing shell semantics.

The host can set command-scoped filter environment once. git-next uses this to propagate root `-c` overrides through standard `GIT_CONFIG_COUNT` variables without changing the parent process environment. Diagnostics show the executable without printing the configuration environment.

Validate with `tests/lfs_acceptance.py`, including git-next staging and checkout with an empty client PATH.
