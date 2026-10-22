use anyhow::{Context, Result, bail, ensure};
use gix::bstr::ByteSlice;
use gix_transport::client::blocking_io::Transport;
use std::{
    fs,
    path::{Path, PathBuf},
    sync::atomic::AtomicBool,
};

pub(crate) fn parsed(url: &str) -> Result<gix::Url> {
    let parsed = if Path::new(url).is_absolute() && !url.contains("://") {
        let normalized = url.replace('\\', "/");
        let uri = if cfg!(windows) {
            format!("file:///{normalized}")
        } else {
            format!("file://{normalized}")
        };
        gix::url::parse(uri.as_bytes().as_bstr())?
    } else {
        gix::url::parse(url.as_bytes().as_bstr()).context("parse repository URL")?
    };
    ensure!(
        parsed.password().is_none(),
        "URL passwords are not accepted; use an SSH key, agent or a credential helper"
    );
    Ok(parsed)
}

pub fn clone_repo(
    url: &str,
    directory: Option<PathBuf>,
    bare: bool,
    branch: Option<String>,
    depth: Option<u32>,
) -> Result<()> {
    let context = crate::runtime::options();
    ensure!(
        context.git_dir.is_none() && context.work_tree.is_none() && !context.bare,
        "global repository-location overrides are not supported for clone; use clone's own destination options"
    );
    let parsed = parsed(url)?;
    let name = parsed
        .path
        .to_str()?
        .trim_end_matches('/')
        .rsplit('/')
        .next()
        .context("repository has no name")?
        .trim_end_matches(".git");
    ensure!(
        !name.is_empty() && name != "." && name != "..",
        "invalid clone destination"
    );
    let destination = directory.unwrap_or_else(|| {
        PathBuf::from(if bare {
            format!("{name}.git")
        } else {
            name.to_owned()
        })
    });
    ensure!(
        !destination.exists(),
        "clone destination already exists; existing files are never removed"
    );
    if parsed.scheme == gix::url::Scheme::File {
        return local_clone(&parsed, &destination, bare, branch.as_deref(), depth)
            .context("native local clone");
    }
    let options = crate::runtime::open_options();
    let kind = if bare {
        gix::create::Kind::Bare
    } else {
        gix::create::Kind::WithWorktree
    };
    let mut prepare = gix::clone::PrepareFetch::new(
        parsed.clone(),
        &destination,
        kind,
        Default::default(),
        options,
    )?
    .with_in_memory_config_overrides([
        "ssh.variant=ssh",
        "checkout.workers=8",
        "pack.threads=8",
    ]);
    if let Some(branch) = &branch {
        prepare = prepare.with_ref_name(Some(branch.as_str()))?;
    }
    if let Some(depth) = depth {
        prepare = prepare.with_shallow(gix::remote::fetch::Shallow::DepthAtRemote(
            depth.try_into().context("depth must be positive")?,
        ));
    }
    let credentials_path = destination.clone();
    prepare = prepare.configure_connection(move |connection| {
        connection.set_credentials(crate::credentials::read_existing(&gix::open_opts(
            &credentials_path,
            crate::runtime::open_options(),
        )?));
        if parsed.scheme == gix::url::Scheme::Ssh {
            let transport = crate::ssh::transport(&parsed, gix_transport::Protocol::V2)
                .map_err(|e| gix::Error::from_error(std::io::Error::other(e.to_string())))?;
            *connection.transport_mut() = transport;
        }
        Ok(())
    });
    let interrupt = AtomicBool::new(false);
    let started = std::time::Instant::now();
    let (repo, _) = prepare.fetch_only(gix::progress::Discard, &interrupt)?;
    let fetched = started.elapsed();
    if !bare {
        let checkout_started = std::time::Instant::now();
        // The fetch already returned an open repository. Reopening it would re-read
        // the configuration and re-scan the pack directory for no benefit, which is
        // pure cost on a large clone.
        crate::eol::configure_new(&repo)?;
        crate::repository::checkout_initial(&crate::eol::defaults(repo)?)?;
        if trace() {
            // The split says whether a slow clone is the network or the disk, which
            // is the first question any clone performance report has to answer.
            eprintln!(
                "trace: transfer+index {} ms, checkout {} ms",
                fetched.as_millis(),
                checkout_started.elapsed().as_millis()
            );
        }
    } else if trace() {
        eprintln!("trace: transfer+index {} ms", fetched.as_millis());
    }
    println!(
        "{}",
        crate::language::format(
            "Cloned into {path}",
            &[("path", destination.display().to_string())]
        )
    );
    Ok(())
}

