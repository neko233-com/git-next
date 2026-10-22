use anyhow::{Result, ensure};
use gix::bstr::ByteSlice;
use std::io::Write;

use crate::status_output::{Row, Rows, Settings};

pub fn render(
    repo: &gix::Repository,
    settings: &Settings,
    rows: &Rows,
    detailed: bool,
) -> Result<Vec<u8>> {
    let mut output = Vec::new();
    let state = repo.state();
    let merging = state == Some(gix::state::InProgress::Merge);
    let picking = matches!(
        state,
        Some(gix::state::InProgress::CherryPick | gix::state::InProgress::CherryPickSequence)
    );
    let reverting = matches!(
        state,
        Some(gix::state::InProgress::Revert | gix::state::InProgress::RevertSequence)
    );
    let bisecting = state == Some(gix::state::InProgress::Bisect);
    let rebasing = matches!(
        state,
        Some(gix::state::InProgress::Rebase | gix::state::InProgress::RebaseInteractive)
    );
    let mailbox = state == Some(gix::state::InProgress::ApplyMailbox);
    let unmerged = rows.values().any(conflict);
    ensure!(
        state.is_none() || merging || picking || reverting || bisecting || rebasing || mailbox,
        "long status for this in-progress operation is not yet supported; use --porcelain"
    );
    let head = repo.head()?;
    let unborn = head.is_unborn();
    if rebasing {
        output.extend_from_slice(&rebase_header(repo, unmerged, settings.hints)?);
    } else if let Some(name) = repo.head_name()? {
        output.extend_from_slice(crate::language::bytes(b"On branch "));
        output.extend_from_slice(name.shorten());
        output.push(b'\n');
    } else {
        output.extend_from_slice(&detached_header(repo)?);
    }
    if unborn {
        output.extend_from_slice(crate::language::bytes(b"\nNo commits yet\n\n"));
    }
    let hints = settings.hints;
    output.extend_from_slice(&crate::status_branch::long_tracking(repo, detailed, hints)?);
    if mailbox {
        output.extend_from_slice(crate::language::bytes(
            b"You are in the middle of an am session.\n",
        ));
        if hints {
            output.extend_from_slice(crate::language::bytes(b"  (fix conflicts and then run \"git am --continue\")\n  (use \"git am --skip\" to skip this patch)\n  (use \"git am --abort\" to restore the original branch)\n"));
        }
        output.push(b'\n');
    }
    if merging {
        if unmerged {
            output.extend_from_slice(crate::language::bytes(b"You have unmerged paths.\n"));
            if hints {
                output.extend_from_slice(crate::language::bytes(b"  (fix conflicts and run \"git commit\")\n  (use \"git merge --abort\" to abort the merge)\n"));
            }
        } else {
            output.extend_from_slice(crate::language::bytes(
                b"All conflicts fixed but you are still merging.\n",
            ));
            if hints {
                output.extend_from_slice(crate::language::bytes(
                    b"  (use \"git commit\" to conclude merge)\n",
                ));
            }
        }
        output.push(b'\n');
    }
    if picking || reverting {
        let (name, verb, reference) = if picking {
            ("cherry-pick", "cherry-picking", "CHERRY_PICK_HEAD")
        } else {
            ("revert", "reverting", "REVERT_HEAD")
        };
        let mut reference = repo.find_reference(reference)?;
        writeln!(
            output,
            "{}",
            crate::language::format(
                "You are currently {verb} commit {id}.",
                &[
                    ("verb", crate::language::text(verb).to_owned()),
                    ("id", reference.peel_to_id()?.shorten()?.to_string()),
                ]
            )
        )?;
        if hints {
            if unmerged {
                writeln!(
                    output,
                    "{}",
                    crate::language::format(
                        "  (fix conflicts and run \"git {name} --continue\")",
                        &[("name", name.to_owned())]
                    )
                )?;
            } else {
                writeln!(
                    output,
                    "{}",
                    crate::language::format(
                        "  (all conflicts fixed: run \"git {name} --continue\")",
                        &[("name", name.to_owned())]
                    )
                )?;
            }
            writeln!(
                output,
                "{}",
                crate::language::format(
                    "  (use \"git {name} --skip\" to skip this patch)",
                    &[("name", name.to_owned())]
                )
            )?;
            writeln!(
                output,
                "{}",
                crate::language::format(
                    "  (use \"git {name} --abort\" to cancel the {name} operation)",
                    &[("name", name.to_owned())]
                )
            )?;
        }
        output.push(b'\n');
    }
    if bisecting {
        let start = read_state(repo, "BISECT_START")?.unwrap_or_default();
        output.extend_from_slice(crate::language::bytes(b"You are currently bisecting"));
        if !start.trim().is_empty() {
            output.extend_from_slice(crate::language::bytes(b", started from branch '"));
            output.extend_from_slice(start.trim());
            output.push(b'\'');
        }
        output.extend_from_slice(b".\n");
        if hints {
            output.extend_from_slice(crate::language::bytes(
                b"  (use \"git bisect reset\" to get back to the original branch)\n",
            ));
        }
        output.push(b'\n');
    }
    let mut staged = false;
    let mut unstaged = false;
    let mut untracked = false;
    for group in 0..5 {
        let selected: Vec<_> = rows
            .iter()
            .filter(|(_, row)| match group {
                0 => !conflict(row) && !matches!(row.code[0], ' ' | '?' | '!'),
                1 => conflict(row),
                2 => !conflict(row) && !matches!(row.code[1], ' ' | '?' | '!'),
                3 => row.code == ['?', '?'],
                _ => row.code == ['!', '!'],
            })
            .collect();
        if selected.is_empty() {
            continue;
        }
        match group {
            0 => {
                staged = true;
                output.extend_from_slice(crate::language::bytes(b"Changes to be committed:\n"));
                if hints && !merging && !picking {
                    output.extend_from_slice(if unborn {
                        crate::language::bytes(
                            b"  (use \"git rm --cached <file>...\" to unstage)\n",
                        )
                    } else {
                        crate::language::bytes(
                            b"  (use \"git restore --staged <file>...\" to unstage)\n",
                        )
                    });
                }
            }
            1 => {
                unstaged = true;
                output.extend_from_slice(crate::language::bytes(b"Unmerged paths:\n"));
                if hints {
                    if !merging && !picking {
                        output.extend_from_slice(crate::language::bytes(
                            b"  (use \"git restore --staged <file>...\" to unstage)\n",
                        ));
                    }
                    let deletion = selected.iter().any(|(_, row)| row.code.contains(&'D'));
                    let addition = selected.iter().any(|(_, row)| row.code != ['D', 'D']);
                    output.extend_from_slice(match (addition, deletion) {
                        (true, true) => crate::language::bytes(
                            b"  (use \"git add/rm <file>...\" as appropriate to mark resolution)\n",
                        ),
                        (false, true) => crate::language::bytes(
                            b"  (use \"git rm <file>...\" to mark resolution)\n",
                        ),
                        _ => crate::language::bytes(
                            b"  (use \"git add <file>...\" to mark resolution)\n",
                        ),
                    });
                }
            }
            2 => {
                unstaged = true;
                output
                    .extend_from_slice(crate::language::bytes(b"Changes not staged for commit:\n"));
                if hints {
                    output.extend_from_slice(
                        if selected.iter().any(|(_, row)| row.code[1] == 'D') {
                            crate::language::bytes(b"  (use \"git add/rm <file>...\" to update what will be committed)\n")
                        } else {
                            crate::language::bytes(b"  (use \"git add <file>...\" to update what will be committed)\n")
                        },
                    );
                    output.extend_from_slice(crate::language::bytes(b"  (use \"git restore <file>...\" to discard changes in working directory)\n"));
                    if selected.iter().any(|(_, row)| {
                        row.submodule
                            .is_some_and(|state| state[2] == b'M' || state[3] == b'U')
                    }) {
                        output.extend_from_slice(crate::language::bytes(b"  (commit or discard the untracked or modified content in submodules)\n"));
                    }
                }
            }
            3 => {
                untracked = true;
                output.extend_from_slice(crate::language::bytes(b"Untracked files:\n"));
                if hints {
                    output.extend_from_slice(crate::language::bytes(
                        b"  (use \"git add <file>...\" to include in what will be committed)\n",
                    ));
                }
            }
            _ => {
                output.extend_from_slice(crate::language::bytes(b"Ignored files:\n"));
                if hints {
                    output.extend_from_slice(crate::language::bytes(
                        b"  (use \"git add -f <file>...\" to include in what will be committed)\n",
                    ));
                }
            }
        }
        for ((_, path), row) in selected {
            output.push(b'\t');
            if group < 3 {
                let label = if group == 1 {
                    match row.code {
                        ['D', 'D'] => "both deleted:",
                        ['A', 'U'] => "added by us:",
                        ['U', 'D'] => "deleted by them:",
                        ['U', 'A'] => "added by them:",
                        ['D', 'U'] => "deleted by us:",
                        ['A', 'A'] => "both added:",
                        _ => "both modified:",
                    }
                } else {
                    match row.code[if group == 0 { 0 } else { 1 }] {
                        'A' => "new file:",
                        'D' => "deleted:",
                        'R' => "renamed:",
                        'C' => "copied:",
                        'T' => "typechange:",
                        _ => "modified:",
                    }
                };
                let label = crate::language::text(label);
                let columns: usize = label
                    .chars()
                    .map(|ch| if ch.is_ascii() { 1 } else { 2 })
                    .sum();
                let width: usize = if group == 1 { 17 } else { 12 };
                write!(
                    output,
                    "{label}{}",
                    " ".repeat(width.saturating_sub(columns))
                )?;
                if let Some(source) = &row.source {
                    output.extend_from_slice(&settings.long_path(source, false));
                    output.extend_from_slice(b" -> ");
                }
            }
            output.extend_from_slice(&settings.long_path(path, row.directory));
            if group == 2
                && let Some(state) = row.submodule
            {
                let mut notes = Vec::new();
                if state[1] == b'C' {
                    notes.push(crate::language::text("new commits"));
                }
                if state[2] == b'M' {
                    notes.push(crate::language::text("modified content"));
                }
                if state[3] == b'U' {
                    notes.push(crate::language::text("untracked content"));
                }
                if !notes.is_empty() {
                    write!(output, " ({})", notes.join(", "))?;
                }
            }
            output.push(b'\n');
        }
        output.push(b'\n');
    }
    if !staged && (!merging || unmerged) {
        output.extend_from_slice(if unstaged {
            if hints {
                crate::language::bytes(b"no changes added to commit (use \"git add\" and/or \"git commit -a\")\n")
            } else {
                crate::language::bytes(b"no changes added to commit\n")
            }
        } else if untracked {
            if hints {
                crate::language::bytes(b"nothing added to commit but untracked files present (use \"git add\" to track)\n")
            } else {
                crate::language::bytes(b"nothing added to commit but untracked files present\n")
            }
        } else if unborn {
            if hints {
                crate::language::bytes(b"nothing to commit (create/copy files and use \"git add\" to track)\n")
            } else {
                crate::language::bytes(b"nothing to commit\n")
            }
        } else if matches!(settings.untracked, gix::status::UntrackedFiles::None) {
            if hints {
                crate::language::bytes(b"nothing to commit (use -u to show untracked files)\n")
            } else {
                crate::language::bytes(b"nothing to commit\n")
            }
        } else {
            crate::language::bytes(b"nothing to commit, working tree clean\n")
        });
    }
    if staged && matches!(settings.untracked, gix::status::UntrackedFiles::None) {
        output.extend_from_slice(crate::language::bytes(
            b"Untracked files not listed (use -u option to show untracked files)\n",
        ));
    }
    Ok(output)
}

