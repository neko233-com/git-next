use std::path::{Path, PathBuf};

use bstr::{BStr, BString};
use std::sync::LazyLock;

/// Other places to find Git in.
#[cfg(windows)]
pub(super) static ALTERNATIVE_LOCATIONS: LazyLock<Vec<PathBuf>> =
    LazyLock::new(|| locations_under_program_files(|key| std::env::var_os(key)));
#[cfg(not(windows))]
pub(super) static ALTERNATIVE_LOCATIONS: LazyLock<Vec<PathBuf>> = LazyLock::new(Vec::new);

#[cfg(windows)]
fn locations_under_program_files<F>(var_os_func: F) -> Vec<PathBuf>
where
    F: Fn(&str) -> Option<std::ffi::OsString>,
{
    // Should give a 64-bit program files path from a 32-bit or 64-bit process on a 64-bit system.
    let varname_64bit = "ProgramW6432";

    // Should give a 32-bit program files path from a 32-bit or 64-bit process on a 64-bit system.
    // This variable is x86-specific, but neither Git nor Rust target 32-bit ARM on Windows.
    let varname_x86 = "ProgramFiles(x86)";

    // Should give a 32-bit program files path on a 32-bit system. We also check this on a 64-bit
    // system, even though it *should* equal the process's architecture-specific variable, so that
    // we cover the case of a parent process that passes down an overly sanitized environment that
    // lacks the architecture-specific variable. On a 64-bit system, because parent and child
    // processes' architectures can be different, Windows sets the child's `ProgramFiles` variable
    // from whichever of the `ProgramW6432` or `ProgramFiles(x86)` variable corresponds to the
    // child's architecture. Only if the parent does not pass down the architecture-specific
    // variable corresponding to the child's architecture does the child receive its `ProgramFiles`
    // variable from `ProgramFiles` as passed down by the parent. But this behavior is not well
    // known. So the situation where a process only passes down `ProgramFiles` sometimes happens.
    let varname_current = "ProgramFiles";

    // Should give the user's local application data path on any system. If a user program files
    // directory exists for this user, then it should be the `Programs` subdirectory of this. If it
    // doesn't exist, or on a future or extremely strangely configured Windows setup where it is
    // somewhere else, it should still be safe to attempt to use it. (This differs from global
    // program files paths, which are usually subdirectories of the root of the system drive, which
    // limited user accounts can usually create their own arbitrarily named directories inside.)
    let varname_user_appdata_local = "LocalAppData";

    // Prefer native ARM64 over an emulated x86_64 executable when both are present.
    // For x86_64, Git for Windows 2.56 uses `ucrt64`; keep `mingw64` for older releases.
    let suffixes_64 = &[r"Git\clangarm64\bin", r"Git\ucrt64\bin", r"Git\mingw64\bin"][..];

    // 32-bit relative bin dirs. So far, this is only ever `mingw32`, not `clang32`.
    let suffixes_32 = &[r"Git\mingw32\bin"][..];

    // Whichever of the 64-bit or 32-bit relative bin better matches this process's architecture.
    // Unlike the system architecture, the process architecture is always known at compile time.
    #[cfg(target_pointer_width = "64")]
    let suffixes_current = suffixes_64;
    #[cfg(target_pointer_width = "32")]
    let suffixes_current = suffixes_32;

    // Bin dirs relative to a user's local application data directory. We try each architecture.
    let suffixes_user = &[
        r"Programs\Git\clangarm64\bin",
        r"Programs\Git\ucrt64\bin",
        r"Programs\Git\mingw64\bin",
        r"Programs\Git\mingw32\bin",
    ][..];

    let rules = [
        (varname_user_appdata_local, suffixes_user),
        (varname_64bit, suffixes_64),
        (varname_x86, suffixes_32),
        (varname_current, suffixes_current),
    ];

    let mut locations = vec![];

    for (varname, suffixes) in rules {
        let Some(program_files_dir) = var_os_func(varname).map(PathBuf::from).filter(|p| p.is_absolute()) else {
            // The environment variable is unset or somehow not an absolute path (e.g. an empty string).
            continue;
        };
        for suffix in suffixes {
            let location = program_files_dir.join(suffix);
            if !locations.contains(&location) {
                locations.push(location);
            }
        }
    }

    locations
}

#[cfg(windows)]
pub(super) const EXE_NAME: &str = "git.exe";
#[cfg(not(windows))]
pub(super) const EXE_NAME: &str = "git";

#[derive(Debug, Default, Eq, PartialEq)]
struct ConfigPaths {
    installation: Option<BString>,
    installation_is_system: bool,
    system: Option<BString>,
}

/// Obtain and cache the installation and system configuration paths.
///
/// An unambiguous `EXEPATH` identifies Git for Windows' conventional configuration location.
/// Otherwise, inspect conventional layouts in `PATH` and alternative locations without execution.
static GIT_CONFIG_PATHS: LazyLock<ConfigPaths> = LazyLock::new(|| {
    let (installation, system) = super::layout::config_paths();
    ConfigPaths {
        installation: installation.and_then(|path| crate::os_string_into_bstring(path.into()).ok()),
        installation_is_system: cfg!(windows),
        system: system.and_then(|path| crate::os_string_into_bstring(path.into()).ok()),
    }
});

/// Try to find the file that contains Git configuration coming with the Git installation.
///
/// This returns the configuration associated with the `git` executable found in the current `PATH`
/// or an alternative location, or `None` if no conventional configuration path was found.
/// Discovery inspects filesystem paths and never executes Git.
pub(super) fn install_config_path() -> Option<&'static BStr> {
    let _span = gix_trace::detail!("gix_path::git::install_config_path()");
    GIT_CONFIG_PATHS.installation.as_ref().map(AsRef::as_ref)
}

pub(super) fn install_config_is_system() -> bool {
    GIT_CONFIG_PATHS.installation_is_system
}

pub(super) fn system_config_path() -> Option<&'static BStr> {
    let _span = gix_trace::detail!("gix_path::git::system_config_path()");
    static FALLBACK: LazyLock<Option<BString>> = LazyLock::new(|| {
        super::system_prefix()
            .map(super::config_path_from_system_prefix)
            .and_then(|path| crate::os_string_into_bstring(path.into()).ok())
    });
    GIT_CONFIG_PATHS
        .system
        .as_ref()
        .or_else(|| FALLBACK.as_ref())
        .map(AsRef::as_ref)
}

/// Given `config_path` as obtained from `install_config_path()`, return the path of the git installation base.
pub(super) fn config_to_base_path(config_path: &Path) -> &Path {
    config_path
        .parent()
        .expect("config file paths always have a file name to pop")
}
