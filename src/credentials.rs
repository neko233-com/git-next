use gix::{
    bstr::ByteSlice,
    credentials::{helper::Action, program::Kind},
};
use std::path::PathBuf;

struct ResolvedHelper {
    path: PathBuf,
    native_callback: Option<PathBuf>,
}

#[derive(serde::Deserialize)]
struct Support {
    schema_version: u32,
    product: String,
    credential_helper: PathBuf,
    native_callback_directory: PathBuf,
}

fn helper_path(name: &str) -> std::io::Result<Option<ResolvedHelper>> {
    if !name
        .bytes()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
    {
        return Ok(None);
    }
    let executable = format!("git-credential-{name}");
    let mut directories = Vec::new();
    if let Ok(current) = std::env::current_exe()
        && let Some(parent) = current.parent()
    {
        if name == "manager" {
            let locator = parent.join("git-next-support.json");
            match std::fs::read(&locator) {
                Ok(bytes) => {
                    let support: Support =
                        serde_json::from_slice(&bytes).map_err(std::io::Error::other)?;
                    if support.schema_version != 1
                        || support.product != "git-next"
                        || !support.credential_helper.is_absolute()
                        || !support.native_callback_directory.is_absolute()
                        || !support.credential_helper.is_file()
                        || !support
                            .native_callback_directory
                            .join(format!("git{}", std::env::consts::EXE_SUFFIX))
                            .is_file()
                    {
                        return Err(std::io::Error::other(
                            "managed credential support is incomplete; reinstall credential support before authenticating",
                        ));
                    }
                    return Ok(Some(ResolvedHelper {
                        path: support.credential_helper,
                        native_callback: Some(support.native_callback_directory),
                    }));
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                Err(error) => return Err(error),
            }
        }
        directories.push(parent.to_owned());
        directories.push(parent.join("support/bin"));
    }
    if let Some(path) = std::env::var_os("GIT_EXEC_PATH") {
        directories.push(path.into());
    }
    directories.extend(std::env::split_paths(
        &std::env::var_os("PATH").unwrap_or_default(),
    ));
    for directory in directories {
        let candidate = directory.join(&executable);
        if candidate.is_file() {
            return Ok(Some(ResolvedHelper {
                path: candidate,
                native_callback: None,
            }));
        }
        if cfg!(windows) {
            let candidate = directory.join(format!("{executable}.exe"));
            if candidate.is_file() {
                return Ok(Some(ResolvedHelper {
                    path: candidate,
                    native_callback: None,
                }));
            }
        }
    }
    Ok(None)
}

pub(crate) fn read_existing(
    repo: &gix::Repository,
) -> impl FnMut(Action) -> gix::Result<Option<gix::credentials::protocol::Outcome>> + use<> {
    let repo = repo.clone();
    move |action| {
        let Action::Get(context) = &action else {
            return Ok(None);
        };
        let url = context
            .url
            .clone()
            .or_else(|| context.to_url())
            .ok_or_else(|| {
                gix::Error::from_error(std::io::Error::other("credential context has no URL"))
            })?;
        let url = gix::url::parse(url.as_bstr())?;
        let (mut cascade, _, prompt) = repo.config_snapshot().credential_helpers(url)?;
        for program in &mut cascade.programs {
            match &program.kind {
                Kind::ExternalName { name_and_args } => {
                    let bytes = name_and_args.as_slice();
                    let separator = bytes
                        .iter()
                        .position(|byte| byte.is_ascii_whitespace())
                        .unwrap_or(bytes.len());
                    let name = std::str::from_utf8(&bytes[..separator]).map_err(|_| {
                        gix::Error::from_error(std::io::Error::other(
                            "credential helper name is not UTF-8",
                        ))
                    })?;
                    let executable = helper_path(name).map_err(gix::Error::from_error)?.ok_or_else(|| gix::Error::from_error(std::io::Error::other("configured credential helper executable was not found; git-next does not launch external Git to resolve helpers")))?;
                    let mut path = executable
                        .path
                        .to_string_lossy()
                        .replace('\\', "/")
                        .into_bytes();
                    path.extend_from_slice(&bytes[separator..]);
                    program.kind = Kind::ExternalPath {
                        path_and_args: path.into(),
                    };
                    if let Some(directory) = executable.native_callback {
                        program.set_environment("GIT_EXEC_PATH", directory);
                    }
                }
                Kind::Builtin => {
                    return Err(gix::Error::from_error(std::io::Error::other(
                        "external git credential fallback is disabled",
                    )));
                }
                Kind::ExternalPath { .. } | Kind::ExternalShellScript(_) => {}
            }
        }
        cascade.invoke(action, prompt)
    }
}
