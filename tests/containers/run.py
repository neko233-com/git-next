import hashlib
import json
from pathlib import Path
import platform
import subprocess
import sys

binary = Path("/opt/native/git-next")
assert binary.read_bytes()[:4] == b"\x7fELF"
reference = subprocess.check_output(["git", "--version"], text=True).strip()
cases = ("eol", "status_cache", "status_head", "status_head_paths", "status_format", "status_v2", "status_ignored", "status_long", "status_operation", "status_directory", "add_directory", "restore", "diff", "worktree", "acceptance", "commit", "repository_format", "native_discovery_unix")
results = []
for case in cases:
    script = f"/tests/{case}_acceptance.py" if case != "acceptance" else "/tests/acceptance.py"
    timeout = 360 if case == "status_v2" else 180
    command = [sys.executable, script, str(binary)]
    if case == "status_ignored":
        command.append("--quick")
    if case in ("status_format", "status_v2"):
        command.append("--include-long")
    try:
        result = subprocess.run(command, cwd="/acceptance", capture_output=True, timeout=timeout)
    except subprocess.TimeoutExpired as error:
        result = subprocess.CompletedProcess(command, 124, error.stdout or b"", (error.stderr or b"") + b"\nFixture aggregate deadline exceeded.")
    entry = {"test": Path(script).name, "exit_code": result.returncode,
             "aggregate_timeout_seconds": timeout,
             "harness_sha256": hashlib.sha256(Path(script).read_bytes()).hexdigest()}
    if case in ("add_directory", "status_format", "status_v2", "status_ignored", "status_long", "status_operation") and result.returncode == 0:
        details = json.loads(result.stdout)
        entry.update(checks_count=len(details["checks"]), acceptance_scope=details["scope"])
        if case == "add_directory":
            entry.update(skipped=details["skipped"], intentional_refusals=details["intentional_refusals"])
    results.append(entry)
    print(f"{case} => {result.returncode}", file=sys.stderr, flush=True)
    if result.returncode:
        print(result.stderr.decode(errors="replace"), file=sys.stderr)
report = {"status": "pass" if len(results) == len(cases) and all(item["exit_code"] == 0 for item in results) else "fail",
          "scope": "Linux x64 Docker container local LF/status/restore/read/worktree/commit/format/Unix-discovery subset; no network/login/Mac or native-host performance acceptance",
          "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
          "native_version": subprocess.check_output([str(binary), "--version"], text=True).strip(),
          "reference_version": reference, "machine": platform.machine(), "libc": platform.libc_ver(),
          "python_version": platform.python_version(),
          "runner_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "results": results}
print(json.dumps(report, indent=2))
raise SystemExit(0 if report["status"] == "pass" else 1)
