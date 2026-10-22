use anyhow::Result;
use clap::{CommandFactory, FromArgMatches, Parser, Subcommand};
use std::path::PathBuf;

#[derive(Parser)]
#[command(
    name = "git",
    version = concat!(env!("CARGO_PKG_VERSION"), " (git-next experimental)"),
    about = "git-next: native Git-compatible client (experimental)"
)]
pub struct Cli {
    #[arg(
        long,
        global = true,
        value_name = "LANGUAGE",
        help = "Select the interface language for this invocation"
    )]
    language: Option<String>,
    #[arg(short = 'C', global = true, value_name = "DIRECTORY")]
    cwd: Vec<PathBuf>,
    #[arg(short = 'c', value_name = "KEY=VALUE")]
    config: Vec<String>,
    #[arg(long)]
    git_dir: Option<PathBuf>,
    #[arg(long, conflicts_with = "bare")]
    work_tree: Option<PathBuf>,
    #[arg(long)]
    bare: bool,
    #[arg(long)]
    no_pager: bool,
    #[arg(long, global = true, value_name = "PRIVATE_KEY")]
    ssh_identity: Option<PathBuf>,
    #[arg(long, global = true, value_name = "KNOWN_HOSTS")]
    ssh_known_hosts: Option<PathBuf>,
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    Language(crate::language::Options),
    Diff(crate::difference::Options),
    Show(crate::difference::ShowOptions),
    Restore(crate::restoration::RestoreOptions),
    Reset(crate::restoration::ResetOptions),
    Eol(crate::eol::Options),
    Lfs(crate::lfs::Options),
    Push(crate::push::Options),
    Add {
        #[arg(short = 'A', long)]
        all: bool,
        #[arg(long)]
        renormalize: bool,
        paths: Vec<PathBuf>,
    },
    Commit {
        #[arg(short = 'm', long)]
        message: Option<String>,
        #[arg(long)]
        no_edit: bool,
        #[arg(long)]
        cleanup: Option<String>,
    },
    Switch {
        #[arg(short = 'c', long)]
        create: Option<String>,
        #[arg(long)]
        detach: bool,
        target: Option<String>,
    },
    Checkout {
        #[arg(short = 'b')]
        create: Option<String>,
        #[arg(long)]
        detach: bool,
        target: Option<String>,
    },
    Merge(crate::merging::Options),
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
        #[arg(long, required = true)]
        ff_only: bool,
        remote: Option<String>,
    },
    Status(crate::status_output::Options),
    Log {
        #[arg(short = 'n', long = "max-count")]
        count: Option<usize>,
        #[arg(long)]
        oneline: bool,
        #[arg(long, alias = "pretty")]
        format: Option<String>,
        #[arg(long)]
        date: Option<String>,
        #[arg(short = 'z')]
        nul: bool,
        #[arg(long)]
        no_decorate: bool,
        #[arg(long)]
        no_color: bool,
        #[arg(short = 's', long = "no-patch")]
        no_patch: bool,
        #[arg(allow_negative_numbers = true)]
        revision: Vec<String>,
    },
    RevParse {
        #[arg(long)]
        show_toplevel: bool,
        #[arg(long)]
        git_dir: bool,
        #[arg(long)]
        absolute_git_dir: bool,
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
    Config(crate::configuration::Options),
    LsRemote {
        url: String,
    },
    Compatibility {
        #[arg(long)]
        json: bool,
    },
    Version {
        #[arg(long)]
        build_options: bool,
        #[arg(long)]
        json: bool,
    },
    Doctor {
        #[arg(long)]
        json: bool,
    },
}

#[derive(Subcommand)]
enum RemoteCommand {
    Add { name: String, url: String },
    GetUrl { name: String },
    Show,
}

pub fn run() -> Result<()> {
    let arguments: Vec<_> = std::env::args_os().collect();
    crate::language::initialize(&arguments)?;
    let mut command = Cli::command();
    command.build();
    let command = crate::language::localize_command(command);
    let arguments = crate::status_output::normalize_args(arguments, &command);
    let matches = command
        .try_get_matches_from(arguments)
        .unwrap_or_else(|error| {
            if !crate::language::is_source() && error.use_stderr() {
                eprintln!(
                    "{}",
                    crate::language::text("Invalid command line arguments; use --help for help.")
                );
            }
            error.exit()
        });
    let cli = Cli::from_arg_matches(&matches)?;
    let _ = cli.language;
    crate::ssh::configure(cli.ssh_identity, cli.ssh_known_hosts)?;
    for path in cli.cwd {
        std::env::set_current_dir(path)?;
    }
    crate::runtime::configure(crate::runtime::Options {
        config: cli.config,
        git_dir: cli.git_dir,
        work_tree: cli.work_tree,
        bare: cli.bare,
    })?;
    match cli.command {
        Commands::Language(options) => crate::language::run(options),
        Commands::Diff(options) => crate::difference::run(options),
        Commands::Show(options) => crate::difference::show(options),
        Commands::Restore(options) => crate::restoration::restore(options),
        Commands::Reset(options) => crate::restoration::reset(options),
        Commands::Eol(options) => crate::eol::run(options),
        Commands::Lfs(options) => crate::lfs::run(options),
        Commands::Push(options) => crate::push::run(options),
        Commands::Add {
            all,
            paths,
            renormalize,
        } => crate::write::add(all, paths, renormalize),
        Commands::Commit {
            message,
            no_edit,
            cleanup,
        } => match message {
            Some(message) => crate::write::commit(&message, cleanup.as_deref()),
            None if no_edit => crate::write::commit_pending(true, cleanup.as_deref()),
            None => anyhow::bail!(
                "pass -m for a commit message, or --no-edit to finish a pending merge; interactive message editing is not yet supported"
            ),
        },
        Commands::Switch {
            create,
            detach,
            target,
        }
        | Commands::Checkout {
            create,
            detach,
            target,
        } => crate::worktree::switch(target.as_deref(), create.as_deref(), detach),
        Commands::Merge(options) => crate::merging::run(options),
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
        Commands::Pull { remote, .. } => crate::network::pull(remote.as_deref()),
        Commands::Status(options) => crate::repository::status(options),
        Commands::Log {
            count,
            oneline,
            revision,
            format,
            date,
            nul,
            no_decorate: _,
            no_color: _,
            no_patch: _,
        } => crate::repository::log(count, oneline, format, date, revision, nul),
        Commands::RevParse {
            show_toplevel,
            git_dir,
            absolute_git_dir,
            is_bare_repository,
            revision,
        } => crate::repository::rev_parse(
            show_toplevel,
            git_dir,
            absolute_git_dir,
            is_bare_repository,
            &revision,
        ),
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
            Some(RemoteCommand::Show) => crate::repository::remotes(verbose),
            None => crate::repository::remotes(verbose),
        },
        Commands::Config(options) => crate::configuration::run(options),
        Commands::LsRemote { url } => crate::network::ls_remote(&url),
        Commands::Compatibility { json } => {
            if json {
                println!("{}", include_str!("../compatibility.json"));
            } else {
                println!("{}", include_str!("../docs/COMPATIBILITY.md"));
            }
            Ok(())
        }
        Commands::Version {
            build_options,
            json,
        } => crate::identity::version(build_options, json),
        Commands::Doctor { json } => crate::identity::doctor(json),
    }
}
