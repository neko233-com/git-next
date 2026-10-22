use anyhow::{Result, ensure};
use gix::bstr::{BString, ByteSlice};
use std::{collections::BTreeMap, ffi::OsString};

#[derive(clap::Args)]
pub struct Options {
    #[arg(long, num_args = 0..=1, require_equals = true, default_missing_value = "v1", value_parser = ["v1", "1", "v2", "2"], overrides_with_all = ["short", "long"])]
    porcelain: Option<String>,
    #[arg(short, long, overrides_with_all = ["porcelain", "long"])]
    short: bool,
    #[arg(long, overrides_with_all = ["porcelain", "short"])]
    long: bool,
    #[arg(short, long)]
    pub branch: bool,
    #[arg(long, overrides_with = "no_ahead_behind")]
    ahead_behind: bool,
    #[arg(long, overrides_with = "ahead_behind")]
    no_ahead_behind: bool,
    #[arg(long, num_args = 0..=1, require_equals = true, default_missing_value = "all", value_parser = ["none", "untracked", "dirty", "all"])]
    pub ignore_submodules: Option<String>,
    #[arg(short = 'z')]
    pub nul: bool,
    #[arg(short = 'u', long = "untracked-files", num_args = 0..=1, require_equals = true, default_missing_value = "all", value_parser = ["no", "normal", "all"])]
    untracked: Option<String>,
    #[arg(long, num_args = 0..=1, require_equals = true, default_missing_value = "traditional", value_parser = ["traditional", "matching", "no"])]
    pub ignored: Option<String>,
    pub paths: Vec<OsString>,
}

pub struct Settings {
    porcelain: bool,
    short: bool,
    branch: bool,
    pub hints: bool,
    pub quote_unicode: bool,
    relative_prefix: BString,
    pub untracked: gix::status::UntrackedFiles,
    pub override_untracked: bool,
}

pub struct Row {
    pub code: [char; 2],
    pub source: Option<BString>,
    pub directory: bool,
    pub submodule: Option<[u8; 4]>,
}

pub type Rows = BTreeMap<(bool, BString), Row>;

impl Default for Row {
    fn default() -> Self {
        Self {
            code: [' '; 2],
            source: None,
            directory: false,
            submodule: None,
        }
    }
}

impl Options {
    pub fn settings(&self, repo: &gix::Repository) -> Result<Settings> {
        let config = repo.config_snapshot();
        ensure!(!self.long || !self.nul, "--long and -z are incompatible");
        let porcelain = self.porcelain.is_some() || self.nul;
        let configured_short = config.try_boolean("status.short")?.unwrap_or(false);
        let configured_branch = config.try_boolean("status.branch")?.unwrap_or(false);
        let hints = config.try_boolean("advice.statusHints")?.unwrap_or(true);
        let short = self.short || (!porcelain && !self.long && configured_short);
        let branch = self.branch || (short && configured_branch);
        let quote_unicode = config.try_boolean("core.quotePath")?.unwrap_or(true);
        let relative = config.try_boolean("status.relativePaths")?.unwrap_or(true);
        let configured = config.string("status.showUntrackedFiles");
        let value = self
            .untracked
            .as_ref()
            .map(|value| value.as_bytes())
            .or_else(|| configured.as_ref().map(|value| value.as_slice()));
        let untracked = match value {
            None | Some(b"normal") => gix::status::UntrackedFiles::Collapsed,
            Some(b"no") => gix::status::UntrackedFiles::None,
            Some(b"all") => gix::status::UntrackedFiles::Files,
            Some(_) => {
                ensure!(self.untracked.is_none(), "unsupported untracked-files mode");
                match config.try_boolean("status.showUntrackedFiles")? {
                    Some(true) => gix::status::UntrackedFiles::Collapsed,
                    Some(false) => gix::status::UntrackedFiles::None,
                    None => anyhow::bail!("status.showUntrackedFiles requires a value"),
                }
            }
        };
        ensure!(
            self.ignored.as_deref() != Some("matching")
                || !matches!(untracked, gix::status::UntrackedFiles::None),
            "unsupported combination of ignored=matching and untracked-files=no"
        );
        let relative_prefix = if !porcelain && relative {
            repo.prefix()?
                .map(|path| {
                    gix::path::into_bstr(path).map(gix::path::to_unix_separators_on_windows)
                })
                .transpose()?
                .map(|value| value.into_owned())
                .unwrap_or_default()
        } else {
            BString::default()
        };
        Ok(Settings {
            porcelain,
            short,
            branch,
            hints,
            quote_unicode,
            relative_prefix,
            untracked,
            override_untracked: self.untracked.is_some(),
        })
    }

