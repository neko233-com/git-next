use anyhow::{Context, Result, bail};
use clap::Parser;
use std::{path::PathBuf, process::Command};

#[derive(Parser)]
#[command(
    version,
    about = "A repository terminal with an isolated git-next preview"
)]
struct Options {
    #[arg(long)]
    cwd: Option<PathBuf>,
    #[arg(long)]
    command: Option<String>,
    #[arg(long)]
    print_setup: bool,
}

fn main() -> std::process::ExitCode {
    match run() {
        Ok(code) => std::process::ExitCode::from(code),
        Err(error) => {
            eprintln!("git-terminal: {error:#}");
            std::process::ExitCode::FAILURE
        }
    }
}

fn run() -> Result<u8> {
    let options = Options::parse();
    let cwd = options
        .cwd
        .unwrap_or(std::env::current_dir()?)
        .canonicalize()?;
    if !cwd.is_dir() {
        bail!("--cwd must identify an existing directory");
    }
    let executable = std::env::current_exe()?;
    let client = executable.with_file_name(if cfg!(windows) {
        "git-next.exe"
    } else {
        "git-next"
    });
    if !client.is_file() {
        bail!("git-next is missing beside git-terminal; reinstall the complete preview");
    }
    let shell = shell();
    if options.print_setup {
        println!(
            "{}",
            serde_json::to_string_pretty(&serde_json::json!({
                "shell": shell, "cwd": cwd, "git_next": client,
                "system_git_replaced": false, "bash_compatibility": false,
            }))?
        );
        return Ok(0);
    }
    let mut process = Command::new(&shell);
    process.current_dir(cwd).env("GIT_NEXT_EXECUTABLE", client);
    if cfg!(windows) {
        process.arg("-NoLogo");
        if options.command.is_none() {
            process.arg("-NoExit");
        }
        let setup = "Set-Alias -Name git-next -Value $env:GIT_NEXT_EXECUTABLE; ";
        process
            .arg("-Command")
            .arg(format!("{setup}{}", options.command.unwrap_or_default()));
    } else if let Some(command) = options.command {
        process.args(["-c", &command]);
    } else {
        process.arg("-i");
    }
    let status = process.status().context("launching the native shell")?;
    Ok(status
        .code()
        .and_then(|code| u8::try_from(code).ok())
        .unwrap_or(1))
}

fn shell() -> PathBuf {
    if cfg!(windows) {
        for directory in std::env::split_paths(&std::env::var_os("PATH").unwrap_or_default()) {
            let path = directory.join("pwsh.exe");
            if path.is_file() {
                return path;
            }
        }
        PathBuf::from(std::env::var_os("SystemRoot").unwrap_or_else(|| "C:\\Windows".into()))
            .join("System32/WindowsPowerShell/v1.0/powershell.exe")
    } else {
        std::env::var_os("SHELL")
            .map(PathBuf::from)
            .filter(|path| path.is_absolute() && path.is_file())
            .unwrap_or_else(|| "/bin/sh".into())
    }
}
