use crate::harness::Harness;
use crate::outcome::{CaseOutcome, run_case};
use anyhow::{Context, Result, ensure};
use std::path::{Path, PathBuf};

const SUITE: &str = "l0";
type MatrixCase = (&'static str, fn(&Harness, bool) -> Result<String>);

pub fn run_matrix(h: &Harness, full: bool) -> Vec<CaseOutcome> {
    let mut outcomes = Vec::new();
    let cases: Vec<MatrixCase> = vec![
        ("init", case_init),
        ("clone", case_clone),
        ("add", case_add),
        ("commit", case_commit),
        ("status", case_status),
        ("diff", case_diff),
        ("show", case_show),
        ("log", case_log),
        ("branch", case_branch),
        ("switch", case_switch),
        ("checkout", case_checkout),
        ("merge", case_merge),
        ("fetch", case_fetch),
        ("pull", case_pull),
        ("push", case_push),
        ("remote", case_remote),
        ("tag", case_tag),
        ("stash", case_stash),
        ("cherry-pick", case_cherry_pick),
        ("revert", case_revert),
        ("rebase", case_rebase),
        ("submodule", case_submodule),
        ("worktree", case_worktree),
        ("restore", case_restore),
        ("reset", case_reset),
        ("rev-parse", case_rev_parse),
        ("cat-file", case_cat_file),
        ("hash-object", case_hash_object),
        ("ls-files", case_ls_files),
        ("show-ref", case_show_ref),
        ("ls-remote", case_ls_remote),
        ("config", case_config),
        ("lfs", case_lfs),
        ("eol", case_eol),
        ("language", case_language),
        ("compatibility", case_compatibility),
        ("version", case_version),
        ("doctor", case_doctor),
        ("install", case_install),
    ];
    for (name, case) in cases {
        outcomes.push(run_case(SUITE, name, case(h, full)));
    }
    outcomes
}

fn bytes_str(bytes: &[u8]) -> Result<String> {
    Ok(String::from_utf8_lossy(bytes).trim().to_owned())
}

fn write_file(path: &Path, bytes: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    std::fs::write(path, bytes)?;
    Ok(())
}

fn seed_repo(h: &Harness, name: &str) -> Result<(PathBuf, Vec<u8>)> {
    let repo = h.repo(name);
    h.init_fixture_repo(&repo, "main")?;
    write_file(&repo.join("README.md"), b"hello\n")?;
    h.reference_git(&repo, &["add", "README.md"])?;
    h.reference_git(&repo, &["commit", "-m", "seed"])?;
    let head = h.reference_git(&repo, &["rev-parse", "HEAD"])?.stdout;
    Ok((repo, head))
}

fn case_init(h: &Harness, _full: bool) -> Result<String> {
    let repo = h.repo("init-case");
    h.client_ok(
        &h.root,
        &["init", "-b", "main", repo.to_str().context("utf8")?],
    )?;
    ensure!(repo.join(".git").exists());
    Ok("bare init layout".into())
}

fn case_clone(h: &Harness, _full: bool) -> Result<String> {
    let (source, head) = seed_repo(h, "clone-src")?;
    let dest = h.repo("clone-dst");
    h.client_ok(
        &h.root,
        &[
            "clone",
            source.to_str().context("utf8")?,
            dest.to_str().context("utf8")?,
        ],
    )?;
    let got = h.client_ok(&dest, &["rev-parse", "HEAD"])?;
    ensure!(got == head);
    Ok("local clone".into())
}

fn case_add(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "add-case")?;
    write_file(&repo.join("extra.txt"), b"x\n")?;
    h.client_ok(&repo, &["add", "extra.txt"])?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    Ok("path add".into())
}

fn case_commit(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "commit-case")?;
    write_file(&repo.join("c.txt"), b"c\n")?;
    h.client_ok(&repo, &["add", "c.txt"])?;
    h.client_ok(&repo, &["commit", "-m", "second"])?;
    h.compare_stdout(&repo, &["log", "--oneline", "-1"])?;
    Ok("commit -m".into())
}

fn case_status(h: &Harness, full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "status-case")?;
    write_file(&repo.join("dirty.txt"), b"d\n")?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    if full {
        h.compare_stdout(&repo, &["status", "-sb"])?;
    }
    Ok("porcelain v1".into())
}

