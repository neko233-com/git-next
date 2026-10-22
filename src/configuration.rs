use anyhow::{Context, Result, bail, ensure};
use clap::Args;
use gix::bstr::{BString, ByteSlice};
use regex::bytes::Regex;
use std::{
    fs,
    io::{self, Read, Write},
    path::{Path, PathBuf},
};

#[derive(Args, Default)]
pub(crate) struct Options {
    pub arguments: Vec<String>,
    #[arg(long, group = "config_scope")]
    pub global: bool,
    #[arg(long, group = "config_scope")]
    pub local: bool,
    #[arg(long, group = "config_scope")]
    pub system: bool,
    #[arg(long, group = "config_scope")]
    pub worktree: bool,
    #[arg(short = 'f', long, group = "config_scope")]
    pub file: Option<PathBuf>,
    #[arg(long, group = "config_action")]
    pub get: bool,
    #[arg(long, alias = "all", group = "config_action")]
    pub get_all: bool,
    #[arg(long, alias = "regexp", group = "config_action")]
    pub get_regexp: bool,
    #[arg(short = 'l', long, group = "config_action")]
    pub list: bool,
    #[arg(long, alias = "append", group = "config_action")]
    pub add: bool,
    #[arg(long, group = "config_action")]
    pub replace_all: bool,
    #[arg(long, group = "config_action")]
    pub unset: bool,
    #[arg(long, group = "config_action")]
    pub unset_all: bool,
    #[arg(short = 'z', long = "null")]
    pub nul: bool,
    #[arg(long)]
    pub name_only: bool,
    #[arg(long)]
    pub show_origin: bool,
    #[arg(long)]
    pub show_scope: bool,
    #[arg(long, conflicts_with = "no_includes")]
    pub includes: bool,
    #[arg(long)]
    pub no_includes: bool,
    #[arg(long)]
    pub fixed_value: bool,
    #[arg(long = "type", value_parser = ["bool", "int", "bool-or-int", "path"], group = "config_type")]
    pub value_type: Option<String>,
    #[arg(long = "bool", group = "config_type")]
    pub boolean: bool,
    #[arg(long = "int", group = "config_type")]
    pub integer: bool,
    #[arg(long, group = "config_type")]
    pub bool_or_int: bool,
    #[arg(long, group = "config_type")]
    pub path: bool,
    #[arg(long)]
    pub default: Option<String>,
}

#[derive(Clone)]
struct Entry {
    key: String,
    value: Option<BString>,
    origin: String,
    scope: &'static str,
}

#[derive(Clone, Copy, PartialEq)]
enum Action {
    Get,
    GetAll,
    Regexp,
    List,
    Set,
    Add,
    Replace,
    Unset,
    UnsetAll,
}

fn home() -> Result<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
        .context("HOME/USERPROFILE is unset")
}

fn global_paths() -> Result<Vec<PathBuf>> {
    if let Some(path) = std::env::var_os("GIT_CONFIG_GLOBAL") {
        return Ok(vec![path.into()]);
    }
    let home = home()?;
    let xdg = std::env::var_os("XDG_CONFIG_HOME")
        .filter(|path| !path.is_empty())
        .map(PathBuf::from)
        .unwrap_or_else(|| home.join(".config"));
    Ok(vec![xdg.join("git/config"), home.join(".gitconfig")])
}

fn system_path() -> PathBuf {
    std::env::var_os("GIT_CONFIG_SYSTEM")
        .map(PathBuf::from)
        .or_else(|| gix::path::env::system_config().map(Path::to_owned))
        .unwrap_or_else(|| {
            if cfg!(windows) {
                PathBuf::from(
                    std::env::var_os("PROGRAMDATA").unwrap_or_else(|| "C:/ProgramData".into()),
                )
                .join("Git/config")
            } else {
                "/etc/gitconfig".into()
            }
        })
}

