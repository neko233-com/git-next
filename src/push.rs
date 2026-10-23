use anyhow::{Context, Result, bail, ensure};
use clap::Args;
use flate2::{Compression, write::ZlibEncoder};
use gix::prelude::Write as _;
use gix::{
    ObjectId,
    bstr::ByteSlice,
    refs::transaction::{PreviousValue, RefEdit},
};
use gix_transport::{
    Protocol, Service,
    client::{
        MessageKind, WriteMode,
        blocking_io::{ReadlineBufRead, Transport},
    },
};
use std::{
    collections::{BTreeMap, BTreeSet, HashSet},
    fs,
    io::{self, Seek, Write},
};

#[derive(Args, Default)]
pub(crate) struct Options {
    pub remote: Option<String>,
    pub refspecs: Vec<String>,
    #[arg(short = 'u', long)]
    pub set_upstream: bool,
    #[arg(short = 'n', long)]
    pub dry_run: bool,
    #[arg(long)]
    pub delete: bool,
    #[arg(long, conflicts_with = "tags")]
    pub all: bool,
    #[arg(long)]
    pub tags: bool,
    #[arg(short = 'f', long)]
    pub force: bool,
    #[arg(long, num_args = 0..=1, require_equals = true, default_missing_value = "")]
    pub force_with_lease: Option<String>,
    #[arg(long)]
    pub atomic: bool,
    #[arg(long)]
    pub porcelain: bool,
}

#[derive(Clone, Debug)]
struct Update {
    name: String,
    source: String,
    old: ObjectId,
    new: ObjectId,
    forced: bool,
    tracking: Option<(String, Option<gix::refs::Target>)>,
}

fn ref_match(pattern: &str, name: &str) -> Option<String> {
    if let Some((prefix, suffix)) = pattern.split_once('*') {
        name.strip_prefix(prefix)?
            .strip_suffix(suffix)
            .map(str::to_owned)
    } else {
        (pattern == name).then(String::new)
    }
}

fn tracking_ref(
    repo: &gix::Repository,
    remote: &str,
    reference: &str,
) -> Result<Option<(String, Option<gix::refs::Target>)>> {
    let Ok(remote) = repo.find_remote(remote) else {
        return Ok(None);
    };
    let mut destination = None;
    for specification in remote.refspecs(gix::remote::Direction::Fetch) {
        let bytes = specification.to_ref().to_bstring();
        let specification = bytes.to_str()?;
        if let Some(excluded) = specification.strip_prefix('^') {
            if ref_match(excluded, reference).is_some() {
                return Ok(None);
            }
            continue;
        }
        let specification = specification.strip_prefix('+').unwrap_or(specification);
        if let Some((source, target)) = specification.split_once(':')
            && let Some(matched) = ref_match(source, reference)
        {
            let mapped = target.replacen('*', &matched, 1);
            let _: gix::refs::FullName = mapped.clone().try_into()?;
            ensure!(
                destination
                    .as_ref()
                    .is_none_or(|existing| existing == &mapped),
                "multiple fetch refspecs map a pushed ref to different local refs"
            );
            destination = Some(mapped);
        }
    }
    destination
        .map(|name| {
            let previous = repo
                .try_find_reference(&name)?
                .map(|reference| reference.target().into());
            Ok((name, previous))
        })
        .transpose()
}

fn selected_remote(repo: &gix::Repository, explicit: Option<&str>) -> Result<String> {
    if let Some(explicit) = explicit {
        return Ok(explicit.to_owned());
    }
    let branch = repo.head_name()?.map(|name| name.shorten().to_string());
    let config = repo.config_snapshot();
    for key in [
        branch
            .as_ref()
            .map(|name| format!("branch.{name}.pushRemote")),
        Some("remote.pushDefault".into()),
        branch.map(|name| format!("branch.{name}.remote")),
    ]
    .into_iter()
    .flatten()
    {
        if let Some(value) = config.string(&key) {
            return Ok(value.to_str()?.to_owned());
        }
    }
    ensure!(
        repo.find_remote("origin").is_ok(),
        "no push destination configured; specify a remote"
    );
    Ok("origin".into())
}

