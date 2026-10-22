use anyhow::{Context, Result, ensure};
use gix::bstr::ByteSlice;
use std::{collections::BTreeMap, fs, io::Write, path::Path};

pub fn open() -> Result<gix::Repository> {
    crate::runtime::open()
}

pub fn atomic_write(path: &Path, bytes: &[u8]) -> Result<()> {
    let lock = path.with_file_name(format!(
        "{}.lock",
        path.file_name()
            .context("invalid file name")?
            .to_string_lossy()
    ));
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&lock)
        .context("another writer holds the lock")?;
    let result = (|| {
        file.write_all(bytes)?;
        file.sync_all()?;
        drop(file);
        fs::rename(&lock, path)?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(lock);
    }
    result
}

/// The default branch name for a new repository.
pub const DEFAULT_BRANCH: &str = "main";

/// Reject a branch name that cannot become a ref, before anything is created.
fn validate_branch(branch: &str) -> Result<()> {
    let _: gix::refs::FullName = format!("refs/heads/{branch}").try_into()?;
    ensure!(!branch.starts_with('-'), "invalid initial branch");
    Ok(())
}

pub fn init(directory: &Path, bare: bool, branch: Option<&str>) -> Result<()> {
    if let Some(branch) = branch {
        validate_branch(branch)?;
    }
    ensure!(
        !directory.join("HEAD").exists() && !directory.join(".git").exists(),
        "destination already contains a repository; refusing to change its HEAD"
    );
    let repo = if bare {
        gix::init_bare(directory)?
    } else {
        gix::init(directory)?
    };
    // The explicit `-b` wins. Otherwise the user's configured default applies,
    // which is what makes `git init` behave like the Git it replaces: a team that
    // standardised on another branch name sets `init.defaultBranch` once instead
    // of passing `-b` to every `init`.
    let branch = match branch {
        Some(branch) => branch.to_owned(),
        None => match repo.config_snapshot().string("init.defaultBranch") {
            Some(configured) => {
                let configured = String::from_utf8_lossy(&configured).into_owned();
                validate_branch(&configured)?;
                configured
            }
            None => DEFAULT_BRANCH.to_owned(),
        },
    };
    atomic_write(
        &repo.git_dir().join("HEAD"),
        format!("ref: refs/heads/{branch}\n").as_bytes(),
    )?;
    if !bare {
        crate::eol::configure_new(&repo)?;
        crate::eol::shared_files(
            repo.workdir()
                .context("new repository has no working tree")?,
        )?;
    }
    println!(
        "{}",
        crate::language::format(
            "Initialized {kind}Git repository in {path}",
            &[
                (
                    "kind",
                    crate::language::text(if bare { "bare " } else { "" }).to_owned()
                ),
                ("path", repo.git_dir().display().to_string()),
            ]
        )
    );
    Ok(())
}

pub fn config_set(repo: &gix::Repository, key: &str, value: &str) -> Result<()> {
    let mut file = repo.config_file_mut(repo.common_dir().join("config"))?;
    file.set_raw_value(key, value)?;
    file.commit()?;
    Ok(())
}

pub fn remote_add(name: &str, url: &str) -> Result<()> {
    let repo = open()?;
    ensure!(repo.find_remote(name).is_err(), "remote already exists");
    let _: gix::refs::FullName = format!("refs/remotes/{name}/branch").try_into()?;
    let _ = gix::url::parse(url.as_bytes().as_bstr())?;
    let mut file = repo.config_file_mut(repo.common_dir().join("config"))?;
    file.set_raw_value(&format!("remote.{name}.url"), url)?;
    file.set_raw_value(
        &format!("remote.{name}.fetch"),
        format!("+refs/heads/*:refs/remotes/{name}/*"),
    )?;
    file.commit()?;
    Ok(())
}

pub fn remote_url(name: &str) -> Result<()> {
    let repo = open()?;
    let remote = repo.find_remote(name)?;
    println!(
        "{}",
        remote
            .url(gix::remote::Direction::Fetch)
            .context("remote has no URL")?
            .to_bstring()
    );
    Ok(())
}

pub fn remotes(verbose: bool) -> Result<()> {
    let repo = open()?;
    for name in repo.remote_names() {
        if !verbose {
            println!("{name}");
            continue;
        }
        let remote = repo.find_remote(name.as_bstr())?;
        for direction in [gix::remote::Direction::Fetch, gix::remote::Direction::Push] {
            if let Some(url) = remote.url(direction) {
                println!("{name}\t{} ({})", url.to_bstring(), direction.as_str());
            }
        }
    }
    Ok(())
}