fn load(
    paths: Vec<PathBuf>,
    source: gix::config::Source,
    includes: bool,
) -> Result<gix::config::File> {
    let metas: Vec<_> = paths
        .into_iter()
        .filter(|path| path.is_file())
        .map(|path| gix::config::file::Metadata::from(source).at(path))
        .collect();
    let home = home().ok();
    let repository = if includes {
        crate::repository::open().ok()
    } else {
        None
    };
    let branch = repository
        .as_ref()
        .map(|repo| repo.head_name())
        .transpose()?
        .flatten();
    let git_dir = repository
        .as_ref()
        .map(|repo| repo.git_dir().canonicalize())
        .transpose()?;
    let mut options = gix::config::file::init::Options::default();
    if includes {
        options.includes =
            gix::config::file::includes::Options::follow_without_conditional(home.as_deref());
        options.includes.conditional.git_dir = git_dir.as_deref();
        options.includes.conditional.branch_name = branch.as_ref().map(|name| name.as_ref());
    }
    Ok(gix::config::File::from_paths_metadata(metas, options)?.unwrap_or_default())
}

fn scope_path(options: &Options, write: bool) -> Result<Option<(PathBuf, gix::config::Source)>> {
    use gix::config::Source;
    if let Some(path) = options
        .file
        .as_ref()
        .cloned()
        .or_else(|| std::env::var_os("GIT_CONFIG").map(PathBuf::from))
    {
        return Ok(Some((path, Source::Local)));
    }
    if options.global {
        let paths = global_paths()?;
        let selected = paths
            .iter()
            .rev()
            .find(|path| path.is_file())
            .cloned()
            .or_else(|| paths.last().cloned())
            .context("no global config path")?;
        return Ok(Some((selected, Source::User)));
    }
    if options.system {
        return Ok(Some((system_path(), Source::System)));
    }
    if options.local || options.worktree || write {
        let repo = crate::repository::open()?;
        if options.worktree
            && repo
                .config_snapshot()
                .boolean("extensions.worktreeConfig")
                .unwrap_or(false)
        {
            return Ok(Some((
                repo.git_dir().join("config.worktree"),
                Source::Worktree,
            )));
        }
        return Ok(Some((repo.common_dir().join("config"), Source::Local)));
    }
    Ok(None)
}

fn read(options: &Options) -> Result<gix::config::File> {
    let explicit_scope = options.global
        || options.local
        || options.system
        || options.worktree
        || options.file.is_some()
        || std::env::var_os("GIT_CONFIG").is_some();
    let includes = options.includes || (!options.no_includes && !explicit_scope);
    if options.global {
        return load(global_paths()?, gix::config::Source::User, includes);
    }
    if let Some((path, source)) = scope_path(options, false)? {
        if path == Path::new("-") {
            let mut bytes = Vec::new();
            io::stdin().read_to_end(&mut bytes)?;
            return Ok(gix::config::File::from_bytes_owned(
                &mut bytes,
                gix::config::file::Metadata::from(source),
                Default::default(),
            )?);
        }
        return load(vec![path], source, includes);
    }
    match crate::repository::open() {
        Ok(repo) if includes => return Ok((*repo.config_snapshot()).clone()),
        Ok(repo) => {
            let mut file = globals(false)?;
            file.append(load(
                vec![repo.common_dir().join("config")],
                gix::config::Source::Local,
                false,
            )?)?;
            file.append(gix::config::File::from_environment_overrides()?)?;
            for value in &crate::runtime::options().config {
                apply_override(&mut file, value)?;
            }
            return Ok(file);
        }
        Err(error) => {
            let missing = error
                .chain()
                .filter_map(|source| source.downcast_ref::<gix::Error>())
                .any(|error| error.is_not_found());
            if !missing {
                return Err(error);
            }
        }
    }
    let mut file = globals(includes)?;
    file.append(gix::config::File::from_environment_overrides()?)?;
    for value in &crate::runtime::options().config {
        apply_override(&mut file, value)?;
    }
    Ok(file)
}

fn apply_override(file: &mut gix::config::File, argument: &str) -> Result<()> {
    let mut overrides = gix::config::File::from_bytes_owned(
        &mut Vec::new(),
        gix::config::file::Metadata::from(gix::config::Source::Cli),
        Default::default(),
    )?;
    let (key, value) = argument
        .split_once('=')
        .map(|(key, value)| (key, Some(value)))
        .unwrap_or((argument, None));
    let (section, subsection, name) = key_parts(key)?;
    overrides
        .section_mut_or_create_new(section, subsection.map(|value| value.as_bytes().as_bstr()))?
        .push(name, value.map(|value| value.as_bytes().as_bstr()))?;
    file.append(overrides)?;
    Ok(())
}

