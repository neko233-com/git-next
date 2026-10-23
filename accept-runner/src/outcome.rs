use serde::Serialize;

pub type LocalCase = (
    &'static str,
    fn(&crate::harness::Harness) -> anyhow::Result<String>,
);

#[derive(Serialize, Clone)]
pub struct CaseOutcome {
    pub suite: &'static str,
    pub id: &'static str,
    pub ok: bool,
    pub skipped: bool,
    pub detail: String,
}

pub fn run_case(
    suite: &'static str,
    id: &'static str,
    result: Result<String, anyhow::Error>,
) -> CaseOutcome {
    match result {
        Ok(detail) => CaseOutcome {
            suite,
            id,
            ok: true,
            skipped: false,
            detail,
        },
        Err(error) => CaseOutcome {
            suite,
            id,
            ok: false,
            skipped: false,
            detail: error.to_string(),
        },
    }
}

pub fn skipped(suite: &'static str, id: &'static str, detail: String) -> CaseOutcome {
    CaseOutcome {
        suite,
        id,
        ok: true,
        skipped: true,
        detail,
    }
}