fn conflict(row: &Row) -> bool {
    row.code.contains(&'U') || matches!(row.code, ['A', 'A'] | ['D', 'D'])
}

fn rebase_header(repo: &gix::Repository, unmerged: bool, hints: bool) -> Result<Vec<u8>> {
    let interactive = repo.git_dir().join("rebase-merge").is_dir();
    let directory = if interactive {
        "rebase-merge"
    } else {
        "rebase-apply"
    };
    let onto = read_state(repo, &format!("{directory}/onto"))?.unwrap_or_default();
    let onto = repo
        .find_object(gix::ObjectId::from_hex(onto.trim())?)?
        .id()
        .shorten()?;
    let head = read_state(repo, &format!("{directory}/head-name"))?.unwrap_or_default();
    let branch = head.trim().strip_prefix(b"refs/heads/");
    let done = read_state(repo, "rebase-merge/done")?.unwrap_or_default();
    let todo = read_state(repo, "rebase-merge/git-rebase-todo")?.unwrap_or_default();
    let commands = |data: &[u8]| -> Result<Vec<Vec<u8>>> {
        let mut result = Vec::new();
        for line in data.lines().map(ByteSlice::trim) {
            if line.is_empty() || line.starts_with(b"#") {
                continue;
            }
            let mut fields = line.splitn(3, |byte| byte.is_ascii_whitespace());
            let command = fields.next().unwrap_or_default();
            let mut formatted = command.to_vec();
            if matches!(
                command,
                b"pick" | b"edit" | b"reword" | b"squash" | b"fixup" | b"drop"
            ) {
                let object = fields.next().unwrap_or_default();
                let id = gix::ObjectId::from_hex(object)?;
                let id = repo.find_object(id)?.id().shorten()?;
                write!(formatted, " {id}")?;
                if let Some(subject) = fields.next() {
                    formatted.push(b' ');
                    formatted.extend_from_slice(subject);
                }
            } else {
                ensure!(
                    matches!(
                        command,
                        b"exec" | b"break" | b"label" | b"reset" | b"noop" | b"update-ref"
                    ),
                    "long status for this rebase command is not yet supported; use --porcelain"
                );
                formatted = line.to_vec();
            }
            result.push(formatted);
        }
        Ok(result)
    };
    let done = commands(&done)?;
    let todo = commands(&todo)?;
    let mut output = Vec::new();
    writeln!(
        output,
        "{}",
        crate::language::format(
            "{interactive}rebase in progress; onto {onto}",
            &[
                (
                    "interactive",
                    crate::language::text(if interactive { "interactive " } else { "" }).to_owned()
                ),
                ("onto", onto.to_string()),
            ]
        )
    )?;
    if interactive && done.is_empty() {
        output.extend_from_slice(crate::language::bytes(b"No commands done.\n"));
    } else if interactive {
        let count = done.len();
        writeln!(
            output,
            "{}",
            crate::language::format(
                "Last command{s} done ({count} command{s} done):",
                &[
                    ("s", if count == 1 { "" } else { "s" }.into()),
                    ("count", count.to_string())
                ]
            )
        )?;
        for command in done.iter().skip(count.saturating_sub(2)) {
            output.extend_from_slice(b"   ");
            output.extend_from_slice(command);
            output.push(b'\n');
        }
        if count > 2 && hints {
            output.extend_from_slice(crate::language::bytes(
                b"  (see more in file .git/rebase-merge/done)\n",
            ));
        }
    }
    if interactive && todo.is_empty() {
        output.extend_from_slice(crate::language::bytes(b"No commands remaining.\n"));
    } else if interactive {
        let count = todo.len();
        writeln!(
            output,
            "{}",
            crate::language::format(
                "Next command{s} to do ({count} remaining command{s}):",
                &[
                    ("s", if count == 1 { "" } else { "s" }.into()),
                    ("count", count.to_string())
                ]
            )
        )?;
        for command in todo.iter().take(2) {
            output.extend_from_slice(b"   ");
            output.extend_from_slice(command);
            output.push(b'\n');
        }
        if hints {
            output.extend_from_slice(crate::language::bytes(
                b"  (use \"git rebase --edit-todo\" to view and edit)\n",
            ));
        }
    }
    let editing = interactive && repo.git_dir().join("rebase-merge/amend").is_file();
    output.extend_from_slice(if editing {
        crate::language::bytes(b"You are currently editing a commit while rebasing")
    } else {
        crate::language::bytes(b"You are currently rebasing")
    });
    if let Some(branch) = branch {
        output.extend_from_slice(crate::language::bytes(b" branch '"));
        output.extend_from_slice(branch);
        writeln!(
            output,
            "{}",
            crate::language::format("' on '{onto}'.", &[("onto", onto.to_string())])
        )?;
    } else {
        output.extend_from_slice(b".\n");
    }
    if hints {
        if editing {
            output.extend_from_slice(crate::language::bytes(b"  (use \"git commit --amend\" to amend the current commit)\n  (use \"git rebase --continue\" once you are satisfied with your changes)\n"));
        } else if unmerged {
            output.extend_from_slice(crate::language::bytes(b"  (fix conflicts and then run \"git rebase --continue\")\n  (use \"git rebase --skip\" to skip this patch)\n  (use \"git rebase --abort\" to check out the original branch)\n"));
        } else {
            output.extend_from_slice(crate::language::bytes(
                b"  (all conflicts fixed: run \"git rebase --continue\")\n",
            ));
        }
    }
    output.push(b'\n');
    Ok(output)
}

