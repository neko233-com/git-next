# git-next reflog integration patch

Upstream: gix-ref 0.69.0, MIT OR Apache-2.0; retain upstream license files.

`git reset HEAD` records a HEAD reset entry even when HEAD already resolves to that object; the unchanged branch receives no new entry. Add the opt-in `LogChange::force_write_reflog`, default false, retaining it on the explicitly named reference during symbolic dereferencing. The referent keeps the normal changed-ID rule. Transactions still acquire the normal reference locks and retain the repository's existing log creation/disable policy. Other callers keep the previous unchanged-ID behavior. Three upstream dead-code expectations become allowances with their original reasons retained, avoiding unfulfilled-expectation warnings in this vendored build.

Scope and compatibility: this is an additive internal Rust API field. On-disk references, lock paths and reflog bytes use the existing Git layout; no data migration is required. Rollback uses the previous binary and existing reflogs. Reset acceptance compares HEAD/branch reflog entries and ORIG_HEAD against reference Git, including unchanged-ID reset and disabled log creation.
