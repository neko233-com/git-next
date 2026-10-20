"""Exercise native checkout safety against reference Git in an isolated home."""
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
    with tempfile.TemporaryDirectory(prefix="git-next-worktree-") as directory:
        root = Path(directory)
        home = root / "home"
        home.mkdir()
        empty = root / "empty"
        empty.mkdir()
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / "config"),
                   GIT_AUTHOR_NAME="Acceptance", GIT_COMMITTER_NAME="Acceptance",
                   GIT_AUTHOR_EMAIL="acceptance@example.invalid",
                   GIT_COMMITTER_EMAIL="acceptance@example.invalid")
        repo = root / "repo"

        def run(client, *command, success=True, cwd=repo):
            variables = {**env}
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty),
                                 GIT_SSH=str(empty / "missing-ssh"),
                                 GIT_SSH_COMMAND=str(empty / "missing-ssh"))
            result = subprocess.run([binary if client else reference, *map(str, command)],
                                    cwd=cwd, env=variables, capture_output=True, timeout=30)
            assert (result.returncode == 0) == success, (command, result.stderr.decode(errors="replace"))
            return result.stdout

        def state():
            return (run(False, "rev-parse", "HEAD"),
                    (repo / ".git/HEAD").read_bytes(),
                    (repo / ".git/index").read_bytes(),
                    {str(path.relative_to(repo)): path.read_bytes()
                     for path in repo.rglob("*")
                     if path.is_file() and ".git" not in path.relative_to(repo).parts})

        run(False, "init", "-b", "main", repo, cwd=root)
        (repo / ".gitattributes").write_bytes(b"*.txt text eol=crlf\n")
        (repo / "keep.txt").write_bytes(b"first\r\n")
        (repo / "deleted.txt").write_bytes(b"delete me\r\n")
        (repo / "stable.txt").write_bytes(b"unchanged\r\n")
        run(False, "add", ".")
        run(False, "commit", "-m", "fixture: first")
        first = run(False, "rev-parse", "HEAD").strip().decode()
        run(False, "switch", "-c", "next")
        (repo / "keep.txt").write_bytes(b"second\r\n")
        (repo / "deleted.txt").unlink()
        (repo / "folder").mkdir()
        (repo / "folder/created.txt").write_bytes(b"created\r\n")
        run(False, "add", "-A")
        run(False, "commit", "-m", "fixture: second")
        second = run(False, "rev-parse", "HEAD").strip().decode()
        run(False, "switch", "main")
        stable_time = (repo / "stable.txt").stat().st_mtime_ns
        (repo / "untracked.dat").write_bytes(b"preserve me")
        run(True, "switch", "next")
        assert run(False, "rev-parse", "HEAD").strip().decode() == second
        assert (repo / "keep.txt").read_bytes() == b"second\r\n"
        assert (repo / "folder/created.txt").read_bytes() == b"created\r\n"
        assert not (repo / "deleted.txt").exists()
        assert (repo / "untracked.dat").read_bytes() == b"preserve me"
        assert (repo / "stable.txt").stat().st_mtime_ns == stable_time
        assert run(False, "status", "--porcelain") == b"?? untracked.dat\n"
        run(False, "fsck", "--full")
        checks.append("switch updates HEAD/index/worktree with CRLF attributes; unchanged and unrelated untracked files preserved")
        run(True, "checkout", "-b", "created", first)
        assert run(False, "symbolic-ref", "--short", "HEAD") == b"created\n"
        assert (repo / "deleted.txt").is_file()
        run(True, "checkout", "--detach", second)
        assert not (repo / ".git/HEAD").read_text().startswith("ref:")
        run(True, "switch", "main")
        run(True, "merge", "--ff-only", "next")
        assert run(False, "rev-parse", "HEAD").strip().decode() == second
        run(True, "merge", "--ff-only", first)
        assert run(False, "rev-parse", "HEAD").strip().decode() == second
        checks.append("branch creation, checkout, detached HEAD and fast-forward merge interoperable with Git")
        run(True, "switch", "created")
        for relative in ["index.lock", "HEAD.lock", "refs/heads/next.lock"]:
            lock = repo / ".git" / relative
            lock.parent.mkdir(parents=True, exist_ok=True)
            lock.write_bytes(b"other writer")
            before = state()
            run(True, "switch", "next", success=False)
            assert state() == before
            assert lock.read_bytes() == b"other writer"
            lock.unlink()
        checks.append("index, HEAD and branch lock conflicts leave HEAD/index/worktree unchanged")
        (repo / "keep.txt").write_bytes(b"dirty tracked\r\n")
        before = state()
        run(True, "switch", "-c", "must-not-exist", "next", success=False)
        assert state() == before
        run(False, "show-ref", "--verify", "refs/heads/must-not-exist", success=False)
        run(False, "restore", "keep.txt")
        (repo / "keep.txt").write_bytes(b"staged change\r\n")
        run(False, "add", "keep.txt")
        before = state()
        run(True, "switch", "next", success=False)
        assert state() == before
        run(False, "restore", "--staged", "keep.txt")
        run(False, "restore", "keep.txt")
        (repo / "folder/created.txt").parent.mkdir(exist_ok=True)
        (repo / "folder/created.txt").write_bytes(b"untracked collision")
        before = state()
        run(True, "switch", "next", success=False)
        assert state() == before
        (repo / "folder/created.txt").unlink()
        checks.append("dirty files and untracked collisions refuse without creating a branch or changing the index")
        run(False, "config", "core.hooksPath", "hooks")
        (repo / "hooks").mkdir()
        (repo / "hooks/post-checkout").write_bytes(b"#!/bin/sh\nexit 1\n")
        before = state()
        run(True, "switch", "next", success=False)
        assert state() == before
        run(False, "config", "--unset", "core.hooksPath")
        (repo / "hooks/post-checkout").unlink()
        (repo / "hooks").rmdir()
        pending = repo / ".git/git-next-checkout-interrupted"
        pending.mkdir()
        (pending / "journal.json").write_bytes(b"{}")
        before = state()
        for command in [("switch", "next"), ("add", "-A"), ("commit", "-m", "blocked")]:
            run(True, *command, success=False)
            assert state() == before
        (pending / "journal.json").unlink()
        pending.rmdir()
        checks.append("custom hooks and interrupted checkout journals block unsafe mutations")
        (repo / "folder").rmdir()
        (repo / "folder").write_bytes(b"file blocks target directory")
        before = state()
        run(True, "switch", "next", success=False)
        assert state() == before
        assert not list((repo / ".git").glob("git-next-checkout-*/journal.json"))
        (repo / "folder").unlink()
        checks.append("mid-apply file/directory conflict rolls back moved files before refs/index publication")
        run(False, "switch", "next")
        run(False, "switch", "-c", "diverged", first)
        (repo / "local.dat").write_bytes(b"local history")
        run(False, "add", ".")
        run(False, "commit", "-m", "fixture: divergence")
        before = state()
        run(True, "merge", "--ff-only", "next", success=False)
        assert state() == before
        run(False, "fsck", "--full")
        checks.append("divergent merge refuses without mutation; final independent fsck passes")
    print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                      "checks": checks, "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