fn connection<'a, 'repo>(
    remote: &'a gix::Remote<'repo>,
    repo: &gix::Repository,
) -> Result<gix::remote::Connection<'a, 'static, 'repo, Box<dyn Transport + Send>>> {
    let (url, version) = remote.sanitized_url_and_version(gix::remote::Direction::Fetch)?;
    if url.scheme == gix::url::Scheme::Ssh {
        Ok(remote
            .to_connection_with_transport(crate::ssh::transport(&url, version)?)
            .with_credentials(crate::credentials::read_existing(repo)))
    } else if url.scheme == gix::url::Scheme::File {
        bail!("internal error: local remotes must use the native local object transfer")
    } else {
        Ok(remote
            .connect(gix::remote::Direction::Fetch)?
            .with_credentials(crate::credentials::read_existing(repo)))
    }
}

pub fn fetch(name: &str) -> Result<()> {
    let repo = crate::repository::open()?;
    let remote = repo.find_remote(name)?;
    let url = remote
        .url(gix::remote::Direction::Fetch)
        .context("remote has no URL")?;
    if url.scheme == gix::url::Scheme::File {
        return local_fetch(&repo, name, url);
    }
    let outcome = connection(&remote, &repo)?
        .prepare_fetch(gix::progress::Discard, Default::default())?
        .receive(gix::progress::Discard, &AtomicBool::new(false))?;
    let mut fetched = String::new();
    for reference in &outcome.ref_map.remote_refs {
        let (reference, id, _) = reference.unpack();
        if let Some(id) = id {
            fetched.push_str(&format!("{id}\tnot-for-merge\t{reference}\n"));
        }
    }
    crate::repository::atomic_write(&repo.git_dir().join("FETCH_HEAD"), fetched.as_bytes())?;
    eprintln!(
        "{}",
        crate::language::format(
            "Fetched {count} advertised refs",
            &[("count", outcome.ref_map.remote_refs.len().to_string())]
        )
    );
    Ok(())
}

pub fn pull(name: Option<&str>) -> Result<()> {
    let repo = crate::repository::open()?;
    ensure!(repo.workdir().is_some(), "pull requires a working tree");
    let head = repo
        .head_name()?
        .context("detached HEAD; choose a branch before pulling")?;
    crate::write::safe_state(&repo)?;
    let branch = head.shorten().to_string();
    let config = repo.config_snapshot();
    let configured_remote = config.string(&format!("branch.{branch}.remote"));
    let name = name
        .or_else(|| {
            configured_remote
                .as_ref()
                .and_then(|value| value.to_str().ok())
        })
        .context("no upstream remote configured; specify a remote")?;
    let merge = config.string(&format!("branch.{branch}.merge"));
    let branch = merge
        .as_ref()
        .map(|value| value.to_str())
        .transpose()?
        .and_then(|value| value.strip_prefix("refs/heads/"))
        .unwrap_or(&branch);
    let revision = format!("refs/remotes/{name}/{branch}");
    fetch(name)?;
    crate::worktree::merge_ff(&revision)
}

pub fn ls_remote(url: &str) -> Result<()> {
    let url = parsed(url)?;
    if url.scheme == gix::url::Scheme::File {
        let repo = gix::open(gix::path::from_bstr(url.path.as_bstr())?)?;
        if let Ok(id) = repo.head_id() {
            println!("{id}\tHEAD")
        }
        for reference in repo.references()?.all()? {
            let mut reference = reference?;
            let name = reference.name().as_bstr().to_owned();
            let id = reference.follow_to_object()?.detach();
            println!("{id}\t{name}");
            let peeled = reference.peel_to_id()?.detach();
            if peeled != id {
                println!("{peeled}\t{name}^{{}}")
            }
        }
        return Ok(());
    }
    let tmp = temp_repository()?;
    gix::init_bare(tmp.path())?;
    let repo = gix::open_opts(tmp.path(), crate::runtime::open_options())?;
    let remote = repo
        .remote_at(url)?
        .with_refspecs(["+refs/*:refs/*"], gix::remote::Direction::Fetch)?;
    let (map, _) = connection(&remote, &repo)?.ref_map(
        gix::progress::Discard,
        gix::remote::ref_map::Options {
            prefix_from_spec_as_filter_on_remote: false, // ls-remote includes HEAD and every advertised namespace.
            ..Default::default()
        },
    )?;
    for reference in map.remote_refs {
        let (name, id, peeled) = reference.unpack();
        if let Some(id) = id {
            println!("{id}\t{name}")
        }
        if let Some(id) = peeled {
            println!("{id}\t{name}^{{}}")
        }
    }
    Ok(())
}

