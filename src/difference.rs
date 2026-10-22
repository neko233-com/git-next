use anyhow::{Context, Result, bail, ensure};
use clap::Args;
use gix::diff::blob::{
    Algorithm, InternedInput, UnifiedDiff,
    unified_diff::{ConsumeHunk, ContextSize, DiffLineKind, HunkHeader},
};
use gix::{
    ObjectId,
    bstr::{BString, ByteSlice},
};
use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Write,
    path::Path,
};

const MAX_BLOB: u64 = 256 * 1024 * 1024;

#[derive(Args, Default, Clone)]
pub(crate) struct Options {
    #[arg(long, alias = "staged")]
    cached: bool,
    #[arg(long, conflicts_with = "name_status")]
    name_only: bool,
    #[arg(long)]
    name_status: bool,
    #[arg(long)]
    numstat: bool,
    #[arg(long)]
    stat: bool,
    #[arg(long)]
    shortstat: bool,
    #[arg(short = 'p', long)]
    patch: bool,
    #[arg(short = 's', long = "no-patch")]
    no_patch: bool,
    #[arg(short = 'U', long, default_value_t = 3)]
    unified: u32,
    #[arg(short = 'z')]
    nul: bool,
    #[arg(short = 'R')]
    reverse: bool,
    #[arg(long)]
    exit_code: bool,
    #[arg(long)]
    quiet: bool,
    #[arg(long)]
    no_renames: bool,
    #[arg(long)]
    no_ext_diff: bool,
    #[arg(long)]
    no_textconv: bool,
    #[arg(long)]
    no_color: bool,
    #[arg(long,num_args=0..=1,require_equals=true,default_missing_value="always")]
    color: Option<String>,
    #[arg(long)]
    full_index: bool,
    #[arg(long)]
    text: bool,
    #[arg(long)]
    diff_algorithm: Option<String>,
    revisions: Vec<String>,
    #[arg(last = true)]
    paths: Vec<String>,
}

#[derive(Args)]
pub(crate) struct ShowOptions {
    #[command(flatten)]
    diff: Options,
    #[arg(long, alias = "pretty")]
    format: Option<String>,
    #[arg(long)]
    oneline: bool,
    #[arg(long)]
    abbrev_commit: bool,
    #[arg(long)]
    first_parent: bool,
    #[arg(long)]
    date: Option<String>,
}

pub(crate) fn log(
    count: Option<usize>,
    oneline: bool,
    format: Option<String>,
    date: Option<String>,
    revisions: Vec<String>,
    nul: bool,
) -> Result<()> {
    use gix::prelude::ObjectIdExt as _;
    let raw: Vec<_> = std::env::args_os().collect();
    ensure!(
        raw.iter()
            .position(|value| value == "--")
            .is_none_or(|position| position + 1 >= raw.len()),
        "path-limited history is not yet supported"
    );
    let mut limit = count.unwrap_or(usize::MAX);
    let mut starting = Vec::new();
    for revision in revisions {
        if let Some(number) = revision
            .strip_prefix('-')
            .filter(|value| !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit()))
        {
            ensure!(
                count.is_none(),
                "mixed history count spellings are not yet supported"
            );
            limit = number.parse()?;
        } else {
            starting.push(revision);
        }
    }
    ensure!(
        starting.len() <= 1,
        "history currently accepts one starting revision"
    );
    ensure!(
        !nul || format.is_some(),
        "NUL history currently requires an explicit format"
    );
    let repo = crate::repository::open()?;
    let commit = repo
        .rev_parse_single(
            starting
                .first()
                .map(String::as_str)
                .unwrap_or("HEAD")
                .as_bytes()
                .as_bstr(),
        )?
        .object()?
        .peel_to_commit()?;
    let selected_date = date
        .or_else(|| {
            repo.config_snapshot()
                .string("log.date")
                .map(|value| value.to_string())
        })
        .unwrap_or_else(|| "default".into());
    let date_format = |time| format_date(time, &selected_date);
    let options = ShowOptions {
        diff: Options {
            no_patch: true,
            nul,
            ..Options::default()
        },
        format,
        oneline,
        abbrev_commit: false,
        first_parent: false,
        date: None,
    };
    let pretty = options.format.as_deref().unwrap_or("medium");
    let separate = !oneline
        && (matches!(pretty, "medium" | "short" | "full" | "fuller" | "raw")
            || pretty.starts_with("format:"));
    let mut output = std::io::stdout().lock();
    for (number, info) in commit
        .id
        .attach(&repo)
        .ancestors()
        .all()?
        .take(limit)
        .enumerate()
    {
        let commit = repo.find_commit(info?.id)?;
        let bytes = render_commit(&repo, &commit, &options, &date_format)?;
        if number != 0 && separate {
            output.write_all(if nul { b"\0" } else { b"\n" })?;
        }
        output.write_all(&bytes)?;
    }
    Ok(())
}

