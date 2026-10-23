use anyhow::{Context, Result, ensure};
#[cfg(windows)]
use std::io::Write;
use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Stdio},
};

pub(crate) fn directory(repo: &gix::Repository) -> Result<PathBuf> {
    if let Some(path) = repo.config_snapshot().trusted_path("core.hooksPath")? {
        Ok(if path.is_absolute() {
            path
        } else {
            repo.workdir().unwrap_or(repo.git_dir()).join(path)
        })
    } else {
        Ok(repo.common_dir().join("hooks"))
    }
}

fn lfs_standard(name: &str) -> String {
    format!(
        "#!/bin/sh\ncommand -v git-lfs >/dev/null 2>&1 || {{ printf >&2 \"\\n%s\\n\\n\" \"This repository is configured for Git LFS but 'git-lfs' was not found on your path. If you no longer wish to use Git LFS, remove this hook by deleting the '{name}' file in the hooks directory (set by 'core.hookspath'; usually '.git/hooks').\"; exit 2; }}\ngit lfs {name} \"$@\""
    )
}

fn is_builtin_lfs(name: &str, contents: &str) -> bool {
    let trimmed = contents.trim_end();
    trimmed == lfs_standard(name) || trimmed == format!("#!/bin/sh\ngit lfs {name} \"$@\"")
}

fn git_lfs_executable() -> Result<PathBuf> {
    let sibling = std::env::current_exe()?
        .parent()
        .context("missing executable directory")?
        .join(if cfg!(windows) {
            "git-lfs.exe"
        } else {
            "git-lfs"
        });
    ensure!(
        sibling.is_file(),
        "git-lfs is not bundled next to this executable"
    );
    Ok(sibling)
}

fn run_lfs(name: &str, args: &[&str]) -> Result<()> {
    let mut command = Command::new(git_lfs_executable()?);
    command.arg(name);
    for arg in args {
        command.arg(arg);
    }
    run_command(&mut command)
}

#[cfg(windows)]
fn run_script(path: &Path, args: &[&str]) -> Result<()> {
    let absolute = path.to_path_buf();
    let mut batch_wrapper = None;
    let extension = absolute
        .extension()
        .and_then(|value| value.to_str())
        .unwrap_or("")
        .to_ascii_lowercase();
    let mut command = if extension == "ps1" {
        let mut cmd = Command::new(
            std::env::var_os("SystemRoot")
                .map(|root| {
                    PathBuf::from(root).join("System32/WindowsPowerShell/v1.0/powershell.exe")
                })
                .context("SystemRoot is unset")?,
        );
        cmd.args(["-NoProfile", "-ExecutionPolicy", "Bypass", "-File"]);
        cmd.arg(&absolute);
        cmd
    } else if extension == "bat" || extension == "cmd" {
        let mut cmd = Command::new("cmd.exe");
        cmd.args(["/D", "/C"]).arg(&absolute);
        cmd
    } else {
        let contents = fs::read(&absolute)?;
        if contents.starts_with(b"#!") {
            let header = String::from_utf8_lossy(&contents);
            let interpreter = header
                .lines()
                .next()
                .and_then(|line| line.strip_prefix("#!"))
                .and_then(|line| line.split_whitespace().next())
                .context("hook shebang is invalid")?;
            let mut cmd = Command::new(interpreter);
            cmd.arg(&absolute);
            cmd
        } else if contents.starts_with(b"@") {
            let mut temporary = tempfile::Builder::new().suffix(".cmd").tempfile()?;
            temporary.write_all(&contents)?;
            temporary.as_file().sync_all()?;
            let script = temporary.path().to_path_buf();
            batch_wrapper = Some(temporary);
            let mut cmd = Command::new("cmd.exe");
            cmd.args(["/D", "/C"]).arg(script);
            cmd
        } else {
            let mut cmd = Command::new("cmd.exe");
            cmd.args(["/D", "/C"]).arg(&absolute);
            cmd
        }
    };
    for arg in args {
        command.arg(arg);
    }
    let result = run_command(&mut command);
    drop(batch_wrapper);
    result
}

#[cfg(not(windows))]
fn run_script(path: &Path, args: &[&str]) -> Result<()> {
    let mut command = Command::new(path);
    for arg in args {
        command.arg(arg);
    }
    run_command(&mut command)
}

fn run_command(command: &mut Command) -> Result<()> {
    command
        .stdin(Stdio::inherit())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    let status = command.status().context("hook process failed to start")?;
    ensure!(
        status.success(),
        "hook exited with status {}",
        status.code().unwrap_or(1)
    );
    Ok(())
}

pub(crate) fn run(repo: &gix::Repository, name: &str, args: &[&str]) -> Result<()> {
    let path = directory(repo)?.join(name);
    if !path.is_file() {
        return Ok(());
    }
    let metadata = fs::symlink_metadata(&path)?;
    ensure!(
        metadata.is_file() && !metadata.file_type().is_symlink(),
        "hook {name} must be a regular file"
    );
    let contents = fs::read(&path)?;
    if contents.starts_with(b"#!") || contents.len() < 4096 {
        let text = String::from_utf8_lossy(&contents);
        if is_builtin_lfs(name, &text) {
            return run_lfs(name, args);
        }
    }
    run_script(&path, args)
}

pub(crate) fn run_many(repo: &gix::Repository, names: &[&str], args: &[&str]) -> Result<()> {
    for name in names {
        run(repo, name, args)?;
    }
    Ok(())
}

pub(crate) fn check_lfs_lockable(
    repo: &gix::Repository,
    names: &[&str],
    target: Option<&gix::index::State>,
) -> Result<()> {
    let directory = directory(repo)?;
    let mut lfs = false;
    for name in names {
        let path = directory.join(name);
        if !path.is_file() {
            continue;
        }
        let contents = fs::read_to_string(&path)?.replace("\r\n", "\n");
        if is_builtin_lfs(name, &contents) {
            lfs = true;
        }
    }
    if !lfs {
        return Ok(());
    }
    let config = repo.config_snapshot();
    ensure!(
        !config.boolean("lfs.locksverify").unwrap_or(false),
        "LFS lock verification needs native locking support"
    );
    if config.boolean("lfs.setlockableReadOnly") == Some(false) {
        return Ok(());
    }
    let current = repo.index_or_empty()?;
    let current_index: &gix::index::State = &current;
    for (index, source) in [
        (
            Some(current_index),
            gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
        ),
        (
            target,
            gix_worktree::stack::state::attributes::Source::IdMapping,
        ),
    ] {
        let Some(index) = index else {
            continue;
        };
        let mut stack = repo.attributes_only(index, source)?;
        let mut outcome = gix::attrs::search::Outcome::default();
        outcome.initialize_with_selection(&Default::default(), ["lockable"]);
        for entry in index.entries() {
            stack
                .at_entry(entry.path(index), Some(entry.mode))?
                .matching_attributes(&mut outcome);
            let attribute = outcome
                .iter_selected()
                .next()
                .context("lockable attribute selection")?;
            ensure!(
                !attribute.assignment.state.is_set(),
                "LFS lockable file permissions need native locking support"
            );
        }
    }
    Ok(())
}
