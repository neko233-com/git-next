mod cache;
mod hooks;
mod pointer;
mod process;
mod setup;
mod transfer;

pub(crate) use hooks::check as check_hooks;
pub(crate) use transfer::upload_before_push;

use anyhow::{Context, Result, ensure};
use cache::Cache;
use clap::{Args, Parser, Subcommand};
use pointer::{LIMIT, Pointer};
use std::{
    fs,
    io::{Read, Write},
    path::PathBuf,
};

pub(crate) fn prepare(mut repo: gix::Repository) -> Result<gix::Repository> {
    let config = repo.config_snapshot();
    let process = config.string("filter.lfs.process");
    let clean = config.string("filter.lfs.clean");
    let smudge = config.string("filter.lfs.smudge");
    let standard = match process.as_ref().map(|v| v.as_slice()) {
        Some(b"git-lfs filter-process" | b"git-lfs filter-process --skip") => true,
        None => {
            clean
                .as_ref()
                .is_some_and(|v| v.as_slice() == b"git-lfs clean -- %f")
                && smudge.as_ref().is_some_and(|v| {
                    matches!(
                        v.as_slice(),
                        b"git-lfs smudge -- %f" | b"git-lfs smudge --skip -- %f"
                    )
                })
        }
        _ => false,
    };
    if standard {
        let executable = std::env::current_exe()?.with_file_name(if cfg!(windows) {
            "git-next-lfs-filter.exe"
        } else {
            "git-next-lfs-filter"
        });
        ensure!(
            executable.is_file(),
            "native LFS filter executable is missing beside git-next"
        );
        let mut snapshot = repo.config_snapshot_mut();
        snapshot.append_config(
            [format!(
                "filter.lfs.process={}",
                executable.to_string_lossy()
            )],
            gix::config::Source::Api,
        )?;
        snapshot.commit()?;
    }
    Ok(repo)
}

pub fn filter_main() -> std::process::ExitCode {
    let outcome = (|| -> Result<()> {
        let repo = crate::repository::open()?;
        let skip = repo
            .config_snapshot()
            .string("filter.lfs.process")
            .is_some_and(|v| v.ends_with(b" --skip"))
            || repo
                .config_snapshot()
                .string("filter.lfs.smudge")
                .is_some_and(|v| v.as_slice() == b"git-lfs smudge --skip -- %f");
        process::run(&repo, &Cache::new(&repo)?, skip)
    })();
    match outcome {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("git-next LFS filter: {error:#}");
            std::process::ExitCode::FAILURE
        }
    }
}

#[derive(Args)]
pub(crate) struct Options {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    Install {
        #[arg(long, required = true)]
        local: bool,
        #[arg(long)]
        skip_smudge: bool,
    },
    Track {
        patterns: Vec<String>,
    },
    PrePush {
        remote: String,
        url: String,
    },
    PostCommit,
    PostCheckout {
        previous: String,
        current: String,
        flag: String,
    },
    PostMerge {
        squash: String,
    },
    Fetch {
        #[arg(default_value = "origin")]
        remote: String,
        revisions: Vec<String>,
    },
    Push {
        #[arg(default_value = "origin")]
        remote: String,
        revisions: Vec<String>,
    },
    Version,
    Clean {
        path: Option<PathBuf>,
    },
    Smudge {
        #[arg(long)]
        skip: bool,
        path: Option<PathBuf>,
    },
    FilterProcess {
        #[arg(long)]
        skip: bool,
    },
    Pointer {
        #[arg(long)]
        file: Option<PathBuf>,
        #[arg(long)]
        stdin: bool,
        #[arg(long)]
        check: bool,
        #[arg(long, conflicts_with = "no_strict")]
        strict: bool,
        #[arg(long)]
        no_strict: bool,
    },
    Env,
}

#[derive(Parser)]
#[command(name = "git-lfs", about = "git-next native Git LFS client")]
struct Standalone {
    #[command(flatten)]
    options: Options,
}

pub fn standalone_main() -> std::process::ExitCode {
    let arguments: Vec<_> = std::env::args_os().collect();
    if arguments.len() == 2
        && arguments
            .get(1)
            .is_some_and(|v| v == "--version" || v == "version")
    {
        println!(
            "git-lfs/{} (git-next native; {})",
            env!("CARGO_PKG_VERSION"),
            std::env::consts::ARCH
        );
        return std::process::ExitCode::SUCCESS;
    }
    let outcome = run(Standalone::parse().options);
    match outcome {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(error) => {
            if let Some(status) = error.downcast_ref::<crate::CommandStatus>() {
                return status.0.into();
            }
            eprintln!("git-next LFS: {error:#}");
            std::process::ExitCode::FAILURE
        }
    }
}

