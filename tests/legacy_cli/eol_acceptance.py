"""LF defaults, editor-only rewrites and deliberate normalization against reference Git."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

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
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-lf-") as temporary:
        root = Path(temporary)
        home, empty = root / "home", root / "empty"
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / "config"),
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def run(native, *command, cwd=root, success=True, extra=None):
            variables = {**env, **(extra or {})}
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            result = subprocess.run([binary if native else reference, *map(str, command)], cwd=cwd,
                                    env=variables, capture_output=True, timeout=30)
            assert (result.returncode == 0) == success, (command, result.returncode, result.stdout, result.stderr)
            return result.stdout

        def git(*command, **options):
            return run(False, *command, **options)

        git("config", "--global", "core.autocrlf", "true")
        repo = root / "project"
        run(True, "init", repo)
        for name in [".gitattributes", ".editorconfig"]:
            assert b"\r" not in (repo / name).read_bytes()
        assert git("config", "--local", "core.autocrlf", cwd=repo).strip() == b"input"
        assert git("config", "--local", "core.eol", cwd=repo).strip() == b"lf"
        text = {"scene.unity": b"%YAML 1.1\n--- !u!1 &1\nm_Name: Fixture\n",
                "prefab.prefab": b"%YAML 1.1\n--- !u!1 &2\nm_Name: Fixture\n",
                "settings.asset": b"%YAML 1.1\n--- !u!114 &3\nm_Name: Fixture\n",
                "script.cs.meta": b"fileFormatVersion: 2\nguid: 00000000000000000000000000000000\n",
                "script.cs": b"class Fixture { }\n"}
        binary_data = {"binary.asset": b"\0asset\r\nbytes\xff\0", "texture.png": b"\x89PNG\0\r\nfixture\0"}
        for name, content in text.items():
            (repo / name).write_bytes(content.replace(b"\n", b"\r\n"))
        for name, content in binary_data.items():
            (repo / name).write_bytes(content)
        run(True, "add", "-A", cwd=repo)
        run(True, "commit", "-m", "fixture LF files", cwd=repo)
        for name, content in {**text, **binary_data}.items():
            assert git("show", f"HEAD:{name}", cwd=repo) == content, name
        before = git("ls-files", "--stage", cwd=repo)
        for name, content in text.items():
            (repo / name).write_bytes(content)
        status = run(True, "status", "--porcelain", cwd=repo)
        assert status == b"", (status, git("status", "--porcelain", cwd=repo), run(True, "diff", cwd=repo))
        for name, content in text.items():
            (repo / name).write_bytes(content.replace(b"\n", b"\r\n"))
        status = run(True, "status", "--porcelain", cwd=repo)
        assert status == b"", (status, git("status", "--porcelain", cwd=repo), run(True, "diff", cwd=repo))
        assert run(True, "diff", cwd=repo) == b""
        assert git("status", "--porcelain", cwd=repo) == b""
        run(True, "add", "-A", cwd=repo)
        assert git("ls-files", "--stage", cwd=repo) == before
        checks.append("new repositories use shared LF/editor rules despite a CRLF global preference; binary bytes remain exact")
        checks.append("Unity YAML/meta/C# LF-to-CRLF editor rewrites remain clean in both clients without changing staged IDs")
        (repo / "script.cs").write_bytes(b"class Fixture { int Value; }\r\n")
        assert b"script.cs" in run(True, "status", "--porcelain", cwd=repo)
        assert b"int Value" in run(True, "diff", cwd=repo)
        clone = root / "clone"
        run(True, "clone", repo, clone)
        for name, content in {**text, **binary_data}.items():
            assert (clone / name).read_bytes() == content, (name, "clone bytes differ")
        assert run(True, "status", "--porcelain", cwd=clone) == b""
        checks.append("fresh local clones use LF for text, preserve binaries and still expose real content changes")

        custom = root / "custom"
        custom.mkdir()
        attributes = b"*.win text eol=crlf\r\n*.asset -text\r\n"
        editor = b"root = true\r\n[*.win]\r\nend_of_line = crlf\r\n"
        (custom / ".gitattributes").write_bytes(attributes)
        (custom / ".editorconfig").write_bytes(editor)
        run(True, "init", custom)
        assert (custom / ".gitattributes").read_bytes() == attributes
        assert (custom / ".editorconfig").read_bytes() == editor
        (custom / "explicit.win").write_bytes(b"explicit\r\n")
        run(True, "add", "-A", cwd=custom)
        run(True, "commit", "-m", "explicit exception", cwd=custom)
        (custom / "explicit.win").write_bytes(b"dirty\n")
        run(True, "restore", "explicit.win", cwd=custom)
        assert (custom / "explicit.win").read_bytes() == b"explicit\r\n"
        report = json.loads(run(True, "eol", "--apply", "--json", cwd=custom))
        assert report["created"] == [] and report["index_renormalized"] is False
        assert (custom / ".gitattributes").read_bytes() == attributes
        assert (custom / ".editorconfig").read_bytes() == editor
        checks.append("existing attributes/editor settings and explicit CRLF exceptions survive init/policy application")

        legacy = root / "legacy"
        git("init", "-b", "main", legacy)
        git("config", "core.autocrlf", "false", cwd=legacy)
        (legacy / "script.cs").write_bytes(b"legacy\r\nsource\r\n")
        (legacy / "binary.bytes").write_bytes(b"\0binary\r\n\0")
        git("add", ".", cwd=legacy)
        git("commit", "-m", "legacy CRLF fixture", cwd=legacy)
        head, index = git("rev-parse", "HEAD", cwd=legacy), (legacy / ".git/index").read_bytes()
        report = json.loads(run(True, "eol", "--apply", "--json", cwd=legacy))
        assert set(report["created"]) == {".gitattributes", ".editorconfig"}
        assert (legacy / ".git/index").read_bytes() == index and git("rev-parse", "HEAD", cwd=legacy) == head
        (legacy / "untracked.cs").write_bytes(b"do not stage\r\n")
        oracle = root / "legacy-reference"
        shutil.copytree(legacy, oracle)
        lock = legacy / ".git/index.lock"
        lock.write_bytes(b"another writer\n")
        run(True, "add", "--renormalize", ".", cwd=legacy, success=False)
        assert lock.read_bytes() == b"another writer\n" and (legacy / ".git/index").read_bytes() == index
        lock.unlink()
        worktree = {name: (legacy / name).read_bytes() for name in ["script.cs", "binary.bytes", "untracked.cs"]}
        run(True, "add", "--renormalize", ".", cwd=legacy)
        git("add", "--renormalize", ".", cwd=oracle)
        assert git("ls-files", "--stage", cwd=legacy) == git("ls-files", "--stage", cwd=oracle)
        assert git("diff", "--cached", cwd=legacy) == git("diff", "--cached", cwd=oracle)
        assert git("show", ":script.cs", cwd=legacy) == b"legacy\nsource\n"
        assert git("show", ":binary.bytes", cwd=legacy) == worktree["binary.bytes"]
        assert all((legacy / name).read_bytes() == data for name, data in worktree.items())
        assert b"untracked.cs" not in git("ls-files", cwd=legacy)
        checks.append("policy application does not stage content; explicit renormalize matches Git, preserves worktree/binaries and excludes untracked files")
        unconfigured = root / "unconfigured"
        git("init", "-b", "main", unconfigured)
        missing_profile = {"GIT_CONFIG_GLOBAL": str(home / "absent-config")}
        (unconfigured / "default.cs").write_bytes(b"default\r\nLF\r\n")
        config_before = (unconfigured / ".git/config").read_bytes()
        run(True, "add", "-A", cwd=unconfigured, extra=missing_profile)
        assert git("show", ":default.cs", cwd=unconfigured) == b"default\nLF\n"
        assert (unconfigured / ".git/config").read_bytes() == config_before
        run(True, "commit", "-m", "unconfigured LF fixture", cwd=unconfigured, extra=missing_profile)
        (unconfigured / "default.cs").unlink()
        run(True, "restore", "default.cs", cwd=unconfigured, extra=missing_profile)
        assert (unconfigured / "default.cs").read_bytes() == b"default\nLF\n"
        checks.append("unconfigured existing repositories use LF fallback without rewriting persistent configuration")
        config = legacy / ".git/config"
        before = config.read_bytes()
        config_lock = legacy / ".git/config.lock"
        config_lock.write_bytes(b"another config writer\n")
        run(True, "eol", "--apply", cwd=legacy, success=False)
        assert config.read_bytes() == before and config_lock.read_bytes() == b"another config writer\n"
        config_lock.unlink()
        invalid = root / "invalid-policy"
        git("init", "-b", "main", invalid)
        (invalid / ".gitattributes").mkdir()
        before = (invalid / ".git/config").read_bytes()
        run(True, "eol", "--apply", cwd=invalid, success=False)
        assert (invalid / ".git/config").read_bytes() == before
        checks.append("configuration locks and non-file policy paths fail before changing configuration")
        git("fsck", "--full", cwd=legacy)
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(), "checks": checks, "runtime_git_on_path": False}, indent=2))


if __name__ == "__main__":
    main()
