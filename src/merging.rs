use anyhow::{Context, Result, ensure};
use clap::Args;
use gix::{
    bstr::ByteSlice,
    refs::transaction::{PreviousValue, RefEdit},
};

#[derive(Args)]
pub(crate) struct Options {
    #[arg(long, group = "merge_ff_mode")]
    ff_only: bool,
    #[arg(long, group = "merge_ff_mode")]
    no_ff: bool,
    #[arg(long, group = "merge_ff_mode")]
    ff: bool,
    #[arg(short = 'm', long)]
    message: Option<String>,
    #[arg(long, conflicts_with_all = ["abort", "continue_merge"])]
    no_edit: bool,
    #[arg(short = 'q', long)]
    quiet: bool,
    #[arg(long, conflicts_with_all = ["abort", "continue_merge"])]
    cleanup: Option<String>,
    #[arg(long, conflicts_with_all = ["abort", "continue_merge"])]
    no_commit: bool,
    #[arg(long, group = "merge_action", conflicts_with_all = ["revision", "message", "ff", "ff_only", "no_ff"])]
    abort: bool,
    #[arg(long = "continue", group = "merge_action", conflicts_with_all = ["revision", "message", "ff", "ff_only", "no_ff"])]
    continue_merge: bool,
    revision: Option<String>,
}

pub(crate) struct State {
    pub peer: gix::ObjectId,
    pub message: String,
}

#[derive(serde::Serialize, serde::Deserialize)]
struct NativeState {
    schema_version: u32,
    original_head: String,
    peer_head: String,
    materialized_tree: String,
    paths: Vec<String>,
}

impl NativeState {
    fn validate(&self, repo: &gix::Repository, peer: gix::ObjectId) -> Result<()> {
        ensure!(
            self.schema_version == 1
                && gix::ObjectId::from_hex(self.original_head.as_bytes())?
                    == repo.head_id()?.detach()
                && gix::ObjectId::from_hex(self.peer_head.as_bytes())? == peer,
            "native merge state no longer matches HEAD and MERGE_HEAD"
        );
        Ok(())
    }
}

pub(crate) fn read_state(repo: &gix::Repository) -> Result<Option<State>> {
    let Some(bytes) = crate::metadata::read(repo, "MERGE_HEAD", 1024)? else {
        return Ok(None);
    };
    let raw_head = bytes.strip_suffix(b"\n").unwrap_or(&bytes);
    ensure!(
        !raw_head.contains(&b'\n'),
        "only a single pending merge head is currently supported"
    );
    let peer = gix::ObjectId::from_hex(raw_head)?;
    ensure!(
        peer.kind() == repo.object_hash(),
        "merge head object format differs from the repository"
    );
    repo.find_commit(peer)?;
    for name in ["MERGE_AUTOSTASH", "MERGE_RR"] {
        ensure!(
            !repo.git_dir().join(name).exists(),
            "merge recovery involving {name} is not yet supported"
        );
    }
    if let Some(mode) = crate::metadata::read(repo, "MERGE_MODE", 64)? {
        ensure!(
            mode.is_empty() || mode == b"no-ff",
            "unsupported pending merge mode"
        );
    }
    let message = String::from_utf8(
        crate::metadata::read(repo, "MERGE_MSG", 16 * 1024 * 1024)?
            .context("merge message is missing")?,
    )?;
    if let Some(bytes) = crate::metadata::read(repo, "git-next-merge.json", 16 * 1024 * 1024)? {
        let native: NativeState = serde_json::from_slice(&bytes)?;
        native.validate(repo, peer)?;
    }
    Ok(Some(State { peer, message }))
}

pub(crate) fn continue_merge() -> Result<()> {
    let repo = crate::repository::open()?;
    crate::write::working_tree(&repo)?;
    crate::write::safe_merge_state(&repo)?;
    crate::write::commit_pending(false, None)
}

