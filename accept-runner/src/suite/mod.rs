mod fixture;
mod local_broad;
mod local_conformance;
mod local_deep;
mod network;

use crate::harness::Harness;
use crate::outcome::CaseOutcome;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tier {
    L0,
    Full,
    Network,
}

pub fn run_tier(h: &Harness, tier: Tier, l0_full_matrix: bool) -> Vec<CaseOutcome> {
    let mut out = Vec::new();
    out.extend(crate::cli39::run_matrix(h, l0_full_matrix));
    if tier == Tier::Full || tier == Tier::Network {
        out.extend(local_deep::run(h));
        out.extend(local_broad::run(h));
        out.extend(local_conformance::run(h));
    }
    if tier == Tier::Network {
        out.extend(network::run(h));
    }
    out
}
