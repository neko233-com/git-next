//! L1 broad local parity (legacy acceptance.py / plumbing / status / restore / commit slices).
use crate::harness::Harness;
use crate::outcome::{CaseOutcome, run_case};
use crate::suite::fixture::{seed_repo, write_file};
use anyhow::{Result, ensure};
use std::path::PathBuf;

const SUITE: &str = "local-broad";

pub fn run(h: &Harness) -> Vec<CaseOutcome> {
    let cases: Vec<crate::outcome::LocalCase> = vec![
        ("clone-local-tree", case_clone_local),
        ("fetch-pull-ff", case_fetch_pull_ff),
        ("version-doctor-json", case_version_doctor),
        ("plumbing-rev-parse", case_plumbing),
        ("status-short-sb", case_status_formats),
        ("commit-message-cleanup", case_commit_cleanup),
        ("restore-source-head1", case_restore_source),
        ("restore-staged", case_restore_staged),
        ("reset-mixed", case_reset_mixed),
        ("fail-clone-exists", case_fail_closed),
        ("index-lock-refuse", case_index_lock),
    ];
    cases
        .into_iter()
        .map(|(id, case)| run_case(SUITE, id, case(h)))
        .collect()
}

fn case_clone_local(h: &Harness) -> Result<String> {
    let source = h.repo("broad-source");
    h.init_fixture_repo(&source, "main")?;
    write_file(&source.join("README.md"), b"hello\n")?;
    write_file(&source.join("folder/data.txt"), b"nested\n")?;
    h.reference_git(&source, &["add", "."])?;
    h.reference_git(&source, &["commit", "-m", "fixture"])?;
    h.reference_git(&source, &["tag", "-a", "v-fixture", "-m", "tag"])?;
    let head = h.reference_git(&source, &["rev-parse", "HEAD"])?.stdout;
    let target = h.repo("broad-clone");
    h.client_ok(
        &h.root,
        &["clone", source.to_str().unwrap(), target.to_str().unwrap()],
    )?;
    ensure!(h.client_ok(&target, &["rev-parse", "HEAD"])? == head);
    ensure!(h.client_ok(&target, &["status", "--porcelain"])?.is_empty());
    h.compare_stdout(&target, &["cat-file", "-p", "HEAD:README.md"])?;
    h.compare_stdout(&target, &["show-ref"])?;
    Ok("clone + refs + cat-file".into())
}

fn case_fetch_pull_ff(h: &Harness) -> Result<String> {
    let source = h.repo("broad-fetch-src");
    h.init_fixture_repo(&source, "main")?;
    write_file(&source.join("README.md"), b"hello\n")?;
    h.reference_git(&source, &["add", "."])?;
    h.reference_git(&source, &["commit", "-m", "one"])?;
    let target = h.repo("broad-fetch-clone");
    h.client_ok(
        &h.root,
        &["clone", source.to_str().unwrap(), target.to_str().unwrap()],
    )?;
    write_file(&source.join("README.md"), b"second\n")?;
    h.reference_git(&source, &["add", "."])?;
    h.reference_git(&source, &["commit", "-m", "two"])?;
    h.client_ok(&target, &["fetch"])?;
    h.client_ok(&target, &["pull", "--ff-only"])?;
    ensure!(std::fs::read_to_string(target.join("README.md"))?.contains("second"));
    Ok("fetch + ff pull".into())
}

fn case_version_doctor(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "broad-meta")?;
    let version = h.client_ok(&repo, &["version", "--json"])?;
    ensure!(String::from_utf8_lossy(&version).contains("git-next"));
    let doctor = h.client_ok(&repo, &["doctor", "--json"])?;
    ensure!(String::from_utf8_lossy(&doctor).contains("replacement_ready"));
    Ok("version + doctor json".into())
}

fn case_plumbing(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "broad-plumb")?;
    h.client_ok(&repo, &["branch", "topic"])?;
    h.compare_canonical_path_stdout(&repo, &["rev-parse", "--show-toplevel"])?;
    let bare = h.client_ok(&repo, &["rev-parse", "--is-bare-repository"])?;
    ensure!(String::from_utf8_lossy(&bare).trim() == "false");
    h.client_ok(&repo, &["config", "user.name", "Plumbing"])?;
    h.compare_stdout(&repo, &["config", "user.name"])?;
    let compat = h.client_ok(&repo, &["compatibility", "--json"])?;
    ensure!(String::from_utf8_lossy(&compat).contains("git-next"));
    h.client_ok(&repo, &["language", "list"])?;
    Ok("plumbing meta".into())
}

