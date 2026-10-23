//! Retired Python restore/status/merge/commit scenarios, executed only by Rust.
use super::fixture::write_file;
use crate::{
    harness::Harness,
    outcome::{CaseOutcome, run_case},
};
use anyhow::{Context, Result, ensure};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};

type Files = BTreeMap<PathBuf, Vec<u8>>;

fn snapshot(root: &Path, include_git: bool) -> Result<Files> {
    let mut files = Files::new();
    for entry in walkdir::WalkDir::new(root).follow_links(false) {
        let entry = entry?;
        let path = entry.path().strip_prefix(root)?;
        if !include_git && path.components().any(|c| c.as_os_str() == ".git") {
            continue;
        }
        if entry.file_type().is_file() {
            files.insert(path.to_owned(), std::fs::read(entry.path())?);
        }
    }
    Ok(files)
}

fn copy_repo(source: &Path, target: &Path) -> Result<()> {
    for entry in walkdir::WalkDir::new(source).follow_links(false) {
        let entry = entry?;
        let path = target.join(entry.path().strip_prefix(source)?);
        if entry.file_type().is_dir() {
            std::fs::create_dir_all(path)?;
        } else {
            std::fs::copy(entry.path(), path)?;
        }
    }
    Ok(())
}

fn init(h: &Harness, name: &str, format: &str) -> Result<PathBuf> {
    let repo = h.repo(&format!("{name}-{format}"));
    std::fs::create_dir_all(&repo)?;
    h.reference_git(
        &repo,
        &[
            "init",
            "-b",
            "main",
            &format!("--object-format={format}"),
            ".",
        ],
    )?;
    Ok(repo)
}

fn commit(h: &Harness, repo: &Path, message: &str) -> Result<()> {
    h.reference_git(repo, &["add", "-A"])?;
    h.reference_git(repo, &["commit", "-m", message])?;
    Ok(())
}

pub fn run(h: &Harness) -> Vec<CaseOutcome> {
    let mut results = Vec::new();
    for (format, restore_suite, status_suite, merge_suite, commit_suite) in [
        (
            "sha1",
            "restore-sha1",
            "status-sha1",
            "merge-sha1",
            "commit-sha1",
        ),
        (
            "sha256",
            "restore-sha256",
            "status-sha256",
            "merge-sha256",
            "commit-sha256",
        ),
    ] {
        let commands: &[(&str, &[&str])] = &[
            ("worktree-path", &["restore", "a.txt"]),
            ("worktree-directory", &["restore", "dir"]),
            ("worktree-all", &["restore", "."]),
            ("staged-path", &["restore", "-S", "a.txt"]),
            ("staged-worktree", &["restore", "-SW", "a.txt", "dir"]),
            (
                "source-worktree",
                &["restore", "--source=HEAD~1", "a.txt", "old.txt", "new.txt"],
            ),
            ("source-staged", &["restore", "--source=HEAD~1", "-S", "."]),
            ("source-both", &["restore", "--source=HEAD~1", "-SW", "."]),
            ("nested-cwd", &["restore", "nested.txt"]),
            ("reset-default", &["reset"]),
            ("reset-mixed", &["reset", "--mixed", "HEAD~1"]),
            ("reset-soft", &["reset", "--soft", "HEAD~1"]),
            ("reset-hard", &["reset", "--hard", "HEAD~1"]),
            ("reset-path", &["reset", "HEAD", "--", "a.txt"]),
            ("reset-directory", &["reset", "HEAD~1", "--", "dir"]),
        ];
        for &(id, command) in commands {
            results.push(run_case(restore_suite, id, restore(h, format, id, command)));
        }
        results.push(run_case(
            restore_suite,
            "refusal-no-write",
            restore_refusal(h, format),
        ));
        results.push(run_case(
            status_suite,
            "porcelain-v2-records",
            status(h, format),
        ));
        for mode in ["clean", "pending", "conflict-abort"] {
            results.push(run_case(merge_suite, mode, merge(h, format, mode)));
        }
        results.push(run_case(
            commit_suite,
            "cleanup-objects",
            commit_cleanup(h, format),
        ));
    }
    results
}