fn abort_merge() -> Result<()> {
    let repo = crate::lfs::prepare(crate::repository::open()?)?.with_object_memory();
    let root = crate::write::working_tree(&repo)?;
    crate::write::safe_merge_state(&repo)?;
    let pending = read_state(&repo)?.context("there is no merge to abort")?;
    let bytes = crate::metadata::read(&repo, "git-next-merge.json", 16 * 1024 * 1024)?
        .context("safe native abort needs its original merge snapshot")?;
    let state: NativeState = serde_json::from_slice(&bytes)?;
    let old = repo.head_id()?.detach();
    state.validate(&repo, pending.peer)?;
    let materialized_tree = gix::ObjectId::from_hex(state.materialized_tree.as_bytes())?;
    let materialized = repo.index_from_tree(&materialized_tree)?;
    let original_tree = repo.find_commit(old)?.tree_id()?;
    let original = repo.index_from_tree(&original_tree)?;
    let mut old_files = crate::worktree::files(&materialized)?;
    let original_files = crate::worktree::files(&original)?;
    let mut paths = state
        .paths
        .into_iter()
        .map(std::path::PathBuf::from)
        .collect::<std::collections::BTreeSet<_>>();
    ensure!(
        paths
            .iter()
            .all(|path| old_files.contains_key(path) || original_files.contains_key(path)),
        "native merge snapshot contains an unknown path"
    );
    let lock = crate::write::IndexLock::acquire(&repo)?;
    let index = repo.index_or_empty()?;
    ensure!(
        !index.is_sparse()
            && index.entries().iter().all(|entry| !entry
                .flags
                .contains(gix::index::entry::Flags::SKIP_WORKTREE)),
        "sparse/skip-worktree merge abort is not yet supported"
    );
    for entry in index
        .entries()
        .iter()
        .filter(|entry| entry.stage_raw() != 0)
    {
        ensure!(
            paths.contains(std::path::Path::new(entry.path(&index).to_str()?)),
            "an unmerged path is outside the original merge snapshot"
        );
    }
    let mut indexed_files = std::collections::BTreeMap::new();
    for entry in index
        .entries()
        .iter()
        .filter(|entry| entry.stage_raw() == 0)
    {
        ensure!(
            matches!(
                entry.mode,
                gix::index::entry::Mode::FILE | gix::index::entry::Mode::FILE_EXECUTABLE
            ),
            "merge abort requires ordinary indexed files"
        );
        let path = std::path::PathBuf::from(entry.path(&index).to_str()?);
        indexed_files.insert(path.clone(), (entry.id, entry.mode));
        old_files.insert(path, (entry.id, entry.mode));
    }
    paths.extend(
        indexed_files
            .keys()
            .chain(original_files.keys())
            .filter(|path| indexed_files.get(*path) != original_files.get(*path))
            .cloned(),
    );
    let (mut filters, _) = repo.filter_pipeline(None)?;
    let filemode = repo
        .config_snapshot()
        .try_boolean("core.filemode")?
        .unwrap_or(cfg!(unix));
    let mut tracked_paths = std::collections::BTreeSet::new();
    for path in &paths {
        let key = gix::path::to_unix_separators_on_windows(gix::path::into_bstr(path)?);
        let tracked = index.entry_index_by_path(key.as_ref()).is_ok();
        if tracked {
            tracked_paths.insert(path.clone());
        }
        ensure!(
            tracked || !original_files.contains_key(path) || !root.join(path).exists(),
            "merge abort would overwrite a newly untracked file"
        );
        if let Some(entry) =
            index.entry_by_path_and_stage(key.as_ref(), gix::index::entry::Stage::Unconflicted)
        {
            let current = filters.worktree_file_to_object(key.as_ref(), &index)?;
            ensure!(
                current.is_some_and(|(id, kind, _)| {
                    let mode: gix::index::entry::Mode = kind.into();
                    id == entry.id
                        && (mode == entry.mode
                            || (!filemode
                                && matches!(
                                    mode,
                                    gix::index::entry::Mode::FILE
                                        | gix::index::entry::Mode::FILE_EXECUTABLE
                                )
                                && matches!(
                                    entry.mode,
                                    gix::index::entry::Mode::FILE
                                        | gix::index::entry::Mode::FILE_EXECUTABLE
                                )))
                }),
                "merge abort would overwrite an unstaged change outside an unresolved conflict"
            );
        }
    }
    let mut restored = gix::index::State::new(repo.object_hash());
    for entry in original.entries() {
        let path = entry.path(&original);
        let stat = if paths.contains(std::path::Path::new(path.to_str()?)) {
            Default::default()
        } else {
            index
                .entry_by_path_and_stage(path, gix::index::entry::Stage::Unconflicted)
                .map(|entry| entry.stat)
                .unwrap_or_default()
        };
        restored.dangerously_push_entry(stat, entry.id, entry.flags, entry.mode, path);
    }
    restored.sort_entries();
    let restored = gix::index::File::from_state(restored, repo.index_path());
    crate::worktree::files(&restored)?;
    let mut new_files = old_files.clone();
    for path in &paths {
        match original_files.get(path) {
            Some(value) => {
                new_files.insert(path.clone(), *value);
            }
            None => {
                new_files.remove(path);
            }
        }
    }
    let worktree_paths = paths
        .into_iter()
        .filter(|path| original_files.contains_key(path) || tracked_paths.contains(path))
        .collect();
    let mut head = head_edit(old, old, "reset: moving to HEAD".into())?;
    if let gix::refs::transaction::Change::Update { log, .. } = &mut head.change {
        log.force_write_reflog = true;
    }
    let mut edits = vec![head, original_head(&repo, old)?];
    if let Some(reference) = repo.try_find_reference("AUTO_MERGE")? {
        edits.push(RefEdit::delete(
            "AUTO_MERGE".try_into()?,
            PreviousValue::MustExistAndMatch(reference.target().into()),
        ));
    }
    let expected_head = repo.find_reference("HEAD")?.target().into();
    crate::worktree::apply(
        &repo,
        &index,
        lock,
        crate::worktree::Update {
            index: restored,
            paths: Some(worktree_paths),
            edits,
            target: Some(old),
            require_clean: false,
            publish_index: true,
            preserve_index: false,
            overwrite_untracked: false,
            worktree_attributes: false,
            expected_head: Some(expected_head),
            file_views: Some((old_files, new_files)),
            metadata: clear_metadata(),
            allow_merge_state: true,
            allow_sequencer_state: false,
        },
    )
}

