// Copyright 2026 git-next contributors. MIT license.
//!
//! The interface language of every message comes from a locale document.
//!
//! A locale document is a JSON file in `locales/`. The build embeds all of them,
//! so a language is added by dropping in a file - there is no enumeration of the
//! supported languages anywhere in the code that could fall out of date.
//!
//! English is the source language: the message templates in the code are English,
//! and a locale document only lists the messages it overrides. A message that a
//! locale does not translate is therefore shown in English rather than disappearing.

use anyhow::{Context, Result, bail, ensure};
use clap::{Args, Subcommand};
use serde::Deserialize;
use std::{collections::BTreeMap, ffi::OsString, fs, io::Read, path::PathBuf, sync::OnceLock};

include!(concat!(env!("OUT_DIR"), "/locales_generated.rs"));

/// The locale code of the source language. It never has a document of its own.
pub(crate) const SOURCE_LANGUAGE: &str = "en";

/// One parsed locale document.
#[derive(Debug, Deserialize)]
struct Locale {
    /// The locale code, for example `zh-Hans`.
    code: String,
    /// The name of the language in that language, for example `简体中文`.
    name: String,
    /// The name of the language in English, for example `Simplified Chinese`.
    #[serde(default)]
    english_name: String,
    /// Command summaries, keyed by the command name.
    #[serde(default)]
    commands: BTreeMap<String, String>,
    /// Message translations, keyed by the English source message.
    #[serde(default)]
    messages: BTreeMap<String, String>,
}

/// Every locale shipped with this build.
///
/// The source language is part of the directory as well. It has no document of
/// its own - its messages are the source text in the code - but it has to be
/// findable, so that a device language such as `en-US` resolves to English
/// instead of falling through to whatever the fallback happens to be.
fn locales() -> &'static [Locale] {
    static LOCALES: OnceLock<Vec<Locale>> = OnceLock::new();
    LOCALES.get_or_init(|| {
        let mut parsed: Vec<Locale> = CATALOGS
            .iter()
            .filter_map(|(code, document)| {
                serde_json::from_str::<Locale>(document)
                    .ok()
                    .map(|mut locale| {
                        // The file name is the identity of a locale, so a document
                        // cannot claim a code it was not published under.
                        locale.code = (*code).to_owned();
                        locale
                    })
            })
            .collect();
        parsed.push(Locale {
            code: SOURCE_LANGUAGE.to_owned(),
            name: "English".to_owned(),
            english_name: "English".to_owned(),
            commands: BTreeMap::new(),
            messages: BTreeMap::new(),
        });
        parsed.sort_by(|left, right| left.code.cmp(&right.code));
        parsed
    })
}

/// Find a locale by its code, comparing case-insensitively.
fn find(code: &str) -> Option<&'static Locale> {
    let wanted = normalize_code(code);
    locales()
        .iter()
        .find(|locale| normalize_code(&locale.code) == wanted)
}

/// Normalize a locale code for comparison: lowercase, `-` separated.
fn normalize_code(code: &str) -> String {
    code.trim().replace('_', "-").to_ascii_lowercase()
}

/// The language that is currently in effect.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Language {
    /// The code of the active locale.
    code: String,
    /// The name of the active locale in that language.
    name: String,
    /// The name of the active locale in English.
    english_name: String,
}

impl Language {
    /// The code of the active language.
    pub(crate) fn code(&self) -> &str {
        &self.code
    }

    /// The name of the active language in that language.
    pub(crate) fn name(&self) -> &str {
        &self.name
    }

    /// The name of the active language in English.
    pub(crate) fn english_name(&self) -> &str {
        &self.english_name
    }

    /// Whether this is the source language, which needs no translations.
    fn is_source(&self) -> bool {
        self.code == SOURCE_LANGUAGE
    }

