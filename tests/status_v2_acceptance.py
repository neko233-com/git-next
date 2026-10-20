"""Compare porcelain-v2 records against Git while preserving every repository byte."""
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
    with tempfile.TemporaryDirectory(prefix="git-next-status-v2-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"), SSH_ROOT_PATH=str(home / ".ssh"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / "config"), GIT_OPTIONAL_LOCKS="0",
                   GIT_AUTHOR_NAME="Fixture", GIT_COMMITTER_NAME="Fixture",
                   GIT_AUTHOR_EMAIL="fixture@example.invalid", GIT_COMMITTER_EMAIL="fixture@example.invalid")
        for object_format in ("sha1", "sha256"):
            repo = root / object_format

            def run(native, *arguments, cwd=None, data=None):
                variables = dict(env)
                if native:
                    variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
                process = subprocess.run([str(binary) if native else reference, *map(str, arguments)],
                                         cwd=cwd or (repo if repo.exists() else root), env=variables,
                                         input=data, capture_output=True, timeout=30)
                assert process.returncode == 0, (arguments, process.returncode, process.stderr)
                return process.stdout

            def git(*arguments, **options):
                return run(False, *arguments, **options)

            def snapshot():
                return {path.relative_to(repo / ".git").as_posix(): path.read_bytes()
                        for path in (repo / ".git").rglob("*") if path.is_file()}

            def compare(label, *options, cwd=None):
                if args.include_long and '--long' not in options:
                    long_options = []
                    for option in options:
                        if option in ('-z', '-s', '--short') or option.startswith('--porcelain'):
                            continue
                        long_options.append('-b' if option in ('-bz', '-sb') else option)
                        if option == 'status':
                            long_options.append('--long')
                    compare('long: ' + label, *long_options, cwd=cwd)
                expected = run(False, *options, cwd=cwd)
                before = snapshot()
                actual = run(True, *options, cwd=cwd)
                assert actual == expected, (object_format, label, actual, expected)
                assert snapshot() == before
                checks.append(object_format + ": " + label)

            git("init", "-b", "main", "--object-format=" + object_format, repo)
            for key, value in (("core.autocrlf", "input"), ("core.eol", "lf"), ("core.filemode", "false"), ("core.symlinks", "false")):
                git("config", key, value)
            compare("unborn empty", "status", "--porcelain=v2", "-z")
            def branch_checks(label):
                for flags in (("--porcelain=v2", "--branch"), ("--porcelain=v2", "-bz"),
                              ("--porcelain", "-b"), ("-sb", "-z"), ("-sb",),
                              ("--porcelain=v2", "-b", "--no-ahead-behind"), ("-sb", "--no-ahead-behind")):
                    compare(label + " " + " ".join(flags), "status", *flags)
            branch_checks("unborn branch")
            names = ["root.txt", "deleted.txt", "space name.txt", "unicode-文.txt", "folder/child.txt", "exact old.txt", "edited old.txt"]
            if os.name != "nt":
                names += ["line\nname", 'quote"name', "tab\tname", os.fsdecode(b"raw-\xff")]
            for name in names:
                path = repo / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(("base " + name + "\n").encode(errors="surrogateescape"))
            (repo / "edited old.txt").write_bytes(b"".join(f"original line {n:03d}\n".encode() for n in range(100)))
            rename_contents = {
                "varying": b"".join((str(n) + "x" * (n % 60) + "\n").encode() for n in range(80)),
                "long": b"".join((str(n) + "x" * 200 + "\n").encode() for n in range(20)),
                "reorder": b"".join(f"ordered line {n:03d}\n".encode() for n in range(80)),
            }
            for name, content in rename_contents.items():
                (repo / (name + " old.txt")).write_bytes(content)
            (repo / ".gitattributes").write_bytes(b"* text=auto eol=lf\n")
            git("add", ".")
            git("commit", "-m", "fixture: v2 metadata")
            compare("clean", "status", "--porcelain=2")
            branch_checks("clean branch")
            base = git("rev-parse", "HEAD").decode().strip()
            git("remote", "add", "origin", "https://example.invalid/status-fixture")
            git("config", "branch.main.remote", "origin")
            git("config", "branch.main.merge", "refs/heads/main")
            branch_checks("gone upstream")
            git("update-ref", "refs/remotes/origin/main", base)
            branch_checks("equal upstream")
            tree = git("rev-parse", "HEAD^{tree}").decode().strip()
            local = git("commit-tree", tree, "-p", base, "-m", "local").decode().strip()
            remote = git("commit-tree", tree, "-p", base, "-m", "remote").decode().strip()
            git("update-ref", "refs/heads/main", local)
            branch_checks("ahead upstream")
            git("update-ref", "refs/remotes/origin/main", remote)
            branch_checks("diverged upstream")
            git("update-ref", "refs/heads/main", base)
            branch_checks("behind upstream")
            git("config", "branch.main.remote", ".")
            git("config", "branch.main.merge", "refs/heads/local-tracking")
            git("update-ref", "refs/heads/local-tracking", remote)
            branch_checks("local upstream")
            git("checkout", "--detach", base)
            branch_checks("detached HEAD")
            git("checkout", "main")
            for name in ("root.txt", "space name.txt", "unicode-文.txt", "folder/child.txt"):
                (repo / name).write_bytes(b"real edit\n")
            (repo / "deleted.txt").unlink()
            git("add", "root.txt")
            (repo / "root.txt").write_bytes(b"new worktree edit\n")
            git("mv", "exact old.txt", "exact new.txt")
            git("mv", "edited old.txt", "edited new.txt")
            content = (repo / "edited new.txt").read_bytes().replace(b"original line 050\n", b"edited line 050\n")
            (repo / "edited new.txt").write_bytes(content)
            git("add", "edited new.txt")
            for name, content in rename_contents.items():
                git("mv", name + " old.txt", name + " new.txt")
                lines = content.splitlines(keepends=True)
                if name == "reorder":
                    lines = lines[20:40] + lines[:20] + lines[40:]
                else:
                    lines[3] = b"replaced line with different length\n"
                (repo / (name + " new.txt")).write_bytes(b"".join(lines))
                git("add", name + " new.txt")
            (repo / "intent.txt").write_bytes(b"intent\n")
            git("add", "-N", "intent.txt")
            (repo / "new dir").mkdir()
            (repo / "new dir/untracked.txt").write_bytes(b"untracked\n")
            for label, options in (("mixed text records", ("--porcelain=v2",)), ("mixed NUL records", ("--porcelain=2", "-z")),
                                   ("untracked all", ("--porcelain=v2", "-uall")), ("untracked no", ("--porcelain=v2", "-uno")),
                                   ("selected paths", ("--porcelain=v2", "--", "folder", "space name.txt"))):
                compare(label, "status", *options)
            compare("status rename disabled", "-c", "status.renames=false", "status", "--porcelain=v2", "-z")
            compare("status copy setting", "-c", "status.renames=copies", "status", "--porcelain=v2", "-z")
            compare("nested cwd root paths", "status", "--porcelain=v2", "-z", cwd=repo / "folder")
            compare("unquoted Unicode", "-c", "core.quotePath=false", "status", "--porcelain=v2")
            git("update-index", "--chmod=+x", "space name.txt")
            compare("executable mode", "status", "--porcelain=v2", "-z")
            target = git("hash-object", "-w", "--stdin", data=b"target-file\n").decode().strip()
            git("update-index", "--cacheinfo", "120000," + target + ",unicode-文.txt")
            (repo / "unicode-文.txt").write_bytes(b"target-file\n")
            compare("emulated symlink type", "status", "--porcelain=v2", "-z")
            git("rm", "--cached", "folder/child.txt")
            compare("tracked deletion plus same-name untracked file", "status", "--porcelain=v2", "-z")
            blob = git("rev-parse", "HEAD:root.txt").strip()
            for mask in range(1, 8):
                git("update-index", "--force-remove", "conflict.txt")
                data = b"".join((b"100644 " + blob + b" " + str(stage).encode() + b"\tconflict.txt\n")
                                for stage in (1, 2, 3) if mask & (1 << (stage - 1)))
                git("update-index", "--index-info", data=data)
                (repo / "conflict.txt").write_bytes(b"conflict worktree\n")
                compare("conflict mask " + str(mask), "status", "--porcelain=v2", "-z")
                compare("conflict mask v1 " + str(mask), "status", "--porcelain=v1", "-z")
            git("update-index", "--force-remove", "conflict.txt")
            git("reset", "--hard", "HEAD")
            source = root / (object_format + "-submodule-source")
            git("init", "-b", "main", "--object-format=" + object_format, source)
            (source / "tracked.txt").write_bytes(b"submodule tracked\n")
            git("add", ".", cwd=source)
            git("commit", "-m", "submodule fixture", cwd=source)
            git("-c", "protocol.file.allow=always", "submodule", "add", str(source), "sub")
            git("commit", "-m", "superproject fixture")
            def submodule_checks(label):
                for flags in (("--porcelain=v2",), ("--porcelain=v2", "-z"), ("--porcelain",), ("-s",),
                              ("--porcelain=v2", "--ignore-submodules=untracked"),
                              ("--porcelain=v2", "--ignore-submodules=dirty"),
                              ("--porcelain=v2", "--ignore-submodules=all"),
                              ("--porcelain=v2", "--ignore-submodules"),
                              ("--porcelain=v2", "--ignore-submodules=none")):
                    compare(label + " " + " ".join(flags), "status", *flags)
            submodule_checks("clean submodule")
            (repo / "sub/untracked.txt").write_bytes(b"untracked\n")
            submodule_checks("untracked submodule")
            (repo / "sub/tracked.txt").write_bytes(b"modified\n")
            submodule_checks("mixed submodule")
            git("add", "tracked.txt", cwd=repo / "sub")
            submodule_checks("staged submodule content")
            git("commit", "-m", "submodule advance", cwd=repo / "sub")
            submodule_checks("advanced submodule HEAD")
            git("add", "sub")
            submodule_checks("staged submodule gitlink")
            for key in ("diff.ignoreSubmodules", "submodule.sub.ignore"):
                for mode in ("none", "untracked", "dirty", "all"):
                    git("config", key, mode)
                    compare("configured " + key + "=" + mode, "status", "--porcelain=v2", "-z")
                    compare("explicit override " + key + "=" + mode, "status", "--porcelain=v2", "--ignore-submodules=none", "-z")
                git("config", "--unset", key)
            for option in (("--porcelain=v3",), ("--porcelain=v2", "--ignore-submodules=unknown")):
                before = snapshot()
                process = subprocess.run([str(binary), "status", *option], cwd=repo,
                                         env={**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty), "GIT_OPTIONAL_LOCKS": "1"},
                                         capture_output=True, timeout=30)
                assert process.returncode != 0 and not process.stdout and before == snapshot()
                checks.append(object_format + ": invalid format/ignore option refuses without writes")
            git("fsck", "--full")
    print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "checks": checks,
                      "includes_long_status": args.include_long,
                      "runtime_git_ssh_on_path": False, "scope": "status records, branch headers and ordinary submodule-status subset; no full Git parity claim"}, indent=2))


if __name__ == "__main__":
    main()
