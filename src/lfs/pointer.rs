use anyhow::{Context, Result, ensure};
use std::collections::{BTreeMap, BTreeSet};

const VERSION: &str = "https://git-lfs.github.com/spec/v1";
pub(super) const LIMIT: usize = 1024;

#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub(crate) struct Pointer {
    pub oid: String,
    pub size: u64,
    #[serde(skip)]
    pub extensions: BTreeMap<String, String>,
}

impl Pointer {
    pub(super) fn new(oid: String, size: u64) -> Self {
        Self {
            oid,
            size,
            extensions: BTreeMap::new(),
        }
    }

    pub(super) fn encode(&self) -> Vec<u8> {
        if self.size == 0 {
            return Vec::new();
        }
        let mut text = format!("version {VERSION}\n");
        for (key, value) in &self.extensions {
            text.push_str(&format!("{key} {value}\n"));
        }
        text.push_str(&format!("oid sha256:{}\nsize {}\n", self.oid, self.size));
        text.into_bytes()
    }

    pub(super) fn parse(bytes: &[u8]) -> Result<Self> {
        ensure!(bytes.len() < LIMIT, "not an LFS pointer: size limit");
        if bytes.is_empty() {
            return Ok(Self::new(
                "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855".into(),
                0,
            ));
        }
        let text = std::str::from_utf8(bytes)?.trim();
        let expected = ["version", "oid", "size"];
        let mut values = Vec::new();
        let mut extensions = BTreeMap::new();
        let mut priorities = BTreeSet::new();
        for line in text.lines().filter(|line| !line.is_empty()) {
            let (key, value) = line.split_once(' ').context("invalid LFS pointer line")?;
            ensure!(values.len() < expected.len(), "extra LFS pointer line");
            if key == expected[values.len()] {
                values.push(value);
                continue;
            }
            let parts: Vec<_> = key.splitn(3, '-').collect();
            ensure!(
                parts.len() == 3
                    && parts[0] == "ext"
                    && parts[1].len() == 1
                    && parts[1].bytes().all(|b| b.is_ascii_digit())
                    && !parts[2].is_empty()
                    && parts[2]
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'_'),
                "invalid LFS pointer key"
            );
            valid_oid(value)?;
            ensure!(
                priorities.insert(parts[1]),
                "duplicate LFS extension priority"
            );
            extensions.insert(key.to_owned(), value.to_owned());
        }
        ensure!(values.len() == 3, "incomplete LFS pointer");
        ensure!(
            [
                VERSION,
                "https://hawser.github.com/spec/v1",
                "http://git-media.io/v/2"
            ]
            .contains(&values[0]),
            "unsupported LFS pointer version"
        );
        let oid = valid_oid(values[1])?.to_owned();
        let size: i64 = values[2].parse().context("invalid LFS pointer size")?;
        ensure!(size >= 0, "negative LFS pointer size");
        Ok(Self {
            oid,
            size: size as u64,
            extensions,
        })
    }
}

fn valid_oid(value: &str) -> Result<&str> {
    let oid = value
        .strip_prefix("sha256:")
        .context("unsupported LFS hash algorithm")?;
    ensure!(
        oid.len() == 64
            && oid
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b)),
        "invalid LFS object identifier"
    );
    Ok(oid)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn canonical_and_legacy() {
        let pointer = Pointer::new("a".repeat(64), 123);
        assert_eq!(
            Pointer::parse(&pointer.encode()).expect("canonical"),
            pointer
        );
        let legacy = String::from_utf8(pointer.encode())
            .expect("UTF8")
            .replace(VERSION, "https://hawser.github.com/spec/v1")
            .replace('\n', "\r\n");
        assert_eq!(Pointer::parse(legacy.as_bytes()).expect("legacy"), pointer);
        assert!(Pointer::parse(&[b'x'; LIMIT]).is_err());
        assert!(
            Pointer::parse(
                format!("version {VERSION}\nsize 3\noid sha256:{}\n", "a".repeat(64)).as_bytes()
            )
            .is_err()
        );
    }
}