fn refs(repo: &gix::Repository) -> Result<BTreeMap<String, ObjectId>> {
    let mut result = BTreeMap::new();
    for reference in repo.references()?.all()? {
        let mut reference = reference?;
        let name = reference.name().as_bstr().to_str()?.to_owned();
        result.insert(name, reference.follow_to_object()?.detach());
    }
    Ok(result)
}

fn destination(name: &str, source: Option<&str>) -> Result<String> {
    let name = if name.starts_with("refs/") {
        name.to_owned()
    } else if source.is_some_and(|name| name.starts_with("refs/tags/")) {
        format!("refs/tags/{name}")
    } else {
        format!("refs/heads/{name}")
    };
    let _: gix::refs::FullName = name.clone().try_into()?;
    ensure!(
        name.starts_with("refs/heads/") || name.starts_with("refs/tags/"),
        "push currently supports branch and tag destinations"
    );
    Ok(name)
}

fn source(repo: &gix::Repository, name: &str) -> Result<(String, ObjectId)> {
    if name == "HEAD" {
        return Ok((
            repo.head_name()?
                .map(|value| value.as_bstr().to_string())
                .unwrap_or_else(|| "HEAD".into()),
            repo.head_id()?.detach(),
        ));
    }
    let branch = format!("refs/heads/{name}");
    let tag = format!("refs/tags/{name}");
    let selected = if name.starts_with("refs/") {
        name
    } else if repo.find_reference(&branch).is_ok() {
        ensure!(
            repo.find_reference(&tag).is_err(),
            "ambiguous source name; specify refs/heads/ or refs/tags/"
        );
        &branch
    } else if repo.find_reference(&tag).is_ok() {
        &tag
    } else {
        name
    };
    Ok((
        selected.to_owned(),
        repo.rev_parse_single(selected.as_bytes().as_bstr())?
            .detach(),
    ))
}