pub(crate) fn show(options: ShowOptions) -> Result<()> {
    validate_options(&options.diff)?;
    ensure!(
        !options.diff.cached && options.diff.revisions.len() <= 1,
        "show supports one object and does not accept --cached"
    );
    let repo = crate::lfs::prepare(crate::repository::open()?)?.with_object_memory();
    let specification = options
        .diff
        .revisions
        .first()
        .map(String::as_str)
        .unwrap_or("HEAD");
    let mut object = repo
        .rev_parse_single(specification.as_bytes().as_bstr())?
        .object()?;
    let mut out = Vec::new();
    let date = options
        .date
        .as_deref()
        .map(str::to_owned)
        .or_else(|| {
            repo.config_snapshot()
                .string("log.date")
                .map(|v| v.to_string())
        })
        .unwrap_or_else(|| "default".into());
    let date_format = |time| format_date(time, &date);
    let mut tags = 0;
    while object.kind == gix::objs::Kind::Tag {
        tags += 1;
        ensure!(tags <= 32, "tag nesting exceeds the supported limit");
        let tag = object.into_tag();
        let decoded = tag.decode()?;
        out.extend_from_slice(b"tag ");
        out.extend_from_slice(decoded.name);
        out.push(b'\n');
        if let Some(tagger) = tag.tagger()? {
            write_signature(&mut out, b"Tagger: ", tagger)?;
            writeln!(&mut out, "Date:   {}", date_format(tagger.time()?)?)?;
        }
        out.push(b'\n');
        out.extend_from_slice(decoded.message);
        if !out.ends_with(b"\n") {
            out.push(b'\n');
        }
        out.push(b'\n');
        object = repo.find_object(tag.target_id()?.detach())?;
    }
    match object.kind {
        gix::objs::Kind::Blob => {
            out.extend_from_slice(&object.data);
        }
        gix::objs::Kind::Tree => {
            writeln!(&mut out, "tree {specification}\n")?;
            let tree = object.into_tree();
            for entry in tree.iter() {
                let entry = entry?;
                out.extend_from_slice(&quote(
                    entry.filename(),
                    repo.config_snapshot()
                        .boolean("core.quotePath")
                        .unwrap_or(true),
                ));
                if entry.mode().kind() == gix::objs::tree::EntryKind::Tree {
                    out.push(b'/');
                }
                out.push(b'\n');
            }
        }
        gix::objs::Kind::Commit => {
            let commit = object.into_commit();
            out.extend_from_slice(&render_commit(&repo, &commit, &options, &date_format)?);
        }
        _ => unreachable!(),
    }
    std::io::stdout().lock().write_all(&out)?;
    Ok(())
}

fn render_commit(
    repo: &gix::Repository,
    commit: &gix::Commit<'_>,
    options: &ShowOptions,
    date_format: &impl Fn(gix::date::Time) -> Result<String>,
) -> Result<Vec<u8>> {
    let mut out = Vec::new();
    let decoded = commit.decode()?;
    ensure!(
        decoded
            .encoding
            .is_none_or(|encoding| encoding.eq_ignore_ascii_case(b"utf-8")),
        "non-UTF8 commit recoding is not yet supported"
    );
    let author = commit.author()?;
    let committer = commit.committer()?;
    let message = commit.message_raw()?;
    let parsed = commit.message()?;
    let mut subject = Vec::new();
    for line in parsed.title.lines() {
        if !subject.is_empty() {
            subject.push(b' ');
        }
        subject.extend_from_slice(line.trim());
    }
    let parents: Vec<_> = commit.parent_ids().map(|id| id.detach()).collect();
    let pretty = options.format.as_deref().unwrap_or("medium");
    let short = if options.oneline || options.abbrev_commit || pretty.contains("%h") {
        commit.short_id()?.to_string()
    } else {
        String::new()
    };
    if options.oneline || pretty == "oneline" {
        write!(
            &mut out,
            "{} ",
            if options.oneline || options.abbrev_commit {
                short.clone()
            } else {
                commit.id.to_string()
            }
        )?;
        out.extend_from_slice(&subject);
        out.push(b'\n');
    } else if matches!(pretty, "medium" | "short" | "full" | "fuller" | "raw") {
        writeln!(
            &mut out,
            "commit {}",
            if options.abbrev_commit {
                short.clone()
            } else {
                commit.id.to_string()
            }
        )?;
        if pretty == "raw" {
            let header_end = commit
                .data
                .windows(2)
                .position(|pair| pair == b"\n\n")
                .context("commit has no message separator")?;
            out.extend_from_slice(&commit.data[..header_end]);
            out.push(b'\n');
        } else {
            if parents.len() > 1 {
                write!(&mut out, "Merge:")?;
                for parent in &parents {
                    write!(&mut out, " {}", repo.find_commit(*parent)?.short_id()?)?;
                }
                out.push(b'\n');
            }
            write_signature(
                &mut out,
                if pretty == "fuller" {
                    b"Author:     "
                } else {
                    b"Author: "
                },
                author,
            )?;
            if pretty == "fuller" {
                writeln!(&mut out, "AuthorDate: {}", date_format(author.time()?)?)?;
            }
            if matches!(pretty, "full" | "fuller") {
                write_signature(
                    &mut out,
                    if pretty == "fuller" {
                        b"Commit:     "
                    } else {
                        b"Commit: "
                    },
                    committer,
                )?;
            }
            if pretty == "fuller" {
                writeln!(&mut out, "CommitDate: {}", date_format(committer.time()?)?)?;
            } else if pretty == "medium" {
                writeln!(&mut out, "Date:   {}", date_format(author.time()?)?)?;
            }
        }
        out.push(b'\n');
        let message = if pretty == "short" {
            subject.as_slice()
        } else {
            message.trim_end_with(|byte| byte == '\n')
        };
        for line in message.split(|byte| *byte == b'\n') {
            out.extend_from_slice(b"    ");
            out.extend_from_slice(line);
            out.push(b'\n');
        }
    } else if !pretty.is_empty() {
        let (format, terminated) = if let Some(format) = pretty.strip_prefix("format:") {
            (format, false)
        } else {
            (pretty.strip_prefix("tformat:").unwrap_or(pretty), true)
        };
        let bytes = format.as_bytes();
        let mut position = 0;
        while position < bytes.len() {
            if bytes[position] != b'%' {
                out.push(bytes[position]);
                position += 1;
                continue;
            }
            position += 1;
            let token = *bytes
                .get(position)
                .context("incomplete show format placeholder")?;
            position += 1;
            match token {
                b'%' => out.push(b'%'),
                b'n' => out.push(b'\n'),
                b'x' => {
                    let value = bytes
                        .get(position..position + 2)
                        .context("incomplete hexadecimal format byte")?;
                    out.push(u8::from_str_radix(std::str::from_utf8(value)?, 16)?);
                    position += 2;
                }
                b'H' => write!(&mut out, "{}", commit.id)?,
                b'h' => out.extend_from_slice(short.as_bytes()),
                b'T' => write!(&mut out, "{}", commit.tree_id()?)?,
                b't' => write!(&mut out, "{}", commit.tree_id()?.shorten()?)?,
                b'P' | b'p' => {
                    for (index, parent) in parents.iter().enumerate() {
                        if index != 0 {
                            out.push(b' ');
                        }
                        if token == b'P' {
                            write!(&mut out, "{parent}")?;
                        } else {
                            write!(&mut out, "{}", repo.find_commit(*parent)?.short_id()?)?;
                        }
                    }
                }
                b's' => out.extend_from_slice(&subject),
                b'B' => out.extend_from_slice(message),
                b'b' => {
                    if let Some(body) = parsed.body {
                        out.extend_from_slice(body);
                    }
                }
                b'a' | b'c' => {
                    let signature = if token == b'a' { author } else { committer };
                    let field = *bytes.get(position).context("incomplete signature format")?;
                    position += 1;
                    match field {
                        b'n' => out.extend_from_slice(signature.name),
                        b'e' => out.extend_from_slice(signature.email),
                        b't' => write!(&mut out, "{}", signature.time()?.seconds)?,
                        b'd' => out.extend_from_slice(date_format(signature.time()?)?.as_bytes()),
                        b'i' => out.extend_from_slice(
                            signature
                                .time()?
                                .format(gix::date::time::format::ISO8601)?
                                .as_bytes(),
                        ),
                        b'I' => out.extend_from_slice(
                            signature
                                .time()?
                                .format(gix::date::time::format::ISO8601_STRICT)?
                                .as_bytes(),
                        ),
                        _ => bail!("unsupported signature format placeholder"),
                    }
                }
                _ => bail!("unsupported show format placeholder"),
            }
        }
        if terminated {
            out.push(if options.diff.nul { 0 } else { b'\n' });
        }
    }
    if !options.diff.no_patch && !options.diff.quiet {
        ensure!(
            parents.len() <= 1 || options.first_parent,
            "combined merge diffs are not yet supported; use --first-parent or --no-patch"
        );
        let index = repo.index_or_empty()?;
        let mut old = parents
            .first()
            .map(|id| from_revision(repo, &id.to_string()))
            .transpose()?
            .unwrap_or_default();
        let mut new = from_revision(repo, &commit.id.to_string())?;
        let mut pathspec = repo.pathspec(
            false,
            options
                .diff
                .paths
                .iter()
                .map(|path| path.as_bytes().as_bstr()),
            false,
            &index,
            gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
        )?;
        old.retain(|path, _| pathspec.is_included(path.as_bstr(), Some(false)));
        new.retain(|path, _| pathspec.is_included(path.as_bstr(), Some(false)));
        if options.diff.reverse {
            std::mem::swap(&mut old, &mut new);
        }
        let changes = changes(repo, &old, &new, options.diff.no_renames)?;
        let bytes = render(repo, &index, &changes, &options.diff)?;
        if !out.is_empty()
            && !bytes.is_empty()
            && options.diff.patch
            && !options.oneline
            && pretty != "oneline"
            && options.diff.stat
        {
            out.extend_from_slice(b"---\n");
        } else if !out.is_empty() && !bytes.is_empty() && !options.oneline && pretty != "oneline" {
            out.push(b'\n');
        }
        out.extend_from_slice(&bytes);
    }
    Ok(out)
}

