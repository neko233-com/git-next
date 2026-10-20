"""Native diff output/exit-code comparisons with no Git on the client PATH."""
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
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-diff-") as temporary:
        root = Path(temporary)
        home, repo, empty = root / "home", root / "repo", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def run(native, *command, extra=None, cwd=repo, content=None):
            variables = {**env, **(extra or {})}
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            return subprocess.run([binary if native else reference, *map(str, command)], cwd=cwd,
                                  env=variables, input=content, capture_output=True, timeout=30)

        def git(*command, cwd=repo):
            result = run(False, *command, cwd=cwd)
            assert result.returncode == 0, result.stderr
            return result.stdout

        def snapshot():
            return {str(path.relative_to(repo)): hashlib.sha256(path.read_bytes()).hexdigest()
                    for path in (repo / ".git").rglob("*") if path.is_file()}

        def compare(*command, cwd=repo):
            # Reference diff also has optional index refresh; disable it for the independent oracle.
            expected = run(False, *command, cwd=cwd, extra={"GIT_OPTIONAL_LOCKS": "0"})
            before = snapshot()
            actual = run(True, *command, cwd=cwd)
            assert (actual.returncode, actual.stdout) == (expected.returncode, expected.stdout), (
                command, "expected", expected.returncode, expected.stdout, "actual", actual.returncode, actual.stdout, actual.stderr)
            assert snapshot() == before, "diff wrote repository files"
            return actual.stdout

        git("init", "-b", "main", repo, cwd=root)
        files = {"first.txt": b"one\ntwo\nthree\n", "delete.txt": b"delete me\n",
                 "empty.txt": b"", "no-newline.txt": b"no newline", "binary.dat": b"\x00binary old\n",
                 "asset with spaces.txt": b"space old\n", "中文.txt": "旧行\n".encode(),
                 "dir/nested.txt": b"nested old\n"}
        for name, data in files.items():
            path = repo / name
            path.parent.mkdir(exist_ok=True)
            path.write_bytes(data)
        git("add", ".")
        git("commit", "-m", "fixture: original")
        for flags in ([], ["--quiet"], ["--exit-code"], ["--stat"], ["--numstat"], ["-R"],
                      ["--no-patch"], ["--cached"], ["HEAD"], ["HEAD", "HEAD"],
                      ["--", "dir"], ["--", "no-such-file"]):
            compare("diff", *flags)
        checks.append("clean worktree output/exit modes, staged/tree comparisons and pathspecs preserve every repository byte")
        (repo / "first.txt").write_bytes(b"one\nmodified\nthree\nfour\n")
        (repo / "delete.txt").unlink()
        (repo / "empty.txt").write_bytes(b"now not empty\n")
        (repo / "no-newline.txt").write_bytes(b"changed no newline")
        (repo / "binary.dat").write_bytes(b"\x00binary changed\n")
        (repo / "asset with spaces.txt").write_bytes(b"space new\n")
        (repo / "中文.txt").write_bytes("新行\n".encode())
        (repo / "dir/nested.txt").write_bytes(b"nested new\n")
        (repo / "untracked.txt").write_bytes(b"not part of diff\n")
        for flags in [[], ["-U0"], ["--name-only"], ["--name-status"], ["--numstat"],
                      ["--name-only", "-z"], ["--name-status", "-z"], ["--full-index"],
                      ["--exit-code"], ["--quiet"], ["-R"], ["--no-patch"], ["--stat"], ["--shortstat"], ["--stat", "-p"], ["--numstat", "-p"]]:
            compare("diff", *flags)
        for pathspec in [["."], ["first.txt"], ["dir"], ["*.txt"], [":(exclude)binary.dat"], ["中文.txt"]]:
            compare("diff", "--", *pathspec)
        compare("diff", "first.txt", "delete.txt")
        compare("diff", "first.txt", "dir/nested.txt")
        compare("diff", "delete.txt")
        compare("diff", "*.txt")
        compare("-c", "core.quotePath=false", "diff")
        compare("-c", "core.abbrev=false", "diff")
        compare("-c", "core.abbrev=12", "diff")
        compare("diff", "--", "nested.txt", cwd=repo / "dir")
        compare("diff", cwd=repo / "dir")
        checks.append("unstaged patches, binary/empty/non-newline/Unicode/space paths, pathspecs and exit status match reference")

        git("add", "-A")
        (repo / "intent.txt").write_bytes(b"intent to add\n")
        git("add", "-N", "intent.txt")
        compare("diff")
        compare("diff", "--cached")
        compare("diff", "--cached", "--numstat")
        git("add", "intent.txt")
        (repo / "intent-empty.txt").write_bytes(b"")
        git("add", "-N", "intent-empty.txt")
        compare("diff")
        compare("diff", "--quiet")
        compare("diff", "--cached")
        git("add", "intent-empty.txt")
        git("commit", "-m", "fixture: staged")
        compare("diff", "HEAD~1", "HEAD")
        compare("diff", "HEAD~1..HEAD")
        compare("diff", "HEAD~1")
        checks.append("cached/revision/range diffs and intent-to-add retain distinct index/worktree behavior")

        git("mv", "first.txt", "renamed.txt")
        for flags in [[], ["--name-status"], ["--name-status", "-z"], ["--numstat"], ["--numstat", "-z"], ["--no-renames"]]:
            compare("diff", "--cached", *flags)
        checks.append("exact renames, name/status/NUL/numstat formats and explicit rename disabling")
        for command in [("show",), ("show", "HEAD~1"), ("show", "--oneline"),
                        ("show", "--pretty=oneline", "--abbrev-commit", "--no-patch"),
                        ("show", "--no-patch"), ("show", "--format=short", "--no-patch"),
                        ("show", "--format=full", "--no-patch"), ("show", "--format=fuller", "--no-patch"),
                        ("show", "--format=raw", "--no-patch"),
                        ("show", "--no-patch", "--format=%H%x00%h%x00%P%x00%an%x00%ae%x00%at%x00%s%x00%B"),
                        ("show", "--no-patch", "--format=format:%H"),
                        ("show", "--no-patch", "--date=iso"), ("show", "--format=", "--", "*.txt"),
                        ("show", "HEAD:first.txt"), ("show", "HEAD:binary.dat"), ("show", "HEAD:dir"),
                        ("show", "--numstat"), ("show", "--name-only"), ("show", "--stat"), ("show", "--stat", "-p"),
                        ("show", "--oneline", "--stat", "-p"), ("show", "--format=", "--stat", "-p"), ("show", "--numstat", "-p")]:
            compare(*command)
        git("tag", "-a", "snapshot", "-m", "fixture tag notes")
        compare("show", "snapshot", "--no-patch")
        checks.append("show commit/blob/tree/tag, pretty formats, signature/date fields and NUL templates match reference")
        git("config", "core.symlinks", "false")
        git("config", "core.autocrlf", "true")
        target_bytes = b"old/target\r\n"
        oid = run(False, "hash-object", "-w", "--stdin", content=target_bytes).stdout.strip().decode()
        git("update-index", "--add", "--cacheinfo", "120000", oid, "fake-link.txt")
        (repo / "fake-link.txt").write_bytes(b"new/target\r\n")
        compare("diff", "--", "fake-link.txt")
        staged = run(True, "add", "fake-link.txt")
        assert staged.returncode == 0, staged.stderr
        assert git("show", ":fake-link.txt") == b"new/target\r\n"
        assert git("ls-files", "--stage", "fake-link.txt").startswith(b"120000 ")
        checks.append("Windows-style emulated symlink target bytes and mode survive diff/add with CRLF enabled")
        seen = {}
        for number in range(10000):
            data = f"abbreviation collision fixture {number}\n".encode()
            digest = hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest()
            if digest[:4] in seen:
                first, second = seen[digest[:4]], data
                break
            seen[digest[:4]] = data
        else:
            raise AssertionError("no deterministic fixture prefix collision")
        first_oid = run(False, "hash-object", "-w", "--stdin", content=first).stdout.strip().decode()
        git("update-index", "--add", "--cacheinfo", "100644", first_oid, "collision.txt")
        (repo / "collision.txt").write_bytes(second)
        compare("-c", "core.abbrev=4", "diff", "--", "collision.txt")
        checks.append("configured/full object IDs and prefix collisions include unstored worktree blobs without writing them")
        git("config", "core.autocrlf", "false")
        (repo / ".gitattributes").write_bytes(b"*.crlf text eol=crlf\n*.opaque -diff\n")
        (repo / "text.crlf").write_bytes(b"first\r\nsecond\r\n")
        (repo / "plain.opaque").write_bytes(b"opaque first\n")
        long = [f"row {number:03d} original\n".encode() for number in range(100)]
        (repo / "long.txt").write_bytes(b"".join(long))
        (repo / "dir/fuzzy-before.txt").write_bytes(b"".join(long))
        git("add", "-A")
        git("commit", "-m", "fixture: rich diffs")
        long[20] = b"row 020 changed\n"
        long[70] = b"row 070 changed\n"
        (repo / "long.txt").write_bytes(b"".join(long))
        (repo / "text.crlf").write_bytes(b"first\r\nmodified\r\n")
        (repo / "plain.opaque").write_bytes(b"opaque second\n")
        for flags in [[], ["-U0"], ["-U1"], ["--numstat"], ["--stat"], ["--text"], ["--diff-algorithm=histogram"]]:
            compare("diff", *flags)
        git("mv", "dir/fuzzy-before.txt", "dir/fuzzy-after.txt")
        (repo / "dir/fuzzy-after.txt").write_bytes(b"".join(long))
        git("add", "dir/fuzzy-after.txt")
        for flags in [[], ["--name-status"], ["--stat"], ["--numstat"], ["--numstat", "-z"]]:
            compare("diff", "--cached", *flags)
        checks.append("multiple hunks, context headers, CRLF/attribute binary rules, algorithms and edited renames match reference")
        git("update-index", "--chmod=+x", "long.txt")
        for flags in [[], ["--name-status"], ["--stat"]]:
            compare("diff", "--cached", *flags)
        git("config", "core.symlinks", "true")
        for flags in [[], ["--name-status"], ["--numstat"], ["--stat"], ["-R"]]:
            compare("diff", *flags, "--", "fake-link.txt")
        checks.append("executable-bit changes remain modifications and file-type changes produce separate removal/addition patches")
        for command in [("log",), ("log", "--oneline"), ("log", "-1"), ("log", "-2", "HEAD~1"),
                        ("log", "-n", "1"), ("log", "-n0"), ("log", "--format=short"), ("log", "--format=fuller"),
                        ("log", "--format=%H%x00%P%x00%at%x00%s"),
                        ("log", "-z", "--format=%H%x00%ct"), ("log", "--format=format:%H"),
                        ("log", "--date=iso", "--no-decorate", "--no-color"), ("log", "snapshot")]:
            compare(*command)
        checks.append("log shares exact commit/date/template formatting, numeric limits, tag peeling and NUL record output")
        for command in [("diff", "--quiet", "--diff-algorithm=unknown"),
                        ("show", "--color=always"), ("show", "--name-only", "--stat"),
                        ("show", "-U1000001"), ("log", "--", "long.txt")]:
            before = snapshot()
            result = run(True, *command)
            assert result.returncode != 0 and not result.stdout, (command, result.stdout, result.stderr)
            assert snapshot() == before, "unsupported read command wrote repository files"
        checks.append("unsupported render/history options fail before output or repository writes")
        git("fsck", "--full")
        print(json.dumps({"status": "pass", "checks": checks, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