fn plan(
    repo: &gix::Repository,
    remote: &str,
    advertised: &BTreeMap<String, ObjectId>,
    options: &Options,
) -> Result<Vec<Update>> {
    ensure!(
        !(options.force && options.force_with_lease.is_some()),
        "choose force or force-with-lease"
    );
    ensure!(
        !(options.delete && (options.all || options.tags)),
        "--delete conflicts with --all/--tags"
    );
    ensure!(
        !((options.all || options.tags) && !options.refspecs.is_empty()),
        "--all/--tags do not accept explicit refspecs"
    );
    let config = repo.config_snapshot();
    let mut specifications = options.refspecs.clone();
    if options.all || options.tags {
        let prefix = if options.all {
            "refs/heads/"
        } else {
            "refs/tags/"
        };
        specifications = refs(repo)?
            .into_keys()
            .filter(|name| name.starts_with(prefix))
            .collect();
    } else if specifications.is_empty() {
        ensure!(!options.delete, "--delete needs a ref name");
        if let Some(values) = config.strings(&format!("remote.{remote}.push")) {
            specifications = values
                .iter()
                .map(|value| value.to_str().map(str::to_owned))
                .collect::<std::result::Result<_, _>>()?;
        } else {
            let mode = config
                .string("push.default")
                .map(|value| value.to_string())
                .unwrap_or_else(|| "simple".into());
            let current = repo
                .head_name()?
                .context("detached HEAD needs an explicit push refspec")?;
            let current = current.shorten().to_string();
            match mode.as_str() {
                "current" => specifications.push(format!("refs/heads/{current}")),
                "simple" | "upstream" => {
                    let upstream_remote = config
                        .string(&format!("branch.{current}.remote"))
                        .context("current branch has no upstream; use push -u REMOTE BRANCH")?;
                    let upstream = config
                        .string(&format!("branch.{current}.merge"))
                        .context("current branch has no upstream merge ref")?;
                    ensure!(
                        upstream_remote == remote.as_bytes(),
                        "push destination differs from the upstream remote; specify a refspec"
                    );
                    if mode == "simple" {
                        ensure!(
                            upstream == format!("refs/heads/{current}").as_bytes(),
                            "upstream branch has a different name; specify a refspec"
                        );
                    }
                    specifications.push(format!("refs/heads/{current}:{upstream}"));
                }
                "matching" => {
                    specifications = refs(repo)?
                        .into_keys()
                        .filter(|name| {
                            name.starts_with("refs/heads/") && advertised.contains_key(name)
                        })
                        .collect()
                }
                "nothing" => bail!("push.default=nothing requires an explicit refspec"),
                _ => bail!("unsupported push.default value"),
            }
        }
    }
    let null = ObjectId::null(repo.object_hash());
    let mut updates = BTreeMap::new();
    for specification in specifications {
        let (force, specification) = if let Some(specification) = specification.strip_prefix('+') {
            (true, specification)
        } else {
            (options.force, specification.as_str())
        };
        ensure!(
            !specification.contains('*'),
            "wildcard push refspecs are not supported yet"
        );
        let (from, to) = if options.delete {
            ("", specification)
        } else {
            specification
                .split_once(':')
                .unwrap_or((specification, specification))
        };
        ensure!(!to.is_empty(), "push destination is empty");
        let (source_name, new) = if from.is_empty() {
            (String::new(), null)
        } else {
            source(repo, from)?
        };
        let name = if to == "HEAD" && !from.is_empty() {
            destination(&source_name, Some(&source_name))?
        } else {
            destination(to, Some(&source_name))?
        };
        let old = advertised.get(&name).copied().unwrap_or(null);
        ensure!(
            !new.is_null() || !old.is_null(),
            "cannot delete a ref that does not exist"
        );
        let leased = if let Some(lease) = &options.force_with_lease {
            let expected = if lease.is_empty() {
                let branch = name
                    .strip_prefix("refs/heads/")
                    .context("tag force-with-lease requires an explicit expected object")?;
                repo.find_reference(&format!("refs/remotes/{remote}/{branch}"))?
                    .follow_to_object()?
                    .detach()
            } else {
                let (lease_ref, expected) = lease
                    .split_once(':')
                    .context("use --force-with-lease=REF:EXPECTED or an implicit tracking lease")?;
                ensure!(
                    destination(lease_ref, Some(&source_name))? == name,
                    "lease does not cover this destination"
                );
                if expected.is_empty() {
                    null
                } else {
                    if let Ok(id) = ObjectId::from_hex(expected.as_bytes()) {
                        ensure!(
                            id.kind() == repo.object_hash(),
                            "lease object format mismatch"
                        );
                        id
                    } else {
                        repo.rev_parse_single(expected.as_bytes().as_bstr())?
                            .detach()
                    }
                }
            };
            ensure!(
                old == expected,
                "stale force-with-lease; remote ref differs from the expected value"
            );
            true
        } else {
            false
        };
        if !new.is_null() && name.starts_with("refs/heads/") {
            repo.find_commit(new)
                .context("branch destination requires a commit object")?;
        }
        ensure!(
            !options.set_upstream
                || (!new.is_null()
                    && name.starts_with("refs/heads/")
                    && source_name.starts_with("refs/heads/")),
            "--set-upstream requires a local branch pushed to a branch"
        );
        if old != new && !old.is_null() && !new.is_null() && !force && !leased {
            ensure!(
                !name.starts_with("refs/tags/"),
                "tag already exists; refusing replacement without explicit force"
            );
            ensure!(
                crate::worktree::is_ancestor(repo, old, new).unwrap_or(false),
                "non-fast-forward push rejected; fetch and integrate remote changes first"
            );
        }
        let tracking = tracking_ref(repo, remote, &name)?;
        ensure!(
            updates
                .insert(
                    name.clone(),
                    Update {
                        name,
                        source: source_name,
                        old,
                        new,
                        forced: force || leased,
                        tracking,
                    }
                )
                .is_none(),
            "duplicate push destination"
        );
    }
    Ok(updates.into_values().collect())
}

