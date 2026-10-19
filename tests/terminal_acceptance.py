"""Native-shell acceptance in a disposable directory; no Git configuration changes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("terminal", type=Path)
    args = parser.parse_args()
    binary = args.terminal.resolve()
    original = shutil.which("git")
    before = hashlib.sha256(Path(original).read_bytes()).hexdigest() if original else None
    with tempfile.TemporaryDirectory(prefix="terminal acceptance ") as temporary:
        root = Path(temporary)
        setup = subprocess.run([binary, "--cwd", root, "--print-setup"], check=True, capture_output=True, text=True)
        report = json.loads(setup.stdout)
        assert report["system_git_replaced"] is False and report["bash_compatibility"] is False
        assert Path(report["git_next"]).is_file()
        command = 'Write-Output terminal-marker; git-next --version; exit 7' if os.name == "nt" else 'printf "terminal-marker\\n"; "$GIT_NEXT_EXECUTABLE" --version; exit 7'
        result = subprocess.run([binary, "--cwd", root, "--command", command], capture_output=True, text=True)
        assert result.returncode == 7, result.stderr
        assert "terminal-marker" in result.stdout and "git-next" in result.stdout
        rejected = subprocess.run([binary, "--cwd", root / "missing", "--print-setup"], capture_output=True)
        assert rejected.returncode != 0
    if original:
        assert hashlib.sha256(Path(original).read_bytes()).hexdigest() == before
    print(json.dumps({"status": "pass", "native_shell": True, "cwd_with_spaces": True, "exit_code": True, "system_git_unchanged": True}))


if __name__ == "__main__":
    main()