fn temp_repository() -> Result<tempfile::TempDir> {
    Ok(tempfile::tempdir()?)
}

fn local_clone(
    url: &gix::Url,
    destination: &Path,
    bare: bool,
    branch: Option<&str>,
    depth: Option<u32>,
) -> Result<()> {
    ensure!(
        depth.is_none(),
        "local shallow clones are not implemented; use HTTPS or SSH --depth"
    );
    let source =
        gix::open(gix::path::from_bstr(url.path.as_bstr())?).context("open local source")?;
    ensure!(
        branch.is_some() || source.head_name()?.is_some() || source.head_id().is_err(),
        "detached local sources require an explicit --branch"
    );
    ensure!(
        source.object_hash() == gix::hash::Kind::Sha1,
        "local SHA-256 clone is not yet validated"
    );
    let parent = destination
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    fs::create_dir_all(parent)?;
    let temp = tempfile::Builder::new()
        .prefix(".git-next-clone-")
        .tempdir_in(parent)?;
    let path = temp.path().join("repo");
    let repo = if bare {
        gix::init_bare(&path)?
    } else {
        gix::init(&path)?
    };
    copy_objects(&source, &repo).context("copy local object database")?;
    let branch = branch
        .map(str::to_owned)
        .or_else(|| {
            source
                .head_name()
                .ok()
                .flatten()
                .map(|name| name.shorten().to_string())
        })
        .unwrap_or_else(|| "main".into());
    let full = format!("refs/heads/{branch}");
    let _: gix::refs::FullName = full.clone().try_into()?;
    if let Some(id) = source.head()?.try_peel_to_id()? {
        ensure!(
            source.find_reference(&full).is_ok() || source.head_name()?.is_none(),
            "requested branch does not exist"
        );
        let _ = id;
    }
    for reference in source.references()?.all()? {
        let mut reference = reference?;
        let name = reference.name().as_bstr().to_str()?.to_owned();
        let id = reference.follow_to_object()?.detach();
        let target = if bare {
            name.clone()
        } else if let Some(name) = name.strip_prefix("refs/heads/") {
            format!("refs/remotes/origin/{name}")
        } else if name.starts_with("refs/tags/") {
            name.clone()
        } else {
            continue;
        };
        repo.reference(
            target,
            id,
            gix::refs::transaction::PreviousValue::MustNotExist,
            "clone: git-next",
        )?;
    }
    if !bare && let Ok(id) = source.rev_parse_single(full.as_bytes().as_bstr()) {
        repo.reference(
            full.clone(),
            id.detach(),
            gix::refs::transaction::PreviousValue::MustNotExist,
            "clone: git-next",
        )?;
    }
    crate::repository::atomic_write(
        &repo.git_dir().join("HEAD"),
        format!("ref: {full}\n").as_bytes(),
    )?;
    crate::repository::config_set(&repo, "remote.origin.url", url.to_bstring().to_str()?)?;
    crate::repository::config_set(
        &repo,
        "remote.origin.fetch",
        if bare {
            "+refs/heads/*:refs/heads/*"
        } else {
            "+refs/heads/*:refs/remotes/origin/*"
        },
    )?;
    if !bare {
        crate::repository::config_set(&repo, &format!("branch.{branch}.remote"), "origin")?;
        crate::repository::config_set(&repo, &format!("branch.{branch}.merge"), &full)?;
        crate::eol::configure_new(&repo)?;
        let checkout = gix::open_opts(&path, crate::runtime::open_options())?;
        crate::repository::checkout_initial(&crate::eol::defaults(checkout)?)
            .context("checkout local clone")?;
    }
    drop(repo);
    drop(source);
    ensure!(
        !destination.exists(),
        "clone destination appeared during transfer"
    );
    fs::rename(&path, destination)?;
    println!(
        "{}",
        crate::language::format(
            "Cloned into {path}",
            &[("path", destination.display().to_string())]
        )
    );
    Ok(())
}