fn clear_metadata() -> Vec<crate::metadata::Change> {
    [
        "MERGE_HEAD",
        "MERGE_MSG",
        "MERGE_MODE",
        "git-next-merge.json",
    ]
    .into_iter()
    .map(|name| crate::metadata::Change {
        name: name.into(),
        content: None,
    })
    .collect()
}

pub(crate) fn finish_commit(
    repo: &gix::Repository,
    old_index: &gix::index::File,
    lock: crate::write::IndexLock,
    old: gix::ObjectId,
    target: gix::ObjectId,
    subject: &str,
) -> Result<()> {
    let expected_head: gix::refs::Target = repo.find_reference("HEAD")?.target().into();
    let mut edits = vec![head_edit(
        old,
        target,
        format!("commit (merge): {subject}"),
    )?];
    if let Some(reference) = repo.try_find_reference("AUTO_MERGE")? {
        edits.push(RefEdit::delete(
            "AUTO_MERGE".try_into()?,
            PreviousValue::MustExistAndMatch(reference.target().into()),
        ));
    }
    crate::worktree::apply(
        repo,
        old_index,
        lock,
        crate::worktree::Update {
            index: gix::index::File::clone(old_index),
            paths: Some(Default::default()),
            edits,
            target: Some(target),
            require_clean: false,
            publish_index: false,
            preserve_index: true,
            overwrite_untracked: false,
            worktree_attributes: false,
            expected_head: Some(expected_head),
            file_views: None,
            metadata: clear_metadata(),
            allow_merge_state: true,
            allow_sequencer_state: false,
        },
    )
}

