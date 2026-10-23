"""Merge message cleanup and conflict hints compared with reference Git."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def snapshot(root):
    return {path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file()}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    with tempfile.TemporaryDirectory(prefix="git-next-merge-message-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / "config"), GIT_TERMINAL_PROMPT="0", GIT_EDITOR="true",
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2001-02-03T04:05:06+0000", GIT_COMMITTER_DATE="2001-02-03T04:05:06+0000",
                   GIT_MERGE_AUTOEDIT="no")

        def run(client, *command, cwd=root, code=0):
            variables = dict(env)
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if client else reference, *map(str, command)],
                                    cwd=cwd, env=variables, capture_output=True, timeout=30)
            assert result.returncode == code, (command, result.returncode, result.stderr)
            return result.stdout

        fixture = root / "fixture"
        run(False, "init", "-b", "main", fixture)
        (fixture / "shared.txt").write_bytes(b"base fixture\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "base", cwd=fixture)
        run(False, "switch", "-c", "topic", cwd=fixture)
        (fixture / "shared.txt").write_bytes(b"their line\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "theirs", cwd=fixture)
        run(False, "switch", "main", cwd=fixture)
        (fixture / "our.txt").write_bytes(b"our file\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "ours", cwd=fixture)

        cases = 0
        for mode in ("default", "whitespace", "strip", "verbatim", "scissors"):
            for conflicted in (False, True):
                expected, actual = [root / f"{mode}-{conflicted}-{kind}" for kind in ("reference", "native")]
                for path in (expected, actual):
                    shutil.copytree(fixture, path)
                    if conflicted:
                        (path / "shared.txt").write_bytes(b"our conflicting line\n")
                        run(False, "add", ".", cwd=path)
                        run(False, "commit", "-m", "our conflict", cwd=path)
                    run(False, "config", "commit.cleanup", mode, cwd=path)
                    run(False, "config", "core.commentString", "//", cwd=path)
                text = "\n// heading\r\n\r\nsubject with space  \r\n\r\n\r\nbody\t\r\n"
                for client, path in ((False, expected), (True, actual)):
                    run(client, "merge", "--no-edit", "-m", text, "topic", cwd=path, code=1 if conflicted else 0)
                if conflicted:
                    left_message = (expected / ".git" / "MERGE_MSG").read_bytes()
                    right_message = (actual / ".git" / "MERGE_MSG").read_bytes()
                    assert right_message == left_message, (mode, left_message, right_message)
                    for path in (expected, actual):
                        (path / "shared.txt").write_bytes(b"resolved fixture\n")
                        run(path == actual, "add", "shared.txt", cwd=path)
                    if mode in ("whitespace", "verbatim"):
                        before = snapshot(actual)
                        run(True, "merge", "--continue", cwd=actual, code=128)
                        assert snapshot(actual) == before
                        run(False, "commit", "--no-edit", cwd=expected)
                        run(True, "commit", "--no-edit", cwd=actual)
                    else:
                        run(False, "merge", "--continue", cwd=expected)
                        run(True, "merge", "--continue", cwd=actual)
                left = run(False, "cat-file", "commit", "HEAD", cwd=expected)
                right = run(False, "cat-file", "commit", "HEAD", cwd=actual)
                assert right == left, (mode, conflicted, left, right)
                run(False, "fsck", "--full", cwd=actual)
                cases += 1

        expected, actual = [root / f"override-{kind}" for kind in ("reference", "native")]
        for path in (expected, actual):
            shutil.copytree(fixture, path)
            run(False, "config", "commit.cleanup", "verbatim", cwd=path)
            run(path == actual, "merge", "--no-edit", "--cleanup=strip", "-m", "# omit\nsubject  ", "topic", cwd=path)
        assert run(False, "cat-file", "commit", "HEAD", cwd=actual) == run(False, "cat-file", "commit", "HEAD", cwd=expected)
        cases += 1

        for key, value in (("commit.cleanup", "invalid-fixture"), ("i18n.commitEncoding", "iso-8859-1")):
            path = root / key
            shutil.copytree(fixture, path)
            run(False, "config", key, value, cwd=path)
            before = snapshot(path)
            result = subprocess.run([binary, "merge", "--no-edit", "topic"], cwd=path,
                                    env={**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty)}, capture_output=True, timeout=30)
            assert result.returncode != 0
            assert snapshot(path) == before, key
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "cleanup_cases": cases, "custom_comment_string": True,
                          "unsupported_encoding_fails_before_publication": True, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
