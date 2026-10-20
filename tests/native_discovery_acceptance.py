"""System configuration discovery must never execute Git, including a Git-shaped tripwire."""
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
    if os.name != "nt":
        parser.error("this fixture validates Windows installation layouts; native Unix acceptance is separate")
    source_binary = args.binary.resolve()
    suffix = ".exe" if os.name == "nt" else ""
    rustc = shutil.which("rustc") or str(Path.home() / ".cargo/bin" / ("rustc" + suffix))
    with tempfile.TemporaryDirectory(prefix="git-next-native-discovery-") as temporary:
        root = Path(temporary)
        client = root / "client"
        client.mkdir()
        binary = str(client / ("git-next" + suffix))
        shutil.copy2(source_binary, binary)
        home = root / "home"
        home.mkdir()
        install = root / "git-install"
        command = install / "cmd"
        command.mkdir(parents=True)
        core = install / "mingw64/libexec/git-core"
        core.mkdir(parents=True)
        system = install / "etc/gitconfig"
        system.parent.mkdir()
        system.write_text("[probe]\nsource = filesystem\n[credential]\nhelper = fixture-only\n[include]\npath = relative.inc\n", encoding="utf-8")
        system.with_name("relative.inc").write_text("[probe]\nincluded = yes\n", encoding="utf-8")
        trap = command / ("git" + suffix)
        subprocess.run([rustc, "--edition=2024", "--crate-name", "config_tripwire", "-O",
                        str(Path(__file__).parent / "fixtures/config_tripwire.rs"), "-o", str(trap)],
                       check=True, capture_output=True, timeout=90)
        marker = root / "git-was-executed"
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(("GIT_", "GCM_")) and key.upper() != "EXEPATH"}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   PATH=str(command), GIT_CONFIG_GLOBAL=str(home / "config"),
                   GIT_NEXT_CONFIG_TRIPWIRE=str(marker), GIT_NEXT_FIXTURE_CONFIG=str(system),
                   GIT_NEXT_FIXTURE_CORE=str(core),
                   ProgramFiles=str(root / "programs"), ProgramW6432=str(root / "programs"),
                   LOCALAPPDATA=str(root / "local"), PROGRAMDATA=str(root / "programdata"))
        env["ProgramFiles(x86)"] = str(root / "programs-x86")

        def run(*arguments, cwd=root, extra=None, code=0):
            result = subprocess.run([binary, *map(str, arguments)], cwd=cwd, env={**env, **(extra or {})},
                                    capture_output=True, timeout=30)
            assert not marker.exists(), (arguments, "native command executed the Git tripwire")
            assert result.returncode == code, (arguments, result.returncode, result.stderr)
            return result.stdout

        run("--version")
        repo = root / "repo"
        run("init", repo)
        assert run("config", "--get", "probe.source", cwd=repo) == b"filesystem\n"
        assert run("config", "--get", "probe.included", cwd=repo) == b"yes\n"
        assert run("config", "--get-all", "credential.helper", cwd=repo) == b"fixture-only\n"
        for working in (root, repo):
            assert run("config", "--get", "probe.source", cwd=working) == b"filesystem\n"
            assert run("config", "--no-includes", "--get", "probe.source", cwd=working) == b"filesystem\n"
            assert run("config", "--system", "--get", "probe.source", cwd=working) == b"filesystem\n"
            assert run("config", "--get", "probe.source", cwd=working,
                       extra={"GIT_CONFIG_NOSYSTEM": "0"}) == b"filesystem\n"
            run("config", "--get", "probe.source", cwd=working,
                extra={"GIT_CONFIG_NOSYSTEM": "1"}, code=1)
        earlier = root / "programdata/Git/config"
        earlier.parent.mkdir(parents=True)
        earlier.write_text("[probe]\nearlier = yes\nsource = earlier\n[credential]\nhelper = earlier-fixture\n", encoding="utf-8")
        for working in (root, repo):
            assert run("config", "--get", "probe.earlier", cwd=working) == b"yes\n"
            assert run("config", "--get", "probe.source", cwd=working) == b"filesystem\n"
            assert run("config", "--get-all", "credential.helper", cwd=working) == b"earlier-fixture\nfixture-only\n"
            assert run("config", "--no-includes", "--get-all", "credential.helper", cwd=working) == b"earlier-fixture\nfixture-only\n"
        override = root / "override-config"
        override.write_text("[probe]\nsource = override\n", encoding="utf-8")
        assert run("config", "--get", "probe.source", cwd=repo, extra={"GIT_CONFIG_SYSTEM": str(override)}) == b"override\n"
        for working in (root, repo):
            assert run("config", "--get-all", "probe.source", cwd=working,
                       extra={"GIT_CONFIG_SYSTEM": str(override)}) == b"override\n"
            run("config", "--get", "probe.earlier", cwd=working,
                extra={"GIT_CONFIG_SYSTEM": str(override)}, code=1)
        empty = root / "empty"
        empty.mkdir()
        for variables in ({"EXEPATH": str(install)}, {"GIT_EXEC_PATH": str(core)}):
            assert run("config", "--get", "probe.source", cwd=repo,
                       extra={"PATH": str(empty), **variables}) == b"filesystem\n"
        run("rev-parse", "--show-toplevel", cwd=repo)
        run("status", "--porcelain", cwd=repo)
        run("status", "--porcelain", cwd=repo, extra={"GIT_CONFIG_NOSYSTEM": "1"})
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "git_tripwire_executed": False, "system_configuration_enabled": True,
                          "native_system_config": True, "relative_includes": True,
                          "system_override": True, "helper_not_duplicated": True,
                          "programdata_precedence": True, "explicit_exec_path": True,
                          "no_sibling_git": True, "no_system_boolean": True}, indent=2))


if __name__ == "__main__":
    main()
