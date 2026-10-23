use anyhow::{Context, Result, ensure};
use clap::{Parser, Subcommand};
use gix::bstr::{BStr, ByteSlice};
use std::path::PathBuf;

#[derive(Parser, Debug)]
pub struct Options {
    #[command(subcommand)]
    command: Option<Command>,
}

#[derive(Subcommand, Debug)]
enum Command {
    Status {
        paths: Vec<PathBuf>,
    },
    Init {
        paths: Vec<PathBuf>,
    },
    Update {
        #[arg(long)]
        init: bool,
        paths: Vec<PathBuf>,
    },
}

pub fn run(options: Options) -> Result<()> {
    match options.command {
        Some(Command::Status { paths }) => status(&paths),
        Some(Command::Init { paths }) => init_submodules(&paths),
        Some(Command::Update { init, paths }) => update(init, &paths),
        None => status(&[]),
    }
}

fn modules(repo: &gix::Repository) -> Result<Vec<gix::Submodule<'_>>> {
    let Some(iter) = repo.submodules()? else {
        return Ok(Vec::new());
    };
    Ok(iter.collect())
}

fn matches_path(name: &BStr, paths: &[PathBuf]) -> bool {
    paths.iter().any(|path| {
        name == path.to_string_lossy().as_bytes().as_bstr()
            || name == path.as_os_str().as_encoded_bytes().as_bstr()
    })
}

fn selected<'repo>(
    repo: &'repo gix::Repository,
    paths: &[PathBuf],
) -> Result<Vec<gix::Submodule<'repo>>> {
    let mut list = modules(repo)?;
    if !paths.is_empty() {
        list.retain(|module| matches_path(module.name(), paths));
    }
    Ok(list)
}

fn submodule_dir(repo: &gix::Repository, module: &gix::Submodule<'_>) -> Result<PathBuf> {
    let root = repo
        .workdir()
        .context("submodule commands require a working tree")?;
    let relative = module.path()?;
    Ok(root.join(gix::path::from_bstr(relative.as_bstr())?))
}

fn status(paths: &[PathBuf]) -> Result<()> {
    let repo = crate::repository::open()?;
    for module in selected(&repo, paths)? {
        let name = module.name().to_string();
        let path = module.path()?.to_string();
        let id = module
            .index_id()?
            .map(|id| id.to_string())
            .unwrap_or_else(|| "-".into());
        let head = module
            .head_id()?
            .map(|id| id.to_string())
            .unwrap_or_else(|| "-".into());
        println!(" {id} {name} ({path}) {head}");
    }
    Ok(())
}

fn init_submodules(paths: &[PathBuf]) -> Result<()> {
    let repo = crate::repository::open()?;
    for module in selected(&repo, paths)? {
        let destination = submodule_dir(&repo, &module)?;
        let git_file = destination.join(".git");
        if git_file.is_dir() || git_file.is_file() {
            continue;
        }
        std::fs::create_dir_all(&destination)?;
        let git_path = repo
            .common_dir()
            .join("modules")
            .join(gix::path::from_bstr(module.validated_name()?)?);
        std::fs::create_dir_all(&git_path)?;
        std::fs::write(git_file, format!("gitdir: {}\n", git_path.display()))?;
    }
    Ok(())
}

fn update(run_init: bool, paths: &[PathBuf]) -> Result<()> {
    if run_init {
        init_submodules(paths)?;
    }
    let repo = crate::repository::open()?;
    for module in selected(&repo, paths)? {
        let url = module.url()?.to_string();
        let destination = submodule_dir(&repo, &module)?;
        if destination.join("HEAD").exists() {
            continue;
        }
        if destination.is_dir() {
            let entries = destination
                .read_dir()?
                .filter_map(|entry| entry.ok())
                .collect::<Vec<_>>();
            let only_gitfile = entries.len() == 1
                && entries[0].file_name() == ".git"
                && entries[0].file_type().is_ok_and(|kind| kind.is_file());
            if only_gitfile {
                std::fs::remove_file(destination.join(".git"))?;
                std::fs::remove_dir(&destination)?;
            } else {
                ensure!(entries.is_empty(), "submodule destination is not empty");
            }
        }
        crate::network::clone_repo(&url, Some(destination), false, None, None)?;
    }
    Ok(())
}
