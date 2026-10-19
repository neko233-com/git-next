mod cli;
mod network;
mod repository;
mod ssh;
mod write;

pub fn main() -> std::process::ExitCode {
    match cli::run() {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("fatal: {error:#}");
            std::process::ExitCode::from(128)
        }
    }
}