    /// Translate a message, falling back to the English source text.
    ///
    /// The locale documents live for the whole process, so a translated message
    /// is available for as long as the caller needs it. The English fallback is
    /// the source text of the caller, so the result borrows from the input.
    fn translate<'a>(&self, english: &'a str) -> &'a str {
        if self.is_source() {
            return english;
        }
        find(&self.code)
            .and_then(|locale| locale.messages.get(english))
            .map(|translated| &**translated)
            .unwrap_or(english)
    }

    /// Translate a command summary, falling back to the English source text.
    ///
    /// The locale document is a process-wide constant, so the summary outlives
    /// the call.
    fn command_about(&self, name: &str) -> Option<&'static str> {
        if self.is_source() {
            return None;
        }
        find(&self.code)
            .and_then(|locale| locale.commands.get(name))
            .map(|translated| &**translated)
    }
}

/// The user's choice: follow the device, or pin one language.
#[derive(Clone, Debug, PartialEq, Eq)]
enum Selection {
    Auto,
    Fixed(String),
}

impl Selection {
    /// Parse a user supplied language name.
    ///
    /// The name may be a locale code (`zh-Hans`), a legacy alias (`chinese`), or
    /// `auto` to follow the device language.
    fn parse(value: &str) -> Result<Self> {
        let trimmed = value.trim();
        if trimmed.is_empty() {
            bail!("--language requires a value");
        }
        if matches!(trimmed.to_ascii_lowercase().as_str(), "auto" | "system") {
            return Ok(Self::Auto);
        }
        // A legacy alias still selects the language it always selected.
        if let Some(code) = legacy_alias(trimmed) {
            return Ok(Self::Fixed(code.to_owned()));
        }
        // A full locale name is accepted as well, and is resolved the same way a
        // device locale is: `zh-TW` selects the traditional document and
        // `ja-JP` the Japanese one, because a user who names the locale of their
        // system should not have to know which code this build ships.
        if let Some(language) = match_locale(trimmed) {
            return Ok(Self::Fixed(language.code().to_owned()));
        }
        bail!("unsupported language; use one of the listed languages or auto")
    }

    /// The name this selection is stored under.
    fn name(&self) -> String {
        match self {
            Self::Auto => "auto".to_owned(),
            Self::Fixed(code) => code.clone(),
        }
    }

    /// The language this selection currently resolves to.
    fn resolve(&self) -> Language {
        match self {
            Self::Auto => system_language(),
            Self::Fixed(code) => find(code)
                .map(to_language)
                .unwrap_or_else(fallback_language),
        }
    }
}

/// Map the language names that earlier releases accepted to a locale code.
///
/// The aliases keep existing configuration files working after the switch to
/// locale files, so a user who pinned `chinese` keeps Simplified Chinese.
fn legacy_alias(value: &str) -> Option<&'static str> {
    match value.to_ascii_lowercase().as_str() {
        "english" | "en" | "en-us" | "en_us" => Some(SOURCE_LANGUAGE),
        "chinese" | "zh" | "zh-cn" | "zh_cn" | "zh-hans" | "中文" => Some("zh-Hans"),
        _ => None,
    }
}

/// Build the runtime language value of a locale document.
fn to_language(locale: &Locale) -> Language {
    Language {
        code: locale.code.clone(),
        name: locale.name.clone(),
        english_name: if locale.english_name.is_empty() {
            locale.name.clone()
        } else {
            locale.english_name.clone()
        },
    }
}

/// The language used when the device language has no matching locale.
///
/// English is the source language, so it is always available and is the only
/// sensible fallback: it is the language the messages are written in.
fn fallback_language() -> Language {
    find(SOURCE_LANGUAGE)
        .map(to_language)
        .unwrap_or_else(|| Language {
            code: SOURCE_LANGUAGE.to_owned(),
            name: "English".to_owned(),
            english_name: "English".to_owned(),
        })
}

