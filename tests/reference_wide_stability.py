"""Reference-only wide-tree stability diagnostic; never launches a native candidate."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
reference = "C:/Program Files/Git/cmd/git.exe"
with tempfile.TemporaryDirectory(prefix="git-next-reference-wide-") as temporary:
    fixture = Path(temporary)
    home = fixture / "home"
    home.mkdir()
    repo = fixture / "repo"
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_GLOBAL=str(home / "config"), GIT_CONFIG_NOSYSTEM="0")

    def git(*arguments, data=None):
        result = subprocess.run([reference, *arguments], cwd=repo if repo.exists() else fixture,
                                env=env, input=data, capture_output=True, check=True, timeout=180)
        return result.stdout

    git("init", "-b", "main", str(repo))
    stream = bytearray()
    for number in range(10000):
        data = f"benchmark fixture {number}\n".encode() * 16
        stream.extend(f"blob\nmark :{number + 1}\ndata {len(data)}\n".encode() + data + b"\n")
    message = b"fixture: reference-only diagnostic\n"
    stream.extend(b"commit refs/heads/main\ncommitter Fixture <fixture@example.invalid> 981173106 +0000\n")
    stream.extend(f"data {len(message)}\n".encode() + message + b"\n")
    for number in range(10000):
        stream.extend(f"M 100644 :{number + 1} dir-{number % 1000:02d}/file-{number:06d}.txt\n".encode())
    stream.extend(b"\ndone\n")
    git("fast-import", "--quiet", "--done", data=bytes(stream))
    del stream
    git("config", "core.autocrlf", "input")
    git("config", "core.eol", "lf")
    git("reset", "--hard", "HEAD")
    stage_before = hashlib.sha256(git("ls-files", "--stage", "-z")).hexdigest()
    results = []
    for label, overrides in (("default", []), ("without_fscache", ["-c", "core.fscache=false"]),
                             ("without_preload", ["-c", "core.preloadIndex=false"])):
        failures = []
        for iteration in range(60):
            output = git(*overrides, "status", "--porcelain")
            if output:
                sample = output.splitlines()[0][3:].decode()
                path = (repo / sample).resolve()
                assert path.is_relative_to(repo.resolve())
                failures.append({"iteration": iteration, "sample": output[:2048].decode(),
                                 "sample_file_exists": path.is_file(),
                                 "sample_file_sha256": hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None})
        results.append({"configuration": label, "runs": 60, "nonempty_status_count": len(failures), "failures": failures})
        print(label, len(failures), flush=True)
    stage_after = hashlib.sha256(git("ls-files", "--stage", "-z")).hexdigest()
    git("fsck", "--full")
    report = {"scope": "Reference Git-only stability diagnostic; no native client executed; no performance acceptance",
              "reference_version": git("--version").decode().strip(), "files": 10000, "directories": 1000,
              "index_stage_entries_unchanged": stage_before == stage_after,
              "stage_sha256": stage_before, "results": results,
              "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (root / ".evidence/reference-wide-stability.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")
