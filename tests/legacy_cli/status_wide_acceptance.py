"""Native wide-tree status must stay clean with independent worktree/index/ref checks."""
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
    parser.add_argument("--files", type=int, default=10000)
    parser.add_argument("--directories", type=int, default=1000)
    parser.add_argument("--runs", type=int, default=60)
    args = parser.parse_args()
    assert args.files > 0 and args.directories > 0 and args.runs > 0
    binary = args.binary.resolve()
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    reference = shutil.which("git")
    assert reference
    results = []
    with tempfile.TemporaryDirectory(prefix="git-next-status-wide-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="0", GIT_CONFIG_GLOBAL=str(home / "config"))
        native_env = {**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty),
                      "GIT_SSH": str(empty / "missing"), "GIT_SSH_COMMAND": str(empty / "missing")}
        for object_format in ("sha1", "sha256"):
            repo = root / object_format

            def git(*arguments, data=None):
                process = subprocess.run([reference, *map(str, arguments)], cwd=repo if repo.exists() else root,
                                         env=env, input=data, capture_output=True, timeout=180)
                assert process.returncode == 0, (arguments, process.stderr)
                return process.stdout

            git("init", "-b", "main", "--object-format=" + object_format, repo)
            stream = bytearray()
            for number in range(args.files):
                data = f"benchmark fixture {number}\n".encode() * 16
                stream.extend(f"blob\nmark :{number + 1}\ndata {len(data)}\n".encode() + data + b"\n")
            message = b"fixture: native wide stability\n"
            stream.extend(b"commit refs/heads/main\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\n")
            stream.extend(f"data {len(message)}\n".encode() + message + b"\n")
            for number in range(args.files):
                stream.extend(f"M 100644 :{number + 1} dir-{number % args.directories:02d}/file-{number:06d}.txt\n".encode())
            stream.extend(b"\ndone\n")
            git("fast-import", "--quiet", "--done", data=bytes(stream))
            del stream
            git("config", "core.autocrlf", "input")
            git("config", "core.eol", "lf")
            git("reset", "--hard", "HEAD")
            stage = hashlib.sha256(git("ls-files", "--stage", "-z")).hexdigest()
            head = git("rev-parse", "HEAD")
            index = repo / ".git/index"
            for iteration in range(args.runs):
                variables = {**native_env}
                if iteration % 2:
                    variables["GIT_OPTIONAL_LOCKS"] = "0"
                before = index.read_bytes()
                process = subprocess.run([str(binary), "status", "--porcelain"], cwd=repo, env=variables,
                                         capture_output=True, timeout=60)
                assert process.returncode == 0 and process.stdout == b"", (object_format, iteration, process.returncode, process.stdout[:2048], process.stderr)
                if iteration % 2:
                    assert index.read_bytes() == before
                assert not (repo / ".git/index.lock").exists()
            assert stage == hashlib.sha256(git("ls-files", "--stage", "-z")).hexdigest()
            assert head == git("rev-parse", "HEAD")
            digest = hashlib.sha256()
            for number in range(args.files):
                relative = f"dir-{number % args.directories:02d}/file-{number:06d}.txt"
                data = (repo / relative).read_bytes()
                assert data == f"benchmark fixture {number}\n".encode() * 16, relative
                digest.update(relative.encode() + b"\0" + data)
            git("fsck", "--full")
            results.append({"object_format": object_format, "status_runs": args.runs,
                            "all_worktree_bytes_verified": True, "worktree_sha256": digest.hexdigest(),
                            "staged_object_ids_and_head_unchanged": True, "stage_sha256": stage,
                            "optional_lock_no_write_and_no_leaked_lock": True})
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == binary_hash
    print(json.dumps({"status": "pass", "scope": "Native wide-tree stability; independent bytes/stage/refs/fsck, not reference-status timing",
                      "binary_sha256": binary_hash, "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      "files": args.files, "directories": min(args.files, args.directories),
                      "results": results, "runtime_git_ssh_on_path": False,
                      "system_configuration": "enabled with isolated profiles", "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