    pub fn is_v2(&self) -> bool {
        matches!(self.porcelain.as_deref(), Some("v2" | "2"))
    }

    pub fn branch_header(&self, repo: &gix::Repository) -> Result<Vec<u8>> {
        crate::status_branch::render(repo, self.is_v2(), self.nul, !self.no_ahead_behind)
    }

    pub fn render(
        &self,
        repo: &gix::Repository,
        settings: &Settings,
        paths: &Rows,
    ) -> Result<Vec<u8>> {
        let mut output = Vec::new();
        if settings.branch && (settings.porcelain || settings.short) {
            output.extend_from_slice(&self.branch_header(repo)?);
        }
        if !settings.porcelain && !settings.short {
            return crate::status_long::render(repo, settings, paths, !self.no_ahead_behind);
        }
        for group in 0..3 {
            for ((_, path), row) in paths {
                let row_group = match row.code {
                    ['?', '?'] => 1,
                    ['!', '!'] => 2,
                    _ => 0,
                };
                if row.code == [' '; 2] || row_group != group {
                    continue;
                }
                let mut code = row.code;
                if !settings.porcelain
                    && code[1] == 'M'
                    && let Some(submodule) = row.submodule
                {
                    code[1] = if submodule[1] == b'C' {
                        'M'
                    } else if submodule[2] == b'M' {
                        'm'
                    } else {
                        '?'
                    };
                }
                output.extend_from_slice(&[code[0] as u8, code[1] as u8, b' ']);
                let mut path = relative_path(path, &settings.relative_prefix);
                if row.directory && !path.ends_with(b"/") {
                    path.push(b'/');
                }
                if self.nul {
                    output.extend_from_slice(&path);
                    output.push(0);
                    if let Some(source) = &row.source {
                        output.extend_from_slice(&relative_path(source, &settings.relative_prefix));
                        output.push(0);
                    }
                } else {
                    if let Some(source) = &row.source {
                        output.extend_from_slice(&quote(
                            &relative_path(source, &settings.relative_prefix),
                            settings.quote_unicode,
                        ));
                        output.extend_from_slice(b" -> ");
                    }
                    output.extend_from_slice(&quote(&path, settings.quote_unicode));
                    output.push(b'\n');
                }
            }
        }
        Ok(output)
    }
}

impl Settings {
    pub fn long_path(&self, path: &[u8], directory: bool) -> Vec<u8> {
        let mut path = relative_path(path, &self.relative_prefix);
        if directory && !path.ends_with(b"/") {
            path.push(b'/');
        }
        crate::difference::quote(&path, self.quote_unicode)
    }
}

fn quote(path: &[u8], quote_unicode: bool) -> Vec<u8> {
    let mut output = crate::difference::quote(path, quote_unicode);
    if output.first() != Some(&b'"') && path.contains(&b' ') {
        output.insert(0, b'"');
        output.push(b'"');
    }
    output
}

