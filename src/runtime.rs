use anyhow::{Context, Result};
use std::{path::PathBuf, sync::OnceLock};

#[derive(Default)]
pub(crate) struct Options {
    pub config: Vec<String>,
    pub git_dir: Option<PathBuf>,
    pub work_tree: Option<PathBuf>,
    pub bare: bool,
}

static OPTIONS: OnceLock<Options> = OnceLock::new();

pub(crate) fn configure(options: Options) -> Result<()> {
    if !options.config.is_empty() {
        let mut count: usize = std::env::var("GIT_CONFIG_COUNT")
            .unwrap_or_else(|_| "0".into())
            .parse()
            .context("invalid GIT_CONFIG_COUNT")?;
        let mut environment = Vec::new();
        for value in &options.config {
            let (key, value) = value.split_once('=').unwrap_or((value.as_str(), "true"));
            environment.push((format!("GIT_CONFIG_KEY_{count}").into(), key.into()));
            environment.push((format!("GIT_CONFIG_VALUE_{count}").into(), value.into()));
            count += 1;
        }
        environment.push(("GIT_CONFIG_COUNT".into(), count.to_string().into()));
        anyhow::ensure!(
            gix::filter::plumbing::driver::set_command_environment(environment),
            "filter command environment is already configured"
        );
    }
    OPTIONS
        .set(options)
        .map_err(|_| anyhow::anyhow!("command context is already configured"))
}

pub(crate) fn options() -> &'static Options {
    OPTIONS.get_or_init(Options::default)
}

pub(crate) fn open_options() -> gix::open::Options {
    let settings = options();
    let mut overrides = Vec::new();
    if let Some(worktree) = &settings.work_tree {
        let path = if worktree.is_absolute() {
            worktree.clone()
        } else {
            std::env::current_dir().unwrap_or_default().join(worktree)
        };
        overrides.push(format!("core.worktree={}", path.display()));
        overrides.push("core.bare=false".into());
    } else if settings.bare {
        overrides.push("core.bare=true".into());
    }
    gix::open::Options::default()
        .bail_if_untrusted(true)
        .strict_config(true)
        .config_overrides(overrides)
        .cli_overrides(settings.config.clone())
}

pub(crate) fn open() -> Result<gix::Repository> {
    let settings = options();
    let options = open_options();
    if let Some(path) = &settings.git_dir {
        return crate::eol::defaults(
            gix::open_opts(path, options.open_path_as_is(true))
                .context("could not open --git-dir")?,
        );
    }
    if settings.bare {
        return crate::eol::defaults(
            gix::open_opts(".", options.open_path_as_is(true))
                .context("--bare requires a repository directory")?,
        );
    }
    let cwd = std::env::current_dir()?;
    // A literal "." hides the metadata directory name from Gitoxide's discovery classifier.
    let directory = if cwd.file_name().is_some_and(|name| name == ".git") {
        cwd
    } else {
        ".".into()
    };
    let repo = gix::ThreadSafeRepository::discover_with_environment_overrides_opts(
        directory,
        Default::default(),
        gix::sec::trust::Mapping {
            full: options.clone(),
            reduced: options,
        },
    )
    .map(Into::into)
    .context("not a Git repository")?;
    crate::eol::defaults(repo)
}
