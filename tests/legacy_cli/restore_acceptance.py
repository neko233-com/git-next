"""Compare native restore/reset state and output with Git in disposable repositories."""
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


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-restore-") as temporary:
        root = Path(temporary)
        home, empty, fixture = root / "home", root / "empty", root / "fixture"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / "config"),
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2026-01-01T12:00:00+0000", GIT_COMMITTER_DATE="2026-01-01T12:00:00+0000")

        def run(native, *command, cwd=fixture, success=True, extra=None):
            variables = {**env, **(extra or {})}
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if native else reference, *map(str, command)], cwd=cwd,
                                    env=variables, capture_output=True, timeout=30)
            assert (result.returncode == 0) == success, (command, result.returncode, result.stdout, result.stderr)
            return result

        def git(*command, cwd=fixture):
            return run(False, *command, cwd=cwd).stdout

        git("init", "-b", "main", fixture, cwd=root)
        (fixture / "dir").mkdir()
        for name, content in {".gitattributes": b"*.txt text eol=crlf\n", "a.txt": b"first\r\n",
                              "old.txt": b"old file\r\n", "dir/nested.txt": b"nested first\r\n"}.items():
            (fixture / name).write_bytes(content)
        git("add", ".")
        git("commit", "-m", "first fixture")
        (fixture / "a.txt").write_bytes(b"second\r\n")
        (fixture / "old.txt").unlink()
        (fixture / "new.txt").write_bytes(b"second new\r\n")
        git("add", "-A")
        git("commit", "-m", "second fixture")
        (fixture / "a.txt").write_bytes(b"staged third\r\n")
        (fixture / "staged.txt").write_bytes(b"staged new\r\n")
        git("add", ".")
        (fixture / "a.txt").write_bytes(b"unstaged fourth\r\n")
        (fixture / "dir/nested.txt").unlink()
        (fixture / "new.txt").write_bytes(b"dirty new\r\n")
        (fixture / "untracked.txt").write_bytes(b"preserve untracked\n")

        def files(repo, include_git=False):
            return {path.relative_to(repo).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
                    for path in repo.rglob("*") if path.is_file()
                    and (include_git or ".git" not in path.relative_to(repo).parts)}

        number = 0

        def compare(*command, subdir="", detached=False, check_index_bytes=False, disable_logs=False, disable_filter_before_inspection=False):
            nonlocal number
            number += 1
            left, right = root / f"reference-{number}", root / f"native-{number}"
            shutil.copytree(fixture, left)
            shutil.copytree(fixture, right)
            if disable_logs:
                for repo in [left, right]:
                    git("config", "core.logAllRefUpdates", "false", cwd=repo)
                    for path in [repo / ".git/logs/HEAD", repo / ".git/logs/refs/heads/main"]:
                        path.unlink()
            if detached:
                for repo in [left, right]:
                    (repo / ".git/HEAD").write_bytes(git("rev-parse", "main", cwd=repo))
            old_index = (right / ".git/index").read_bytes()
            expected = run(False, *command, cwd=left / subdir)
            actual = run(True, *command, cwd=right / subdir)
            assert actual.stdout == expected.stdout, (command, expected.stdout, actual.stdout)
            if check_index_bytes and command[0] != "restore":
                assert (right / ".git/index").read_bytes() == old_index, "worktree-only restore changed index bytes"
            assert files(left) == files(right), (command, "worktree differs", files(left), files(right))
            if disable_filter_before_inspection:
                for repo in [left, right]:
                    git("config", "--remove-section", "filter.not-selected", cwd=repo)
            assert (left / ".git/HEAD").read_bytes() == (right / ".git/HEAD").read_bytes(), command
            for inspect in [("ls-files", "--stage", "-z"), ("rev-parse", "HEAD"),
                            ("status", "--porcelain"), ("reflog", "--format=%H%x00%gs", "HEAD"),
                            ("reflog", "--format=%H%x00%gs", "main")]:
                assert git(*inspect, cwd=left) == git(*inspect, cwd=right), (command, inspect, git(*inspect, cwd=left), git(*inspect, cwd=right), git("diff", "--", "a.txt", cwd=right), git("ls-files", "--debug", "a.txt", cwd=right))
            for pseudo in ["ORIG_HEAD"]:
                first, second = left / ".git" / pseudo, right / ".git" / pseudo
                assert first.exists() == second.exists(), (command, pseudo)
                if first.exists():
                    assert first.read_bytes() == second.read_bytes(), (command, pseudo)
            assert not list((right / ".git").glob("git-next-checkout-*")), "successful update retained a journal"
            git("fsck", "--full", cwd=right)

        for command in [("restore", "a.txt"), ("restore", "dir"), ("restore", "."),
                        ("restore", "-S", "a.txt"), ("restore", "-SW", "a.txt", "dir"),
                        ("restore", "--source=HEAD~1", "a.txt", "old.txt", "new.txt"),
                        ("restore", "--source=HEAD~1", "-S", "."),
                        ("restore", "--source=HEAD~1", "-SW", "."),
                        ("restore", "--", "*.txt", ":(exclude)a.txt")]:
            compare(*command, check_index_bytes="-S" not in command and "-SW" not in command)
        compare("restore", "nested.txt", subdir="dir", check_index_bytes=True)
        checks.append("restore index/worktree/source/pathspec combinations match Git and preserve unrelated files")
        for command in [("reset",), ("reset", "--mixed", "HEAD~1"), ("reset", "--soft", "HEAD~1"),
                        ("reset", "--hard", "HEAD~1"), ("reset", "--hard"), ("reset", "--hard", "--quiet"),
                        ("reset", "--quiet"), ("reset", "HEAD", "--", "a.txt"),
                        ("reset", "HEAD~1", "--", "dir"), ("reset", "a.txt"),
                        ("reset", "--", "*.txt", ":(exclude)a.txt")]:
            compare(*command, check_index_bytes="--soft" in command)
        compare("reset", "--soft", "HEAD~1", detached=True, check_index_bytes=True)
        compare("reset", "--hard", "HEAD~1", detached=True)
        compare("reset", disable_logs=True)
        compare("reset", "--soft", "HEAD~1", disable_logs=True)
        checks.append("mixed/soft/hard/path resets match refs, index, worktree, ORIG_HEAD, reflogs and output")
        (fixture / ".gitattributes").write_bytes(b"*.txt text eol=lf\n")
        compare("restore", "a.txt", check_index_bytes=True)
        compare("restore", ".", check_index_bytes=True)
        (fixture / ".gitattributes").write_bytes(b"*.txt text eol=lf\nstaged.txt filter=not-selected\n")
        git("config", "filter.not-selected.smudge", "nonexistent-git-next-filter")
        git("config", "filter.not-selected.required", "true")
        compare("restore", "a.txt", check_index_bytes=True, disable_filter_before_inspection=True)
        git("config", "--remove-section", "filter.not-selected")
        (fixture / ".gitattributes").write_bytes(b"*.txt text eol=crlf\n")
        checks.append("restore honors worktree/target attributes and does not execute filters for unselected files")
        for lock_name, command in [("index.lock", ("restore", "a.txt")),
                                   ("index.lock", ("reset", "--hard", "HEAD~1")),
                                   ("HEAD.lock", ("reset", "--soft", "HEAD~1")),
                                   ("refs/heads/main.lock", ("reset", "--mixed", "HEAD~1")),
                                   ("ORIG_HEAD.lock", ("reset", "--hard", "HEAD~1"))]:
            number += 1
            repo = root / f"locked-{number}"
            shutil.copytree(fixture, repo)
            (repo / ".git" / lock_name).write_bytes(b"another writer\n")
            before = files(repo, include_git=True)
            run(True, *command, cwd=repo, success=False)
            assert files(repo, include_git=True) == before, (lock_name, "locked command wrote data")
        checks.append("index/HEAD/branch/ORIG_HEAD locks fail without changing files, refs or another writer's lock")
        for command in [("restore", "a.txt", "unknown-path"), ("restore", "--source=missing", "a.txt"),
                        ("reset", "--soft", "--", "a.txt"), ("reset", "--hard", "--", "a.txt"),
                        ("reset", "--merge"), ("restore", "--ours", "a.txt")]:
            before = files(fixture, include_git=True)
            run(True, *command, success=False)
            assert files(fixture, include_git=True) == before, (command, "failed command modified data")
        checks.append("invalid or unsupported restore/reset semantics refuse before partial changes")
        for command in [("reset",), ("reset", "--soft"), ("reset", "--hard"),
                        ("reset", "--", "new.txt"), ("reset", "HEAD", "--", "new.txt"),
                        ("reset", "--", "does-not-exist")]:
            number += 1
            pair = [root / f"unborn-reference-{number}", root / f"unborn-native-{number}"]
            for repo in pair:
                git("init", "-b", "main", repo, cwd=root)
                (repo / "new.txt").write_bytes(b"unborn fixture\n")
                git("add", ".", cwd=repo)
            expected = run(False, *command, cwd=pair[0])
            actual = run(True, *command, cwd=pair[1])
            assert expected.stdout == actual.stdout, (command, expected.stdout, actual.stdout)
            assert files(pair[0]) == files(pair[1]), (command, "unborn worktree differs")
            assert git("ls-files", "--stage", cwd=pair[0]) == git("ls-files", "--stage", cwd=pair[1])
            assert (pair[0] / ".git/HEAD").read_bytes() == (pair[1] / ".git/HEAD").read_bytes()
            assert not (pair[1] / ".git/ORIG_HEAD").exists()
        checks.append("unborn-branch resets preserve symbolic HEAD and match empty-tree/index semantics")
        print(json.dumps({"status": "pass", "checks": checks, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