fn write_signature(
    out: &mut Vec<u8>,
    label: &[u8],
    signature: gix::actor::SignatureRef<'_>,
) -> Result<()> {
    out.extend_from_slice(label);
    out.extend_from_slice(signature.name);
    out.extend_from_slice(b" <");
    out.extend_from_slice(signature.email);
    out.extend_from_slice(b">\n");
    Ok(())
}

type Files = BTreeMap<BString, (ObjectId, gix::index::entry::Mode)>;

fn validate_options(options: &Options) -> Result<()> {
    ensure!(options.unified <= 1_000_000, "diff context is too large");
    if let Some(color) = &options.color {
        ensure!(
            matches!(color.as_str(), "never" | "auto"),
            "colored diff output is not yet implemented"
        );
    }
    if let Some(algorithm) = &options.diff_algorithm {
        ensure!(
            matches!(
                algorithm.as_str(),
                "myers" | "default" | "minimal" | "histogram"
            ),
            "unsupported diff algorithm"
        );
    }
    ensure!(
        !(options.name_only || options.name_status)
            || !(options.numstat || options.stat || options.shortstat),
        "combined name and statistics output modes are not yet supported"
    );
    Ok(())
}

pub(crate) fn run(mut options: Options) -> Result<()> {
    validate_options(&options)?;
    let repo = crate::lfs::prepare(crate::repository::open()?)?.with_object_memory();
    let index = repo.index_or_empty()?;
    ensure!(
        !index.is_sparse() && index.entries().iter().all(|entry| entry.stage_raw() == 0),
        "conflicted and sparse diff semantics are not yet supported"
    );
    if options.revisions.len() == 1 && options.revisions[0].contains("..") {
        let range = options.revisions.pop().context("missing diff range")?;
        ensure!(
            !range.contains("..."),
            "three-dot diff requires merge-base support"
        );
        let (old, new) = range.split_once("..").context("invalid diff range")?;
        options.revisions = vec![
            if old.is_empty() { "HEAD" } else { old }.into(),
            if new.is_empty() { "HEAD" } else { new }.into(),
        ];
    }
    let mut revisions = Vec::new();
    let explicit_paths = std::env::args_os().any(|arg| arg == "--");
    let mut path_mode = false;
    for value in &options.revisions {
        let revision = repo.rev_parse_single(value.as_bytes().as_bstr()).is_ok();
        let mut candidate = repo.pathspec(
            false,
            [value.as_bytes().as_bstr()],
            false,
            &index,
            gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
        )?;
        let known_path = Path::new(value).exists()
            || (value.contains(['*', '?', '[']) || value.starts_with(":("))
                && index
                    .entries()
                    .iter()
                    .any(|entry| candidate.is_included(entry.path(&index), Some(false)));
        if !explicit_paths && known_path && (path_mode || !revision) {
            path_mode = true;
            options.paths.push(value.clone());
        } else {
            ensure!(
                !path_mode,
                "unknown path after diff path arguments; use -- to separate revisions and pathspecs"
            );
            ensure!(
                explicit_paths || !revision || !known_path,
                "ambiguous diff argument names both a revision and a path; use --"
            );
            revisions.push(value.clone());
        }
    }
    ensure!(
        revisions.len() <= 2 && (!options.cached || revisions.len() <= 1),
        "unsupported diff revision combination"
    );
    let mut selection = repo.pathspec(
        false,
        options.paths.iter().map(|path| path.as_bytes().as_bstr()),
        false,
        &index,
        gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
    )?;
    let selected = |files: Files, selection: &mut gix::Pathspec<'_>| -> Files {
        files
            .into_iter()
            .filter(|(path, _)| selection.is_included(path.as_bstr(), Some(false)))
            .collect()
    };
    let old = if let Some(revision) = revisions.first() {
        Some(from_revision(&repo, revision)?)
    } else if options.cached {
        Some(
            repo.head()?
                .try_peel_to_id()?
                .map(|id| from_revision(&repo, &id.to_string()))
                .transpose()?
                .unwrap_or_default(),
        )
    } else {
        None
    };
    let mut new = if revisions.len() == 2 {
        from_revision(&repo, &revisions[1])?
    } else if options.cached {
        from_index(&index, true)
    } else {
        match worktree(&repo, &index)? {
            Some(files) => files,
            None if old.is_none() => {
                let bytes = render(&repo, &index, &[], &options)?;
                std::io::stdout().lock().write_all(&bytes)?;
                return Ok(());
            }
            None => from_index(&index, false),
        }
    };
    let mut old = old.unwrap_or_else(|| from_index(&index, true));
    old = selected(old, &mut selection);
    new = selected(new, &mut selection);
    if options.reverse {
        std::mem::swap(&mut old, &mut new);
    }
    let changes = changes(&repo, &old, &new, options.no_renames)?;
    let bytes = render(&repo, &index, &changes, &options)?;
    std::io::stdout().lock().write_all(&bytes)?;
    if !changes.is_empty() && (options.exit_code || options.quiet) {
        return Err(crate::CommandStatus(1).into());
    }
    Ok(())
}

