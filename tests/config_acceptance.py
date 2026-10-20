"""Differential config and global command context checks in isolated homes."""
import argparse
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
    with tempfile.TemporaryDirectory(prefix="git-next-config-") as temporary:
        root = Path(temporary)
        home = root / "home"
        home.mkdir()
        empty = root / "empty-path"
        empty.mkdir()
        global_file = home / ".gitconfig"
        global_file.write_text('# global comment\n[user]\n name = Fixture Global\n[test]\n scope = global\n', encoding="utf-8")
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=str(global_file),
                   GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def run(client, *command, cwd=root, extra=None):
            variables = {**env, **(extra or {})}
            if client:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty), GIT_SSH=str(empty / "missing-ssh"),
                                 GIT_SSH_COMMAND=str(empty / "missing-ssh"))
            return subprocess.run([binary if client else reference, *map(str, command)],
                                  cwd=cwd, env=variables, capture_output=True, timeout=20)

        def compare(*command, cwd=root, extra=None):
            expected = run(False, *command, cwd=cwd, extra=extra)
            actual = run(True, *command, cwd=cwd, extra=extra)
            assert (actual.returncode, actual.stdout) == (expected.returncode, expected.stdout), (
                command, "expected", expected.returncode, expected.stdout,
                "actual", actual.returncode, actual.stdout, actual.stderr)
            return actual

        for command in [("config", "--global", "--get", "user.name"),
                        ("config", "--global", "--list"),
                        ("config", "--global", "--show-origin", "--get", "user.name"),
                        ("config", "--get", "test.scope"),
                        ("config", "--get", "missing.key"),
                        ("config", "--get", "--default", "fallback", "missing.key"),
                        ("-c", "test.scope=command", "config", "--get", "test.scope"),
                        ("-c", "test.flag", "config", "--bool", "--get", "test.flag"),
                        ("-c", "test.scope=command", "config", "--show-scope", "--show-origin", "--get", "test.scope")]:
            compare(*command)
        checks.append("global/no-repository reads, missing-key status, defaults and command overrides")
        repo = root / "repo"
        assert run(False, "init", "-b", "main", repo).returncode == 0
        (repo / "nested").mkdir()
        local = repo / ".git/config"
        with local.open("a", encoding="utf-8") as file:
            file.write('\n# local comment\n[test]\n scope = local\n flag\n empty =\n number = 2k\n '
                       'quoted = "hello # world\\nnext"\n value = first\n value = second\n'
                       '[test "Case.Sub"]\n value = preserved case\n')
        included = root / "included config.ini"
        included.write_text('[test]\n included = include-value\n', encoding="utf-8")
        assert run(False, "config", "include.path", str(included), cwd=repo).returncode == 0
        for command in [("config", "--get", "test.scope"),
                        ("config", "--bool", "--get", "test.flag"),
                        ("config", "--bool", "--get", "test.empty"),
                        ("config", "--int", "--get", "test.number"),
                        ("config", "--get", "test.quoted"),
                        ("config", "--get-all", "test.value"),
                        ("config", "--get-regexp", r"^test\."),
                        ("config", "--get-regexp", r"^test\.", "!second"),
                        ("config", "--get", "test.Case.Sub.value"),
                        ("config", "--get", "test.included"),
                        ("config", "--local", "--get", "test.included"),
                        ("config", "--local", "--includes", "--get", "test.included"),
                        ("config", "--null", "--list"),
                        ("config", "--show-origin", "--show-scope", "--get", "test.scope"),
                        ("config", "--null", "--show-origin", "--show-scope", "--list"),
                        ("config", "get", "--all", "test.value"),
                        ("config", "list", "--local"),
                        ("-c", "test.scope=command", "config", "--get", "test.scope")]:
            compare(*command, cwd=repo)
        checks.append("repository precedence, includes, implicit/empty values, multivars, types, regex, NUL and provenance")
        compare("-C", repo, "-C", "nested", "rev-parse", "--show-toplevel")
        compare("--git-dir", repo / ".git", "rev-parse", "--is-bare-repository")
        compare("rev-parse", "--show-toplevel", cwd=root, extra={"GIT_DIR": str(repo / ".git"), "GIT_WORK_TREE": str(repo)})
        checks.append("repeated -C, --git-dir and GIT_DIR/GIT_WORK_TREE environment context")
        conditional = root / "conditional.ini"
        conditional.write_text('[test]\n conditional = matched\n', encoding="utf-8")
        assert run(False, "config", "--global", "--add", "includeIf.gitdir:**/repo/.git.path", str(conditional)).returncode == 0
        compare("config", "--get", "user.name")
        compare("config", "--get", "test.conditional", cwd=repo)
        compare("config", "--global", "--includes", "--get", "test.conditional", cwd=repo)
        checks.append("conditional global includes match repository context and skip safely outside a repository")
        initial = '# preserve this comment\n[test]\n value = first\n value = second\n untouched = keep\n'
        reference_file = root / "write-reference.ini"
        native_file = root / "write-native.ini"
        for command in [("--add", "test.value", "third"),
                        ("--replace-all", "test.value", "replacement", "first|second"),
                        ("--unset", "test.value", "third"),
                        ("test.single", "one"),
                        ("test.single", "two"),
                        ("--unset-all", "test.value"),
                        ("--unset", "test.single"),
                        ("--bool", "test.flag", "yes"),
                        ("--int", "test.number", "3k"),
                        ("set", "--all", "test.value", "modern"),
                        ("set", "--append", "test.value", "append-modern")]:
            if not reference_file.exists():
                reference_file.write_text(initial, encoding="utf-8")
                native_file.write_text(initial, encoding="utf-8")
            if command[0] in ("set", "unset"):
                expected_args = ("config", command[0], "--file", reference_file, *command[1:])
                actual_args = ("config", command[0], "--file", native_file, *command[1:])
            else:
                expected_args = ("config", "--file", reference_file, *command)
                actual_args = ("config", "--file", native_file, *command)
            expected = run(False, *expected_args)
            actual = run(True, *actual_args)
            assert actual.returncode == expected.returncode, (command, expected.returncode, actual.returncode, actual.stderr)
            expected = run(False, "config", "--file", reference_file, "--list")
            actual = run(True, "config", "--file", native_file, "--list")
            assert actual.stdout == expected.stdout, (command, expected.stdout, actual.stdout)
            assert "# preserve this comment" in native_file.read_text()
        checks.append("native add/set/replace/unset and typed writes preserve unrelated comments")
        before = native_file.read_bytes()
        lock = Path(str(native_file) + ".lock")
        lock.write_bytes(b"other writer")
        result = run(True, "config", "--file", native_file, "test.single", "must-not-write")
        assert result.returncode != 0 and native_file.read_bytes() == before and lock.read_bytes() == b"other writer"
        lock.unlink()
        checks.append("exclusive config writer lock leaves existing data and another writer's lock intact")
    print(json.dumps({"status": "pass", "checks": checks, "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
