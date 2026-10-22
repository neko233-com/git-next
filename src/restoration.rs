use anyhow::{Context, Result, ensure};
use clap::Args;
use gix::{
    bstr::{BString, ByteSlice},
    refs::transaction::{PreviousValue, RefEdit},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    io::Write,
    path::PathBuf,
};

#[derive(Args)]
pub(crate) struct RestoreOptions {
    #[arg(short = 's', long)]
    source: Option<String>,
    #[arg(short = 'S', long)]
    staged: bool,
    #[arg(short = 'W', long)]
    worktree: bool,
    #[arg(required = true)]
    paths: Vec<String>,
}

#[derive(Args)]
pub(crate) struct ResetOptions {
    #[arg(long, conflicts_with_all = ["mixed", "hard"])]
    soft: bool,
    #[arg(long, conflicts_with = "hard")]
    mixed: bool,
    #[arg(long)]
    hard: bool,
    #[arg(short = 'q', long)]
    quiet: bool,
    revisions: Vec<String>,
    #[arg(last = true)]
    paths: Vec<String>,
}

fn supported_index(index: &gix::index::State) -> Result<()> {
    ensure!(
        !index.is_sparse()
            && index.entries().iter().all(|entry| {
                entry.stage_raw() == 0
                    && !entry
                        .flags
                        .contains(gix::index::entry::Flags::SKIP_WORKTREE)
            }),
        "sparse, conflicted and skip-worktree restoration is not yet supported"
    );
    Ok(())
}

fn source_index(repo: &gix::Repository, revision: &str) -> Result<gix::index::File> {
    let tree = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?
        .peel_to_tree()?
        .id;
    Ok(repo.index_from_tree(&tree)?)
}

fn selected_index(
    repo: &gix::Repository,
    old: &gix::index::State,
    source: &gix::index::State,
    patterns: &[String],
    require_match: bool,
    allow_untracked: bool,
) -> Result<(gix::index::File, BTreeSet<PathBuf>)> {
    supported_index(old)?;
    supported_index(source)?;
    let paths: BTreeSet<BString> = old
        .entries()
        .iter()
        .map(|entry| entry.path(old).to_owned())
        .chain(
            source
                .entries()
                .iter()
                .map(|entry| entry.path(source).to_owned()),
        )
        .collect();
    let attributes = gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping;
    // Check each positive selector so a miss cannot partly apply another valid selector.
    for pattern in patterns {
        if !require_match || (allow_untracked && std::path::Path::new(pattern).exists()) {
            continue;
        }
        let mut selector = repo.pathspec(
            false,
            [pattern.as_bytes().as_bstr()],
            false,
            old,
            attributes,
        )?;
        if selector
            .search()
            .patterns()
            .all(|pattern| pattern.is_excluded())
        {
            continue;
        }
        ensure!(
            paths
                .iter()
                .any(|path| selector.is_included(path.as_bstr(), Some(false))),
            "pathspec did not match a tracked path"
        );
    }
    let mut selector = repo.pathspec(
        false,
        patterns.iter().map(|pattern| pattern.as_bytes().as_bstr()),
        false,
        old,
        attributes,
    )?;
    let selected: BTreeSet<_> = paths
        .into_iter()
        .filter(|path| selector.is_included(path.as_bstr(), Some(false)))
        .collect();
    let mut state = gix::index::State::new(repo.object_hash());
    for (index, restoring) in [(old, false), (source, true)] {
        for entry in index.entries() {
            let path = entry.path(index);
            if selected.contains(path) == restoring {
                state.dangerously_push_entry(
                    if restoring {
                        Default::default()
                    } else {
                        entry.stat
                    },
                    entry.id,
                    entry.flags,
                    entry.mode,
                    path,
                );
            }
        }
    }
    state.sort_entries();
    let selected = selected
        .into_iter()
        .map(|path| Ok(PathBuf::from(path.to_str()?)))
        .collect::<Result<_>>()?;
    Ok((
        gix::index::File::from_state(state, repo.index_path()),
        selected,
    ))
}

pub(crate) fn restore(options: RestoreOptions) -> Result<()> {
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    crate::write::safe_state(&repo)?;
    ensure!(repo.workdir().is_some(), "restore requires a working tree");
    let lock = crate::write::IndexLock::acquire(&repo)?;
    let old = repo.index_or_empty()?;
    let source = match options
        .source
        .as_deref()
        .or(options.staged.then_some("HEAD"))
    {
        Some(revision) => source_index(&repo, revision)?,
        None => gix::index::File::clone(&old),
    };
    let (index, paths) = selected_index(&repo, &old, &source, &options.paths, true, false)?;
    if options.worktree || !options.staged {
        crate::lfs::check_hooks(&repo, &["post-checkout"], Some(&index))?;
        crate::worktree::apply(
            &repo,
            &old,
            lock,
            crate::worktree::Update {
                index,
                paths: Some(paths),
                edits: Vec::new(),
                target: None,
                require_clean: false,
                publish_index: true,
                preserve_index: !options.staged,
                overwrite_untracked: true,
                worktree_attributes: true,
                expected_head: None,
                file_views: None,
                metadata: Vec::new(),
                allow_merge_state: false,
            },
        )
    } else {
        lock.persist(&index, &repo.index_path())
    }
}