fn from_index(index: &gix::index::State, omit_intent: bool) -> Files {
    index
        .entries()
        .iter()
        .filter(|entry| {
            !omit_intent
                || !entry
                    .flags
                    .contains(gix::index::entry::Flags::INTENT_TO_ADD)
        })
        .map(|entry| (entry.path(index).to_owned(), (entry.id, entry.mode)))
        .collect()
}

fn from_revision(repo: &gix::Repository, revision: &str) -> Result<Files> {
    let tree = repo
        .rev_parse_single(revision.as_bytes().as_bstr())?
        .object()?
        .peel_to_tree()?
        .id;
    let index = repo.index_from_tree(&tree)?;
    Ok(from_index(&index, true))
}

fn worktree(repo: &gix::Repository, index: &gix::index::State) -> Result<Option<Files>> {
    let root = repo
        .workdir()
        .context("worktree diff is unavailable in a bare repository")?;
    let mut changed = BTreeSet::new();
    let threads = index.entries().len().div_ceil(2000).clamp(1, 8);
    for item in repo
        .status(gix::progress::Discard)?
        .untracked_files(gix::status::UntrackedFiles::None)
        .index_worktree_options_mut(|options| options.thread_limit = Some(threads))
        .into_index_worktree_iter(std::iter::empty())?
    {
        let item = item?;
        if item.summary().is_some() && index.entry_index_by_path(item.rela_path()).is_ok() {
            changed.insert(item.rela_path().to_owned());
        }
    }
    let (mut filters, _) = repo.filter_pipeline(None)?;
    if changed.is_empty()
        && !index.entries().iter().any(|entry| {
            entry
                .flags
                .contains(gix::index::entry::Flags::INTENT_TO_ADD)
        })
    {
        return Ok(None);
    }
    let mut files = from_index(index, false);
    for path in changed {
        let native_path = gix::path::from_bstr(path.as_bstr())?;
        match fs::symlink_metadata(root.join(&native_path)) {
            Ok(metadata) if metadata.is_dir() => {
                ensure!(
                    files
                        .get(&path)
                        .is_none_or(|(_, mode)| *mode != gix::index::entry::Mode::COMMIT),
                    "dirty submodule worktree diff needs submodule support"
                );
                files.remove(&path);
                continue;
            }
            Ok(metadata) => ensure!(
                metadata.len() <= MAX_BLOB,
                "worktree blob exceeds the current 256 MiB diff limit"
            ),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                files.remove(&path);
                continue;
            }
            Err(error) => return Err(error.into()),
        }
        if let Some((id, kind, _)) = filters.worktree_file_to_object(path.as_bstr(), index)? {
            let mut mode: gix::index::entry::Mode = kind.into();
            if !repo
                .config_snapshot()
                .boolean("core.filemode")
                .unwrap_or(cfg!(unix))
                && matches!(
                    mode,
                    gix::index::entry::Mode::FILE | gix::index::entry::Mode::FILE_EXECUTABLE
                )
                && let Some((_, original)) = files.get(&path)
                && matches!(
                    *original,
                    gix::index::entry::Mode::FILE | gix::index::entry::Mode::FILE_EXECUTABLE
                )
            {
                mode = *original;
            }
            files.insert(path, (id, mode));
        } else {
            files.remove(&path);
        }
    }
    Ok(Some(files))
}

#[derive(Clone)]
struct Delta {
    old_path: BString,
    new_path: BString,
    old: Option<(ObjectId, gix::objs::tree::EntryMode)>,
    new: Option<(ObjectId, gix::objs::tree::EntryMode)>,
    rewrite: Option<(bool, u32)>,
}

