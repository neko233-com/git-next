use anyhow::{Context, Result, bail, ensure};
use gix::{
    bstr::ByteSlice,
    refs::transaction::{PreviousValue, RefEdit},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Write,
    path::{Component, Path, PathBuf},
};

pub fn has_pending_checkout(repo: &gix::Repository) -> Result<bool> {
    for item in fs::read_dir(repo.git_dir())? {
        let item = item?;
        if item
            .file_name()
            .to_string_lossy()
            .starts_with("git-next-checkout-")
            && item.path().join("journal.json").exists()
        {
            return Ok(true);
        }
    }
    Ok(false)
}

fn branch_name(name: &str) -> Result<gix::refs::FullName> {
    ensure!(
        !name.starts_with('-') && name != "HEAD",
        "invalid branch name"
    );
    Ok(format!("refs/heads/{name}").try_into()?)
}

pub fn switch(target: Option<&str>, create: Option<&str>, detach: bool) -> Result<()> {
    ensure!(
        !(create.is_some() && detach),
        "--create and --detach conflict"
    );
    ensure!(
        target.is_some() || create.is_some(),
        "specify a branch or --create"
    );
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    let revision = target.unwrap_or("HEAD");
    let id = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    let old_head: gix::refs::Target = repo.find_reference("HEAD")?.target().into();
    let mut edits = Vec::new();
    let new_head = if detach {
        gix::refs::Target::Object(id)
    } else {
        let name = branch_name(create.unwrap_or(revision))?;
        if create.is_some() {
            edits.push(RefEdit::update(
                name.clone(),
                id,
                PreviousValue::MustNotExist,
                "branch: git-next switch",
            ));
        } else {
            let mut reference = repo
                .find_reference(name.as_ref())
                .context("switch requires an existing local branch; use --create or --detach")?;
            ensure!(
                reference.follow_to_object()?.detach() == id,
                "branch changed during switch"
            );
            edits.push(RefEdit::update(
                name.clone(),
                id,
                PreviousValue::MustExistAndMatch(id.into()),
                "switch: git-next",
            ));
        }
        gix::refs::Target::Symbolic(name)
    };
    edits.push(RefEdit::update(
        "HEAD".try_into()?,
        new_head,
        PreviousValue::MustExistAndMatch(old_head),
        "checkout: git-next",
    ));
    checkout(&repo, id, edits)?;
    println!(
        "{}",
        crate::language::format(
            "Switched to {target}",
            &[("target", create.unwrap_or(revision).to_owned())]
        )
    );
    Ok(())
}

pub fn is_ancestor(
    repo: &gix::Repository,
    ancestor: gix::ObjectId,
    descendant: gix::ObjectId,
) -> Result<bool> {
    for entry in repo.find_commit(descendant)?.id().ancestors().all()? {
        if entry?.id == ancestor {
            return Ok(true);
        }
    }
    Ok(false)
}

pub fn merge_ff(revision: &str) -> Result<()> {
    let repo = crate::repository::open()?;
    let next = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    fast_forward(&repo, next)
}

pub fn fast_forward(repo: &gix::Repository, next: gix::ObjectId) -> Result<()> {
    let prepared = crate::lfs::prepare(repo.clone())?;
    let repo = &prepared;
    crate::write::safe_state(repo)?;
    let head = repo
        .head_name()?
        .context("fast-forward requires a local branch")?;
    let old = repo.head_id()?.detach();
    if old == next || is_ancestor(repo, next, old)? {
        println!("{}", crate::language::text("Already up to date."));
        return Ok(());
    }
    ensure!(
        is_ancestor(repo, old, next)?,
        "branches diverged; only fast-forward is supported"
    );
    checkout(
        repo,
        next,
        vec![
            RefEdit::update(
                head.clone(),
                next,
                PreviousValue::MustExistAndMatch(old.into()),
                "merge: fast-forward by git-next",
            ),
            RefEdit::update(
                "HEAD".try_into()?,
                gix::refs::Target::Symbolic(head.clone()),
                PreviousValue::MustExistAndMatch(gix::refs::Target::Symbolic(head)),
                "merge: fast-forward by git-next",
            ),
        ],
    )?;
    println!(
        "{}",
        crate::language::format(
            "Fast-forward {old}..{next}",
            &[("old", old.to_string()), ("next", next.to_string())]
        )
    );
    Ok(())
}

