"""Clean merge --no-commit state, continuation and abort in both object formats."""
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
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    cases = 0
    with tempfile.TemporaryDirectory(prefix="git-next-pending-merge-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / "config"), GIT_TERMINAL_PROMPT="0", GIT_EDITOR="true",
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2001-02-03T04:05:06+0000", GIT_COMMITTER_DATE="2001-02-03T04:05:06+0000",
                   GIT_MERGE_AUTOEDIT="no")

        def run(client, *command, cwd=root):
            variables = dict(env)
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if client else reference, *map(str, command)],
                                    cwd=cwd, env=variables, capture_output=True, timeout=30)
            assert result.returncode == 0, (command, result.returncode, result.stderr)
            return result.stdout

        def verify(expected, actual, pending):
            for command in (("cat-file", "commit", "HEAD"), ("rev-parse", "ORIG_HEAD"),
                            ("status", "--porcelain"), ("ls-files", "--stage")):
                left, right = run(False, *command, cwd=expected), run(False, *command, cwd=actual)
                assert right == left, (command, left, right)
            for name in ("HEAD", "logs/HEAD", "logs/refs/heads/main", "MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "AUTO_MERGE"):
                left, right = expected / ".git" / name, actual / ".git" / name
                assert right.is_file() == left.is_file(), name
                if left.is_file():
                    assert right.read_bytes() == left.read_bytes(), name
            left_files = {p.relative_to(expected): p.read_bytes() for p in expected.iterdir() if p.is_file()}
            right_files = {p.relative_to(actual): p.read_bytes() for p in actual.iterdir() if p.is_file()}
            assert right_files == left_files
            assert (actual / ".git/git-next-merge.json").exists() == pending
            assert not list((actual / ".git").glob("git-next-checkout-*"))
            run(False, "fsck", "--full", cwd=actual)

        for object_format in ("sha1", "sha256"):
            fixture = root / object_format
            run(False, "init", "-b", "main", "--object-format=" + object_format, fixture)
            (fixture / "shared.txt").write_bytes(b"first line\n" + b"stable fixture\n" * 20 + b"last line\n")
            run(False, "add", ".", cwd=fixture)
            run(False, "commit", "-m", "base", cwd=fixture)
            base = run(False, "rev-parse", "HEAD", cwd=fixture).strip().decode()
            run(False, "switch", "-c", "topic", cwd=fixture)
            (fixture / "shared.txt").write_bytes((fixture / "shared.txt").read_bytes().replace(b"last line", b"their final line"))
            run(False, "add", ".", cwd=fixture)
            run(False, "commit", "-m", "theirs", cwd=fixture)
            run(False, "switch", "main", cwd=fixture)
            (fixture / "shared.txt").write_bytes((fixture / "shared.txt").read_bytes().replace(b"first line", b"our first line"))
            run(False, "add", ".", cwd=fixture)
            run(False, "commit", "-m", "ours", cwd=fixture)
            run(False, "tag", "fixture-lightweight", "topic", cwd=fixture)
            for ff_case in ("diverged", "fast-forward", "forced-commit", "lightweight-tag", "custom-message"):
                for action in (("commit", "--no-edit"), ("merge", "--continue"), ("merge", "--abort")):
                    paths = [root / f"{object_format}-{ff_case}-{action[-1]}-{kind}" for kind in ("reference", "native")]
                    for path in paths:
                        shutil.copytree(fixture, path)
                        if ff_case in ("fast-forward", "forced-commit"):
                            run(False, "reset", "--hard", base, cwd=path)
                    options = ["merge", "--no-commit"]
                    if ff_case == "forced-commit":
                        options += ["--no-ff"]
                    if ff_case == "custom-message":
                        options += ["--cleanup=strip", "-m", "# raw fixture hint\n\nsubject with spaces  \n"]
                    options += ["fixture-lightweight" if ff_case == "lightweight-tag" else "topic"]
                    for client, path in ((False, paths[0]), (True, paths[1])):
                        run(client, *options, cwd=path)
                    pending = ff_case != "fast-forward"
                    verify(*paths, pending)
                    if pending:
                        for client, path in ((False, paths[0]), (True, paths[1])):
                            run(client, *action, cwd=path)
                        verify(*paths, False)
                    cases += 1
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "object_formats": ["sha1", "sha256"], "cases": cases,
                          "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
