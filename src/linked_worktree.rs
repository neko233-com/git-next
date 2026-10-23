use anyhow::{Context, Result, ensure};
use clap::{Parser, Subcommand};
use gix::{
    bstr::ByteSlice,
    worktree::{add::Head, remove::Force},
};
use std::{path::PathBuf, sync::atomic::AtomicBool};

#[derive(Parser, Debug)]
pub struct Options {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand, Debug)]
enum Command {
    List {
        #[arg(long = "porcelain")]
        porcelain: bool,
    },
    Add {
        path: PathBuf,
        #[arg(long = "detach")]
        detach: bool,
        revision: Option<String>,
    },
    Remove {
        path: PathBuf,
        #[arg(long)]
        force: bool,
    },
}

pub fn run(options: Options) -> Result<()> {
    match options.command {
        Command::List { porcelain } => list(porcelain),
        Command::Add {
            path,
            detach,
            revision,
        } => add(&path, detach, revision.as_deref()),
        Command::Remove { path, force } => remove(&path, force),
    }
}

fn list(porcelain: bool) -> Result<()> {
    let repo = crate::repository::open()?;
    for worktree in repo.worktrees_including_main()? {
        let worktree = worktree?;
        let path = worktree
            .workdir()
            .map(|value| value.canonicalize())
            .transpose()?
            .unwrap_or_else(|| worktree.git_dir().to_path_buf());
        let head = worktree
            .head()
            .ok()
            .and_then(|mut head| head.try_peel_to_id().ok().flatten())
            .map(|id| id.to_string())
            .unwrap_or_else(|| "(unknown)".into());
        if porcelain {
            println!("worktree {} {}", path.display(), head);
        } else {
            println!("{}  {}", path.display(), head);
        }
    }
    Ok(())
}

fn add(path: &PathBuf, detach: bool, revision: Option<&str>) -> Result<()> {
    let repo = crate::lfs::prepare(crate::repository::open()?)?;
    let revision = revision.unwrap_or("HEAD");
    let id = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?
        .peel_to_commit()?
        .id;
    let head = if detach {
        Head::Detached(id)
    } else {
        let name = repo
            .head_name()?
            .context("attached worktree add requires a checked-out branch")?;
        Head::Attached(name)
    };
    let interrupt = AtomicBool::new(false);
    repo.add_worktree(path, head, gix::progress::Discard, &interrupt)?;
    Ok(())
}

fn remove(path: &PathBuf, force: bool) -> Result<()> {
    ensure!(
        force,
        "worktree remove requires --force in this implementation"
    );
    let repo = crate::repository::open()?;
    repo.remove_worktree(path, Force::DiscardChanges, gix::progress::Discard)?;
    Ok(())
}
