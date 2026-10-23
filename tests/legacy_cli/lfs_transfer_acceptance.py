"""Native HTTPS LFS batch/basic transfer and push-before-ref-update acceptance."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

import argparse
import base64
import datetime
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ipaddress
import json
import os
from pathlib import Path
import secrets
import shutil
import ssl
import subprocess
import tempfile
import threading
from urllib.parse import parse_qs, urlsplit

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID


class Fixture:
    def __init__(self, root):
        self.password = secrets.token_urlsafe(24)
        self.objects = {}
        self.requests = []
        self.client_ports = []
        self.failure = None
        self.verified = []
        self.lock = threading.Lock()
        self.locks_supported = False
        self.asset_locks = {}
        owner = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def handle(self):
                try:
                    super().handle()
                except (ConnectionResetError, BrokenPipeError):
                    pass

            def log_message(self, *_):
                pass

            def respond(self, status, body=b"", kind="application/vnd.git-lfs+json"):
                self.send_response(status)
                self.send_header("Content-Type", kind)
                self.send_header("Content-Length", str(len(body)))
                if status == 401:
                    self.send_header("WWW-Authenticate", 'Basic realm="lfs-fixture"')
                self.end_headers()
                self.wfile.write(body)

            def handle_request(self):
                count = int(self.headers.get("Content-Length", "0"))
                assert 0 <= count <= 8 * 1024 * 1024
                content = self.rfile.read(count)
                basic = "Basic " + base64.b64encode(("fixture:" + owner.password).encode()).decode()
                authorized = self.headers.get("Authorization") == basic
                with owner.lock:
                    owner.requests.append((self.command, self.path, authorized))
                    owner.client_ports.append(self.client_address[1])
                if not authorized:
                    self.respond(401)
                    return
                route = urlsplit(self.path).path
                if owner.locks_supported and (route.endswith('/locks') or '/locks/' in route):
                    value = json.loads(content) if content else {}
                    if route.endswith('/locks/verify'):
                        records = list(owner.asset_locks.values())
                        self.respond(200,json.dumps({'ours':[item for item in records if item['owner']['name']=='fixture'],
                                                     'theirs':[item for item in records if item['owner']['name']!='fixture']}).encode())
                    elif route.endswith('/unlock'):
                        identity = route.split('/')[-2]
                        item = owner.asset_locks.get(identity)
                        if not item: self.respond(404); return
                        if item['owner']['name']!='fixture' and not value.get('force'): self.respond(403); return
                        del owner.asset_locks[identity]
                        self.respond(200,json.dumps({'lock':item}).encode())
                    elif self.command == 'POST':
                        existing = next((item for item in owner.asset_locks.values() if item['path']==value['path']),None)
                        if existing: self.respond(409,json.dumps({'lock':existing,'message':'already locked'}).encode()); return
                        identity = secrets.token_hex(12)
                        item = dict(id=identity,path=value['path'],locked_at='2026-10-21T00:00:00Z',owner={'name':'fixture'})
                        owner.asset_locks[identity] = item
                        self.respond(201,json.dumps({'lock':item}).encode())
                    else:
                        query = parse_qs(urlsplit(self.path).query)
                        items = list(owner.asset_locks.values())
                        if 'path' in query: items=[item for item in items if item['path']==query['path'][0]]
                        self.respond(200,json.dumps({'locks':items}).encode())
                elif self.path.endswith("/objects/batch"):
                    batch = json.loads(content)
                    assert batch["operation"] in ("upload", "download") and "basic" in batch.get("transfers", ["basic"])
                    objects = []
                    for pointer in batch["objects"]:
                        oid, size = pointer["oid"], pointer["size"]
                        item = {"oid": oid, "size": size}
                        if owner.failure == "reject":
                            item["error"] = {"code": 403}
                        elif batch["operation"] == "upload":
                            if oid not in owner.objects:
                                item["actions"] = {"upload": {"href": owner.url + "/object/" + oid},
                                                   "verify": {"href": owner.url + "/verify"}}
                        elif oid not in owner.objects:
                            item["error"] = {"code": 404}
                        else:
                            href = owner.url + "/object/" + oid
                            if owner.failure == "downgrade":
                                href = href.replace("https:", "http:")
                            item["actions"] = {"download": {"href": href}}
                        objects.append(item)
                    if owner.failure == "duplicate":
                        objects *= 2
                    if owner.failure == "omit":
                        objects = []
                    self.respond(200, json.dumps({"transfer": "basic", "objects": objects}).encode())
                elif self.path.endswith("/verify"):
                    value = json.loads(content)
                    if not value:
                        self.respond(200)
                        return
                    assert len(owner.objects[value["oid"]]) == value["size"]
                    owner.verified.append(value["oid"])
                    self.respond(200)
                elif "/object/" in self.path:
                    oid = self.path.rsplit("/", 1)[1]
                    if self.command == "PUT":
                        assert hashlib.sha256(content).hexdigest() == oid
                        owner.objects[oid] = content
                        self.respond(200)
                    else:
                        data = owner.objects[oid]
                        if owner.failure == "corrupt":
                            data = b"x" * len(data)
                        if owner.failure == "oversize":
                            data += b"x"
                        self.respond(200, data, "application/octet-stream")
                else:
                    self.respond(404)

            do_GET = handle_request
            do_POST = handle_request
            do_PUT = handle_request

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        now = datetime.datetime.now(datetime.UTC)
        key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "LFS fixture CA")])
        ca = (x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key())
              .serial_number(x509.random_serial_number()).not_valid_before(now - datetime.timedelta(minutes=1))
              .not_valid_after(now + datetime.timedelta(days=1))
              .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True).sign(key, hashes.SHA256()))
        server_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        cert = (x509.CertificateBuilder().subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "127.0.0.1")]))
                .issuer_name(name).public_key(server_key.public_key()).serial_number(x509.random_serial_number())
                .not_valid_before(now - datetime.timedelta(minutes=1)).not_valid_after(now + datetime.timedelta(days=1))
                .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
                .add_extension(x509.SubjectAlternativeName([x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]), critical=False)
                .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False).sign(key, hashes.SHA256()))
        self.ca = root / "ca.pem"
        self.ca.write_bytes(ca.public_bytes(serialization.Encoding.PEM))
        cert_file, key_file = root / "certificate.pem", root / "key.pem"
        cert_file.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
        key_file.write_bytes(server_key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
        key_file.chmod(0o600)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(cert_file, key_file)
        self.server.socket = tls.wrap_socket(self.server.socket, server_side=True)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"https://127.0.0.1:{self.server.server_port}/lfs"

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(5)


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument("binary", type=Path)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    reference = shutil.which("git")
    reference_lfs = str(Path(reference).with_name("git-lfs.exe"))
    checks = []
    with tempfile.TemporaryDirectory(prefix="git-next-lfs-transfer-") as temporary:
        root = Path(temporary)
        home, repo, target, empty = [root / name for name in ["home", "repo", "target.git", "empty"]]
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / ".config"), GIT_CONFIG_NOSYSTEM="1",
                   GIT_CONFIG_GLOBAL=str(home / ".gitconfig"), GIT_TERMINAL_PROMPT="0", GCM_INTERACTIVE="never",
                   GIT_AUTHOR_NAME="LFS Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                   GIT_COMMITTER_NAME="LFS Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid")

        def git(*command, cwd=repo):
            return subprocess.run([reference, *map(str, command)], cwd=cwd, env=env, check=True, capture_output=True, timeout=30).stdout

        git("init", "-b", "main", repo, cwd=root)
        git("init", "--bare", "-b", "main", target, cwd=root)
        git("remote", "add", "origin", target)
        fixture = Fixture(root)
        try:
            secret, helper_log = root / "private-fixture-secret", root / "helper-actions"
            secret.write_text("fixture\n" + fixture.password, encoding="utf-8")
            secret.chmod(0o600)
            helper_dir = root / "helper directory ~ literal"
            helper_dir.mkdir()
            helper = helper_dir / "git-credential-fixture.exe"
            compiler = shutil.which("rustc") or str(Path.home() / ".cargo/bin/rustc.exe")
            subprocess.run([compiler, str(Path(__file__).parent / "fixtures/credential_helper.rs"), "-O", "-o", str(helper)], check=True, capture_output=True, timeout=60)
            env.update(GIT_NEXT_TEST_SECRET_FILE=str(secret), GIT_NEXT_TEST_HELPER_LOG=str(helper_log))
            git("config", "lfs.url", fixture.url)
            git("config", "http.sslCAInfo", fixture.ca)
            git("config", "credential.helper", helper.as_posix())
            client_env = {**env, "PATH": str(empty), "GIT_EXEC_PATH": str(empty)}

            def native(*command, content=None, success=True, extra=None):
                result = subprocess.run([binary, *map(str, command)], cwd=repo, env={**client_env, **(extra or {})}, input=content, capture_output=True, timeout=40)
                assert (result.returncode == 0) == success, (command, result.returncode, result.stderr)
                return result.stdout

            data = b"\x00HTTPS LFS native transfer\n" * 65537
            pointer = native("lfs", "clean", "asset.bin", content=data)
            oid = hashlib.sha256(data).hexdigest()
            object_path = repo / ".git/lfs/objects" / oid[:2] / oid[2:4] / oid
            (repo / "asset.bin").write_bytes(pointer)
            git("add", ".")
            git("commit", "-m", "fixture: pointer")
            head = git("rev-parse", "HEAD").strip()
            native("lfs", "push", "origin")
            assert fixture.objects[oid] == data and oid in fixture.verified
            puts = sum(method == "PUT" for method, _, _ in fixture.requests)
            native("lfs", "push", "origin")
            assert sum(method == "PUT" for method, _, _ in fixture.requests) == puts
            checks.append("authenticated HTTPS batch upload, PUT, verify and already-present deduplication")

            object_path.unlink()
            assert native("lfs", "smudge", "asset.bin", content=pointer) == data
            assert object_path.read_bytes() == data
            object_path.unlink()
            native("lfs", "fetch", "origin")
            assert object_path.read_bytes() == data
            checks.append("native smudge and fetch download/cache publication with strict fixture CA")

            object_path.unlink()
            git("config", "--unset", "http.sslCAInfo")
            before_requests = len(fixture.requests)
            native("lfs", "smudge", "asset.bin", content=pointer, success=False)
            assert len(fixture.requests) == before_requests and not object_path.exists()
            git("config", "http.sslCAInfo", fixture.ca)
            native("lfs", "fetch", "origin")
            checks.append("untrusted fixture certificate fails before any HTTP request")

            object_path.unlink()
            git("config", "credential.helper", f'!"{helper.as_posix()}"')
            reference_result = subprocess.run([reference_lfs, "smudge", "asset.bin"], input=pointer, cwd=repo, env=env, capture_output=True, timeout=40)
            git("config", "credential.helper", helper.as_posix())
            assert reference_result.returncode == 0 and reference_result.stdout == data, reference_result.stderr
            checks.append("official Git LFS downloads the same batch/basic objects into the shared cache")

            for failure in ["corrupt", "oversize", "duplicate", "omit", "reject", "downgrade"]:
                object_path.unlink(missing_ok=True)
                fixture.failure = failure
                assert native("lfs", "smudge", "asset.bin", content=pointer, success=False) == b""
                assert not object_path.exists()
            fixture.failure = None
            native("lfs", "fetch", "origin")
            checks.append("corrupt/oversized/duplicate/omitted/rejected objects and HTTPS downgrade fail without cache publication")

            fixture.objects.clear()
            before = len(fixture.requests)
            native("push", "--dry-run", "origin", "main")
            assert len(fixture.requests) == before
            fixture.failure = "reject"
            native("push", "origin", "main", success=False)
            refs = git("show-ref", cwd=target) if (target / "refs/heads/main").exists() else b""
            assert refs == b""
            fixture.failure = None
            native("push", "origin", "main")
            assert fixture.objects[oid] == data
            assert git("rev-parse", "HEAD", cwd=target).strip() == head
            checks.append("Git push uploads LFS before publishing refs; LFS failure and dry-run preserve target refs")
            assert set(helper_log.read_text().splitlines()) == {"get"}
            assert secret.read_text() == "fixture\n" + fixture.password
            checks.append("existing credential helper reused without store, erase or saved-login changes")

            second_data = b"second persistent connection fixture\n" * 2048
            second_pointer = native("lfs", "clean", "second.bin", content=second_data)
            second_oid = hashlib.sha256(second_data).hexdigest()
            fixture.objects[second_oid] = second_data
            second_path = repo / ".git/lfs/objects" / second_oid[:2] / second_oid[2:4] / second_oid
            object_path.unlink()
            second_path.unlink()

            def packet(value):
                return f"{len(value)+4:04x}".encode() + value

            def group(values):
                return b"".join(packet(value) for value in values) + b"0000"

            wire = group([b"git-filter-client\n", b"version=2\n"]) + group([b"capability=smudge\n"])
            for value in [pointer, second_pointer]:
                wire += group([b"command=smudge\n", b"pathname=fixture.bin\n"]) + group([value])
            start = len(fixture.client_ports)
            native("lfs", "filter-process", content=wire)
            assert object_path.read_bytes() == data and second_path.read_bytes() == second_data
            assert len(set(fixture.client_ports[start:])) == 1
            checks.append("multiple smudges reuse one verified TLS connection in the persistent native filter")
        finally:
            fixture.close()
        print(json.dumps({"passed": checks, "client_path": "empty", "bytes": len(data)}, indent=2))


if __name__ == "__main__":
    main()