type Files = BTreeMap<PathBuf, (gix::ObjectId, gix::index::entry::Mode)>;

pub(crate) fn files(index: &gix::index::File) -> Result<Files> {
    ensure!(!index.is_sparse(), "sparse checkout is not supported");
    let mut result = BTreeMap::new();
    let mut names = BTreeSet::new();
    for entry in index.entries() {
        ensure!(
            entry.stage_raw() == 0
                && !entry
                    .flags
                    .contains(gix::index::entry::Flags::SKIP_WORKTREE)
                && matches!(
                    entry.mode,
                    gix::index::entry::Mode::FILE | gix::index::entry::Mode::FILE_EXECUTABLE
                ),
            "checkout supports ordinary files only; conflicts, skip-worktree paths, symlinks and submodules are refused"
        );
        let path = PathBuf::from(entry.path(index).to_str()?);
        ensure!(
            path.components().all(|part| match part {
                Component::Normal(name) => {
                    let name = name.to_string_lossy();
                    !name.eq_ignore_ascii_case(".git")
                        && !name.contains(':')
                        && !name.ends_with(['.', ' '])
                }
                _ => false,
            }),
            "unsafe checkout path"
        );
        if cfg!(windows) {
            ensure!(
                names.insert(path.to_string_lossy().to_lowercase()),
                "case-colliding checkout paths"
            );
        }
        result.insert(path, (entry.id, entry.mode));
    }
    Ok(result)
}

fn reject_link(path: &Path) -> Result<()> {
    if let Ok(metadata) = fs::symlink_metadata(path) {
        ensure!(
            !metadata.file_type().is_symlink(),
            "checkout cannot traverse a symlink"
        );
        #[cfg(windows)]
        {
            use std::os::windows::fs::MetadataExt;
            ensure!(
                metadata.file_attributes() & 0x400 == 0,
                "checkout cannot traverse a reparse point"
            );
        }
    }
    Ok(())
}

pub(crate) fn clean(repo: &gix::Repository) -> Result<()> {
    let threads = crate::repository::status_threads(repo)?;
    for item in repo
        .status(gix::progress::Discard)?
        .index_worktree_options_mut(|options| options.thread_limit = Some(threads))
        .into_iter(std::iter::empty())?
    {
        match item? {
            gix::status::Item::IndexWorktree(change)
                if change.summary()
                    == Some(
                        gix::status::plumbing::index_as_worktree_with_renames::Summary::Added,
                    ) => {}
            _ => bail!("working tree or index has changes; checkout refused"),
        }
    }
    Ok(())
}

fn checkout(repo: &gix::Repository, target: gix::ObjectId, edits: Vec<RefEdit>) -> Result<()> {
    crate::write::safe_state(repo)?;
    let lock = crate::write::IndexLock::acquire(repo)?;
    clean(repo)?;
    let old_index = repo.index_or_empty()?;
    let tree = repo.find_commit(target)?.tree_id()?;
    let index = repo.index_from_tree(&tree)?;
    crate::lfs::check_hooks(repo, &["post-checkout", "post-merge"], Some(&index))?;
    apply(
        repo,
        &old_index,
        lock,
        Update {
            index,
            paths: None,
            edits,
            target: Some(target),
            require_clean: true,
            publish_index: true,
            preserve_index: false,
            overwrite_untracked: false,
            worktree_attributes: false,
            expected_head: None,
            file_views: None,
            metadata: Vec::new(),
            allow_merge_state: false,
            allow_sequencer_state: false,
        },
    )
}

