use anyhow::{Context, Result, bail, ensure};
use gix::bstr::ByteSlice;
use std::{collections::BTreeMap, fs, io::Write, path::Path};

pub fn open() -> Result<gix::Repository> {
    gix::discover(".").context("not a Git repository")
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

pub fn init(directory: &Path, bare: bool, branch: &str) -> Result<()> {
    let _: gix::refs::FullName = format!("refs/heads/{branch}").try_into()?;
    ensure!(!branch.starts_with('-'), "invalid initial branch");
    ensure!(
        !directory.join("HEAD").exists() && !directory.join(".git").exists(),
        "destination already contains a repository; refusing to change its HEAD"
    );
    let repo = if bare {
        gix::init_bare(directory)?
    } else {
        gix::init(directory)?
    };
    atomic_write(
        &repo.git_dir().join("HEAD"),
        format!("ref: refs/heads/{branch}\n").as_bytes(),
    )?;
    println!(
        "Initialized {}Git repository in {}",
        if bare { "bare " } else { "" },
        repo.git_dir().display()
    );
    Ok(())
}

pub fn config_set(repo: &gix::Repository, key: &str, value: &str) -> Result<()> {
    let mut file = repo.config_file_mut(repo.common_dir().join("config"))?;
    file.set_raw_value(key, value)?;
    file.commit()?;
    Ok(())
}

pub fn config(key: &str, value: Option<&str>) -> Result<()> {
    let repo = open()?;
    if let Some(value) = value {
        return config_set(&repo, key, value);
    }
    let config = repo.config_snapshot();
    let value = config
        .string(key)
        .with_context(|| format!("configuration key {key:?} is unset"))?;
    println!("{value}");
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

pub fn rev_parse(top: bool, dir: bool, bare: bool, revision: &str) -> Result<()> {
    let repo = open()?;
    if top {
        println!(
            "{}",
            repo.workdir()
                .context("bare repository has no working tree")?
                .canonicalize()?
                .display()
        )
    } else if dir {
        println!("{}", repo.git_dir().display())
    } else if bare {
        println!("{}", repo.is_bare())
    } else {
        println!("{}", repo.rev_parse_single(revision.as_bytes().as_bstr())?)
    }
    Ok(())
}

pub fn log(count: usize, oneline: bool, revision: &str) -> Result<()> {
    let repo = open()?;
    let id = repo.rev_parse_single(revision.as_bytes().as_bstr())?;
    for info in id.ancestors().all()?.take(count) {
        let commit = repo.find_commit(info?.id)?;
        let message = commit.message_raw()?;
        if oneline {
            println!(
                "{} {}",
                commit.id.to_hex_with_len(12),
                message.lines().next().unwrap_or_default().as_bstr()
            )
        } else {
            let author = commit.author()?;
            println!(
                "commit {}\nAuthor: {} <{}>\n\n    {}\n",
                commit.id,
                author.name,
                author.email,
                message.as_bstr()
            )
        }
    }
    Ok(())
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

pub fn status(porcelain: bool) -> Result<()> {
    let repo = open()?;
    ensure!(!repo.is_bare(), "status requires a working tree");
    let mut paths: BTreeMap<String, [char; 2]> = BTreeMap::new();
    for item in repo
        .status(gix::progress::Discard)?
        .into_iter(std::iter::empty())?
    {
        let item = item?;
        let path = item.location().to_string();
        let entry = paths.entry(path).or_insert([' '; 2]);
        match item {
            gix::status::Item::IndexWorktree(change) => {
                if let Some(summary) = change.summary() {
                    use gix::status::plumbing::index_as_worktree_with_renames::Summary;
                    match summary {
                        Summary::Added => *entry = ['?', '?'],
                        Summary::Removed => entry[1] = 'D',
                        Summary::Modified => entry[1] = 'M',
                        Summary::TypeChange => entry[1] = 'T',
                        Summary::IntentToAdd => entry[1] = 'A',
                        Summary::Conflict => *entry = ['U', 'U'],
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
                    Change::Modification { .. } => 'M',
                    Change::Rewrite { copy, .. } => {
                        if copy {
                            'C'
                        } else {
                            'R'
                        }
                    }
                };
            }
        }
    }
    if !porcelain {
        println!(
            "On {}",
            repo.head_name()?
                .map(|n| n.shorten().to_string())
                .unwrap_or_else(|| "detached HEAD".into())
        );
    }
    for (path, code) in paths {
        if code != [' '; 2] {
            println!("{}{} {path}", code[0], code[1])
        }
    }
    Ok(())
}

pub fn checkout_clean(repo: &gix::Repository, target: gix::ObjectId) -> Result<()> {
    ensure!(repo.workdir().is_some(), "checkout requires a working tree");
    if repo
        .status(gix::progress::Discard)?
        .into_iter(std::iter::empty())?
        .next()
        .is_some()
    {
        bail!("working tree is not clean; refusing to overwrite changes")
    }
    let _ = target;
    bail!("safe incremental checkout is not implemented yet; no worktree files were changed")
}

pub fn checkout_initial(repo: &gix::Repository) -> Result<()> {
    let Some(head) = repo.head()?.try_peel_to_id()? else {
        return Ok(());
    };
    let tree = head.object()?.peel_to_tree()?.id;
    let mut index = repo.index_from_tree(&tree)?;
    let mut options =
        repo.checkout_options(gix_worktree::stack::state::attributes::Source::IdMapping)?;
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
