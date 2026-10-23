//! L1 deep local scenarios (legacy Python extended/plumbing slices).
use crate::harness::Harness;
use crate::outcome::{CaseOutcome, run_case};
use crate::suite::fixture::{seed_repo, write_file};
use anyhow::{Context, Result, ensure};

const SUITE: &str = "local-deep";

pub fn run(h: &Harness) -> Vec<CaseOutcome> {
    let cases: Vec<crate::outcome::LocalCase> = vec![
        ("extended-stash", case_extended_stash),
        ("hooks-pre-commit", case_hooks_pre_commit),
        ("merge-ff-files", case_merge_ff_files),
        ("restore-worktree", case_restore_worktree),
        ("status-porcelain-v2", case_status_porcelain_v2),
        ("show-patch", case_show_patch),
        ("diff-staged", case_diff_staged),
        ("reset-soft", case_reset_soft),
    ];
    cases
        .into_iter()
        .map(|(id, case)| run_case(SUITE, id, case(h)))
        .collect()
}

fn case_extended_stash(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-stash")?;
    write_file(&repo.join("README.md"), b"dirty\n")?;
    h.client_ok(&repo, &["stash", "push", "-m", "wip"])?;
    ensure!(h.client_ok(&repo, &["status", "--porcelain"])?.is_empty());
    ensure!(std::fs::read(repo.join("README.md"))? == b"seed\n");
    h.client_ok(&repo, &["stash", "pop"])?;
    ensure!(std::fs::read(repo.join("README.md"))? == b"dirty\n");
    Ok("push pop round-trip".into())
}

fn case_hooks_pre_commit(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-hooks")?;
    let hooks = repo.join(".git/hooks");
    std::fs::create_dir_all(&hooks)?;
    let marker = h.root.join("hook.ran");
    if cfg!(windows) {
        write_file(
            &hooks.join("pre-commit"),
            format!("@echo off\r\necho ran> \"{}\"\r\n", marker.display()).as_bytes(),
        )?;
    } else {
        write_file(
            &hooks.join("pre-commit"),
            format!("#!/bin/sh\necho ran > '{}'\n", marker.display()).as_bytes(),
        )?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(
                hooks.join("pre-commit"),
                std::fs::Permissions::from_mode(0o755),
            )?;
        }
    }
    write_file(&repo.join("hook.txt"), b"x\n")?;
    h.client_ok(&repo, &["add", "hook.txt"])?;
    h.client_ok(&repo, &["commit", "-m", "hook test"])?;
    let ran = std::fs::read_to_string(&marker).context("hook marker")?;
    ensure!(ran.trim() == "ran");
    Ok("pre-commit ran".into())
}

fn case_merge_ff_files(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-merge")?;
    h.reference_git(&repo, &["switch", "-c", "side"])?;
    write_file(&repo.join("side.txt"), b"side\n")?;
    h.reference_git(&repo, &["add", "side.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "side"])?;
    h.reference_git(&repo, &["switch", "main"])?;
    h.client_ok(&repo, &["merge", "side", "--ff-only"])?;
    ensure!(repo.join("side.txt").exists());
    Ok("ff-only merge tree".into())
}

fn case_restore_worktree(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-restore")?;
    write_file(&repo.join("README.md"), b"changed\n")?;
    h.client_ok(&repo, &["restore", "README.md"])?;
    ensure!(std::fs::read(repo.join("README.md"))? == b"seed\n");
    Ok("restore path".into())
}

fn case_status_porcelain_v2(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-status-v2")?;
    write_file(&repo.join("new.txt"), b"n\n")?;
    h.client_ok(&repo, &["add", "new.txt"])?;
    h.compare_stdout(&repo, &["status", "--porcelain=v2"])?;
    Ok("porcelain v2".into())
}

fn case_show_patch(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-show")?;
    write_file(&repo.join("extra.txt"), b"e\n")?;
    h.reference_git(&repo, &["add", "extra.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "extra"])?;
    h.compare_stdout(&repo, &["show", "--no-patch", "HEAD"])?;
    Ok("show HEAD".into())
}

fn case_diff_staged(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-diff")?;
    write_file(&repo.join("README.md"), b"staged\n")?;
    h.client_ok(&repo, &["add", "README.md"])?;
    h.compare_stdout(&repo, &["diff", "--cached", "--no-ext-diff"])?;
    Ok("cached diff".into())
}

fn case_reset_soft(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "full-reset")?;
    write_file(&repo.join("extra.txt"), b"e\n")?;
    h.reference_git(&repo, &["add", "extra.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "extra"])?;
    let before = h.client_ok(&repo, &["rev-parse", "HEAD"])?;
    h.client_ok(&repo, &["reset", "--soft", "HEAD~1"])?;
    let after = h.client_ok(&repo, &["rev-parse", "HEAD"])?;
    ensure!(before != after);
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    Ok("soft reset + staged status".into())
}