pub fn rev_parse(
    top: bool,
    dir: bool,
    absolute_dir: bool,
    bare: bool,
    revision: &str,
) -> Result<()> {
    let repo = open()?;
    if top {
        println!(
            "{}",
            git_path(
                &repo
                    .workdir()
                    .context("bare repository has no working tree")?
                    .canonicalize()?
            )?
        )
    } else if absolute_dir {
        println!("{}", git_path(&repo.git_dir().canonicalize()?)?)
    } else if dir {
        println!("{}", git_path(repo.git_dir())?)
    } else if bare {
        println!("{}", repo.is_bare())
    } else {
        println!("{}", repo.rev_parse_single(revision.as_bytes().as_bstr())?)
    }
    Ok(())
}

pub(crate) fn git_path(path: &Path) -> Result<String> {
    let mut path = path.to_str().context("path is not UTF-8")?.to_owned();
    if cfg!(windows) {
        if let Some(unc) = path.strip_prefix(r"\\?\UNC\") {
            path = format!(r"\\{unc}");
        } else if let Some(ordinary) = path.strip_prefix(r"\\?\") {
            path = ordinary.to_owned();
        }
        path = path.replace('\\', "/");
    }
    while let Some(relative) = path.strip_prefix("./") {
        path = relative.to_owned();
    }
    Ok(path)
}

pub fn log(
    count: Option<usize>,
    oneline: bool,
    format: Option<String>,
    date: Option<String>,
    revision: Vec<String>,
    nul: bool,
) -> Result<()> {
    crate::difference::log(count, oneline, format, date, revision, nul)
}

pub fn cat_file(spec: &str, kind: bool, size: bool) -> Result<()> {
    ensure!(!(kind && size), "choose either -t, -s or -p");
    let repo = open()?;
    let object = repo.rev_parse_single(spec.as_bytes().as_bstr())?.object()?;
    if kind {
        println!("{}", object.kind)
    } else if size {
        println!("{}", object.data.len())
    } else if object.kind == gix::objs::Kind::Tree {
        for entry in object.into_tree().iter() {
            let entry = entry?;
            let kind = match entry.mode().kind() {
                gix::objs::tree::EntryKind::Tree => "tree",
                gix::objs::tree::EntryKind::Commit => "commit",
                _ => "blob",
            };
            println!(
                "{:06o} {} {}\t{}",
                entry.mode().value(),
                kind,
                entry.oid(),
                entry.filename()
            );
        }
    } else {
        std::io::stdout().write_all(&object.data)?
    }
    Ok(())
}

pub fn hash_object(path: &Path, write: bool) -> Result<()> {
    let file = fs::File::open(path)?;
    if write {
        println!("{}", open()?.write_blob_stream(file)?)
    } else {
        let data = fs::read(path)?;
        let hash = open()
            .map(|r| r.object_hash())
            .unwrap_or(gix::hash::Kind::Sha1);
        println!(
            "{}",
            gix::objs::compute_hash(hash, gix::objs::Kind::Blob, &data)?
        );
    }
    Ok(())
}

pub fn ls_files(stage: bool) -> Result<()> {
    let repo = open()?;
    let index = repo.index_or_empty()?;
    for entry in index.entries() {
        if stage {
            print!(
                "{:06o} {} {}\t",
                entry.mode.bits(),
                entry.id,
                entry.stage_raw()
            )
        }
        println!("{}", entry.path(&index));
    }
    Ok(())
}

pub fn show_ref(head: bool) -> Result<()> {
    let repo = open()?;
    if head && let Ok(id) = repo.head_id() {
        println!("{id} HEAD")
    }
    let mut refs = Vec::new();
    for reference in repo.references()?.all()? {
        let mut reference = reference?;
        let id = reference.follow_to_object()?.detach();
        refs.push((reference.name().as_bstr().to_owned(), id));
    }
    refs.sort_by(|a, b| a.0.cmp(&b.0));
    for (name, id) in refs {
        println!("{id} {name}")
    }
    Ok(())
}