pub(crate) fn run(options: Options) -> Result<()> {
    let repo = crate::repository::open()?;
    let remote_name = selected_remote(&repo, options.remote.as_deref())?;
    let named_remote = if repo
        .remote_names()
        .iter()
        .any(|name| name.as_slice() == remote_name.as_bytes())
    {
        Some(repo.find_remote(remote_name.as_str())?)
    } else {
        None
    };
    if named_remote.is_some() {
        let config = repo.config_snapshot();
        for key in [
            format!("remote.{remote_name}.url"),
            format!("remote.{remote_name}.pushurl"),
        ] {
            ensure!(
                config.strings(&key).is_none_or(|values| values.len() <= 1),
                "multiple remote push URLs are not supported yet"
            );
        }
    }
    let url = match &named_remote {
        Some(remote) => remote
            .url(gix::remote::Direction::Push)
            .context("remote has no push URL")?
            .clone(),
        None => crate::network::parsed(&remote_name)?,
    };
    ensure!(
        url.password().is_none(),
        "URL passwords are refused; use the configured credential helper"
    );
    ensure!(
        !options.set_upstream || named_remote.is_some(),
        "--set-upstream requires a configured remote"
    );
    crate::lfs::check_hooks(&repo, &["pre-push"], None)?;
    if url.scheme == gix::url::Scheme::File {
        let target = crate::network::open_local_file_repo(&url)?;
        ensure!(
            target.is_bare(),
            "local push currently requires a bare destination"
        );
        ensure!(
            target.object_hash() == repo.object_hash(),
            "push object-format mismatch"
        );
        for hook in [
            "pre-receive",
            "update",
            "post-receive",
            "post-update",
            "reference-transaction",
        ] {
            ensure!(
                !target.common_dir().join("hooks").join(hook).exists(),
                "local receive hooks are not supported; no hooks were skipped"
            );
        }
        ensure!(
            target.config_snapshot().string("core.hooksPath").is_none(),
            "local custom receive hooks are not supported"
        );
        let advertised = refs(&target)?;
        let updates = plan(&repo, &remote_name, &advertised, &options)?;
        let named = named_remote.is_some().then_some(remote_name.as_str());
        if !options.dry_run {
            crate::lfs::upload_before_push(
                &repo,
                &remote_name,
                updates.iter().map(|update| update.new).collect(),
                advertised.values().copied().collect(),
                None,
            )?;
        }
        if !options.dry_run && !options.atomic {
            let mut accepted = Vec::new();
            let mut errors = Vec::new();
            for update in &updates {
                match local_push(&repo, &target, std::slice::from_ref(update)) {
                    Ok(()) => accepted.push(update.clone()),
                    Err(error) => errors.push(format!("{}: {error:#}", update.name)),
                }
            }
            finish(&repo, named, &accepted, &options)?;
            ensure!(
                errors.is_empty(),
                "local receive rejected refs: {}",
                errors.join("; ")
            );
            return Ok(());
        } else if !options.dry_run {
            local_push(&repo, &target, &updates)?;
        }
        finish(&repo, named, &updates, &options)
    } else {
        network_push(
            &repo,
            &url,
            named_remote.is_some().then_some(remote_name.as_str()),
            &remote_name,
            &options,
        )
    }
}

