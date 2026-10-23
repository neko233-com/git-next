//! Verify an installed git-next without trusting the installer that installed it.
//!
//! An installation is a directory plus a manifest that says what should be in it.
//! Verifying it therefore does not need the Windows Installer, PowerShell, a
//! scheduled task or an elevated shell: the manifest is read, the files are checked
//! against it, and the result is reported as data an agent can act on.
//!
//! The check takes no locks. It only reads, so it can run while Git is in use, while
//! another verification runs, and while the updater is working - a verification that
//! needs an exclusive lock would be useless exactly when it is needed most.

use anyhow::{Context, Result, bail};
use serde_json::{Map, Value, json};
use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};

#[derive(clap::Args)]
pub(crate) struct Options {
    #[command(subcommand)]
    action: Action,
}

#[derive(clap::Subcommand)]
enum Action {
    /// Check an installation against its manifest and report what is wrong.
    Verify {
        /// Installation root; defaults to the tree this executable belongs to.
        #[arg(long)]
        root: Option<PathBuf>,
        /// Hash every file instead of a deterministic sample.
        #[arg(long)]
        hash_all: bool,
        /// How many files to hash when --hash-all is not given.
        #[arg(long, default_value_t = 128)]
        sample: usize,
        /// Write the report as JSON.
        #[arg(long)]
        json: bool,
    },
}

pub(crate) fn run(options: Options) -> Result<()> {
    match options.action {
        Action::Verify {
            root,
            hash_all,
            sample,
            json,
        } => verify(root, hash_all, sample, json),
    }
}

/// The installation root this executable belongs to.
///
/// A `git` installed by the package lives in `<root>\cmd` or `<root>\bin`, so the
/// parent of the executable's directory is the root. Deriving it from the running
/// binary means the command checks the installation that is actually being used, not
/// one that happens to be registered somewhere.
fn install_root(explicit: Option<PathBuf>) -> Result<PathBuf> {
    if let Some(root) = explicit {
        return Ok(root);
    }
    let executable = std::env::current_exe().context("cannot locate the running executable")?;
    let directory = executable
        .parent()
        .context("the executable has no parent directory")?;
    if !matches!(
        directory.file_name().and_then(|name| name.to_str()),
        Some("cmd" | "bin" | "mingw64" | "usr")
    ) {
        bail!(
            "{} is not inside an installed git-next tree; pass --root",
            executable.display()
        );
    }
    Ok(directory
        .parent()
        .context("the executable is not two levels below an installation root")?
        .to_path_buf())
}

fn sha256(path: &Path) -> Result<String> {
    let bytes = std::fs::read(path).with_context(|| format!("cannot read {}", path.display()))?;
    Ok(hex(&sha256_digest(&bytes)))
}

fn hex(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push_str(&format!("{byte:02x}"));
    }
    out
}

/// SHA-256 of a byte slice.
///
/// The verifier is the only user, so a compact implementation is preferable to a new
/// dependency: it keeps the verification path independent of the runtime, which is
/// the property that makes it useful when the runtime is what is broken.
fn sha256_digest(input: &[u8]) -> [u8; 32] {
    const K: [u32; 64] = [
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4,
        0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe,
        0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f,
        0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
        0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc,
        0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
        0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116,
        0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
        0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7,
        0xc67178f2,
    ];
    let mut h: [u32; 8] = [
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab,
        0x5be0cd19,
    ];
    let mut message = input.to_vec();
    let bit_length = (input.len() as u64) * 8;
    message.push(0x80);
    while message.len() % 64 != 56 {
        message.push(0);
    }
    message.extend_from_slice(&bit_length.to_be_bytes());

    for chunk in message.as_chunks::<64>().0 {
        let mut w = [0u32; 64];
        for (index, word) in chunk.as_chunks::<4>().0.iter().enumerate() {
            w[index] = u32::from_be_bytes([word[0], word[1], word[2], word[3]]);
        }
        for index in 16..64 {
            let s0 = w[index - 15].rotate_right(7)
                ^ w[index - 15].rotate_right(18)
                ^ (w[index - 15] >> 3);
            let s1 = w[index - 2].rotate_right(17)
                ^ w[index - 2].rotate_right(19)
                ^ (w[index - 2] >> 10);
            w[index] = w[index - 16]
                .wrapping_add(s0)
                .wrapping_add(w[index - 7])
                .wrapping_add(s1);
        }
        let (mut a, mut b, mut c, mut d, mut e, mut f, mut g, mut hh) =
            (h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]);
        for index in 0..64 {
            let s1 = e.rotate_right(6) ^ e.rotate_right(11) ^ e.rotate_right(25);
            let ch = (e & f) ^ ((!e) & g);
            let temp1 = hh
                .wrapping_add(s1)
                .wrapping_add(ch)
                .wrapping_add(K[index])
                .wrapping_add(w[index]);
            let s0 = a.rotate_right(2) ^ a.rotate_right(13) ^ a.rotate_right(22);
            let maj = (a & b) ^ (a & c) ^ (b & c);
            let temp2 = s0.wrapping_add(maj);
            hh = g;
            g = f;
            f = e;
            e = d.wrapping_add(temp1);
            d = c;
            c = b;
            b = a;
            a = temp1.wrapping_add(temp2);
        }
        h[0] = h[0].wrapping_add(a);
        h[1] = h[1].wrapping_add(b);
        h[2] = h[2].wrapping_add(c);
        h[3] = h[3].wrapping_add(d);
        h[4] = h[4].wrapping_add(e);
        h[5] = h[5].wrapping_add(f);
        h[6] = h[6].wrapping_add(g);
        h[7] = h[7].wrapping_add(hh);
    }

    let mut digest = [0u8; 32];
    for (index, word) in h.iter().enumerate() {
        digest[index * 4..index * 4 + 4].copy_from_slice(&word.to_be_bytes());
    }
    digest
}

