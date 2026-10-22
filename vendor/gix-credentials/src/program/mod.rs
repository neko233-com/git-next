use std::{
    path::Path,
    process::{Command, Stdio},
};

use bstr::{BString, ByteSlice, ByteVec};
use gix_error::Result;

use crate::{Program, helper};

/// Resolve a configured helper name directly, without an external Git dispatcher.
fn external_name_command(
    _git_program: &Path,
    name_and_args: &bstr::BStr,
    action: &helper::Action,
) -> Result<std::process::Command> {
    let mut args = BString::from("git-credential-");
    args.push_str(name_and_args);
    gix_command::prepare(gix_path::from_bstr(args.as_bstr())?.into_owned())
        .arg(action.as_arg(true))
        .command_may_be_shell_script_allow_manual_argument_splitting()
        .try_into()
}

/// The kind of helper program to use.
#[derive(Debug, Clone, Eq, PartialEq)]
pub enum Kind {
    /// The built-in `git credential` helper program, part of any `git` distribution.
    Builtin,
    /// A custom credentials helper, as identified just by the name with optional arguments
    ExternalName {
        /// The name like `foo` along with optional args, like `foo --arg --bar="a b"`, with arguments using `sh` shell quoting rules.
        /// The program executed will be `git-credential-foo [args]` if `name_and_args` starts with `foo [args]`.
        /// Note that a shell is only used if it's needed.
        name_and_args: BString,
    },
    /// A custom credentials helper identified by an absolute path and optional arguments; literal executable paths run directly.
    ExternalPath {
        /// The absolute path to the executable, like `/path/to/exe` along with optional args, like `/path/to/exe --arg --bar="a b"`, with arguments using `sh`
        /// shell quoting rules.
        path_and_args: BString,
    },
    /// A script to execute with `sh`.
    ExternalShellScript(BString),
}

/// Initialization
impl Program {
    /// Create a new program of the given `kind`.
    pub fn from_kind(kind: Kind) -> Self {
        Program {
            kind,
            child: None,
            stderr: true,
            environment: Vec::new(),
        }
    }

    /// Parse the given input as per the custom helper definition, supporting `!<script>`, `name` and `/absolute/name`, the latter two
    /// also support arguments which are ignored here.
    ///
    /// Returns an error if the helper name or path cannot be represented by the platform.
    pub fn from_custom_definition(input: impl Into<BString>) -> Result<Self> {
        fn from_custom_definition_inner(mut input: BString) -> Result<Program> {
            let kind = if input.starts_with(b"!") {
                input.remove(0);
                Kind::ExternalShellScript(input)
            } else {
                let path = gix_path::from_bstr(
                    input
                        .find_byte(b' ')
                        .map_or(input.as_slice(), |pos| &input[..pos])
                        .as_bstr(),
                )?;
                if gix_path::is_absolute(path) {
                    Kind::ExternalPath {
                        path_and_args: input,
                    }
                } else {
                    Kind::ExternalName {
                        name_and_args: input,
                    }
                }
            };
            Ok(Program {
                kind,
                child: None,
                stderr: true,
                environment: Vec::new(),
            })
        }
        from_custom_definition_inner(input.into())
    }

    /// Convert the program into the respective command, suitable to invoke `action`.
    ///
    /// Returns an error if helper or executable paths cannot be represented in the required encoding.
    pub fn to_command(&self, action: &helper::Action) -> Result<std::process::Command> {
        let git_program = gix_path::env::exe_invocation();
        let mut cmd = match &self.kind {
            Kind::Builtin => {
                let mut cmd = Command::try_from(gix_command::prepare(git_program))?;
                cmd.arg("credential").arg(action.as_arg(false));
                cmd
            }
            Kind::ExternalName { name_and_args } => {
                external_name_command(git_program, name_and_args.as_bstr(), action)?
            }
            Kind::ExternalPath { path_and_args } => {
                let path = gix_path::from_bstr(path_and_args.as_bstr())?;
                if path.is_file() {
                    // Existing executable paths, including spaces and Windows short names, are literal.
                    let mut cmd = Command::new(path.as_ref());
                    cmd.arg(action.as_arg(true));
                    cmd
                } else {
                    gix_command::prepare(path.as_ref())
                        .command_may_be_shell_script_allow_manual_argument_splitting()
                        .arg(action.as_arg(true))
                        .try_into()?
                }
            }
            Kind::ExternalShellScript(for_shell) => {
                gix_command::prepare(gix_path::from_bstr(for_shell.as_bstr())?.as_ref())
                    .command_may_be_shell_script()
                    .arg(action.as_arg(true))
                    .try_into()?
            }
        };
        cmd.envs(self.environment.iter().map(|(key, value)| (key, value)))
            .stdin(Stdio::piped())
            .stdout(if action.expects_output() {
                Stdio::piped()
            } else {
                Stdio::null()
            })
            .stderr(if self.stderr {
                Stdio::inherit()
            } else {
                Stdio::null()
            });
        Ok(cmd)
    }
}

/// Builder
impl Program {
    /// Override one environment variable for this helper process without changing the parent.
    pub fn set_environment(&mut self, name: impl Into<std::ffi::OsString>, value: impl Into<std::ffi::OsString>) {
        self.environment.push((name.into(), value.into()));
    }

    /// By default `stderr` of programs is inherited and typically displayed in the terminal.
    pub fn suppress_stderr(mut self) -> Self {
        self.stderr = false;
        self
    }
}

impl Program {
    pub(crate) fn start(
        &mut self,
        action: &helper::Action,
    ) -> std::io::Result<(std::process::ChildStdin, Option<std::process::ChildStdout>)> {
        assert!(self.child.is_none(), "BUG: must not call `start()` twice");
        let mut cmd = self
            .to_command(action)
            .map_err(|err| std::io::Error::new(std::io::ErrorKind::InvalidInput, err))?;
        gix_trace::debug!(cmd = ?cmd, "launching credential helper");
        let mut child = cmd.spawn()?;
        let stdin = child.stdin.take().expect("stdin to be configured");
        let stdout = child.stdout.take();

        self.child = child.into();
        Ok((stdin, stdout))
    }

    pub(crate) fn finish(&mut self) -> std::io::Result<()> {
        let mut child = self
            .child
            .take()
            .expect("Call `start()` before calling finish()");
        let status = child.wait()?;
        if status.success() {
            Ok(())
        } else {
            let failure =
                gix_error::message("Credentials helper program failed").with_exit_status(status);
            Err(std::io::Error::other(failure))
        }
    }
}

///
pub mod main;
pub use main::function::main;

#[cfg(test)]
mod tests {
    use std::{ffi::OsStr, path::Path};

    use crate::helper;

    #[test]
    fn git_program_with_spaces_is_quoted_in_external_name_shell_scripts()
    -> gix_testtools::TestResult {
        let cmd = super::external_name_command(
            Path::new(r"C:\Program Files\Git\mingw64\bin\git.exe"),
            "manager --config=~/credentials".into(),
            &helper::Action::Get(Default::default()),
        )?;
        let script = cmd
            .get_args()
            .skip_while(|arg| *arg != OsStr::new("-c"))
            .nth(1)
            .expect("a shell invocation has its script after the '-c' argument");

        assert_eq!(
            script,
            OsStr::new(if cfg!(windows) {
                r#"'C:/Program Files/Git/mingw64/bin/git.exe' credential-manager --config=~/credentials "$@""#
            } else {
                r#"'C:\Program Files\Git\mingw64\bin\git.exe' credential-manager --config=~/credentials "$@""#
            }),
            "the Git executable is a single shell token even when its path contains spaces"
        );
        Ok(())
    }
}