fn case_diff(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "diff-case")?;
    write_file(&repo.join("README.md"), b"changed\n")?;
    h.compare_stdout(&repo, &["diff", "--no-ext-diff"])?;
    Ok("worktree diff".into())
}

fn case_show(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "show-case")?;
    h.compare_stdout(&repo, &["show", "--no-patch", "HEAD"])?;
    Ok("show header".into())
}

fn case_log(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "log-case")?;
    h.compare_stdout(&repo, &["log", "--oneline", "-1"])?;
    Ok("oneline".into())
}

fn case_branch(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "branch-case")?;
    h.client_ok(&repo, &["branch", "topic"])?;
    let listed = h.client_ok(&repo, &["branch"])?;
    ensure!(String::from_utf8_lossy(&listed).contains("topic"));
    Ok("create + list".into())
}

fn case_switch(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "switch-case")?;
    h.client_ok(&repo, &["branch", "topic"])?;
    h.client_ok(&repo, &["switch", "topic"])?;
    let branches = h.client_ok(&repo, &["branch"])?;
    let text = String::from_utf8_lossy(&branches);
    ensure!(text.contains("* topic") || text.contains("*\ttopic"));
    Ok("switch branch".into())
}

fn case_checkout(h: &Harness, _full: bool) -> Result<String> {
    let (repo, head) = seed_repo(h, "checkout-case")?;
    h.client_ok(&repo, &["checkout", "--detach", &bytes_str(&head)?])?;
    Ok("detach".into())
}

fn case_merge(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "merge-case")?;
    h.client_ok(&repo, &["branch", "side"])?;
    h.client_ok(&repo, &["switch", "side"])?;
    write_file(&repo.join("m.txt"), b"m\n")?;
    h.reference_git(&repo, &["add", "m.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "side"])?;
    h.client_ok(&repo, &["switch", "main"])?;
    h.client_ok(&repo, &["merge", "--ff-only", "side"])?;
    Ok("ff merge".into())
}

fn case_fetch(h: &Harness, _full: bool) -> Result<String> {
    let bare = h.repo("fetch-bare");
    std::fs::create_dir_all(&bare)?;
    h.reference_git(&bare, &["init", "--bare", "."])?;
    let (repo, _) = seed_repo(h, "fetch-wt")?;
    h.client_ok(
        &repo,
        &["remote", "add", "origin", bare.to_str().context("utf8")?],
    )?;
    h.client_ok(&repo, &["fetch", "origin"])?;
    Ok("fetch origin".into())
}

fn case_pull(h: &Harness, _full: bool) -> Result<String> {
    let bare = h.repo("pull-bare");
    std::fs::create_dir_all(&bare)?;
    h.reference_git(&bare, &["init", "--bare", "."])?;
    let (repo, _) = seed_repo(h, "pull-wt")?;
    h.client_ok(
        &repo,
        &["remote", "add", "origin", bare.to_str().context("utf8")?],
    )?;
    h.client_ok(&repo, &["push", "-u", "origin", "main"])?;
    h.client_ok(&repo, &["pull", "--ff-only", "origin"])?;
    Ok("ff-only pull".into())
}

fn case_push(h: &Harness, _full: bool) -> Result<String> {
    let bare = h.repo("push-bare");
    std::fs::create_dir_all(&bare)?;
    h.reference_git(&bare, &["init", "--bare", "."])?;
    let (repo, _) = seed_repo(h, "push-wt")?;
    h.client_ok(
        &repo,
        &["remote", "add", "origin", bare.to_str().context("utf8")?],
    )?;
    h.client_ok(&repo, &["push", "-u", "origin", "main"])?;
    Ok("push main".into())
}

fn case_remote(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "remote-case")?;
    h.client_ok(
        &repo,
        &[
            "remote",
            "add",
            "origin",
            "https://example.invalid/repo.git",
        ],
    )?;
    h.client_ok(&repo, &["remote", "get-url", "origin"])?;
    Ok("add + get-url".into())
}