fn changes(
    repo: &gix::Repository,
    old: &Files,
    new: &Files,
    no_renames: bool,
) -> Result<Vec<Delta>> {
    if old == new {
        return Ok(Vec::new());
    }
    let tree = |files: &Files| -> Result<ObjectId> {
        let mut tree = crate::write::Tree::default();
        for (path, (id, mode)) in files {
            tree.insert(
                path.as_slice(),
                mode.to_tree_entry_mode()
                    .context("invalid diff entry mode")?,
                *id,
            )?;
        }
        tree.write(repo)
    };
    let old = repo.find_tree(tree(old)?)?;
    let new = repo.find_tree(tree(new)?)?;
    let options = no_renames.then(|| gix::diff::Options::default().with_rewrites(None));
    let mut changes = Vec::new();
    for change in repo.diff_tree_to_tree(Some(&old), Some(&new), options)? {
        use gix::diff::tree_with_rewrites::Change;
        let delta = match change {
            Change::Addition {
                location,
                entry_mode,
                id,
                ..
            } => Delta {
                old_path: location.clone(),
                new_path: location,
                old: None,
                new: Some((id, entry_mode)),
                rewrite: None,
            },
            Change::Deletion {
                location,
                entry_mode,
                id,
                ..
            } => Delta {
                old_path: location.clone(),
                new_path: location,
                old: Some((id, entry_mode)),
                new: None,
                rewrite: None,
            },
            Change::Modification {
                location,
                previous_entry_mode,
                previous_id,
                entry_mode,
                id,
                ..
            } => Delta {
                old_path: location.clone(),
                new_path: location,
                old: Some((previous_id, previous_entry_mode)),
                new: Some((id, entry_mode)),
                rewrite: None,
            },
            Change::Rewrite {
                source_location,
                source_entry_mode,
                source_id,
                location,
                entry_mode,
                id,
                diff,
                copy,
                ..
            } => Delta {
                old_path: source_location,
                new_path: location,
                old: Some((source_id, source_entry_mode)),
                new: Some((id, entry_mode)),
                rewrite: Some((
                    copy,
                    diff.map(|diff| (diff.similarity * 100.0).floor() as u32)
                        .unwrap_or(100),
                )),
            },
        };
        if delta
            .old
            .iter()
            .chain(delta.new.iter())
            .any(|(_, mode)| mode.kind() == gix::objs::tree::EntryKind::Tree)
        {
            continue;
        }
        changes.push(delta);
    }
    changes.sort_by(|a, b| a.new_path.cmp(&b.new_path));
    Ok(changes)
}

pub(crate) fn status_rename_score(
    repo: &gix::Repository,
    old: &gix::index::Entry,
    new: &gix::index::Entry,
) -> Result<u32> {
    if old.id == new.id {
        return Ok(100);
    }
    let old = object_data(
        repo,
        Some((
            old.id,
            old.mode
                .to_tree_entry_mode()
                .context("invalid rename source mode")?,
        )),
    )?;
    let new = object_data(
        repo,
        Some((
            new.id,
            new.mode
                .to_tree_entry_mode()
                .context("invalid rename destination mode")?,
        )),
    )?;
    let source = span_counts(&old);
    let destination = span_counts(&new);
    let copied: u64 = source
        .iter()
        .map(|(hash, count)| count.min(destination.get(hash).unwrap_or(&0)))
        .sum();
    Ok((copied * 100 / old.len().max(new.len()).max(1) as u64) as u32)
}

fn span_counts(data: &[u8]) -> BTreeMap<u32, u64> {
    // Git's similarity metric counts reusable LF/64-byte spans, irrespective of their order.
    let text = !data[..data.len().min(8000)].contains(&0);
    let mut counts = BTreeMap::new();
    let mut span = Vec::with_capacity(64);
    let mut record = |span: &[u8]| {
        let signature = span.iter().fold(0_u64, |state, byte| {
            let rotated = state.rotate_left(7);
            (rotated & 0xffff_ffff_0000_0000)
                | u64::from((rotated as u32).wrapping_add(u32::from(*byte)))
        });
        let hash =
            (signature as u32).wrapping_add(((signature >> 32) as u32).wrapping_mul(97)) % 107927;
        *counts.entry(hash).or_default() += span.len() as u64;
    };
    for (position, &byte) in data.iter().enumerate() {
        if text && byte == b'\r' && data.get(position + 1) == Some(&b'\n') {
            continue;
        }
        span.push(byte);
        if span.len() == 64 || byte == b'\n' {
            record(&span);
            span.clear();
        }
    }
    if !span.is_empty() {
        record(&span);
    }
    counts
}

fn object_data(
    repo: &gix::Repository,
    object: Option<(ObjectId, gix::objs::tree::EntryMode)>,
) -> Result<Vec<u8>> {
    let Some((id, mode)) = object else {
        return Ok(Vec::new());
    };
    if mode.kind() == gix::objs::tree::EntryKind::Commit {
        return Ok(format!("Subproject commit {id}\n").into_bytes());
    }
    ensure!(
        repo.find_header(id)?.size() <= MAX_BLOB,
        "blob exceeds the current 256 MiB diff limit"
    );
    let object = repo.find_object(id)?;
    ensure!(
        object.kind == gix::objs::Kind::Blob,
        "diff entry does not point to a blob"
    );
    Ok(object.data.to_vec())
}

pub(crate) fn quote(path: &[u8], quote_unicode: bool) -> Vec<u8> {
    if !path
        .iter()
        .any(|byte| matches!(byte, 0..=31 | b'"' | b'\\' | 127) || (quote_unicode && *byte >= 128))
    {
        return path.to_vec();
    }
    let mut out = vec![b'"'];
    for &byte in path {
        match byte {
            b'"' | b'\\' => {
                out.push(b'\\');
                out.push(byte);
            }
            b'\n' => out.extend_from_slice(b"\\n"),
            b'\r' => out.extend_from_slice(b"\\r"),
            b'\t' => out.extend_from_slice(b"\\t"),
            7 => out.extend_from_slice(b"\\a"),
            8 => out.extend_from_slice(b"\\b"),
            11 => out.extend_from_slice(b"\\v"),
            12 => out.extend_from_slice(b"\\f"),
            0..=31 | 127..=255 if byte < 32 || byte == 127 || quote_unicode => {
                out.extend_from_slice(format!("\\{byte:03o}").as_bytes())
            }
            _ => out.push(byte),
        }
    }
    out.push(b'"');
    out
}