/// Match a device locale against the shipped locales.
///
/// The device reports locales such as `zh-CN`, `zh-Hans-CN` or `pt-BR`. The
/// matching therefore walks from the most specific form to the least specific
/// one: the full code, then the code without its region, then the bare language.
/// That way `zh-Hans` is found for `zh-CN` and `pt` is found for `pt-BR` when a
/// regional document is not shipped.
fn match_locale(locale: &str) -> Option<Language> {
    let normalized = normalize_code(locale);
    if normalized.is_empty() || normalized == "c" || normalized == "posix" {
        return None;
    }
    let parts: Vec<&str> = normalized.split('-').collect();
    let mut candidates: Vec<String> = vec![normalized.clone()];
    if parts.len() > 1 {
        // A region implies a writing system for the languages that distinguish
        // one: `zh-CN` is written in simplified characters and `zh-TW` in
        // traditional ones. The script is therefore resolved before the region
        // is dropped, so `zh-TW` does not end up on a simplified document.
        if let Some(script) = script_for_region(parts[parts.len() - 1]) {
            let with_script = format!("{}-{}", parts[0], script);
            if !candidates.contains(&with_script) {
                candidates.push(with_script);
            }
        }
        // Drop the region: `zh-cn` becomes `zh`.
        let without_region = parts[..parts.len() - 1].join("-");
        if !candidates.contains(&without_region) {
            candidates.push(without_region);
        }
        // Drop the script as well: `zh-hans-cn` becomes `zh`.
        if parts.len() > 2 {
            let language_only = parts[0].to_owned();
            if !candidates.contains(&language_only) {
                candidates.push(language_only);
            }
        }
    }
    for candidate in candidates {
        if let Some(locale) = find(&candidate) {
            return Some(to_language(locale));
        }
    }
    None
}

/// The writing system a region implies, for the languages that distinguish one.
///
/// Only the scripts that are actually shipped need an entry, so the table stays
/// small and a new locale file does not require a change here.
fn script_for_region(region: &str) -> Option<&'static str> {
    match region {
        "cn" | "sg" | "my" => Some("hans"),
        "tw" | "hk" | "mo" => Some("hant"),
        _ => None,
    }
}

/// The language of the device, used when the user follows the system language.
fn system_language() -> Language {
    // The native API is the authority on Windows and on Apple platforms: it
    // reports the language the user actually chose for the system, which is
    // what "follow the device language" has to mean there.
    //
    // The environment variables are only consulted where the native API does
    // not exist, because a shell on those platforms can export a locale that
    // has nothing to do with the language the user picked.
    for locale in sys_locale::get_locales() {
        if let Some(language) = match_locale(&locale) {
            return language;
        }
    }
    for key in ["LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"] {
        if let Ok(value) = std::env::var(key)
            && !value.is_empty()
        {
            let entries: Vec<&str> = value
                .split(':')
                .map(|entry| entry.split(['.', '@']).next().unwrap_or(entry))
                .collect();
            if let Some(language) = entries.iter().find_map(|entry| match_locale(entry)) {
                return language;
            }
            // Either the locale states that the user has no preference, or it
            // names a language no document ships for. The source language is the
            // honest answer in both cases: guessing a related language would
            // show messages the user cannot read.
            let _ = is_unset_locale(entries.first().copied().unwrap_or_default());
            return fallback_language();
        }
    }
    fallback_language()
}

/// Whether a locale states that the user has no language preference.
fn is_unset_locale(locale: &str) -> bool {
    let normalized = normalize_code(locale);
    normalized.is_empty() || normalized == "c" || normalized == "posix"
}

#[derive(Args)]
pub(crate) struct Options {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Print the active language; JSON also reports its selection source.
    Get {
        #[arg(long)]
        json: bool,
    },
    /// Persist a language for this user, or auto to follow the device.
    Set { language: String },
    /// List the built-in languages.
    List,
}

struct Active {
    language: Language,
    selection: Selection,
    source: &'static str,
}

static ACTIVE: OnceLock<Active> = OnceLock::new();

