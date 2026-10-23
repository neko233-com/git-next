"""Stash, sequencer, submodule, linked worktree, and hooks against reference Git fixtures."""
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
    with tempfile.TemporaryDirectory(prefix="git-next-extended-") as directory:
        root = Path(directory)
        home = root / "home"
        home.mkdir()
        empty = root / "empty"
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
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

        def run(client, *command, success=True, cwd=repo, input=None):
            variables = {**env}
            if client:
                variables.update(
                    PATH=str(empty),
                    GIT_EXEC_PATH=str(empty),
                    GIT_SSH=str(empty / "missing-ssh"),
                    GIT_SSH_COMMAND=str(empty / "missing-ssh"),
                )
            result = subprocess.run(
                [binary if client else reference, *map(str, command)],
                cwd=cwd,
                env=variables,
                capture_output=True,
                timeout=120,
                input=input,
            )
            assert (result.returncode == 0) == success, (
                command,
                result.returncode,
                result.stderr.decode(errors="replace"),
                result.stdout.decode(errors="replace"),
            )
            return result.stdout

        run(False, "init", "-b", "main", repo, cwd=root)
        (repo / "tracked.txt").write_bytes(b"base\n")
        run(False, "add", "tracked.txt")
        run(False, "commit", "-m", "base")
        base = run(False, "rev-parse", "HEAD").strip().decode()

        (repo / "tracked.txt").write_bytes(b"dirty\n")
        run(True, "stash", "push", "-m", "wip")
        assert run(True, "status", "--porcelain") == b""
        assert (repo / "tracked.txt").read_bytes() == b"base\n"
        run(True, "stash", "pop")
        assert (repo / "tracked.txt").read_bytes() == b"dirty\n"

        run(False, "switch", "-c", "feature")
        (repo / "feature.txt").write_bytes(b"feature\n")
        run(False, "add", "feature.txt")
        run(False, "commit", "-m", "feature commit")
        feature = run(False, "rev-parse", "HEAD").strip().decode()
        run(False, "switch", "main")
        run(True, "cherry-pick", feature)
        assert (repo / "feature.txt").read_bytes() == b"feature\n"

        run(True, "revert", "HEAD")
        assert not (repo / "feature.txt").exists()

        main_tip = run(False, "rev-parse", "main").strip().decode()
        run(False, "switch", "-c", "rebase-me")
        (repo / "rebase.txt").write_bytes(b"one\n")
        run(False, "add", "rebase.txt")
        run(False, "commit", "-m", "rebase one")
        run(False, "switch", "main")
        run(True, "rebase", "--onto", main_tip, main_tip, "rebase-me")
        assert (repo / "rebase.txt").read_bytes() == b"one\n"

        hooks = repo / ".git/hooks"
        hooks.mkdir(exist_ok=True)
        marker = root / "hook.ran"
        if os.name == "nt":
            hook = hooks / "pre-commit"
            hook.write_text(f'@echo off\r\necho ran> "{marker}"\r\n', encoding="utf-8")
        else:
            hook = hooks / "pre-commit"
            hook.write_text(f"#!/bin/sh\necho ran > '{marker}'\n", encoding="utf-8")
            hook.chmod(0o755)
        (repo / "hook.txt").write_bytes(b"x\n")
        run(True, "add", "hook.txt")
        run(True, "commit", "-m", "hook test")
        assert marker.read_text(encoding="utf-8").strip() == "ran"

        wt = root / "linked-wt"
        run(True, "worktree", "add", "--detach", wt, base)
        listed = run(True, "worktree", "list").decode()
        assert str(wt.resolve()) in listed or wt.name in listed
        run(True, "worktree", "remove", "--force", wt)

        sub_fixture = root / "sub-upstream"
        run(False, "init", "-b", "main", sub_fixture, cwd=root)
        (sub_fixture / "sub.txt").write_bytes(b"sub\n")
        run(False, "add", "sub.txt", cwd=sub_fixture)
        run(False, "commit", "-m", "sub", cwd=sub_fixture)
        sub_url = sub_fixture.resolve().as_uri()
        (repo / ".gitmodules").write_text(
            f'[submodule "child"]\n\tpath = child\n\turl = {sub_url}\n',
            encoding="utf-8",
        )
        run(False, "add", ".gitmodules")
        run(False, "submodule", "init")
        run(True, "submodule", "update")
        assert (repo / "child/sub.txt").read_bytes() == b"sub\n"
        status = run(True, "submodule", "status").decode()
        assert "child" in status

        print("extended_commands_acceptance: ok")


if __name__ == "__main__":
    main()
