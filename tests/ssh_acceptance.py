"""Loopback SSH acceptance. Paramiko and reference Git are fixture-only dependencies."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import tempfile
import threading

import paramiko


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    with tempfile.TemporaryDirectory(prefix="git-next-ssh-") as directory:
        root = Path(directory)
        home = root / "home"
        (home / ".ssh").mkdir(parents=True)
        empty = root / "empty-path"
        empty.mkdir()
        env = {**os.environ, "HOME": str(home), "USERPROFILE": str(home),
               "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": str(home / "config"),
               "GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
               "GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid"}
        source = root / "source"
        for command in [["init", "-b", "main", str(source)]]:
            subprocess.run([reference, *command], env=env, check=True, capture_output=True)
        (source / "README.md").write_bytes(b"native ssh\n")
        for command in [["add", "."], ["commit", "-m", "fixture: SSH"]]:
            subprocess.run([reference, *command], cwd=source, env=env, check=True, capture_output=True)
        host = paramiko.RSAKey.generate(2048)
        identity = paramiko.RSAKey.generate(2048)
        key_file = root / "identity"
        identity.write_private_key_file(str(key_file))
        listener = socket.socket()
        listener.bind(("127.0.0.1", 0))
        listener.listen(5)
        port = listener.getsockname()[1]
        known = root / "known_hosts"
        known.write_text(f"[127.0.0.1]:{port} {host.get_name()} {host.get_base64()}\n", encoding="utf-8")
        (home / ".ssh/config").write_text(f"Host fixture\n HostName 127.0.0.1\n User fixture\n Port {port}\n IdentitiesOnly yes\n", encoding="utf-8")

        class Server(paramiko.ServerInterface):
            def __init__(self):
                self.ready = threading.Event()
                self.environment = {}

            def check_auth_publickey(self, username, key):
                return paramiko.AUTH_SUCCESSFUL if username == "fixture" and key == identity else paramiko.AUTH_FAILED

            def get_allowed_auths(self, username):
                return "publickey"

            def check_channel_request(self, kind, channel_id):
                return paramiko.OPEN_SUCCEEDED if kind == "session" else paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

            def check_channel_env_request(self, channel, name, value):
                if name == b"GIT_PROTOCOL":
                    self.environment["GIT_PROTOCOL"] = value.decode()
                    return True
                return False

            def check_channel_exec_request(self, channel, command):
                if shlex.split(command.decode()) != ["git-upload-pack", "/repository.git"]:
                    return False
                self.ready.set()
                return True

        def serve(client):
            transport = paramiko.Transport(client)
            process = None
            try:
                transport.add_server_key(host)
                server = Server()
                transport.start_server(server=server)
                channel = transport.accept(15)
                if channel is None or not server.ready.wait(15):
                    return
                process = subprocess.Popen([reference, "upload-pack", str(source)], env={**env, **server.environment},
                                           stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)

                def request():
                    try:
                        while data := channel.recv(65536):
                            process.stdin.write(data)
                            process.stdin.flush()
                    except (OSError, EOFError):
                        pass
                    finally:
                        process.stdin.close()

                threading.Thread(target=request, daemon=True).start()
                while data := process.stdout.read1(65536):
                    channel.sendall(data)
                channel.send_exit_status(process.wait(10))
                channel.close()
            except (EOFError, OSError, paramiko.SSHException):
                pass
            finally:
                if process and process.poll() is None:
                    process.kill()
                    process.wait()
                transport.close()

        def accept():
            while True:
                try:
                    client, _ = listener.accept()
                except OSError:
                    return
                threading.Thread(target=serve, args=(client,), daemon=True).start()

        threading.Thread(target=accept, daemon=True).start()
        client_env = {**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty), "GIT_SSH_COMMAND": str(empty / "absent")}

        def run(*command, trusted=known, success=True):
            result = subprocess.run([binary, "--ssh-identity", str(key_file), "--ssh-known-hosts", str(trusted), *command],
                                    env=client_env, cwd=root, capture_output=True, timeout=45)
            assert (result.returncode == 0) == success, result.stderr.decode(errors="replace")
            return result.stdout

        try:
            target = root / "clone"
            run("clone", "ssh://fixture/repository.git", str(target))
            assert (target / "README.md").read_bytes() == b"native ssh\n"
            subprocess.run([reference, "fsck", "--full"], cwd=target, env=env, check=True, capture_output=True)
            empty_known = root / "unknown_hosts"
            empty_known.write_text("")
            run("ls-remote", "ssh://fixture/repository.git", trusted=empty_known, success=False)
            other = paramiko.RSAKey.generate(2048)
            wrong = root / "changed_hosts"
            wrong.write_text(f"[127.0.0.1]:{port} {other.get_name()} {other.get_base64()}\n", encoding="utf-8")
            run("ls-remote", "ssh://fixture/repository.git", trusted=wrong, success=False)
            print(json.dumps({"status": "pass", "embedded_ssh_clone": True, "host_config": True,
                              "unknown_host_rejected": True, "changed_host_rejected": True,
                              "client_path_contains_git_or_ssh": False, "production_accessed": False}, indent=2))
        finally:
            listener.close()


if __name__ == "__main__":
    main()
