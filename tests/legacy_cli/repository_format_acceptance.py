"""Repository extension negotiation; reference Git only creates isolated fixtures."""
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


def snapshot(root):
    return {path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file()}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-format-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / "config"), GIT_TERMINAL_PROMPT="0",
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def run(client, *command, cwd=root, success=True):
            variables = dict(env)
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if client else reference, *map(str, command)],
                                    cwd=cwd, env=variables, capture_output=True, timeout=30)
            assert (result.returncode == 0) == success, (command, result.returncode, result.stderr)
            return result.stdout

        fixture = root / "fixture"
        run(False, "init", "-b", "main", fixture)
        (fixture / "file.txt").write_bytes(b"base fixture\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "base", cwd=fixture)

        def refuses(path):
            before = snapshot(path)
            for command in (("status", "--porcelain"), ("add", "file.txt"),
                            ("commit", "-m", "must not publish"), ("reset", "--hard", "HEAD")):
                run(True, *command, cwd=path, success=False)
                assert snapshot(path) == before, command

        reftable = root / "reftable"
        run(False, "init", "-b", "main", "--ref-format=reftable", reftable)
        (reftable / "file.txt").write_bytes(b"reftable fixture\n")
        run(False, "add", ".", cwd=reftable)
        run(False, "commit", "-m", "base", cwd=reftable)
        refuses(reftable)
        run(False, "fsck", "--full", cwd=reftable)
        checks.append("existing reftable repositories refuse before status/index/object/ref writes")

        for sequence, (version, key, value) in enumerate((
                (1, "extensions.fixtureUnknown", "true"),
                (1, "extensions.fixture.subsection", "true"),
                (1, "extensions.refStorage", "invalid-fixture"),
                (0, "extensions.refStorage", "files"),
                (1, "extensions.partialClone", "fixture-promisor"),
                (1, "extensions.compatObjectFormat", "sha256"),
                (1, "extensions.preciousObjects", "invalid-fixture"),
                (2, "extensions.noop", "true"))):
            path = root / f"unsupported-{sequence}"
            shutil.copytree(fixture, path)
            run(False, "config", "core.repositoryFormatVersion", str(version), cwd=path)
            # config --file also handles extension names which Git cannot otherwise open.
            run(False, "config", "--file", str(path / ".git" / "config"), key, value)
            (path / "file.txt").write_bytes(b"must remain unstaged\n")
            refuses(path)
        checks.append("unknown required extensions, incompatible hashes, promisor layout and invalid values fail without mutations")

        included = root / "included"
        shutil.copytree(fixture, included)
        run(False, "config", "core.repositoryFormatVersion", "1", cwd=included)
        (included / ".git" / "extension-policy").write_bytes(b"[extensions]\n fixtureUnknown = true\n")
        run(False, "config", "include.path", "extension-policy", cwd=included)
        refuses(included)
        checks.append("required extension negotiation includes effective included configuration")

        for version in (0, 1):
            path = root / f"supported-{version}"
            shutil.copytree(fixture, path)
            run(False, "config", "core.repositoryFormatVersion", str(version), cwd=path)
            run(False, "config", "extensions.noop", "true", cwd=path)
            run(False, "config", "extensions.preciousObjects", "true", cwd=path)
            if version == 1:
                run(False, "config", "extensions.refStorage", "files", cwd=path)
            else:
                run(False, "config", "extensions.fixtureUnknown", "ignored-v0", cwd=path)
            assert run(True, "status", "--porcelain", cwd=path) == b""
            (path / "file.txt").write_bytes(b"supported layout change\n")
            run(True, "add", "file.txt", cwd=path)
            run(True, "commit", "-m", "supported layout", cwd=path)
            run(False, "fsck", "--full", cwd=path)
        checks.append("ordinary files references, precious objects and v0 optional keys retain native read/write support")
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "checks": checks, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