fn restore_fixture(h: &Harness, format: &str, name: &str) -> Result<PathBuf> {
    let repo = init(h, name, format)?;
    for (name, bytes) in [
        (".gitattributes", b"*.txt text eol=crlf\n".as_slice()),
        ("a.txt", b"first\r\n"),
        ("old.txt", b"old\r\n"),
        ("dir/nested.txt", b"nested\r\n"),
    ] {
        write_file(&repo.join(name), bytes)?;
    }
    commit(h, &repo, "first")?;
    write_file(&repo.join("a.txt"), b"second\r\n")?;
    std::fs::remove_file(repo.join("old.txt"))?;
    write_file(&repo.join("new.txt"), b"new\r\n")?;
    commit(h, &repo, "second")?;
    write_file(&repo.join("a.txt"), b"staged\r\n")?;
    write_file(&repo.join("staged.txt"), b"staged new\r\n")?;
    h.reference_git(&repo, &["add", "."])?;
    write_file(&repo.join("a.txt"), b"unstaged\r\n")?;
    std::fs::remove_file(repo.join("dir/nested.txt"))?;
    write_file(&repo.join("untracked.txt"), b"preserve\n")?;
    Ok(repo)
}

fn restore(h: &Harness, format: &str, id: &str, args: &[&str]) -> Result<String> {
    let actual = restore_fixture(h, format, &format!("restore-{id}"))?;
    let expected = actual.with_extension("reference");
    copy_repo(&actual, &expected)?;
    let index_before = std::fs::read(actual.join(".git/index"))?;
    let (actual_cwd, expected_cwd) = if id == "nested-cwd" {
        (actual.join("dir"), expected.join("dir"))
    } else {
        (actual.clone(), expected.clone())
    };
    let reference = h.reference_git(&expected_cwd, args)?;
    let client = h.client_ok(&actual_cwd, args)?;
    ensure!(
        client == reference.stdout,
        "stdout differs for {args:?}: client={:?}, reference={:?}",
        String::from_utf8_lossy(&client),
        String::from_utf8_lossy(&reference.stdout)
    );
    ensure!(
        snapshot(&actual, false)? == snapshot(&expected, false)?,
        "worktree differs for {args:?}"
    );
    if args.contains(&"--soft") {
        ensure!(
            std::fs::read(actual.join(".git/index"))? == index_before,
            "soft reset changed index bytes"
        );
    }
    for inspect in [
        &["ls-files", "--stage", "-z"][..],
        &["rev-parse", "HEAD"],
        &["status", "--porcelain"],
        &["reflog", "--format=%H%x00%gs", "HEAD"],
        &["reflog", "--format=%H%x00%gs", "main"],
    ] {
        ensure!(
            h.reference_git(&actual, inspect)?.stdout
                == h.reference_git(&expected, inspect)?.stdout,
            "repository state differs for {inspect:?}"
        );
    }
    for path in ["HEAD", "ORIG_HEAD"] {
        ensure!(
            std::fs::read(actual.join(".git").join(path)).ok()
                == std::fs::read(expected.join(".git").join(path)).ok(),
            "{path} differs"
        );
    }
    h.reference_git(&actual, &["fsck", "--full"])?;
    Ok("stdout, tree, index, HEAD, ORIG_HEAD and reflogs".into())
}

fn restore_refusal(h: &Harness, format: &str) -> Result<String> {
    let repo = restore_fixture(h, format, "restore-refusal")?;
    let commands: &[&[&str]] = &[
        &["restore", "a.txt", "unknown-path"],
        &["restore", "--source=missing", "a.txt"],
        &["reset", "--soft", "--", "a.txt"],
        &["reset", "--hard", "--", "a.txt"],
        &["reset", "--merge"],
        &["restore", "--ours", "a.txt"],
    ];
    for args in commands {
        let before = snapshot(&repo, true)?;
        h.client_expect(&repo, args, false)?;
        ensure!(
            snapshot(&repo, true)? == before,
            "refused {args:?} wrote repository bytes"
        );
    }
    for (lock, args) in [
        ("index.lock", &["restore", "a.txt"][..]),
        ("HEAD.lock", &["reset", "--soft", "HEAD~1"]),
        ("refs/heads/main.lock", &["reset", "--mixed", "HEAD~1"]),
        ("ORIG_HEAD.lock", &["reset", "--hard", "HEAD~1"]),
    ] {
        let path = repo.join(".git").join(lock);
        write_file(&path, b"another writer\n")?;
        let before = snapshot(&repo, true)?;
        h.client_expect(&repo, args, false)?;
        ensure!(
            snapshot(&repo, true)? == before,
            "{lock} refusal modified files"
        );
        std::fs::remove_file(path)?;
    }
    Ok("6 invalid commands and 4 writer locks preserve every byte".into())
}