fn case_tag(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "tag-case")?;
    h.client_ok(&repo, &["tag", "light"])?;
    h.client_ok(&repo, &["tag", "-a", "annotated", "-m", "note"])?;
    let tags = h.client_ok(&repo, &["tag"])?;
    ensure!(tags.windows(b"annotated".len()).any(|w| w == b"annotated"));
    Ok("light + annotated".into())
}

fn case_stash(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "stash-case")?;
    write_file(&repo.join("README.md"), b"wip\n")?;
    h.client_ok(&repo, &["stash", "push", "-m", "wip"])?;
    h.client_ok(&repo, &["stash", "pop"])?;
    Ok("push pop".into())
}

fn case_cherry_pick(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "pick-case")?;
    h.client_ok(&repo, &["switch", "-c", "feature"])?;
    write_file(&repo.join("pick.txt"), b"p\n")?;
    h.reference_git(&repo, &["add", "pick.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "feature"])?;
    let feature = h.reference_git(&repo, &["rev-parse", "HEAD"])?.stdout;
    h.client_ok(&repo, &["switch", "main"])?;
    h.client_ok(&repo, &["cherry-pick", &bytes_str(&feature)?])?;
    ensure!(repo.join("pick.txt").exists());
    Ok("linear pick".into())
}

fn case_revert(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "revert-case")?;
    write_file(&repo.join("r.txt"), b"r\n")?;
    h.reference_git(&repo, &["add", "r.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "add r"])?;
    h.client_ok(&repo, &["revert", "HEAD"])?;
    ensure!(!repo.join("r.txt").exists());
    Ok("revert HEAD".into())
}

fn case_rebase(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "rebase-case")?;
    let main_tip = h.reference_git(&repo, &["rev-parse", "HEAD"])?.stdout;
    h.client_ok(&repo, &["switch", "-c", "topic"])?;
    write_file(&repo.join("rebase.txt"), b"rb\n")?;
    h.reference_git(&repo, &["add", "rebase.txt"])?;
    h.reference_git(&repo, &["commit", "-m", "topic commit"])?;
    h.client_ok(&repo, &["switch", "main"])?;
    h.client_ok(
        &repo,
        &[
            "rebase",
            "--onto",
            &bytes_str(&main_tip)?,
            &bytes_str(&main_tip)?,
            "topic",
        ],
    )?;
    ensure!(repo.join("rebase.txt").exists());
    Ok("rebase --onto".into())
}

fn case_submodule(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "sub-parent")?;
    let sub = h.repo("sub-upstream");
    h.init_fixture_repo(&sub, "main")?;
    write_file(&sub.join("sub.txt"), b"sub\n")?;
    h.reference_git(&sub, &["add", "sub.txt"])?;
    h.reference_git(&sub, &["commit", "-m", "sub"])?;
    let path = sub.canonicalize()?;
    let normalized = path.display().to_string().replace('\\', "/");
    let url = if cfg!(windows) {
        format!("file:///{normalized}")
    } else {
        format!("file://{normalized}")
    };
    write_file(
        &repo.join(".gitmodules"),
        format!("[submodule \"child\"]\n\tpath = child\n\turl = \"{url}\"\n").as_bytes(),
    )?;
    h.reference_git(&repo, &["add", ".gitmodules"])?;
    h.reference_git(&repo, &["commit", "-m", "add submodule meta"])?;
    h.client_ok(&repo, &["submodule", "init"])?;
    h.client_ok(&repo, &["submodule", "update"])?;
    ensure!(repo.join("child/sub.txt").exists());
    h.client_ok(&repo, &["submodule", "status"])?;
    Ok("init update status".into())
}

fn case_worktree(h: &Harness, _full: bool) -> Result<String> {
    let (repo, head) = seed_repo(h, "wt-case")?;
    let linked = h.repo("wt-linked");
    h.client_ok(
        &repo,
        &[
            "worktree",
            "add",
            "--detach",
            linked.to_str().context("utf8")?,
            &bytes_str(&head)?,
        ],
    )?;
    let list = h.client_ok(&repo, &["worktree", "list"])?;
    ensure!(String::from_utf8_lossy(&list).contains("wt-linked"));
    h.client_ok(
        &repo,
        &[
            "worktree",
            "remove",
            "--force",
            linked.to_str().context("utf8")?,
        ],
    )?;
    Ok("add list remove".into())
}

