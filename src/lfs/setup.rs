use anyhow::{Context, Result, ensure};
use std::{fs, io::Write};

pub(super) fn install(repo: &gix::Repository, skip_smudge: bool) -> Result<()> {
    let directory = crate::hooks::directory(repo)?;
    let names = ["pre-push", "post-checkout", "post-commit", "post-merge"];
    crate::hooks::check_lfs_lockable(repo, &names, None)?;
    let mut config = repo.config_file_mut(repo.common_dir().join("config"))?;
    config.set_raw_value("filter.lfs.clean", "git-lfs clean -- %f")?;
    config.set_raw_value(
        "filter.lfs.smudge",
        if skip_smudge {
            "git-lfs smudge --skip -- %f"
        } else {
            "git-lfs smudge -- %f"
        },
    )?;
    config.set_raw_value(
        "filter.lfs.process",
        if skip_smudge {
            "git-lfs filter-process --skip"
        } else {
            "git-lfs filter-process"
        },
    )?;
    config.set_raw_value("filter.lfs.required", "true")?;
    fs::create_dir_all(&directory)?;
    let mut created = Vec::new();
    let result = (|| -> Result<()> {
        for name in names {
            let path = directory.join(name);
            if path.exists() {
                continue;
            }
            let mut temporary = tempfile::NamedTempFile::new_in(&directory)?;
            writeln!(temporary, "#!/bin/sh\ngit lfs {name} \"$@\"")?;
            temporary.as_file().sync_all()?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                temporary
                    .as_file()
                    .set_permissions(fs::Permissions::from_mode(0o755))?;
            }
            temporary.persist_noclobber(&path)?;
            created.push((
                path,
                format!("#!/bin/sh\ngit lfs {name} \"$@\"\n").into_bytes(),
            ));
        }
        config.commit()?;
        Ok(())
    })();
    if result.is_err() {
        for (path, contents) in created {
            if fs::read(&path).is_ok_and(|current| current == contents) {
                let _ = fs::remove_file(path);
            }
        }
    }
    result?;
    println!(
        "{}",
        crate::language::text(
            "Git LFS initialized (git-next native; repository-local configuration)."
        )
    );
    Ok(())
}

pub(super) fn track(repo: &gix::Repository, patterns: &[String]) -> Result<()> {
    let root = repo
        .workdir()
        .context("lfs track requires a working tree")?;
    ensure!(
        std::env::current_dir()?.canonicalize()? == root.canonicalize()?,
        "run lfs track at the repository root (use -C)"
    );
    ensure!(!patterns.is_empty(), "specify LFS track patterns");
    let path = root.join(".gitattributes");
    let lock_path = root.join(".gitattributes.lock");
    let file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&lock_path)
        .context("attributes file is locked by another writer")?;
    let mut lock = AttributeLock {
        path: lock_path,
        file: Some(file),
        active: true,
    };
    if let Ok(metadata) = fs::symlink_metadata(&path) {
        ensure!(
            metadata.is_file() && !metadata.file_type().is_symlink(),
            "attributes file is not a regular file"
        );
        lock.file
            .as_ref()
            .context("closed attribute lock")?
            .set_permissions(metadata.permissions())?;
    }
    let mut contents = match fs::read(&path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Vec::new(),
        Err(error) => return Err(error.into()),
    };
    for pattern in patterns {
        ensure!(
            !pattern.is_empty()
                && !pattern.contains(['\n', '\r', '\0'])
                && !pattern.starts_with('!'),
            "invalid LFS attribute pattern"
        );
        let quoted = if pattern
            .bytes()
            .any(|byte| byte.is_ascii_whitespace() || matches!(byte, b'"' | b'\\' | b'#'))
        {
            format!(
                "\"{}\"",
                pattern
                    .replace('\\', "\\\\")
                    .replace('"', "\\\"")
                    .replace('\t', "\\t")
            )
        } else {
            pattern.clone()
        };
        let line = format!("{quoted} filter=lfs diff=lfs merge=lfs -text");
        if contents
            .split(|byte| *byte == b'\n')
            .any(|existing| existing.strip_suffix(b"\r").unwrap_or(existing) == line.as_bytes())
        {
            continue;
        }
        if !contents.is_empty() && !contents.ends_with(b"\n") {
            contents.push(b'\n');
        }
        contents.extend_from_slice(line.as_bytes());
        contents.push(b'\n');
    }
    let mut file = lock.file.take().context("closed attribute lock")?;
    file.write_all(&contents)?;
    file.sync_all()?;
    drop(file);
    fs::rename(&lock.path, &path)?;
    lock.active = false;
    Ok(())
}

struct AttributeLock {
    path: std::path::PathBuf,
    file: Option<fs::File>,
    active: bool,
}
impl Drop for AttributeLock {
    fn drop(&mut self) {
        drop(self.file.take());
        if self.active {
            let _ = fs::remove_file(&self.path);
        }
    }
}