pub fn branch(name: Option<&str>, revision: &str) -> Result<()> {
    let repo = open()?;
    if let Some(name) = name {
        ensure!(
            !name.starts_with('-') && name != "HEAD",
            "invalid branch name"
        );
        let id = repo.rev_parse_single(revision.as_bytes().as_bstr())?;
        repo.reference(
            format!("refs/heads/{name}"),
            id.detach(),
            gix::refs::transaction::PreviousValue::MustNotExist,
            "branch: created by git-next",
        )?;
    } else {
        let current = repo.head_name()?;
        for reference in repo.references()?.local_branches()? {
            let reference = reference?;
            println!(
                "{} {}",
                if current.as_ref().is_some_and(|h| h == reference.name()) {
                    "*"
                } else {
                    " "
                },
                reference.name().shorten()
            );
        }
    }
    Ok(())
}

pub fn status(options: crate::status_output::Options) -> Result<()> {
    let repo = crate::lfs::prepare(open()?)?;
    ensure!(!repo.is_bare(), "status requires a working tree");
    validate_status_config(&repo)?;
    let settings = options.settings(&repo)?;
    let ignore_staged_submodules = crate::status_output::ignore_staged_submodules(&repo, &options)?;
    let patterns = options
        .paths
        .iter()
        .map(|path| gix::path::os_str_into_bstr(path).map(|path| path.to_owned()))
        .collect::<std::result::Result<Vec<_>, _>>()?;
    let optional_locks = std::env::var("GIT_OPTIONAL_LOCKS")
        .ok()
        .is_none_or(|value| {
            !["", "0", "false", "no", "off"]
                .iter()
                .any(|flag| value.eq_ignore_ascii_case(flag))
        });
    let refresh_lock = optional_locks
        .then(|| crate::write::IndexLock::acquire(&repo).ok())
        .flatten();
    let mut paths = crate::status_output::Rows::new();
    let mut conflicts = std::collections::BTreeSet::new();
    let index = repo.index_or_empty()?;
    let threads = index.entries().len().div_ceil(2000).clamp(1, 8);
    let mut platform = repo.status(gix::progress::Discard)?;
    if let Some(value) = &options.ignore_submodules {
        use gix::submodule::config::Ignore;
        let ignore = match value.as_str() {
            "none" => Ignore::None,
            "untracked" => Ignore::Untracked,
            "dirty" => Ignore::Dirty,
            _ => Ignore::All,
        };
        platform = platform.index_worktree_submodules(gix::status::Submodule::Given {
            ignore,
            check_dirty: false,
        });
    }
    if settings.override_untracked {
        let walk = repo.dirwalk_options()?;
        platform =
            platform.index_worktree_options_mut(|options| options.dirwalk_options = Some(walk));
    }
    let mut items = platform
        .index(index.clone().into())
        .untracked_files(
            if !patterns.is_empty()
                && matches!(settings.untracked, gix::status::UntrackedFiles::Collapsed)
            {
                gix::status::UntrackedFiles::Files
            } else {
                settings.untracked
            },
        )
        .index_worktree_options_mut(|options| options.thread_limit = Some(threads))
        .into_iter(patterns.clone())?;
    for item in items.by_ref() {
        let item = item?;
        let untracked = matches!(&item, gix::status::Item::IndexWorktree(change) if change.summary() == Some(gix::status::plumbing::index_as_worktree_with_renames::Summary::Added));
        let mut path = item.location().to_owned();
        // A directory result must not overwrite a tracked file at the same pathname.
        if let gix::status::Item::IndexWorktree(
            gix::status::index_worktree::Item::DirectoryContents { entry, .. },
        ) = &item
            && matches!(entry.status, gix::dir::entry::Status::Untracked)
            && entry.disk_kind.is_some_and(|kind| kind.is_dir())
            && !path.ends_with(b"/")
        {
            path.push(b'/');
        }
        let row = paths.entry((untracked, path.clone())).or_default();
        let entry = &mut row.code;
        match item {
            gix::status::Item::IndexWorktree(change) => {
                if let gix::status::index_worktree::Item::Modification {
                    status:
                        gix::status::plumbing::index_as_worktree::EntryStatus::Change(
                            gix::status::plumbing::index_as_worktree::Change::SubmoduleModification(
                                status,
                            ),
                        ),
                    ..
                } = &change
                {
                    row.submodule = Some(crate::status_v2::submodule_state(status));
                }
                if let gix::status::index_worktree::Item::DirectoryContents { entry, .. } = &change
                {
                    row.directory = entry.disk_kind.is_some_and(|kind| kind.is_dir());
                }
                if let gix::status::index_worktree::Item::Rewrite { source, .. } = &change {
                    row.source = Some(source.rela_path().to_owned());
                }
                if let Some(summary) = change.summary() {
                    use gix::status::plumbing::index_as_worktree_with_renames::Summary;
                    match summary {
                        Summary::Added => *entry = ['?', '?'],
                        Summary::Removed => entry[1] = 'D',
                        Summary::Modified => entry[1] = 'M',
                        Summary::TypeChange => entry[1] = 'T',
                        Summary::IntentToAdd => entry[1] = 'A',
                        Summary::Conflict => {
                            conflicts.insert(path);
                            *entry = ['U', 'U'];
                        }
                        Summary::Renamed => entry[1] = 'R',
                        Summary::Copied => entry[1] = 'C',
                    }
                }
            }
            gix::status::Item::TreeIndex(change) => {
                use gix::diff::index::Change;
                entry[0] = match change {
                    Change::Addition { .. } => 'A',
                    Change::Deletion { .. } => 'D',
                    Change::Modification {
                        previous_entry_mode,
                        entry_mode,
                        ..
                    } => {
                        if previous_entry_mode.bits() & 0o170000 != entry_mode.bits() & 0o170000 {
                            'T'
                        } else {
                            'M'
                        }
                    }
                    Change::Rewrite {
                        source_location,
                        copy,
                        ..
                    } => {
                        row.source = Some(source_location.into_owned());
                        if copy { 'C' } else { 'R' }
                    }
                };
            }
        }
    }
    // Every conflict stage is tracked, including paths with no stage-zero/ours entry.
    paths.retain(|(untracked, path), _| {
        !*untracked || index.entry_index_by_path(path.as_bstr()).is_err()
    });
    crate::status_ignored::collect(&repo, &index, &patterns, &options, &settings, &mut paths)?;
    let mut outcome = items.into_outcome();
    let head_index = outcome
        .as_ref()
        .and_then(|outcome| outcome.tree_index.as_ref())
        .map(|outcome| &outcome.tree_index);
    if ignore_staged_submodules {
        paths.retain(|(untracked, path), row| {
            if *untracked {
                return true;
            }
            let source = row.source.as_ref().unwrap_or(path);
            let gitlink = index
                .entry_by_path_and_stage(path.as_bstr(), gix::index::entry::Stage::Unconflicted)
                .into_iter()
                .chain(head_index.and_then(|head| head.entry_by_path(source.as_bstr())))
                .any(|entry| entry.mode == gix::index::entry::Mode::COMMIT);
            !gitlink
        });
    }
    crate::status_output::filter_collapsed_paths(&repo, &index, &patterns, &settings, &mut paths)?;
    if !conflicts.is_empty()
        && let Some(outcome) = &outcome
    {
        let index: &gix::index::File = match &outcome.worktree_index {
            gix::worktree::IndexPersistedOrInMemory::Persisted(index) => index,
            gix::worktree::IndexPersistedOrInMemory::InMemory(index) => index,
        };
        let mut masks = BTreeMap::<gix::bstr::BString, u32>::new();
        for entry in index
            .entries()
            .iter()
            .filter(|entry| entry.stage_raw() != 0 && conflicts.contains(entry.path(index)))
        {
            *masks.entry(entry.path(index).to_owned()).or_default() |= 1 << (entry.stage_raw() - 1);
        }
        for (path, mask) in masks {
            let code = match mask {
                1 => ['D', 'D'],
                2 => ['A', 'U'],
                3 => ['U', 'D'],
                4 => ['U', 'A'],
                5 => ['D', 'U'],
                6 => ['A', 'A'],
                _ => ['U', 'U'],
            };
            paths.entry((false, path)).or_default().code = code;
        }
    }
    let output = if options.is_v2() {
        let head = &outcome
            .as_ref()
            .and_then(|outcome| outcome.tree_index.as_ref())
            .context("status did not complete the HEAD comparison")?
            .tree_index;
        crate::status_v2::render(&repo, &options, &settings, &paths, head, &index)?
    } else {
        options.render(&repo, &settings, &paths)?
    };
    if let Some(lock) = refresh_lock
        && let Some(outcome) = &mut outcome
        && outcome.has_changes()
        && status_index_refreshable(&repo.index_path(), repo.object_hash())
        && let Some(index) = outcome.apply_changes()
    {
        // This lock predates the scan, so a concurrent index writer cannot be overwritten.
        let _ = lock.persist(&index, &repo.index_path());
    }
    std::io::stdout().lock().write_all(&output)?;
    Ok(())
}