struct Hunks<'a> {
    bytes: Vec<u8>,
    old_lines: Vec<&'a [u8]>,
}
impl ConsumeHunk for Hunks<'_> {
    type Out = Vec<u8>;
    fn consume_hunk(
        &mut self,
        header: HunkHeader,
        lines: &[(DiffLineKind, &[u8])],
    ) -> std::io::Result<()> {
        let range = |start: u32, count: u32| match count {
            0 => format!("{},0", start.saturating_sub(1)),
            1 => start.to_string(),
            _ => format!("{start},{count}"),
        };
        write!(
            &mut self.bytes,
            "@@ -{} +{} @@",
            range(header.before_hunk_start, header.before_hunk_len),
            range(header.after_hunk_start, header.after_hunk_len)
        )?;
        let previous = header.before_hunk_start.saturating_sub(1) as usize;
        if let Some(line) = self.old_lines.get(..previous).and_then(|lines| {
            lines.iter().rev().find(|line| {
                line.first()
                    .is_some_and(|c| c.is_ascii_alphabetic() || matches!(c, b'_' | b'$'))
            })
        }) {
            let line = line.strip_suffix(b"\n").unwrap_or(line);
            let line = line.strip_suffix(b"\r").unwrap_or(line);
            self.bytes.push(b' ');
            self.bytes.extend_from_slice(&line[..line.len().min(80)]);
        }
        self.bytes.push(b'\n');
        for (kind, line) in lines {
            self.bytes.push(kind.to_prefix() as u8);
            self.bytes.extend_from_slice(line);
            if !line.ends_with(b"\n") {
                self.bytes
                    .extend_from_slice(b"\n\\ No newline at end of file\n");
            }
        }
        Ok(())
    }
    fn finish(self) -> Self::Out {
        self.bytes
    }
}

