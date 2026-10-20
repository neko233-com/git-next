"""Run local-only Linux fixtures with read-only host inputs and disposable tmpfs storage."""
import argparse
import json
from pathlib import Path
import subprocess
import uuid

root = Path(__file__).resolve().parents[2]
parser = argparse.ArgumentParser(__doc__)
parser.add_argument("--arm64", action="store_true", help="emulated Docker ARM64 conformance, not hardware performance")
parser.add_argument("--timeout", type=int, default=1800, help="aggregate seconds; QEMU is not native speed")
args = parser.parse_args()
assert 60 <= args.timeout <= 7200
triple = "aarch64-unknown-linux-gnu" if args.arm64 else "x86_64-unknown-linux-musl"
platform = "linux/arm64" if args.arm64 else "linux/amd64"
name = "git-next-local-acceptance-arm64:20261020" if args.arm64 else "git-next-local-acceptance:20261020"
stem = "linux-runtime-arm64" if args.arm64 else "linux-runtime"
mounts = [(root / "target" / triple / "release", "/opt/native"),
          (root / "tests", "/tests"), (Path(__file__).parent, "/harness")]
owned_name = "git-next-runtime-" + uuid.uuid4().hex
command = ["docker", "run", "--rm", "--name", owned_name,
           "--label", "git-next.acceptance.run=" + owned_name, "--platform", platform, "--network", "none", "--read-only",
           "--tmpfs", "/tmp:rw,exec,size=512m", "--tmpfs", "/acceptance:rw,exec,size=64m"]
for source, destination in mounts:
    command += ["--mount", f"type=bind,source={source.resolve()},target={destination},readonly"]
image = subprocess.check_output(["docker", "image", "inspect", name, "--format", "{{.Id}}"], text=True).strip()
command += [image, "python", "/harness/run.py"]
try:
    result = subprocess.run(command, capture_output=True, timeout=args.timeout)
except subprocess.TimeoutExpired as error:
    result = subprocess.CompletedProcess(command, 124, error.stdout or b"", (error.stderr or b"") + b"\nHost aggregate deadline exceeded.\n")
finally:
    probe = subprocess.run(["docker", "inspect", owned_name], capture_output=True)
    if probe.returncode == 0:
        owned = json.loads(probe.stdout)[0]
        if owned["Config"]["Labels"].get("git-next.acceptance.run") != owned_name or owned["Image"] != image:
            raise RuntimeError("Unexpected owner of runtime acceptance container")
        subprocess.run(["docker", "rm", "--force", owned_name], capture_output=True, check=True)
    absent = subprocess.run(["docker", "inspect", owned_name], capture_output=True)
    if absent.returncode == 0:
        raise RuntimeError("Owned runtime acceptance container was not removed")
(root / ".evidence").mkdir(exist_ok=True)
(root / ".evidence" / (stem + "-output.txt")).write_bytes(result.stdout + b"\n" + result.stderr)
print(result.stderr.decode(errors="replace"))
try:
    report = json.loads(result.stdout)
except json.JSONDecodeError:
    report = {"status": "fail", "exit_code": result.returncode,
              "reason": "Container exceeded its aggregate deadline or returned no complete JSON report"}
report["image_id"] = image
report["target"] = triple
report["container_platform"] = platform
report["emulated_arm64"] = args.arm64
report["aggregate_timeout_seconds"] = args.timeout
report["owned_container_removed"] = True
if args.arm64:
    report["scope"] = "Emulated Linux ARM64 Docker local LF/status/restore/read/worktree/commit/format/Unix-discovery subset; no physical ARM64 or performance acceptance"
(root / ".evidence" / (stem + ".json")).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")
print(json.dumps(report, indent=2))
raise SystemExit(result.returncode)