/// One manifest entry: the file name, its size when the manifest records one, and its
/// digest. An installed manifest maps a name to the digest alone, because the installed
/// size is whatever the file system reports; a distribution manifest also records the
/// expected size. Both are accepted so one verifier serves an installed tree and a
/// package payload.
struct Entry {
    name: String,
    bytes: Option<u64>,
    sha256: String,
}

fn entries(manifest: &Value) -> Vec<Entry> {
    let Some(files) = manifest.get("files").and_then(Value::as_object) else {
        return Vec::new();
    };
    files
        .iter()
        .filter_map(|(name, value)| {
            let digest = match value {
                Value::String(digest) => digest.clone(),
                Value::Object(fields) => fields.get("sha256")?.as_str()?.to_owned(),
                _ => return None,
            };
            Some(Entry {
                name: name.clone(),
                bytes: value.get("bytes").and_then(Value::as_u64),
                sha256: digest,
            })
        })
        .collect()
}

/// Check one installation and return the report.
fn verify(root: Option<PathBuf>, hash_all: bool, sample: usize, json_output: bool) -> Result<()> {
    let root = install_root(root)?;
    let manifest_path = root.join("package.json");
    let mut checks: Vec<Value> = Vec::new();
    let mut problems: Vec<String> = Vec::new();
    let mut next_steps: Vec<String> = Vec::new();

    if !manifest_path.is_file() {
        problems.push(format!(
            "{} is missing; this directory is not a git-next installation",
            manifest_path.display()
        ));
        next_steps
            .push("reinstall the package, or pass --root with the installation directory".into());
        return finish(json_output, &root, None, checks, problems, next_steps);
    }

    let text = std::fs::read_to_string(&manifest_path)
        .with_context(|| format!("cannot read {}", manifest_path.display()))?;
    let manifest: Value = serde_json::from_str(&text)
        .with_context(|| format!("{} is not valid JSON", manifest_path.display()))?;

    let version = manifest
        .get("version")
        .and_then(Value::as_str)
        .unwrap_or("unknown")
        .to_owned();
    let platform = manifest
        .get("platform")
        .and_then(Value::as_str)
        .unwrap_or("unknown")
        .to_owned();

    // The manifest identity has to be a git-next manifest; anything else means the
    // directory was overwritten by something that does not belong here.
    let product_ok = manifest.get("product").and_then(Value::as_str) == Some("git-next");
    checks.push(json!({"check": "manifest_identity", "ok": product_ok,
        "detail": format!("product={:?}", manifest.get("product"))}));
    if !product_ok {
        problems.push("package.json does not describe a git-next installation".into());
        next_steps.push("restore the directory from the package that owns it".into());
    }

    // Entry points are what tools call; they are checked by size because a bridge
    // forwarder and a real payload are both named git.exe.
    for (relative, minimum) in [
        ("cmd/git.exe", 1_000_000u64),
        ("bin/git.exe", 1_000_000),
        ("updater/msi-worker.exe", 1),
    ] {
        let path = root.join(relative);
        let bytes = path.metadata().map(|meta| meta.len()).unwrap_or(0);
        let ok = bytes >= minimum;
        checks.push(json!({"check": format!("entry_point:{relative}"), "ok": ok, "detail": format!("{bytes} bytes")}));
        if !ok {
            problems.push(format!("{relative} is missing or not the real payload"));
        }
    }

    let entries = entries(&manifest);
    checks.push(json!({"check": "manifest_files", "ok": !entries.is_empty(),
        "detail": format!("{} entries", entries.len())}));

    // Every file must exist at the recorded size. Hashing is the expensive part, so a
    // deterministic sample is hashed unless the caller asks for everything: the sample
    // is spread across the sorted manifest rather than taken from the front, so a
    // truncated installation cannot hide behind an intact beginning.
    let mut missing = 0usize;
    let mut wrong_size = 0usize;
    let mut wrong_hash = 0usize;
    let mut checked = 0usize;
    let step = if hash_all || sample == 0 {
        1
    } else {
        (entries.len() / sample.max(1)).max(1)
    };
    for (index, entry) in entries.iter().enumerate() {
        let path = root.join(&entry.name);
        match path.metadata() {
            Ok(metadata) if entry.bytes.is_none_or(|bytes| metadata.len() == bytes) => {}
            Ok(_) => {
                wrong_size += 1;
                continue;
            }
            Err(_) => {
                missing += 1;
                continue;
            }
        }
        if hash_all
            || index % step == 0
            || entry.name.starts_with("cmd/")
            || entry.name.starts_with("bin/")
        {
            checked += 1;
            match sha256(&path) {
                Ok(digest) if digest == entry.sha256 => {}
                Ok(_) => wrong_hash += 1,
                Err(_) => wrong_hash += 1,
            }
        }
    }

    // An empty result is not a pass: a manifest that declares files must be readable by
    // this verifier, otherwise "nothing to check" would be reported as "nothing wrong".
    let files_ok = !entries.is_empty() && missing == 0 && wrong_size == 0 && wrong_hash == 0;
    checks.push(json!({"check": "files", "ok": files_ok, "detail": json!({
        "manifest_entries": entries.len(), "missing": missing,
        "wrong_size": wrong_size, "wrong_hash": wrong_hash, "hashed": checked,
    })}));
    if entries.is_empty() {
        problems.push("the manifest declares no verifiable files".into());
        next_steps.push(
            "the manifest format is not the one this build writes; check the installed version"
                .into(),
        );
    }
    if missing > 0 {
        problems.push(format!("{missing} files are missing"));
    }
    if wrong_size > 0 {
        problems.push(format!("{wrong_size} files have the wrong size"));
    }
    if wrong_hash > 0 {
        problems.push(format!(
            "{wrong_hash} files do not match their recorded digest"
        ));
    }
    if !files_ok {
        let code = manifest
            .get("product_code")
            .and_then(Value::as_str)
            .unwrap_or("<product code>");
        next_steps.push(format!("repair the installation: msiexec /famus {code}"));
        next_steps.push("if the repair cannot find its source, reinstall the package".into());
    }

    // The updater is an optional integration: its absence is a warning that has a
    // named remedy, not a broken installation.
    let task_present = updater_task(&platform);
    checks.push(json!({"check": "updater_task", "ok": task_present, "detail": platform}));
    if !task_present {
        next_steps.push(
            "the automatic updater is not registered; run install.ps1 -RepairUpdater or reinstall to restore it"
                .into(),
        );
    }

    finish(
        json_output,
        &root,
        Some((version, platform, manifest)),
        checks,
        problems,
        next_steps,
    )
}