fn render(
    repo: &gix::Repository,
    index: &gix::index::State,
    changes: &[Delta],
    options: &Options,
) -> Result<Vec<u8>> {
    let config = repo.config_snapshot();
    let algorithm = options
        .diff_algorithm
        .clone()
        .or_else(|| config.string("diff.algorithm").map(|v| v.to_string()))
        .unwrap_or_else(|| "myers".into());
    let algorithm = match algorithm.as_str() {
        "myers" | "default" => Algorithm::Myers,
        "minimal" => Algorithm::MyersMinimal,
        "histogram" => Algorithm::Histogram,
        _ => bail!("unsupported diff algorithm"),
    };
    if options.quiet || changes.is_empty() {
        return Ok(Vec::new());
    }
    ensure!(
        options.no_ext_diff
            || (config.string("diff.external").is_none()
                && std::env::var_os("GIT_EXTERNAL_DIFF").is_none()),
        "external diff drivers need explicit driver support; use --no-ext-diff for internal diff"
    );
    let quote_unicode = config.boolean("core.quotePath").unwrap_or(true);
    let mut attributes = repo.attributes_only(
        index,
        gix_worktree::stack::state::attributes::Source::WorktreeThenIdMapping,
    )?;
    let mut outcome = gix::attrs::search::Outcome::default();
    outcome.initialize_with_selection(&Default::default(), ["diff"]);
    let mut out = Vec::new();
    let mut numeric = Vec::new();
    let mut statistics = Vec::new();
    for delta in changes {
        let status = if let Some((copy, score)) = delta.rewrite {
            format!("{}{score:03}", if copy { 'C' } else { 'R' })
        } else if delta.old.is_none() {
            "A".into()
        } else if delta.new.is_none() {
            "D".into()
        } else if different_types(delta) {
            "T".into()
        } else {
            "M".into()
        };
        let path = if options.nul {
            delta.new_path.to_vec()
        } else {
            quote(delta.new_path.as_slice(), quote_unicode)
        };
        let separator = if options.nul { 0 } else { b'\n' };
        if options.name_only {
            out.extend_from_slice(&path);
            out.push(separator);
            continue;
        }
        if options.name_status {
            out.extend_from_slice(status.as_bytes());
            out.push(if options.nul { 0 } else { b'\t' });
            if delta.rewrite.is_some() {
                out.extend_from_slice(&if options.nul {
                    delta.old_path.to_vec()
                } else {
                    quote(delta.old_path.as_slice(), quote_unicode)
                });
                out.push(if options.nul { 0 } else { b'\t' });
            }
            out.extend_from_slice(&path);
            out.push(separator);
            continue;
        }
        let before = object_data(repo, delta.old)?;
        let after = object_data(repo, delta.new)?;
        attributes
            .at_entry(delta.new_path.as_bstr(), None)?
            .matching_attributes(&mut outcome);
        let attr = outcome
            .iter_selected()
            .next()
            .context("diff attribute selection")?;
        let mut binary = before
            .iter()
            .take(8000)
            .chain(after.iter().take(8000))
            .any(|byte| *byte == 0);
        match attr.assignment.state {
            gix::attrs::StateRef::Unset => binary = true,
            gix::attrs::StateRef::Set => binary = false,
            gix::attrs::StateRef::Value(driver) => {
                let driver = driver.as_bstr().to_str()?;
                ensure!(
                    options.no_textconv
                        || config.string(&format!("diff.{driver}.textconv")).is_none(),
                    "configured text conversion is not yet supported"
                );
                ensure!(
                    options.no_ext_diff
                        || config.string(&format!("diff.{driver}.command")).is_none(),
                    "configured external diff command is not yet supported"
                );
                if let Some(value) = config.boolean(&format!("diff.{driver}.binary")) {
                    binary = value;
                }
            }
            _ => {}
        }
        if options.text {
            binary = false;
        }
        let input = InternedInput::new(
            gix::diff::blob::sources::byte_lines(if binary { &[] } else { &before }),
            gix::diff::blob::sources::byte_lines(if binary { &[] } else { &after }),
        );
        let diff = gix::diff::blob::diff_with_slider_heuristics(algorithm, &input);
        statistics.push(FileStat {
            path: if delta.rewrite.is_some() {
                rename_path(delta.old_path.as_slice(), delta.new_path.as_slice())
            } else {
                delta.new_path.to_vec()
            },
            added: if binary {
                0
            } else {
                diff.count_additions() as usize
            },
            removed: if binary {
                0
            } else {
                diff.count_removals() as usize
            },
            binary: binary.then_some((before.len(), after.len())),
        });
        if options.numstat {
            if binary {
                numeric.extend_from_slice(b"-\t-\t");
            } else {
                write!(
                    &mut numeric,
                    "{}\t{}\t",
                    diff.count_additions(),
                    diff.count_removals()
                )?;
            }
            if delta.rewrite.is_some() {
                if options.nul {
                    numeric.push(0);
                    numeric.extend_from_slice(delta.old_path.as_slice());
                    numeric.push(0);
                    numeric.extend_from_slice(delta.new_path.as_slice());
                } else {
                    numeric.extend_from_slice(&quote(
                        &rename_path(delta.old_path.as_slice(), delta.new_path.as_slice()),
                        quote_unicode,
                    ));
                }
            } else {
                numeric.extend_from_slice(&path);
            }
            numeric.push(separator);
        }
        if options.no_patch
            || ((options.numstat || options.stat || options.shortstat) && !options.patch)
        {
            continue;
        }
        if different_types(delta) {
            let mut patch_options = options.clone();
            patch_options.numstat = false;
            patch_options.stat = false;
            patch_options.shortstat = false;
            let removed = Delta {
                old_path: delta.old_path.clone(),
                new_path: delta.old_path.clone(),
                old: delta.old,
                new: None,
                rewrite: None,
            };
            let added = Delta {
                old_path: delta.new_path.clone(),
                new_path: delta.new_path.clone(),
                old: None,
                new: delta.new,
                rewrite: None,
            };
            out.extend_from_slice(&render(repo, index, &[removed, added], &patch_options)?);
            continue;
        }
        let prefixed = |prefix: &[u8], path: &[u8]| {
            let mut value = prefix.to_vec();
            value.extend_from_slice(path);
            quote(&value, quote_unicode)
        };
        let old_label = prefixed(
            if options.reverse { b"b/" } else { b"a/" },
            delta.old_path.as_slice(),
        );
        let new_label = prefixed(
            if options.reverse { b"a/" } else { b"b/" },
            delta.new_path.as_slice(),
        );
        out.extend_from_slice(b"diff --git ");
        out.extend_from_slice(&old_label);
        out.push(b' ');
        out.extend_from_slice(&new_label);
        out.push(b'\n');
        match (delta.old, delta.new) {
            (None, Some((_, mode))) => writeln!(&mut out, "new file mode {:06o}", mode.value())?,
            (Some((_, mode)), None) => {
                writeln!(&mut out, "deleted file mode {:06o}", mode.value())?
            }
            (Some((_, old)), Some((_, new))) if old != new => writeln!(
                &mut out,
                "old mode {:06o}\nnew mode {:06o}",
                old.value(),
                new.value()
            )?,
            _ => {}
        }
        if let Some((copy, score)) = delta.rewrite {
            writeln!(&mut out, "similarity index {score}%")?;
            for (kind, path) in [("from", &delta.old_path), ("to", &delta.new_path)] {
                write!(&mut out, "{} {kind} ", if copy { "copy" } else { "rename" })?;
                out.extend_from_slice(&quote(path.as_slice(), quote_unicode));
                out.push(b'\n');
            }
        }
        if delta.old.map(|(id, _)| id) == delta.new.map(|(id, _)| id) {
            continue;
        }
        let full_length = repo.object_hash().len_in_bytes() * 2;
        let configured = config.string("core.abbrev");
        let length = if options.full_index
            || configured
                .as_ref()
                .is_some_and(|value| matches!(value.as_slice(), b"false" | b"0"))
        {
            full_length
        } else if let Some(value) = configured.filter(|value| value.as_slice() != b"auto") {
            value.to_str()?.parse::<usize>()?
        } else {
            (64 - repo.objects.packed_object_count()?.leading_zeros())
                .div_ceil(2)
                .max(7) as usize
        };
        ensure!(
            (4..=full_length).contains(&length),
            "invalid abbreviated object ID length"
        );
        write!(
            &mut out,
            "index {}..{}",
            abbreviated(repo, delta.old.map(|(id, _)| id), length)?,
            abbreviated(repo, delta.new.map(|(id, _)| id), length)?
        )?;
        if let (Some((_, old)), Some((_, new))) = (delta.old, delta.new)
            && old == new
        {
            write!(&mut out, " {:06o}", old.value())?;
        }
        out.push(b'\n');
        let before_label = if delta.old.is_some() {
            old_label
        } else {
            b"/dev/null".to_vec()
        };
        let after_label = if delta.new.is_some() {
            new_label
        } else {
            b"/dev/null".to_vec()
        };
        if binary {
            out.extend_from_slice(b"Binary files ");
            out.extend_from_slice(&before_label);
            out.extend_from_slice(b" and ");
            out.extend_from_slice(&after_label);
            out.extend_from_slice(b" differ\n");
            continue;
        }
        let hunks = UnifiedDiff::new(
            &diff,
            &input,
            Hunks {
                bytes: Vec::new(),
                old_lines: before.split_inclusive(|b| *b == b'\n').collect(),
            },
            ContextSize::symmetrical(options.unified),
        )
        .consume()?;
        if hunks.is_empty() {
            continue;
        }
        for (kind, label) in [
            (b"--- ".as_slice(), &before_label),
            (b"+++ ".as_slice(), &after_label),
        ] {
            out.extend_from_slice(kind);
            out.extend_from_slice(label);
            if label.contains(&b' ') && !label.starts_with(b"\"") {
                out.push(b'\t');
            }
            out.push(b'\n');
        }
        out.extend_from_slice(&hunks);
    }
    if options.stat || options.shortstat {
        numeric.extend_from_slice(&render_statistics(
            &statistics,
            options.stat,
            quote_unicode,
        )?);
    }
    if !numeric.is_empty() && !out.is_empty() {
        numeric.push(b'\n');
    }
    numeric.extend_from_slice(&out);
    Ok(numeric)
}

