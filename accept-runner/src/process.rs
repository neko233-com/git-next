use anyhow::{Context, Result, bail};
use std::{
    io::Read,
    process::{Child, Command, Output, Stdio},
    time::{Duration, Instant},
};

pub struct Process {
    child: Child,
    #[cfg(windows)]
    job: windows_sys::Win32::Foundation::HANDLE,
}

impl Process {
    pub fn spawn(command: &mut Command) -> Result<Self> {
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            command.process_group(0);
        }
        let child = command.spawn().context("spawn acceptance process")?;
        let process = Self {
            child,
            #[cfg(windows)]
            job: std::ptr::null_mut(),
        };
        #[cfg(windows)]
        let process = {
            let mut process = process;
            process.limit_job()?;
            process
        };
        Ok(process)
    }

    #[cfg(windows)]
    fn limit_job(&mut self) -> Result<()> {
        use std::os::windows::io::AsRawHandle;
        use windows_sys::Win32::System::JobObjects::*;
        // Closing the job also stops descendants that kept captured pipes open.
        unsafe {
            self.job = CreateJobObjectW(std::ptr::null(), std::ptr::null());
            anyhow::ensure!(
                !self.job.is_null(),
                "create process-limit job: {}",
                std::io::Error::last_os_error()
            );
            let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
            limits.BasicLimitInformation.LimitFlags =
                JOB_OBJECT_LIMIT_ACTIVE_PROCESS | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            limits.BasicLimitInformation.ActiveProcessLimit = 64;
            anyhow::ensure!(
                SetInformationJobObject(
                    self.job,
                    JobObjectExtendedLimitInformation,
                    &limits as *const _ as *const _,
                    std::mem::size_of_val(&limits) as u32
                ) != 0,
                "set process limit: {}",
                std::io::Error::last_os_error()
            );
            anyhow::ensure!(
                AssignProcessToJobObject(self.job, self.child.as_raw_handle()) != 0,
                "assign process-limit job: {}",
                std::io::Error::last_os_error()
            );
        }
        Ok(())
    }

    pub fn try_wait(&mut self) -> std::io::Result<Option<std::process::ExitStatus>> {
        self.child.try_wait()
    }

    fn stop(&mut self) {
        #[cfg(windows)]
        if !self.job.is_null() {
            unsafe {
                windows_sys::Win32::System::JobObjects::TerminateJobObject(self.job, 1);
            }
        }
        #[cfg(unix)]
        unsafe {
            libc::kill(-(self.child.id() as i32), libc::SIGKILL);
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

impl Drop for Process {
    fn drop(&mut self) {
        self.stop();
        #[cfg(windows)]
        if !self.job.is_null() {
            unsafe {
                windows_sys::Win32::Foundation::CloseHandle(self.job);
            }
        }
    }
}

pub fn output(command: &mut Command, timeout: Duration) -> Result<Output> {
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let label = format!(
        "{:?} {:?}",
        command.get_program(),
        command.get_args().collect::<Vec<_>>()
    );
    let mut process = Process::spawn(command)?;
    let mut stdout = process.child.stdout.take().context("stdout pipe")?;
    let mut stderr = process.child.stderr.take().context("stderr pipe")?;
    let out = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        stdout.read_to_end(&mut bytes).map(|_| bytes)
    });
    let err = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        stderr.read_to_end(&mut bytes).map(|_| bytes)
    });
    let deadline = Instant::now() + timeout;
    let status = loop {
        if let Some(status) = process.try_wait()? {
            break Some(status);
        }
        if Instant::now() >= deadline {
            break None;
        }
        std::thread::sleep(Duration::from_millis(10));
    };
    process.stop();
    let stdout = out
        .join()
        .map_err(|_| anyhow::anyhow!("stdout reader panicked"))??;
    let stderr = err
        .join()
        .map_err(|_| anyhow::anyhow!("stderr reader panicked"))??;
    let Some(status) = status else {
        bail!("process timed out after {timeout:?}: {label}");
    };
    Ok(Output {
        status,
        stdout,
        stderr,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(mode: &str) -> Command {
        let mut command = Command::new(std::env::current_exe().unwrap());
        command.args([
            "--exact",
            "process::tests::child_fixture",
            "--ignored",
            "--nocapture",
        ]);
        command.env("GIT_NEXT_PROCESS_FIXTURE", mode);
        command
    }

    #[test]
    #[ignore = "subprocess fixture"]
    #[allow(clippy::zombie_processes)] // The outer harness must reap this deliberately orphaned fixture.
    fn child_fixture() {
        match std::env::var("GIT_NEXT_PROCESS_FIXTURE").unwrap().as_str() {
            "hang" => std::thread::sleep(Duration::from_secs(30)),
            "pipes" => {
                print!("{}", "x".repeat(100_000));
                eprint!("{}", "y".repeat(100_000));
            }
            "descendant" => {
                let _child = fixture("hang").spawn().unwrap();
                println!("descendant spawned");
            }
            _ => panic!("unknown fixture"),
        }
    }

    #[test]
    fn drains_both_pipes_without_deadlock() {
        let result = output(&mut fixture("pipes"), Duration::from_secs(5)).unwrap();
        assert!(result.status.success());
        assert!(result.stdout.len() >= 100_000);
        assert!(result.stderr.len() >= 100_000);
    }

    #[test]
    fn terminates_timed_out_child() {
        let start = Instant::now();
        let error = output(&mut fixture("hang"), Duration::from_millis(150)).unwrap_err();
        assert!(error.to_string().contains("timed out"));
        assert!(start.elapsed() < Duration::from_secs(5));
    }

    #[test]
    fn reaps_descendants_after_parent_exits() {
        let start = Instant::now();
        let result = output(&mut fixture("descendant"), Duration::from_secs(5)).unwrap();
        assert!(result.status.success());
        assert!(String::from_utf8_lossy(&result.stdout).contains("descendant spawned"));
        assert!(start.elapsed() < Duration::from_secs(5));
    }
}