fn globals(includes: bool) -> Result<gix::config::File> {
    let mut file = gix::config::File::default();
    let no_system = std::env::var("GIT_CONFIG_NOSYSTEM")
        .ok()
        .is_some_and(|value| matches!(value.to_lowercase().as_str(), "1" | "true" | "yes" | "on"));
    if !no_system {
        let mut seen = std::collections::BTreeSet::new();
        for source in [
            gix::config::Source::GitInstallation,
            gix::config::Source::System,
        ] {
            if let Some(path) = source.storage_location(&mut gix::path::env::var)
                && seen.insert(path.clone())
            {
                file.append(load(vec![path], source, includes)?)?;
            }
        }
    }
    file.append(load(global_paths()?, gix::config::Source::User, includes)?)?;
    Ok(file)
}

fn key_parts(key: &str) -> Result<(&str, Option<&str>, &str)> {
    let (section, rest) = key
        .split_once('.')
        .context("config key must contain section.name")?;
    let (subsection, name) = rest
        .rsplit_once('.')
        .map(|(subsection, name)| (Some(subsection), name))
        .unwrap_or((None, rest));
    ensure!(
        !section.is_empty()
            && section
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
            && name.as_bytes().first().is_some_and(u8::is_ascii_alphabetic)
            && name
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-'),
        "invalid configuration key"
    );
    Ok((section, subsection, name))
}

fn normalized_key(key: &str) -> Result<String> {
    let (section, subsection, name) = key_parts(key)?;
    Ok(match subsection {
        Some(subsection) => format!(
            "{}.{subsection}.{}",
            section.to_ascii_lowercase(),
            name.to_ascii_lowercase()
        ),
        None => format!(
            "{}.{}",
            section.to_ascii_lowercase(),
            name.to_ascii_lowercase()
        ),
    })
}

fn entries(file: &gix::config::File) -> Result<Vec<Entry>> {
    use gix::config::{Source, parse::EventRef};
    let mut entries = Vec::new();
    for section in file.sections() {
        let header = section.header();
        let prefix = match header.subsection_name() {
            Some(subsection) => format!(
                "{}.{subsection}",
                header.name().to_string().to_ascii_lowercase()
            ),
            None => header.name().to_string().to_ascii_lowercase(),
        };
        let meta = section.meta();
        if matches!(meta.source, Source::EnvOverride | Source::Api) {
            continue;
        }
        let origin = meta
            .path
            .as_ref()
            .map(|path| {
                let mut display = path.to_string_lossy().into_owned();
                if cfg!(windows) && !path.is_absolute() {
                    display = display.replace('\\', "/");
                }
                while let Some(relative) = display.strip_prefix("./") {
                    display = relative.to_owned();
                }
                format!("file:{display}")
            })
            .unwrap_or_else(|| "command line:".into());
        let scope = match meta.source {
            Source::GitInstallation | Source::System => "system",
            Source::Git | Source::User => "global",
            Source::Local => "local",
            Source::Worktree => "worktree",
            _ => "command",
        };
        let mut bytes = Vec::new();
        section.write_to(&mut bytes)?;
        let parsed = gix::config::parse::Events::from_bytes(&bytes, None)?;
        let mut current: Option<String> = None;
        let mut has_separator = false;
        let mut partial = Vec::new();
        let emit = |name: String, value, entries: &mut Vec<Entry>| {
            entries.push(Entry {
                key: format!("{prefix}.{}", name.to_ascii_lowercase()),
                value,
                origin: origin.clone(),
                scope,
            });
        };
        for event in parsed.iter() {
            match event {
                EventRef::SectionValueName(name) => {
                    if let Some(previous) = current.take() {
                        emit(previous, None, &mut entries);
                    }
                    current = Some(name.to_string());
                    has_separator = false;
                }
                EventRef::KeyValueSeparator => has_separator = true,
                EventRef::Value(value) => {
                    if let Some(name) = current.take() {
                        emit(
                            name,
                            has_separator
                                .then(|| gix::config::value::normalize(value).into_owned()),
                            &mut entries,
                        );
                    }
                }
                EventRef::ValueNotDone(value) => partial.extend_from_slice(value),
                EventRef::ValueDone(value) => {
                    if let Some(name) = current.take() {
                        partial.extend_from_slice(value);
                        emit(
                            name,
                            Some(gix::config::value::normalize(&partial).into_owned()),
                            &mut entries,
                        );
                        partial.clear();
                    }
                }
                _ => {}
            }
        }
        if let Some(name) = current {
            emit(name, None, &mut entries);
        }
    }
    Ok(entries)
}

