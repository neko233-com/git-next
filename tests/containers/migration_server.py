"""Reference server and old-client seed; all identities exist only in the disposable Docker volume."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import shutil
import socket
import subprocess
import threading
import time

import paramiko
from push_acceptance import HttpFixture


ROOT = Path('/migration')
PRIVATE = Path('/tmp/reference')
PRIVATE.mkdir(mode=0o700)
HOME = ROOT / 'home'
(HOME / '.ssh').mkdir(parents=True, mode=0o700)
GCM = Path('/tmp/gcm')
shutil.copytree('/inputs/gcm', GCM)
(GCM / 'git-credential-manager').chmod(0o755)
CONFIG = HOME / '.gitconfig'
ENV = {key: value for key, value in os.environ.items() if not key.startswith(('GIT_', 'GCM_', 'SSH_'))}
ENV.update(HOME=str(HOME), USERPROFILE=str(HOME), SSH_ROOT_PATH=str(HOME / '.ssh'),
           XDG_CONFIG_HOME=str(HOME / '.config'), GIT_CONFIG_GLOBAL=str(CONFIG), GIT_CONFIG_NOSYSTEM='1',
           GIT_AUTHOR_NAME='Fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
           GIT_COMMITTER_NAME='Fixture', GIT_COMMITTER_EMAIL='fixture@example.invalid',
           GIT_TERMINAL_PROMPT='0', GCM_PROVIDER='generic', GCM_INTERACTIVE='never',
           GCM_AUTODETECT_TIMEOUT='-1', GCM_CREDENTIAL_STORE='plaintext',
           GCM_PLAINTEXT_STORE_PATH=str(HOME / '.gcm/store'),
           PATH=f'{GCM}:' + os.environ['PATH'])
REFERENCE = shutil.which('git')


def git(*args, cwd=PRIVATE):
    result = subprocess.run([REFERENCE, *map(str, args)], cwd=cwd, env=ENV,
                            capture_output=True, timeout=30)
    assert result.returncode == 0, f'reference fixture command failed: {args[:2]}'
    return result.stdout


TARGET = PRIVATE / 'repository.git'
SEED = PRIVATE / 'seed'
git('init', '--bare', '-b', 'main', TARGET)
git('config', 'http.receivepack', 'true', cwd=TARGET)
git('init', '-b', 'main', SEED)
(SEED / '.gitattributes').write_bytes(b'* text=auto eol=lf\n*.bytes -text\n')
(SEED / 'scene.unity').write_bytes(b'%YAML 1.1\nfixture: original\n')
(SEED / 'data.bytes').write_bytes(b'\x00\r\n\xff\n')
git('add', '-A', cwd=SEED)
git('commit', '-m', 'fixture: before migration', cwd=SEED)
INITIAL = git('rev-parse', 'HEAD', cwd=SEED).decode().strip()
git('remote', 'add', 'origin', TARGET, cwd=SEED)
git('push', 'origin', 'main', cwd=SEED)
PASSWORD = secrets.token_urlsafe(32)
backend = Path(git('--exec-path').decode().strip()) / 'git-http-backend'
HTTP = HttpFixture(PRIVATE, str(backend), ENV, PASSWORD)
shutil.copy2(HTTP.ca_file, ROOT / 'fixture-ca.pem')


class SshFixture:
    def __init__(self):
        self.host = paramiko.RSAKey.generate(2048)
        self.identity = paramiko.RSAKey.generate(2048)
        self.identity.write_private_key_file(str(HOME / '.ssh/id_rsa'))
        (HOME / '.ssh/id_rsa').chmod(0o600)
        self.listener = socket.socket()
        self.listener.bind(('127.0.0.1', 0))
        self.listener.listen(16)
        self.port = self.listener.getsockname()[1]
        self.commands = {'git-upload-pack': 0, 'git-receive-pack': 0}
        self.authenticated = 0
        owner = self

        class Server(paramiko.ServerInterface):
            def __init__(self):
                self.ready = threading.Event()
                self.command = None
                self.environment = {}

            def check_auth_publickey(self, username, key):
                if username == 'fixture' and key == owner.identity:
                    owner.authenticated += 1
                    return paramiko.AUTH_SUCCESSFUL
                return paramiko.AUTH_FAILED

            def get_allowed_auths(self, _):
                return 'publickey'

            def check_channel_request(self, kind, _):
                return paramiko.OPEN_SUCCEEDED if kind == 'session' else paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

            def check_channel_env_request(self, channel, name, value):
                if name == b'GIT_PROTOCOL':
                    self.environment['GIT_PROTOCOL'] = value.decode()
                    return True
                return False

            def check_channel_exec_request(self, channel, command):
                parts = shlex.split(command.decode())
                if len(parts) != 2 or parts[0] not in owner.commands or parts[1] != '/repository.git':
                    return False
                self.command = parts[0]
                owner.commands[self.command] += 1
                self.ready.set()
                return True

        def serve(client):
            transport = paramiko.Transport(client)
            process = None
            try:
                transport.add_server_key(owner.host)
                server = Server()
                transport.start_server(server=server)
                channel = transport.accept(15)
                if channel is None or not server.ready.wait(15):
                    return
                process = subprocess.Popen([REFERENCE, server.command.removeprefix('git-'), str(TARGET)],
                                           cwd=PRIVATE, env={**ENV, **server.environment}, stdin=subprocess.PIPE,
                                           stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)

                def request():
                    try:
                        while data := channel.recv(65536):
                            process.stdin.write(data)
                            process.stdin.flush()
                    except (OSError, EOFError, ValueError):
                        pass
                    finally:
                        try:
                            process.stdin.close()
                        except OSError:
                            pass

                threading.Thread(target=request, daemon=True).start()
                while data := process.stdout.read1(65536):
                    channel.sendall(data)
                channel.send_exit_status(process.wait(15))
                channel.shutdown_write()
                deadline = time.monotonic() + 5
                while transport.is_active() and not channel.closed and time.monotonic() < deadline:
                    time.sleep(0.01)
                if not channel.closed:
                    channel.close()
                deadline = time.monotonic() + 2
                while transport.is_active() and time.monotonic() < deadline:
                    time.sleep(0.01)
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
                    client, _ = self.listener.accept()
                except OSError:
                    return
                threading.Thread(target=serve, args=(client,), daemon=True).start()

        threading.Thread(target=accept, daemon=True).start()


SSH = SshFixture()
(HOME / '.ssh/known_hosts').write_text(f'[127.0.0.1]:{SSH.port} {SSH.host.get_name()} {SSH.host.get_base64()}\n')
(HOME / '.ssh/config').write_text(f'Host fixture\n HostName 127.0.0.1\n User fixture\n Port {SSH.port}\n IdentitiesOnly yes\n IdentityFile ~/.ssh/id_rsa\n')
wrong_host = paramiko.RSAKey.generate(2048)
(ROOT / 'changed-hosts').write_text(f'[127.0.0.1]:{SSH.port} {wrong_host.get_name()} {wrong_host.get_base64()}\n')
(ROOT / 'unknown-hosts').write_text('')
wrong_host.write_private_key_file(str(ROOT / 'wrong-identity'))
(ROOT / 'wrong-identity').chmod(0o600)
CONFIG.write_text('[user]\n name = Fixture\n email = fixture@example.invalid\n'
                  '[core]\n autocrlf = input\n eol = lf\n'
                  '[credential]\n helper = manager\n username = fixture\n'
                  '[http]\n sslCAInfo = /migration/fixture-ca.pem\n')
context = f'protocol=https\nhost=127.0.0.1:{HTTP.server.server_port}\nusername=fixture\n'
result = subprocess.run([str(GCM / 'git-credential-manager'), 'store'],
                        input=(context + f'password={PASSWORD}\n\n').encode(), capture_output=True,
                        env={**ENV, 'GIT_EXEC_PATH': '/usr/bin'}, cwd=ROOT, timeout=30)
assert result.returncode == 0, 'isolated GCM seeding failed'
store = HOME / '.gcm/store'
assert list(store.rglob('*.credential'))
WORKTREE = ROOT / 'old-worktree'
git('clone', TARGET, WORKTREE)
git('remote', 'set-url', 'origin', HTTP.url, cwd=WORKTREE)
git('remote', 'add', 'ssh', 'ssh://fixture/repository.git', cwd=WORKTREE)
assert git('ls-remote', 'origin', cwd=WORKTREE).splitlines()


def snapshot(path):
    return {str(file.relative_to(path)): hashlib.sha256(file.read_bytes()).hexdigest()
            for file in path.rglob('*') if file.is_file()}


def write_json(path, value):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(value) + '\n')
    temporary.replace(path)


write_json(ROOT / 'ready.json', {'initial_head': INITIAL, 'https_url': HTTP.url,
                               'ssh_url': 'ssh://fixture/repository.git',
                               'profile_before': snapshot(HOME), 'worktree_before': snapshot(WORKTREE),
                               'reference_version': git('--version').decode().strip()})
seen = 0
try:
    deadline = time.monotonic() + 540
    while time.monotonic() < deadline:
        request = ROOT / f'request-{seen + 1}.json'
        if not request.exists():
            time.sleep(0.02)
            continue
        action = json.loads(request.read_text())['action']
        seen += 1
        if action == 'advance':
            git('fetch', 'origin', cwd=SEED)
            git('reset', '--hard', 'origin/main', cwd=SEED)
            (SEED / 'from-server.txt').write_bytes(b'fixture server advance\n')
            git('add', '-A', cwd=SEED)
            git('commit', '-m', 'fixture: server advance', cwd=SEED)
            git('push', 'origin', 'main', cwd=SEED)
        elif action == 'reject-password':
            HTTP.password = secrets.token_urlsafe(32)
        elif action == 'restore-password':
            HTTP.password = PASSWORD
        elif action not in ('inspect', 'finish'):
            raise AssertionError('unknown fixture control action')
        git('fsck', '--full', cwd=TARGET)
        for worktree in ROOT.glob('*worktree*'):
            if (worktree / '.git').is_dir():
                git('fsck', '--full', cwd=worktree)
        write_json(ROOT / f'response-{seen}.json', {
            'head': git('rev-parse', 'refs/heads/main', cwd=TARGET).decode().strip(),
            'refs': sorted(git('ls-remote', TARGET).decode().splitlines()),
            'https_authenticated_requests': HTTP.authenticated_requests,
            'ssh_authenticated_sessions': SSH.authenticated,
            'ssh_commands': SSH.commands, 'fsck': 'pass'})
        if action == 'finish':
            break
    else:
        raise AssertionError('fixture aggregate deadline exceeded')
finally:
    HTTP.close()
    SSH.listener.close()