fn local_fetch(repo: &gix::Repository, name: &str, url: &gix::Url) -> Result<()> {
    ensure!(
        !repo.is_bare(),
        "local fetch into a bare repository is not yet implemented"
    );
    let remote = repo.find_remote(name)?;
    let specs = remote.refspecs(gix::remote::Direction::Fetch);
    let expected = format!("+refs/heads/*:refs/remotes/{name}/*");
    ensure!(
        specs.len() == 1 && specs[0].to_ref().to_bstring() == expected.as_bytes(),
        "local fetch currently requires the default branch refspec"
    );
    let source = gix::open(gix::path::from_bstr(url.path.as_bstr())?)?;
    ensure!(
        repo.object_hash() == source.object_hash(),
        "object hash mismatch"
    );
    copy_objects(&source, repo)?;
    let mut fetched = String::new();
    for reference in source.references()?.local_branches()? {
        let mut reference = reference?;
        let branch = reference.name().shorten().to_str()?.to_owned();
        let id = reference.follow_to_object()?.detach();
        repo.reference(
            format!("refs/remotes/{name}/{branch}"),
            id,
            gix::refs::transaction::PreviousValue::Any,
            "fetch: git-next",
        )?;
        fetched.push_str(&format!("{id}\t\tbranch '{branch}' of local remote\n"));
    }
    crate::repository::atomic_write(&repo.git_dir().join("FETCH_HEAD"), fetched.as_bytes())?;
    Ok(())
}

fn copy_objects(source: &gix::Repository, target: &gix::Repository) -> Result<()> {
    let source = source.common_dir().join("objects");
    let target = target.common_dir().join("objects");
    for marker in ["info/alternates", "info/http-alternates"] {
        let path = source.join(marker);
        ensure!(
            !path.exists() || fs::read(path)?.is_empty(),
            "local alternates are not supported yet"
        );
    }
    for entry in walkdir::WalkDir::new(&source)
        .min_depth(1)
        .follow_links(false)
    {
        let entry = entry?;
        ensure!(
            !entry.file_type().is_symlink(),
            "symlinks in object storage are refused"
        );
        if !entry.file_type().is_file() {
            continue;
        }
        let relative = entry.path().strip_prefix(&source)?;
        let normalized = relative.to_string_lossy().replace('\\', "/");
        let loose = normalized.split_once('/').is_some_and(|(a, b)| {
            a.len() == 2
                && (b.len() == 38 || b.len() == 62)
                && a.bytes().chain(b.bytes()).all(|c| c.is_ascii_hexdigit())
        });
        let packed = normalized.starts_with("pack/pack-")
            && ["pack", "idx", "rev"]
                .iter()
                .any(|ext| entry.path().extension().is_some_and(|s| s == *ext));
        ensure!(
            entry.path().extension().is_none_or(|s| s != "promisor"),
            "partial local repositories are not supported"
        );
        if !loose && !packed {
            continue;
        }
        let destination = target.join(relative);
        if destination.exists() {
            continue;
        }
        fs::create_dir_all(destination.parent().context("invalid object path")?)?;
        let mut file = tempfile::NamedTempFile::new_in(
            destination.parent().context("object directory missing")?,
        )?;
        std::io::copy(&mut fs::File::open(entry.path())?, &mut file)?;
        file.as_file().sync_all()?;
        match file.persist_noclobber(destination) {
            Ok(_) => {}
            Err(e) if e.error.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e.error.into()),
        }
    }
    Ok(())
}

/// Whether to print the phase timings of a clone.
///
/// This is a diagnostic for performance work, not a user-facing option: it stays
/// behind an environment variable so normal output is unchanged.
fn trace() -> bool {
    std::env::var_os("GIT_NEXT_TRACE_TIMING").is_some()
}
