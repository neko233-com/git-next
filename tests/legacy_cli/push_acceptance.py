"""Differential native push over local, authenticated HTTP and embedded SSH; disposable data only."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

import argparse
import base64
import datetime
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import ipaddress
import os
from pathlib import Path
import secrets
import ssl
import shlex
import shutil
import socket
import subprocess
import tempfile
import threading
from urllib.parse import urlsplit

import paramiko
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID


class HttpFixture:
    def __init__(self, root, backend, environment, password):
        self.password = password
        self.authenticated_requests = 0
        self.after_receive = None
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                self.handle_git()

            def do_POST(self):
                self.handle_git()

            def handle_git(self):
                expected = "Basic " + base64.b64encode(("fixture:" + owner.password).encode()).decode()
                if self.headers.get("Authorization") != expected:
                    self.send_response(401)
                    self.send_header("WWW-Authenticate", 'Basic realm="isolated-fixture"')
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                owner.authenticated_requests += 1
                if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                    body = bytearray()
                    while True:
                        count = int(self.rfile.readline().split(b";")[0], 16)
                        if not count:
                            while self.rfile.readline().strip():
                                pass
                            break
                        assert len(body) + count < 16 * 1024 * 1024
                        body.extend(self.rfile.read(count))
                        assert self.rfile.read(2) == b"\r\n"
                    body = bytes(body)
                else:
                    count = int(self.headers.get("Content-Length", "0"))
                    assert count < 16 * 1024 * 1024
                    body = self.rfile.read(count)
                url = urlsplit(self.path)
                env = {**environment, "GIT_PROJECT_ROOT": str(root), "GIT_HTTP_EXPORT_ALL": "1",
                       "PATH_INFO": url.path, "QUERY_STRING": url.query,
                       "REQUEST_METHOD": self.command, "CONTENT_TYPE": self.headers.get("Content-Type", ""),
                       "CONTENT_LENGTH": str(len(body)), "REMOTE_USER": "fixture",
                       "SERVER_PROTOCOL": "HTTP/1.1", "REMOTE_ADDR": "127.0.0.1"}
                result = subprocess.run([backend], cwd=root, input=body, env=env, capture_output=True, timeout=20)
                assert result.returncode == 0, "fixture HTTP backend failed"
                if self.command == "POST" and url.path.endswith("/git-receive-pack") and owner.after_receive:
                    callback = owner.after_receive
                    owner.after_receive = None
                    callback()
                headers, response = result.stdout.split(b"\r\n\r\n", 1)
                values = [line.decode().split(":", 1) for line in headers.split(b"\r\n")]
                status = next((int(value.strip().split()[0]) for key, value in values if key.lower() == "status"), 200)
                self.send_response(status)
                for key, value in values:
                    if key.lower() not in ("status", "content-length"):
                        self.send_header(key, value.strip())
                self.send_header("Content-Length", str(len(response)))
                self.end_headers()
                self.wfile.write(response)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        now = datetime.datetime.now(datetime.UTC)
        ca_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        ca_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "git-next fixture CA")])
        ca = (x509.CertificateBuilder().subject_name(ca_name).issuer_name(ca_name)
              .public_key(ca_key.public_key()).serial_number(x509.random_serial_number())
              .not_valid_before(now - datetime.timedelta(minutes=1)).not_valid_after(now + datetime.timedelta(days=1))
              .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
              .sign(ca_key, hashes.SHA256()))
        server_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        certificate = (x509.CertificateBuilder()
                       .subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "127.0.0.1")]))
                       .issuer_name(ca_name).public_key(server_key.public_key()).serial_number(x509.random_serial_number())
                       .not_valid_before(now - datetime.timedelta(minutes=1)).not_valid_after(now + datetime.timedelta(days=1))
                       .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
                       .add_extension(x509.SubjectAlternativeName([x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]), critical=False)
                       .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False)
                       .sign(ca_key, hashes.SHA256()))
        self.ca_file = root / "fixture-ca.pem"
        self.ca_file.write_bytes(ca.public_bytes(serialization.Encoding.PEM))
        certificate_file = root / "fixture-certificate.pem"
        certificate_file.write_bytes(certificate.public_bytes(serialization.Encoding.PEM))
        key_file = root / "fixture-tls-key.pem"
        key_file.write_bytes(server_key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
        key_file.chmod(0o600)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(str(certificate_file), str(key_file))
        self.server.socket = tls.wrap_socket(self.server.socket, server_side=True)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"https://127.0.0.1:{self.server.server_port}/repository.git"

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(5)


class SshFixture:
    def __init__(self, root, target, reference, environment):
        self.host = paramiko.RSAKey.generate(2048)
        self.identity = paramiko.RSAKey.generate(2048)
        self.key_file = root / "identity"
        self.identity.write_private_key_file(str(self.key_file))
        self.key_file.chmod(0o600)
        self.listener = socket.socket()
        self.listener.bind(("127.0.0.1", 0))
        self.listener.listen(8)
        self.workers = []
        port = self.listener.getsockname()[1]
        self.known = root / "known_hosts"
        self.known.write_text(f"[127.0.0.1]:{port} {self.host.get_name()} {self.host.get_base64()}\n", encoding="utf-8")
        self.url = f"ssh://fixture@127.0.0.1:{port}/repository.git"
        owner = self

        class Server(paramiko.ServerInterface):
            def __init__(self):
                self.ready = threading.Event()

            def check_auth_publickey(self, username, key):
                return paramiko.AUTH_SUCCESSFUL if username == "fixture" and key == owner.identity else paramiko.AUTH_FAILED

            def get_allowed_auths(self, _):
                return "publickey"

            def check_channel_request(self, kind, _):
                return paramiko.OPEN_SUCCEEDED if kind == "session" else paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

            def check_channel_exec_request(self, channel, command):
                parts = shlex.split(command.decode())
                if parts != ["git-receive-pack", "/repository.git"]:
                    return False
                self.ready.set()
                return True

        def serve(client):
            transport = paramiko.Transport(client)
            process = None
            try:
                transport.add_server_key(owner.host)
                server = Server()
                transport.start_server(server=server)
                channel = transport.accept(10)
                if channel is None or not server.ready.wait(10):
                    return
                process = subprocess.Popen([reference, "receive-pack", str(target)], cwd=root, env=environment,
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
                    client, _ = owner.listener.accept()
                except OSError:
                    return
                worker = threading.Thread(target=serve, args=(client,), daemon=True)
                owner.workers.append(worker)
                worker.start()

        threading.Thread(target=accept, daemon=True).start()

    def close(self):
        self.listener.close()
        for worker in self.workers:
            worker.join(5)


def exercise(binary, reference, mode, root):
    home = root / "home"
    home.mkdir()
    empty = root / "empty-path"
    empty.mkdir()
    environment = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    environment.update(HOME=str(home), USERPROFILE=str(home), GIT_CONFIG_NOSYSTEM="1",
                       GIT_CONFIG_GLOBAL=str(home / "gitconfig"), GIT_TERMINAL_PROMPT="0",
                       GIT_AUTHOR_NAME="Fixture", GIT_COMMITTER_NAME="Fixture",
                       GIT_AUTHOR_EMAIL="fixture@example.invalid", GIT_COMMITTER_EMAIL="fixture@example.invalid")
    source = root / "source"
    target = root / "repository.git"
    checks = []

    def git(*command, cwd=source, success=True):
        result = subprocess.run([reference, *map(str, command)], cwd=cwd, env=environment, capture_output=True, timeout=30)
        assert (result.returncode == 0) == success, (command, result.stderr.decode(errors="replace"))
        return result.stdout

    git("init", "-b", "main", source, cwd=root)
    git("init", "--bare", "-b", "main", target, cwd=root)
    (source / "data.txt").write_bytes(b"native push\n")
    git("add", ".")
    git("commit", "-m", "fixture: initial")
    initial = git("rev-parse", "HEAD").strip().decode()
    server = None
    client_environment = {**environment, "PATH": str(empty), "GIT_EXEC_PATH": str(empty),
                          "GIT_SSH_COMMAND": str(empty / "missing-ssh"), "GIT_SSH": str(empty / "missing-ssh")}
    prefix = []
    if mode == "http":
        git("config", "http.receivepack", "true", cwd=target)
        password = secrets.token_urlsafe(24)
        secret = root / "private-fixture-secret"
        secret.write_text("fixture\n" + password, encoding="utf-8")
        secret.chmod(0o600)
        helper_log = root / "helper-actions"
        client_environment.update(GIT_NEXT_TEST_SECRET_FILE=str(secret), GIT_NEXT_TEST_HELPER_LOG=str(helper_log))
        compiler = shutil.which("rustc") or str(Path.home() / ".cargo/bin" / ("rustc.exe" if os.name == "nt" else "rustc"))
        helper_directory = root / "helper directory ~ literal"
        helper_directory.mkdir()
        helper = helper_directory / ("git-credential-fixture.exe" if os.name == "nt" else "git-credential-fixture")
        subprocess.run([compiler, str(Path(__file__).parent / "fixtures/credential_helper.rs"), "-O", "-o", str(helper)],
                       check=True, capture_output=True, timeout=60)
        git("config", "credential.helper", str(helper).replace("\\", "/"))
        backend = Path(git("--exec-path", cwd=root).decode().strip()) / ("git-http-backend.exe" if os.name == "nt" else "git-http-backend")
        server = HttpFixture(root, str(backend), environment, password)
        git("config", "http.sslCAInfo", server.ca_file)
        url = server.url
    elif mode == "ssh":
        server = SshFixture(root, target, reference, environment)
        url = server.url
        prefix = ["--ssh-identity", str(server.key_file), "--ssh-known-hosts", str(server.known)]
    else:
        url = target.as_uri()

    def native(*command, success=True):
        result = subprocess.run([binary, *prefix, *map(str, command)], cwd=source,
                                env=client_environment, capture_output=True, timeout=45)
        assert (result.returncode == 0) == success, (command, result.stderr.decode(errors="replace"))
        return result.stdout

    def target_head(name="main"):
        return git("rev-parse", f"refs/heads/{name}", cwd=target).strip().decode()

    def files():
        return {str(path.relative_to(target)): hashlib.sha256(path.read_bytes()).hexdigest()
                for path in target.rglob("*") if path.is_file()}

    try:
        native("remote", "add", "origin", url)
        native("push", "-u", "origin", "main")
        assert target_head() == initial
        assert git("config", "branch.main.remote") == b"origin\n"
        assert git("config", "branch.main.merge") == b"refs/heads/main\n"
        native("push")
        checks.append("new branch, upstream and already-current implicit push")
        if mode == "http":
            shutil.copy2(helper, empty / helper.name)
            git("config", "credential.helper", "fixture")
            native("push")
            checks.append("literal helper paths with spaces/tilde and named helpers run directly without Git or a shell")
            before = files()
            server.password = secrets.token_urlsafe(24)
            native("push", success=False)
            assert files() == before
            assert secret.read_text() == "fixture\n" + password
            server.password = password
            checks.append("HTTP authentication failure preserves saved helper login and repository data")
        (source / "data.txt").write_bytes(b"incremental native push\n")
        native("add", "-A")
        native("commit", "-m", "fixture: native increment")
        second = git("rev-parse", "HEAD").strip().decode()
        native("push")
        assert target_head() == second
        assert git("rev-parse", "origin/main").strip().decode() == second
        git("config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/mapped/origin/*")
        git("update-ref", "-d", "refs/remotes/origin/main")
        native("push")
        assert git("rev-parse", "refs/mapped/origin/main").strip().decode() == second
        git("show-ref", "--verify", "refs/remotes/origin/main", success=False)
        git("config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
        native("push")
        checks.append("local tracking updates follow configured fetch refspec mapping")
        if mode == "http":
            (source / "concurrent.dat").write_bytes(b"native concurrent tracking check\n")
            native("add", "-A")
            native("commit", "-m", "fixture: tracking concurrency")
            current = git("rev-parse", "HEAD").strip().decode()
            server.after_receive = lambda: git("update-ref", "refs/remotes/origin/main", initial)
            native("push", success=False)
            assert target_head() == current
            assert git("rev-parse", "origin/main").strip().decode() == initial
            git("update-ref", "refs/remotes/origin/main", current)
            second = current
            checks.append("concurrent local tracking change survives after a successful remote push")
        git("tag", "-a", "v-fixture", "-m", "fixture tag")
        native("push", "--tags", "origin")
        assert git("cat-file", "-t", "refs/tags/v-fixture", cwd=target) == b"tag\n"
        git("fsck", "--full", cwd=target)
        checks.append("incremental pack, annotated tag, local tracking and independent fsck")
        before = files()
        native("push", "--dry-run", "origin", "main:dry-run")
        assert files() == before
        git("show-ref", "--verify", "refs/heads/dry-run", cwd=target, success=False)
        checks.append("dry-run leaves destination objects and refs byte-for-byte unchanged")
        native("push", "--atomic", "origin", "main:one", "main:two")
        assert target_head("one") == second == target_head("two")
        native("push", "--delete", "origin", "one", "two")
        git("show-ref", "--verify", "refs/heads/one", cwd=target, success=False)
        git("show-ref", "--verify", "refs/heads/two", cwd=target, success=False)
        checks.append("atomic multi-ref push and branch deletion")
        other = root / "other"
        git("clone", str(target), other, cwd=root)
        (other / "remote.dat").write_bytes(b"remote-only change\n")
        git("add", ".", cwd=other)
        git("commit", "-m", "fixture: remote update", cwd=other)
        git("push", "origin", "main", cwd=other)
        external = git("rev-parse", "HEAD", cwd=other).strip().decode()
        (source / "local.dat").write_bytes(b"local-only change\n")
        native("add", "-A")
        native("commit", "-m", "fixture: local divergence")
        local = git("rev-parse", "HEAD").strip().decode()
        before = files()
        native("push", "origin", "main", success=False)
        native("push", "--force-with-lease", "origin", "main", success=False)
        assert files() == before and target_head() == external
        native("push", f"--force-with-lease=refs/heads/main:{external}", "origin", "main")
        assert target_head() == local
        checks.append("non-fast-forward and stale lease refuse; exact remote-object lease succeeds")
        git("config", "receive.denyDeletes", "true", cwd=target)
        before = files()
        native("push", "--atomic", "origin", "main:new-ref", ":main", success=False)
        assert target_head() == local
        git("show-ref", "--verify", "refs/heads/new-ref", cwd=target, success=False)
        assert files() == before
        checks.append("remote deletion policy rejects an atomic batch without a partial ref update")
        native("push", "origin", "main:new-ref", ":main", success=False)
        assert target_head("new-ref") == local and target_head() == local
        assert git("rev-parse", "origin/new-ref").strip().decode() == local
        checks.append("non-atomic partial rejection returns failure and records only accepted refs locally")
        git("config", "--unset", "receive.denyDeletes", cwd=target)
        native("push", "--delete", "origin", "new-ref")
        assert git("rev-parse", "--is-bare-repository", cwd=target) == b"true\n"
        reference_delete = subprocess.run([reference, "push", str(target), "--delete", "main"],
                                          cwd=source, env=environment, capture_output=True, timeout=30)
        assert reference_delete.returncode != 0 and b"deletion of the current branch prohibited" in reference_delete.stderr
        native("push", "--delete", "origin", "main", success=False)
        assert target_head() == local
        git("config", "receive.denyDeleteCurrent", "ignore", cwd=target)
        native("push", "--delete", "origin", "main")
        git("show-ref", "--verify", "refs/heads/main", cwd=target, success=False)
        git("fsck", "--full", cwd=target)
        if mode == "http":
            assert server.authenticated_requests > 0
            assert helper_log.read_text().splitlines() and set(helper_log.read_text().splitlines()) == {"get"}
            assert secret.read_text() == "fixture\n" + password
            checks.append("existing HTTP helper login reused; no store/erase and fixture secret unchanged")
        return {"transport": mode, "status": "pass", "checks": checks}
    finally:
        if server:
            server.close()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--transport", choices=["all", "local", "http", "ssh"], default="all")
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    assert reference
    results = []
    with tempfile.TemporaryDirectory(prefix="git-next-push-") as temporary:
        root = Path(temporary)
        for mode in (["local", "http", "ssh"] if args.transport == "all" else [args.transport]):
            directory = root / mode
            directory.mkdir()
            results.append(exercise(binary, reference, mode, directory))
    print(json.dumps({"status": "pass", "binary_sha256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                      "runtime_git_ssh_on_path": False, "results": results, "full_git_parity": False}, indent=2))


if __name__ == "__main__":
    main()
