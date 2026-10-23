"""Status metadata refresh preserves staging, optional locks and unfamiliar index layouts."""
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
import stat
import tempfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-status-cache-") as temporary:
        root = Path(temporary)
        repo, home, empty = root / "repo", root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def run(native, *command, extra=None, success=True, cwd=repo):
            variables = {**env, **(extra or {})}
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if native else reference, *map(str, command)], cwd=cwd,
                                    env=variables, capture_output=True, timeout=20)
            assert (result.returncode == 0) == success, (command, result.stderr)
            return result.stdout

        run(True, "init", repo, cwd=root)
        for number in range(20):
            (repo / f"file-{number:02d}.txt").write_bytes(f"unchanged {number:02d}\n".encode() * 128)
        run(True, "add", "-A")
        run(True, "commit", "-m", "fixture: native zero-stat index")
        index = repo / ".git/index"
        before = index.read_bytes()
        staged = run(False, "ls-files", "--stage")
        head = run(False, "rev-parse", "HEAD")
        assert run(True, "status", "--porcelain") == b""
        assert index.read_bytes() != before
        assert run(False, "ls-files", "--stage") == staged
        assert run(False, "rev-parse", "HEAD") == head
        run(False, "fsck", "--full")
        assert not list((repo / ".git").glob("git-next-status-*"))
        checks.append("native zero-stat entries refresh without changing staged object IDs or HEAD")

        path = repo / "file-00.txt"

        def touch():
            stat = path.stat()
            os.utime(path, ns=(stat.st_atime_ns, stat.st_mtime_ns + 2_000_000_000))

        touch()
        before = index.read_bytes()
        assert run(True, "status", "--porcelain", extra={"GIT_OPTIONAL_LOCKS": "0"}) == b""
        assert index.read_bytes() == before
        lock = repo / ".git/index.lock"
        lock.write_bytes(b"another writer")
        assert run(True, "status", "--porcelain") == b""
        assert index.read_bytes() == before and lock.read_bytes() == b"another writer"
        lock.unlink()
        assert run(True, "status", "--porcelain") == b"" and index.read_bytes() != before
        checks.append("optional locks disabled or held by another writer leave index bytes untouched")

        original = path.read_bytes()
        path.write_bytes(b"X" + original[1:])
        actual = run(True, "status", "--porcelain")
        expected = run(False, "status", "--porcelain", extra={"GIT_OPTIONAL_LOCKS": "0"})
        assert actual == expected and b" M file-00.txt" in actual
        run(True, "add", "file-00.txt")
        assert run(True, "status", "--porcelain").startswith(b"M  file-00.txt")
        checks.append("same-size edits remain visible and staging remains distinct from worktree changes")

        run(False, "update-index", "--index-version", "4")
        touch()
        before = index.read_bytes()
        run(True, "status", "--porcelain")
        assert index.read_bytes() == before
        run(False, "update-index", "--index-version", "2")
        run(False, "update-index", "--untracked-cache")
        touch()
        before = index.read_bytes()
        assert b"UNTR" in before
        run(True, "status", "--porcelain")
        assert index.read_bytes() == before
        checks.append("v4 and untracked-cache extension indexes are read without automatic rewrite")

        run(False, "update-index", "--no-untracked-cache")
        plain = index.read_bytes()
        payload = b"preserve optional fixture extension"
        extended = plain[:-20] + b"NEXT" + len(payload).to_bytes(4, "big") + payload
        index.write_bytes(extended + hashlib.sha1(extended).digest())
        touch()
        before = index.read_bytes()
        run(True, "status", "--porcelain")
        assert index.read_bytes() == before
        index.write_bytes(plain)
        mode = stat.S_IMODE(index.stat().st_mode)
        index.chmod(mode & ~0o222)
        try:
            before = index.read_bytes()
            run(True, "status", "--porcelain")
            assert index.read_bytes() == before and not lock.exists()
        finally:
            index.chmod(mode)
        checks.append("unknown optional extensions and read-only index files remain byte-for-byte unchanged")

        custom = root / "index.custom"
        custom.write_bytes(index.read_bytes())
        custom_lock = root / "index.custom.lock"
        custom_lock.write_bytes(b"custom index owner")
        before_regular = index.read_bytes()
        before_custom = custom.read_bytes()
        run(True, "add", "file-00.txt", extra={"GIT_INDEX_FILE": str(custom)}, success=False)
        assert custom_lock.read_bytes() == b"custom index owner" and custom.read_bytes() == before_custom
        assert index.read_bytes() == before_regular and not (root / "index.lock").exists()
        custom_lock.unlink()
        run(True, "add", "file-00.txt", extra={"GIT_INDEX_FILE": str(custom)})
        touch()
        run(True, "status", "--porcelain", extra={"GIT_INDEX_FILE": str(custom)})
        assert index.read_bytes() == before_regular
        run(False, "ls-files", "--stage", extra={"GIT_INDEX_FILE": str(custom)})
        checks.append("GIT_INDEX_FILE uses its full filename plus .lock and preserves the default index")
        print(json.dumps({"status": "pass", "checks": checks, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
