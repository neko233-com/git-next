use anyhow::{Context, Result, bail, ensure};
use clap::Parser;
use gix::refs::transaction::{PreviousValue, RefEdit};
use std::{fs, io::BufRead};

#[derive(Parser, Debug)]
pub struct Options {
    #[command(subcommand)]
    command: Option<Command>,
    #[arg(short = 'm', long)]
    message: Option<String>,
    #[arg(short = 'u', long = "include-untracked")]
    include_untracked: bool,
}

#[derive(Parser, Debug)]
enum Command {
    Push {
        #[arg(short = 'm', long)]
        message: Option<String>,
        #[arg(short = 'u', long = "include-untracked")]
        include_untracked: bool,
    },
    Pop,
    Apply,
    List,
    Drop,
}

pub fn run(options: Options) -> Result<()> {
    match options.command {
        Some(Command::Push {
            message,
            include_untracked,
        }) => push(message.as_deref(), include_untracked),
        Some(Command::Pop) => pop(true),
        Some(Command::Apply) => pop(false),
        Some(Command::List) => list(),
        Some(Command::Drop) => drop_stash(),
        None => push(options.message.as_deref(), options.include_untracked),
    }
}

fn branch_label(repo: &gix::Repository) -> Result<String> {
    Ok(if let Some(name) = repo.head_name()? {
        name.shorten().to_string()
    } else {
        "HEAD".into()
    })
}

fn push(message: Option<&str>, include_untracked: bool) -> Result<()> {
    ensure!(
        !include_untracked,
        "git stash push -u is not implemented yet"
    );
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    crate::write::safe_state(&repo)?;
    crate::write::working_tree(&repo)?;
    let head = repo.head_id()?.detach();
    let head_tree = repo.find_commit(head)?.tree_id()?;
    let initial = repo.index_or_empty()?.to_owned();
    let index_tree = crate::write::index_tree(&repo, &initial)?;
    crate::write::add(true, Vec::new(), false)?;
    let repo = crate::repository::open()?;
    let updated = repo.index_or_empty()?.to_owned();
    let worktree_tree = crate::write::index_tree(&repo, &updated)?;
    if worktree_tree == head_tree && index_tree == head_tree {
        bail!("No local changes to save");
    }
    let branch = branch_label(&repo)?;
    let subject = short_subject(&repo, head)?;
    let index_message = format!("index on {branch}: {head} {subject}");
    let wip_message = message
        .map(str::to_owned)
        .unwrap_or_else(|| format!("WIP on {branch}: {head} {subject}"));
    let index_commit = repo.new_commit(index_message, index_tree, [head])?.id;
    let stash_commit = repo
        .new_commit(wip_message, worktree_tree, [head, index_commit])?
        .id;
    let previous = repo
        .find_reference("refs/stash")
        .ok()
        .map(|mut reference| reference.peel_to_id().map(|id| id.detach()))
        .transpose()?;
    repo.edit_references([RefEdit::update(
        "refs/stash".try_into()?,
        stash_commit,
        previous.map_or(PreviousValue::Any, |id| {
            PreviousValue::MustExistAndMatch(id.into())
        }),
        "stash: push",
    )])?;
    crate::restoration::reset_hard_quiet()?;
    println!(
        "Saved working directory and index state On {branch}: {}",
        message.unwrap_or("WIP")
    );
    Ok(())
}

fn short_subject(repo: &gix::Repository, id: gix::ObjectId) -> Result<String> {
    Ok(repo.find_commit(id)?.message()?.title.to_string())
}

fn read_stash(repo: &gix::Repository) -> Result<gix::ObjectId> {
    Ok(repo.find_reference("refs/stash")?.peel_to_id()?.detach())
}

fn stash_reflog(repo: &gix::Repository) -> Result<Vec<gix::ObjectId>> {
    let path = repo.common_dir().join("logs/refs/stash");
    if !path.is_file() {
        let id = read_stash(repo).ok();
        return Ok(id.into_iter().collect());
    }
    let file = fs::File::open(path)?;
    let reader = std::io::BufReader::new(file);
    let mut ids = Vec::new();
    for line in reader.lines() {
        let line = line?;
        let new = line
            .split_whitespace()
            .nth(1)
            .context("invalid stash reflog line")?;
        ids.push(gix::ObjectId::from_hex(new.as_bytes())?);
    }
    ids.reverse();
    Ok(ids)
}

fn list() -> Result<()> {
    let repo = crate::repository::open()?;
    for (index, id) in stash_reflog(&repo)?.into_iter().enumerate() {
        let commit = repo.find_commit(id)?;
        let message = commit.message_raw()?.to_string();
        let first_line = message.lines().next().unwrap_or("stash");
        println!("stash@{{{index}}}: {first_line}");
    }
    Ok(())
}

fn apply_tree(repo: &gix::Repository, stash_id: gix::ObjectId) -> Result<()> {
    let commit = repo.find_commit(stash_id)?;
    let tree = commit.tree_id()?;
    let old_index = repo.index_or_empty()?;
    let lock = crate::write::IndexLock::acquire(repo)?;
    let index = repo.index_from_tree(tree.as_ref())?;
    let paths = crate::worktree::files(&old_index)?
        .into_keys()
        .chain(crate::worktree::files(&index)?.into_keys())
        .collect();
    crate::worktree::apply(
        repo,
        &old_index,
        lock,
        crate::worktree::Update {
            index,
            paths: Some(paths),
            edits: Vec::new(),
            target: None,
            require_clean: false,
            publish_index: true,
            preserve_index: false,
            overwrite_untracked: false,
            worktree_attributes: true,
            expected_head: None,
            file_views: None,
            metadata: Vec::new(),
            allow_merge_state: false,
            allow_sequencer_state: false,
        },
    )
}

fn pop(remove: bool) -> Result<()> {
    let repo = crate::repository::open()?;
    let stash_id = read_stash(&repo)?;
    apply_tree(&repo, stash_id)?;
    if remove {
        drop_stash()?;
    }
    Ok(())
}

fn drop_stash() -> Result<()> {
    let repo = crate::repository::open()?;
    let stash_id = read_stash(&repo)?;
    let entries = stash_reflog(&repo)?;
    ensure!(!entries.is_empty(), "no stash entries");
    if entries.len() == 1 {
        repo.edit_references([RefEdit::delete(
            "refs/stash".try_into()?,
            PreviousValue::MustExistAndMatch(stash_id.into()),
        )])?;
        let _ = fs::remove_file(repo.common_dir().join("logs/refs/stash"));
        return Ok(());
    }
    let next = entries[1];
    repo.edit_references([RefEdit::update(
        "refs/stash".try_into()?,
        next,
        PreviousValue::MustExistAndMatch(stash_id.into()),
        "stash: drop",
    )])?;
    Ok(())
}
