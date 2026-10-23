"""Plumbing and meta commands: branch, rev-parse, config, compatibility, language."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    with tempfile.TemporaryDirectory(prefix="git-next-plumbing-") as directory:
        root = Path(directory)
        home = root / "home"
        home.mkdir()
        empty = root / "empty"
        empty.mkdir()
        env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        env.update(
            HOME=str(home),
            USERPROFILE=str(home),
            GIT_CONFIG_NOSYSTEM="1",
            GIT_CONFIG_GLOBAL=str(home / "config"),
            GIT_AUTHOR_NAME="Acceptance",
            GIT_COMMITTER_NAME="Acceptance",
            GIT_AUTHOR_EMAIL="acceptance@example.invalid",
            GIT_COMMITTER_EMAIL="acceptance@example.invalid",
        )
        repo = root / "repo"

        def run(client, *command, cwd=repo, success=True):
            variables = {**env}
            if client:
                variables.update(
                    PATH=str(empty),
                    GIT_EXEC_PATH=str(empty),
                    GIT_SSH=str(empty / "missing"),
                    GIT_SSH_COMMAND=str(empty / "missing"),
                )
            exe = binary if client else reference
            result = subprocess.run(
                [exe, *map(str, command)],
                cwd=cwd,
                env=variables,
                capture_output=True,
                timeout=60,
            )
            assert (result.returncode == 0) == success, (
                command,
                result.stderr.decode(errors="replace"),
            )
            return result.stdout

        run(False, "init", "-b", "main", repo, cwd=root)
        (repo / "a.txt").write_bytes(b"a\n")
        run(False, "add", "a.txt")
        run(False, "commit", "-m", "one")
        run(True, "branch", "topic")
        top = run(True, "rev-parse", "--show-toplevel").strip().decode()
        assert Path(top).resolve() == repo.resolve()
        assert run(True, "rev-parse", "--git-dir").strip().endswith(b".git")
        assert run(True, "rev-parse", "--is-bare-repository").strip() == b"false"
        run(True, "-C", repo, "config", "user.name", "Plumbing")
        assert run(True, "config", "user.name").strip() == b"Plumbing"
        compat = json.loads(run(True, "compatibility", "--json"))
        assert compat["product"] == "git-next"
        assert compat["native_git_linked"] is False
        version = json.loads(run(True, "version", "--json"))
        assert version["implementation"] == "rust"
        doctor = json.loads(run(True, "doctor", "--json"))
        assert doctor["replacement_ready"] is False
        run(True, "language", "list")
        print("plumbing_cli_acceptance: ok")


if __name__ == "__main__":
    main()
