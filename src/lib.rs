mod cli;
mod configuration;
mod credentials;
mod difference;
mod eol;
mod identity;
mod language;
mod lfs;
mod merging;
mod metadata;
mod network;
mod push;
mod repository;
mod restoration;
mod runtime;
mod ssh;
mod status_branch;
mod status_ignored;
mod status_long;
mod status_output;
mod status_v2;
mod worktree;
mod write;

pub fn lfs_main() -> std::process::ExitCode {
    lfs::standalone_main()
}

pub fn lfs_filter_main() -> std::process::ExitCode {
    if std::env::args_os()
        .nth(1)
        .is_some_and(|value| value == "--version")
    {
        println!("git-next-lfs-filter version {}", env!("CARGO_PKG_VERSION"));
        return std::process::ExitCode::SUCCESS;
    }
    lfs::filter_main()
}

pub fn main() -> std::process::ExitCode {
    match identity::fast_version().unwrap_or_else(cli::run) {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(error) => {
            if let Some(status) = error.downcast_ref::<CommandStatus>() {
                return std::process::ExitCode::from(status.0);
            }
            eprintln!("{}{}", language::text("fatal: "), language::error(&error));
            std::process::ExitCode::from(128)
        }
    }
}

#[derive(Debug)]
pub(crate) struct CommandStatus(pub u8);
impl std::fmt::Display for CommandStatus {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "command exited with status {}", self.0)
    }
}
impl std::error::Error for CommandStatus {}
