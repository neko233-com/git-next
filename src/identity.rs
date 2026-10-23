use anyhow::Result;

pub const VERSION: &str = concat!(
    "git-next version ",
    env!("CARGO_PKG_VERSION"),
    " (experimental)"
);

pub fn version(build_options: bool, json: bool) -> Result<()> {
    if json {
        println!(
            "{}",
            serde_json::to_string_pretty(&serde_json::json!({
                "product": "git-next",
                "version": env!("CARGO_PKG_VERSION"),
                "implementation": "rust",
                "engine": "gitoxide 0.89",
                "behavior_spec": "https://git-scm.com/docs",
                "native_git_linked": false,
                "ssh": "russh (embedded)",
                "os": std::env::consts::OS,
                "arch": std::env::consts::ARCH,
                "complete_git_parity": false,
                "external_git_fallback": false,
                "external_ssh_fallback": false,
            }))?
        );
    } else {
        println!("{VERSION}");
        if build_options {
            println!(
                "implementation: rust\nengine: gitoxide 0.89\nbehavior spec: https://git-scm.com/docs\nnative Git linked: false\nssh: russh (embedded)\ncpu: {}\nos: {}\nexternal Git fallback: disabled\nexternal OpenSSH fallback: disabled\ncomplete Git parity: false",
                std::env::consts::ARCH,
                std::env::consts::OS
            );
        }
    }
    Ok(())
}

pub fn doctor(json: bool) -> Result<()> {
    let report: serde_json::Value = serde_json::from_str(include_str!("../compatibility.json"))?;
    if json {
        println!(
            "{}",
            serde_json::to_string_pretty(&serde_json::json!({
                "product": "git-next", "version": env!("CARGO_PKG_VERSION"),
                "executable": std::env::current_exe()?,
                "replacement_ready": report["complete_git_parity"],
                "blockers": report["replacement_blockers"],
                "external_git_fallback": false, "external_ssh_fallback": false,
            }))?
        );
    } else {
        version(true, false)?;
        println!(
            "{}",
            crate::language::text("default branch: main\nsystem Git replacement: blocked")
        );
        if let Some(blockers) = report["replacement_blockers"].as_array() {
            for blocker in blockers {
                println!(
                    "  - {}",
                    crate::language::text(blocker.as_str().unwrap_or_default())
                );
            }
        }
    }
    Ok(())
}

pub fn fast_version() -> Option<Result<()>> {
    let mut args = std::env::args_os().skip(1);
    let first = args.next()?;
    if first != "--version" && first != "-v" && first != "version" {
        return None;
    }
    let (mut build_options, mut json) = (false, false);
    for arg in args {
        if arg == "--build-options" {
            build_options = true;
        } else if arg == "--json" {
            json = true;
        } else {
            return None;
        }
    }
    Some(version(build_options, json))
}