pub(crate) fn reachable(
    repo: &gix::Repository,
    tips: impl IntoIterator<Item = ObjectId>,
    stop: &HashSet<ObjectId>,
    allow_missing: bool,
) -> Result<HashSet<ObjectId>> {
    let mut pending: Vec<_> = tips
        .into_iter()
        .filter(|id| !id.is_null())
        .map(|id| (id, None))
        .collect();
    let mut result = HashSet::new();
    while let Some((id, hint)) = pending.pop() {
        if stop.contains(&id) || result.contains(&id) {
            continue;
        }
        if hint == Some(gix::objs::Kind::Blob) {
            // Tree entries already identify blobs; reachability never needs their potentially large contents.
            result.insert(id);
            continue;
        }
        let object = if allow_missing {
            let Some(object) = repo.try_find_object(id)? else {
                continue;
            };
            object
        } else {
            repo.find_object(id)?
        };
        result.insert(id);
        match object.kind {
            gix::objs::Kind::Commit => {
                let commit = object.into_commit();
                pending.push((commit.tree_id()?.detach(), Some(gix::objs::Kind::Tree)));
                pending.extend(
                    commit
                        .parent_ids()
                        .map(|id| (id.detach(), Some(gix::objs::Kind::Commit))),
                );
            }
            gix::objs::Kind::Tree => {
                for entry in object.into_tree().iter() {
                    let entry = entry?;
                    if entry.mode().kind() != gix::objs::tree::EntryKind::Commit {
                        pending.push((
                            entry.oid().to_owned(),
                            Some(if entry.mode().kind() == gix::objs::tree::EntryKind::Tree {
                                gix::objs::Kind::Tree
                            } else {
                                gix::objs::Kind::Blob
                            }),
                        ));
                    }
                }
            }
            gix::objs::Kind::Tag => pending.push((object.into_tag().target_id()?.detach(), None)),
            gix::objs::Kind::Blob => {}
        }
    }
    Ok(result)
}

fn local_push(repo: &gix::Repository, target: &gix::Repository, updates: &[Update]) -> Result<()> {
    let config = target.config_snapshot();
    for update in updates.iter().filter(|update| update.old != update.new) {
        if update.new.is_null()
            && target
                .head_name()?
                .is_some_and(|name| name.as_bstr() == update.name.as_bytes().as_bstr())
        {
            match config
                .string("receive.denyDeleteCurrent")
                .as_ref()
                .map(|value| value.as_slice())
                .unwrap_or(b"refuse")
            {
                b"false" | b"ignore" => {}
                b"warn" => eprintln!("warning: deleting the remote current branch"),
                b"true" | b"refuse" => {
                    bail!("remote receive.denyDeleteCurrent rejects deletion of its current branch")
                }
                _ => bail!("invalid receive.denyDeleteCurrent configuration"),
            }
        }
        ensure!(
            !update.new.is_null() || !config.boolean("receive.denyDeletes").unwrap_or(false),
            "remote receive.denyDeletes rejects deletion"
        );
        if !update.old.is_null()
            && !update.new.is_null()
            && update.name.starts_with("refs/heads/")
            && config
                .boolean("receive.denyNonFastForwards")
                .unwrap_or(false)
        {
            ensure!(
                crate::worktree::is_ancestor(repo, update.old, update.new).unwrap_or(false),
                "remote receive.denyNonFastForwards rejects update"
            );
        }
    }
    let edits: Vec<_> = updates
        .iter()
        .filter(|update| update.old != update.new)
        .map(|update| {
            let expected = if update.old.is_null() {
                PreviousValue::MustNotExist
            } else {
                PreviousValue::MustExistAndMatch(update.old.into())
            };
            if update.new.is_null() {
                RefEdit::delete(
                    update.name.clone().try_into().expect("validated ref"),
                    expected,
                )
            } else {
                RefEdit::update(
                    update.name.clone().try_into().expect("validated ref"),
                    update.new,
                    expected,
                    "push: git-next",
                )
            }
        })
        .collect();
    if edits.is_empty() {
        return Ok(());
    }
    let transaction = target.refs.transaction().prepare(
        edits,
        gix::lock::acquire::Fail::Immediately,
        gix::lock::acquire::Fail::Immediately,
    )?;
    let stop = reachable(
        repo,
        updates.iter().map(|update| update.old),
        &HashSet::new(),
        true,
    )?;
    let objects = reachable(repo, updates.iter().map(|update| update.new), &stop, false)?;
    for id in objects {
        if target.try_find_header(id)?.is_some() {
            continue;
        }
        let object = repo.find_object(id)?;
        let actual = gix::objs::compute_hash(target.object_hash(), object.kind, &object.data)?;
        ensure!(actual == id, "source object hash verification failed");
        target
            .objects
            .write_buf_with_known_id(object.kind, &object.data, id)?;
    }
    transaction.commit(target.committer().transpose()?)?;
    Ok(())
}