fn read_state(repo: &gix::Repository, name: &str) -> Result<Option<Vec<u8>>> {
    use std::io::Read;
    let file = match std::fs::File::open(repo.git_dir().join(name)) {
        Ok(file) => file,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error.into()),
    };
    let mut data = Vec::new();
    file.take(65537).read_to_end(&mut data)?;
    ensure!(
        data.len() <= 65536,
        "operation metadata exceeds the supported size"
    );
    Ok(Some(data))
}

fn detached_header(repo: &gix::Repository) -> Result<Vec<u8>> {
    let current = repo.head_id()?.detach();
    let mut log = repo.head()?.log_iter();
    if let Some(entries) = log.rev()? {
        for entry in entries {
            let entry = entry?;
            let Some(message) = entry.message.strip_prefix(b"checkout: moving from ") else {
                continue;
            };
            let Some(position) = message.rfind(b" to ") else {
                continue;
            };
            let target = message[position + 4..].as_bstr();
            let starting = entry.new_oid;
            let mut description = repo
                .find_object(starting)?
                .id()
                .shorten()?
                .to_string()
                .into_bytes();
            if target != b"HEAD".as_bstr()
                && let Some(mut reference) = repo.try_find_reference(target).ok().flatten()
                && reference.peel_to_id()?.detach() == starting
            {
                let name = reference.name().as_bstr();
                description = name.strip_prefix(b"refs/tags/").unwrap_or(name).to_vec();
            }
            let mut output = Vec::new();
            write!(
                output,
                "{}",
                crate::language::format(
                    "HEAD detached {where} ",
                    &[(
                        "where",
                        crate::language::text(if current == starting { "at" } else { "from" })
                            .to_owned()
                    )]
                )
            )?;
            output.extend_from_slice(&description);
            output.push(b'\n');
            return Ok(output);
        }
    }
    Ok(crate::language::bytes(b"Not currently on any branch.\n").to_vec())
}