fn append(file: &mut gix::config::File, key: &str, value: &gix::bstr::BStr) -> Result<()> {
    let (section, subsection, name) = key_parts(key)?;
    file.section_mut_or_create_new(section, subsection.map(|value| value.as_bytes().as_bstr()))?
        .push(name, Some(value))?;
    Ok(())
}

struct Lock {
    path: PathBuf,
    file: Option<fs::File>,
    active: bool,
}
impl Drop for Lock {
    fn drop(&mut self) {
        drop(self.file.take());
        if self.active {
            let _ = fs::remove_file(&self.path);
        }
    }
}

fn mutate(
    options: &Options,
    action: Action,
    key: &str,
    value: Option<&str>,
    predicate: &impl Fn(&[u8]) -> bool,
) -> Result<()> {
    let key = normalized_key(key)?;
    let (mut path, source) =
        scope_path(options, true)?.context("no writable configuration scope")?;
    ensure!(
        path != Path::new("-"),
        "cannot write configuration to stdin"
    );
    if fs::symlink_metadata(&path).is_ok_and(|meta| meta.file_type().is_symlink()) {
        path = path.canonicalize()?;
    }
    let lock_path = path.with_file_name(format!(
        "{}.lock",
        path.file_name()
            .context("invalid config path")?
            .to_string_lossy()
    ));
    let mut open = fs::OpenOptions::new();
    open.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        open.mode(0o600);
    }
    let file = open
        .open(&lock_path)
        .context("could not lock configuration file")?;
    let mut lock = Lock {
        path: lock_path,
        file: Some(file),
        active: true,
    };
    if let Ok(meta) = fs::metadata(&path) {
        lock.file
            .as_ref()
            .context("config lock closed")?
            .set_permissions(meta.permissions())?;
    }
    let mut config = load(vec![path.clone()], source, false)?;
    let matching: Vec<_> = entries(&config)?
        .into_iter()
        .filter(|entry| entry.key == key)
        .enumerate()
        .filter(|(_, entry)| {
            predicate(
                entry
                    .value
                    .as_ref()
                    .map(|value| value.as_slice())
                    .unwrap_or_default(),
            )
        })
        .map(|(index, _)| index)
        .collect();
    if matches!(action, Action::Unset | Action::UnsetAll) && matching.is_empty() {
        return Err(crate::CommandStatus(5).into());
    }
    if matches!(action, Action::Set | Action::Unset) && matching.len() > 1 {
        return Err(crate::CommandStatus(5).into());
    }
    if action == Action::Add || matching.is_empty() {
        append(
            &mut config,
            &key,
            value
                .context("configuration value is required")?
                .as_bytes()
                .as_bstr(),
        )?;
    } else {
        let mut values = config.raw_values_mut(key.as_str())?;
        if matches!(action, Action::Unset | Action::UnsetAll) {
            for index in matching.into_iter().rev() {
                values.delete(index);
            }
        } else {
            values.set_at(
                matching[0],
                value.context("configuration value is required")?,
            )?;
            for index in matching.into_iter().skip(1).rev() {
                values.delete(index);
            }
        }
    }
    let mut file = lock.file.take().context("config lock closed")?;
    config.write_to(&mut file)?;
    file.flush()?;
    file.sync_all()?;
    drop(file);
    fs::rename(&lock.path, path)?;
    lock.active = false;
    Ok(())
}

