"""Warm clean two-parent merge latency with exact reference-Git state verification."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import random
import shutil
import statistics
import stat
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--runs", type=int, default=40)
    parser.add_argument("--files", type=int, default=1000)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    assert args.runs >= 10 and args.files >= 3
    binary = args.binary.resolve()
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    reference = shutil.which("git")
    assert reference
    source = Path(__file__).resolve().parents[1]
    digest = hashlib.sha256()
    for path in sorted([source / "Cargo.toml", source / "Cargo.lock", source / "compatibility.json",
                        source / "docs/COMPATIBILITY.md", *source.joinpath("src").rglob("*.rs"),
                        *source.joinpath("vendor").rglob("*.rs"), *source.joinpath("vendor").rglob("Cargo.toml")]):
        digest.update(path.relative_to(source).as_posix().encode() + b"\0" + path.read_bytes())
    results = {"git_next": [], "git": []}
    order = random.Random(233)
    with tempfile.TemporaryDirectory(prefix="git-next-merge-benchmark-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="0",
                   GIT_CONFIG_GLOBAL=str(home / "config"), GIT_TERMINAL_PROMPT="0", GIT_MERGE_AUTOEDIT="no",
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2001-02-03T04:05:06+0000", GIT_COMMITTER_DATE="2001-02-03T04:05:06+0000")

        def run(client, *command, cwd=root):
            variables = dict(env)
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            process = subprocess.run([str(binary) if client else reference, *map(str, command)],
                                     cwd=cwd, env=variables, capture_output=True, timeout=60)
            assert process.returncode == 0, (command, process.returncode, process.stderr)
            return process.stdout

        def remove_readonly(function, path, error):
            resolved = Path(path).resolve()
            assert resolved.is_relative_to(root.resolve()) and resolved != root.resolve()
            if not isinstance(error, PermissionError):
                raise error
            os.chmod(path, stat.S_IREAD | stat.S_IWRITE)
            function(path)

        fixture = root / "fixture"
        run(False, "init", "-b", "main", fixture)
        run(False, "config", "core.autocrlf", "false", cwd=fixture)
        (fixture / ".gitattributes").write_bytes(b"* text=auto eol=lf\n")
        for number in range(args.files):
            (fixture / f"file-{number}.txt").write_bytes(b"first line\n" + b"stable fixture content\n" * 38 + b"last line\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "base", cwd=fixture)
        run(False, "switch", "-c", "topic", cwd=fixture)
        (fixture / "file-0.txt").write_bytes((fixture / "file-0.txt").read_bytes().replace(b"last line", b"their final line"))
        (fixture / "file-1.txt").write_bytes(b"their fixture edit\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "theirs", cwd=fixture)
        run(False, "switch", "main", cwd=fixture)
        (fixture / "file-0.txt").write_bytes((fixture / "file-0.txt").read_bytes().replace(b"first line", b"our first line"))
        (fixture / "file-2.txt").write_bytes(b"our fixture edit\n")
        run(False, "add", ".", cwd=fixture)
        run(False, "commit", "-m", "ours", cwd=fixture)
        versions = {"git_next": run(True, "--version").decode().strip(), "git": run(False, "--version").decode().strip()}
        for index in range(args.runs + 3):
            clients = [False, True]
            order.shuffle(clients)
            evidence = []
            for client in clients:
                destination = root / f"run-{index}-{client}"
                shutil.copytree(fixture, destination)
                assert run(False, "status", "--porcelain", cwd=destination) == b""
                start = time.perf_counter_ns()
                run(client, "merge", "--no-edit", "topic", cwd=destination)
                duration = (time.perf_counter_ns() - start) / 1_000_000
                if index >= 3:
                    results["git_next" if client else "git"].append(duration)
                worktree = {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                            for path in destination.iterdir() if path.is_file()}
                evidence.append((run(False, "cat-file", "commit", "HEAD", cwd=destination),
                                 run(False, "ls-files", "--stage", cwd=destination),
                                 run(False, "status", "--porcelain", cwd=destination),
                                 (destination / ".git/ORIG_HEAD").read_bytes(),
                                 (destination / ".git/logs/HEAD").read_bytes(), worktree))
                run(False, "fsck", "--full", cwd=destination)
                assert destination.resolve().parent == root.resolve() and destination.resolve() != root.resolve()
                shutil.rmtree(destination, onexc=remove_readonly)
            assert evidence[0] == evidence[1], "measured clients produced different repository state"
        assert hashlib.sha256(binary.read_bytes()).hexdigest() == binary_hash
        report = {"schema_version": 1, "product": "git-next", "binary_sha256": binary_hash,
                  "source_tree_sha256": digest.hexdigest(), "versions": versions,
                  "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  "os": platform.platform(), "arch": platform.machine(), "cpu_count": os.cpu_count(),
                  "runs": args.runs, "warmups": 3, "seed": 233, "files": args.files,
                  "merged_paths": 2, "fixture_commits": 3, "system_configuration_enabled": True,
                  "fixture_index_primed_before_measurement": True,
                  "client_git_on_path": False, "scope": "warm clean two-parent merge; process/captured-output included; no memory or cold-cache claim",
                  "verified": ["raw commit/parents/tree", "index stages", "worktree file hashes", "ORIG_HEAD", "HEAD reflog", "clean status", "independent fsck"],
                  "results": {name: {"median_ms": round(statistics.median(values), 3),
                                     "p95_ms": round(sorted(values)[math.ceil(len(values) * .95) - 1], 3),
                                     "samples_ms": [round(value, 3) for value in values]}
                              for name, values in results.items()}}
        args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")
        print(json.dumps({"status": "pass", "binary_sha256": binary_hash,
                          "results": {name: {key: value for key, value in values.items() if key != "samples_ms"}
                                      for name, values in report["results"].items()}}, indent=2))


if __name__ == "__main__":
    main()