struct DigestWriter<W> {
    inner: W,
    digest: gix::hash::Hasher,
}
impl<W: Write> Write for DigestWriter<W> {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        let written = self.inner.write(bytes)?;
        self.digest.update(&bytes[..written]);
        Ok(written)
    }
    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

fn pack(
    repo: &gix::Repository,
    updates: &[Update],
    advertised: &BTreeMap<String, ObjectId>,
) -> Result<fs::File> {
    let known = reachable(repo, advertised.values().copied(), &HashSet::new(), true)?;
    let objects = reachable(
        repo,
        updates
            .iter()
            .filter(|update| update.new != update.old)
            .map(|update| update.new),
        &known,
        false,
    )?;
    let count = u32::try_from(objects.len()).context("pack has too many objects")?;
    let mut writer = DigestWriter {
        inner: tempfile::tempfile()?,
        digest: gix::hash::hasher(repo.object_hash()),
    };
    writer.write_all(b"PACK")?;
    writer.write_all(&2u32.to_be_bytes())?;
    writer.write_all(&count.to_be_bytes())?;
    let config = repo.config_snapshot();
    let level = config
        .integer("pack.compression")
        .or_else(|| config.integer("core.compression"))
        .unwrap_or(6);
    ensure!((-1..=9).contains(&level), "invalid pack compression level");
    let compression = if level == -1 {
        Compression::default()
    } else {
        Compression::new(level as u32)
    };
    let mut ids: Vec<_> = objects.into_iter().collect();
    ids.sort();
    for id in ids {
        let object = repo.find_object(id)?;
        ensure!(
            gix::objs::compute_hash(repo.object_hash(), object.kind, &object.data)? == id,
            "source object hash verification failed"
        );
        let mut size = object.data.len() as u64;
        let kind = match object.kind {
            gix::objs::Kind::Commit => 1,
            gix::objs::Kind::Tree => 2,
            gix::objs::Kind::Blob => 3,
            gix::objs::Kind::Tag => 4,
        };
        let mut header = (kind << 4) | (size as u8 & 15);
        size >>= 4;
        if size != 0 {
            header |= 128;
        }
        writer.write_all(&[header])?;
        while size != 0 {
            let mut next = size as u8 & 127;
            size >>= 7;
            if size != 0 {
                next |= 128;
            }
            writer.write_all(&[next])?;
        }
        let mut compressor = ZlibEncoder::new(&mut writer, compression);
        compressor.write_all(&object.data)?;
        compressor.finish()?;
    }
    let checksum = writer.digest.try_finalize()?;
    writer.inner.write_all(checksum.as_slice())?;
    writer.inner.rewind()?;
    Ok(writer.inner)
}

