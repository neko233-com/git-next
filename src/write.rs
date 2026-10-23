use anyhow::{Context, Result, ensure};
use gix::bstr::{BString, ByteSlice};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Write,
    path::{Component, Path, PathBuf},
};

pub(crate) struct IndexLock {
    path: PathBuf,
    file: Option<fs::File>,
    active: bool,
}
impl IndexLock {
    pub(crate) fn acquire(repo: &gix::Repository) -> Result<Self> {
        let mut path = repo.index_path().as_os_str().to_owned();
        path.push(".lock");
        let path = PathBuf::from(path);
        let file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)
            .context("index is locked by another writer")?;
        let lock = Self {
            path,
            file: Some(file),
            active: true,
        };
        if let Ok(metadata) = fs::symlink_metadata(repo.index_path()) {
            ensure!(
                !metadata.file_type().is_symlink() && metadata.is_file(),
                "index is not a regular file"
            );
            ensure!(!metadata.permissions().readonly(), "index is read-only");
            lock.file
                .as_ref()
                .context("index lock is closed")?
                .set_permissions(metadata.permissions())?;
        }
        Ok(lock)
    }
    pub(crate) fn persist(mut self, index: &gix::index::File, destination: &Path) -> Result<()> {
        let mut file = self.file.take().context("index lock is closed")?;
        index.write_to(&mut file, Default::default())?;
        file.flush()?;
        file.sync_all()?;
        drop(file);
        fs::rename(&self.path, destination)?;
        self.active = false;
        Ok(())
    }
}
pub(crate) fn index_tree(
    repo: &gix::Repository,
    index: &gix::index::State,
) -> Result<gix::ObjectId> {
    let mut tree = Tree::default();
    for entry in index.entries() {
        tree.insert(
            entry.path(index),
            entry
                .mode
                .to_tree_entry_mode()
                .context("unsupported index mode")?,
            entry.id,
        )?;
    }
    tree.write(repo)
}

impl Drop for IndexLock {
    fn drop(&mut self) {
        drop(self.file.take());
        if self.active {
            let _ = fs::remove_file(&self.path);
        }
    }
}

pub(crate) fn safe_state(repo: &gix::Repository) -> Result<()> {
    safe_state_inner(repo, false, false)
}

pub(crate) fn safe_sequencer_state(repo: &gix::Repository) -> Result<()> {
    safe_state_inner(repo, false, true)
}

pub(crate) fn working_tree(repo: &gix::Repository) -> Result<PathBuf> {
    let root = repo
        .workdir()
        .context("this operation requires a working tree")?
        .canonicalize()?;
    if crate::runtime::options().work_tree.is_none() && std::env::var_os("GIT_WORK_TREE").is_none()
    {
        ensure!(
            !std::env::current_dir()?
                .canonicalize()?
                .starts_with(repo.git_dir().canonicalize()?),
            "this operation must be run from a working tree"
        );
    }
    Ok(root)
}

pub(crate) fn safe_merge_state(repo: &gix::Repository) -> Result<()> {
    safe_state_inner(repo, true, false)
}

fn safe_state_inner(
    repo: &gix::Repository,
    allow_merge: bool,
    allow_sequencer: bool,
) -> Result<()> {
    ensure!(
        !crate::worktree::has_pending_checkout(repo)?,
        "an interrupted git-next checkout needs recovery; keep its journal and backups"
    );
    for name in [
        "MERGE_HEAD",
        "CHERRY_PICK_HEAD",
        "REVERT_HEAD",
        "rebase-merge",
        "rebase-apply",
        "sequencer",
    ] {
        if allow_merge && name == "MERGE_HEAD" {
            continue;
        }
        if allow_sequencer && matches!(name, "CHERRY_PICK_HEAD" | "REVERT_HEAD") {
            continue;
        }
        ensure!(
            !repo.git_dir().join(name).exists(),
            "in-progress Git operation {name} is not supported"
        );
    }
    ensure!(
        !repo
            .config_snapshot()
            .boolean("core.sparseCheckout")
            .unwrap_or(false),
        "sparse writes are not supported"
    );
    Ok(())
}