pub fn ignore_staged_submodules(repo: &gix::Repository, options: &Options) -> Result<bool> {
    let config = repo.config_snapshot();
    let configured = config.string("diff.ignoreSubmodules");
    let value = options
        .ignore_submodules
        .as_deref()
        .map(str::as_bytes)
        .or_else(|| configured.as_ref().map(|value| value.as_slice()));
    if let Some(value) = value {
        ensure!(
            matches!(value, b"none" | b"untracked" | b"dirty" | b"all"),
            "invalid diff.ignoreSubmodules value"
        );
    }
    // Only the CLI ignore-all suppresses staged gitlinks; configuration affects worktree status.
    Ok(options.ignore_submodules.as_deref() == Some("all"))
}

pub fn filter_collapsed_paths(
    repo: &gix::Repository,
    index: &gix::index::State,
    patterns: &[BString],
    settings: &Settings,
    paths: &mut Rows,
) -> Result<()> {
    if !matches!(settings.untracked, gix::status::UntrackedFiles::Collapsed)
        || !paths.values().any(|row| row.code == ['?', '?'])
    {
        return Ok(());
    }
    let mut selection = repo.pathspec(
        false,
        patterns.iter().map(|path| path.as_bstr()),
        false,
        index,
        gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
    )?;
    let mut collapsed = std::collections::BTreeSet::new();
    for ((_, path), _) in paths.iter().filter(|(_, row)| row.code == ['?', '?']) {
        for position in path
            .iter()
            .enumerate()
            .filter_map(|(position, byte)| (*byte == b'/').then_some(position))
        {
            let directory = path[..position].as_bstr();
            let prefix = path[..=position].as_bstr();
            // Only a directly matching, wholly untracked directory can replace its selected children.
            if index.prefixed_entries(prefix).is_none()
                && (selection.is_included(directory, Some(true))
                    || selection.is_included(prefix, Some(true)))
            {
                collapsed.insert(directory.to_owned());
                break;
            }
        }
    }
    paths.retain(|(_, path), row| {
        row.code != ['?', '?']
            || !path.iter().enumerate().any(|(position, byte)| {
                *byte == b'/' && collapsed.contains(path[..position].as_bstr())
            })
    });
    for mut path in collapsed {
        if index.entry_by_path(path.as_bstr()).is_some() {
            // Normal mode suppresses the untracked directory at an indexed file path.
            continue;
        }
        path.push(b'/');
        paths.entry((true, path)).or_insert(Row {
            code: ['?', '?'],
            directory: true,
            source: None,
            submodule: None,
        });
    }
    Ok(())
}

fn relative_path(path: &[u8], prefix: &[u8]) -> Vec<u8> {
    let parts: Vec<_> = path.split(|byte| *byte == b'/').collect();
    let prefix: Vec<_> = prefix
        .split(|byte| *byte == b'/')
        .filter(|part| !part.is_empty())
        .collect();
    let common = parts
        .iter()
        .zip(&prefix)
        .take_while(|(left, right)| left == right)
        .count();
    let mut output = b"../".repeat(prefix.len() - common);
    for (index, part) in parts[common..].iter().enumerate() {
        if index != 0 {
            output.push(b'/');
        }
        output.extend_from_slice(part);
    }
    output
}

