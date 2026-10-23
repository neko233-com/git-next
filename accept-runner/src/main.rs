mod cli39;
mod harness;
mod outcome;
mod process;
mod suite;

use anyhow::Result;
use clap::{Parser, ValueEnum};
use harness::Harness;
use outcome::CaseOutcome;
use serde::Serialize;
use std::path::PathBuf;
use suite::Tier;

#[derive(Clone, Copy, Debug, Default, ValueEnum, Serialize)]
#[value(rename_all = "kebab-case")]
#[serde(rename_all = "kebab-case")]
enum TierArg {
    #[default]
    L0,
    Full,
    Network,
}

impl From<TierArg> for Tier {
    fn from(value: TierArg) -> Self {
        match value {
            TierArg::L0 => Tier::L0,
            TierArg::Full => Tier::Full,
            TierArg::Network => Tier::Network,
        }
    }
}

#[derive(Parser, Debug)]
#[command(
    name = "git-next-accept",
    about = "Rust-native git-next CLI acceptance (primary Mac/Windows gate)"
)]
struct Args {
    /// Path to git-next client (git.exe).
    client: PathBuf,
    /// Acceptance tier: l0 (39 commands), full (+ local deep/broad), network (+ loopback git daemon).
    #[arg(long, value_enum, default_value_t = TierArg::L0)]
    tier: TierArg,
    /// Shorthand for `--tier full`.
    #[arg(long, conflicts_with_all = ["tier", "network"])]
    full: bool,
    /// Shorthand for `--tier network`.
    #[arg(long, conflicts_with = "tier")]
    network: bool,
    #[arg(long)]
    json: Option<PathBuf>,
}

#[derive(Serialize)]
struct Report {
    client: String,
    tier: String,
    passed: usize,
    failed: usize,
    skipped: usize,
    cases: usize,
    results: Vec<CaseOutcome>,
}

fn resolved_tier(args: &Args) -> Tier {
    if args.network {
        Tier::Network
    } else if args.full {
        Tier::Full
    } else {
        args.tier.into()
    }
}

fn tier_label(tier: Tier) -> &'static str {
    match tier {
        Tier::L0 => "l0",
        Tier::Full => "full",
        Tier::Network => "network",
    }
}

fn main() -> Result<std::process::ExitCode> {
    let args = Args::parse();
    let tier = resolved_tier(&args);
    let l0_full = tier != Tier::L0;
    let harness = Harness::new(args.client)?;
    let results = suite::run_tier(&harness, tier, l0_full);
    let passed = results
        .iter()
        .filter(|item| item.ok && !item.skipped)
        .count();
    let skipped = results.iter().filter(|item| item.skipped).count();
    let failed = results.iter().filter(|item| !item.ok).count();
    for item in &results {
        let mark = if item.skipped {
            "skip"
        } else if item.ok {
            "ok"
        } else {
            "FAIL"
        };
        println!("[{mark}] {}::{} — {}", item.suite, item.id, item.detail);
    }
    println!(
        "\ntier {}: {} passed, {} failed, {} skipped (reference Git fixtures only)",
        tier_label(tier),
        passed,
        failed,
        skipped
    );
    if let Some(path) = args.json {
        let report = Report {
            client: harness.client.display().to_string(),
            tier: tier_label(tier).into(),
            passed,
            failed,
            skipped,
            cases: results.len(),
            results,
        };
        if let Some(parent) = path.parent().filter(|p| !p.as_os_str().is_empty()) {
            std::fs::create_dir_all(parent)?;
        }
        std::fs::write(&path, serde_json::to_vec_pretty(&report)?)?;
    }
    Ok(if failed > 0 {
        std::process::ExitCode::FAILURE
    } else {
        std::process::ExitCode::SUCCESS
    })
}
