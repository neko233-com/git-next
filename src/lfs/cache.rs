use super::pointer::{LIMIT, Pointer};
use anyhow::{Context, Result, ensure};
use std::{
    fs,
    io::{Read, Seek, Write},
    path::PathBuf,
};

pub(super) struct Cache {
    root: PathBuf,
}

impl Cache {
    pub(super) fn new(repo: &gix::Repository) -> Result<Self> {
        let configured = repo
            .config_snapshot()
            .string("lfs.storage")
            .map(|v| PathBuf::from(v.to_string()));
        let root = match configured {
            Some(path) if path.is_absolute() => path,
            Some(path) => repo.common_dir().join(path),
            None => repo.common_dir().join("lfs"),
        };
        Ok(Self { root })
    }

    pub(super) fn root(&self) -> &std::path::Path {
        &self.root
    }
    pub(super) fn path(&self, pointer: &Pointer) -> PathBuf {
        self.root
            .join("objects")
            .join(&pointer.oid[..2])
            .join(&pointer.oid[2..4])
            .join(&pointer.oid)
    }

    pub(super) fn temporary(&self) -> Result<tempfile::NamedTempFile> {
        let directory = self.root.join("tmp");
        fs::create_dir_all(&directory)?;
        tempfile::Builder::new()
            .prefix("git-next-")
            .tempfile_in(directory)
            .context("create LFS cache temporary")
    }

    pub(super) fn verified(&self, pointer: &Pointer) -> Result<Option<fs::File>> {
        let path = self.path(pointer);
        let mut file = match fs::File::open(&path) {
            Ok(file) => file,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(error.into()),
        };
        ensure!(
            file.metadata()?.is_file(),
            "LFS object is not a regular file"
        );
        ensure!(
            file.metadata()?.len() == pointer.size,
            "LFS cache object size mismatch"
        );
        let actual = hash(&mut file, std::io::sink())?;
        ensure!(
            actual.oid == pointer.oid && actual.size == pointer.size,
            "LFS cache object checksum mismatch"
        );
        file.rewind()?;
        Ok(Some(file))
    }

    pub(super) fn publish(
        &self,
        temporary: tempfile::NamedTempFile,
        pointer: &Pointer,
    ) -> Result<()> {
        temporary.as_file().sync_all()?;
        let path = self.path(pointer);
        fs::create_dir_all(path.parent().context("invalid LFS cache path")?)?;
        match temporary.persist_noclobber(path) {
            Ok(_) => Ok(()),
            Err(error) if error.error.kind() == std::io::ErrorKind::AlreadyExists => {
                self.verified(pointer)?
                    .context("LFS object disappeared during publication")?;
                Ok(())
            }
            Err(error) => Err(error.error.into()),
        }
    }

    pub(super) fn clean(&self, mut input: impl Read, mut output: impl Write) -> Result<()> {
        let mut prefix = Vec::with_capacity(LIMIT);
        input.by_ref().take(LIMIT as u64).read_to_end(&mut prefix)?;
        if let Ok(pointer) = Pointer::parse(&prefix) {
            // Preserve extension records rather than silently discarding their transformation chain.
            output.write_all(&pointer.encode())?;
            return Ok(());
        }
        let mut temporary = self.temporary()?;
        let pointer = hash(
            std::io::Cursor::new(prefix).chain(input),
            temporary.as_file_mut(),
        )?;
        self.publish(temporary, &pointer)?;
        output.write_all(&pointer.encode())?;
        Ok(())
    }
}

pub(super) fn hash(mut input: impl Read, mut output: impl Write) -> Result<Pointer> {
    let mut hasher = gix::hash::hasher(gix::hash::Kind::Sha256);
    let mut buffer = vec![0; 128 * 1024];
    let mut size = 0u64;
    loop {
        let count = input.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        hasher.update(&buffer[..count]);
        output.write_all(&buffer[..count])?;
        size = size
            .checked_add(count as u64)
            .context("LFS object too large")?;
        ensure!(
            size <= i64::MAX as u64,
            "LFS object exceeds protocol size limit"
        );
    }
    Ok(Pointer::new(hasher.try_finalize()?.to_string(), size))
}
