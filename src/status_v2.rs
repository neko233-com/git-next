use anyhow::{Context, Result, ensure};
use gix::{
    bstr::ByteSlice,
    index::entry::{Mode, Stage},
};
use std::io::Write;

pub fn render(
    repo: &gix::Repository,
    options: &crate::status_output::Options,
    settings: &crate::status_output::Settings,
    rows: &crate::status_output::Rows,
    head: &gix::index::State,
    index: &gix::index::State,
) -> Result<Vec<u8>> {
    let mut output = Vec::new();
    if options.branch {
        output.extend_from_slice(&options.branch_header(repo)?);
    }
    let null = repo.object_hash().null();
    let config = repo.config_snapshot();
    let filemode = config.try_boolean("core.filemode")?.unwrap_or(true);
    let symlinks = config.try_boolean("core.symlinks")?.unwrap_or(true);
    for group in 0..4 {
        for ((_, path), row) in rows {
            if row.code == [' '; 2] {
                continue;
            }
            let stages = [Stage::Base, Stage::Ours, Stage::Theirs]
                .map(|stage| index.entry_by_path_and_stage(path.as_bstr(), stage));
            let unmerged = stages.iter().any(Option::is_some);
            let untracked = row.code == ['?', '?'];
            let ignored = row.code == ['!', '!'];
            if group
                != if ignored {
                    3
                } else if untracked {
                    2
                } else {
                    usize::from(unmerged)
                }
            {
                continue;
            }
            if untracked {
                output.extend_from_slice(b"? ");
            } else if ignored {
                output.extend_from_slice(b"! ");
            } else {
                let xy: String = row
                    .code
                    .iter()
                    .map(|code| if *code == ' ' { '.' } else { *code })
                    .collect();
                if unmerged {
                    let modes = stages.map(|entry| entry.map_or(0, |entry| entry.mode.bits()));
                    let ids = stages.map(|entry| entry.map_or(null, |entry| entry.id));
                    let representative = stages[1].or(stages[0]).or(stages[2]);
                    let mode = worktree_mode(repo, path, representative, filemode, symlinks)?;
                    write!(
                        output,
                        "u {xy} N... {:06o} {:06o} {:06o} {mode:06o} {} {} {} ",
                        modes[0], modes[1], modes[2], ids[0], ids[1], ids[2]
                    )?;
                } else {
                    let source = row.source.as_ref().unwrap_or(path);
                    let h = head.entry_by_path_and_stage(source.as_bstr(), Stage::Unconflicted);
                    let i = index
                        .entry_by_path_and_stage(path.as_bstr(), Stage::Unconflicted)
                        .filter(|entry| {
                            !entry
                                .flags
                                .contains(gix::index::entry::Flags::INTENT_TO_ADD)
                        });
                    let m_h = h.map_or(0, |entry| entry.mode.bits());
                    let m_i = i.map_or(0, |entry| entry.mode.bits());
                    let sub = if m_i == Mode::COMMIT.bits() || m_h == Mode::COMMIT.bits() {
                        row.submodule.unwrap_or(*b"S...")
                    } else {
                        *b"N..."
                    };
                    let sub = std::str::from_utf8(&sub)?;
                    let m_w = if row.code[1] == 'D' || (row.code[0] == 'D' && i.is_none()) {
                        0
                    } else {
                        worktree_mode(repo, path, i, filemode, symlinks)?
                    };
                    let h_h = h.map_or(null, |entry| entry.id);
                    let h_i = i.map_or(null, |entry| entry.id);
                    write!(
                        output,
                        "{} {xy} {sub} {m_h:06o} {m_i:06o} {m_w:06o} {h_h} {h_i} ",
                        if row.source.is_some() { '2' } else { '1' }
                    )?;
                    if row.source.is_some() {
                        ensure!(
                            matches!(row.code[0], 'R' | 'C'),
                            "porcelain v2 worktree renames are not yet supported"
                        );
                        let score = crate::difference::status_rename_score(
                            repo,
                            h.context("missing rename source")?,
                            i.context("missing rename destination")?,
                        )?;
                        write!(output, "{}{score} ", row.code[0])?;
                    }
                }
            }
            if options.nul {
                output.extend_from_slice(path);
            } else {
                output.extend_from_slice(&crate::difference::quote(path, settings.quote_unicode));
            }
            if let Some(source) = &row.source {
                output.push(if options.nul { 0 } else { b'\t' });
                if options.nul {
                    output.extend_from_slice(source);
                } else {
                    output.extend_from_slice(&crate::difference::quote(
                        source,
                        settings.quote_unicode,
                    ));
                }
            }
            output.push(if options.nul { 0 } else { b'\n' });
        }
    }
    Ok(output)
}

pub fn submodule_state(status: &gix::submodule::Status) -> [u8; 4] {
    use gix::status::{
        Item,
        index_worktree::Item as Worktree,
        plumbing::index_as_worktree::{Change, EntryStatus},
    };
    let mut flags = *b"S...";
    if status.checked_out_head_id != status.index_id {
        flags[1] = b'C';
    }
    for item in status.changes.iter().flatten() {
        match item {
            Item::IndexWorktree(Worktree::Modification {
                status: EntryStatus::Change(Change::SubmoduleModification(nested)),
                ..
            }) => {
                let nested = submodule_state(nested);
                if nested[1] == b'C' || nested[2] == b'M' {
                    flags[2] = b'M';
                }
                if nested[3] == b'U' {
                    flags[3] = b'U';
                }
            }
            Item::IndexWorktree(change)
                if change.summary()
                    == Some(
                        gix::status::plumbing::index_as_worktree_with_renames::Summary::Added,
                    ) =>
            {
                flags[3] = b'U'
            }
            _ => flags[2] = b'M',
        }
    }
    flags
}

fn worktree_mode(
    repo: &gix::Repository,
    path: &[u8],
    indexed: Option<&gix::index::Entry>,
    filemode: bool,
    symlinks: bool,
) -> Result<u32> {
    let path = repo
        .workdir()
        .context("status requires a working tree")?
        .join(gix::path::from_bstr(path.as_bstr())?);
    let metadata = match std::fs::symlink_metadata(&path) {
        Ok(metadata) => metadata,
        Err(error)
            if matches!(
                error.kind(),
                std::io::ErrorKind::NotFound | std::io::ErrorKind::NotADirectory
            ) =>
        {
            return Ok(0);
        }
        Err(error) => return Err(error).context("could not read status file metadata"),
    };
    if metadata.file_type().is_symlink()
        || (!symlinks
            && metadata.is_file()
            && indexed.is_some_and(|entry| entry.mode == Mode::SYMLINK))
    {
        return Ok(Mode::SYMLINK.bits());
    }
    if metadata.is_dir() {
        return Ok(if indexed.is_some_and(|entry| entry.mode == Mode::COMMIT) {
            Mode::COMMIT.bits()
        } else {
            0
        });
    }
    ensure!(
        metadata.is_file(),
        "porcelain v2 special-file modes are not yet supported"
    );
    if !filemode {
        return Ok(indexed
            .filter(|entry| matches!(entry.mode, Mode::FILE | Mode::FILE_EXECUTABLE))
            .map_or(Mode::FILE.bits(), |entry| entry.mode.bits()));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        Ok(if metadata.permissions().mode() & 0o100 != 0 {
            Mode::FILE_EXECUTABLE.bits()
        } else {
            Mode::FILE.bits()
        })
    }
    #[cfg(not(unix))]
    {
        Ok(Mode::FILE.bits())
    }
}