pub(crate) fn reset(mut options: ResetOptions) -> Result<()> {
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    crate::write::safe_state(&repo)?;
    let explicit_paths = std::env::args_os().any(|arg| arg == "--");
    let unborn = repo.head()?.try_peel_to_id()?.is_none();
    let mut revision = "HEAD".to_owned();
    if let Some(first) = options.revisions.first() {
        if repo.rev_parse_single(first.as_bytes().as_bstr()).is_ok()
            || (explicit_paths && unborn && first == "HEAD")
        {
            ensure!(
                explicit_paths || !std::path::Path::new(first).exists(),
                "ambiguous reset argument names both a revision and a path; use --"
            );
            revision = options.revisions.remove(0);
        } else {
            ensure!(!explicit_paths, "unknown reset revision");
        }
    }
    options.paths.splice(0..0, options.revisions);
    if !options.paths.is_empty() {
        ensure!(
            !options.soft && !options.hard,
            "path reset cannot use --soft or --hard"
        );
        ensure!(
            repo.workdir().is_some(),
            "path reset requires a working tree"
        );
        let lock = crate::write::IndexLock::acquire(&repo)?;
        let old = repo.index_or_empty()?;
        let source = if unborn && revision == "HEAD" {
            empty_index(&repo)
        } else {
            source_index(&repo, &revision)?
        };
        let (index, _) =
            selected_index(&repo, &old, &source, &options.paths, !explicit_paths, true)?;
        let summary = reset_summary(&repo, &index, options.quiet)?;
        lock.persist(&index, &repo.index_path())?;
        std::io::stdout().lock().write_all(&summary)?;
        return Ok(());
    }
    ensure!(
        options.soft || repo.workdir().is_some(),
        "mixed and hard reset require a working tree"
    );
    ensure!(
        repo.git_dir().canonicalize()? == repo.common_dir().canonicalize()?
            && !repo.common_dir().join("worktrees").exists(),
        "reset with linked worktrees is not yet supported"
    );
    if unborn && revision == "HEAD" {
        if options.soft {
            return Ok(());
        }
        let lock = crate::write::IndexLock::acquire(&repo)?;
        let old_index = repo.index_or_empty()?;
        supported_index(&old_index)?;
        let index = empty_index(&repo);
        return if options.hard {
            crate::worktree::apply(
                &repo,
                &old_index,
                lock,
                crate::worktree::Update {
                    index,
                    paths: Some(crate::worktree::files(&old_index)?.into_keys().collect()),
                    edits: Vec::new(),
                    target: None,
                    require_clean: false,
                    publish_index: true,
                    preserve_index: false,
                    overwrite_untracked: true,
                    worktree_attributes: false,
                    expected_head: None,
                    file_views: None,
                    metadata: Vec::new(),
                    allow_merge_state: false,
                },
            )
        } else {
            lock.persist(&index, &repo.index_path())
        };
    }
    let target = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    let old = repo.head()?.try_peel_to_id()?.map(|id| id.detach());
    let expected_head: gix::refs::Target = repo.find_reference("HEAD")?.target().into();
    let mut head = RefEdit::update(
        "HEAD".try_into()?,
        target,
        old.map_or(PreviousValue::MustNotExist, |id| {
            PreviousValue::MustExistAndMatch(id.into())
        }),
        format!("reset: moving to {revision}"),
    );
    head.deref = true;
    if let gix::refs::transaction::Change::Update { log, .. } = &mut head.change {
        log.force_write_reflog = true;
    }
    let mut edits = vec![head];
    if let Some(old) = old {
        let original = repo.try_find_reference("ORIG_HEAD")?;
        let expected = original.map_or(PreviousValue::MustNotExist, |reference| {
            PreviousValue::MustExistAndMatch(reference.target().into())
        });
        edits.push(RefEdit::update(
            "ORIG_HEAD".try_into()?,
            old,
            expected,
            "reset: save previous HEAD",
        ));
    }
    if options.soft {
        let transaction = repo.refs.transaction().prepare(
            edits,
            gix::lock::acquire::Fail::Immediately,
            gix::lock::acquire::Fail::Immediately,
        )?;
        ensure!(
            expected_head == repo.find_reference("HEAD")?.target(),
            "HEAD changed while preparing reset"
        );
        transaction.commit(repo.committer().transpose()?)?;
        return Ok(());
    }
    let lock = crate::write::IndexLock::acquire(&repo)?;
    let old_index = repo.index_or_empty()?;
    supported_index(&old_index)?;
    let index = source_index(&repo, &target.to_string())?;
    if options.hard {
        let paths = crate::worktree::files(&old_index)?
            .into_keys()
            .chain(crate::worktree::files(&index)?.into_keys())
            .collect();
        crate::worktree::apply(
            &repo,
            &old_index,
            lock,
            crate::worktree::Update {
                index,
                paths: Some(paths),
                edits,
                target: Some(target),
                require_clean: false,
                publish_index: true,
                preserve_index: false,
                overwrite_untracked: true,
                worktree_attributes: false,
                expected_head: Some(expected_head),
                file_views: None,
                metadata: Vec::new(),
                allow_merge_state: false,
            },
        )?;
        if !options.quiet {
            println!(
                "{}",
                crate::language::format(
                    "HEAD is now at {id} {subject}",
                    &[
                        ("id", repo.find_commit(target)?.short_id()?.to_string()),
                        (
                            "subject",
                            repo.find_commit(target)?.message()?.title.to_string()
                        ),
                    ]
                )
            );
        }
    } else {
        let transaction = repo.refs.transaction().prepare(
            edits,
            gix::lock::acquire::Fail::Immediately,
            gix::lock::acquire::Fail::Immediately,
        )?;
        ensure!(
            expected_head == repo.find_reference("HEAD")?.target(),
            "HEAD changed while preparing reset"
        );
        let committer = repo.committer().transpose()?;
        let summary = reset_summary(&repo, &index, options.quiet)?;
        // A published journal also blocks later writers after interrupted index/ref publication.
        let previous = tempfile::Builder::new()
            .prefix("git-next-checkout-")
            .tempdir_in(repo.git_dir())?;
        if repo.index_path().exists() {
            std::fs::copy(repo.index_path(), previous.path().join("old-index"))?;
        }
        let journal = std::fs::File::create(previous.path().join("journal.json"))?;
        serde_json::to_writer_pretty(
            &journal,
            &serde_json::json!({
                "schema_version": 1, "operation": "mixed-reset", "target": target.to_string(),
                "old_head": old.map(|id| id.to_string()), "paths": [],
                "recovery": "Keep old-index and journal; inspect HEAD and ORIG_HEAD before restoring the index.",
            }),
        )?;
        journal.sync_all()?;
        let recovery = previous.keep();
        lock.persist(&index, &repo.index_path()).with_context(|| {
            format!(
                "reset index publication failed; recovery: {}",
                recovery.display()
            )
        })?;
        transaction.commit(committer).with_context(|| {
            format!(
                "reset ref publication failed; recovery: {}",
                recovery.display()
            )
        })?;
        std::fs::remove_file(recovery.join("journal.json"))?;
        std::fs::remove_dir_all(recovery)?;
        std::io::stdout().lock().write_all(&summary)?;
    }
    Ok(())
}

