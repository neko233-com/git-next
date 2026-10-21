"""Reproducible warm process+command latency; no credentials or existing repositories."""
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
import sys
import tempfile
import time
from process_measurement import measured_run


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--reference", type=Path)
    parser.add_argument("--runs", type=int, default=40)
    parser.add_argument("--files", type=int, default=1000)
    parser.add_argument("--directories", type=int, default=16, help="data directory count; use a wide layout to measure many small directories")
    parser.add_argument("--timeout", type=int, default=30, help="per-command seconds, including large fixture setup")
    parser.add_argument("--setup-timeout", type=int, default=180, help="fixture-only command timeout; measured commands retain --timeout")
    parser.add_argument("--fixture-layout", choices=("loose", "packed"), default="loose",
                        help="packed uses fixture-only fast-import and LF checkout instead of add")
    parser.add_argument("--client-fscache", choices=["default", "on", "off"], default="default")
    parser.add_argument("--reference-read-safety", action="store_true",
                        help="apply the shipped Windows read-safety defaults to the reference too")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--read-commands", action="store_true", help="also measure clean diff, show and log")
    parser.add_argument("--system-config-enabled", action="store_true", help="read conventional system configuration with an isolated user profile")
    parser.add_argument("--source-checkpoint", type=Path, help="pin a previous binary to its already recorded source-tree hash")
    parser.add_argument("--baseline-binary", type=Path, help="also measure a retained native client in the same randomized fixture")
    parser.add_argument("--baseline-checkpoint", type=Path, help="required binary/source identity for the retained native client")
    parser.add_argument("--native-distribution", action="store_true")
    args = parser.parse_args()
    reference_overrides = (["core.fscache=false", "core.fsmonitor=false", "core.preloadIndex=false",
                            "index.threads=1", "grep.threads=1"] if args.reference_read_safety else [])
    assert not args.reference_read_safety or os.name == "nt"
    assert args.runs >= 10 and args.files >= 1 and args.timeout > 0 and args.directories > 0
    assert bool(args.baseline_binary) == bool(args.baseline_checkpoint)
    binary = str(args.binary.resolve())
    binary_hash = hashlib.sha256(Path(binary).read_bytes()).hexdigest()
    reference = str(args.reference.resolve()) if args.reference else shutil.which("git")
    assert reference
    results = {}
    source_root = Path(__file__).resolve().parents[1]
    source_digest = hashlib.sha256()
    for path in sorted([source_root / "Cargo.toml", source_root / "Cargo.lock", source_root / "compatibility.json",
                        source_root / "docs/COMPATIBILITY.md",
                        *source_root.joinpath("src").rglob("*.rs"),
                        *source_root.joinpath("vendor").rglob("*.rs"),
                        *source_root.joinpath("vendor").rglob("Cargo.toml")]):
        source_digest.update(path.relative_to(source_root).as_posix().encode() + b"\0" + path.read_bytes())
    source_hash = source_digest.hexdigest()
    if args.source_checkpoint:
        checkpoint = json.loads(args.source_checkpoint.read_text())
        assert checkpoint["binary_sha256"] == binary_hash, "checkpoint does not identify this binary"
        source_hash = checkpoint["source_tree_sha256"]
    baseline = None
    baseline_identity = None
    if args.baseline_binary:
        baseline = str(args.baseline_binary.resolve())
        baseline_identity = json.loads(args.baseline_checkpoint.read_text(encoding="utf-8-sig"))
        assert hashlib.sha256(Path(baseline).read_bytes()).hexdigest() == baseline_identity["binary_sha256"]
        baseline_identity = {key: baseline_identity[key] for key in ("binary_sha256", "source_tree_sha256")}
    with tempfile.TemporaryDirectory(prefix="git-next-benchmark-") as directory:
        root = Path(directory)
        home = root / "home"
        home.mkdir()
        empty = root / "empty-path"
        empty.mkdir()
        repo = root / "repo"
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="0" if args.system_config_enabled else "1",
                   GIT_CONFIG_GLOBAL=str(home / "config"),
                   GIT_AUTHOR_NAME="Benchmark", GIT_COMMITTER_NAME="Benchmark",
                   GIT_AUTHOR_EMAIL="benchmark@example.invalid", GIT_COMMITTER_EMAIL="benchmark@example.invalid")

        last_peak = None
        def run(executable, command, cwd=root, input_bytes=None, setup=False):
            nonlocal last_peak
            variables = {**env}
            if executable != reference:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty),
                                 GIT_SSH=str(empty / "missing-ssh"),
                                 GIT_SSH_COMMAND=str(empty / "missing-ssh"))
                if args.native_distribution:
                    for name in ('GIT_EXEC_PATH','GIT_SSH','GIT_SSH_COMMAND'): variables.pop(name,None)
                if args.client_fscache != "default":
                    variables.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.fscache",
                                     GIT_CONFIG_VALUE_0="true" if args.client_fscache == "on" else "false")
            options = [value for setting in reference_overrides for value in ("-c", setting)] if executable == reference else []
            result, elapsed, last_peak = measured_run([executable, *options, *command], cwd=cwd, env=variables,
                                                       timeout=args.setup_timeout if setup else args.timeout, data=input_bytes)
            assert result.returncode == 0, (command, result.stderr.decode(errors="replace"))
            return elapsed, result.stdout

        print(f"Preparing {args.files}-file {args.fixture_layout} fixture", file=sys.stderr, flush=True)
        run(reference, ["init", "-b", "main", "--object-format=sha1", str(repo)], setup=True)
        if args.fixture_layout == "packed":
            stream = bytearray()
            for number in range(args.files):
                data = f"benchmark fixture {number}\n".encode() * 16
                stream.extend(f"blob\nmark :{number + 1}\ndata {len(data)}\n".encode())
                stream.extend(data + b"\n")
            message = b"fixture: benchmark\n"
            stream.extend(b"commit refs/heads/main\ncommitter Benchmark <benchmark@example.invalid> 981173106 +0000\n")
            stream.extend(f"data {len(message)}\n".encode() + message + b"\n")
            for number in range(args.files):
                stream.extend(f"M 100644 :{number + 1} dir-{number % args.directories:02d}/file-{number:06d}.txt\n".encode())
            stream.extend(b"\ndone\n")
            run(reference, ["fast-import", "--quiet", "--done"], cwd=repo, input_bytes=bytes(stream), setup=True)
            del stream
            run(reference, ["config", "core.autocrlf", "input"], cwd=repo)
            run(reference, ["config", "core.eol", "lf"], cwd=repo)
            run(reference, ["reset", "--hard", "HEAD"], cwd=repo, setup=True)
        else:
            for number in range(args.files):
                path = repo / f"dir-{number % args.directories:02d}" / f"file-{number:06d}.txt"
                path.parent.mkdir(exist_ok=True)
                path.write_bytes(f"benchmark fixture {number}\n".encode() * 16)
            run(reference, ["add", "."], cwd=repo, setup=True)
            run(reference, ["commit", "-m", "fixture: benchmark"], cwd=repo, setup=True)
        clients = {"git_next": binary, "git": reference}
        if baseline:
            clients["git_next_baseline"] = baseline
        initial_stage_hash = hashlib.sha256(run(reference, ["ls-files", "--stage", "-z"], cwd=repo)[1]).hexdigest()

        def refuse_output(name, key, output, expected):
            report = {"status": "fail", "stage": "output validation", "operation": name, "client": key,
                      "binary_sha256": binary_hash, "source_tree_sha256": source_hash,
                      "files": args.files, "directories": args.directories,
                      "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      "actual_sample": output[:2048].decode(errors="replace"),
                      "expected_sample": expected[:2048].decode(errors="replace"),
                      "initial_stage_sha256": initial_stage_hash, "valid_latency_report": False}
            if name == "status_clean":
                probes = {}
                for label, options in (("reference_retry", []), ("reference_without_fscache", ["-c", "core.fscache=false"]),
                                       ("reference_without_fsmonitor", ["-c", "core.fsmonitor=false"])):
                    try:
                        process = subprocess.run([reference, *options, "status", "--porcelain"], cwd=repo,
                                                 env={**env, "GIT_OPTIONAL_LOCKS": "0"}, capture_output=True, timeout=30)
                        probes[label] = {"exit_code": process.returncode, "output_sample": process.stdout[:2048].decode(errors="replace")}
                    except subprocess.TimeoutExpired:
                        probes[label] = {"timeout": True}
                report["probes"] = probes
                report["current_stage_sha256"] = hashlib.sha256(run(reference, ["ls-files", "--stage", "-z"], cwd=repo)[1]).hexdigest()
                samples = []
                for line in output.splitlines()[:5]:
                    relative = line[3:].decode(errors="replace")
                    path = (repo / relative).resolve()
                    if not path.is_relative_to(repo.resolve()):
                        continue
                    item = {"path": relative, "exists": path.exists(), "is_file": path.is_file()}
                    if path.is_file():
                        item.update(size=path.stat().st_size, sha256=hashlib.sha256(path.read_bytes()).hexdigest())
                    samples.append(item)
                report["live_file_samples"] = samples
            destination = args.output.with_suffix(".failure.json") if args.output else source_root / ".evidence/benchmark-validation-failure.json"
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")
            raise AssertionError((name, key, output[:1024], expected[:1024]))
        versions = {key: run(executable, ["--version"])[1].decode().strip()
                    for key, executable in clients.items()}
        rng = random.Random(233)
        commands = [("version", ["--version"]),
                              ("rev_parse", ["rev-parse", "HEAD"]),
                              ("status_clean", ["status", "--porcelain"]),
                              ("cat_file", ["cat-file", "-p", "HEAD:dir-00/file-000000.txt"])]
        if args.read_commands:
            commands.extend([("diff_clean", ["diff", "--no-ext-diff", "--no-textconv"]),
                             ("show_metadata", ["show", "--no-patch", "--format=fuller"]),
                             ("log_metadata", ["log", "-n", "20", "--format=%H%x00%s"]),
                             ("grep_files", ["grep", "-l", "-F", "benchmark fixture"])])
        for name, command in commands:
            print(f"Measuring {name}", file=sys.stderr, flush=True)
            expected = run(reference, command, cwd=repo)[1]
            samples = {key: [] for key in clients}
            memory = {key: [] for key in clients}
            for key, executable in clients.items():
                for _ in range(3):
                    _, output = run(executable, command, cwd=repo)
                    if name != "version" and output != expected:
                        refuse_output(name, key, output, expected)
            for _ in range(args.runs):
                order = list(clients)
                rng.shuffle(order)
                for key in order:
                    elapsed, output = run(clients[key], command, cwd=repo)
                    if name != "version" and output != expected:
                        refuse_output(name, key, output, expected)
                    samples[key].append(elapsed)
                    if last_peak is not None: memory[key].append(last_peak)
            results[name] = {
                key: {"median_ms": round(statistics.median(values), 3),
                      "p95_ms": round(sorted(values)[math.ceil(len(values) * .95) - 1], 3),
                      "min_ms": round(min(values), 3)}
                for key, values in samples.items()
            }
            for key, values in memory.items():
                results[name][key]['peak_working_set_samples'] = len(values)
                if values:
                    results[name][key].update(peak_working_set_median_bytes=int(statistics.median(values)),
                                             peak_working_set_p95_bytes=sorted(values)[math.ceil(len(values)*.95)-1])
        run(reference, ["fsck", "--full"], cwd=repo)
        print("Verified fsck; cleaning isolated fixture", file=sys.stderr, flush=True)
    assert hashlib.sha256(Path(binary).read_bytes()).hexdigest() == binary_hash, "Binary changed during benchmark; discard this run"
    if baseline:
        assert hashlib.sha256(Path(baseline).read_bytes()).hexdigest() == baseline_identity["binary_sha256"], "Retained binary changed during benchmark"
    report = {"schema_version": 1, "versions": versions,
              "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "binary_sha256": binary_hash,
              "source_tree_sha256": source_hash,
              "source_hash_origin": "recorded checkpoint verified against binary hash" if args.source_checkpoint else "current build inputs",
              "os": platform.platform(), "arch": platform.machine(),
              "cpu_count": os.cpu_count(), "cpu": platform.processor(),
              "cache": "warm after 3 unmeasured runs; OS cache not flushed",
              "scope": "process creation + command + captured output; local disposable SHA-1 repository",
              "files": args.files, "runs_per_client_per_command": args.runs,
              "configured_data_directories": args.directories, "actual_data_directories": min(args.files, args.directories),
              "command_timeout_seconds": args.timeout, "fixture_setup_timeout_seconds": args.setup_timeout,
              "fixture_layout": args.fixture_layout,
              "fixture_local_eol": "input/lf" if args.fixture_layout == "packed" else "existing isolated/system settings",
              "randomized_order_seed": 233, "runtime_git_ssh_on_path": False,
              "client_core_fscache": args.client_fscache,
              "reference_read_safety_overrides": reference_overrides,
              "system_configuration": "enabled conventional filesystem paths" if args.system_config_enabled else "disabled",
              "results": results, "full_git_parity": False,
              "memory_scope": "Windows primary process peak working set; queried after latency interval; child processes excluded"}
    if baseline:
        report["native_baseline_identity"] = baseline_identity
    content = json.dumps(report, indent=2)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(content + "\n", encoding="utf-8", newline="\n")
    print(content)


if __name__ == "__main__":
    main()