fn original_head(repo: &gix::Repository, old: gix::ObjectId) -> Result<RefEdit> {
    let previous = repo
        .try_find_reference("ORIG_HEAD")?
        .map_or(PreviousValue::MustNotExist, |reference| {
            PreviousValue::MustExistAndMatch(reference.target().into())
        });
    Ok(RefEdit::update(
        "ORIG_HEAD".try_into()?,
        old,
        previous,
        "merge: save previous HEAD",
    ))
}

fn head_edit(old: gix::ObjectId, next: gix::ObjectId, message: String) -> Result<RefEdit> {
    let mut edit = RefEdit::update(
        "HEAD".try_into()?,
        next,
        PreviousValue::MustExistAndMatch(old.into()),
        message,
    );
    edit.deref = true;
    Ok(edit)
}

fn message(repo: &gix::Repository, revision: &str) -> Result<String> {
    let config = repo.config_snapshot();
    let kind = if repo
        .try_find_reference(format!("refs/heads/{revision}").as_str())?
        .is_some()
    {
        "branch"
    } else if repo
        .try_find_reference(format!("refs/tags/{revision}").as_str())?
        .is_some()
    {
        "tag"
    } else {
        "commit"
    };
    let destination = repo
        .head_name()?
        .map(|name| name.shorten().to_string())
        .unwrap_or_else(|| "HEAD".into());
    let mut patterns = vec!["main".to_owned(), "master".to_owned()];
    if let Some(values) = config.strings("merge.suppressDest") {
        patterns.clear();
        for value in values {
            let value = value.to_str()?;
            if value.is_empty() {
                patterns.clear();
            } else {
                patterns.push(value.to_owned());
            }
        }
    }
    let mut suppress = false;
    for pattern in patterns {
        suppress |= glob::Pattern::new(&pattern)?.matches(&destination);
    }
    Ok(format!(
        "Merge {kind} '{revision}'{}",
        if suppress {
            String::new()
        } else {
            format!(" into {destination}")
        }
    ))
}

