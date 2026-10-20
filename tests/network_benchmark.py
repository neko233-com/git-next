"""Paired loopback HTTPS clone/fetch/pull latency with isolated profiles and independently checked data."""
import argparse
import base64
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import random
import secrets
import shutil
import statistics
import subprocess
import sys
import tempfile
import time

from push_acceptance import HttpFixture
from process_measurement import measured_run


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def snapshot(root, worktree=False):
    return {path.relative_to(root).as_posix(): digest(path) for path in root.rglob("*")
            if path.is_file() and (not worktree or ".git" not in path.relative_to(root).parts)}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--reference", type=Path)
    parser.add_argument("--source-checkpoint", type=Path)
    parser.add_argument("--runs", type=int, default=40)
    parser.add_argument("--files", type=int, default=1000)
    parser.add_argument("--timeout", type=int, default=90)
    parser.add_argument("--validation-only", action="store_true", help="conformance smoke only; do not publish latency statistics")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--native-distribution", action="store_true")
    args = parser.parse_args()
    assert args.runs >= (1 if args.validation_only else 10) and args.files >= 10 and args.timeout > 0
    binary = args.binary.resolve()
    reference = str(args.reference.resolve()) if args.reference else shutil.which("git")
    assert reference
    binary_hash = digest(binary)
    source_root = Path(__file__).resolve().parents[1]
    source = hashlib.sha256()
    for path in sorted([source_root / "Cargo.toml", source_root / "Cargo.lock", source_root / "compatibility.json",
                        source_root / "docs/COMPATIBILITY.md", *source_root.joinpath("src").rglob("*.rs"),
                        *source_root.joinpath("vendor").rglob("*.rs"), *source_root.joinpath("vendor").rglob("Cargo.toml")]):
        source.update(path.relative_to(source_root).as_posix().encode() + b"\0" + path.read_bytes())
    source_hash = source.hexdigest()
    if args.source_checkpoint:
        checkpoint = json.loads(args.source_checkpoint.read_text(encoding="utf-8-sig"))
        assert checkpoint["binary_sha256"] == binary_hash
        source_hash = checkpoint["source_tree_sha256"]
    results = {}
    with tempfile.TemporaryDirectory(prefix="git-next-network-benchmark-") as temporary:
        root = Path(temporary)
        home, empty, source_repo, remote, seed = (root / name for name in ("home", "empty", "source", "repository.git", "seed"))
        home.mkdir()
        empty.mkdir()
        environment = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        environment.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                           GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / "config"), GIT_TERMINAL_PROMPT="0",
                           GIT_AUTHOR_NAME="Benchmark", GIT_COMMITTER_NAME="Benchmark",
                           GIT_AUTHOR_EMAIL="benchmark@example.invalid", GIT_COMMITTER_EMAIL="benchmark@example.invalid")
        clients = {"git_next": str(binary), "git": reference}

        last_peak = None
        def run(executable, command, cwd=root, data=None):
            nonlocal last_peak
            variables = dict(environment)
            if executable == str(binary):
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty), GIT_SSH=str(empty / "missing-ssh"),
                                 GIT_SSH_COMMAND=str(empty / "missing-ssh"))
                if args.native_distribution:
                    for name in ('GIT_EXEC_PATH','GIT_SSH','GIT_SSH_COMMAND'): variables.pop(name,None)
            elif os.name == "nt" and not args.native_distribution:
                # The temporary CA has no revocation service; OpenSSL still verifies its chain/hostname.
                variables.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="http.sslBackend", GIT_CONFIG_VALUE_0="openssl")
            process, elapsed, last_peak = measured_run([executable, *map(str, command)], cwd=cwd, data=data,
                                                       timeout=args.timeout, env=variables)
            assert process.returncode == 0, (command[0], process.returncode, process.stderr.decode(errors="replace"))
            return elapsed, process.stdout

        def git(*command, cwd=root, data=None):
            return run(reference, command, cwd, data)[1]

        print(f"Preparing {args.files}-file loopback TLS fixture", file=sys.stderr, flush=True)
        git("init", "-b", "main", source_repo)
        for number in range(args.files):
            path = source_repo / f"dir-{number % 16}" / f"file-{number:07d}.txt"
            path.parent.mkdir(exist_ok=True)
            path.write_bytes((f"fixture file {number}\n" * 16).encode())
        (source_repo / ".gitattributes").write_bytes(b"* text=auto eol=lf\n")
        git("-C", source_repo, "add", ".")
        git("-C", source_repo, "commit", "-m", "fixture: network baseline")
        old_head = git("-C", source_repo, "rev-parse", "HEAD").strip()
        old_tree = snapshot(source_repo, worktree=True)
        old_bytes = sum(path.stat().st_size for path in source_repo.rglob("*") if path.is_file() and ".git" not in path.relative_to(source_repo).parts)
        git("clone", "--bare", "--no-hardlinks", source_repo, remote)
        git("-C", remote, "gc", "--prune=now")
        core = git("--exec-path").decode().strip()
        backend = Path(core) / ("git-http-backend.exe" if os.name == "nt" else "git-http-backend")
        assert backend.is_file()
        token = secrets.token_hex(24)
        server = HttpFixture(root, str(backend), environment, token)
        try:
            authorization = base64.b64encode(("fixture:" + token).encode()).decode()
            configuration = ('[core]\nautocrlf = input\neol = lf\n[credential]\nhelper =\n'
                             '[http]\nsslVerify = true\nsslCAInfo = "' + server.ca_file.as_posix() +
                             '"\nextraHeader = Authorization: Basic ' + authorization + '\n')
            (home / "config").write_text(configuration, encoding="utf-8", newline="\n")
            if args.native_distribution and os.name=='nt':
                with (home/'config').open('a') as stream: stream.write('\n[http]\n sslBackend = schannel\n schannelUseSSLCAInfo = true\n schannelCheckRevoke = false\n')
            (home / "config").chmod(0o600)
            versions = {label: run(client, ["--version"])[1].decode().strip() for label, client in clients.items()}
            git("clone", "--no-hardlinks", remote, seed)
            git("-C", seed, "remote", "set-url", "origin", server.url)
            old_stage = git("-C", seed, "ls-files", "--stage", "-z")
            old_objects = {line.split(b" ", 1)[0] for line in git("-C", seed, "rev-list", "--objects", "--all").splitlines()}
            rng = random.Random(233)
            requests = {}

            def remove(target):
                assert target.resolve().is_relative_to(root.resolve()) and target != root
                def writable(function, path, _):
                    os.chmod(path, 0o700)
                    function(path)
                shutil.rmtree(target, onexc=writable)

            for operation in ("clone", "fetch", "pull_ff_only"):
                if operation == "fetch":
                    for number in range(10):
                        (source_repo / f"dir-{number % 16}" / f"file-{number:07d}.txt").write_bytes(f"incremental edit {number}\n".encode())
                    git("-C", source_repo, "add", ".")
                    git("-C", source_repo, "commit", "-m", "fixture: incremental network update")
                    git("-C", source_repo, "push", remote, "main")
                expected_head = old_head if operation == "clone" else git("-C", source_repo, "rev-parse", "HEAD").strip()
                expected_tree = old_tree if operation == "clone" else snapshot(source_repo, worktree=True)
                expected_stage = git("-C", source_repo, "ls-files", "--stage", "-z")
                remote_objects = snapshot(remote / "objects")
                samples = {label: [] for label in clients}
                memory = {label: [] for label in clients}
                requests[operation] = {label: [] for label in clients}
                print("Measuring " + operation, file=sys.stderr, flush=True)
                for iteration in range(args.runs + 3):
                    order = list(clients)
                    rng.shuffle(order)
                    for label in order:
                        client = clients[label]
                        target = root / "trial"
                        if operation != "clone":
                            shutil.copytree(seed, target)
                            assert run(client, ["status", "--porcelain"], target)[1] == b""
                            saved_index = (target / ".git/index").read_bytes()
                        before_requests = server.authenticated_requests
                        command = ["clone", server.url, target] if operation == "clone" else (["fetch"] if operation == "fetch" else ["pull", "--ff-only"])
                        elapsed, _ = run(client, command, target if operation != "clone" else root)
                        operation_peak = last_peak
                        request_count = server.authenticated_requests - before_requests
                        assert request_count > 0
                        target_head = git("-C", target, "rev-parse", "HEAD").strip()
                        assert target_head == (old_head if operation == "fetch" else expected_head)
                        assert git("-C", target, "rev-parse", "refs/remotes/origin/main").strip() == expected_head
                        assert snapshot(target, worktree=True) == (old_tree if operation == "fetch" else expected_tree)
                        assert git("-C", target, "ls-files", "--stage", "-z") == (old_stage if operation == "fetch" else expected_stage)
                        if operation == "fetch":
                            assert (target / ".git/index").read_bytes() == saved_index
                            objects = {line.split(b" ", 1)[0] for line in git("-C", target, "rev-list", "--objects", "--all").splitlines()}
                            assert old_objects.issubset(objects) and len(objects) > len(old_objects)
                        git("-C", target, "fsck", "--full")
                        assert snapshot(remote / "objects") == remote_objects
                        if iteration >= 3:
                            samples[label].append(elapsed)
                            if operation_peak is not None: memory[label].append(operation_peak)
                            requests[operation][label].append(request_count)
                        remove(target)
                results[operation] = {label: {"median_ms": round(statistics.median(values), 3),
                                             "p95_ms": round(sorted(values)[math.ceil(len(values) * 0.95) - 1], 3),
                                             "min_ms": round(min(values), 3)} for label, values in samples.items()}
                for label, values in memory.items():
                    results[operation][label]['peak_working_set_samples'] = len(values)
                    if values:
                        results[operation][label].update(peak_working_set_median_bytes=int(statistics.median(values)),
                                                        peak_working_set_p95_bytes=sorted(values)[math.ceil(len(values)*.95)-1])
        finally:
            server.close()
    assert digest(binary) == binary_hash
    report = {"schema_version": 1, "status": "pass", "binary_sha256": binary_hash, "source_tree_sha256": source_hash,
              "harness_sha256": digest(Path(__file__)), "http_fixture_sha256": digest(Path(__file__).with_name("push_acceptance.py")),
              "versions": versions, "os": platform.platform(), "arch": platform.machine(), "cpu_count": os.cpu_count(),
              "files": args.files, "initial_worktree_bytes": old_bytes, "incremental_file_edits": 10,
              "fixture_contents": "16 repeated LF text lines per data file; one attribute policy; next commit edits 10 files",
              "measured_at_utc": datetime.datetime.now(datetime.UTC).isoformat(),
              "runs_per_client_per_command": args.runs, "randomized_order_seed": 233,
              "cache": "warm after 3 unmeasured runs; OS cache not flushed", "results": {} if args.validation_only else results,
              "performance_accepted": not args.validation_only,
              "authenticated_request_counts": requests, "runtime_git_ssh_on_path": False,
              "scope": "loopback smart HTTPS, temporary private CA and static fixture authorization; clone/fetch/ff-only pull",
              "system_configuration": "disabled with isolated profiles and explicit input/LF fixture policy",
              "tls_backends": ({"git_next":"Schannel","git":"Schannel"} if args.native_distribution and os.name=='nt' else {"git_next": "rustls", "git": "OpenSSL explicitly selected in the Windows reference process" if os.name == "nt" else "reference default"}),
              "memory_scope": "Windows primary process peak working set; queried after latency interval; transport/child processes excluded",
              "certificate_verification": "enabled; private fixture CA and loopback IP SAN; no user trust store changes",
              "http_response_protocol": server.server.RequestHandlerClass.protocol_version,
              "protocol_scope": "fixture default smart HTTP; no v2 packet or object-reuse acceptance",
              "verification": "each trial verifies HEAD/tracking refs, all worktree bytes, staged IDs, fsck and unchanged source object bytes; fetch preserves HEAD/index/worktree",
              "packet_bytes_or_object_reuse_proven": False, "real_provider_login_verified": False, "full_git_parity": False}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
