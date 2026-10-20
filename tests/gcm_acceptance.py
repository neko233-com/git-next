"""Exercise an explicitly selected Windows GCM with temporary DPAPI credentials and native Git CLI."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
from urllib.parse import urlsplit

from push_acceptance import HttpFixture


def snapshot(root):
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file()}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--helper", type=Path, required=True)
    parser.add_argument("--installed-support", action="store_true")
    parser.add_argument("--native-distribution", action="store_true")
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("this acceptance uses Windows DPAPI; native Keychain acceptance is separate")
    binary = args.binary.resolve()
    helper = args.helper.resolve()
    assert binary.is_file() and helper.is_file()
    rustc = os.environ.get("GIT_NEXT_FIXTURE_RUSTC") or shutil.which("rustc") or str(Path.home() / ".cargo/bin/rustc.exe")
    reference = shutil.which("git")
    assert reference
    helper_hash = hashlib.sha256(helper.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="git-next-gcm-") as temporary:
        root = Path(temporary)
        native = root / "native"
        native.mkdir()
        shutil.copy2(binary, native / "git.exe")
        client = binary if args.installed_support or args.native_distribution else native / "git.exe"
        trap_dir = root / "old-git-tripwire"
        trap_dir.mkdir()
        subprocess.run([rustc, "--edition=2024", "--crate-name", "gcm_tripwire", "-O",
                        str(Path(__file__).parent / "fixtures/config_tripwire.rs"),
                        "-o", str(trap_dir / "git.exe")], check=True, capture_output=True, timeout=90)
        marker = root / "git-was-executed"
        home = root / "home"
        home.mkdir()
        config = home / ".gitconfig"
        config.write_text("[test]\nsource = native-fixture\n", encoding="utf-8")
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(("GIT_", "GCM_")) and key.upper() != "EXEPATH"}
        store = root / "dpapi-store"
        env.update(HOME=str(home), USERPROFILE=str(home), APPDATA=str(home / "roaming"),
                   LOCALAPPDATA=str(home / "local"), XDG_CONFIG_HOME=str(home / ".config"),
                   GIT_CONFIG_GLOBAL=str(config), GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0",
                   GIT_EXEC_PATH=str(native), PATH=str(trap_dir), GIT_NEXT_CONFIG_TRIPWIRE=str(marker),
                   GCM_PROVIDER="generic", GCM_INTERACTIVE="never", GCM_ALLOW_WINDOWSAUTH="0",
                   GCM_AUTODETECT_TIMEOUT="-1", GCM_CREDENTIAL_STORE="dpapi", GCM_DPAPI_STORE_PATH=str(store))
        if args.native_distribution:
            env['GIT_EXEC_PATH'] = str(binary.parent)

        def run(program, *arguments, cwd=root, data=None, code=0, extra=None):
            variables={**env, **(extra or {})}
            if args.native_distribution and Path(program)==client: variables.pop('GIT_EXEC_PATH',None)
            result = subprocess.run([str(program), *map(str, arguments)], cwd=cwd, env=variables,
                                    input=data, capture_output=True, timeout=30)
            assert not marker.exists(), "GCM executed the old Git tripwire"
            assert result.returncode == code, (Path(program).name, arguments, Path(cwd).name,
                                               result.returncode, result.stderr if Path(program) == client else b"GCM failed")
            return result.stdout

        repo = root / "repo"
        run(client, "init", repo)
        (repo/'fixture.txt').write_bytes(b'generated DPAPI migration fixture\n')
        for name in ("origin", "second"):
            run(client, "remote", "add", name, f"https://{name}.example.invalid/repo", cwd=repo)
        for working in (repo, repo / ".git"):
            expected = subprocess.run([reference, "rev-parse", "--absolute-git-dir"], cwd=working,
                                      env=env, check=True, capture_output=True).stdout
            assert run(client, "rev-parse", "--absolute-git-dir", cwd=working) == expected
        run(client, "rev-parse", "--absolute-git-dir", code=128)
        expected = subprocess.run([reference, "remote", "-v", "show"], cwd=repo,
                                  env=env, check=True, capture_output=True).stdout
        assert run(client, "remote", "-v", "show", cwd=repo) == expected
        token = secrets.token_urlsafe(32)
        context = b"protocol=https\nhost=fixture.example.invalid\nusername=fixture-user\n"
        run(helper, "store", data=context + f"password={token}\n\n".encode())
        assert store.is_dir() and any(store.rglob("*.credential")), "fixture did not use the isolated DPAPI store"
        before = snapshot(root)
        for working in (root, repo):
            output = run(helper, "get", cwd=working, data=context + b"\n")
            values = dict(line.split(b"=", 1) for line in output.splitlines() if b"=" in line)
            assert values.get(b"username") == b"fixture-user"
            assert values.get(b"password") == token.encode()
        assert snapshot(root) == before, "credential lookup modified the isolated profile or repository"
        backend_env = {**env, "PATH": os.environ.get("PATH", "")}
        backend_env.pop("GIT_EXEC_PATH", None)
        core = subprocess.run([reference, "--exec-path"], env=backend_env, check=True,
                              capture_output=True).stdout.decode().strip()
        backend_env["GIT_EXEC_PATH"] = core
        subprocess.run([reference, "init", "--bare", "-b", "main", str(root / "repository.git")],
                       env=backend_env, check=True, capture_output=True)
        run(client, "add", "-A", cwd=repo)
        run(client, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
            "commit", "-m", "fixture", cwd=repo)
        subprocess.run([reference, "push", str(root / "repository.git"), "main"], cwd=repo,
                       env=backend_env, check=True, capture_output=True)
        expected_refs = subprocess.run([reference, "ls-remote", str(root / "repository.git")],
                                       env=backend_env, check=True, capture_output=True).stdout.splitlines()
        server = HttpFixture(root, str(Path(core) / "git-http-backend.exe"), backend_env, token)
        locator = {"schema_version": 1, "product": "git-next", "credential_helper": str(helper),
                   "native_callback_directory": str(native)}
        locator_path = client.parent / "git-next-support.json"
        if args.native_distribution:
            locator_before = None
        elif args.installed_support:
            locator_before = locator_path.read_bytes()
            assert Path(json.loads(locator_before)["credential_helper"]).samefile(helper)
        else:
            locator_path.write_text(json.dumps(locator) + "\n", encoding="utf-8")
        with config.open("a", encoding="utf-8") as file:
            file.write(f'\n[credential]\nhelper = manager\nusername = fixture\n[http]\nsslCAInfo = "{server.ca_file.as_posix()}"\n')
            if args.native_distribution: file.write('sslBackend = schannel\nschannelUseSSLCAInfo = true\nschannelCheckRevoke = false\n')
        host = urlsplit(server.url).netloc
        http_context = f"protocol=https\nhost={host}\nusername=fixture\n".encode()
        try:
            run(helper, "store", data=http_context + f"password={token}\n\n".encode())
            saved_credentials = snapshot(store)
            advertised = run(client, "ls-remote", server.url, extra={} if args.native_distribution else {"GIT_EXEC_PATH": str(trap_dir)})
            assert expected_refs and sorted(advertised.splitlines()) == sorted(expected_refs)
            assert server.authenticated_requests > 0
            assert snapshot(store) == saved_credentials, "native authentication changed saved credentials"
            if not args.installed_support and not args.native_distribution:
                locator["native_callback_directory"] = str(root / "missing-callback")
                locator_path.write_text(json.dumps(locator) + "\n", encoding="utf-8")
                run(client, "ls-remote", server.url, code=128, extra={"GIT_EXEC_PATH": str(trap_dir)})
            elif not args.native_distribution:
                assert locator_path.read_bytes() == locator_before
            assert snapshot(store) == saved_credentials
        finally:
            server.close()
        assert hashlib.sha256(helper.read_bytes()).hexdigest() == helper_hash
        print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                          "helper_sha256": helper_hash, "existing_helper_get": True,
                          "native_git_callbacks": True, "original_git_tripwire_executed": False,
                          "managed_named_helper_https_authentication": True,
                          "incomplete_support_fails_without_old_git": not args.installed_support,
                          "temporary_dpapi_store_only": True, "get_did_not_modify_files": True,
                          "real_account_login_verified": False, "standalone_helper_packaged": args.installed_support}, indent=2))


if __name__ == "__main__":
    main()