fn path_bytes(path: &Path) -> Result<BString> {
    let mut normalized = String::new();
    for part in path.components() {
        match part {
            Component::CurDir => {}
            Component::Normal(part) => {
                let part = part
                    .to_str()
                    .context("non-UTF8 add paths are not supported")?;
                ensure!(
                    !part.eq_ignore_ascii_case(".git") && !part.contains([':', '*', '?', '[', ']']),
                    "unsupported or protected path"
                );
                if !normalized.is_empty() {
                    normalized.push('/')
                }
                normalized.push_str(part);
            }
            _ => anyhow::bail!("add paths must be literal paths relative to the repository root"),
        }
    }
    Ok(normalized.into())
}

pub fn add(all: bool, mut paths: Vec<PathBuf>, renormalize: bool) -> Result<()> {
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    safe_merge_state(&repo)?;
    crate::merging::read_state(&repo)?;
    let root = repo
        .workdir()
        .context("add needs a working tree")?
        .canonicalize()?;
    ensure!(
        root == std::env::current_dir()?.canonicalize()?,
        "run add at the repository root (use -C)"
    );
    ensure!(
        all || renormalize || !paths.is_empty(),
        "specify literal paths or -A"
    );
    if paths.is_empty() {
        paths.push(".".into())
    }
    let selectors = paths
        .iter()
        .map(|p| path_bytes(p))
        .collect::<Result<Vec<_>>>()?;
    for path in &paths {
        let mut ancestor = root.clone();
        for component in path
            .components()
            .take(path.components().count().saturating_sub(1))
        {
            ancestor.push(component.as_os_str());
            if let Ok(metadata) = fs::symlink_metadata(&ancestor) {
                ensure!(
                    !metadata.file_type().is_symlink(),
                    "add cannot traverse a symlink directory"
                );
            }
        }
    }
    let selected = |path: &[u8]| {
        selectors.iter().any(|prefix| {
            prefix.is_empty()
                || path == prefix.as_slice()
                || (path.starts_with(prefix.as_slice()) && path.get(prefix.len()) == Some(&b'/'))
        })
    };
    let lock = IndexLock::acquire(&repo)?;
    let index = repo.index_or_empty()?;
    ensure!(
        !index.is_sparse()
            && index.entries().iter().all(|entry| {
                entry.mode != gix::index::entry::Mode::COMMIT
                    && !entry
                        .flags
                        .contains(gix::index::entry::Flags::SKIP_WORKTREE)
            }),
        "sparse/skip-worktree indexes and submodules are not supported"
    );
    let mut candidates: BTreeSet<BString> = index
        .entries()
        .iter()
        .filter(|entry| !renormalize || entry.stage_raw() == 0)
        .map(|entry| entry.path(&index).to_owned())
        .filter(|path| selected(path))
        .collect();
    for path in paths.into_iter().filter(|_| !renormalize) {
        let mut builder = ignore::WalkBuilder::new(root.join(path));
        builder
            .hidden(false)
            .git_global(false)
            .parents(false)
            .follow_links(false)
            .filter_entry(|entry| entry.file_name() != ".git");
        for entry in builder.build() {
            let entry = entry?;
            let relative = entry.path().strip_prefix(&root)?;
            ensure!(
                relative.as_os_str().is_empty()
                    || !entry.file_type().is_some_and(|kind| kind.is_dir())
                    || !entry.path().join(".git").exists(),
                "adding a nested Git repository is not supported"
            );
            if relative.as_os_str().is_empty()
                || entry.file_type().is_some_and(|kind| kind.is_dir())
            {
                continue;
            }
            candidates.insert(path_bytes(relative)?);
        }
    }
    let mut parents = BTreeMap::new();
    if renormalize {
        for path in &candidates {
            let parent = parent_state(&root, path.as_bstr(), &mut parents)?;
            ensure!(
                parent == ParentState::Directory,
                "stage missing paths and directory replacements before renormalizing"
            );
            match fs::symlink_metadata(root.join(gix::path::from_bstr(path.as_bstr())?)) {
                Ok(metadata) => ensure!(
                    !metadata.is_dir(),
                    "stage file/directory replacements before renormalizing"
                ),
                Err(error) => {
                    return Err(error).context(
                        "cannot renormalize a tracked path; stage directory replacements first",
                    );
                }
            }
        }
    }
    let (mut filters, _) = repo.filter_pipeline(None)?;
    if renormalize {
        filters.renormalize();
    }
    let mut entries: BTreeMap<
        (BString, u32),
        (
            gix::index::entry::Stat,
            gix::ObjectId,
            gix::index::entry::Flags,
            gix::index::entry::Mode,
        ),
    > = BTreeMap::new();
    for entry in index.entries() {
        entries.insert(
            (entry.path(&index).to_owned(), entry.stage_raw()),
            (entry.stat, entry.id, entry.flags, entry.mode),
        );
    }
    for path in candidates {
        ensure!(selected(&path), "path outside selection");
        let object = if parent_state(&root, path.as_bstr(), &mut parents)? == ParentState::Directory
        {
            filters.worktree_file_to_object(path.as_bstr(), &index)?
        } else {
            None
        };
        match object {
            Some((id, kind, _)) => {
                let mut mode: gix::index::entry::Mode = kind.into();
                if !repo
                    .config_snapshot()
                    .boolean("core.filemode")
                    .unwrap_or(cfg!(unix))
                    && let Some((_, _, _, existing)) = [0, 2, 3, 1]
                        .iter()
                        .find_map(|stage| entries.get(&(path.clone(), *stage)))
                    && (mode == gix::index::entry::Mode::FILE
                        || mode == gix::index::entry::Mode::FILE_EXECUTABLE)
                    && matches!(
                        *existing,
                        gix::index::entry::Mode::FILE | gix::index::entry::Mode::FILE_EXECUTABLE
                    )
                {
                    mode = *existing;
                }
                // Zero stat forces a content check after staging, including racy same-size edits.
                for stage in 0..=3 {
                    entries.remove(&(path.clone(), stage));
                }
                // Selecting a child also replaces an indexed file at its ancestor path.
                for offset in path
                    .iter()
                    .enumerate()
                    .filter_map(|(offset, byte)| (*byte == b'/').then_some(offset))
                {
                    let ancestor = path[..offset].as_bstr().to_owned();
                    for stage in 0..=3 {
                        entries.remove(&(ancestor.clone(), stage));
                    }
                }
                let mut prefix = path.clone();
                prefix.push(b'/');
                let descendants: Vec<_> = entries
                    .range((prefix.clone(), 0)..)
                    .take_while(|((name, _), _)| name.starts_with(prefix.as_slice()))
                    .map(|(key, _)| key.clone())
                    .collect();
                for descendant in descendants {
                    entries.remove(&descendant);
                }
                entries.insert(
                    (path, 0),
                    (
                        Default::default(),
                        id,
                        gix::index::entry::Flags::empty(),
                        mode,
                    ),
                );
            }
            None => {
                for stage in 0..=3 {
                    entries.remove(&(path.clone(), stage));
                }
            }
        }
    }
    let mut state = gix::index::State::new(repo.object_hash());
    for ((path, _), (stat, id, flags, mode)) in entries {
        state.dangerously_push_entry(stat, id, flags, mode, path.as_bstr());
    }
    state.sort_entries();
    validate_index_paths(&state)?;
    let index = gix::index::File::from_state(state, repo.index_path());
    lock.persist(&index, &repo.index_path())?;
    Ok(())
}

