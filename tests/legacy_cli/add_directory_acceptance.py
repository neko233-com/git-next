"""Native staging must resolve file/directory boundaries into the same valid index as Git."""
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
    checks, failures, skipped, intentional_refusals = [], [], [], []
    with tempfile.TemporaryDirectory(prefix="git-next-add-directory-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / "config"), SSH_ROOT_PATH=str(home / ".ssh"),
                   GIT_AUTHOR_NAME="Fixture", GIT_COMMITTER_NAME="Fixture",
                   GIT_AUTHOR_EMAIL="fixture@example.invalid", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def run(native, *command, cwd=root, required=True, input_bytes=None):
            variables = dict(env)
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            process = subprocess.run([str(binary) if native else reference, *map(str, command)], cwd=cwd,
                                     env=variables, capture_output=True, timeout=30, input=input_bytes)
            if required:
                assert process.returncode == 0, (command, process.returncode, process.stderr)
            return process

        def worktree(path):
            files = {}
            for file in path.rglob("*"):
                if ".git" in file.relative_to(path).parts:
                    continue
                if file.is_symlink():
                    files[file.relative_to(path).as_posix()] = ("symlink", os.readlink(file))
                elif file.is_file():
                    files[file.relative_to(path).as_posix()] = ("file", file.read_bytes())
            return files

        for object_format in ("sha1", "sha256"):
            source = root / (object_format + "-source")
            run(False, "init", "-b", "main", "--object-format=" + object_format, source)
            (source / "file").write_bytes(b"old tracked file\n")
            (source / "file-keep.txt").write_bytes(b"unrelated sibling\n")
            (source / "file0.txt").write_bytes(b"original neighbor\n")
            (source / "folder").mkdir()
            (source / "folder/old.txt").write_bytes(b"old tracked child\n")
            (source / ".gitattributes").write_bytes(b"* text=auto eol=lf\n")
            run(False, "add", ".", cwd=source)
            run(False, "commit", "-m", "fixture: staging boundaries", cwd=source)
            outside = root / (object_format + "-outside")
            outside.mkdir()
            marker = b"outside fixture bytes must never be staged\n"
            (outside / "old.txt").write_bytes(marker)
            outside_id = run(False, "hash-object", "--stdin", cwd=source, input_bytes=marker).stdout.strip().decode()
            cases = (("all", ("-A",), "both"), ("root", (".",), "both"), ("parents", ("file", "folder"), "both"),
                     ("child-only", ("file/new.txt",), "both"), ("renormalize-tracked", ("--renormalize", "."), "both"),
                     ("renormalize-parent-file", ("--renormalize", "."), "parent-file"),
                     ("renormalize-file-dir", ("--renormalize", "."), "file-dir"),
                     ("renormalize-missing-parent", ("--renormalize", "."), "missing-parent"),
                     ("renormalize-missing-file", ("--renormalize", "."), "missing-file"),
                     ("renormalize-unmerged", ("--renormalize", "."), "unmerged"),
                     ("renormalize-unmerged-missing", ("--renormalize", "."), "unmerged-missing"),
                     ("renormalize-intent-to-add", ("--renormalize", "."), "intent"),
                     ("symlink-parent-all", ("-A",), "symlink"), ("symlink-parent-literal", ("folder",), "symlink"),
                     ("renormalize-symlink-parent", ("--renormalize", "."), "symlink"))
            for label, options, mutation in cases:
                targets = []
                supported = True
                for name in ("native", "reference"):
                    target = root / (object_format + "-" + label + "-" + name)
                    run(False, "clone", "--no-hardlinks", source, target)
                    run(False, "config", "core.autocrlf", "input", cwd=target)
                    (target / "file0.txt").write_bytes(b"unstaged neighbor edit\n")
                    if mutation in ("unmerged", "unmerged-missing"):
                        entries = b""
                        for stage, path in enumerate(("file", "file-keep.txt", "file0.txt"), 1):
                            oid = run(False, "rev-parse", "HEAD:" + path, cwd=target).stdout.strip()
                            entries += b"100644 " + oid + b" " + str(stage).encode() + b"\tconflict.txt\n"
                        run(False, "update-index", "--index-info", cwd=target, input_bytes=entries)
                        if mutation == "unmerged":
                            (target / "conflict.txt").write_bytes(b"<<<<<<< one\nconflict\n=======\nother\n>>>>>>> two\n")
                    if mutation == "intent":
                        (target / "intent.txt").write_bytes(b"intent data\r\n")
                        run(False, "add", "-N", "intent.txt", cwd=target)
                    if mutation == "missing-file":
                        (target / "file").unlink()
                    if mutation in ("both", "file-dir"):
                        (target / "file").unlink()
                        (target / "file").mkdir()
                        (target / "file/new.txt").write_bytes(b"new child\r\n")
                    if mutation in ("both", "parent-file", "missing-parent", "symlink"):
                        (target / "folder/old.txt").unlink()
                        (target / "folder").rmdir()
                        if mutation == "symlink":
                            try:
                                os.symlink(outside, target / "folder", target_is_directory=True)
                            except OSError:
                                supported = False
                                skipped.append(object_format + ": " + label + " (symlink privilege unavailable)")
                                break
                            run(False, "config", "core.symlinks", "true", cwd=target)
                        elif mutation != "missing-parent":
                            (target / "folder").write_bytes(b"new replacement file\r\n")
                    targets.append(target)
                if not supported:
                    continue
                native, expected = targets
                before = worktree(native)
                head = (native / ".git/HEAD").read_bytes()
                before_index = (native / ".git/index").read_bytes()
                before_git = {path.relative_to(native / ".git").as_posix(): path.read_bytes() for path in (native / ".git").rglob("*") if path.is_file()}
                expected_outcome = run(False, "add", *options, cwd=expected, required=False)
                outcome = run(True, "add", *options, cwd=native, required=False)
                parent_link_renormalize = mutation == "symlink" and options[0] == "--renormalize"
                if outcome.returncode and (expected_outcome.returncode or parent_link_renormalize):
                    assert (native / ".git/index").read_bytes() == before_index
                    assert {path.relative_to(native / ".git").as_posix(): path.read_bytes() for path in (native / ".git").rglob("*") if path.is_file()} == before_git
                    assert worktree(native) == before and (native / ".git/HEAD").read_bytes() == head
                    assert not (native / ".git/index.lock").exists()
                    assert run(False, "cat-file", "-e", outside_id, cwd=native, required=False).returncode != 0
                    if parent_link_renormalize and expected_outcome.returncode == 0:
                        intentional_refusals.append(object_format + ": parent-link renormalization refuses before repository writes; not reference behavior")
                    checks.append(object_format + ": " + label + " refused with index/HEAD/worktree unchanged")
                    continue
                actual_stage = run(False, "ls-files", "--stage", "-z", cwd=native).stdout
                expected_stage = run(False, "ls-files", "--stage", "-z", cwd=expected).stdout
                assert worktree(native) == before and (native / ".git/HEAD").read_bytes() == head
                assert not (native / ".git/index.lock").exists()
                if outcome.returncode or expected_outcome.returncode or actual_stage != expected_stage:
                    failure = {"object_format": object_format, "case": label, "returncode": outcome.returncode,
                               "reference_returncode": expected_outcome.returncode, "reference_error": expected_outcome.stderr.decode(errors="backslashreplace"),
                               "error": outcome.stderr.decode(errors="backslashreplace"),
                               "actual_stage": actual_stage.decode(errors="backslashreplace"),
                               "expected_stage": expected_stage.decode(errors="backslashreplace")}
                    if not args.diagnose:
                        raise AssertionError(failure)
                    failures.append(failure)
                    continue
                if not mutation.startswith("unmerged"):
                    assert run(False, "write-tree", cwd=native).stdout == run(False, "write-tree", cwd=expected).stdout
                assert run(True, "status", "--porcelain", cwd=native).stdout == run(False, "status", "--porcelain", cwd=expected).stdout
                run(False, "fsck", "--full", cwd=native)
                assert run(False, "cat-file", "-e", outside_id, cwd=native, required=False).returncode != 0
                assert (outside / "old.txt").read_bytes() == marker
                checks.append(object_format + ": " + label)
            malformed = root / (object_format + "-malformed")
            run(False, "clone", "--no-hardlinks", source, malformed)
            blob = bytes.fromhex(run(False, "hash-object", "-w", "--stdin", cwd=malformed, input_bytes=b"conflicting child\n").stdout.strip().decode())
            import struct
            data = (malformed / ".git/index").read_bytes()
            assert data[:8] == b"DIRC\x00\x00\x00\x02"
            count = int.from_bytes(data[8:12], "big")
            position, records = 12, []
            for _ in range(count):
                start = position
                position += 42 + len(blob)
                end = data.index(0, position)
                name = data[position:end]
                position = start + ((end + 1 - start + 7) & ~7)
                records.append((name, data[start:position]))
            name = b"file/new.txt"
            record = struct.pack(">10I", 0, 0, 0, 0, 0, 0, 0o100644, 0, 0, 0) + blob + struct.pack(">H", len(name)) + name + b"\0"
            record += b"\0" * (-len(record) % 8)
            records.append((name, record))
            encoded = b"DIRC" + struct.pack(">II", 2, len(records)) + b"".join(value for _, value in sorted(records))
            (malformed / ".git/index").write_bytes(encoded + hashlib.new(object_format, encoded).digest())
            before = {path.relative_to(malformed / ".git").as_posix(): path.read_bytes() for path in (malformed / ".git").rglob("*") if path.is_file()}
            outcome = run(True, "commit", "-m", "fixture: reject malformed index", cwd=malformed, required=False)
            after = {path.relative_to(malformed / ".git").as_posix(): path.read_bytes() for path in (malformed / ".git").rglob("*") if path.is_file()}
            if outcome.returncode == 0 or before != after:
                failure = {"object_format": object_format, "case": "malformed-prefix-commit", "returncode": outcome.returncode, "repository_unchanged": before == after}
                if not args.diagnose:
                    raise AssertionError(failure)
                failures.append(failure)
            else:
                checks.append(object_format + ": malformed prefix commit refused without repository writes")
    print(json.dumps({"status": "fail" if failures else "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "checks": checks,
                      "failures": failures, "skipped": skipped, "intentional_refusals": intentional_refusals,
                      "scope": "ordinary staging parity; parent-link renormalization is an explicit no-write refusal; full Git parity remains false",
                      "runtime_git_ssh_on_path": False}, indent=2))
    if failures:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
