//! Developer disk maintenance (temp + Rust target junk). Does not delete source trees.
use anyhow::{Context, Result};
use clap::Parser;
use std::{
    path::{Path, PathBuf},
    time::SystemTime,
};

#[derive(Parser, Debug)]
#[command(name = "git-next-maint")]
struct Args {
    #[arg(long)]
    apply: bool,
    #[arg(long, default_value_t = 7)]
    temp_age_days: u64,
    #[arg(long)]
    aggressive_temp_root: bool,
    #[arg(long)]
    report_temp_top: u32,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let mut bytes = 0u64;
    let mut actions = Vec::new();

    if args.report_temp_top > 0 {
        let temp = std::env::temp_dir();
        println!("Top temp folders under {}", temp.display());
        let mut sizes = Vec::new();
        for entry in std::fs::read_dir(&temp)? {
            let entry = entry?;
            if !entry.file_type()?.is_dir() {
                continue;
            }
            let size = dir_size(&entry.path())?;
            sizes.push((entry.file_name().to_string_lossy().to_string(), size));
        }
        sizes.sort_by_key(|a| std::cmp::Reverse(a.1));
        for (name, size) in sizes.into_iter().take(args.report_temp_top as usize) {
            println!(
                "  {:>8.2} GB  {name}",
                size as f64 / 1024.0 / 1024.0 / 1024.0
            );
        }
    }

    let cutoff = SystemTime::now()
        .checked_sub(std::time::Duration::from_secs(args.temp_age_days * 86400))
        .context("time")?;
    for temp in temp_roots() {
        if !temp.is_dir() {
            continue;
        }
        for entry in std::fs::read_dir(&temp)? {
            let entry = entry?;
            let name = entry.file_name().to_string_lossy().to_string();
            let meta = entry.metadata()?;
            let old = meta.modified().ok().is_some_and(|time| time < cutoff);
            let git_next = name.starts_with("git-next-");
            let aggressive = args.aggressive_temp_root
                && old
                && meta.is_dir()
                && !name.starts_with("Low")
                && !name.starts_with("ms-")
                && !name.starts_with("VS")
                && !name.starts_with("docker")
                && !name.starts_with("wsl");
            if git_next || aggressive {
                let path = entry.path();
                let size = dir_size(&path)?;
                bytes += size;
                let path_buf = path.clone();
                actions.push(path_buf.clone());
                if args.apply {
                    let _ = remove_dir_all(&path_buf);
                }
            }
        }
    }

    for path in rust_cleanup_targets() {
        if path.is_dir() {
            bytes += dir_size(&path)?;
            actions.push(path.clone());
            if args.apply {
                let _ = remove_dir_all(&path);
            }
        }
    }

    let mode = if args.apply { "APPLY" } else { "DRY-RUN" };
    println!(
        "\n{mode}: {} paths, {:.2} GB",
        actions.len(),
        bytes as f64 / 1024.0 / 1024.0 / 1024.0
    );
    if !args.apply {
        println!("Re-run with --apply to delete.");
    }
    Ok(())
}

fn temp_roots() -> Vec<PathBuf> {
    let mut roots = vec![std::env::temp_dir()];
    if let Ok(local) = std::env::var("LOCALAPPDATA") {
        roots.push(PathBuf::from(local).join("Temp"));
    }
    roots.sort();
    roots.dedup();
    roots
}

fn dev_workspace_root() -> PathBuf {
    if let Ok(root) = std::env::var("GITSSS_DEV_ROOT") {
        return PathBuf::from(root);
    }
    let mut dir = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
    for _ in 0..8 {
        if dir.join("git-next").join("Cargo.toml").is_file() {
            return dir;
        }
        if !dir.pop() {
            break;
        }
    }
    PathBuf::from(".")
}

fn rust_cleanup_targets() -> Vec<PathBuf> {
    let base = dev_workspace_root();
    [
        base.join("git-next/target/debug"),
        base.join("git-next/target/release/incremental"),
        base.join("git-next/target/vendor-status-tests"),
        base.join("git-terminal/target/debug"),
        base.join("git-terminal/target/release/incremental"),
    ]
    .into_iter()
    .filter(|path| path.is_dir() || path.parent().is_some_and(|p| p.exists()))
    .collect()
}

fn dir_size(path: &Path) -> Result<u64> {
    let mut total = 0u64;
    if path.is_file() {
        return Ok(path.metadata()?.len());
    }
    for entry in walkdir::WalkDir::new(path)
        .into_iter()
        .filter_map(|entry| entry.ok())
    {
        if entry.file_type().is_file() {
            total += entry.metadata()?.len();
        }
    }
    Ok(total)
}

fn remove_dir_all(path: &Path) -> Result<()> {
    if path.is_dir() {
        std::fs::remove_dir_all(path)?;
    }
    Ok(())
}
