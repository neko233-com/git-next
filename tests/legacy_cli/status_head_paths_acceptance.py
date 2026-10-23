"""A matching HEAD/index/cache cannot bypass protected tree-component validation."""
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
    with tempfile.TemporaryDirectory(prefix="git-next-status-paths-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / "config"), GIT_OPTIONAL_LOCKS="0")
        for object_format in ("sha1", "sha256"):
            for name, mode in ((b".git", 0o100644), (b".gitmodules", 0o120000)):
                repo = root / (object_format + "-" + name.decode().lstrip("."))

                def git(*arguments, data=None):
                    result = subprocess.run([reference, *map(str, arguments)], cwd=repo if repo.exists() else root,
                                            env=env, input=data, capture_output=True, timeout=30)
                    assert result.returncode == 0, (arguments, result.stderr)
                    return result.stdout.strip()

                git("init", "-b", "main", "--object-format=" + object_format, repo)
                blob = bytes.fromhex(git("hash-object", "-w", "--stdin", data=b"fixture-target\n").decode())
                tree_data = f"{mode:o} ".encode() + name + b"\0" + blob
                tree = bytes.fromhex(git("hash-object", "-w", "-t", "tree", "--stdin", "--literally", data=tree_data).decode())
                signature = b"Fixture <fixture@example.invalid> 981173106 +0000\n"
                commit_data = b"tree " + tree.hex().encode() + b"\nauthor " + signature + b"committer " + signature + b"\nfixture: intentionally protected tree\n"
                commit = git("hash-object", "-w", "-t", "commit", "--stdin", data=commit_data)
                git("update-ref", "refs/heads/main", commit.decode())
                stat = b"\0" * 24 + mode.to_bytes(4, "big") + b"\0" * 12
                entry = stat + blob + len(name).to_bytes(2, "big") + name + b"\0"
                entry += b"\0" * (-len(entry) % 8)
                cached = b"\0" + b"1 0\n" + tree
                data = b"DIRC\0\0\0\x02\0\0\0\x01" + entry + b"TREE" + len(cached).to_bytes(4, "big") + cached
                index = repo / ".git/index"
                index.write_bytes(data + hashlib.new(object_format, data).digest())
                before = {path.relative_to(repo).as_posix(): path.read_bytes() for path in (repo / ".git").rglob("*") if path.is_file()}
                variables = {**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty)}
                result = subprocess.run([str(binary), "status", "--porcelain"], cwd=repo, env=variables,
                                        capture_output=True, timeout=30)
                assert result.returncode == 128, (object_format, name, result.returncode, result.stdout, result.stderr)
                after = {path.relative_to(repo).as_posix(): path.read_bytes() for path in (repo / ".git").rglob("*") if path.is_file()}
                assert after == before, "refused protected tree changed repository bytes"
                checks.append(object_format + ": matching cache/HEAD/index refuses protected " + name.decode() + " without writes")
    print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "scope": "intentionally malformed disposable trees/indexes; never user data",
                      "checks": checks, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