fn network_push(
    repo: &gix::Repository,
    url: &gix::Url,
    remote_name: Option<&str>,
    selected: &str,
    options: &Options,
) -> Result<()> {
    ensure!(
        matches!(
            url.scheme,
            gix::url::Scheme::Ssh
                | gix::url::Scheme::Http
                | gix::url::Scheme::Https
                | gix::url::Scheme::Git
        ),
        "unsupported push transport"
    );
    let transport: Box<dyn Transport + Send> = if url.scheme == gix::url::Scheme::Ssh {
        crate::ssh::transport_for_service(url, Protocol::V0, Service::ReceivePack)?
    } else {
        gix_transport::client::blocking_io::connect::connect(
            url.clone(),
            gix_transport::client::blocking_io::connect::Options {
                version: Protocol::V0,
                ..Default::default()
            },
        )?
    };
    let remote = repo.remote_at(url.clone())?;
    let mut connection = remote.to_connection_with_transport(transport);
    let authenticate = crate::credentials::read_existing(repo);
    let url_bytes = url.to_bstring();
    if let Some(config) = repo.transport_options(
        url_bytes.as_bstr(),
        remote_name.map(|name| name.as_bytes().as_bstr()),
    )? {
        connection.transport_mut().configure(&*config)?;
    }
    let handshake = gix::protocol::handshake(
        connection.transport_mut(),
        Service::ReceivePack,
        authenticate,
        Vec::new(),
        &mut gix::progress::Discard,
    )?;
    ensure!(
        matches!(
            handshake.server_protocol_version,
            Protocol::V0 | Protocol::V1
        ),
        "receive-pack requires the v0/v1 protocol"
    );
    ensure!(
        handshake.capabilities.contains("report-status"),
        "server lacks required report-status capability"
    );
    if let Some(format) = handshake
        .capabilities
        .capability("object-format")
        .and_then(|capability| capability.value())
    {
        ensure!(
            format == repo.object_hash().to_string().as_bytes().as_bstr(),
            "server object format differs from the local repository"
        );
    } else {
        ensure!(
            repo.object_hash() == gix::hash::Kind::Sha1,
            "server did not advertise SHA-256 support"
        );
    }
    let mut advertised = BTreeMap::new();
    for reference in handshake.refs.context("server did not advertise refs")? {
        let (name, id, _) = reference.unpack();
        if let Some(id) = id
            && !id.is_null()
        {
            advertised.insert(name.to_str()?.to_owned(), id.to_owned());
        }
    }
    let updates = plan(repo, selected, &advertised, options)?;
    let changed: Vec<_> = updates
        .iter()
        .filter(|update| update.old != update.new)
        .collect();
    if options.dry_run || changed.is_empty() {
        return finish(repo, remote_name, &updates, options);
    }
    crate::lfs::upload_before_push(
        repo,
        selected,
        changed.iter().map(|update| update.new).collect(),
        advertised.values().copied().collect(),
        None,
    )?;
    ensure!(
        !changed.iter().any(|update| update.new.is_null())
            || handshake.capabilities.contains("delete-refs"),
        "server does not permit ref deletion"
    );
    ensure!(
        !options.atomic || handshake.capabilities.contains("atomic"),
        "server does not support atomic push"
    );
    let mut capabilities = vec!["report-status".to_owned()];
    if options.atomic {
        capabilities.push("atomic".into());
    }
    if handshake.capabilities.contains("agent") {
        capabilities.push(format!("agent=git-next/{}", env!("CARGO_PKG_VERSION")));
    }
    if handshake.capabilities.contains("object-format") {
        capabilities.push(format!("object-format={}", repo.object_hash()));
    }
    let mut pack = if changed.iter().any(|update| !update.new.is_null()) {
        Some(pack(repo, &updates, &advertised)?)
    } else {
        None
    };
    let mut request =
        connection
            .transport_mut()
            .request(WriteMode::Binary, MessageKind::Flush, false)?;
    for (position, update) in changed.iter().enumerate() {
        let mut command = format!("{} {} {}", update.old, update.new, update.name);
        if position == 0 {
            command.push('\0');
            command.push_str(&capabilities.join(" "));
        }
        command.push('\n');
        request.write_all(command.as_bytes())?;
    }
    request.write_message(MessageKind::Flush)?;
    let (mut writer, mut response) = request.into_parts();
    if let Some(pack) = pack.as_mut() {
        io::copy(pack, &mut writer)?;
    }
    writer.flush()?;
    drop(writer);
    let mut text = String::new();
    ensure!(
        response.readline_str(&mut text)? != 0 && text.trim_end() == "unpack ok",
        "server rejected the pack or omitted unpack status: {}",
        text.trim_end()
    );
    let mut accepted = BTreeSet::new();
    let mut reported = BTreeSet::new();
    let mut rejected = Vec::new();
    loop {
        text.clear();
        if response.readline_str(&mut text)? == 0 {
            break;
        }
        let line = text.trim_end();
        if let Some(name) = line.strip_prefix("ok ") {
            ensure!(
                changed.iter().any(|update| update.name == name)
                    && reported.insert(name.to_owned()),
                "invalid or duplicate push report ref"
            );
            accepted.insert(name.to_owned());
        } else if let Some(reason) = line.strip_prefix("ng ") {
            let (name, explanation) = reason
                .split_once(' ')
                .context("invalid rejected-ref status")?;
            ensure!(
                !explanation.is_empty()
                    && changed.iter().any(|update| update.name == name)
                    && reported.insert(name.to_owned()),
                "invalid or duplicate rejected-ref status"
            );
            rejected.push(reason.to_owned());
        } else {
            bail!("malformed push status response");
        }
    }
    ensure!(
        reported.len() == changed.len(),
        "server omitted status for one or more pushed refs"
    );
    if !rejected.is_empty() {
        if !options.atomic {
            let successful: Vec<_> = updates
                .iter()
                .filter(|update| update.old == update.new || accepted.contains(&update.name))
                .cloned()
                .collect();
            finish(repo, remote_name, &successful, options)?;
        }
        bail!("server rejected refs: {}", rejected.join("; "));
    }
    finish(repo, remote_name, &updates, options)
}