fn settings_path() -> Result<PathBuf> {
    if let Some(root) = std::env::var_os("GIT_NEXT_CONFIG_HOME").filter(|value| !value.is_empty()) {
        let root = std::path::PathBuf::from(root);
        ensure!(
            root.is_absolute(),
            "GIT_NEXT_CONFIG_HOME must be an absolute directory"
        );
        return Ok(root.join("settings.json"));
    }
    let root =
        if let Some(root) = std::env::var_os("XDG_CONFIG_HOME").filter(|value| !value.is_empty()) {
            std::path::PathBuf::from(root)
        } else {
            let home = std::env::var_os("HOME")
                .or_else(|| std::env::var_os("USERPROFILE"))
                .context("HOME/USERPROFILE is unset")?;
            std::path::PathBuf::from(home).join(".config")
        };
    ensure!(
        root.is_absolute(),
        "language settings require an absolute user configuration directory"
    );
    Ok(root.join("git-next/settings.json"))
}

fn settings(path: &std::path::Path) -> Result<serde_json::Value> {
    let file = match fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(serde_json::json!({"schema_version": 1, "language": "auto"}));
        }
        Err(error) => return Err(error).context("could not read git-next language settings"),
    };
    let mut bytes = Vec::new();
    file.take(65537).read_to_end(&mut bytes)?;
    ensure!(
        bytes.len() <= 65536,
        "git-next language settings exceed the size limit"
    );
    let value: serde_json::Value = serde_json::from_slice(&bytes)
        .context("invalid git-next language settings; existing file was preserved")?;
    ensure!(
        value.is_object() && value["schema_version"] == 1,
        "unsupported git-next settings schema"
    );
    ensure!(
        value["language"].is_string(),
        "git-next settings contain no valid language selection"
    );
    Ok(value)
}

/// The language currently in effect.
fn active() -> &'static Active {
    ACTIVE
        .get()
        .expect("the language is initialized before it is used")
}

pub(crate) fn initialize(arguments: &[OsString]) -> Result<()> {
    let mut explicit = None;
    let mut args = arguments.iter().skip(1);
    while let Some(arg) = args.next() {
        if arg == "--" {
            break;
        }
        if arg == "--language" {
            explicit = Some(
                args.next()
                    .context("--language requires a value")?
                    .to_str()
                    .context("language must be UTF-8")?
                    .to_owned(),
            );
        } else if let Some(value) = arg
            .to_str()
            .and_then(|value| value.strip_prefix("--language="))
        {
            explicit = Some(value.to_owned());
        }
    }
    let (selection, source) = if let Some(value) = explicit {
        (Selection::parse(&value)?, "command-line")
    } else if let Ok(value) = std::env::var("GIT_NEXT_LANGUAGE") {
        (Selection::parse(&value)?, "environment")
    } else {
        let selection = match settings_path() {
            Ok(path) => {
                let settings = settings(&path)?;
                Selection::parse(settings["language"].as_str().unwrap_or("auto"))?
            }
            Err(_) if std::env::var_os("GIT_NEXT_CONFIG_HOME").is_none() => Selection::Auto,
            Err(error) => return Err(error),
        };
        let source = if selection == Selection::Auto {
            "system"
        } else {
            "settings"
        };
        (selection, source)
    };
    let language = selection.resolve();
    ACTIVE
        .set(Active {
            language,
            selection,
            source,
        })
        .map_err(|_| anyhow::anyhow!("language is already initialized"))
}

/// Whether the active language is the source language.
pub(crate) fn is_source() -> bool {
    ACTIVE
        .get()
        .is_some_and(|active| active.language.is_source())
}

pub(crate) fn text(english: &str) -> &str {
    active().language.translate(english)
}

pub(crate) fn bytes(english: &[u8]) -> &[u8] {
    // The translation only replaces the source bytes when it really is a
    // translation of them; otherwise the source bytes are returned unchanged.
    if !is_source()
        && let Ok(source) = std::str::from_utf8(english)
    {
        let translated = text(source);
        if translated != source {
            return translated.as_bytes();
        }
    }
    english
}