pub(crate) struct Update {
    pub index: gix::index::File,
    pub paths: Option<BTreeSet<PathBuf>>,
    pub edits: Vec<RefEdit>,
    pub target: Option<gix::ObjectId>,
    pub require_clean: bool,
    pub publish_index: bool,
    pub preserve_index: bool,
    pub overwrite_untracked: bool,
    pub worktree_attributes: bool,
    pub expected_head: Option<gix::refs::Target>,
    pub file_views: Option<(Files, Files)>,
    pub metadata: Vec<crate::metadata::Change>,
    pub allow_merge_state: bool,
    pub allow_sequencer_state: bool,
}

fn snapshot_attributes(
    repo: &gix::Repository,
    root: &Path,
    destination: &Path,
    paths: &BTreeSet<PathBuf>,
    files: &Files,
) -> Result<()> {
    let mut attributes = BTreeSet::new();
    for path in paths.iter().filter(|path| files.contains_key(*path)) {
        let mut directory = path.parent();
        while let Some(parent) = directory {
            attributes.insert(parent.join(".gitattributes"));
            directory = parent.parent();
        }
    }
    for path in attributes {
        let data = if paths.contains(&path) {
            files
                .get(&path)
                .map(|(id, _)| {
                    repo.find_object(*id)?
                        .try_into_blob()
                        .map(|mut blob| std::mem::take(&mut blob.data))
                })
                .transpose()?
        } else {
            let source = root.join(&path);
            reject_link(&source)?;
            match fs::read(&source) {
                Ok(data) => Some(data),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
                Err(error) => return Err(error.into()),
            }
        };
        if let Some(data) = data {
            let target = destination.join(path);
            fs::create_dir_all(target.parent().context("invalid attribute snapshot path")?)?;
            fs::write(target, data)?;
        }
    }
    Ok(())
}

