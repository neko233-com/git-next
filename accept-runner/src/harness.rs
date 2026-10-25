use anyhow::{Context, Result, bail, ensure};
use std::{
    collections::HashMap,
    path::{Path, PathBuf},
    process::{Command, Output},
    time::Duration,
};

pub struct Harness {
    pub root: PathBuf,
    pub empty: PathBuf,
    pub client: PathBuf,
    pub reference: PathBuf,
    env: HashMap<String, String>,
    _temporary: tempfile::TempDir,
}

impl Harness {
    pub fn new(client: PathBuf) -> Result<Self> {
        let client = if cfg!(windows) && !client.is_file() && client.extension().is_none() {
            client.with_extension("exe")
        } else {
            client
        };
        let client = client.canonicalize().context("client binary")?;
        let reference = which_git()?;
        ensure!(
            client != reference.canonicalize()?,
            "client must differ from reference Git"
        );
        let temporary = tempfile::Builder::new()
            .prefix("git-next-accept-")
            .tempdir()?;
        let root = temporary.path().to_path_buf();
        let home = root.join("home");
        let empty = root.join("empty-path");
        std::fs::create_dir_all(&home)?;
        std::fs::create_dir_all(&empty)?;
        let mut env = HashMap::new();
        for (key, value) in std::env::vars() {
            if !key.starts_with("GIT_") {
                env.insert(key, value);
            }
        }
        env.insert("HOME".into(), home.display().to_string());
        env.insert("USERPROFILE".into(), home.display().to_string());
        env.insert(
            "XDG_CONFIG_HOME".into(),
            home.join(".config").display().to_string(),
        );
        env.insert(
            "SSH_ROOT_PATH".into(),
            home.join(".ssh").display().to_string(),
        );
        env.insert("GIT_OPTIONAL_LOCKS".into(), "0".into());
        env.insert("GIT_TERMINAL_PROMPT".into(), "0".into());
        env.insert("GIT_AUTHOR_DATE".into(), "2026-01-01T12:00:00+0000".into());
        env.insert(
            "GIT_COMMITTER_DATE".into(),
            "2026-01-01T12:00:00+0000".into(),
        );
        std::fs::write(
            home.join("config"),
            "[init]\n\tdefaultBranch = main\n[core]\n\tautocrlf = false\n\tfilemode = false\n\tsymlinks = false\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n",
        )?;
        env.insert("GIT_CONFIG_NOSYSTEM".into(), "1".into());
        // Installed reference Git can ship active LFS hooks in its templates.
        env.insert("GIT_TEMPLATE_DIR".into(), empty.display().to_string());
        env.insert(
            "GIT_CONFIG_GLOBAL".into(),
            home.join("config").display().to_string(),
        );
        env.insert("GIT_AUTHOR_NAME".into(), "Accept".into());
        env.insert("GIT_COMMITTER_NAME".into(), "Accept".into());
        env.insert(
            "GIT_AUTHOR_EMAIL".into(),
            "acceptance@example.invalid".into(),
        );
        env.insert(
            "GIT_COMMITTER_EMAIL".into(),
            "acceptance@example.invalid".into(),
        );
        env.insert("LC_ALL".into(), "C".into());
        env.insert("LANG".into(), "C".into());
        Ok(Self {
            root,
            empty,
            client,
            reference,
            env,
            _temporary: temporary,
        })
    }

    pub fn repo(&self, name: &str) -> PathBuf {
        self.root.join(name)
    }

    pub fn reference_git(&self, cwd: &Path, args: &[&str]) -> Result<Output> {
        let output = self.run_executable(&self.reference, cwd, args, false)?;
        ensure!(
            output.status.success(),
            "reference fixture {:?} failed: {}",
            args,
            String::from_utf8_lossy(&output.stderr)
        );
        Ok(output)
    }

    pub fn client_git(&self, cwd: &Path, args: &[&str]) -> Result<Output> {
        self.run_executable(&self.client, cwd, args, true)
    }

    pub fn client_ok(&self, cwd: &Path, args: &[&str]) -> Result<Vec<u8>> {
        let output = self.client_git(cwd, args)?;
        ensure!(
            output.status.success(),
            "client {:?} failed: {}",
            args,
            String::from_utf8_lossy(&output.stderr)
        );
        Ok(output.stdout)
    }

    pub fn client_expect(&self, cwd: &Path, args: &[&str], success: bool) -> Result<()> {
        let output = self.client_git(cwd, args)?;
        ensure!(
            output.status.success() == success,
            "client {:?} expected success={success}, got {}: {}",
            args,
            output.status,
            String::from_utf8_lossy(&output.stderr)
        );
        Ok(())
    }

    pub fn compare_canonical_path_stdout(&self, cwd: &Path, args: &[&str]) -> Result<()> {
        let expected = self.reference_git(cwd, args)?;
        let actual = self.client_git(cwd, args)?;
        ensure!(expected.status.success() && actual.status.success());
        let left = PathBuf::from(String::from_utf8_lossy(&expected.stdout).trim());
        let right = PathBuf::from(String::from_utf8_lossy(&actual.stdout).trim());
        ensure!(
            left.canonicalize()? == right.canonicalize()?,
            "path stdout mismatch for {:?}",
            args
        );
        Ok(())
    }

    pub fn compare_stdout(&self, cwd: &Path, args: &[&str]) -> Result<()> {
        let expected = self.reference_git(cwd, args)?;
        let actual = self.client_git(cwd, args)?;
        ensure!(
            expected.status.success() && actual.status.success(),
            "compare {:?}: ref={} client={}",
            args,
            expected.status,
            actual.status
        );
        ensure!(
            expected.stdout == actual.stdout,
            "stdout mismatch for {:?}",
            args
        );
        Ok(())
    }

    fn run_executable(
        &self,
        exe: &Path,
        cwd: &Path,
        args: &[&str],
        isolate: bool,
    ) -> Result<Output> {
        let mut command = self.command(exe, cwd);
        if isolate {
            command.args(["--language", "english"]);
        }
        command.args(args);
        if isolate {
            command.env("PATH", &self.empty);
            command.env("GIT_EXEC_PATH", &self.empty);
            command.env("GIT_SSH", self.empty.join("missing-ssh"));
            command.env("GIT_SSH_COMMAND", self.empty.join("missing-ssh"));
        }
        crate::process::output(&mut command, command_timeout())
    }

    pub fn reference_command(&self, cwd: &Path) -> Command {
        self.command(&self.reference, cwd)
    }

    fn command(&self, exe: &Path, cwd: &Path) -> Command {
        let mut command = Command::new(exe);
        command.current_dir(cwd).env_clear();
        for (key, value) in &self.env {
            command.env(key, value);
        }
        command
    }

    pub fn init_fixture_repo(&self, path: &Path, branch: &str) -> Result<()> {
        std::fs::create_dir_all(path)?;
        self.reference_git(path, &["init", "-b", branch, "."])?;
        Ok(())
    }
}

pub fn which_git() -> Result<PathBuf> {
    let path_var = std::env::var_os("PATH").context("PATH")?;
    for dir in std::env::split_paths(&path_var) {
        let candidate = dir.join(if cfg!(windows) { "git.exe" } else { "git" });
        if candidate.is_file() {
            return Ok(candidate);
        }
    }
    bail!("reference git not found on PATH (fixtures only)")
}

pub fn command_timeout() -> Duration {
    Duration::from_secs(120)
}