fn abbreviated(repo: &gix::Repository, id: Option<ObjectId>, minimum: usize) -> Result<String> {
    let Some(id) = id else {
        return Ok("0".repeat(minimum));
    };
    for length in minimum..=id.kind().len_in_bytes() * 2 {
        let prefix = gix::hash::Prefix::new(&id, length)?;
        match repo.objects.lookup_prefix(prefix, None)? {
            None => return Ok(prefix.to_string()),
            Some(Ok(candidate)) if candidate == id => return Ok(prefix.to_string()),
            _ => {}
        }
    }
    Ok(id.to_string())
}

fn different_types(delta: &Delta) -> bool {
    matches!((delta.old,delta.new),(Some((_,old)),Some((_,new))) if old.value() & 0o170000 != new.value() & 0o170000)
}

struct FileStat {
    path: Vec<u8>,
    added: usize,
    removed: usize,
    binary: Option<(usize, usize)>,
}

fn rename_path(old: &[u8], new: &[u8]) -> Vec<u8> {
    let old_parts: Vec<_> = old.split(|byte| *byte == b'/').collect();
    let new_parts: Vec<_> = new.split(|byte| *byte == b'/').collect();
    let mut prefix = 0;
    while prefix < old_parts.len().min(new_parts.len()) && old_parts[prefix] == new_parts[prefix] {
        prefix += 1;
    }
    let mut suffix = 0;
    while suffix < old_parts.len().min(new_parts.len()).saturating_sub(prefix)
        && old_parts[old_parts.len() - suffix - 1] == new_parts[new_parts.len() - suffix - 1]
    {
        suffix += 1;
    }
    let mut out = Vec::new();
    for part in &old_parts[..prefix] {
        out.extend_from_slice(part);
        out.push(b'/');
    }
    if prefix != 0 || suffix != 0 {
        out.push(b'{');
    }
    out.extend_from_slice(&old_parts[prefix..old_parts.len() - suffix].join(b"/".as_slice()));
    out.extend_from_slice(b" => ");
    out.extend_from_slice(&new_parts[prefix..new_parts.len() - suffix].join(b"/".as_slice()));
    if prefix != 0 || suffix != 0 {
        out.push(b'}');
    }
    for part in &old_parts[old_parts.len() - suffix..] {
        out.push(b'/');
        out.extend_from_slice(part);
    }
    out
}

fn render_statistics(files: &[FileStat], details: bool, quote_unicode: bool) -> Result<Vec<u8>> {
    let mut out = Vec::new();
    if files.is_empty() {
        return Ok(out);
    }
    if details {
        let paths: Vec<_> = files
            .iter()
            .map(|file| quote(&file.path, quote_unicode))
            .collect();
        let width = paths.iter().map(Vec::len).max().unwrap_or(0).min(50);
        let largest = files
            .iter()
            .filter(|file| file.binary.is_none())
            .map(|file| file.added + file.removed)
            .max()
            .unwrap_or(0);
        let digits =
            largest
                .to_string()
                .len()
                .max(if files.iter().any(|file| file.binary.is_some()) {
                    3
                } else {
                    1
                });
        let graph_width = 80usize
            .saturating_sub(width + digits + 6)
            .min(largest)
            .max(1);
        for (file, path) in files.iter().zip(paths) {
            let path = if path.len() > width {
                let mut value = b"...".to_vec();
                value.extend_from_slice(&path[path.len() - width.saturating_sub(3)..]);
                value
            } else {
                path
            };
            out.push(b' ');
            out.extend_from_slice(&path);
            out.extend(std::iter::repeat_n(b' ', width.saturating_sub(path.len())));
            out.extend_from_slice(b" | ");
            if let Some((before, after)) = file.binary {
                writeln!(&mut out, "Bin {before} -> {after} bytes")?;
                continue;
            }
            let total = file.added + file.removed;
            write!(&mut out, "{total:>digits$}")?;
            if total != 0 {
                out.push(b' ');
                let scale = |count: usize| {
                    if largest <= graph_width {
                        count
                    } else {
                        (count * graph_width / largest).max(usize::from(count != 0))
                    }
                };
                out.extend(std::iter::repeat_n(b'+', scale(file.added)));
                out.extend(std::iter::repeat_n(b'-', scale(file.removed)));
            }
            out.push(b'\n');
        }
    }
    let added: usize = files.iter().map(|file| file.added).sum();
    let removed: usize = files.iter().map(|file| file.removed).sum();
    write!(
        &mut out,
        " {} file{} changed",
        files.len(),
        if files.len() == 1 { "" } else { "s" }
    )?;
    if added != 0 || removed == 0 {
        write!(
            &mut out,
            ", {added} insertion{}(+)",
            if added == 1 { "" } else { "s" }
        )?;
    }
    if removed != 0 || added == 0 {
        write!(
            &mut out,
            ", {removed} deletion{}(-)",
            if removed == 1 { "" } else { "s" }
        )?;
    }
    out.push(b'\n');
    Ok(out)
}

fn format_date(time: gix::date::Time, date: &str) -> Result<String> {
    use gix::date::time::format;
    Ok(match date {
        "default" => time.format(format::DEFAULT)?,
        "iso" | "iso8601" => time.format(format::ISO8601)?,
        "iso-strict" | "iso8601-strict" => time.format(format::ISO8601_STRICT)?,
        "rfc" | "rfc2822" => time.format(format::GIT_RFC2822)?,
        "short" => time.format(format::SHORT)?,
        "unix" => time.format(format::UNIX)?,
        "raw" => time.format(format::RAW)?,
        _ => bail!("unsupported commit date format"),
    })
}
