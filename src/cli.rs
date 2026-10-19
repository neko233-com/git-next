use anyhow::Result;
use clap::{Parser, Subcommand};
use std::path::PathBuf;

#[derive(Parser)]
#[command(
    name = "git",
    version = concat!("version ", env!("CARGO_PKG_VERSION"), " (git-next experimental)"),
    about = "Native Git-compatible client (experimental)"
)]
pub struct Cli {
    #[arg(short = 'C', global = true, value_name = "DIRECTORY")]
    cwd: Option<PathBuf>,
    #[arg(long, global = true, value_name = "PRIVATE_KEY")]
    ssh_identity: Option<PathBuf>,
    #[arg(long, global = true, value_name = "KNOWN_HOSTS")]
    ssh_known_hosts: Option<PathBuf>,
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    Add {
        #[arg(short = 'A', long)]
        all: bool,
        paths: Vec<PathBuf>,
    },
    Commit {
        #[arg(short = 'm', long)]
        message: String,
    },
    Init {
        #[arg(default_value = ".")]
        directory: PathBuf,
        #[arg(long)]
        bare: bool,
        #[arg(short = 'b', long = "initial-branch", default_value = "main")]
        branch: String,
    },
    Clone {
        url: String,
        directory: Option<PathBuf>,
        #[arg(long)]
        bare: bool,
        #[arg(short = 'b', long)]
        branch: Option<String>,
        #[arg(long)]
        depth: Option<u32>,
    },
    Fetch {
        #[arg(default_value = "origin")]
        remote: String,
    },
    Pull {
        #[arg(long)]
        ff_only: bool,
        #[arg(default_value = "origin")]
        remote: String,
    },
    Status {
        #[arg(long)]
        porcelain: bool,
        #[arg(short)]
        short: bool,
    },
    Log {
        #[arg(short = 'n', long = "max-count", default_value_t = 20)]
        count: usize,
        #[arg(long)]
        oneline: bool,
        #[arg(default_value = "HEAD")]
        revision: String,
    },
    RevParse {
        #[arg(long)]
        show_toplevel: bool,
        #[arg(long)]
        git_dir: bool,
        #[arg(long)]
        is_bare_repository: bool,
        #[arg(default_value = "HEAD")]
        revision: String,
    },
    CatFile {
        #[arg(short = 't')]
        kind: bool,
        #[arg(short = 's')]
        size: bool,
        #[arg(short = 'p')]
        pretty: bool,
        object: String,
    },
    HashObject {
        #[arg(short = 'w')]
        write: bool,
        file: PathBuf,
    },
    LsFiles {
        #[arg(short = 's', long)]
        stage: bool,
    },
    ShowRef {
        #[arg(long)]
        head: bool,
    },
    Branch {
        name: Option<String>,
        #[arg(default_value = "HEAD")]
        revision: String,
    },
    Remote {
        #[command(subcommand)]
        command: Option<RemoteCommand>,
        #[arg(short = 'v')]
        verbose: bool,
    },
    Config {
        key: String,
        value: Option<String>,
        #[arg(long)]
        get: bool,
    },
    LsRemote {
        url: String,
    },
    Compatibility {
        #[arg(long)]
        json: bool,
    },
    Doctor,
}

#[derive(Subcommand)]
enum RemoteCommand {
    Add { name: String, url: String },
    GetUrl { name: String },
}

pub fn run() -> Result<()> {
    let cli = Cli::parse();
    crate::ssh::configure(cli.ssh_identity, cli.ssh_known_hosts)?;
    if let Some(path) = cli.cwd {
        std::env::set_current_dir(path)?;
    }
    match cli.command {
        Commands::Add { all, paths } => crate::write::add(all, paths),
        Commands::Commit { message } => crate::write::commit(&message),
        Commands::Init {
            directory,
            bare,
            branch,
        } => crate::repository::init(&directory, bare, &branch),
        Commands::Clone {
            url,
            directory,
            bare,
            branch,
            depth,
        } => crate::network::clone_repo(&url, directory, bare, branch, depth),
        Commands::Fetch { remote } => crate::network::fetch(&remote),
        Commands::Pull { remote, .. } => crate::network::pull(&remote),
        Commands::Status { porcelain, short } => crate::repository::status(porcelain || short),
        Commands::Log {
            count,
            oneline,
            revision,
        } => crate::repository::log(count, oneline, &revision),
        Commands::RevParse {
            show_toplevel,
            git_dir,
            is_bare_repository,
            revision,
        } => crate::repository::rev_parse(show_toplevel, git_dir, is_bare_repository, &revision),
        Commands::CatFile {
            kind,
            size,
            pretty: _,
            object,
        } => crate::repository::cat_file(&object, kind, size),
        Commands::HashObject { write, file } => crate::repository::hash_object(&file, write),
        Commands::LsFiles { stage } => crate::repository::ls_files(stage),
        Commands::ShowRef { head } => crate::repository::show_ref(head),
        Commands::Branch { name, revision } => {
            crate::repository::branch(name.as_deref(), &revision)
        }
        Commands::Remote { command, verbose } => match command {
            Some(RemoteCommand::Add { name, url }) => crate::repository::remote_add(&name, &url),
            Some(RemoteCommand::GetUrl { name }) => crate::repository::remote_url(&name),
            None => crate::repository::remotes(verbose),
        },
        Commands::Config { key, value, get: _ } => {
            crate::repository::config(&key, value.as_deref())
        }
        Commands::LsRemote { url } => crate::network::ls_remote(&url),
        Commands::Compatibility { json } => {
            if json {
                println!("{}", include_str!("../compatibility.json"));
            } else {
                println!("{}", include_str!("../docs/COMPATIBILITY.md"));
            }
            Ok(())
        }
        Commands::Doctor => {
            println!(
                "git-next {}\nengine: gitoxide 0.89\nssh: russh (embedded)\nexternal Git fallback: disabled\nexternal OpenSSH fallback: disabled\ndefault branch: main\nstatus: experimental; see compatibility",
                env!("CARGO_PKG_VERSION")
            );
            Ok(())
        }
    }
}