fn status_compare(h: &Harness, repo: &Path, args: &[&str]) -> Result<()> {
    let git_dir = repo
        .ancestors()
        .map(|p| p.join(".git"))
        .find(|p| p.is_dir())
        .context("status repository")?;
    let before = snapshot(&git_dir, true)?;
    h.compare_stdout(repo, args)?;
    ensure!(
        snapshot(&git_dir, true)? == before,
        "status wrote metadata: {args:?}"
    );
    Ok(())
}

fn status(h: &Harness, format: &str) -> Result<String> {
    let repo = init(h, "status-v2", format)?;
    let branch_flags: &[&[&str]] = &[
        &["status", "--porcelain=v2", "--branch"],
        &["status", "--porcelain=v2", "-bz"],
        &["status", "--porcelain", "-b"],
        &["status", "-sb", "-z"],
        &["status", "--porcelain=v2", "-b", "--no-ahead-behind"],
    ];
    for args in branch_flags {
        status_compare(h, &repo, args)?;
    }
    for name in [
        "root.txt",
        "deleted.txt",
        "space name.txt",
        "unicode-文.txt",
        "folder/child.txt",
        "old.txt",
    ] {
        write_file(&repo.join(name), b"base\n")?;
    }
    commit(h, &repo, "status base")?;
    let base = h.reference_git(&repo, &["rev-parse", "HEAD"])?.stdout;
    let base = std::str::from_utf8(&base)?.trim();
    for tracking in ["absent", "equal"] {
        h.reference_git(&repo, &["config", "branch.main.remote", "origin"])?;
        h.reference_git(&repo, &["config", "branch.main.merge", "refs/heads/main"])?;
        h.reference_git(
            &repo,
            &["remote", "add", "origin", "https://example.invalid/status"],
        )
        .or_else(|_| {
            h.reference_git(
                &repo,
                &[
                    "remote",
                    "set-url",
                    "origin",
                    "https://example.invalid/status",
                ],
            )
        })?;
        if tracking == "equal" {
            h.reference_git(&repo, &["update-ref", "refs/remotes/origin/main", base])?;
        }
        for args in branch_flags {
            status_compare(h, &repo, args)?;
        }
    }
    write_file(&repo.join("root.txt"), b"staged\n")?;
    h.reference_git(&repo, &["add", "root.txt"])?;
    write_file(&repo.join("root.txt"), b"unstaged\n")?;
    std::fs::remove_file(repo.join("deleted.txt"))?;
    write_file(&repo.join("space name.txt"), b"dirty\n")?;
    write_file(&repo.join("unicode-文.txt"), b"dirty\n")?;
    h.reference_git(&repo, &["mv", "old.txt", "new.txt"])?;
    write_file(&repo.join("intent.txt"), b"intent\n")?;
    h.reference_git(&repo, &["add", "-N", "intent.txt"])?;
    write_file(&repo.join("new dir/untracked.txt"), b"untracked\n")?;
    for args in [
        &["status", "--porcelain=v2"][..],
        &["status", "--porcelain=2", "-z"],
        &["status", "--porcelain=v2", "-uall"],
        &["status", "--porcelain=v2", "-uno"],
        &["status", "--porcelain=v2", "--", "folder", "space name.txt"],
        &[
            "-c",
            "status.renames=false",
            "status",
            "--porcelain=v2",
            "-z",
        ],
        &["-c", "core.quotePath=false", "status", "--porcelain=v2"],
    ] {
        status_compare(h, &repo, args)?;
    }
    status_compare(h, &repo.join("folder"), &["status", "--porcelain=v2", "-z"])?;
    Ok(
        "23 unborn/tracking/mixed/rename/intent/Unicode/NUL/nested checks, metadata unchanged"
            .into(),
    )
}

