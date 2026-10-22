use super::{Cache, smudge};
use anyhow::{Context, Result, ensure};
use std::io::{Read, Seek, Write};

fn packet(input: &mut impl Read) -> Result<Option<Vec<u8>>> {
    let mut prefix = [0; 4];
    let count = input.read(&mut prefix[..1])?;
    if count == 0 {
        return Ok(None);
    }
    input.read_exact(&mut prefix[1..])?;
    let size = usize::from_str_radix(std::str::from_utf8(&prefix)?, 16)
        .context("invalid filter packet length")?;
    if size == 0 {
        return Ok(Some(Vec::new()));
    }
    ensure!((4..=65520).contains(&size), "invalid filter packet size");
    let mut bytes = vec![0; size - 4];
    ensure!(!bytes.is_empty(), "empty data packet is not a flush");
    input.read_exact(&mut bytes)?;
    Ok(Some(bytes))
}

fn list(input: &mut impl Read) -> Result<Option<Vec<Vec<u8>>>> {
    let mut values = Vec::new();
    loop {
        match packet(input)? {
            Some(bytes) if bytes.is_empty() => return Ok(Some(values)),
            Some(bytes) => {
                ensure!(
                    values.len() < 64 && bytes.len() < 8192,
                    "oversized filter header"
                );
                values.push(bytes);
            }
            None => {
                ensure!(values.is_empty(), "truncated filter header");
                return Ok(None);
            }
        }
    }
}

fn send(output: &mut impl Write, bytes: &[u8]) -> Result<()> {
    ensure!(
        bytes.len() <= 65516 && !bytes.is_empty(),
        "invalid outgoing filter packet"
    );
    write!(output, "{:04x}", bytes.len() + 4)?;
    output.write_all(bytes)?;
    Ok(())
}
fn flush(output: &mut impl Write) -> Result<()> {
    output.write_all(b"0000")?;
    output.flush()?;
    Ok(())
}
fn has(lines: &[Vec<u8>], expected: &[u8]) -> bool {
    lines
        .iter()
        .any(|v| v.strip_suffix(b"\n").unwrap_or(v) == expected)
}

pub(super) fn run(repo: &gix::Repository, cache: &Cache, skip: bool) -> Result<()> {
    let mut input = std::io::stdin().lock();
    let mut output = std::io::stdout().lock();
    let hello = list(&mut input)?.context("missing filter greeting")?;
    ensure!(
        has(&hello, b"git-filter-client") && has(&hello, b"version=2"),
        "unsupported filter protocol"
    );
    send(&mut output, b"git-filter-server\n")?;
    send(&mut output, b"version=2\n")?;
    flush(&mut output)?;
    let offered = list(&mut input)?.context("missing filter capabilities")?;
    for capability in [
        b"capability=clean\n".as_slice(),
        b"capability=smudge\n".as_slice(),
    ] {
        if has(
            &offered,
            capability
                .strip_suffix(b"\n")
                .context("capability newline")?,
        ) {
            send(&mut output, capability)?;
        }
    }
    flush(&mut output)?;
    let mut download = None;
    while let Some(headers) = list(&mut input)? {
        let command = if has(&headers, b"command=clean") {
            "clean"
        } else if has(&headers, b"command=smudge") {
            "smudge"
        } else {
            "unsupported"
        };
        let mut original = cache.temporary()?;
        loop {
            let bytes = packet(&mut input)?.context("truncated filter content")?;
            if bytes.is_empty() {
                break;
            }
            original.write_all(&bytes)?;
        }
        original.rewind()?;
        let mut result = cache.temporary()?;
        let outcome = match command {
            "clean" => cache.clean(original.as_file_mut(), result.as_file_mut()),
            "smudge" => smudge(
                repo,
                cache,
                original.as_file_mut(),
                result.as_file_mut(),
                skip,
                &mut download,
            ),
            _ => Err(anyhow::anyhow!("unsupported filter command")),
        };
        if let Err(error) = outcome {
            eprintln!("git-next LFS filter: {error:#}");
            send(&mut output, b"status=error\n")?;
            flush(&mut output)?;
            continue;
        }
        result.rewind()?;
        send(&mut output, b"status=success\n")?;
        flush(&mut output)?;
        let mut buffer = vec![0; 65516];
        loop {
            let count = result.read(&mut buffer)?;
            if count == 0 {
                break;
            }
            send(&mut output, &buffer[..count])?;
        }
        flush(&mut output)?;
        flush(&mut output)?;
    }
    Ok(())
}
