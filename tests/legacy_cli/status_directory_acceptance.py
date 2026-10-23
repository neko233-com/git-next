"""Status must retain both tracked deletions and untracked replacements at file/directory boundaries."""
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
    parser.add_argument("--diagnose", action="store_true")
    args = parser.parse_args()
    binary = args.binary.resolve()
    reference = shutil.which("git")
    assert reference
    checks = []
    mismatches = []
    with tempfile.TemporaryDirectory(prefix="git-next-status-directory-") as temporary:
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

            def run(native, *arguments, cwd=None):
                variables = dict(env)
                if native:
                    variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
                process = subprocess.run([str(binary) if native else reference, *map(str, arguments)],
                                         cwd=cwd or (repo if repo.exists() else root), env=variables,
                                         capture_output=True, timeout=30)
                assert process.returncode == 0, (arguments, process.returncode, process.stderr)
                return process.stdout

            def git(*arguments):
                return run(False, *arguments)

            git("init", "-b", "main", "--object-format=" + object_format, repo)
            git("config", "core.autocrlf", "input")
            (repo / "file").write_bytes(b"tracked file\n")
            (repo / "folder").mkdir()
            (repo / "folder/old.txt").write_bytes(b"tracked child\n")
            (repo / "tracked").mkdir()
            (repo / "tracked/keep.txt").write_bytes(b"tracked sibling\n")
            git("add", ".")
            git("commit", "-m", "fixture: file directory boundary")
            (repo / "file").unlink()
            (repo / "file").mkdir()
            (repo / "file/new.txt").write_bytes(b"untracked replacement child\n")
            (repo / "folder/old.txt").unlink()
            (repo / "folder").rmdir()
            (repo / "folder").write_bytes(b"untracked replacement file\n")
            (repo / "tracked/new.txt").write_bytes(b"untracked sibling\n")
            for name in ("loose/one.txt", "loose/deep/two.txt", "loose/subrepo/readme.txt"):
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"nested fixture\n")
            run(False, "init", "-b", "main", repo / "loose/subrepo")
            def compare(label, arguments):
                before = (repo / ".git/index").read_bytes()
                head = (repo / ".git/HEAD").read_bytes()
                expected = run(False, "status", *arguments)
                actual = run(True, "status", *arguments)
                if actual != expected:
                    if not args.diagnose:
                        raise AssertionError((object_format, label, arguments, actual, expected))
                    mismatches.append({"object_format": object_format, "label": label, "arguments": arguments,
                                       "actual": actual.decode(errors="backslashreplace"), "expected": expected.decode(errors="backslashreplace")})
                assert (repo / ".git/index").read_bytes() == before and (repo / ".git/HEAD").read_bytes() == head
                assert not (repo / ".git/index.lock").exists()
                checks.append(object_format + ": " + label + " " + " ".join(arguments))

            for arguments in (("--porcelain",), ("--porcelain", "-z"), ("--porcelain", "-uall"),
                              ("--porcelain", "--", "file"), ("--porcelain", "--", "file/*.txt"),
                              ("--porcelain", "--", "loose"), ("--porcelain", "--", "loose/*"),
                              ("--porcelain", "--", "loose", ":(exclude)loose/one.txt"),
                              ("--porcelain", "--", "tracked", ":(exclude)tracked/keep.txt")):
                compare("file/directory and nested repository", arguments)
            git("rm", "--cached", "file")
            for arguments in (("--porcelain",), ("--porcelain", "-z"), ("--porcelain", "-uall"), ("--porcelain", "-uno"), ("--porcelain", "--", "file")):
                compare("staged deletion and same-name untracked directory", arguments)
            git("reset", "--hard", "HEAD")
            (repo / "file").unlink()
            (repo / "file").mkdir()
            compare("tracked file replaced by empty directory", ("--porcelain",))
            git("reset", "--hard", "HEAD")
            git("rm", "--cached", "file")
            for arguments in (("--porcelain",), ("--porcelain", "-z"), ("--porcelain", "-uall"), ("--porcelain", "--", "file")):
                compare("staged deletion retains same-name untracked file", arguments)
            git("fsck", "--full")
    print(json.dumps({"status": "fail" if mismatches else "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      "checks": checks, "mismatches": mismatches, "runtime_git_ssh_on_path": False}, indent=2))
    if mismatches:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