fn case_restore(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "restore-case")?;
    write_file(&repo.join("README.md"), b"dirty\n")?;
    h.client_ok(&repo, &["restore", "README.md"])?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    Ok("restore worktree".into())
}

fn case_reset(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "reset-case")?;
    write_file(&repo.join("README.md"), b"dirty\n")?;
    h.client_ok(&repo, &["add", "README.md"])?;
    h.client_ok(&repo, &["reset", "--hard", "HEAD"])?;
    h.compare_stdout(&repo, &["status", "--porcelain"])?;
    Ok("hard reset".into())
}

fn case_rev_parse(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "revparse-case")?;
    h.compare_canonical_path_stdout(&repo, &["rev-parse", "--show-toplevel"])?;
    h.compare_stdout(&repo, &["rev-parse", "--git-dir"])?;
    h.compare_stdout(&repo, &["rev-parse", "--is-bare-repository"])?;
    Ok("toplevel git-dir bare".into())
}

fn case_cat_file(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "catfile-case")?;
    h.compare_stdout(&repo, &["cat-file", "-p", "HEAD:README.md"])?;
    Ok("blob peel".into())
}

fn case_hash_object(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "hash-case")?;
    h.compare_stdout(&repo, &["hash-object", "README.md"])?;
    Ok("hash file".into())
}

fn case_ls_files(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "lsfiles-case")?;
    h.compare_stdout(&repo, &["ls-files"])?;
    h.compare_stdout(&repo, &["ls-files", "--stage"])?;
    Ok("list + stage".into())
}

fn case_show_ref(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "showref-case")?;
    h.compare_stdout(&repo, &["show-ref"])?;
    h.compare_stdout(&repo, &["show-ref", "--head"])?;
    Ok("refs".into())
}

fn case_ls_remote(h: &Harness, _full: bool) -> Result<String> {
    let (bare, _) = seed_repo(h, "lsremote-bare")?;
    h.compare_stdout(&h.root, &["ls-remote", bare.to_str().context("utf8")?])?;
    Ok("local ls-remote".into())
}

fn case_config(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "config-case")?;
    h.client_ok(&repo, &["config", "user.name", "Acceptance"])?;
    h.compare_stdout(&repo, &["config", "user.name"])?;
    Ok("set + get".into())
}

fn case_lfs(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "lfs-case")?;
    h.client_ok(&repo, &["lfs", "version"])?;
    Ok("lfs version".into())
}

fn case_eol(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "eol-case")?;
    write_file(&repo.join(".gitattributes"), b"*.txt text eol=lf\n")?;
    h.client_ok(&repo, &["add", ".gitattributes"])?;
    h.client_ok(&repo, &["commit", "-m", "attrs"])?;
    h.client_ok(&repo, &["eol", "--apply"])?;
    Ok("eol check".into())
}

fn case_language(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "lang-case")?;
    h.client_ok(&repo, &["language", "list"])?;
    h.client_ok(&repo, &["language", "get"])?;
    Ok("list + get".into())
}

fn case_compatibility(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "compat-case")?;
    let out = h.client_ok(&repo, &["compatibility", "--json"])?;
    ensure!(
        out.windows(b"git-next".len())
            .any(|window| window == b"git-next")
    );
    Ok("json contract".into())
}

fn case_version(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "version-case")?;
    let out = h.client_ok(&repo, &["version", "--json"])?;
    ensure!(out.windows(b"rust".len()).any(|window| window == b"rust"));
    Ok("json version".into())
}

fn case_doctor(h: &Harness, _full: bool) -> Result<String> {
    let (repo, _) = seed_repo(h, "doctor-case")?;
    let out = h.client_ok(&repo, &["doctor", "--json"])?;
    ensure!(
        out.windows(b"replacement_ready".len())
            .any(|w| w == b"replacement_ready")
    );
    Ok("doctor json".into())
}

fn case_install(h: &Harness, _full: bool) -> Result<String> {
    let output = h.client_git(&h.root, &["install", "--help"])?;
    ensure!(output.status.success());
    Ok("install help".into())
}