pub fn normalize_args(arguments: Vec<OsString>, command: &clap::Command) -> Vec<OsString> {
    // Git requires -u's optional value to be attached; clap's equals rule applies to short flags too.
    let mut cursor = 1;
    while cursor < arguments.len() {
        let Some(value) = arguments[cursor].to_str() else {
            return arguments;
        };
        if value == "status" {
            break;
        }
        if let Some(value) = value.strip_prefix("--") {
            let (name, assigned) = value
                .split_once('=')
                .map_or((value, false), |(name, _)| (name, true));
            let Some(argument) = command
                .get_arguments()
                .find(|arg| arg.get_long() == Some(name))
            else {
                return arguments;
            };
            cursor += usize::from(argument.get_action().takes_values() && !assigned);
        } else if let Some(value) = value.strip_prefix('-') {
            for (offset, flag) in value.char_indices() {
                let Some(argument) = command
                    .get_arguments()
                    .find(|arg| arg.get_short() == Some(flag))
                else {
                    return arguments;
                };
                if argument.get_action().takes_values() {
                    cursor += usize::from(offset + flag.len_utf8() == value.len());
                    break;
                }
            }
        } else {
            return arguments;
        }
        cursor += 1;
    }
    if cursor == arguments.len() {
        return arguments;
    }
    let mut normalized = arguments[..=cursor].to_vec();
    let mut literal = false;
    let mut global_value = false;
    for argument in &arguments[cursor + 1..] {
        if global_value {
            normalized.push(argument.clone());
            global_value = false;
            continue;
        }
        if argument == "--" {
            literal = true;
        }
        if !literal && let Some(value) = argument.to_str() {
            let (global, attached) = if let Some(name) = value.strip_prefix("--") {
                let (name, attached) = name
                    .split_once('=')
                    .map_or((name, false), |(name, _)| (name, true));
                (
                    command
                        .get_arguments()
                        .find(|arg| arg.get_long() == Some(name)),
                    attached,
                )
            } else if value.starts_with('-') {
                (
                    command
                        .get_arguments()
                        .find(|arg| arg.get_short() == value.chars().nth(1)),
                    value.len() > 2,
                )
            } else {
                (None, false)
            };
            if global.is_some_and(|arg| arg.is_global_set() && arg.get_action().takes_values()) {
                global_value = !attached;
                normalized.push(argument.clone());
                continue;
            }
        }
        if !literal
            && let Some(value) = argument
                .to_str()
                .and_then(|value| value.strip_prefix('-'))
                .filter(|value| !value.starts_with('-'))
            && let Some(position) = value.find('u')
        {
            let (flags, value) = value.split_at(position);
            let mode = &value[1..];
            if matches!(mode, "" | "no" | "normal" | "all") {
                if !flags.is_empty() {
                    normalized.push(format!("-{flags}").into());
                }
                normalized.push(
                    format!(
                        "--untracked-files={}",
                        if mode.is_empty() { "all" } else { mode }
                    )
                    .into(),
                );
                continue;
            }
        }
        normalized.push(argument.clone());
    }
    normalized
}

#[cfg(test)]
mod tests {
    use super::normalize_args;

    #[test]
    fn attached_untracked_values_preserve_global_values_and_literal_paths() {
        let command = clap::Command::new("git")
            .arg(
                clap::Arg::new("cwd")
                    .short('C')
                    .global(true)
                    .action(clap::ArgAction::Append),
            )
            .arg(
                clap::Arg::new("config")
                    .short('c')
                    .action(clap::ArgAction::Append),
            );
        for (input, expected) in [
            (
                vec!["git", "status", "-suno"],
                vec!["git", "status", "-s", "--untracked-files=no"],
            ),
            (
                vec!["git", "-C", "status", "add", "-uno"],
                vec!["git", "-C", "status", "add", "-uno"],
            ),
            (
                vec!["git", "-c", "key=status", "status", "-uall"],
                vec!["git", "-c", "key=status", "status", "--untracked-files=all"],
            ),
            (
                vec!["git", "status", "-C", "-uno", "-u"],
                vec!["git", "status", "-C", "-uno", "--untracked-files=all"],
            ),
            (
                vec!["git", "status", "--", "-uno"],
                vec!["git", "status", "--", "-uno"],
            ),
            (
                vec!["git", "status", "-Cuno", "-unormal"],
                vec!["git", "status", "-Cuno", "--untracked-files=normal"],
            ),
            (
                vec!["git", "status", "-uinvalid"],
                vec!["git", "status", "-uinvalid"],
            ),
        ] {
            let actual = normalize_args(input.into_iter().map(Into::into).collect(), &command);
            assert_eq!(
                actual,
                expected
                    .into_iter()
                    .map(std::ffi::OsString::from)
                    .collect::<Vec<_>>()
            );
        }
    }
}