fn converted(value: Option<&gix::bstr::BStr>, kind: Option<&str>) -> Result<BString> {
    let raw = value.unwrap_or_default();
    Ok(match kind {
        Some("bool") => {
            if value.is_none() {
                "true".into()
            } else {
                bool::from(gix::config::Boolean::try_from(raw)?)
                    .to_string()
                    .into()
            }
        }
        Some("int") => gix::config::Integer::from_bytes::<i64>(raw)?
            .to_string()
            .into(),
        Some("bool-or-int") => {
            if value.is_none() {
                "true".into()
            } else if let Ok(value) = gix::config::Boolean::try_from(raw) {
                bool::from(value).to_string().into()
            } else {
                gix::config::Integer::from_bytes::<i64>(raw)?
                    .to_string()
                    .into()
            }
        }
        Some("path") => {
            let value = raw.to_str()?;
            if value == "~" {
                home()?.to_string_lossy().as_bytes().into()
            } else if let Some(path) = value.strip_prefix("~/") {
                home()?.join(path).to_string_lossy().as_bytes().into()
            } else {
                raw.to_owned()
            }
        }
        Some(_) => bail!("unsupported configuration value type"),
        None => raw.to_owned(),
    })
}

fn quoted_path(path: &[u8], quote_non_ascii: bool) -> Vec<u8> {
    if !path.iter().any(|byte| {
        *byte < 32
            || *byte == 127
            || matches!(byte, b'"' | b'\\')
            || (quote_non_ascii && *byte >= 128)
    }) {
        return path.to_vec();
    }
    let mut result = vec![b'"'];
    for &byte in path {
        match byte {
            b'"' => result.extend_from_slice(b"\\\""),
            b'\\' => result.extend_from_slice(b"\\\\"),
            b'\n' => result.extend_from_slice(b"\\n"),
            b'\r' => result.extend_from_slice(b"\\r"),
            b'\t' => result.extend_from_slice(b"\\t"),
            7 => result.extend_from_slice(b"\\a"),
            8 => result.extend_from_slice(b"\\b"),
            11 => result.extend_from_slice(b"\\v"),
            12 => result.extend_from_slice(b"\\f"),
            byte if byte < 32 || byte == 127 || (quote_non_ascii && byte >= 128) => {
                result.extend_from_slice(format!("\\{byte:03o}").as_bytes())
            }
            byte => result.push(byte),
        }
    }
    result.push(b'"');
    result
}