fn finish(
    repo: &gix::Repository,
    remote: Option<&str>,
    updates: &[Update],
    options: &Options,
) -> Result<()> {
    for update in updates {
        if !options.dry_run
            && let Some((tracking, previous)) = &update.tracking
        {
            let expected = previous
                .clone()
                .map(PreviousValue::MustExistAndMatch)
                .unwrap_or(PreviousValue::MustNotExist);
            if update.new.is_null() {
                if previous.is_some() {
                    repo.edit_reference(RefEdit::delete(tracking.as_str().try_into()?, expected))
                        .context(
                            "remote push succeeded; local tracking ref could not be updated",
                        )?;
                }
            } else {
                repo.reference(
                    tracking.as_str(),
                    update.new,
                    expected,
                    "update by push: git-next",
                )
                .context("remote push succeeded; local tracking ref could not be updated")?;
            }
        }
        if !options.dry_run
            && options.set_upstream
            && let Some(remote) = remote
        {
            let local = update
                .source
                .strip_prefix("refs/heads/")
                .context("upstream requires a local branch source")?;
            let mut config = repo.config_file_mut(repo.common_dir().join("config"))?;
            config.set_raw_value(&format!("branch.{local}.remote"), remote)?;
            config.set_raw_value(&format!("branch.{local}.merge"), &update.name)?;
            config.commit()?;
        }
        let status = if update.old == update.new {
            "up to date"
        } else if update.new.is_null() {
            "deleted"
        } else if update.old.is_null() {
            "new ref"
        } else if update.forced {
            "forced update"
        } else {
            "fast-forward"
        };
        if options.porcelain {
            println!(
                "{}\t{}:{}\t[{status}]",
                if update.old == update.new {
                    '='
                } else if update.new.is_null() {
                    '-'
                } else if update.old.is_null() {
                    '*'
                } else if update.forced {
                    '+'
                } else {
                    ' '
                },
                update.source,
                update.name
            );
        } else {
            println!(
                "{}{}: {status}",
                if options.dry_run { "dry-run " } else { "" },
                update.name
            );
        }
    }
    Ok(())
}