fn validate_status_config(repo: &gix::Repository) -> Result<()> {
    let config = repo.config_snapshot();
    for key in ["status.renames", "diff.renames"] {
        if config
            .string(key)
            .is_none_or(|value| !matches!(value.as_slice(), b"copy" | b"copies"))
        {
            config.try_boolean(key)?;
        }
    }
    for key in ["status.renameLimit", "diff.renameLimit"] {
        if let Some(value) = config.try_integer(key)? {
            ensure!(
                (0..=i64::from(i32::MAX)).contains(&value),
                "{key} must be a nonnegative 32-bit integer"
            );
        }
    }
    if let Some(algorithm) = config.string("diff.algorithm") {
        ensure!(
            matches!(
                algorithm.as_slice(),
                b"myers" | b"default" | b"minimal" | b"histogram"
            ),
            "unsupported diff algorithm for status"
        );
    } else {
        ensure!(
            config.try_boolean("diff.algorithm")?.is_none(),
            "diff.algorithm requires a value"
        );
    }
    Ok(())
}

fn status_index_refreshable(path: &Path, hash: gix::hash::Kind) -> bool {
    let parse = || -> Option<()> {
        let metadata = fs::symlink_metadata(path).ok()?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return None;
        }
        let bytes = fs::read(path).ok()?;
        if bytes.get(..8)? != b"DIRC\0\0\0\x02" {
            return None;
        }
        let count = u32::from_be_bytes(bytes.get(8..12)?.try_into().ok()?) as usize;
        let hash_length = hash.len_in_bytes();
        let limit = bytes.len().checked_sub(hash_length)?;
        if bytes[limit..].iter().any(|byte| *byte != 0) {
            let mut digest = gix::hash::hasher(hash);
            digest.update(&bytes[..limit]);
            if digest.try_finalize().ok()?.as_slice() != &bytes[limit..] {
                return None;
            }
        }
        let mut position = 12usize;
        for _ in 0..count {
            let start = position;
            position = position.checked_add(42 + hash_length)?;
            let name_length = bytes
                .get(position..limit)?
                .iter()
                .position(|byte| *byte == 0)?;
            let size = position
                .checked_add(name_length)?
                .checked_add(1)?
                .checked_sub(start)?;
            position = start.checked_add(size.checked_add(7)? & !7)?;
        }
        while position < limit {
            let signature = bytes.get(position..position.checked_add(4)?)?;
            if ![b"TREE".as_slice(), b"EOIE".as_slice(), b"IEOT".as_slice()].contains(&signature) {
                return None;
            }
            let size = u32::from_be_bytes(
                bytes
                    .get(position + 4..position.checked_add(8)?)?
                    .try_into()
                    .ok()?,
            ) as usize;
            position = position.checked_add(8)?.checked_add(size)?;
        }
        (position == limit).then_some(())
    };
    parse().is_some()
}