fn validate_index_paths(index: &gix::index::State) -> Result<()> {
    index.verify_entries()?;
    for entry in index
        .entries()
        .iter()
        .filter(|entry| entry.stage_raw() == 0)
    {
        let path = entry.path(index);
        for offset in path
            .iter()
            .enumerate()
            .filter_map(|(offset, byte)| (*byte == b'/').then_some(offset))
        {
            ensure!(
                index
                    .entry_by_path_and_stage(
                        path[..offset].as_bstr(),
                        gix::index::entry::Stage::Unconflicted
                    )
                    .is_none(),
                "index contains a file/directory conflict at {}",
                path[..offset].as_bstr()
            );
        }
    }
    Ok(())
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum ParentState {
    Directory,
    Missing,
    Replaced,
}

fn parent_state(
    root: &Path,
    path: &gix::bstr::BStr,
    cache: &mut BTreeMap<BString, ParentState>,
) -> Result<ParentState> {
    for offset in path
        .iter()
        .enumerate()
        .filter_map(|(offset, byte)| (*byte == b'/').then_some(offset))
    {
        let prefix = path[..offset].as_bstr();
        let state = if let Some(state) = cache.get(prefix) {
            *state
        } else {
            let state = match fs::symlink_metadata(root.join(gix::path::from_bstr(prefix)?)) {
                Ok(metadata) if metadata.is_dir() && !metadata.file_type().is_symlink() => {
                    ParentState::Directory
                }
                Ok(_) => ParentState::Replaced,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => ParentState::Missing,
                Err(error) => return Err(error).context("cannot inspect an add path's parent"),
            };
            cache.insert(prefix.to_owned(), state);
            state
        };
        if state != ParentState::Directory {
            return Ok(state);
        }
    }
    Ok(ParentState::Directory)
}

#[derive(Default)]
pub(crate) struct Tree {
    files: Vec<gix::objs::tree::Entry>,
    dirs: BTreeMap<BString, Tree>,
}
impl Tree {
    pub(crate) fn insert(
        &mut self,
        path: &[u8],
        mode: gix::objs::tree::EntryMode,
        id: gix::ObjectId,
    ) -> Result<()> {
        if let Some(slash) = path.iter().position(|c| *c == b'/') {
            self.dirs
                .entry(path[..slash].into())
                .or_default()
                .insert(&path[slash + 1..], mode, id)
        } else {
            ensure!(!path.is_empty(), "empty index path");
            self.files.push(gix::objs::tree::Entry {
                mode,
                filename: path.into(),
                oid: id,
            });
            Ok(())
        }
    }
    pub(crate) fn write(mut self, repo: &gix::Repository) -> Result<gix::ObjectId> {
        for (name, tree) in self.dirs {
            self.files.push(gix::objs::tree::Entry {
                mode: gix::objs::tree::EntryKind::Tree.into(),
                filename: name,
                oid: tree.write(repo)?,
            });
        }
        self.files.sort();
        Ok(repo
            .write_object(gix::objs::Tree {
                entries: self.files,
            })?
            .detach())
    }
}

pub(crate) fn comment_marker(repo: &gix::Repository) -> Result<String> {
    let config = repo.config_snapshot();
    let value = config
        .string("core.commentString")
        .or_else(|| config.string("core.commentChar"))
        .map(|value| value.to_str().map(str::to_owned))
        .transpose()?
        .unwrap_or_else(|| "#".into());
    ensure!(
        !value.is_empty() && value != "auto" && !value.contains(['\n', '\r', '\0']),
        "automatic or invalid commit comment markers are not yet supported"
    );
    Ok(value)
}

pub(crate) fn cleanup_mode(repo: &gix::Repository, cleanup: Option<&str>) -> Result<String> {
    let config = repo.config_snapshot();
    if let Some(encoding) = config.string("i18n.commitEncoding") {
        let encoding = encoding.to_str()?;
        ensure!(
            encoding.eq_ignore_ascii_case("utf-8") || encoding.eq_ignore_ascii_case("utf8"),
            "non-UTF-8 commit encoding is not yet supported"
        );
    }
    let mode = match cleanup {
        Some(mode) => mode.to_owned(),
        None => config
            .string("commit.cleanup")
            .map(|value| value.to_str().map(str::to_owned))
            .transpose()?
            .unwrap_or_else(|| "default".into()),
    };
    ensure!(
        matches!(
            mode.as_str(),
            "default" | "verbatim" | "strip" | "whitespace" | "scissors"
        ),
        "unsupported commit cleanup mode"
    );
    Ok(mode)
}

pub(crate) fn prepare_message(
    repo: &gix::Repository,
    input: &str,
    edited: bool,
    cleanup: Option<&str>,
) -> Result<String> {
    ensure!(!input.contains('\0'), "commit message contains a NUL byte");
    let mode = cleanup_mode(repo, cleanup)?;
    if mode == "verbatim" {
        return Ok(input.to_owned());
    }
    let strip = match mode.as_str() {
        "default" => edited,
        "strip" => true,
        "whitespace" | "scissors" => false,
        _ => anyhow::bail!("unsupported commit cleanup mode"),
    };
    let comment = if strip || (mode == "scissors" && edited) {
        comment_marker(repo)?
    } else {
        "#".into()
    };
    let scissors = format!("{comment} ------------------------ >8 ------------------------");
    let mut lines: Vec<&str> = Vec::new();
    for line in input.split('\n') {
        let line = line.trim_end_matches([' ', '\t', '\r', '\u{b}', '\u{c}']);
        if mode == "scissors" && edited && line == scissors {
            break;
        }
        if strip && line.starts_with(&comment) {
            continue;
        }
        if line.is_empty() && (lines.is_empty() || lines.last() == Some(&"")) {
            continue;
        }
        lines.push(line);
    }
    while lines.last() == Some(&"") {
        lines.pop();
    }
    Ok(if lines.is_empty() {
        String::new()
    } else {
        format!("{}\n", lines.join("\n"))
    })
}

pub fn commit(message: &str, cleanup: Option<&str>) -> Result<()> {
    let mut text = message.to_owned();
    if !text.ends_with('\n') {
        text.push('\n');
    }
    commit_inner(&text, false, None, cleanup)
}

pub(crate) fn commit_pending(no_edit: bool, cleanup: Option<&str>) -> Result<()> {
    let repo = crate::repository::open()?;
    ensure!(
        no_edit
            || !matches!(
                cleanup_mode(&repo, cleanup)?.as_str(),
                "whitespace" | "verbatim"
            ),
        "this cleanup mode requires interactive merge editing; use commit --no-edit or commit -m explicitly"
    );
    let merge = crate::merging::read_state(&repo)?.context("there is no merge to commit")?;
    commit_inner(&merge.message, !no_edit, Some(merge.peer), cleanup)
}

fn commit_inner(
    message: &str,
    edited: bool,
    expected_merge: Option<gix::ObjectId>,
    cleanup: Option<&str>,
) -> Result<()> {
    let repo = crate::repository::open()?;
    working_tree(&repo)?;
    safe_merge_state(&repo)?;
    let merge = crate::merging::read_state(&repo)?;
    if let Some(expected) = expected_merge {
        ensure!(
            merge.as_ref().is_some_and(|merge| merge.peer == expected),
            "pending merge changed before commit"
        );
    }
    let text = prepare_message(&repo, message, edited, cleanup)?;
    ensure!(!text.is_empty(), "empty commit message");
    let repo = if merge.is_some() {
        repo.with_object_memory()
    } else {
        repo
    };
    ensure!(
        !repo
            .config_snapshot()
            .try_boolean("commit.gpgSign")?
            .unwrap_or(false),
        "signed commits are not implemented"
    );
    crate::lfs::check_hooks(
        &repo,
        &[
            "pre-commit",
            "prepare-commit-msg",
            "commit-msg",
            "post-commit",
        ],
        None,
    )?;
    let lock = IndexLock::acquire(&repo)?;
    let index = repo.index_or_empty()?;
    ensure!(
        !index.is_sparse()
            && index.entries().iter().all(|entry| entry.stage_raw() == 0
                && !entry
                    .flags
                    .contains(gix::index::entry::Flags::SKIP_WORKTREE)),
        "resolve all unmerged entries before committing; sparse/skip-worktree commits are not yet supported"
    );
    validate_index_paths(&index)?;
    let mut tree = Tree::default();
    for entry in index.entries() {
        tree.insert(
            entry.path(&index),
            entry
                .mode
                .to_tree_entry_mode()
                .context("unsupported index mode")?,
            entry.id,
        )?;
    }
    let tree = tree.write(&repo)?;
    let parent = repo.head()?.try_peel_to_id()?.map(|id| id.detach());
    if let Some(parent) = parent
        && merge.is_none()
    {
        ensure!(
            repo.find_commit(parent)?.tree_id()?.detach() != tree,
            "nothing staged for commit"
        );
    }
    let subject = text.lines().next().unwrap_or_default().to_owned();
    let id = if let Some(merge) = merge {
        let old = parent.context("merge commit requires an existing HEAD")?;
        let current =
            crate::merging::read_state(&repo)?.context("merge state disappeared before commit")?;
        ensure!(
            merge.peer == current.peer,
            "merge head changed before commit"
        );
        let id = repo.new_commit(text, tree, [old, merge.peer])?.id;
        crate::merging::finish_commit(&repo, &index, lock, old, id, &subject)?;
        id
    } else {
        repo.commit("HEAD", text, tree, parent)?.detach()
    };
    println!("[{}] {}", id.to_hex_with_len(12), subject);
    Ok(())
}
