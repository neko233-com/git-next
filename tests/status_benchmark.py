"""Warm status comparison on native-created zero-stat indexes; isolated profiles only."""
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
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=40)
    parser.add_argument("--files", type=int, default=1000)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    assert args.runs >= 10 and args.files >= 1
    clients = {"git_next": str(args.binary.resolve()), "before_refresh": str(args.baseline.resolve()),
               "git": shutil.which("git")}
    hashes = {key: hashlib.sha256(Path(executable).read_bytes()).hexdigest() for key, executable in clients.items()}
    with tempfile.TemporaryDirectory(prefix="git-next-status-benchmark-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / ".gitconfig"), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                   GIT_AUTHOR_DATE="2026-10-19T00:00:00Z", GIT_COMMITTER_DATE="2026-10-19T00:00:00Z")

        def run(executable, *command, cwd):
            variables = {**env}
            if executable != clients["git"]:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            start = time.perf_counter_ns()
            result = subprocess.run([executable, *map(str, command)], cwd=cwd, env=variables,
                                    capture_output=True, timeout=30)
            elapsed = (time.perf_counter_ns() - start) / 1_000_000
            assert result.returncode == 0, (command, result.stderr)
            return elapsed, result.stdout

        repos = {}
        for key in clients:
            repo = root / key
            run(clients["git_next"], "init", repo, cwd=root)
            for number in range(args.files):
                path = repo / f"dir-{number % 16:02d}" / f"file-{number:06d}.txt"
                path.parent.mkdir(exist_ok=True)
                path.write_bytes(f"fixture {number}\n".encode() * 32)
            run(clients["git_next"], "add", "-A", cwd=repo)
            run(clients["git_next"], "commit", "-m", "fixture: native index", cwd=repo)
            repos[key] = repo
        versions = {key: run(executable, "--version", cwd=root)[1].decode().strip() for key, executable in clients.items()}
        first, samples = {}, {key: [] for key in clients}
        rng = random.Random(233)
        order = list(clients)
        rng.shuffle(order)
        for key in order:
            first[key], output = run(clients[key], "status", "--porcelain", cwd=repos[key])
            assert output == b""
            for _ in range(3):
                assert run(clients[key], "status", "--porcelain", cwd=repos[key])[1] == b""
        for _ in range(args.runs):
            order = list(clients)
            rng.shuffle(order)
            for key in order:
                elapsed, output = run(clients[key], "status", "--porcelain", cwd=repos[key])
                assert output == b""
                samples[key].append(elapsed)
        for key, repo in repos.items():
            run(clients["git"], "fsck", "--full", cwd=repo)
        assert len({run(clients["git"], "rev-parse", "HEAD", cwd=repo)[1] for repo in repos.values()}) == 1
    assert hashes == {key: hashlib.sha256(Path(executable).read_bytes()).hexdigest() for key, executable in clients.items()}
    report = {"schema_version": 1, "scope": "warm status on separate equivalent native-created zero-stat indexes; process creation and output included",
              "versions": versions, "binary_sha256": hashes, "os": platform.platform(), "cpu_count": os.cpu_count(),
              "files": args.files, "runs_per_client": args.runs, "randomized_order_seed": 233,
              "runtime_git_on_native_path": False, "first_status_ms": {key: round(value, 3) for key, value in first.items()},
              "results": {key: {"median_ms": round(statistics.median(values), 3),
                                "p95_ms": round(sorted(values)[math.ceil(len(values) * .95) - 1], 3)} for key, values in samples.items()},
              "full_git_parity": False}
    content = json.dumps(report, indent=2)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(content + "\n", encoding="utf-8")
    print(content)


if __name__ == "__main__":
    main()
