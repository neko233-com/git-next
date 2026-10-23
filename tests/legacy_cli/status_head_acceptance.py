"""An eligible cache-tree never hides staged changes: verify HEAD paths/modes/IDs against one index snapshot."""
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
    binary = args.binary.resolve()
    reference = shutil.which("git")
    assert reference
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-status-head-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / "config"), GIT_OPTIONAL_LOCKS="0",
                   GIT_AUTHOR_NAME="Fixture", GIT_COMMITTER_NAME="Fixture",
                   GIT_AUTHOR_EMAIL="fixture@example.invalid", GIT_COMMITTER_EMAIL="fixture@example.invalid")
        for object_format in ("sha1", "sha256"):
            repo = root / object_format
            size = 20 if object_format == "sha1" else 32

            def run(native, *arguments, cwd=None, code=0):
                variables = {**env}
                if native:
                    variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
                process = subprocess.run([str(binary) if native else reference, *map(str, arguments)],
                                         cwd=cwd or (repo if repo.exists() else root), env=variables,
                                         capture_output=True, timeout=30)
                assert process.returncode == code, (arguments, process.returncode, process.stderr)
                return process.stdout

            def git(*arguments):
                return run(False, *arguments)

            git("init", "-b", "main", "--object-format=" + object_format, repo)
            for key, value in (("core.autocrlf", "input"), ("core.eol", "lf"), ("core.filemode", "false"), ("core.quotePath", "false"), ("index.version", "2")):
                git("config", key, value)
            files = ("a.b", "a/file.txt", "a/z/nested.txt", "a0", "a-z", "top.txt", "z/file.txt")
            for name in files:
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(("base " + name + "\n").encode())
            (repo / ".gitattributes").write_bytes(b"* text=auto eol=lf\n")
            git("add", ".")
            git("commit", "-m", "fixture: sorted paths and cached HEAD")
            index = repo / ".git/index"

            def parts():
                data = index.read_bytes()
                assert data[:4] == b"DIRC" and int.from_bytes(data[4:8], "big") in (2, 3)
                count = int.from_bytes(data[8:12], "big")
                position = 12
                for _ in range(count):
                    start = position
                    flags = int.from_bytes(data[position + 40 + size:position + 42 + size], "big")
                    position += 42 + size + (2 if flags & 0x4000 else 0)
                    position = data.index(b"\0", position) + 1
                    position = start + ((position - start + 7) & ~7)
                extensions = []
                cursor = position
                while cursor < len(data) - size:
                    length = int.from_bytes(data[cursor + 4:cursor + 8], "big")
                    extensions.append((data[cursor:cursor + 4], data[cursor + 8:cursor + 8 + length]))
                    cursor += 8 + length
                assert cursor == len(data) - size
                return data[:position], extensions, count

            def set_tree(payload):
                prefix, extensions, count = parts()
                data = bytearray(prefix)
                for signature, value in extensions:
                    if signature != b"TREE":
                        data.extend(signature + len(value).to_bytes(4, "big") + value)
                if payload is not None:
                    data.extend(b"TREE" + len(payload).to_bytes(4, "big") + payload)
                digest = hashlib.new(object_format, data).digest()
                index.write_bytes(data + digest)
                return count

            cached = next(value for signature, value in parts()[1] if signature == b"TREE")
            assert cached.startswith(b"\0" + str(parts()[2]).encode() + b" ")
            head = git("rev-parse", "HEAD")

            def compare(label, *, stale=False, cwd=None):
                if stale:
                    saved = index.read_bytes()
                    set_tree(None)
                    expected = run(False, "status", "--porcelain", cwd=cwd)
                    index.write_bytes(saved)
                else:
                    expected = run(False, "status", "--porcelain", cwd=cwd)
                before = index.read_bytes()
                actual = run(True, "status", "--porcelain", cwd=cwd)
                assert actual == expected, (label, actual, expected)
                assert index.read_bytes() == before and git("rev-parse", "HEAD") == head
                assert not (repo / ".git/index.lock").exists()
                checks.append(object_format + ": " + label)
                return actual

            compare("clean cached tree with directory/file sort boundaries")
            compare("nested current directory", cwd=repo / "a")
            (repo / "a/file.txt").write_bytes(b"base a/file.txt\r\n")
            assert git("hash-object", "--path=a/file.txt", "a/file.txt") == git("rev-parse", ":a/file.txt")
            before = index.read_bytes()
            assert run(True, "status", "--porcelain") == b"" and index.read_bytes() == before
            checks.append(object_format + ": canonical LF/CRLF-only save remains clean")
            (repo / "a/file.txt").write_bytes(b"base a/file.txt\n")
            (repo / "top.txt").write_bytes(b"real edit\n")
            assert b" M top.txt" in compare("real worktree edit")
            (repo / "scratch.txt").write_bytes(b"untracked\n")
            assert b"?? scratch.txt" in compare("untracked file")
            (repo / "scratch.txt").unlink()
            git("add", "top.txt")
            git("write-tree")
            assert b"M  top.txt" in compare("valid cached tree for staged content differs from HEAD")
            set_tree(cached)
            assert b"M  top.txt" in compare("stale matching cache ID cannot hide staged content", stale=True)
            git("reset", "--hard", "HEAD")
            git("update-index", "--chmod=+x", "top.txt")
            set_tree(cached)
            assert b"M  top.txt" in compare("stale matching cache ID cannot hide staged mode", stale=True)
            git("reset", "--hard", "HEAD")
            git("mv", "top.txt", "renamed.txt")
            (repo / "renamed.txt").write_bytes(b"entirely different replacement text\n" * 100)
            git("add", "renamed.txt")
            set_tree(cached)
            changed = compare("stale matching cache ID cannot hide changed paths", stale=True)
            assert b"A  renamed.txt" in changed and b"D  top.txt" in changed
            git("reset", "--hard", "HEAD")
            set_tree(None)
            compare("missing cache-tree")
            set_tree(b"\0-1 0\n")
            compare("invalid cache-tree", stale=True)
            tree_id = bytes.fromhex(git("rev-parse", "HEAD^{tree}").decode().strip())
            set_tree(b"\0" + str(parts()[2] + 5).encode() + b" 0\n" + tree_id)
            compare("wrong cache count", stale=True)
            git("reset", "--hard", "HEAD")
            (repo / "intent.txt").write_bytes(b"intent\n")
            git("add", "-N", "intent.txt")
            set_tree(b"\0" + str(parts()[2]).encode() + b" 0\n" + tree_id)
            assert b" A intent.txt" in compare("intent-to-add with forged valid-looking cache", stale=True)
            git("reset", "--hard", "HEAD")
            for key in ("status.renames", "status.renameLimit", "diff.renames", "diff.renameLimit"):
                assert run(True, "-c", key + "=false" if "Limit" not in key else key + "=0", "status", "--porcelain") == b""
            for override in ("status.renames=invalid", "diff.renames=invalid", "status.renameLimit=invalid", "diff.renameLimit=invalid", "diff.algorithm=invalid"):
                before = index.read_bytes()
                run(True, "-c", override, "status", "--porcelain", code=128)
                assert index.read_bytes() == before
            checks.append(object_format + ": explicit rename settings retain complete validation; invalid configuration refuses without index writes")
            git("fsck", "--full")
    print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "object_formats": ["sha1", "sha256"], "checks": checks,
                      "runtime_git_on_path": False, "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