#[allow(clippy::too_many_arguments)]
fn publish_pending(
    repo: &gix::Repository,
    old_index: &gix::index::File,
    lock: crate::write::IndexLock,
    old: gix::ObjectId,
    peer: gix::ObjectId,
    expected_head: gix::refs::Target,
    original: RefEdit,
    tree: gix::ObjectId,
    merged: Option<&gix::merge::tree::Outcome<'_>>,
    message: &str,
    no_ff: bool,
    quiet: bool,
    cleanup: &str,
) -> Result<()> {
    let materialized = repo.index_from_tree(&tree)?;
    let old_files = crate::worktree::files(old_index)?;
    let new_files = crate::worktree::files(&materialized)?;
    let mut index = repo.index_from_tree(&tree)?;
    if let Some(merged) = merged {
        use gix::diff::tree_with_rewrites::Change;
        ensure!(
            merged.conflicts.iter().filter(|conflict| conflict.is_unresolved(Default::default())).all(|conflict| {
                matches!(conflict.resolution, Ok(gix::merge::tree::Resolution::OursModifiedTheirsModifiedThenBlobContentMerge { .. }))
                    && matches!(conflict.ours, Change::Modification { .. } | Change::Addition { .. })
                    && matches!(conflict.theirs, Change::Modification { .. } | Change::Addition { .. })
                    && conflict.ours.location() == conflict.theirs.location()
            }),
            "complex rename/tree conflict recovery is not yet supported"
        );
        ensure!(
            merged.index_changed_after_applying_conflicts(
                &mut index,
                Default::default(),
                gix::merge::tree::apply_index_entries::RemovalMode::Prune
            ),
            "conflict index was not produced"
        );
    }
    let mut masks = std::collections::BTreeMap::<String, u32>::new();
    for entry in index
        .entries()
        .iter()
        .filter(|entry| entry.stage_raw() != 0)
    {
        ensure!(
            matches!(
                entry.mode,
                gix::index::entry::Mode::FILE | gix::index::entry::Mode::FILE_EXECUTABLE
            ),
            "conflicted symlinks/submodules are not yet supported"
        );
        let path = entry.path(&index).to_str()?.to_owned();
        *masks.entry(path).or_default() |= 1 << (entry.stage_raw() - 1);
    }
    ensure!(
        merged.is_none()
            || (!masks.is_empty() && masks.values().all(|mask| matches!(*mask, 6 | 7))),
        "this conflict layout is not yet supported"
    );
    ensure!(
        masks.keys().all(|path| std::path::Path::new(path)
            .file_name()
            .is_none_or(|name| name != ".gitattributes")),
        "conflicted attribute policy is not yet supported"
    );
    let paths: Vec<_> = old_files
        .keys()
        .chain(new_files.keys())
        .filter(|path| old_files.get(*path) != new_files.get(*path))
        .cloned()
        .chain(masks.keys().map(std::path::PathBuf::from))
        .collect::<std::collections::BTreeSet<_>>()
        .into_iter()
        .map(|path| path.to_string_lossy().into_owned())
        .collect();
    let native = NativeState {
        schema_version: 1,
        original_head: old.to_string(),
        peer_head: peer.to_string(),
        materialized_tree: tree.to_string(),
        paths,
    };
    let mut text = message.to_owned();
    text.push('\n');
    if merged.is_some() {
        let comment = crate::write::comment_marker(repo)?;
        if cleanup == "scissors" {
            text.push_str(&format!(
            "\n{comment} ------------------------ >8 ------------------------\n{comment} Do not modify or remove the line above.\n{comment} Everything below it will be ignored.\n{comment}"
            ));
        }
        text.push_str(&format!("\n{comment} Conflicts:\n"));
        for path in masks.keys() {
            text.push_str(&format!("{comment}\t{path}\n"));
        }
    }
    let mut native_bytes = serde_json::to_vec_pretty(&native)?;
    native_bytes.push(b'\n');
    let metadata = [
        ("MERGE_HEAD", format!("{peer}\n").into_bytes()),
        ("MERGE_MSG", text.into_bytes()),
        (
            "MERGE_MODE",
            if no_ff { b"no-ff".to_vec() } else { Vec::new() },
        ),
        ("git-next-merge.json", native_bytes),
    ]
    .into_iter()
    .map(|(name, content)| crate::metadata::Change {
        name: name.into(),
        content: Some(content),
    })
    .collect();
    let expected = repo
        .try_find_reference("AUTO_MERGE")?
        .map_or(PreviousValue::MustNotExist, |reference| {
            PreviousValue::MustExistAndMatch(reference.target().into())
        });
    let auto_merge = RefEdit::update(
        "AUTO_MERGE".try_into()?,
        tree,
        expected,
        "merge: record conflict tree",
    );
    crate::worktree::apply(
        repo,
        old_index,
        lock,
        crate::worktree::Update {
            index,
            paths: None,
            edits: vec![
                original,
                head_edit(old, old, "merge: stop before commit".into())?,
                auto_merge,
            ],
            target: Some(old),
            require_clean: true,
            publish_index: true,
            preserve_index: false,
            overwrite_untracked: false,
            worktree_attributes: false,
            expected_head: Some(expected_head),
            file_views: Some((old_files, new_files)),
            metadata,
            allow_merge_state: false,
            allow_sequencer_state: false,
        },
    )?;
    if !quiet {
        if merged.is_some() {
            println!(
                "{}",
                crate::language::text(
                    "Automatic merge failed; resolve conflicts and commit the result."
                )
            );
        } else {
            println!(
                "{}",
                crate::language::text(
                    "Automatic merge succeeded; stopped before committing as requested."
                )
            );
        }
    }
    if merged.is_some() {
        Err(crate::CommandStatus(1).into())
    } else {
        Ok(())
    }
}

