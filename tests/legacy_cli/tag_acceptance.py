"""Lightweight and annotated tags against reference Git."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

import argparse
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
    with tempfile.TemporaryDirectory(prefix="git-next-tag-") as directory:
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

        def run(client, *command, success=True, cwd=repo):
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
        (repo / "f.txt").write_bytes(b"v1\n")
        run(False, "add", "f.txt")
        run(False, "commit", "-m", "initial")
        head = run(False, "rev-parse", "HEAD").strip().decode()
        run(True, "tag", "light")
        run(True, "tag", "-a", "annotated", "-m", "note")
        assert b"annotated\n" in run(True, "tag")
        assert b"light\n" in run(True, "tag")
        run(True, "tag", "-d", "light")
        assert run(True, "tag") == b"annotated\n"
        run(True, "tag", "-f", "annotated", head)
        assert run(True, "show-ref", "--head") == run(False, "show-ref", "--head")
        print("tag_acceptance: ok")


if __name__ == "__main__":
    main()