pub(crate) fn format(english: &str, values: &[(&str, String)]) -> String {
    let template = text(english);
    let mut output = String::with_capacity(template.len() + 32);
    let mut remaining = template;
    while let Some(start) = remaining.find('{') {
        output.push_str(&remaining[..start]);
        let Some(end) = remaining[start + 1..].find('}') else {
            output.push_str(&remaining[start..]);
            return output;
        };
        let end = start + 1 + end;
        let key = &remaining[start + 1..end];
        if let Some((_, value)) = values.iter().find(|(name, _)| *name == key) {
            output.push_str(value)
        } else {
            output.push_str(&remaining[start..=end])
        }
        remaining = &remaining[end + 1..];
    }
    output.push_str(remaining);
    output
}

pub(crate) fn error(error: &anyhow::Error) -> String {
    error
        .chain()
        .map(|cause| text(&cause.to_string()).to_owned())
        .collect::<Vec<_>>()
        .join(": ")
}

pub(crate) fn run(options: Options) -> Result<()> {
    match options.command {
        Command::Get { json } => {
            let active = active();
            if json {
                let supported: Vec<&str> = locales()
                    .iter()
                    .map(|locale| locale.code.as_str())
                    .collect();
                println!(
                    "{}",
                    serde_json::to_string_pretty(&serde_json::json!({
                        "schema_version":1, "language":active.language.code(),
                        "name":active.language.name(),
                        "english_name":active.language.english_name(),
                        "configured":active.selection.name(), "source":active.source,
                        "supported":supported,
                    }))?
                );
            } else {
                println!("{}", active.language.code());
            }
        }
        Command::Set { language } => {
            let selection = Selection::parse(&language)?;
            let path = settings_path()?;
            fs::create_dir_all(path.parent().context("invalid language settings path")?)?;
            let mut value = settings(&path)?;
            value["language"] = selection.name().into();
            let mut data = serde_json::to_vec_pretty(&value)?;
            data.push(b'\n');
            crate::repository::atomic_write(&path, &data)?;
            // The confirmation follows the newly saved preference, not the old
            // process setting, and names the language in that language.
            let resolved = selection.resolve();
            println!(
                "{}",
                text(&format!(
                    "Language set to {name} ({code}).",
                    name = resolved.name(),
                    code = resolved.code()
                ))
            );
        }
        Command::List => {
            for locale in locales() {
                println!(
                    "{}\t{}\t{}",
                    locale.code,
                    locale.name,
                    if locale.english_name.is_empty() {
                        &locale.name
                    } else {
                        &locale.english_name
                    }
                );
            }
            println!("auto\t{}", text("Follow the device language"));
        }
    }
    Ok(())
}

pub(crate) fn localize_command(mut command: clap::Command) -> clap::Command {
    let name = command.get_name().to_owned();
    if let Some(about) = active().language.command_about(&name) {
        command = command.about(about.to_owned());
    }
    if !is_source() {
        // The help template uses clap's own placeholder syntax, so the braces
        // must not be escaped here. Only the headings around the placeholders are
        // replaced with the translated text.
        command = command
            .subcommand_help_heading(text("Commands"))
            .help_template(format!(
                "{{about-with-newline}}\n{usage} {{usage}}\n\n{{all-args}}{{after-help}}",
                usage = text("Usage:"),
            ));
        let arguments: Vec<_> = command
            .get_arguments()
            .map(|arg| {
                (
                    arg.get_id().clone(),
                    arg.get_help().map(ToString::to_string),
                )
            })
            .collect();
        for (id, help) in arguments {
            command = command.mut_arg(id, |arg| {
                let positional = arg.is_positional();
                let mut arg =
                    arg.help_heading(text(if positional { "Arguments" } else { "Options" }));
                if let Some(help) = help {
                    arg = arg.help(text(&help).to_owned());
                }
                arg
            });
        }
    }
    let children: Vec<_> = command
        .get_subcommands()
        .map(|sub| sub.get_name().to_owned())
        .collect();
    for child in children {
        command = command.mut_subcommand(child, localize_command);
    }
    command
}
