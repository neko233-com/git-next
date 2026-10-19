use anyhow::{Context, Result, ensure};
use gix::bstr::{BString, ByteSlice};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Write,
    path::{Component, Path, PathBuf},
};

struct IndexLock {
    path: PathBuf,
    file: Option<fs::File>,
    active: bool,
}
impl IndexLock {
    fn acquire(repo: &gix::Repository) -> Result<Self> {
        let path = repo.index_path().with_extension("lock");
        let file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)
            .context("index is locked by another writer")?;
        Ok(Self {
            path,
            file: Some(file),
            active: true,
        })
    }
    fn persist(mut self, index: &gix::index::File, destination: &Path) -> Result<()> {
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
impl Drop for IndexLock {
    fn drop(&mut self) {
        drop(self.file.take());
        if self.active {
            let _ = fs::remove_file(&self.path);
        }
    }
}

fn safe_state(repo: &gix::Repository) -> Result<()> {
    for name in [
        "MERGE_HEAD",
        "CHERRY_PICK_HEAD",
        "REVERT_HEAD",
        "rebase-merge",
        "rebase-apply",
        "sequencer",
    ] {
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

pub fn add(all: bool, mut paths: Vec<PathBuf>) -> Result<()> {
    let repo = crate::repository::open()?;
    safe_state(&repo)?;
    let root = repo
        .workdir()
        .context("add needs a working tree")?
        .canonicalize()?;
    ensure!(
        root == std::env::current_dir()?.canonicalize()?,
        "run add at the repository root (use -C)"
    );
    ensure!(all || !paths.is_empty(), "specify literal paths or -A");
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
                entry.stage_raw() == 0 && entry.mode != gix::index::entry::Mode::COMMIT
            }),
        "sparse/conflicted indexes and submodules are not supported"
    );
    let mut candidates: BTreeSet<BString> = index
        .entries()
        .iter()
        .map(|entry| entry.path(&index).to_owned())
        .filter(|path| selected(path))
        .collect();
    for path in paths {
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
    let (mut filters, _) = repo.filter_pipeline(None)?;
    let mut entries: BTreeMap<
        BString,
        (
            gix::index::entry::Stat,
            gix::ObjectId,
            gix::index::entry::Flags,
            gix::index::entry::Mode,
        ),
    > = BTreeMap::new();
    for entry in index.entries() {
        entries.insert(
            entry.path(&index).to_owned(),
            (entry.stat, entry.id, entry.flags, entry.mode),
        );
    }
    for path in candidates {
        ensure!(selected(&path), "path outside selection");
        match filters.worktree_file_to_object(path.as_bstr(), &index)? {
            Some((id, kind, _)) => {
                let mut mode: gix::index::entry::Mode = kind.into();
                if !repo
                    .config_snapshot()
                    .boolean("core.filemode")
                    .unwrap_or(cfg!(unix))
                    && let Some((_, _, _, existing)) = entries.get(&path)
                    && (mode == gix::index::entry::Mode::FILE
                        || mode == gix::index::entry::Mode::FILE_EXECUTABLE)
                {
                    mode = *existing;
                }
                // Zero stat forces a content check after staging, including racy same-size edits.
                entries.insert(
                    path,
                    (
                        Default::default(),
                        id,
                        gix::index::entry::Flags::empty(),
                        mode,
                    ),
                );
            }
            None => {
                entries.remove(&path);
            }
        }
    }
    let mut state = gix::index::State::new(repo.object_hash());
    for (path, (stat, id, flags, mode)) in entries {
        state.dangerously_push_entry(stat, id, flags, mode, path.as_bstr());
    }
    state.sort_entries();
    let index = gix::index::File::from_state(state, repo.index_path());
    lock.persist(&index, &repo.index_path())?;
    Ok(())
}

#[derive(Default)]
struct Tree {
    files: Vec<gix::objs::tree::Entry>,
    dirs: BTreeMap<BString, Tree>,
}
impl Tree {
    fn insert(
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
    fn write(mut self, repo: &gix::Repository) -> Result<gix::ObjectId> {
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

pub fn commit(message: &str) -> Result<()> {
    ensure!(!message.trim().is_empty(), "empty commit message");
    let repo = crate::repository::open()?;
    safe_state(&repo)?;
    ensure!(
        repo.config_snapshot().string("core.hooksPath").is_none(),
        "custom hooks require reference Git"
    );
    ensure!(
        !repo
            .config_snapshot()
            .boolean("commit.gpgSign")
            .unwrap_or(false),
        "signed commits are not implemented"
    );
    for hook in [
        "pre-commit",
        "prepare-commit-msg",
        "commit-msg",
        "post-commit",
    ] {
        ensure!(
            !repo.common_dir().join("hooks").join(hook).exists(),
            "commit hooks require reference Git; none were skipped"
        );
    }
    let _lock = IndexLock::acquire(&repo)?;
    let index = repo.index_or_empty()?;
    ensure!(
        !index.is_sparse() && index.entries().iter().all(|entry| entry.stage_raw() == 0),
        "sparse/conflicted indexes require reference Git"
    );
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
    if let Some(parent) = parent {
        ensure!(
            repo.find_commit(parent)?.tree_id()?.detach() != tree,
            "nothing staged for commit"
        );
    }
    let id = repo.commit("HEAD", format!("{}\n", message.trim_end()), tree, parent)?;
    println!(
        "[{}] {}",
        id.to_hex_with_len(12),
        message.lines().next().unwrap_or_default()
    );
    Ok(())
}
