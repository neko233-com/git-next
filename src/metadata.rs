use anyhow::{Context, Result, ensure};
use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
};

pub(crate) fn read(repo: &gix::Repository, name: &str, limit: u64) -> Result<Option<Vec<u8>>> {
    let path = repo.git_dir().join(name);
    let metadata = match fs::symlink_metadata(&path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error.into()),
    };
    ensure!(
        metadata.is_file() && !metadata.file_type().is_symlink() && metadata.len() <= limit,
        "unsupported merge metadata file"
    );
    let mut bytes = Vec::new();
    fs::File::open(path)?
        .take(limit + 1)
        .read_to_end(&mut bytes)?;
    ensure!(
        bytes.len() as u64 <= limit,
        "merge metadata grew beyond the supported size"
    );
    Ok(Some(bytes))
}

pub(crate) struct Change {
    pub name: String,
    pub content: Option<Vec<u8>>,
}

pub(crate) struct Lock {
    name: String,
    target: PathBuf,
    path: PathBuf,
    file: Option<fs::File>,
    original: Option<Vec<u8>>,
    content: Option<Vec<u8>>,
    active: bool,
}

impl Lock {
    pub(crate) fn acquire(repo: &gix::Repository, change: Change) -> Result<Self> {
        ensure!(
            matches!(
                change.name.as_str(),
                "MERGE_HEAD" | "MERGE_MSG" | "MERGE_MODE" | "git-next-merge.json"
            ),
            "unsupported merge metadata path"
        );
        ensure!(
            change
                .content
                .as_ref()
                .is_none_or(|bytes| bytes.len() <= 16 * 1024 * 1024),
            "merge metadata exceeds the supported size"
        );
        let target = repo.git_dir().join(&change.name);
        let path = repo.git_dir().join(format!("{}.lock", change.name));
        let file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)
            .context("merge metadata is locked by another writer")?;
        let mut lock = Self {
            name: change.name,
            target,
            path,
            file: Some(file),
            original: None,
            content: change.content,
            active: true,
        };
        lock.original = match fs::symlink_metadata(&lock.target) {
            Ok(metadata) => {
                ensure!(
                    metadata.is_file() && !metadata.file_type().is_symlink(),
                    "merge metadata is not a regular file"
                );
                ensure!(
                    !metadata.permissions().readonly(),
                    "merge metadata is read-only"
                );
                ensure!(
                    metadata.len() <= 16 * 1024 * 1024,
                    "merge metadata exceeds the supported size"
                );
                lock.file
                    .as_ref()
                    .context("metadata lock closed")?
                    .set_permissions(metadata.permissions())?;
                Some(fs::read(&lock.target)?)
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => return Err(error.into()),
        };
        if let Some(content) = &lock.content {
            let file = lock.file.as_mut().context("metadata lock closed")?;
            file.write_all(content)?;
            file.flush()?;
            file.sync_all()?;
        }
        Ok(lock)
    }

    pub(crate) fn backup(&self, root: &Path) -> Result<serde_json::Value> {
        if let Some(original) = &self.original {
            fs::write(root.join(&self.name), original)?;
        }
        Ok(serde_json::json!({"name": self.name, "original_exists": self.original.is_some()}))
    }

    pub(crate) fn publish(&mut self) -> Result<()> {
        let current = match fs::read(&self.target) {
            Ok(bytes) => Some(bytes),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => return Err(error.into()),
        };
        ensure!(
            current == self.original,
            "merge metadata changed during publication"
        );
        drop(self.file.take());
        if self.content.is_some() {
            fs::rename(&self.path, &self.target)?;
        } else {
            if self.original.is_some() {
                fs::remove_file(&self.target)?;
            }
            fs::remove_file(&self.path)?;
        }
        self.active = false;
        Ok(())
    }
}

impl Drop for Lock {
    fn drop(&mut self) {
        drop(self.file.take());
        if self.active {
            let _ = fs::remove_file(&self.path);
        }
    }
}
