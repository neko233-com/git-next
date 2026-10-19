use anyhow::{Context, Result, bail, ensure};
use gix::bstr::ByteSlice;
use gix_transport::client::blocking_io::Transport;
use std::{
    fs,
    path::{Path, PathBuf},
    sync::atomic::AtomicBool,
};

fn parsed(url: &str) -> Result<gix::Url> {
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
    let options = gix::open::Options::default();
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
    if parsed.scheme == gix::url::Scheme::Ssh {
        prepare = prepare.configure_connection(move |connection| {
            let transport = crate::ssh::transport(&parsed, gix_transport::Protocol::V2)
                .map_err(|e| gix::Error::from_error(std::io::Error::other(e.to_string())))?;
            *connection.transport_mut() = transport;
            Ok(())
        });
    }
    let interrupt = AtomicBool::new(false);
    if bare {
        prepare.fetch_only(gix::progress::Discard, &interrupt)?;
    } else {
        let (mut checkout, _) = prepare.fetch_then_checkout(gix::progress::Discard, &interrupt)?;
        checkout.main_worktree(gix::progress::Discard, &interrupt)?;
    }
    println!("Cloned into {}", destination.display());
    Ok(())
}

fn connection<'a, 'repo>(
    remote: &'a gix::Remote<'repo>,
) -> Result<gix::remote::Connection<'a, 'static, 'repo, Box<dyn Transport + Send>>> {
    let (url, version) = remote.sanitized_url_and_version(gix::remote::Direction::Fetch)?;
    if url.scheme == gix::url::Scheme::Ssh {
        Ok(remote.to_connection_with_transport(crate::ssh::transport(&url, version)?))
    } else if url.scheme == gix::url::Scheme::File {
        bail!("internal error: local remotes must use the native local object transfer")
    } else {
        Ok(remote.connect(gix::remote::Direction::Fetch)?)
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
    let outcome = connection(&remote)?
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
        "Fetched {} advertised refs",
        outcome.ref_map.remote_refs.len()
    );
    Ok(())
}

pub fn pull(name: &str) -> Result<()> {
    let repo = crate::repository::open()?;
    ensure!(repo.workdir().is_some(), "pull requires a working tree");
    let head = repo
        .head_name()?
        .context("detached HEAD; choose a branch before pulling")?;
    let branch = head.shorten().to_string();
    let old = repo.head_id()?.detach();
    fetch(name)?;
    let repo = crate::repository::open()?;
    let next = repo
        .rev_parse_single(format!("refs/remotes/{name}/{branch}").as_bytes().as_bstr())?
        .detach();
    if old == next {
        println!("Already up to date.");
        return Ok(());
    }
    let is_ancestor = repo
        .find_object(next)?
        .id()
        .ancestors()
        .all()?
        .any(|entry| entry.is_ok_and(|e| e.id == old));
    ensure!(
        is_ancestor,
        "branches diverged; git-next currently supports fast-forward pulls only"
    );
    crate::repository::checkout_clean(&repo, next)?;
    repo.reference(
        head,
        next,
        gix::refs::transaction::PreviousValue::MustExistAndMatch(old.into()),
        "pull: fast-forward by git-next",
    )?;
    println!("Updated {old}..{next}");
    Ok(())
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
    let repo = gix::init_bare(tmp.path())?;
    let remote = repo
        .remote_at(url)?
        .with_refspecs(["+refs/*:refs/*"], gix::remote::Direction::Fetch)?;
    let (map, _) = connection(&remote)?.ref_map(gix::progress::Discard, Default::default())?;
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
        crate::repository::checkout_initial(&repo).context("checkout local clone")?;
    }
    drop(repo);
    drop(source);
    ensure!(
        !destination.exists(),
        "clone destination appeared during transfer"
    );
    fs::rename(&path, destination)?;
    println!("Cloned into {}", destination.display());
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
