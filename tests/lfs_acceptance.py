"""Native LFS pointer/cache/filter interoperability with isolated reference tools."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def pkt(value):
    return f"{len(value)+4:04x}".encode() + value


def packets(values):
    return b"".join(pkt(value) for value in values) + b"0000"


def wire_content(content):
    return packets([content[pos:pos+60000] for pos in range(0, len(content), 60000)])


def decode(data):
    groups, current = [], []
    while data:
        length = int(data[:4], 16)
        data = data[4:]
        if length == 0:
            groups.append(current)
            current = []
        else:
            assert 4 < length <= 65520 and len(data) >= length - 4
            current.append(data[:length-4])
            data = data[length-4:]
    assert not current
    return groups


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--reference-lfs", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    lfs = str(args.reference_lfs or Path(reference).with_name("git-lfs.exe"))
    with tempfile.TemporaryDirectory(prefix="git-next-lfs-") as temporary:
        root = Path(temporary)
        home, repo, empty = root / "home", root / "repo", root / "empty-path"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(home / ".gitconfig"),
                   GIT_TERMINAL_PROMPT="0", GCM_INTERACTIVE="never")
        subprocess.run([reference, "init", "-b", "main", repo], env=env, check=True, capture_output=True)

        def run(command, content=b"", native=True, extra=None, success=True):
            variables = {**env, **(extra or {})}
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty),
                                 GIT_SSH=str(empty / "missing"), GIT_SSH_COMMAND=str(empty / "missing"))
            result = subprocess.run(([binary, "lfs"] if native else [lfs]) + command,
                                    input=content, cwd=repo, env=variables, capture_output=True, timeout=30)
            if success:
                assert result.returncode == 0, (command, result.returncode, result.stderr)
            return result

        checks = []
        data = b"\x00native LFS fixture\r\n" * 131073
        expected = run(["clean", "asset.bin"], data, native=False).stdout
        actual = run(["clean", "asset.bin"], data).stdout
        assert actual == expected
        oid = hashlib.sha256(data).hexdigest()
        cache = repo / ".git/lfs/objects" / oid[:2] / oid[2:4] / oid
        assert cache.read_bytes() == data
        assert run(["clean", "asset.bin"], actual).stdout == actual
        assert run(["smudge", "asset.bin"], actual).stdout == data
        assert run(["smudge", "asset.bin"], actual, native=False).stdout == data
        assert run(["clean", "empty"], b"").stdout == b""
        assert run(["smudge", "--skip", "asset.bin"], actual).stdout == actual
        assert run(["smudge", "asset.bin"], actual, extra={"GIT_LFS_SKIP_SMUDGE": "1"}).stdout == actual
        plain = b"ordinary content\r\n" * 5000
        assert run(["smudge", "ordinary"], plain).stdout == plain
        checks.append("reference clean/smudge interoperability, streaming cache, idempotence, empty and skip")

        variants = [actual, actual.replace(b"\n", b"\r\n"), actual.rstrip(b"\n"),
                    b"\n" + actual, actual.replace(b"size ", b"size +"),
                    actual.replace(b"github.com", b"example.invalid"), b"", b"not a pointer",
                    actual.replace(b"sha256:", b"sha512:"), actual + b"unknown value\n",
                    actual.replace(b"size ", b"size -"),
                    actual.replace(b"version https://git-lfs", b"version https://hawser")]
        for value in variants:
            for strict in [False, True]:
                command = ["pointer", "--check", "--stdin"] + (["--strict"] if strict else [])
                theirs = run(command, value, native=False, success=False)
                ours = run(command, value, success=False)
                assert ours.returncode == theirs.returncode, (command, value, theirs.returncode, ours.returncode, ours.stderr)
        checks.append("pointer validity/canonical status differential, legacy versions and invalid inputs")

        cache.write_bytes(b"x" * len(data))
        failed = run(["smudge", "asset.bin"], actual, success=False)
        assert failed.returncode != 0 and failed.stdout == b""
        assert cache.read_bytes() == b"x" * len(data)
        assert run(["clean", "asset.bin"], data, success=False).returncode != 0
        cache.write_bytes(data)
        checks.append("corrupt cache refused without partial output or destructive overwrite")

        greeting = packets([b"git-filter-client\n", b"version=2\n"])
        offered = packets([b"capability=clean\n", b"capability=smudge\n", b"capability=delay\n"])
        missing = actual.replace(oid.encode(), b"f" * 64)
        wire = greeting + offered
        for command, content in [(b"clean", data), (b"smudge", actual), (b"smudge", missing), (b"clean", b"")]:
            wire += packets([b"command=" + command + b"\n", b"pathname=asset with spaces.bin\n"]) + wire_content(content)
        groups = decode(run(["filter-process"], wire).stdout)
        assert groups[0] == [b"git-filter-server\n", b"version=2\n"]
        assert set(groups[1]) == {b"capability=clean\n", b"capability=smudge\n"}
        assert groups[2] == [b"status=success\n"] and b"".join(groups[3]) == actual and groups[4] == []
        assert groups[5] == [b"status=success\n"] and b"".join(groups[6]) == data and groups[7] == []
        assert groups[8] == [b"status=error\n"]
        assert groups[9:] == [[b"status=success\n"], [], []]
        checks.append("filter-process v2 multiple requests, capability negotiation and continued operation after error")

        # The reference Git drives our long-running filter without any Git executable on the child PATH.
        filter_exe = Path(binary).with_name("git-lfs.exe")
        if not filter_exe.is_file():
            filter_exe = Path(binary).with_name("git-next-lfs.exe")
        command = f'"{filter_exe.as_posix()}" filter-process'
        driven_env = {**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty)}
        (repo / ".gitattributes").write_text("*.bin filter=lfs diff=lfs merge=lfs -text\n", encoding="utf-8")
        (repo / "asset.bin").write_bytes(data)
        driven = subprocess.run([reference, "-c", f"filter.lfs.process={command}", "-c", "filter.lfs.required=true", "add", "asset.bin"],
                                cwd=repo, env=driven_env, capture_output=True, timeout=30)
        assert driven.returncode == 0, driven.stderr
        stored = subprocess.run([reference, "show", ":asset.bin"], cwd=repo, env=env, capture_output=True, check=True).stdout
        assert stored == actual
        checks.append("reference Git stages through native filter-process with empty client PATH")

        for key, value in [("user.name", "Fixture"), ("user.email", "fixture@example.invalid"),
                           ("filter.lfs.process", "git-lfs filter-process"), ("filter.lfs.clean", "git-lfs clean -- %f"),
                           ("filter.lfs.smudge", "git-lfs smudge -- %f"), ("filter.lfs.required", "true")]:
            subprocess.run([reference, "config", key, value], cwd=repo, env=env, check=True, capture_output=True)
        native_env = {**driven_env, "GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
                      "GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"}

        def git_next(*command, success=True):
            result = subprocess.run([binary, *command], cwd=repo, env=native_env, capture_output=True, timeout=30)
            assert (result.returncode == 0) == success, (command, result.stderr)
            return result.stdout

        git_next("add", "-A")
        git_next("commit", "-m", "fixture: native LFS")
        git_next("switch", "-c", "other")
        other = b"other LFS file version\n" * 8192
        (repo / "asset.bin").write_bytes(other)
        git_next("add", "asset.bin")
        git_next("commit", "-m", "fixture: other LFS")
        assert git_next("status", "--porcelain") == b""
        git_next("switch", "main")
        assert (repo / "asset.bin").read_bytes() == data
        git_next("switch", "other")
        assert (repo / "asset.bin").read_bytes() == other
        assert git_next("status", "--porcelain") == b""
        checks.append("native add/status/switch route standard LFS filters directly with no Git, shell or OpenSSH on PATH")

        git_next("lfs", "install", "--local")
        git_next("lfs", "track", "*.bin", "asset with spaces.dat")
        before = (repo / ".gitattributes").read_bytes()
        git_next("lfs", "track", "*.bin", "asset with spaces.dat")
        assert (repo / ".gitattributes").read_bytes() == before
        override = b"command-scoped LFS storage\n" * 1025
        (repo / "override.bin").write_bytes(override)
        git_next("-c", "lfs.storage=command-lfs", "add", "override.bin")
        override_oid = hashlib.sha256(override).hexdigest()
        override_cache = repo / ".git/command-lfs/objects" / override_oid[:2] / override_oid[2:4] / override_oid
        assert override_cache.read_bytes() == override
        assert not (repo / ".git/lfs/objects" / override_oid[:2] / override_oid[2:4] / override_oid).exists()
        checks.append("repository-local install, idempotent attribute tracking and -c overrides inherited by native filters")
        attr_lock = repo / ".gitattributes.lock"
        attr_lock.write_bytes(b"another writer")
        before = (repo / ".gitattributes").read_bytes()
        git_next("lfs", "track", "*.blocked", success=False)
        assert attr_lock.read_bytes() == b"another writer" and (repo / ".gitattributes").read_bytes() == before
        attr_lock.unlink()
        post_commit = repo / ".git/hooks/post-commit"
        saved_hook = post_commit.read_bytes()
        post_commit.write_bytes(saved_hook + b"echo custom-hook\n")
        before_head = (repo / ".git/HEAD").read_bytes()
        before_index = (repo / ".git/index").read_bytes()
        git_next("commit", "-m", "must refuse custom hook", success=False)
        assert (repo / ".git/HEAD").read_bytes() == before_head and (repo / ".git/index").read_bytes() == before_index
        before_config = (repo / ".git/config").read_bytes()
        git_next("lfs", "install", "--local", success=False)
        assert (repo / ".git/config").read_bytes() == before_config
        post_commit.write_bytes(saved_hook)
        with (repo / ".gitattributes").open("ab") as attributes:
            attributes.write(b"*.bin lockable\n")
        git_next("add", ".gitattributes")
        git_next("commit", "-m", "must refuse lockable permission semantics", success=False)
        checks.append("attribute writer locks, custom-hook preservation and unsupported lockable semantics fail before publication")
        print(json.dumps({"passed": checks, "client_path": "empty", "fixture_bytes": len(data)}, indent=2))


if __name__ == "__main__":
    main()
