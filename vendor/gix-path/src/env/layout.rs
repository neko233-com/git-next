use std::{
    env,
    path::{Path, PathBuf},
};

fn absolute(path: PathBuf) -> Option<PathBuf> {
    if path.is_absolute() {
        Some(path)
    } else {
        env::current_dir().ok().map(|cwd| cwd.join(path))
    }
}

fn path_executable() -> Option<PathBuf> {
    env::split_paths(&env::var_os("PATH")?).find_map(|directory| {
        let path = directory.join(super::git::EXE_NAME);
        path.is_file().then(|| absolute(path)).flatten()
    })
}

#[cfg(windows)]
const RUNTIMES: &[&str] = &["clangarm64", "ucrt64", "mingw64", "mingw32"];

#[cfg(windows)]
fn root_from_executable(executable: &Path) -> Option<PathBuf> {
    let directory = executable.parent()?;
    let name = directory.file_name()?.to_str()?;
    if name.eq_ignore_ascii_case("cmd") {
        return directory.parent().map(Path::to_owned);
    }
    if name.eq_ignore_ascii_case("git-core") {
        let libexec = directory.parent()?;
        if libexec.file_name()?.eq_ignore_ascii_case("libexec") {
            let runtime = libexec.parent()?;
            if RUNTIMES
                .iter()
                .any(|name| runtime.file_name().is_some_and(|part| part.eq_ignore_ascii_case(name)))
            {
                return runtime.parent().map(Path::to_owned);
            }
        }
    }
    if name.eq_ignore_ascii_case("bin") {
        let parent = directory.parent()?;
        if RUNTIMES
            .iter()
            .any(|name| parent.file_name().is_some_and(|part| part.eq_ignore_ascii_case(name)))
        {
            return parent.parent().map(Path::to_owned);
        }
        if RUNTIMES.iter().any(|runtime| parent.join(runtime).is_dir()) {
            return Some(parent.to_owned());
        }
    }
    None
}

#[cfg(windows)]
pub(super) fn windows_root() -> Option<PathBuf> {
    if let Some(root) = env::var_os("EXEPATH")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute())
    {
        if RUNTIMES.iter().any(|runtime| root.join(runtime).is_dir()) {
            return Some(root);
        }
    }
    if let Some(root) = path_executable().and_then(|executable| root_from_executable(&executable)) {
        return Some(root);
    }
    super::git::ALTERNATIVE_LOCATIONS.iter().find_map(|directory| {
        let executable = directory.join(super::git::EXE_NAME);
        executable
            .is_file()
            .then(|| root_from_executable(&executable))
            .flatten()
    })
}

pub(super) fn core_dir() -> Option<PathBuf> {
    if let Some(path) = env::var_os("GIT_EXEC_PATH").filter(|path| !path.is_empty()) {
        return absolute(path.into());
    }
    #[cfg(windows)]
    {
        let root = windows_root()?;
        let preferred = if cfg!(target_arch = "aarch64") {
            ["clangarm64", "ucrt64", "mingw64", "mingw32"]
        } else if cfg!(target_pointer_width = "32") {
            ["mingw32", "ucrt64", "mingw64", "clangarm64"]
        } else {
            ["ucrt64", "mingw64", "clangarm64", "mingw32"]
        };
        preferred
            .iter()
            .map(|runtime| root.join(runtime).join("libexec/git-core"))
            .find(|path| path.is_dir())
    }
    #[cfg(not(windows))]
    {
        let executable = path_executable()?;
        let executable = executable.canonicalize().unwrap_or(executable);
        let prefix = executable.parent()?.parent()?;
        ["libexec/git-core", "lib/git-core"]
            .iter()
            .map(|suffix| prefix.join(suffix))
            .find(|path| path.is_dir())
    }
}

#[cfg(not(windows))]
fn homebrew_root(executable: &Path) -> Option<&Path> {
    let prefix = executable.parent()?.parent()?;
    let parent = prefix.parent()?;
    if prefix.file_name()? == "git" && parent.file_name()? == "opt" {
        return parent.parent();
    }
    let cellar = parent.parent()?;
    (parent.file_name()? == "git" && cellar.file_name()? == "Cellar")
        .then(|| cellar.parent())
        .flatten()
}

pub(super) fn config_paths() -> (Option<PathBuf>, Option<PathBuf>) {
    #[cfg(windows)]
    {
        let system = windows_root().map(|root| root.join("etc/gitconfig"));
        let installation = env::var_os("PROGRAMDATA")
            .map(PathBuf::from)
            .filter(|path| path.is_absolute())
            .map(|path| path.join("Git/config"))
            .filter(|path| path.is_file());
        (installation, system)
    }
    #[cfg(not(windows))]
    {
        let executable = path_executable().map(|path| path.canonicalize().unwrap_or(path));
        if let Some(root) = executable.as_deref().and_then(homebrew_root) {
            // Homebrew sets sysconfdir outside the versioned Cellar prefix.
            return (None, Some(root.join("etc/gitconfig")));
        }
        let installation = executable.and_then(|path| {
            let config = path.parent()?.parent()?.join("etc/gitconfig");
            (config != Path::new("/etc/gitconfig") && config.is_file()).then_some(config)
        });
        (installation, Some("/etc/gitconfig".into()))
    }
}
