"""Differential acceptance in disposable repositories; reference Git is fixture-only."""
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
    parser.add_argument("--https", action="store_true")
    parser.add_argument("--system-config-enabled", action="store_true")
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference, "Reference Git is required for isolated test fixtures"
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-acceptance-") as tmp:
        root = Path(tmp)
        home = root / "home"
        home.mkdir()
        empty_path = root / "empty-path"
        empty_path.mkdir()
        env = {**os.environ, "HOME": str(home), "USERPROFILE": str(home),
               "GIT_CONFIG_NOSYSTEM": "0" if args.system_config_enabled else "1", "GIT_CONFIG_GLOBAL": str(home / "config"),
               "GIT_AUTHOR_NAME": "Acceptance", "GIT_AUTHOR_EMAIL": "acceptance@example.invalid",
               "GIT_COMMITTER_NAME": "Acceptance", "GIT_COMMITTER_EMAIL": "acceptance@example.invalid"}
        env.pop("GIT_DIR", None)
        env.pop("GIT_WORK_TREE", None)

        def run(exe, *command, cwd=root, client=False, success=True):
            variables = {**env}
            if client:
                variables.update(PATH=str(empty_path), GIT_EXEC_PATH=str(empty_path),
                                 GIT_SSH=str(empty_path / "ssh-does-not-exist"),
                                 GIT_SSH_COMMAND=str(empty_path / "ssh-does-not-exist"))
            result = subprocess.run([exe, *map(str, command)], cwd=cwd, env=variables,
                                    capture_output=True, timeout=90)
            if success and result.returncode:
                raise AssertionError((command, result.returncode, result.stderr.decode(errors="replace")))
            if not success:
                assert result.returncode != 0, command
            return result.stdout

        def new(*command, **kwargs):
            return run(binary, *command, client=True, **kwargs)

        for command in [("--version",), ("-v",), ("version",), ("version", "--build-options")]:
            assert b"git-next" in new(*command)
        identity = json.loads(new("--version", "--json"))
        assert identity["product"] == "git-next" and identity["complete_git_parity"] is False
        readiness = json.loads(new("doctor", "--json"))
        assert readiness["replacement_ready"] is False and readiness["blockers"]
        checks.append("all version forms identify git-next; structured doctor reports actual replacement blockers")

        source = root / "source"
        run(reference, "init", "-b", "main", source)
        (source / "README.md").write_bytes(b"hello\n")
        (source / "folder").mkdir()
        (source / "folder/data.txt").write_bytes(b"nested\n")
        run(reference, "add", ".", cwd=source)
        run(reference, "commit", "-m", "fixture: initial", cwd=source)
        run(reference, "tag", "-a", "v-fixture", "-m", "fixture tag", cwd=source)
        head = run(reference, "rev-parse", "HEAD", cwd=source).strip()
        target = root / "clone"
        new("clone", source, target)
        assert new("rev-parse", "HEAD", cwd=target).strip() == head
        assert new("status", "--porcelain", cwd=target) == b""
        content = new("cat-file", "-p", "HEAD:README.md", cwd=target)
        assert content == b"hello\n", repr(content)
        assert new("hash-object", "README.md", cwd=target) == run(reference, "hash-object", "README.md", cwd=target)
        assert new("show-ref", cwd=target) == run(reference, "show-ref", cwd=target)
        assert new("ls-remote", source) == run(reference, "ls-remote", source)
        run(reference, "fsck", "--full", cwd=target)
        checks.append("local clone, objects, index, annotated tags, refs, default main, fsck; PATH contains no Git/SSH")
        (source / "README.md").write_bytes(b"second\n")
        run(reference, "add", ".", cwd=source)
        run(reference, "commit", "-m", "fixture: next", cwd=source)
        run(reference, "gc", "--prune=now", cwd=source)
        new("fetch", cwd=target)
        expected = run(reference, "rev-parse", "HEAD", cwd=source).strip()
        assert new("rev-parse", "origin/main", cwd=target).strip() == expected
        assert new("rev-parse", "HEAD", cwd=target).strip() == head
        assert (target / "README.md").read_text() == "hello\n"
        new("pull", "--ff-only", cwd=target)
        assert new("rev-parse", "HEAD", cwd=target).strip() == expected
        assert (target / "README.md").read_text() == "second\n"
        run(reference, "fsck", "--full", cwd=target)
        checks.append("incremental local packed fetch and native fast-forward pull update HEAD/index/worktree")
        new("clone", source, target, success=False)
        new("not-a-command", cwd=target, success=False)
        new("push", cwd=target, success=False)
        checks.append("existing clone destination and unknown/unimplemented commands fail closed")
        created = root / "created"
        new("init", created)
        new("config", "user.name", "Acceptance", cwd=created)
        new("config", "user.email", "acceptance@example.invalid", cwd=created)
        (created / "src").mkdir()
        (created / "src/main.txt").write_bytes(b"native write\n")
        new("add", "-A", cwd=created)
        new("commit", "-m", "fixture: native commit", cwd=created)
        assert new("status", "--porcelain", cwd=created) == b""
        assert run(reference, "show", "HEAD:src/main.txt", cwd=created) == b"native write\n"
        run(reference, "fsck", "--full", cwd=created)
        lock = created / ".git/index.lock"
        lock.write_text("other writer", encoding="utf-8")
        new("add", "-A", cwd=created, success=False)
        assert lock.read_text() == "other writer"
        lock.unlink()
        checks.append("native init/add/commit readable by reference Git; index writer lock preserved")
        index_before = (created / ".git/index").read_bytes()
        nested = created / "nested"
        run(reference, "init", "-b", "main", nested)
        (nested / "nested.txt").write_bytes(b"must not flatten nested repository\n")
        new("add", "-A", cwd=created, success=False)
        assert (created / ".git/index").read_bytes() == index_before
        shutil.rmtree(nested)
        (created / ".git/MERGE_HEAD").write_text(head.decode().strip(), encoding="ascii")
        new("add", "-A", cwd=created, success=False)
        assert (created / ".git/index").read_bytes() == index_before
        (created / ".git/MERGE_HEAD").unlink()
        hooks = created / ".git/hooks"
        hooks.mkdir(exist_ok=True)
        hook = hooks / "pre-commit"
        hook.write_bytes(b"#!/bin/sh\nexit 0\n")
        hook.chmod(0o755)
        (created / "another.txt").write_bytes(b"hook must not be bypassed\n")
        new("add", "-A", cwd=created)
        new("commit", "-m", "must refuse active hooks", cwd=created, success=False)
        hook.unlink()
        checks.append("nested repositories, merge state and active hooks refuse without unsafe writes")
        if args.https:
            network = root / "https"
            public_url = "https://github.com/octocat/Hello-World.git"
            expected_refs = run(reference, "ls-remote", public_url).splitlines()
            actual_refs = new("ls-remote", public_url).splitlines()
            assert expected_refs and sorted(actual_refs) == sorted(expected_refs)
            new("clone", "--depth", "1", public_url, network)
            assert new("status", "--porcelain", cwd=network) == b""
            run(reference, "fsck", "--full", cwd=network)
            checks.append("public HTTPS complete advertised refs match reference and shallow clone passes; client PATH contains no Git/SSH")
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                          "system_configuration_enabled": args.system_config_enabled,
                          "checks": checks, "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