pub(crate) fn run(mut options: Options) -> Result<()> {
    let mut action = if options.list {
        Action::List
    } else if options.get_all {
        Action::GetAll
    } else if options.get_regexp {
        Action::Regexp
    } else if options.add {
        Action::Add
    } else if options.replace_all {
        Action::Replace
    } else if options.unset {
        Action::Unset
    } else if options.unset_all {
        Action::UnsetAll
    } else if options.get || options.arguments.len() < 2 {
        Action::Get
    } else {
        Action::Set
    };
    if let Some(command) = options.arguments.first().map(String::as_str) {
        let modern = match command {
            "get" => Some(if options.get_regexp {
                Action::Regexp
            } else if options.get_all {
                Action::GetAll
            } else {
                Action::Get
            }),
            "set" => Some(if options.add {
                Action::Add
            } else if options.replace_all || options.get_all {
                Action::Replace
            } else {
                Action::Set
            }),
            "unset" => Some(if options.unset_all || options.get_all {
                Action::UnsetAll
            } else {
                Action::Unset
            }),
            "list" => Some(Action::List),
            _ => None,
        };
        if let Some(modern) = modern {
            action = modern;
            options.arguments.remove(0);
        }
    }
    let write = matches!(
        action,
        Action::Set | Action::Add | Action::Replace | Action::Unset | Action::UnsetAll
    );
    ensure!(
        !(write
            && (options.show_origin
                || options.show_scope
                || options.name_only
                || options.nul
                || options.default.is_some())),
        "output options cannot be used for config writes"
    );
    let expected = match action {
        Action::List => 0..=0,
        Action::Set | Action::Add | Action::Replace => 2..=3,
        _ => 1..=2,
    };
    ensure!(
        expected.contains(&options.arguments.len()),
        "wrong number of config arguments"
    );
    let value_type = options.value_type.as_deref().or(if options.boolean {
        Some("bool")
    } else if options.integer {
        Some("int")
    } else if options.bool_or_int {
        Some("bool-or-int")
    } else if options.path {
        Some("path")
    } else {
        None
    });
    let pattern = if write && !matches!(action, Action::Unset | Action::UnsetAll) {
        options.arguments.get(2)
    } else {
        options.arguments.get(1)
    };
    let negated = pattern.is_some_and(|pattern| pattern.starts_with('!') && !options.fixed_value);
    let pattern = pattern.map(|pattern| {
        if negated {
            &pattern[1..]
        } else {
            pattern.as_str()
        }
    });
    let regex = if !options.fixed_value {
        pattern.map(Regex::new).transpose()?
    } else {
        None
    };
    let predicate = |value: &[u8]| {
        let matches = if options.fixed_value {
            pattern.is_none_or(|pattern| pattern.as_bytes() == value)
        } else {
            regex.as_ref().is_none_or(|pattern| pattern.is_match(value))
        };
        matches != negated
    };
    if write {
        let value = if matches!(action, Action::Set | Action::Add | Action::Replace) {
            Some(converted(
                Some(options.arguments[1].as_bytes().as_bstr()),
                value_type,
            )?)
        } else {
            None
        };
        return mutate(
            &options,
            action,
            &options.arguments[0],
            value.as_ref().map(|value| value.to_str()).transpose()?,
            &predicate,
        );
    }
    let config = read(&options)?;
    let mut selected = entries(&config)?;
    let quote_path = if let Some(entry) = selected
        .iter()
        .rev()
        .find(|entry| entry.key == "core.quotepath")
    {
        converted(
            entry.value.as_ref().map(|value| value.as_bstr()),
            Some("bool"),
        )?
        .as_slice()
            == b"true"
    } else {
        true
    };
    if action == Action::Regexp {
        let key = Regex::new(&options.arguments[0])?;
        selected.retain(|entry| {
            key.is_match(entry.key.as_bytes())
                && predicate(
                    entry
                        .value
                        .as_ref()
                        .map(|value| value.as_slice())
                        .unwrap_or_default(),
                )
        });
    } else if action != Action::List {
        let key = normalized_key(&options.arguments[0])?;
        selected.retain(|entry| {
            entry.key == key
                && predicate(
                    entry
                        .value
                        .as_ref()
                        .map(|value| value.as_slice())
                        .unwrap_or_default(),
                )
        });
        if action == Action::Get {
            selected = selected.pop().into_iter().collect();
        }
    }
    if selected.is_empty() && action != Action::List {
        if let Some(default) = options.default {
            io::stdout()
                .write_all(converted(Some(default.as_bytes().as_bstr()), value_type)?.as_slice())?;
            io::stdout().write_all(if options.nul { b"\0" } else { b"\n" })?;
            return Ok(());
        }
        return Err(crate::CommandStatus(1).into());
    }
    let mut output = io::stdout().lock();
    let separator = if options.nul {
        b"\0".as_slice()
    } else {
        b"\t".as_slice()
    };
    for entry in selected {
        if options.show_scope {
            output.write_all(entry.scope.as_bytes())?;
            output.write_all(separator)?;
        }
        if options.show_origin {
            if !options.nul
                && let Some(path) = entry.origin.strip_prefix("file:")
            {
                output.write_all(b"file:")?;
                output.write_all(&quoted_path(path.as_bytes(), quote_path))?;
            } else {
                output.write_all(entry.origin.as_bytes())?;
            }
            output.write_all(separator)?;
        }
        if action == Action::List || action == Action::Regexp || options.name_only {
            output.write_all(entry.key.as_bytes())?;
            if !options.name_only && entry.value.is_some() {
                output.write_all(if options.nul {
                    b"\n"
                } else if action == Action::List {
                    b"="
                } else {
                    b" "
                })?;
            }
        }
        if !options.name_only {
            output.write_all(
                converted(
                    entry.value.as_ref().map(|value| value.as_bstr()),
                    value_type,
                )?
                .as_slice(),
            )?;
        }
        output.write_all(if options.nul { b"\0" } else { b"\n" })?;
    }
    Ok(())
}