pub(crate) fn apply(
    repo: &gix::Repository,
    old_index: &gix::index::File,
    lock: crate::write::IndexLock,
    update: Update,
) -> Result<()> {
    if update.allow_merge_state {
        crate::write::safe_merge_state(repo)?;
    } else if update.allow_sequencer_state {
        crate::write::safe_sequencer_state(repo)?;
    } else {
        crate::write::safe_state(repo)?;
    }
    ensure!(
        repo.git_dir().canonicalize()? == repo.common_dir().canonicalize()?,
        "linked worktree checkout is not yet supported"
    );
    ensure!(
        !repo.common_dir().join("worktrees").exists(),
        "repositories with linked worktrees are not supported"
    );
    let root = repo
        .workdir()
        .context("checkout needs a working tree")?
        .canonicalize()?;
    let (old_files, new_files) = match update.file_views {
        Some(views) => views,
        None => (files(old_index)?, files(&update.index)?),
    };
    let index = update.index;
    let paths = update.paths.unwrap_or_else(|| {
        old_files
            .keys()
            .chain(new_files.keys())
            .filter(|path| old_files.get(*path) != new_files.get(*path))
            .cloned()
            .collect()
    });
    for path in &paths {
        let absolute = root.join(path);
        let mut parent = absolute.parent();
        while let Some(path) = parent {
            reject_link(path)?;
            if path == root {
                break;
            }
            parent = path.parent();
        }
        reject_link(&absolute)?;
        if old_files.contains_key(path) && update.require_clean {
            ensure!(
                absolute.is_file(),
                "tracked checkout path is not a regular file"
            );
        } else if !update.overwrite_untracked {
            ensure!(
                old_files.contains_key(path) || !absolute.exists(),
                "untracked or ignored file would be overwritten"
            );
        } else {
            ensure!(
                !absolute.exists() || absolute.is_file(),
                "restore cannot replace a directory or an indirect path"
            );
        }
    }
    // Reference locks are held before changing the index or any tracked file.
    let transaction = if update.edits.is_empty() {
        None
    } else {
        Some(repo.refs.transaction().prepare(
            update.edits,
            gix::lock::acquire::Fail::Immediately,
            gix::lock::acquire::Fail::Immediately,
        )?)
    };
    if let Some(expected) = &update.expected_head {
        ensure!(
            *expected == repo.find_reference("HEAD")?.target(),
            "HEAD changed while preparing the worktree update"
        );
    }
    let mut metadata = update
        .metadata
        .into_iter()
        .map(|change| crate::metadata::Lock::acquire(repo, change))
        .collect::<Result<Vec<_>>>()?;
    let committer = if transaction.is_some() {
        repo.committer().transpose()?
    } else {
        None
    };
    let temporary = tempfile::Builder::new()
        .prefix("git-next-checkout-")
        .tempdir_in(repo.git_dir())?;
    let staged = temporary.path().join("staged");
    fs::create_dir(&staged)?;
    if !paths.is_empty() {
        let mut options = repo.checkout_options(if update.worktree_attributes {
            gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping
        } else {
            gix_worktree::stack::state::attributes::Source::IdMapping
        })?;
        let attribute_root = temporary.path().join("attributes");
        fs::create_dir(&attribute_root)?;
        if update.worktree_attributes {
            snapshot_attributes(repo, &root, &attribute_root, &paths, &new_files)?;
        }
        let mut attributes = gix_worktree::Stack::from_state_and_ignore_case(
            attribute_root,
            options.fs.ignore_case,
            gix_worktree::stack::State::AttributesStack(std::mem::take(&mut options.attributes)),
            &index,
            index.path_backing(),
        );
        for path in paths.iter().filter(|path| new_files.contains_key(*path)) {
            let (id, mode) = new_files
                .get(path)
                .context("missing selected checkout file")?;
            let object = repo.find_object(*id)?.try_into_blob()?;
            let relative = gix::path::to_unix_separators_on_windows(gix::path::into_bstr(path)?);
            let platform = attributes.at_entry(relative.as_ref(), None, &repo.objects)?;
            if path
                .file_name()
                .is_some_and(|name| name == ".gitattributes")
            {
                let mut assignments = gix::attrs::search::Outcome::default();
                assignments.initialize_with_selection(
                    &Default::default(),
                    ["filter", "working-tree-encoding", "ident"],
                );
                platform.matching_attributes(&mut assignments);
                ensure!(
                    assignments.iter_selected().all(|attribute| matches!(
                        attribute.assignment.state,
                        gix::attrs::StateRef::Unspecified | gix::attrs::StateRef::Unset
                    )),
                    "transformed .gitattributes files are not yet supported"
                );
            }
            let mut filtered = options.filters.convert_to_worktree(
                &object.data,
                relative.as_ref(),
                &mut |_, outcome| {
                    platform.matching_attributes(outcome);
                },
                gix::filter::plumbing::pipeline::convert::to_worktree::Options {
                    can_delay: gix::filter::plumbing::driver::apply::Delay::Forbid,
                    ..Default::default()
                },
            )?;
            let destination = staged.join(path);
            fs::create_dir_all(destination.parent().context("invalid staging path")?)?;
            let mut output = fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&destination)?;
            std::io::copy(&mut filtered, &mut output)?;
            output.flush()?;
            #[cfg(unix)]
            if options.fs.executable_bit && *mode == gix::index::entry::Mode::FILE_EXECUTABLE {
                use std::os::unix::fs::PermissionsExt;
                let permissions = output.metadata()?.permissions().mode() | 0o111;
                output.set_permissions(fs::Permissions::from_mode(permissions))?;
            }
            #[cfg(not(unix))]
            let _ = mode;
        }
    }
    let mut index = if update.preserve_index {
        gix::index::File::clone(old_index)
    } else {
        index
    };
    // Capture staging metadata before exposing files to concurrent worktree writers.
    for (entry, path) in index.entries_mut_with_paths() {
        let native = gix::path::from_bstr(path)?;
        entry.stat = if paths.contains(native.as_ref()) {
            if new_files.get(native.as_ref()) == Some(&(entry.id, entry.mode)) {
                gix::index::entry::Stat::from_fs(&gix::index::fs::Metadata::from_path_no_follow(
                    &staged.join(native.as_ref()),
                )?)?
            } else {
                Default::default()
            }
        } else {
            old_index
                .entry_by_path(path)
                .filter(|old| old.id == entry.id && old.mode == entry.mode)
                .map(|old| old.stat)
                .unwrap_or_default()
        };
    }
    let backup = temporary.path().join("backup");
    fs::create_dir(&backup)?;
    if repo.index_path().exists() {
        fs::copy(repo.index_path(), temporary.path().join("old-index"))?;
    }
    let metadata_backup = temporary.path().join("metadata");
    if !metadata.is_empty() {
        fs::create_dir(&metadata_backup)?;
    }
    let metadata_journal = metadata
        .iter()
        .map(|item| item.backup(&metadata_backup))
        .collect::<Result<Vec<_>>>()?;
    let journal = serde_json::json!({
        "schema_version": 1, "target": update.target.map(|id| id.to_string()),
        "old_head": repo.head()?.try_peel_to_id()?.map(|id| id.to_string()),
        "paths": paths.iter().map(|path| path.to_string_lossy()).collect::<Vec<_>>(),
        "metadata": metadata_journal,
        "recovery": "Keep backup, old-index and journal; inspect HEAD and refs before restoring files.",
    });
    let journal_path = temporary.path().join("journal.json");
    let journal_file = fs::File::create(&journal_path)?;
    serde_json::to_writer_pretty(&journal_file, &journal)?;
    journal_file.sync_all()?;
    let mut moved = Vec::new();
    let mut installed = Vec::new();
    let mut directories = Vec::new();
    let changed = (|| -> Result<()> {
        if update.require_clean {
            clean(repo)?;
        }
        if let Some(objects) = repo.objects.reset_object_memory() {
            for (expected, (kind, data)) in objects.iter() {
                let actual = gix::objs::Write::write_buf(&*repo.objects, *kind, data)?;
                ensure!(
                    actual == *expected,
                    "candidate object changed before merge publication"
                );
            }
        }
        for path in &paths {
            let destination = root.join(path);
            if (old_files.contains_key(path) || update.overwrite_untracked) && destination.is_file()
            {
                let saved = backup.join(path);
                fs::create_dir_all(saved.parent().context("invalid backup path")?)?;
                fs::rename(&destination, saved)?;
                moved.push(path.clone());
            }
        }
        for path in paths.iter().filter(|path| new_files.contains_key(*path)) {
            let destination = root.join(path);
            let mut missing = Vec::new();
            let mut parent = destination.parent();
            while let Some(path) = parent {
                if path.exists() {
                    break;
                }
                missing.push(path.to_owned());
                parent = path.parent();
            }
            for directory in missing.into_iter().rev() {
                fs::create_dir(&directory)?;
                directories.push(directory);
            }
            // A no-clobber publish also protects files created after preflight.
            let staged_path = staged.join(path);
            fs::hard_link(&staged_path, &destination)?;
            installed.push(path.clone());
        }
        Ok(())
    })();
    if let Err(error) = changed {
        let rollback = (|| -> Result<()> {
            for path in installed.iter().rev() {
                fs::remove_file(root.join(path))?;
            }
            for directory in directories.iter().rev() {
                fs::remove_dir(directory)?;
            }
            for path in moved.iter().rev() {
                fs::rename(backup.join(path), root.join(path))?;
            }
            Ok(())
        })();
        if let Err(rollback_error) = rollback {
            let recovery = temporary.keep();
            bail!(
                "{error:#}; rollback incomplete ({rollback_error:#}); backups retained at {}",
                recovery.display()
            );
        }
        return Err(error).context("checkout rolled back before publishing index or refs");
    }
    // Keep recovery bytes if index/ref publication fails or the process is interrupted.
    let recovery = temporary.keep();
    for item in &mut metadata {
        item.publish().with_context(|| {
            format!(
                "merge metadata publication failed; recovery: {}",
                recovery.display()
            )
        })?;
    }
    if update.publish_index {
        lock.persist(&index, &repo.index_path()).with_context(|| {
            format!("index publication failed; recovery: {}", recovery.display())
        })?;
    }
    if let Some(transaction) = transaction {
        transaction
            .commit(committer)
            .with_context(|| format!("ref publication failed; recovery: {}", recovery.display()))?;
    }
    fs::remove_file(recovery.join("journal.json"))?;
    fs::remove_dir_all(&recovery).context("checkout complete; temporary cleanup failed")?;
    Ok(())
}
