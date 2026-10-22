use anyhow::{Context, Result, ensure};
use clap::Args;
use std::{fs, io::Write, path::Path};

const ATTRIBUTES: &str = "# git-next: shared text normalization; binary detection and later project rules remain authoritative.\n* text=auto eol=lf\n";
const EDITOR: &str = "# git-next: change line endings without changing indentation, encoding or whitespace rules.\n[*]\nend_of_line = lf\n";

#[derive(Args)]
pub(crate) struct Options {
    #[arg(long)]
    apply: bool,
    #[arg(long)]
    json: bool,
}

pub(crate) fn defaults(mut repo: gix::Repository) -> Result<gix::Repository> {
    let config = repo.config_snapshot();
    let auto_missing = config.string("core.autocrlf").is_none();
    let eol_missing = config.string("core.eol").is_none();
    let mut values = Vec::new();
    if auto_missing && eol_missing {
        values.push("core.autocrlf=input");
    }
    if eol_missing {
        values.push("core.eol=lf");
    }
    if !values.is_empty() {
        let mut config = repo.config_snapshot_mut();
        config.append_config(values, gix::config::Source::Api)?;
        config.commit()?;
    }
    Ok(repo)
}

pub(crate) fn configure_new(repo: &gix::Repository) -> Result<()> {
    crate::repository::config_set(repo, "core.autocrlf", "input")?;
    crate::repository::config_set(repo, "core.eol", "lf")
}

fn create(path: &Path, contents: &str) -> Result<bool> {
    match fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
    {
        Ok(mut file) => {
            file.write_all(contents.as_bytes())?;
            file.flush()?;
            Ok(true)
        }
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => Ok(false),
        Err(error) => Err(error.into()),
    }
}

pub(crate) fn shared_files(root: &Path) -> Result<Vec<&'static str>> {
    validate_files(root)?;
    let mut created = Vec::new();
    for (name, contents) in [(".gitattributes", ATTRIBUTES), (".editorconfig", EDITOR)] {
        if create(&root.join(name), contents)? {
            created.push(name);
        }
    }
    Ok(created)
}

fn validate_files(root: &Path) -> Result<()> {
    for name in [".gitattributes", ".editorconfig"] {
        match fs::symlink_metadata(root.join(name)) {
            Ok(metadata) => ensure!(
                metadata.is_file() && !metadata.file_type().is_symlink(),
                "existing line-ending policy must be a regular file"
            ),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
    }
    Ok(())
}

pub(crate) fn run(options: Options) -> Result<()> {
    let mut repo = crate::repository::open()?;
    let root = repo
        .workdir()
        .context("line-ending policy requires a working tree")?
        .to_owned();
    let mut created = Vec::new();
    if options.apply {
        crate::write::safe_state(&repo)?;
        ensure!(
            !repo.is_bare(),
            "line-ending policy requires a working tree"
        );
        validate_files(&root)?;
        for (key, value) in [("core.autocrlf", "input"), ("core.eol", "lf")] {
            crate::configuration::run(crate::configuration::Options {
                local: true,
                arguments: vec![key.into(), value.into()],
                ..Default::default()
            })?;
        }
        created = shared_files(&root)?;
        repo = crate::repository::open()?;
    }
    let report = serde_json::json!({
        "target_policy": "LF", "core_autocrlf": repo.config_snapshot().string("core.autocrlf").map(|value| value.to_string()),
        "core_eol": repo.config_snapshot().string("core.eol").map(|value| value.to_string()),
        "attributes_present": root.join(".gitattributes").exists(),
        "editorconfig_present": root.join(".editorconfig").exists(),
        "created": created, "existing_rules_preserved": true, "index_renormalized": false,
        "migration": "Review tracked CRLF files, then use add --renormalize in a dedicated normalization change.",
    });
    if options.json {
        println!("{}", serde_json::to_string_pretty(&report)?);
    } else {
        println!(
            "{}",
            crate::language::format(
                "Line endings (target LF): core.autocrlf={autocrlf}, core.eol={eol}",
                &[
                    ("autocrlf", report["core_autocrlf"].to_string()),
                    ("eol", report["core_eol"].to_string()),
                ]
            )
        );
        println!(
            "{}",
            crate::language::text(
                "Existing project rules preserved; no files staged or renormalized."
            )
        );
    }
    Ok(())
}
