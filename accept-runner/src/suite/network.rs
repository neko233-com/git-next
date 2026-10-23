//! L2 loopback network (git daemon). Reference Git runs the daemon; client uses empty PATH.
use crate::harness::Harness;
use crate::outcome::{CaseOutcome, run_case, skipped};
use crate::process::Process;
use crate::suite::fixture::write_file;
use anyhow::{Context, Result, bail, ensure};
use std::{net::TcpListener, path::Path, process::Stdio, time::Duration};

const SUITE: &str = "network";

struct GitDaemon {
    _child: Process,
    port: u16,
}

impl GitDaemon {
    fn start(h: &Harness, base: &Path) -> Result<Self> {
        let listener = TcpListener::bind("127.0.0.1:0").context("bind daemon port")?;
        let port = listener.local_addr()?.port();
        drop(listener);
        let mut command = h.reference_command(base);
        command
            .args([
                "daemon",
                "--reuseaddr",
                &format!("--base-path={}", base.display()),
                "--export-all",
                "--listen=127.0.0.1",
                &format!("--port={port}"),
            ])
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        let mut child = Process::spawn(&mut command).context("spawn git daemon")?;
        for _ in 0..30 {
            if std::net::TcpStream::connect(("127.0.0.1", port)).is_ok() {
                break;
            }
            if child.try_wait()?.is_some() {
                bail!("git daemon exited before listening on port {port}");
            }
            std::thread::sleep(Duration::from_millis(100));
        }
        ensure!(
            std::net::TcpStream::connect(("127.0.0.1", port)).is_ok(),
            "git daemon did not accept connections on 127.0.0.1:{port}"
        );
        Ok(Self {
            _child: child,
            port,
        })
    }

    fn url(&self, name: &str) -> String {
        format!("git://127.0.0.1:{}/{name}", self.port)
    }
}

pub fn run(h: &Harness) -> Vec<CaseOutcome> {
    if std::env::var("GIT_NEXT_ACCEPT_SKIP_NETWORK")
        .ok()
        .as_deref()
        == Some("1")
    {
        return vec![skipped(
            SUITE,
            "network-tier",
            "GIT_NEXT_ACCEPT_SKIP_NETWORK=1".into(),
        )];
    }
    let mut out = vec![
        run_case(SUITE, "git-daemon-clone", case_git_daemon_clone(h)),
        run_case(
            SUITE,
            "git-daemon-fetch-pull",
            case_git_daemon_fetch_pull(h),
        ),
    ];
    if std::env::var("GIT_NEXT_ACCEPT_PUBLIC_HTTPS")
        .ok()
        .as_deref()
        == Some("1")
    {
        out.push(run_case(
            SUITE,
            "public-https-ls-remote",
            case_public_https(h),
        ));
    } else {
        out.push(skipped(
            SUITE,
            "public-https-ls-remote",
            "set GIT_NEXT_ACCEPT_PUBLIC_HTTPS=1 to enable".into(),
        ));
    }
    out
}

fn case_git_daemon_clone(h: &Harness) -> Result<String> {
    let daemon_root = h.repo("daemon-root");
    std::fs::create_dir_all(&daemon_root)?;
    let (_bare, bare_name) = {
        let source = h.repo("daemon-clone-src");
        h.init_fixture_repo(&source, "main")?;
        write_file(&source.join("README.md"), b"daemon\n")?;
        h.reference_git(&source, &["add", "README.md"])?;
        h.reference_git(&source, &["commit", "-m", "daemon"])?;
        let bare_name = "daemon-clone.git";
        let bare = daemon_root.join(bare_name);
        h.reference_git(&h.root, &["init", "--bare", bare.to_str().unwrap()])?;
        h.reference_git(&source, &["push", bare.to_str().unwrap(), "main"])?;
        (bare, bare_name.to_string())
    };
    let daemon = GitDaemon::start(h, &daemon_root)?;
    let url = daemon.url(&bare_name);
    let head = h
        .reference_git(&daemon_root.join(&bare_name), &["rev-parse", "HEAD"])?
        .stdout;
    let target = h.repo("daemon-clone-dst");
    h.client_ok(&h.root, &["clone", &url, target.to_str().unwrap()])?;
    ensure!(h.client_ok(&target, &["rev-parse", "HEAD"])? == head);
    ensure!(h.client_ok(&target, &["status", "--porcelain"])?.is_empty());
    Ok("git:// clone".into())
}

fn case_git_daemon_fetch_pull(h: &Harness) -> Result<String> {
    let daemon_root = h.repo("daemon-fetch-root");
    std::fs::create_dir_all(&daemon_root)?;
    let source = h.repo("daemon-fetch-src");
    h.init_fixture_repo(&source, "main")?;
    write_file(&source.join("README.md"), b"v1\n")?;
    h.reference_git(&source, &["add", "."])?;
    h.reference_git(&source, &["commit", "-m", "v1"])?;
    let bare_name = "daemon-fetch.git";
    let bare = daemon_root.join(bare_name);
    h.reference_git(&h.root, &["init", "--bare", bare.to_str().unwrap()])?;
    h.reference_git(&source, &["push", bare.to_str().unwrap(), "main"])?;
    let daemon = GitDaemon::start(h, &daemon_root)?;
    let url = daemon.url(bare_name);
    let clone = h.repo("daemon-fetch-clone");
    h.client_ok(&h.root, &["clone", &url, clone.to_str().unwrap()])?;
    write_file(&source.join("README.md"), b"v2\n")?;
    h.reference_git(&source, &["add", "."])?;
    h.reference_git(&source, &["commit", "-m", "v2"])?;
    h.reference_git(&source, &["push", bare.to_str().unwrap(), "main"])?;
    h.client_ok(&clone, &["fetch", "origin"])?;
    h.client_ok(&clone, &["pull", "--ff-only"])?;
    ensure!(std::fs::read_to_string(clone.join("README.md"))?.contains("v2"));
    Ok("git:// fetch + pull".into())
}

fn case_public_https(h: &Harness) -> Result<String> {
    let url = "https://github.com/octocat/Hello-World.git";
    h.compare_stdout(&h.root, &["ls-remote", url])?;
    Ok("public HTTPS ls-remote".into())
}