pub(crate) fn run(options: Options) -> Result<()> {
    if options.continue_merge {
        return continue_merge();
    }
    if options.abort {
        return abort_merge();
    }
    let revision = options
        .revision
        .as_deref()
        .context("specify a merge revision")?;
    let mut repo = crate::lfs::prepare(crate::repository::open()?)?.with_object_memory();
    let initial_index = repo.index_or_empty()?;
    let cache = repo.compute_object_cache_size_for_tree_diffs(&initial_index);
    repo.object_cache_size_if_unset(cache);
    crate::write::safe_state(&repo)?;
    ensure!(repo.workdir().is_some(), "merge requires a working tree");
    if crate::runtime::options().work_tree.is_none() && std::env::var_os("GIT_WORK_TREE").is_none()
    {
        ensure!(
            !std::env::current_dir()?
                .canonicalize()?
                .starts_with(repo.git_dir().canonicalize()?),
            "merge must be run from a working tree"
        );
    }
    crate::worktree::files(&initial_index)?;
    let config = repo.config_snapshot();
    let cleanup = crate::write::cleanup_mode(&repo, options.cleanup.as_deref())?;
    let signed_commits = config.try_boolean("commit.gpgSign")?.unwrap_or(false);
    ensure!(
        !config.try_boolean("merge.autostash")?.unwrap_or(false),
        "merge autostash is not yet supported"
    );
    ensure!(
        !config.try_boolean("rerere.enabled")?.unwrap_or(false),
        "rerere merge resolution is not yet supported"
    );
    ensure!(
        !config
            .try_boolean("merge.verifySignatures")?
            .unwrap_or(false),
        "merge signature verification is not yet supported"
    );
    if let Some(name) = repo.head_name()? {
        ensure!(
            config
                .string(&format!("branch.{}.mergeOptions", name.shorten()))
                .is_none(),
            "configured branch mergeOptions are not yet supported"
        );
    }
    let mut ff_only = options.ff_only;
    let mut no_ff = options.no_ff;
    if !options.ff
        && !ff_only
        && !no_ff
        && let Some(value) = config.string("merge.ff")
    {
        if value.as_bstr() == b"only".as_bstr() {
            ff_only = true;
        } else {
            no_ff = !gix::config::Boolean::try_from(value.as_bstr())?.0;
        }
    }
    let old = repo.head_id()?.detach();
    let expected_head: gix::refs::Target = repo.find_reference("HEAD")?.target().into();
    let object = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?;
    ensure!(
        object.kind != gix::objs::Kind::Tag,
        "annotated tag merge messages and mergetag headers are not yet supported"
    );
    let next = object.peel_to_commit()?.id;
    let original = original_head(&repo, old)?;
    if old == next || crate::worktree::is_ancestor(&repo, next, old)? {
        let transaction = repo.refs.transaction().prepare(
            [
                original,
                head_edit(old, old, "merge: already up to date".into())?,
            ],
            gix::lock::acquire::Fail::Immediately,
            gix::lock::acquire::Fail::Immediately,
        )?;
        ensure!(
            expected_head == repo.find_reference("HEAD")?.target(),
            "HEAD changed during merge"
        );
        transaction.commit(repo.committer().transpose()?)?;
        if !options.quiet {
            println!("{}", crate::language::text("Already up to date."));
        }
        return Ok(());
    }
    let fast_forward = crate::worktree::is_ancestor(&repo, old, next)?;
    ensure!(
        fast_forward || !ff_only,
        "not possible to fast-forward; merge refused"
    );
    let lock = crate::write::IndexLock::acquire(&repo)?;
    crate::worktree::clean(&repo)?;
    let old_index = repo.index_or_empty()?;
    crate::worktree::files(&old_index)?;
    let (target, log_message) = if fast_forward && !no_ff {
        (next, format!("merge {}: Fast-forward", revision))
    } else {
        ensure!(
            !signed_commits,
            "signed merge commits are not yet supported"
        );
        for key in ["merge.log", "merge.summary"] {
            if let Some(value) = config.string(key) {
                ensure!(
                    !gix::config::Boolean::try_from(value.as_bstr())
                        .map(|value| value.0)
                        .unwrap_or(true),
                    "configured merge shortlog is not yet supported"
                );
            }
        }
        for section in config.sections() {
            if section
                .header()
                .name()
                .to_string()
                .eq_ignore_ascii_case("merge")
                && section.header().subsection_name().is_some()
            {
                ensure!(
                    section.value("driver").is_none(),
                    "custom merge drivers are not yet supported"
                );
            }
        }
        crate::lfs::check_hooks(
            &repo,
            &[
                "pre-merge-commit",
                "prepare-commit-msg",
                "commit-msg",
                "post-merge",
            ],
            Some(&old_index),
        )?;
        let mut merged = repo.merge_commits(
            old,
            next,
            gix::merge::blob::builtin_driver::text::Labels {
                current: Some(b"HEAD".as_bstr()),
                other: Some(revision.as_bytes().as_bstr()),
                ancestor: None,
            },
            repo.tree_merge_options()?.into(),
        )?;
        let unresolved = merged
            .tree_merge
            .has_unresolved_conflicts(Default::default());
        let tree = merged.tree_merge.tree.write()?.detach();
        let text = match options.message.as_deref() {
            Some(value) => value.to_owned(),
            None => message(&repo, revision)?,
        };
        ensure!(!text.trim().is_empty(), "empty merge message");
        if unresolved || options.no_commit {
            return publish_pending(
                &repo,
                &old_index,
                lock,
                old,
                next,
                expected_head,
                original,
                tree,
                if unresolved {
                    Some(&merged.tree_merge)
                } else {
                    None
                },
                &text,
                no_ff,
                options.quiet,
                &cleanup,
            );
        }
        drop(merged);
        let text = crate::write::prepare_message(&repo, &text, false, Some(&cleanup))?;
        ensure!(!text.is_empty(), "empty merge message after cleanup");
        let target = repo.new_commit(text, tree, [old, next])?.id;
        (
            target,
            format!("merge {}: Merge made by the 'ort' strategy.", revision),
        )
    };
    let tree = repo.find_commit(target)?.tree_id()?;
    let index = repo.index_from_tree(&tree)?;
    crate::lfs::check_hooks(&repo, &["post-merge"], Some(&index))?;
    crate::worktree::apply(
        &repo,
        &old_index,
        lock,
        crate::worktree::Update {
            index,
            paths: None,
            edits: vec![original, head_edit(old, target, log_message)?],
            target: Some(target),
            require_clean: true,
            publish_index: true,
            preserve_index: false,
            overwrite_untracked: false,
            worktree_attributes: false,
            expected_head: Some(expected_head),
            file_views: None,
            metadata: Vec::new(),
            allow_merge_state: false,
            allow_sequencer_state: false,
        },
    )?;
    if !options.quiet {
        if target == next {
            println!(
                "{}",
                crate::language::format(
                    "Fast-forward {old}..{next}",
                    &[("old", old.to_string()), ("next", next.to_string())]
                )
            );
        } else {
            println!(
                "{}",
                crate::language::text("Merge made by the native git-next three-way strategy.")
            );
        }
    }
    Ok(())
}