/// Whether the scheduled task that runs the updater exists, by its definition file.
///
/// Reading the task's definition file avoids the Task Scheduler COM API, so the check
/// works from a normal shell and cannot be blocked by a missing service.
fn updater_task(platform: &str) -> bool {
    let directory = std::path::Path::new(r"C:\Windows\System32\Tasks");
    let prefix = format!("git-next-update-{platform}-");
    match std::fs::read_dir(directory) {
        Ok(entries) => entries.flatten().any(|entry| {
            entry
                .file_name()
                .to_str()
                .is_some_and(|name| name.starts_with(&prefix))
        }),
        Err(_) => false,
    }
}

fn finish(
    json_output: bool,
    root: &Path,
    identity: Option<(String, String, Value)>,
    checks: Vec<Value>,
    problems: Vec<String>,
    next_steps: Vec<String>,
) -> Result<()> {
    let ok = problems.is_empty();
    if json_output {
        let mut report = Map::new();
        report.insert("schema_version".into(), json!(1));
        report.insert("product".into(), json!("git-next"));
        report.insert("ok".into(), json!(ok));
        report.insert("install_root".into(), json!(root.display().to_string()));
        if let Some((version, platform, manifest)) = &identity {
            report.insert("version".into(), json!(version));
            report.insert("platform".into(), json!(platform));
            for key in [
                "install_scope",
                "install_kind",
                "product_code",
                "release_eligible",
                "complete_git_parity",
            ] {
                if let Some(value) = manifest.get(key) {
                    report.insert(key.into(), value.clone());
                }
            }
        }
        report.insert("checks".into(), json!(checks));
        report.insert("problems".into(), json!(problems));
        report.insert("next_steps".into(), json!(next_steps));
        println!("{}", serde_json::to_string_pretty(&Value::Object(report))?);
    } else {
        let mut lines = BTreeMap::new();
        if let Some((version, platform, _)) = &identity {
            lines.insert("version", version.clone());
            lines.insert("platform", platform.clone());
        }
        lines.insert("install root", root.display().to_string());
        for (key, value) in lines {
            println!("{key:<14} {value}");
        }
        for check in &checks {
            let name = check.get("check").and_then(Value::as_str).unwrap_or("?");
            let passed = check.get("ok").and_then(Value::as_bool).unwrap_or(false);
            println!("  {:<28} {}", name, if passed { "ok" } else { "PROBLEM" });
        }
        for problem in &problems {
            println!("problem: {problem}");
        }
        for step in &next_steps {
            println!("next:    {step}");
        }
        println!("{}", if ok { "PASS" } else { "FAIL" });
    }
    if !ok {
        std::process::exit(1);
    }
    Ok(())
}
