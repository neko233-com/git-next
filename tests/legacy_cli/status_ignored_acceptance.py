"""Reference conformance for ignored status, pathspecs, grouping and no-write failures."""
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
    parser.add_argument("--quick", action="store_true")
    parser.add_argument("--diagnose", action="store_true")
    args = parser.parse_args()
    binary = args.binary.resolve()
    reference = shutil.which("git")
    assert reference
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    checks = []
    mismatches = []
    symlinks_tested = []
    with tempfile.TemporaryDirectory(prefix="git-next-status-ignored-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith(("GIT_", "GIT_NEXT_"))}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"), SSH_ROOT_PATH=str(home / ".ssh"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / "config"), GIT_OPTIONAL_LOCKS="0",
                   GIT_AUTHOR_NAME="Fixture", GIT_COMMITTER_NAME="Fixture",
                   GIT_AUTHOR_EMAIL="fixture@example.invalid", GIT_COMMITTER_EMAIL="fixture@example.invalid")
        for object_format in ("sha1", "sha256"):
            repo = root / object_format

            def run(native, *arguments, cwd=None, success=True):
                variables = dict(env)
                if native:
                    variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
                result = subprocess.run([str(binary) if native else reference, *map(str, arguments)],
                                        cwd=cwd or (repo if repo.exists() else root), env=variables, capture_output=True, timeout=30)
                if success:
                    assert result.returncode == 0, (arguments, result.stderr)
                return result

            def git(*arguments, **kwargs):
                return run(False, *arguments, **kwargs).stdout

            def snapshot():
                return {p.relative_to(repo / ".git").as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                        for p in (repo / ".git").rglob("*") if p.is_file()}

            def compare(label, *arguments, cwd=None):
                before = snapshot()
                expected = run(False, *arguments, cwd=cwd, success=False)
                actual = run(True, *arguments, cwd=cwd, success=False)
                assert expected.returncode == 0 or (expected.returncode == 128 and b"Unsupported combination" in expected.stderr), expected.stderr
                if actual.returncode != expected.returncode or actual.stdout != expected.stdout:
                    mismatch = {"format": object_format, "fixture": label, "arguments": arguments,
                                "actual_exit": actual.returncode, "expected_exit": expected.returncode,
                                "actual": actual.stdout.decode(errors="backslashreplace"),
                                "expected": expected.stdout.decode(errors="backslashreplace")}
                    if not args.diagnose:
                        raise AssertionError(mismatch)
                    mismatches.append(mismatch)
                assert (actual.stderr == b"") if actual.returncode == 0 else bool(actual.stderr), actual.stderr
                assert snapshot() == before, (label, "repository changed")
                checks.append({"format": object_format, "fixture": label, "arguments": arguments,
                               "reference_exit_code": expected.returncode})

            git("init", "-q", "-b", "main", "--object-format=" + object_format, repo)
            git("config", "core.autocrlf", "input")
            git("config", "core.eol", "lf")
            (repo / "tracked").mkdir()
            (repo / ".gitignore").write_bytes(b"/explicit/\n/empty-ignored/\n/nested-empty/\n*.tmp\nhidden.txt\n/mixed/ignore/\n*.cache\n!keep.cache\n")
            (repo / "tracked/.gitignore").write_bytes(b"*.local\n")
            (repo / "tracked/keep.txt").write_bytes(b"tracked\n")
            (repo / "changed.txt").write_bytes(b"old\n")
            git("add", ".")
            (repo / "explicit").mkdir()
            (repo / "explicit/tracked.txt").write_bytes(b"forced tracked\n")
            git("add", "-f", "explicit/tracked.txt")
            git("commit", "-qm", "fixture: ignored status")
            (repo / "changed.txt").write_bytes(b"real edit\n")
            (repo / "explicit/tracked.txt").write_bytes(b"forced tracked real edit\n")
            (repo / "empty-ignored").mkdir()
            (repo / "nested-empty/deep/empty").mkdir(parents=True)
            for name in ("explicit/a.txt", "explicit/yes.tmp", "explicit/deep/b.txt", "all-by-pattern/a.tmp", "all-by-pattern/deep/b.tmp",
                         "mixed/visible.txt", "mixed/hidden.tmp", "mixed/ignore/a.txt", "loose/visible.txt", "loose/a.tmp",
                         "tracked/secret.local", "hidden.txt", "a.tmp", "space name.tmp", "文-é-🚀.tmp", "keep.cache",
                         "new.cache", "global.global", "private.info"):
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"fixture\n")
            if os.name != "nt":
                for name in ("tab\tname.tmp", "line\nbreak.tmp", "raw-\udcff.tmp"):
                    (repo / name).write_bytes(b"raw filename fixture\n")
            outside = root / (object_format + "-outside")
            outside.mkdir()
            (outside / "must-not-traverse.tmp").write_bytes(b"outside fixture\n")
            try:
                os.symlink(outside, repo / "link.tmp", target_is_directory=True)
                symlinks_tested.append(object_format)
            except OSError as error:
                if os.name != "nt" or error.winerror != 1314:
                    raise
            (repo / ".git/info/exclude").write_bytes(b"*.info\n")
            (home / "ignore").write_bytes(b"*.global\n")
            git("config", "core.excludesFile", home / "ignore")
            formats = [("--porcelain",), ("--porcelain=v2",)] if args.quick else [
                ("--porcelain",), ("--porcelain", "-z"), ("--porcelain=v2",), ("--porcelain=v2", "-z"), ("--short",)]
            selections = [(), ("--", "explicit"), ("--", "all-by-pattern")] if args.quick else [
                (), ("--", "explicit"), ("--", "explicit/deep/b.txt"), ("--", "all-by-pattern"),
                ("--", "*.tmp"), ("--", "mixed", ":(exclude)mixed/ignore"), ("--", "loose")]
            for mode in ("--ignored", "--ignored=traditional", "--ignored=matching", "--ignored=no"):
                for untracked in ("normal", "all", "no"):
                    for style in formats:
                        for selection in selections:
                            compare("ignored/untracked/style/pathspec", "status", mode, "--untracked-files=" + untracked, *style, *selection)
            for mode in ("traditional", "matching", "no"):
                compare("short relative paths", "status", "--ignored=" + mode, "--short", cwd=repo / "tracked")
                compare("untracked config no", "-c", "status.showUntrackedFiles=no", "status", "--ignored=" + mode, "--porcelain")
                compare("CLI all overrides config no", "-c", "status.showUntrackedFiles=no", "status", "--ignored=" + mode, "-uall", "--porcelain=v2")
                compare("untracked config all", "-c", "status.showUntrackedFiles=all", "status", "--ignored=" + mode, "--porcelain=v2")
            (repo / "explicit/nested-repository").mkdir()
            (repo / "explicit/nested-repository/visible.txt").write_bytes(b"nested repository\n")
            git("init", "-q", "-b", "main", repo / "explicit/nested-repository")
            for mode in ("traditional", "matching"):
                for untracked in ("normal", "all"):
                    compare("ignored nested repository boundary", "status", "--ignored=" + mode,
                            "--untracked-files=" + untracked, "--porcelain=v2")
            git("rm", "-f", "--cached", "explicit/tracked.txt")
            for mode in ("traditional", "matching", "no"):
                for style in (("--porcelain",), ("--porcelain=v2", "-z")):
                    compare("staged deletion and same-name ignored file", "status", "--ignored=" + mode, "-uall", *style)
            for arguments in (("status", "--ignored=bad"), ("status", "--ignored="),
                              ("-c", "core.quotePath=invalid", "status", "--ignored", "--porcelain")):
                before = snapshot()
                result = run(True, *arguments, success=False)
                assert result.returncode != 0 and result.stdout == b"" and snapshot() == before, arguments
            git("fsck", "--full")
    assert digest == hashlib.sha256(binary.read_bytes()).hexdigest()
    print(json.dumps({"status": "fail" if mismatches else "pass", "scope": "Ignored status and ordinary pathspec subset; no full Git replacement claim",
                      "binary_sha256": digest, "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      "checks": checks, "mismatches": mismatches, "symlink_formats_tested": symlinks_tested,
                      "runtime_git_ssh_on_path": False, "repository_snapshots_preserved": True}, indent=2))
    if mismatches:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