pub(crate) fn run(options: Options) -> Result<()> {
    match options.command {
        Command::Version => {
            println!(
                "git-lfs/{} (git-next native; {})",
                env!("CARGO_PKG_VERSION"),
                std::env::consts::ARCH
            );
            Ok(())
        }
        Command::Pointer {
            file,
            stdin,
            check,
            strict,
            no_strict: _,
        } => {
            ensure!(
                stdin ^ file.is_some(),
                "pointer requires exactly one of --file or --stdin"
            );
            if check {
                let mut input: Box<dyn Read> = if let Some(path) = file {
                    Box::new(fs::File::open(path)?)
                } else {
                    Box::new(std::io::stdin().lock())
                };
                let mut bytes = Vec::new();
                input.by_ref().take(LIMIT as u64).read_to_end(&mut bytes)?;
                let pointer = Pointer::parse(&bytes).map_err(|_| crate::CommandStatus(1))?;
                ensure!(
                    !strict || pointer.encode() == bytes,
                    crate::CommandStatus(2)
                );
                Ok(())
            } else {
                ensure!(!strict && !stdin, "pointer generation requires --file");
                let pointer = cache::hash(
                    fs::File::open(file.context("missing --file")?)?,
                    std::io::sink(),
                )?;
                std::io::stdout().lock().write_all(&pointer.encode())?;
                Ok(())
            }
        }
        command => {
            let repo = crate::repository::open()?;
            let cache = Cache::new(&repo)?;
            let mut download = None;
            let upload = matches!(&command, Command::Push { .. });
            match command {
                Command::Install {
                    local: _,
                    skip_smudge,
                } => setup::install(&repo, skip_smudge),
                Command::Track { patterns } => setup::track(&repo, &patterns),
                Command::PrePush { remote, url: _ } => {
                    use std::io::BufRead;
                    check_hooks(&repo, &["pre-push"], None)?;
                    let mut tips = Vec::new();
                    let mut known = Vec::new();
                    for line in std::io::stdin().lock().lines() {
                        let line = line?;
                        let parts: Vec<_> = line.split_whitespace().collect();
                        ensure!(parts.len() == 4, "invalid LFS pre-push input");
                        let new = gix::ObjectId::from_hex(parts[1].as_bytes())?;
                        let old = gix::ObjectId::from_hex(parts[3].as_bytes())?;
                        ensure!(
                            new.kind() == repo.object_hash() && old.kind() == repo.object_hash(),
                            "LFS pre-push object format mismatch"
                        );
                        tips.push(new);
                        known.push(old);
                    }
                    upload_before_push(&repo, &remote, tips, known, None)
                }
                Command::PostCommit => check_hooks(&repo, &["post-commit"], None),
                Command::PostCheckout {
                    previous: _,
                    current: _,
                    flag,
                } => {
                    ensure!(flag == "0" || flag == "1", "invalid checkout hook flag");
                    check_hooks(&repo, &["post-checkout"], None)
                }
                Command::PostMerge { squash } => {
                    ensure!(squash == "0" || squash == "1", "invalid merge hook flag");
                    check_hooks(&repo, &["post-merge"], None)
                }
                Command::Clean { path: _ } => {
                    cache.clean(std::io::stdin().lock(), std::io::stdout().lock())
                }
                Command::Smudge { skip, path: _ } => smudge(
                    &repo,
                    &cache,
                    std::io::stdin().lock(),
                    std::io::stdout().lock(),
                    skip,
                    &mut download,
                ),
                Command::FilterProcess { skip } => process::run(&repo, &cache, skip),
                Command::Fetch { remote, revisions } | Command::Push { remote, revisions } => {
                    let revisions = if revisions.is_empty() {
                        vec!["HEAD".into()]
                    } else {
                        revisions
                    };
                    let tips = revisions
                        .iter()
                        .map(|revision| {
                            repo.rev_parse_single(revision.as_str())
                                .map(|id| id.detach())
                        })
                        .collect::<std::result::Result<Vec<_>, _>>()?;
                    let pointers =
                        transfer::pointers(&repo, tips, &std::collections::HashSet::new())?;
                    if !pointers.is_empty() {
                        let session = transfer::Session::new(&repo, &remote, upload)?;
                        if upload {
                            session.upload(&cache, &pointers, None)?;
                        } else {
                            session.download(&cache, &pointers, None)?;
                        }
                    }
                    Ok(())
                }
                Command::Env => {
                    println!(
                        "git-lfs/{} (git-next native)\nLocalWorkingDir={}\nLocalGitDir={}\nLocalMediaDir={}",
                        env!("CARGO_PKG_VERSION"),
                        repo.workdir()
                            .map(|p| p.display().to_string())
                            .unwrap_or_default(),
                        repo.git_dir().display(),
                        cache.root().join("objects").display()
                    );
                    Ok(())
                }
                _ => unreachable!(),
            }
        }
    }
}

fn smudge<'repo>(
    repo: &'repo gix::Repository,
    cache: &Cache,
    mut input: impl Read,
    mut output: impl Write,
    skip: bool,
    download: &mut Option<transfer::Session<'repo>>,
) -> Result<()> {
    let mut prefix = Vec::with_capacity(LIMIT);
    input.by_ref().take(LIMIT as u64).read_to_end(&mut prefix)?;
    let pointer = Pointer::parse(&prefix).ok();
    let skip = skip
        || std::env::var("GIT_LFS_SKIP_SMUDGE")
            .is_ok_and(|v| !v.is_empty() && v != "0" && !v.eq_ignore_ascii_case("false"));
    match pointer {
        Some(pointer) if pointer.size != 0 && !skip => {
            ensure!(
                pointer.extensions.is_empty(),
                "LFS pointer extensions need their configured transformation implementation"
            );
            let mut file = match cache.verified(&pointer)? {
                Some(file) => file,
                None => {
                    if download.is_none() {
                        *download = Some(transfer::Session::new(repo, "origin", false)?);
                    }
                    download
                        .as_ref()
                        .context("missing LFS download session")?
                        .download(cache, std::slice::from_ref(&pointer), None)?;
                    cache
                        .verified(&pointer)?
                        .context("LFS object is still missing after download")?
                }
            };
            std::io::copy(&mut file, &mut output)?;
        }
        _ => {
            output.write_all(&prefix)?;
            std::io::copy(&mut input, &mut output)?;
        }
    }
    Ok(())
}
