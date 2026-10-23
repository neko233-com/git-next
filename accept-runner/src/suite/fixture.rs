use crate::harness::Harness;
use anyhow::Result;
use std::path::{Path, PathBuf};

pub fn write_file(path: &Path, bytes: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    std::fs::write(path, bytes)?;
    Ok(())
}

pub fn seed_repo(h: &Harness, name: &str) -> Result<PathBuf> {
    let repo = h.repo(name);
    h.init_fixture_repo(&repo, "main")?;
    write_file(&repo.join("README.md"), b"seed\n")?;
    h.reference_git(&repo, &["add", "README.md"])?;
    h.reference_git(&repo, &["commit", "-m", "seed"])?;
    Ok(repo)
}