pub(crate) fn status_threads(repo: &gix::Repository) -> Result<usize> {
    // Avoid starting one worker per logical CPU for small, latency-sensitive scans.
    Ok(repo
        .index_or_empty()?
        .entries()
        .len()
        .div_ceil(2000)
        .clamp(1, 8))
}

pub fn checkout_initial(repo: &gix::Repository) -> Result<()> {
    let repo = crate::lfs::prepare(repo.clone())?;
    let Some(head) = repo.head()?.try_peel_to_id()? else {
        return Ok(());
    };
    let tree = head.object()?.peel_to_tree()?.id;
    let mut index = repo.index_from_tree(&tree)?;
    let mut options =
        repo.checkout_options(gix_worktree::stack::state::attributes::Source::IdMapping)?;
    options.thread_limit = Some(options.thread_limit.unwrap_or(8).clamp(1, 8));
    options.destination_is_initially_empty = true;
    let outcome = gix_worktree_state::checkout(
        &mut index,
        repo.workdir().context("bare checkout")?,
        repo.objects.clone().into_arc()?,
        &gix::progress::Discard,
        &gix::progress::Discard,
        &std::sync::atomic::AtomicBool::new(false),
        options,
    )?;
    ensure!(
        outcome.collisions.is_empty() && outcome.errors.is_empty(),
        "checkout encountered collisions or errors"
    );
    index.write(Default::default())?;
    Ok(())
}
