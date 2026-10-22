use anyhow::{Context, Result, ensure};
use std::{fs, path::PathBuf};

fn standard(name: &str) -> String {
    format!(
        "#!/bin/sh\ncommand -v git-lfs >/dev/null 2>&1 || {{ printf >&2 \"\\n%s\\n\\n\" \"This repository is configured for Git LFS but 'git-lfs' was not found on your path. If you no longer wish to use Git LFS, remove this hook by deleting the '{name}' file in the hooks directory (set by 'core.hookspath'; usually '.git/hooks').\"; exit 2; }}\ngit lfs {name} \"$@\""
    )
}

pub(super) fn directory(repo: &gix::Repository) -> Result<PathBuf> {
    if let Some(path) = repo.config_snapshot().trusted_path("core.hooksPath")? {
        Ok(if path.is_absolute() {
            path
        } else {
            repo.workdir().unwrap_or(repo.git_dir()).join(path)
        })
    } else {
        Ok(repo.common_dir().join("hooks"))
    }
}

pub(crate) fn check(
    repo: &gix::Repository,
    names: &[&str],
    target: Option<&gix::index::State>,
) -> Result<()> {
    let directory = directory(repo)?;
    let mut lfs = false;
    for name in names {
        let path = directory.join(name);
        if !path.exists() {
            continue;
        }
        let metadata = fs::symlink_metadata(&path)?;
        ensure!(
            metadata.is_file() && !metadata.file_type().is_symlink() && metadata.len() < 2048,
            "custom or indirect {name} hooks need native hook execution support"
        );
        let contents = fs::read_to_string(path)?.replace("\r\n", "\n");
        ensure!(
            ["pre-push", "post-checkout", "post-commit", "post-merge"].contains(name)
                && (contents.trim_end() == standard(name)
                    || contents.trim_end() == format!("#!/bin/sh\ngit lfs {name} \"$@\"")),
            "custom {name} hook is not supported; no hook was skipped"
        );
        lfs = true;
    }
    if !lfs {
        return Ok(());
    }
    let config = repo.config_snapshot();
    ensure!(
        !config.boolean("lfs.locksverify").unwrap_or(false),
        "LFS lock verification needs native locking support"
    );
    if config.boolean("lfs.setlockableReadOnly") == Some(false) {
        return Ok(());
    }
    let current = repo.index_or_empty()?;
    let current_index: &gix::index::State = &current;
    for (index, source) in [
        (
            Some(current_index),
            gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
        ),
        (
            target,
            gix_worktree::stack::state::attributes::Source::IdMapping,
        ),
    ] {
        let Some(index) = index else {
            continue;
        };
        let mut stack = repo.attributes_only(index, source)?;
        let mut outcome = gix::attrs::search::Outcome::default();
        outcome.initialize_with_selection(&Default::default(), ["lockable"]);
        for entry in index.entries() {
            stack
                .at_entry(entry.path(index), Some(entry.mode))?
                .matching_attributes(&mut outcome);
            let attribute = outcome
                .iter_selected()
                .next()
                .context("lockable attribute selection")?;
            ensure!(
                !attribute.assignment.state.is_set(),
                "LFS lockable file permissions need native locking support"
            );
        }
    }
    Ok(())
}
