"""Commit message/object-format conformance in isolated repositories; Git is fixture-only."""
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
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    with tempfile.TemporaryDirectory(prefix="git-next-commit-") as temporary:
        root = Path(temporary)
        home = root / "home"
        home.mkdir()
        empty = root / "empty"
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_GLOBAL=str(home / "config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2001-02-03T04:05:06+0000", GIT_COMMITTER_DATE="2001-02-03T04:05:06+0000")

        def run(client, *command, cwd=root):
            variables = {**env}
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if client else reference, *command], cwd=cwd,
                                    env=variables, capture_output=True, timeout=30)
            assert result.returncode == 0, (command, result.returncode, result.stderr)
            return result.stdout

        cases = 0
        for object_format in ("sha1", "sha256"):
            fixture = root / object_format
            run(False, "init", "-b", "main", "--object-format=" + object_format, str(fixture))
            run(False, "config", "core.autocrlf", "false", cwd=fixture)
            (fixture / "file.txt").write_bytes(b"base fixture\n")
            run(False, "add", ".", cwd=fixture)
            run(False, "commit", "-m", "base", cwd=fixture)
            for cleanup in ("default", "whitespace", "strip", "verbatim", "scissors"):
                expected, actual = [root / f"{object_format}-{cleanup}-{kind}" for kind in ("reference", "native")]
                for path in (expected, actual):
                    shutil.copytree(fixture, path)
                    run(False, "config", "commit.cleanup", cleanup, cwd=path)
                    (path / "file.txt").write_bytes(b"changed fixture\n")
                    run(path == actual, "add", "file.txt", cwd=path)
                text = "\n# heading\r\n\r\nsubject with space  \r\n\r\n\r\nbody\t\r\n"
                run(False, "commit", "-m", text, cwd=expected)
                run(True, "commit", "-m", text, cwd=actual)
                left = run(False, "cat-file", "commit", "HEAD", cwd=expected)
                right = run(False, "cat-file", "commit", "HEAD", cwd=actual)
                assert right == left, (object_format, cleanup, left, right)
                run(False, "fsck", "--full", cwd=actual)
                cases += 1
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "object_formats": ["sha1", "sha256"], "cleanup_cases": cases,
                          "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