fn empty_index(repo: &gix::Repository) -> gix::index::File {
    gix::index::File::from_state(
        gix::index::State::new(repo.object_hash()),
        repo.index_path(),
    )
}

fn reset_summary(repo: &gix::Repository, index: &gix::index::File, quiet: bool) -> Result<Vec<u8>> {
    if quiet {
        return Ok(Vec::new());
    }
    let mut paths = BTreeMap::new();
    for change in repo
        .status(gix::progress::Discard)?
        .index(index.clone().into())
        .untracked_files(gix::status::UntrackedFiles::None)
        .index_worktree_rewrites(None)
        .into_index_worktree_iter(std::iter::empty())?
    {
        let change = change?;
        use gix::status::plumbing::index_as_worktree_with_renames::Summary;
        let status = match change.summary() {
            Some(Summary::Modified) => 'M',
            Some(Summary::Removed) => 'D',
            Some(Summary::TypeChange) => 'T',
            Some(Summary::IntentToAdd) => 'A',
            None | Some(Summary::Added) => continue,
            _ => anyhow::bail!("unsupported reset refresh status"),
        };
        paths.insert(change.rela_path().to_owned(), status);
    }
    let mut out = Vec::new();
    let quoting = repo
        .config_snapshot()
        .boolean("core.quotePath")
        .unwrap_or(true);
    if !paths.is_empty() {
        out.extend_from_slice(b"Unstaged changes after reset:\n");
        for (path, status) in paths {
            write!(&mut out, "{status}\t")?;
            out.extend_from_slice(&crate::difference::quote(path.as_slice(), quoting));
            out.push(b'\n');
        }
    }
    Ok(out)
}
