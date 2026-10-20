"""Filesystem-only prefix/Homebrew discovery; no actual Git executable is available to the client."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    if os.name == "nt":
        parser.error("run this Unix filesystem fixture natively or inside the isolated Linux container")
    binary = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix="git-next-unix-discovery-") as temporary:
        root = Path(temporary)
        home = root / "home"
        home.mkdir()
        marker = root / "git-was-executed"
        trap = b'#!/bin/sh\nprintf invoked > "$GIT_NEXT_CONFIG_TRIPWIRE"\nexit 88\n'
        prefix = root / "custom"
        brew = root / "brew"
        cellar = brew / "Cellar/git/2.54.0"
        for location in (prefix, cellar):
            (location / "bin").mkdir(parents=True)
            (location / "bin/git").write_bytes(trap)
            (location / "bin/git").chmod(0o755)
            (location / "libexec/git-core").mkdir(parents=True)
        (brew / "bin").mkdir()
        (brew / "bin/git").symlink_to("../Cellar/git/2.54.0/bin/git")
        (brew / "opt").mkdir()
        (brew / "opt/git").symlink_to("../Cellar/git/2.54.0", target_is_directory=True)
        shims = root / "shims"
        shims.mkdir()
        (shims / "git").symlink_to(cellar / "bin/git")
        for location, value in ((prefix, "prefix"), (brew, "homebrew"), (cellar, "incorrect-cellar")):
            (location / "etc").mkdir()
            (location / "etc/gitconfig").write_text(
                f"[probe]\nsource = {value}\n[credential]\nhelper = {value}-fixture\n"
                "[include]\npath = relative.inc\n", encoding="utf-8")
            (location / "etc/relative.inc").write_text(f"[probe]\nincluded = {value}\n", encoding="utf-8")
        env = {key: value for key, value in os.environ.items() if not key.startswith(("GIT_", "GCM_"))}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_GLOBAL=str(home / "config"), GIT_NEXT_CONFIG_TRIPWIRE=str(marker))

        def run(path, *arguments, cwd=root, extra=None, code=0):
            variables = {**env, "PATH": os.pathsep.join(map(str, path)), **(extra or {})}
            result = subprocess.run([str(binary), *map(str, arguments)], cwd=cwd, env=variables,
                                    capture_output=True, timeout=30)
            assert not marker.exists(), (arguments, "native discovery executed Git")
            assert result.returncode == code, (arguments, result.returncode, result.stderr)
            return result.stdout

        direct = [prefix / "bin", brew / "bin"]
        assert run(direct, "config", "--get", "probe.source") == b"prefix\n"
        assert run(direct, "config", "--get", "probe.included") == b"prefix\n"
        repo = root / "repo"
        run(direct, "init", repo)
        assert run(direct, "config", "--get", "probe.source", cwd=repo) == b"prefix\n"
        override = root / "override-config"
        override.write_text("[probe]\nsource = override\n", encoding="utf-8")
        originals = {path: path.read_bytes() for path in root.rglob("*") if path.is_file() and "etc" in path.parts}
        cases = 0
        for command_directory in (brew / "bin", brew / "opt/git/bin", cellar / "bin", shims):
            path = [command_directory]
            for working in (root, repo):
                assert run(path, "config", "--get", "probe.source", cwd=working) == b"homebrew\n"
                assert run(path, "config", "--get", "probe.included", cwd=working) == b"homebrew\n"
                assert run(path, "config", "--no-includes", "--get", "probe.source", cwd=working) == b"homebrew\n"
                assert run(path, "config", "--system", "--get", "probe.source", cwd=working) == b"homebrew\n"
                assert run(path, "config", "--show-scope", "--get", "probe.source", cwd=working) == b"system\thomebrew\n"
                assert run(path, "config", "--get-all", "credential.helper", cwd=working) == b"homebrew-fixture\n"
                assert run(path, "config", "--get", "probe.source", cwd=working,
                           extra={"GIT_CONFIG_NOSYSTEM": "0"}) == b"homebrew\n"
                for value in ("1", "true"):
                    run(path, "config", "--get", "probe.source", cwd=working,
                        extra={"GIT_CONFIG_NOSYSTEM": value}, code=1)
                assert run(path, "config", "--get-all", "probe.source", cwd=working,
                           extra={"GIT_CONFIG_SYSTEM": str(override)}) == b"override\n"
                assert run(path, "config", "--system", "--get", "probe.source", cwd=working,
                           extra={"GIT_CONFIG_SYSTEM": str(override)}) == b"override\n"
                run(path, "config", "--get", "probe.source", cwd=working,
                    extra={"GIT_CONFIG_SYSTEM": ""}, code=1)
                run(path, "status", "--porcelain", cwd=repo)
                cases += 1
        assert all(path.read_bytes() == data for path, data in originals.items())
        # Explicit system writes must select the same file as reads and retain relative includes.
        run([brew / "bin"], "config", "--system", "probe.written", "native")
        assert run([brew / "bin"], "config", "--system", "--get", "probe.written") == b"native\n"
        assert run([brew / "bin"], "config", "--get", "probe.included", cwd=repo) == b"homebrew\n"
        assert all(path.read_bytes() == data for path, data in originals.items() if path != brew / "etc/gitconfig")
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                          "scope": "Unix synthetic filesystem conformance; not native macOS/Homebrew installation acceptance",
                          "layouts": ["prefix", "homebrew-bin-symlink", "homebrew-opt-symlink", "homebrew-cellar", "external-symlink"],
                          "brew_layout_context_pairs": cases, "git_tripwire_executed": False,
                          "relative_includes": True, "system_scope_overrides_and_writes": True,
                          "inactive_distribution_ignored": True, "real_account_login_verified": False}, indent=2))


if __name__ == "__main__":
    main()
