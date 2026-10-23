use anyhow::{Context, Result, ensure};
use clap::Parser;
use gix::bstr::ByteSlice;
use gix::refs::transaction::{PreviousValue, RefEdit};

#[derive(Parser, Debug)]
pub struct Options {
    /// Tag name to create, list filter, or delete.
    name: Option<String>,
    /// Create an annotated tag object.
    #[arg(short = 'a', long = "annotate")]
    annotate: bool,
    #[arg(short = 'm', long)]
    message: Option<String>,
    #[arg(short = 'd', long)]
    delete: bool,
    #[arg(short = 'f', long)]
    force: bool,
    /// Object to tag; defaults to `HEAD`.
    commit: Option<String>,
}

pub fn run(options: Options) -> Result<()> {
    let repo = crate::repository::open()?;
    let Some(name) = options.name else {
        let mut names = Vec::new();
        for reference in repo.references()?.tags()? {
            names.push(reference?.name().shorten().to_string());
        }
        names.sort();
        for name in names {
            println!("{name}");
        }
        return Ok(());
    };
    ensure!(!name.starts_with('-') && name != "HEAD", "invalid tag name");
    let full: gix::refs::FullName = format!("refs/tags/{name}")
        .try_into()
        .context("invalid tag name")?;
    if options.delete {
        repo.edit_references([RefEdit::delete(full, PreviousValue::MustExist)])?;
        return Ok(());
    }
    let target = options.commit.as_deref().unwrap_or("HEAD");
    let id = repo.rev_parse_single(target.as_bytes().as_bstr())?;
    let kind = id.object()?.kind;
    let constraint = if options.force {
        PreviousValue::Any
    } else {
        PreviousValue::MustNotExist
    };
    if options.annotate {
        let message = options
            .message
            .as_deref()
            .context("annotated tags require -m")?;
        let tagger = repo
            .committer()
            .transpose()?
            .context("annotated tags require user.name and user.email")?;
        repo.tag(&name, id.detach(), kind, Some(tagger), message, constraint)?;
    } else {
        ensure!(
            options.message.is_none(),
            "cannot use -m with lightweight tags"
        );
        repo.tag_reference(name, id.detach(), constraint)?;
    }
    Ok(())
}
