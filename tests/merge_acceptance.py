"""Native clean two-parent merge state compared with Git, including fail-before-publication cases."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def snapshot(root):
    return {path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file()}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--object-format", choices=("sha1", "sha256"), default="sha1")
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-merge-") as temporary:
        root = Path(temporary)
        home = root / "home"
        home.mkdir()
        empty = root / "empty"
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_GLOBAL=str(home / "config"), GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0",
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2001-02-03T04:05:06+0000", GIT_COMMITTER_DATE="2001-02-03T04:05:06+0000",
                   GIT_MERGE_AUTOEDIT="no")

        def run(client, *command, cwd=root, success=True, code=None):
            variables = {**env}
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty), GIT_SSH=str(empty / "absent-ssh"))
            result = subprocess.run([binary if client else reference, *map(str, command)],
                                    cwd=cwd, env=variables, capture_output=True, timeout=30)
            assert (result.returncode == 0) == success, (command, result.returncode, result.stderr)
            if code is not None:
                assert result.returncode == code, (command, result.returncode, result.stderr)
            return result.stdout

        fixture = root / "fixture"
        run(False, "init", "-b", "main", "--object-format=" + args.object_format, fixture)
        run(False, "config", "core.autocrlf", "false", cwd=fixture)
        (fixture / ".gitattributes").write_bytes(b"* text=auto eol=lf\n*.bin -text\n")
        (fixture / "shared.txt").write_bytes(b"".join(f"line {number}\n".encode() for number in range(40)))
        (fixture / "stable.bin").write_bytes(bytes(range(256)))
        (fixture / "rename.txt").write_bytes(b"rename fixture\n" * 50)
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "base", cwd=fixture)
        base = run(False, "rev-parse", "HEAD", cwd=fixture).strip().decode()
        run(False, "switch", "-c", "topic", cwd=fixture)
        data = (fixture / "shared.txt").read_bytes().replace(b"line 39\n", b"their final line\n")
        (fixture / "shared.txt").write_bytes(data)
        (fixture / "their.txt").write_bytes(b"their new file\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "theirs", cwd=fixture)
        run(False, "switch", "main", cwd=fixture)
        data = (fixture / "shared.txt").read_bytes().replace(b"line 0\n", b"our first line\n")
        (fixture / "shared.txt").write_bytes(data)
        (fixture / "our.txt").write_bytes(b"our new file\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "ours", cwd=fixture)

        sequence = 0

        def pair(setup=None):
            nonlocal sequence
            sequence += 1
            paths = [root / f"reference-{sequence}", root / f"native-{sequence}"]
            for path in paths:
                shutil.copytree(fixture, path)
                if setup:
                    setup(path)
            return paths

        def verify(expected, actual):
            left_commit = run(False, "cat-file", "commit", "HEAD", cwd=expected)
            right_commit = run(False, "cat-file", "commit", "HEAD", cwd=actual)
            assert right_commit == left_commit, ("commit", left_commit, right_commit)
            for command in (("rev-parse", "HEAD"), ("rev-parse", "HEAD^{tree}"),
                            ("rev-list", "--parents", "-1", "HEAD"), ("show", "-s", "--format=%B", "HEAD"),
                            ("rev-parse", "ORIG_HEAD"), ("status", "--porcelain"), ("ls-files", "--stage")):
                left, right = run(False, *command, cwd=expected), run(False, *command, cwd=actual)
                assert right == left, (command, left, right)
            for name in ("HEAD", "logs/HEAD", "logs/refs/heads/main"):
                left, right = expected / ".git" / name, actual / ".git" / name
                assert right.read_bytes() == left.read_bytes(), name
            expected_files = {name: value for name, value in snapshot(expected).items() if not name.startswith(".git/")}
            actual_files = {name: value for name, value in snapshot(actual).items() if not name.startswith(".git/")}
            assert actual_files == expected_files
            run(False, "fsck", "--full", cwd=actual)
            assert not list((actual / ".git").glob("git-next-checkout-*"))

        def compare(arguments, setup=None):
            expected, actual = pair(setup)
            run(False, "merge", *arguments, cwd=expected)
            run(True, "merge", *arguments, cwd=actual)
            verify(expected, actual)

        compare(("--no-edit", "topic"))
        compare(("-m", "fixture merge\n\nUnicode 合并", "topic"))
        compare(("--no-ff", "-m", "explicit two-parent merge", "topic"))
        compare(("-q", "--no-edit", "topic"))
        checks.append("disjoint and same-file nonoverlapping edits produce exact merge commit/tree/parents, index, worktree, ORIG_HEAD and reflogs")

        def fast_forward(path):
            run(False, "reset", "--hard", base, cwd=path)

        compare(("--no-edit", "topic"), fast_forward)
        compare(("--ff-only", "topic"), fast_forward)
        compare(("--no-ff", "-m", "forced merge", "topic"), fast_forward)
        compare(("topic",), lambda path: (fast_forward(path), run(False, "config", "merge.ff", "false", cwd=path)))
        compare(("--ff", "topic"), lambda path: (fast_forward(path), run(False, "config", "merge.ff", "false", cwd=path)))
        compare(("--no-edit", "topic"), lambda path: run(False, "config", "merge.suppressDest", "main", cwd=path))
        compare(("--no-edit", "topic"), lambda path: run(False, "config", "merge.log", "false", cwd=path))
        compare(("HEAD",))
        compare((base,))
        compare(("HEAD",), lambda path: (path / "our.txt").write_bytes(b"unrelated dirty data\n"))
        compare(("--no-edit", "topic"), lambda path: run(False, "switch", "--detach", "HEAD", cwd=path))
        compare(("--no-edit", "topic"), lambda path: run(False, "switch", "-c", "integration", cwd=path))
        checks.append("default/forced/ff-only merges and merge.ff/merge.suppressDest settings match reference")

        def crlf(path):
            with (path / ".gitattributes").open("ab") as file:
                file.write(b"shared.txt text eol=crlf\n")
            run(False, "add", ".gitattributes", cwd=path)
            run(False, "commit", "-m", "CRLF checkout rule", cwd=path)
            (path / "shared.txt").unlink()
            run(False, "reset", "--hard", "HEAD", cwd=path)
            assert b"\r\n" in (path / "shared.txt").read_bytes()
            assert run(False, "status", "--porcelain", cwd=path) == b""

        compare(("--no-edit", "topic"), crlf)

        def rename(path):
            run(False, "mv", "shared.txt", "renamed.txt", cwd=path)
            run(False, "commit", "-m", "rename", cwd=path)

        compare(("--no-edit", "topic"), rename)

        def same_tree(path):
            run(False, "reset", "--hard", "topic", cwd=path)
            tree = run(False, "rev-parse", "topic^{tree}", cwd=path).strip().decode()
            commit = run(False, "commit-tree", tree, "-p", base, "-m", "same tree, different history", cwd=path).strip().decode()
            run(False, "update-ref", "refs/heads/main", commit, cwd=path)

        compare(("--no-edit", "topic"), same_tree)
        checks.append("detached/no-op/locally-ahead and identical-tree merges, rename/edit and explicit CRLF checkout rules preserve Git state")

        def refuse(arguments, setup=None):
            _, actual = pair(setup)
            before = snapshot(actual)
            run(True, "merge", *arguments, cwd=actual, success=False)
            assert snapshot(actual) == before, arguments

        refuse(("--ff-only", "topic"))
        refuse(("topic",), lambda path: (path / "shared.txt").write_bytes(b"dirty fixture\n"))
        refuse(("topic",), lambda path: (path / "their.txt").write_bytes(b"untracked fixture\n"))
        for name in ("index.lock", "HEAD.lock", "refs/heads/main.lock", "ORIG_HEAD.lock"):
            refuse(("topic",), lambda path, name=name: (path / ".git" / name).write_bytes(b"another writer\n"))
        refuse(("topic",), lambda path: run(False, "config", "commit.gpgSign", "true", cwd=path))
        refuse(("topic",), lambda path: run(False, "config", "commit.gpgSign", "invalid-fixture", cwd=path))
        refuse(("topic",), lambda path: run(False, "config", "merge.verifySignatures", "invalid-fixture", cwd=path))
        refuse(("topic",), lambda path: run(False, "config", "merge.fixture.driver", "must-not-execute", cwd=path))
        refuse(("--squash", "topic"))
        refuse(("--no-ff", "-m", "", "topic"))
        refuse(("topic",), lambda path: run(False, "update-index", "--skip-worktree", "shared.txt", cwd=path))
        refuse(("topic",), lambda path: (fast_forward(path), run(False, "config", "commit.cleanup", "invalid-fixture", cwd=path)))
        refuse(("--cleanup=invalid-fixture", "topic"), fast_forward)
        refuse(("topic",), lambda path: (fast_forward(path), run(False, "config", "commit.gpgSign", "invalid-fixture", cwd=path)))
        refuse(("topic",), lambda path: run(False, "config", "merge.autostash", "true", cwd=path))
        refuse(("fixture-tag",), lambda path: run(False, "tag", "-a", "fixture-tag", "topic", "-m", "annotated fixture", cwd=path))
        checks.append("dirty/untracked collisions, all writer locks, unsupported drivers/signing/options and empty message refuse without writing candidate objects")

        def conflict(path):
            (path / "shared.txt").write_bytes((path / "shared.txt").read_bytes().replace(b"line 39\n", b"our conflicting last line\n"))
            run(False, "add", ".", cwd=path)
            run(False, "commit", "-m", "conflict", cwd=path)

        expected, actual = pair(conflict)
        run(False, "merge", "--no-edit", "topic", cwd=expected, success=False, code=1)
        run(True, "merge", "--no-edit", "topic", cwd=actual, success=False, code=1)
        for command in (("rev-parse", "HEAD"), ("rev-parse", "ORIG_HEAD"), ("rev-parse", "AUTO_MERGE"),
                        ("ls-files", "--stage"), ("status", "--porcelain")):
            left = run(False, *command, cwd=expected)
            right = run(True, *command, cwd=actual)
            assert left == right, (command, left, right)
        for name in ("MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "logs/HEAD", "logs/refs/heads/main"):
            left, right = (expected / ".git" / name).read_bytes(), (actual / ".git" / name).read_bytes()
            assert right == left, (name, left, right)
        assert (actual / "shared.txt").read_bytes() == (expected / "shared.txt").read_bytes()
        for path in (expected, actual):
            (path / "shared.txt").write_bytes(b"resolved fixture\n")
            run(path == actual, "add", "shared.txt", cwd=path)
        env["GIT_EDITOR"] = "true"
        run(False, "merge", "--continue", cwd=expected)
        run(True, "merge", "--continue", cwd=actual)
        verify(expected, actual)
        for name in ("MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "AUTO_MERGE", "git-next-merge.json"):
            assert not (actual / ".git" / name).exists(), name
        expected, actual = pair(conflict)
        run(False, "merge", "topic", cwd=expected, success=False, code=1)
        run(True, "merge", "topic", cwd=actual, success=False, code=1)
        for path in (expected, actual):
            (path / "our.txt").write_bytes(b"unrelated post-merge change\n")
            (path / "untracked.txt").write_bytes(b"untracked fixture must survive\n")
        run(False, "merge", "--abort", cwd=expected)
        run(True, "merge", "--abort", cwd=actual)
        verify(expected, actual)
        for name in ("MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "AUTO_MERGE", "git-next-merge.json"):
            assert not (actual / ".git" / name).exists(), name
        checks.append("native abort restores the original merge paths and metadata while preserving unrelated dirty and untracked files")

        for new_file in (False, True):
            expected, actual = pair(conflict)
            for path in (expected, actual):
                run(path == actual, "merge", "topic", cwd=path, success=False, code=1)
                name = "post-merge-added.txt" if new_file else "our.txt"
                (path / name).write_bytes(b"unrelated post-merge staged change\n")
                run(path == actual, "add", name, cwd=path)
            run(False, "merge", "--abort", cwd=expected)
            run(True, "merge", "--abort", cwd=actual)
            verify(expected, actual)
        checks.append("abort follows reset-merge semantics for unrelated post-merge staged edits and additions")

        def multiple_conflicts(path):
            run(False, "switch", "topic", cwd=path)
            (path / "rename.txt").write_bytes(b"their rename line\n" + b"rename fixture\n" * 49)
            run(False, "add", ".", cwd=path)
            run(False, "commit", "-m", "their second conflict", cwd=path)
            run(False, "switch", "main", cwd=path)
            (path / "rename.txt").write_bytes(b"our rename line\n" + b"rename fixture\n" * 49)
            conflict(path)

        expected, actual = pair(multiple_conflicts)
        run(False, "merge", "topic", cwd=expected, success=False, code=1)
        run(True, "merge", "topic", cwd=actual, success=False, code=1)
        for path in (expected, actual):
            (path / "shared.txt").write_bytes(b"first resolution\n")
            run(path == actual, "add", "shared.txt", cwd=path)
        assert run(True, "ls-files", "--stage", cwd=actual) == run(False, "ls-files", "--stage", cwd=expected)
        assert run(True, "status", "--porcelain", cwd=actual) == run(False, "status", "--porcelain", cwd=expected)
        before = snapshot(actual)
        run(True, "merge", "--continue", cwd=actual, success=False)
        assert snapshot(actual) == before
        for path in (expected, actual):
            (path / "rename.txt").write_bytes(b"second resolution\n")
            run(path == actual, "add", "rename.txt", cwd=path)
        run(False, "commit", "--no-edit", cwd=expected)
        run(True, "commit", "--no-edit", cwd=actual)
        verify(expected, actual)
        checks.append("partial resolution preserves every unselected conflict stage; unresolved continue is read-only and commit --no-edit completes after all resolutions")

        expected, actual = pair(conflict)
        for path in (expected, actual):
            run(False, "merge", "topic", cwd=path, success=False, code=1)
            (path / "shared.txt").write_bytes(b"existing Git merge resolution\n")
            run(path == actual, "add", "shared.txt", cwd=path)
        run(False, "merge", "--continue", cwd=expected)
        run(True, "merge", "--continue", cwd=actual)
        verify(expected, actual)
        checks.append("an existing standard Git single-head merge can be resolved and continued without resetting its state")

        for name in ("MERGE_HEAD.lock", "MERGE_MSG.lock", "MERGE_MODE.lock", "git-next-merge.json.lock", "AUTO_MERGE.lock"):
            refuse(("topic",), lambda path, name=name: (conflict(path), (path / ".git" / name).write_bytes(b"other writer\n")))
        _, actual = pair(conflict)
        run(True, "merge", "topic", cwd=actual, success=False, code=1)
        for name in ("index.lock", "HEAD.lock", "MERGE_HEAD.lock", "MERGE_MSG.lock", "AUTO_MERGE.lock"):
            lock = actual / ".git" / name
            lock.write_bytes(b"other writer\n")
            before = snapshot(actual)
            run(True, "merge", "--abort", cwd=actual, success=False)
            assert snapshot(actual) == before
            lock.unlink()
        checks.append("merge metadata/AUTO_MERGE locks reject start or abort without publishing any partial state")

        _, actual = pair(conflict)
        run(True, "merge", "topic", cwd=actual, success=False, code=1)
        (actual / "shared.txt").write_bytes(b"staged resolution\n")
        run(True, "add", "shared.txt", cwd=actual)
        (actual / "shared.txt").write_bytes(b"new unstaged edit after resolution\n")
        before = snapshot(actual)
        run(True, "merge", "--abort", cwd=actual, success=False)
        assert snapshot(actual) == before
        run(False, "add", "shared.txt", cwd=actual)
        run(False, "rm", "--cached", "shared.txt", cwd=actual)
        before = snapshot(actual)
        run(True, "merge", "--abort", cwd=actual, success=False)
        assert snapshot(actual) == before
        checks.append("abort preserves unstaged edits after a staged resolution and refuses to overwrite a newly untracked original file")

        def add_add(path):
            run(False, "switch", "topic", cwd=path)
            (path / "both.txt").write_bytes(b"their added file\n")
            run(False, "add", ".", cwd=path)
            run(False, "commit", "-m", "their addition", cwd=path)
            run(False, "switch", "main", cwd=path)
            (path / "both.txt").write_bytes(b"our added file\n")
            run(False, "add", ".", cwd=path)
            run(False, "commit", "-m", "our addition", cwd=path)

        for setup in (add_add, conflict):
            expected, actual = pair(setup)
            run(False, "merge", "topic", cwd=expected, success=False, code=1)
            run(True, "merge", "topic", cwd=actual, success=False, code=1)
            assert run(True, "status", "--porcelain", cwd=actual) == run(False, "status", "--porcelain", cwd=expected)
            assert run(True, "ls-files", "--stage", cwd=actual) == run(False, "ls-files", "--stage", cwd=expected)
            run(False, "merge", "--abort", cwd=expected)
            run(True, "merge", "--abort", cwd=actual)
            verify(expected, actual)
        checks.append("add/add AA status and native abort match standard Git alongside both-modified UU conflicts")

        for style in ("diff3", "zdiff3"):
            expected, actual = pair(conflict)
            for path in (expected, actual):
                run(False, "config", "merge.conflictStyle", style, cwd=path)
                run(path == actual, "merge", "topic", cwd=path, success=False, code=1)
            left, right = (expected / "shared.txt").read_bytes(), (actual / "shared.txt").read_bytes()
            assert right == left, (style, left, right)
            assert run(True, "rev-parse", "AUTO_MERGE", cwd=actual) == run(False, "rev-parse", "AUTO_MERGE", cwd=expected)
            run(False, "merge", "--abort", cwd=expected)
            run(True, "merge", "--abort", cwd=actual)
            verify(expected, actual)
        checks.append("diff3/zdiff3 markers, ancestor labels and AUTO_MERGE trees match reference")

        def rename_conflict(path):
            run(False, "switch", "topic", cwd=path)
            run(False, "mv", "rename.txt", "their-name.txt", cwd=path)
            run(False, "commit", "-m", "their rename", cwd=path)
            run(False, "switch", "main", cwd=path)
            run(False, "mv", "rename.txt", "our-name.txt", cwd=path)
            run(False, "commit", "-m", "our rename", cwd=path)

        refuse(("topic",), rename_conflict)
        checks.append("complex rename conflicts refuse before candidate objects, index, files or merge metadata are published")

        def binary_conflict(path):
            run(False, "switch", "topic", cwd=path)
            (path / "stable.bin").write_bytes(b"\0their binary fixture\xff")
            run(False, "add", ".", cwd=path)
            run(False, "commit", "-m", "their binary", cwd=path)
            run(False, "switch", "main", cwd=path)
            (path / "stable.bin").write_bytes(b"\0our binary fixture\xfe")
            run(False, "add", ".", cwd=path)
            run(False, "commit", "-m", "our binary", cwd=path)

        expected, actual = pair(binary_conflict)
        for client, path in ((False, expected), (True, actual)):
            run(client, "merge", "topic", cwd=path, success=False, code=1)
        assert (actual / "stable.bin").read_bytes() == (expected / "stable.bin").read_bytes()
        for command in (("ls-files", "--stage"), ("status", "--porcelain"), ("rev-parse", "AUTO_MERGE")):
            assert run(True, *command, cwd=actual) == run(False, *command, cwd=expected), command
        run(False, "merge", "--abort", cwd=expected)
        run(True, "merge", "--abort", cwd=actual)
        verify(expected, actual)
        checks.append("binary conflicts retain ours and all three index stages, including unchanged materialized paths during abort")

        _, invalid_commit = pair()
        run(False, "config", "commit.gpgSign", "invalid-fixture", cwd=invalid_commit)
        (invalid_commit / "our.txt").write_bytes(b"staged fixture change\n")
        run(False, "add", ".", cwd=invalid_commit)
        before = snapshot(invalid_commit)
        run(True, "commit", "-m", "must refuse invalid signing configuration", cwd=invalid_commit, success=False)
        assert snapshot(invalid_commit) == before
        checks.append("text conflict markers, three-stage index and standard merge state match Git; native add and merge --continue publish the same resolved commit and clean metadata")
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "object_format": args.object_format,
                          "checks": checks, "runtime_git_on_path": False, "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