fn case_status_formats(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "broad-status")?;
    write_file(&repo.join("dirty.txt"), b"d\n")?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    h.compare_stdout(&repo, &["status", "-sb"])?;
    h.compare_stdout(&repo, &["status", "--short"])?;
    Ok("porcelain + short + branch".into())
}

fn case_commit_cleanup(h: &Harness) -> Result<String> {
    let repo = h.repo("broad-commit");
    h.init_fixture_repo(&repo, "main")?;
    h.reference_git(&repo, &["config", "commit.cleanup", "strip"])?;
    write_file(&repo.join("file.txt"), b"base\n")?;
    h.reference_git(&repo, &["add", "."])?;
    h.reference_git(&repo, &["commit", "-m", "base"])?;
    write_file(&repo.join("file.txt"), b"changed\n")?;
    h.client_ok(&repo, &["add", "file.txt"])?;
    let msg = "\n# heading\r\n\r\nsubject with space  \r\n\r\n\r\nbody\t\r\n";
    h.client_ok(&repo, &["commit", "-m", msg])?;
    h.compare_stdout(&repo, &["cat-file", "-p", "HEAD"])?;
    Ok("commit.cleanup strip".into())
}

fn setup_restore_fixture(h: &Harness, name: &str) -> Result<PathBuf> {
    let repo = h.repo(name);
    h.init_fixture_repo(&repo, "main")?;
    write_file(&repo.join(".gitattributes"), b"*.txt text eol=crlf\n")?;
    write_file(&repo.join("a.txt"), b"first\r\n")?;
    h.reference_git(&repo, &["add", "."])?;
    h.reference_git(&repo, &["commit", "-m", "first"])?;
    write_file(&repo.join("a.txt"), b"second\r\n")?;
    h.reference_git(&repo, &["add", "a.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "second"])?;
    write_file(&repo.join("a.txt"), b"dirty\r\n")?;
    Ok(repo)
}

fn case_restore_source(h: &Harness) -> Result<String> {
    let repo = setup_restore_fixture(h, "broad-restore-source")?;
    h.client_ok(&repo, &["restore", "--source=HEAD~1", "a.txt"])?;
    ensure!(std::fs::read(repo.join("a.txt"))? == b"first\r\n");
    Ok("restore --source=HEAD~1".into())
}

fn case_restore_staged(h: &Harness) -> Result<String> {
    let repo = setup_restore_fixture(h, "broad-restore-staged")?;
    write_file(&repo.join("a.txt"), b"staged\r\n")?;
    h.client_ok(&repo, &["add", "a.txt"])?;
    h.client_ok(&repo, &["restore", "-S", "a.txt"])?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    Ok("restore -S".into())
}

fn case_reset_mixed(h: &Harness) -> Result<String> {
    let repo = setup_restore_fixture(h, "broad-reset-mixed")?;
    h.client_ok(&repo, &["reset", "--mixed", "HEAD~1"])?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    Ok("reset --mixed".into())
}

fn case_fail_closed(h: &Harness) -> Result<String> {
    let source = seed_repo(h, "broad-fail-src")?;
    let target = h.repo("broad-fail-dst");
    h.client_ok(
        &h.root,
        &["clone", source.to_str().unwrap(), target.to_str().unwrap()],
    )?;
    h.client_expect(
        &h.root,
        &["clone", source.to_str().unwrap(), target.to_str().unwrap()],
        false,
    )?;
    h.client_expect(&target, &["not-a-command"], false)?;
    Ok("clone exists + unknown cmd".into())
}

fn case_index_lock(h: &Harness) -> Result<String> {
    let repo = seed_repo(h, "broad-lock")?;
    write_file(&repo.join(".git/index.lock"), b"other writer")?;
    h.client_expect(&repo, &["add", "-A"], false)?;
    ensure!(std::fs::read_to_string(repo.join(".git/index.lock"))? == "other writer");
    Ok("index.lock preserved".into())
}
