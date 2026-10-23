"""Compare porcelain-v1 bytes, quoting, paths and untracked modes without runtime Git fallback."""
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
    parser.add_argument("--include-long", action="store_true")
    args = parser.parse_args()
    binary = args.binary.resolve()
    reference = shutil.which("git")
    assert reference
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-status-format-") as temporary:
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

            def run(native, *arguments, cwd=None, data=None, code=0):
                variables = dict(env)
                if native:
                    variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
                process = subprocess.run([str(binary) if native else reference, *map(str, arguments)],
                                         cwd=cwd or (repo if repo.exists() else root), env=variables,
                                         input=data, capture_output=True, timeout=30)
                assert process.returncode == code, (arguments, process.returncode, process.stderr)
                return process.stdout

            def git(*arguments, **options):
                return run(False, *arguments, **options)

            git("init", "-b", "main", "--object-format=" + object_format, repo)
            for key, value in (("core.autocrlf", "input"), ("core.eol", "lf"), ("core.filemode", "false")):
                git("config", key, value)
            index = repo / ".git/index"

            def compare(label, *arguments, cwd=None):
                if args.include_long and '--long' not in arguments:
                    long_arguments = []
                    for argument in arguments:
                        if argument in ('-z', '-s', '--short') or argument.startswith('--porcelain'):
                            continue
                        long_arguments.append('-uno' if argument == '-suno' else argument)
                        if argument == 'status':
                            long_arguments.append('--long')
                    compare('long: ' + label, *long_arguments, cwd=cwd)
                expected = run(False, *arguments, cwd=cwd)
                before = index.read_bytes() if index.exists() else None
                head = (repo / ".git/HEAD").read_bytes()
                actual = run(True, *arguments, cwd=cwd)
                assert actual == expected, (object_format, label, arguments, actual, expected)
                assert (index.read_bytes() if index.exists() else None) == before
                assert (repo / ".git/HEAD").read_bytes() == head and not (repo / ".git/index.lock").exists()
                checks.append(object_format + ": " + label)

            compare("unborn clean", "status", "--porcelain=v1", "-z")
            names = ["root.txt", "rename old.txt", "deleted.txt", "space name.txt", "unicode-文.txt", "nested/edit.txt"]
            if os.name != "nt":
                names += ['quote"name', "line\nname", "tab\tname", "back\\slash", os.fsdecode(b"invalid-\xff.txt")]
            for name in names:
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(("base " + name + "\n").encode(errors="surrogateescape"))
            (repo / ".gitattributes").write_bytes(b"* text=auto eol=lf\n")
            (repo / ".gitignore").write_bytes(b"ignored/\n*.ignored\n")
            git("add", ".")
            git("commit", "-m", "fixture: status formats")
            for name in names:
                if name not in ("rename old.txt", "deleted.txt"):
                    (repo / name).write_bytes(b"real content edit\n")
            (repo / "deleted.txt").unlink()
            git("mv", "rename old.txt", "renamed new.txt")
            git("add", "root.txt")
            (repo / "root.txt").write_bytes(b"staged plus unstaged edit\n")
            (repo / "intent.txt").write_bytes(b"intent\n")
            git("add", "-N", "intent.txt")
            for name in ("a-untracked", "new dir/one.txt", "new dir/two.txt", "nested/untracked", "ignored/skip", "skip.ignored"):
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b"untracked\n")
            for label, arguments in (
                    ("default porcelain", ("--porcelain",)),
                    ("explicit v1", ("--porcelain=v1",)),
                    ("numeric v1", ("--porcelain=1",)),
                    ("implicit NUL", ("-z",)),
                    ("porcelain NUL rename destination/source", ("--porcelain=v1", "-z")),
                    ("short NUL", ("--short", "-z")),
                    ("untracked no", ("--porcelain", "-uno")),
                    ("untracked normal", ("--porcelain", "--untracked-files=normal")),
                    ("untracked all", ("--porcelain", "-uall")),
                    ("bare untracked option means all", ("--porcelain", "-u")),
                    ("directory pathspec", ("--porcelain", "--", "nested")),
                    ("untracked directory pathspec", ("--porcelain", "--", "new dir")),
                    ("untracked directory exclusion", ("--porcelain", "--", "new dir", ":(exclude)new dir/one.txt")),
                    ("wildcard with all untracked children", ("--porcelain", "--", "*.txt")),
                    ("combined short untracked mode", ("-suno",)),
                    ("pathspec excludes", ("--porcelain", "-z", "--", ":(glob)**/*.txt", ":(exclude)root.txt"))):
                compare(label, "status", *arguments)
            compare("nested porcelain stays root relative", "status", "--porcelain=v1", cwd=repo / "nested")
            compare("nested short relative paths", "status", "--short", cwd=repo / "nested")
            compare("nested short NUL relative paths", "status", "--short", "-z", cwd=repo / "nested")
            compare("nested pathspec", "status", "--porcelain", "--", "edit.txt", cwd=repo / "nested")
            for value in ("true", "false"):
                compare("quotePath " + value, "-c", "core.quotePath=" + value, "status", "--porcelain")
            compare("short root relative config", "-c", "status.relativePaths=false", "status", "-s", cwd=repo / "nested")
            compare("untracked config no", "-c", "status.showUntrackedFiles=no", "status", "--porcelain")
            compare("CLI untracked overrides config", "-c", "status.showUntrackedFiles=no", "status", "--porcelain", "-uall")
            compare("rename detection disabled", "-c", "status.renames=false", "status", "--porcelain", "-z")
            compare("status overrides disabled diff renames", "-c", "diff.renames=false", "-c", "status.renames=true", "status", "--porcelain")
            compare("diff rename fallback disabled", "-c", "diff.renames=false", "status", "--porcelain")
            compare("merge config does not disable status renames", "-c", "merge.renames=false", "status", "--porcelain")
            compare("copy detection setting", "-c", "status.renames=copies", "status", "--porcelain")
            git("update-index", "--chmod=+x", "space name.txt")
            compare("staged executable mode", "status", "--porcelain", "-z")
            git("config", "core.symlinks", "false")
            target = b"target-file\n"
            target_id = git("hash-object", "-w", "--stdin", data=target).strip().decode()
            git("update-index", "--add", "--cacheinfo", "120000," + target_id + ",unicode-文.txt")
            (repo / "unicode-文.txt").write_bytes(target)
            compare("staged type change with emulated symlink", "status", "--porcelain=v1", "-z")
            if os.name != "nt":
                compare("raw non-UTF8 pathspec", "status", "--porcelain", "-z", "--", os.fsdecode(b"invalid-\xff.txt"))
            blob = git("rev-parse", "HEAD:root.txt").strip()
            for mask in range(1, 8):
                git("update-index", "--force-remove", "conflict.txt")
                entries = b"".join(b"100644 " + blob + b" " + str(stage).encode() + b"\tconflict.txt\n"
                                   for stage in (1, 2, 3) if mask & (1 << (stage - 1)))
                git("update-index", "--index-info", data=entries)
                compare("unmerged stage mask " + str(mask), "status", "--porcelain=v1", "-z")
                compare("pathspec excludes conflict mask " + str(mask), "status", "--porcelain", "--", "root.txt")
            git("update-index", "--force-remove", "conflict.txt")
            saved = index.read_bytes()
            for key in ("status.relativePaths", "core.quotePath", "status.showUntrackedFiles"):
                run(True, "-c", key + "=not-valid", "status", "--porcelain", code=128)
                assert index.read_bytes() == saved and not (repo / ".git/index.lock").exists()
                checks.append(object_format + ": invalid " + key + " refuses without mutation")
            for setting in ("status.renameLimit=-1", "diff.renameLimit=2147483648"):
                run(True, "-c", setting, "status", "--porcelain", code=128)
                assert index.read_bytes() == saved and not (repo / ".git/index.lock").exists()
                checks.append(object_format + ": out-of-range rename limit refuses without mutation")
            process = subprocess.run([str(binary), "status", "--porcelain=v9"], cwd=repo,
                                     env={**env, "PATH": str(empty)}, capture_output=True, timeout=30)
            assert process.returncode != 0 and not process.stdout and index.read_bytes() == saved
            git("fsck", "--full")
    print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      "checks": checks, "runtime_git_ssh_on_path": False,
                      "includes_long_status": args.include_long,
                      "scope": "status filename and selection subset; full Git parity remains false"}, indent=2))


if __name__ == "__main__":
    main()
