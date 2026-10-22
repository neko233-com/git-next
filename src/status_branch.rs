use anyhow::Result;
use gix::{ObjectId, bstr::ByteSlice};
use std::{collections::HashSet, io::Write};

struct Information {
    name: Option<gix::refs::FullName>,
    id: Option<ObjectId>,
    upstream: Option<gix::refs::FullName>,
    upstream_id: Option<ObjectId>,
    counts: Option<(usize, usize)>,
}

fn information(repo: &gix::Repository, detailed: bool) -> Result<Information> {
    let name = repo.head_name()?;
    let id = repo.head()?.try_into_peeled_id()?.map(|id| id.detach());
    let upstream = name
        .as_ref()
        .and_then(|name| {
            if repo
                .branch_remote_name(name.shorten(), gix::remote::Direction::Fetch)
                .is_some_and(|remote| remote.as_bstr() == b".".as_bstr())
            {
                repo.branch_remote_ref_name(name.as_ref(), gix::remote::Direction::Fetch)
            } else {
                repo.branch_remote_tracking_ref_name(name.as_ref(), gix::remote::Direction::Fetch)
            }
        })
        .transpose()?;
    let upstream_id = if let Some(name) = &upstream {
        repo.try_find_reference(name.as_ref())?
            .map(|mut reference| reference.peel_to_id().map(|id| id.detach()))
            .transpose()?
    } else {
        None
    };
    let counts = id
        .zip(upstream_id)
        .map(|(local, remote)| {
            if local == remote {
                Ok((0, 0))
            } else if detailed {
                ahead_behind(repo, local, remote)
            } else {
                Ok((usize::MAX, usize::MAX))
            }
        })
        .transpose()?;
    Ok(Information {
        name,
        id,
        upstream,
        upstream_id,
        counts,
    })
}

pub fn long_tracking(repo: &gix::Repository, detailed: bool, hints: bool) -> Result<Vec<u8>> {
    let Information {
        id,
        upstream,
        upstream_id,
        counts,
        ..
    } = information(repo, detailed)?;
    let mut output = Vec::new();
    let Some(upstream) = upstream.filter(|_| id.is_some()) else {
        return Ok(output);
    };
    let name = upstream.shorten();
    if upstream_id.is_none() {
        writeln!(
            output,
            "{}",
            crate::language::format(
                "Your branch is based on '{name}', but the upstream is gone.",
                &[("name", name.to_string())]
            )
        )?;
        if hints {
            output.extend_from_slice(crate::language::bytes(
                b"  (use \"git branch --unset-upstream\" to fixup)\n",
            ));
        }
    } else if let Some((ahead, behind)) = counts {
        match (ahead, behind) {
            (0, 0) => writeln!(
                output,
                "{}",
                crate::language::format(
                    "Your branch is up to date with '{name}'.",
                    &[("name", name.to_string())]
                )
            )?,
            (usize::MAX, _) => {
                writeln!(
                    output,
                    "{}",
                    crate::language::format(
                        "Your branch and '{name}' refer to different commits.",
                        &[("name", name.to_string())]
                    )
                )?;
                if hints {
                    output.extend_from_slice(crate::language::bytes(
                        b"  (use \"git status --ahead-behind\" for details)\n",
                    ));
                }
            }
            (ahead, 0) => {
                writeln!(
                    output,
                    "{}",
                    crate::language::format(
                        "Your branch is ahead of '{name}' by {count} commit{s}.",
                        &[
                            ("name", name.to_string()),
                            ("count", ahead.to_string()),
                            ("s", if ahead == 1 { "" } else { "s" }.into())
                        ]
                    )
                )?;
                if hints {
                    output.extend_from_slice(crate::language::bytes(
                        b"  (use \"git push\" to publish your local commits)\n",
                    ));
                }
            }
            (0, behind) => {
                writeln!(
                    output,
                    "{}",
                    crate::language::format(
                        "Your branch is behind '{name}' by {count} commit{s}, and can be fast-forwarded.",
                        &[
                            ("name", name.to_string()),
                            ("count", behind.to_string()),
                            ("s", if behind == 1 { "" } else { "s" }.into())
                        ]
                    )
                )?;
                if hints {
                    output.extend_from_slice(crate::language::bytes(
                        b"  (use \"git pull\" to update your local branch)\n",
                    ));
                }
            }
            (ahead, behind) => {
                writeln!(
                    output,
                    "{}",
                    crate::language::format(
                        "Your branch and '{name}' have diverged,\nand have {ahead} and {behind} different commits each, respectively.",
                        &[
                            ("name", name.to_string()),
                            ("ahead", ahead.to_string()),
                            ("behind", behind.to_string())
                        ]
                    )
                )?;
                if hints {
                    output.extend_from_slice(crate::language::bytes(b"  (use \"git pull\" if you want to integrate the remote branch with yours)\n"));
                }
            }
        }
    }
    output.push(b'\n');
    Ok(output)
}

pub fn render(repo: &gix::Repository, v2: bool, nul: bool, detailed: bool) -> Result<Vec<u8>> {
    let mut output = Vec::new();
    let Information {
        name,
        id,
        upstream,
        upstream_id,
        counts,
    } = information(repo, detailed)?;
    let end = if nul { 0 } else { b'\n' };
    if v2 {
        write!(
            output,
            "# branch.oid {}",
            id.map_or_else(|| "(initial)".into(), |id| id.to_string())
        )?;
        output.push(end);
        output.extend_from_slice(b"# branch.head ");
        output.extend_from_slice(
            name.as_ref()
                .map_or(b"(detached)".as_slice(), |name| name.shorten().as_ref()),
        );
        output.push(end);
        if let Some(upstream) = &upstream {
            output.extend_from_slice(b"# branch.upstream ");
            output.extend_from_slice(upstream.shorten());
            output.push(end);
            if let Some((ahead, behind)) = counts {
                if ahead == usize::MAX {
                    output.extend_from_slice(b"# branch.ab +? -?");
                } else {
                    write!(output, "# branch.ab +{ahead} -{behind}")?;
                }
                output.push(end);
            }
        }
    } else {
        output.extend_from_slice(b"## ");
        if id.is_none() {
            output.extend_from_slice(b"No commits yet on ");
        }
        output.extend_from_slice(
            name.as_ref()
                .map_or(b"HEAD (no branch)".as_slice(), |name| {
                    name.shorten().as_ref()
                }),
        );
        if let Some(upstream) = &upstream {
            output.extend_from_slice(b"...");
            output.extend_from_slice(upstream.shorten());
            if upstream_id.is_none() {
                output.extend_from_slice(b" [gone]");
            } else if let Some((ahead, behind)) = counts {
                match (ahead, behind) {
                    (0, 0) => {}
                    (usize::MAX, _) => output.extend_from_slice(b" [different]"),
                    (0, behind) => write!(output, " [behind {behind}]")?,
                    (ahead, 0) => write!(output, " [ahead {ahead}]")?,
                    (ahead, behind) => write!(output, " [ahead {ahead}, behind {behind}]")?,
                }
            }
        }
        output.push(end);
    }
    Ok(output)
}

fn ahead_behind(
    repo: &gix::Repository,
    local: ObjectId,
    remote: ObjectId,
) -> Result<(usize, usize)> {
    let reachable = |tip| -> Result<HashSet<ObjectId>> {
        repo.rev_walk([tip])
            .all()?
            .map(|commit| Ok(commit?.id))
            .collect()
    };
    let local = reachable(local)?;
    let remote = reachable(remote)?;
    Ok((
        local.difference(&remote).count(),
        remote.difference(&local).count(),
    ))
}