fn merge(h: &Harness, format: &str, mode: &str) -> Result<String> {
    let repo = init(h, &format!("merge-{mode}"), format)?;
    write_file(&repo.join("shared.txt"), b"base\n")?;
    commit(h, &repo, "base")?;
    h.reference_git(&repo, &["switch", "-c", "topic"])?;
    write_file(&repo.join("their.txt"), b"theirs\n")?;
    if mode == "conflict-abort" {
        write_file(&repo.join("shared.txt"), b"theirs\n")?;
    }
    commit(h, &repo, "theirs")?;
    let theirs = h.client_ok(&repo, &["rev-parse", "HEAD"])?;
    h.reference_git(&repo, &["switch", "main"])?;
    write_file(&repo.join("our.txt"), b"ours\n")?;
    if mode == "conflict-abort" {
        write_file(&repo.join("shared.txt"), b"ours\n")?;
    }
    commit(h, &repo, "ours")?;
    let ours = h.client_ok(&repo, &["rev-parse", "HEAD"])?;
    let before = snapshot(&repo, false)?;
    if mode == "conflict-abort" {
        h.client_expect(&repo, &["merge", "topic", "-m", "merge"], false)?;
        ensure!(
            repo.join(".git/MERGE_HEAD").is_file(),
            "conflict did not publish merge state"
        );
        let stages = h.reference_git(&repo, &["ls-files", "--unmerged"])?.stdout;
        ensure!(!stages.is_empty(), "missing conflict index stages");
        h.client_ok(&repo, &["merge", "--abort"])?;
        ensure!(
            snapshot(&repo, false)? == before,
            "abort did not restore original tree"
        );
        ensure!(h.client_ok(&repo, &["rev-parse", "HEAD"])? == ours);
    } else {
        let args = if mode == "pending" {
            vec!["merge", "--no-commit", "topic", "-m", "merge"]
        } else {
            vec!["merge", "topic", "-m", "merge"]
        };
        h.client_ok(&repo, &args)?;
        if mode == "pending" {
            ensure!(h.client_ok(&repo, &["rev-parse", "HEAD"])? == ours);
            ensure!(std::fs::read(repo.join(".git/MERGE_HEAD"))? == theirs);
            h.client_ok(&repo, &["commit", "--no-edit"])?;
        }
        ensure!(std::fs::read(repo.join("our.txt"))? == b"ours\n");
        ensure!(std::fs::read(repo.join("their.txt"))? == b"theirs\n");
        let parents = h.client_ok(&repo, &["cat-file", "-p", "HEAD"])?;
        let text = std::str::from_utf8(&parents)?;
        let actual: Vec<_> = text
            .lines()
            .filter_map(|line| line.strip_prefix("parent "))
            .collect();
        ensure!(
            actual
                == vec![
                    std::str::from_utf8(&ours)?.trim(),
                    std::str::from_utf8(&theirs)?.trim()
                ],
            "merge parents differ"
        );
    }
    ensure!(!repo.join(".git/MERGE_HEAD").exists(), "stale merge state");
    ensure!(h.client_ok(&repo, &["status", "--porcelain"])?.is_empty());
    h.reference_git(&repo, &["fsck", "--full"])?;
    Ok("tree, ordered parents, index, merge state and fsck".into())
}

fn commit_cleanup(h: &Harness, format: &str) -> Result<String> {
    for cleanup in ["strip", "whitespace", "verbatim", "scissors"] {
        let actual = init(h, &format!("commit-{cleanup}"), format)?;
        write_file(&actual.join("file.txt"), b"base\n")?;
        commit(h, &actual, "base")?;
        write_file(&actual.join("file.txt"), b"changed\n")?;
        h.reference_git(&actual, &["add", "file.txt"])?;
        let expected = actual.with_extension("reference");
        copy_repo(&actual, &expected)?;
        let msg = "\n# comment\n\nsubject with space  \n\n\nbody\t\n# ------------------------ >8 ------------------------\ncut\n";
        let arg = format!("--cleanup={cleanup}");
        let args = ["commit", &arg, "-m", msg];
        h.reference_git(&expected, &args)?;
        h.client_ok(&actual, &args)?;
        ensure!(
            h.client_ok(&actual, &["cat-file", "-p", "HEAD"])?
                == h.reference_git(&expected, &["cat-file", "commit", "HEAD"])?
                    .stdout,
            "commit object differs for {cleanup}"
        );
        h.reference_git(&actual, &["fsck", "--full"])
            .context("commit object fsck")?;
    }
    Ok("4 cleanup modes compare raw commit objects".into())
}
