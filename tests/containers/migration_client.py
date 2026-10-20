"""Run native-only migration/HTTPS/SSH/LF acceptance; never export generated credentials."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time
import tarfile
import shlex


ROOT = Path('/migration')
HOME = ROOT / 'home'
WORKTREE = ROOT / 'old-worktree'
assert all(not Path(path).exists() for path in ('/usr/bin/git', '/usr/bin/ssh', '/usr/lib/git-core'))
assert shutil.which('git') is None and shutil.which('ssh') is None
for package in ('git', 'git-lfs', 'openssh-client'):
    installed = subprocess.run(['dpkg-query', '-W', '-f=${db:Status-Status}', package], capture_output=True)
    assert installed.stdout != b'installed', 'client image contains an original Git/OpenSSH package'
NATIVE = Path('/tmp/native')
NATIVE.mkdir()
FULL = os.environ.get('GIT_NEXT_NATIVE_PACKAGE') == '1'
if FULL:
    with tarfile.open('/inputs/native-package.tar.gz') as package:
        package.extractall(NATIVE,filter='data')
    CLIENT = NATIVE/'bin/git'
    GCM = NATIVE/'runtime/usr/lib/git-next/gcm'
else:
    for name in ('git-next', 'git-lfs', 'git-next-lfs-filter', 'git-terminal'):
        shutil.copy2(Path('/inputs/native') / name, NATIVE / name)
        (NATIVE / name).chmod(0o755)
    shutil.copy2(NATIVE / 'git-next', NATIVE / 'git')
    CLIENT = NATIVE/'git'
    GCM = Path('/tmp/gcm')
    shutil.copytree('/inputs/gcm', GCM)
    (GCM / 'git-credential-manager').chmod(0o755)
LOCATOR = NATIVE / 'git-next-support.json'
locator = {'schema_version': 1, 'product': 'git-next',
           'credential_helper': str(GCM / 'git-credential-manager'), 'native_callback_directory': str(NATIVE)}
LOCATOR.write_text(json.dumps(locator) + '\n')
EMPTY = Path('/tmp/empty-path')
EMPTY.mkdir()
ENV = {key: value for key, value in os.environ.items() if not key.startswith(('GIT_', 'GCM_', 'SSH_'))}
ENV.update(HOME=str(HOME), USERPROFILE=str(HOME), SSH_ROOT_PATH=str(HOME / '.ssh'),
           XDG_CONFIG_HOME=str(HOME / '.config'), GIT_CONFIG_GLOBAL=str(HOME / '.gitconfig'),
           GIT_CONFIG_NOSYSTEM='1', GIT_TERMINAL_PROMPT='0', GIT_OPTIONAL_LOCKS='0',
           GIT_EXEC_PATH=str(EMPTY), PATH=str(EMPTY), GIT_SSH=str(EMPTY / 'absent-ssh'),
           GIT_SSH_COMMAND=str(EMPTY / 'absent-ssh'), GCM_PROVIDER='generic', GCM_INTERACTIVE='never',
           GCM_AUTODETECT_TIMEOUT='-1', GCM_CREDENTIAL_STORE='plaintext',
           GCM_PLAINTEXT_STORE_PATH=str(HOME / '.gcm/store'))
if FULL:
    for name in ('GIT_EXEC_PATH','GIT_SSH','GIT_SSH_COMMAND'):
        ENV.pop(name,None)
    ENV['PATH'] = str(NATIVE/'bin')+':'+str(NATIVE/'runtime/usr/bin')+':/usr/local/bin:/usr/bin:/bin'
READY = json.loads((ROOT / 'ready.json').read_text())
CHECKS = []
SEQUENCE = 0


def snapshot(path):
    return {str(file.relative_to(path)): hashlib.sha256(file.read_bytes()).hexdigest()
            for file in path.rglob('*') if file.is_file()}


def native(*args, cwd=WORKTREE, success=True, extra=None):
    result = subprocess.run([str(CLIENT), *map(str, args)], cwd=cwd,
                            env={**ENV, **(extra or {})}, capture_output=True, timeout=45)
    # Diagnostics deliberately exclude helper output and generated credential contents.
    detail=re.sub(r'[A-Za-z0-9_+=/-]{32,}','<redacted>',result.stderr.decode(errors='replace')[-450:])
    assert (result.returncode == 0) == success, f'native command expectation failed: {args[:2]}, exit={result.returncode}, detail={detail}'
    return result.stdout


def control(action):
    global SEQUENCE
    SEQUENCE += 1
    target = ROOT / f'request-{SEQUENCE}.json'
    temporary = target.with_suffix('.tmp')
    temporary.write_text(json.dumps({'action': action}))
    temporary.replace(target)
    response = ROOT / f'response-{SEQUENCE}.json'
    deadline = time.monotonic() + 40
    while not response.exists():
        assert time.monotonic() < deadline, 'reference fixture control deadline exceeded'
        time.sleep(0.02)
    result = json.loads(response.read_text())
    assert result['fsck'] == 'pass'
    return result


def preserved_failure(*args, extra=None):
    before = stable_repository_snapshot()
    profile = snapshot(HOME)
    native(*args, success=False, extra=extra)
    assert stable_repository_snapshot() == before, 'failed native operation changed the old repository'
    assert snapshot(HOME) == profile, 'failed native operation changed the migrated profile'
    assert not list(WORKTREE.rglob('*.lock')), 'failed native operation left a writer lock'


def stable_repository_snapshot():
    values=snapshot(WORKTREE)
    # The full Git engine truncates this diagnostic file before a failed fetch.
    if FULL: values.pop('.git/FETCH_HEAD',None)
    return values


assert b'git-next' in native('--version')
assert snapshot(HOME) == READY['profile_before']
assert snapshot(WORKTREE) == READY['worktree_before']
assert native('rev-parse', 'HEAD').decode().strip() == READY['initial_head']
assert native('status', '--porcelain=v1', '-z') == b''
assert snapshot(WORKTREE) == READY['worktree_before']
CHECKS.append('old Git repository/index/config read directly with native branded git; optional locks disabled')

scene = WORKTREE / 'scene.unity'
scene.write_bytes(scene.read_bytes().replace(b'\n', b'\r\n'))
assert native('status', '--porcelain=v1', '-z') == b''
assert native('diff', '--exit-code') == b''
assert (WORKTREE / 'data.bytes').read_bytes() == b'\x00\r\n\xff\n'
scene.write_bytes(scene.read_bytes().replace(b'original', b'real edit'))
assert native('status', '--porcelain=v1', '-z') == b' M scene.unity\0'
native('add', 'scene.unity')
assert b'\r' not in native('show', ':scene.unity')
native('commit', '-m', 'fixture: native post-migration HTTPS')
CHECKS.append('Unity-style pure CRLF save stays clean; real edit remains visible and stages canonical LF; binary bytes preserved')

expected = control('inspect')['refs']
assert sorted(native('ls-remote', READY['https_url']).decode().splitlines()) == expected
native('push', 'origin', 'main')
head = native('rev-parse', 'HEAD').decode().strip()
assert control('inspect')['head'] == head
CHECKS.append('migrated named manager helper authenticates private HTTPS read and native push without changing credential files')

HTTPS_CLONE = ROOT / 'https-worktree'
native('clone', READY['https_url'], HTTPS_CLONE, cwd=ROOT)
assert native('rev-parse', 'HEAD', cwd=HTTPS_CLONE).decode().strip() == head
assert b'\r' not in (HTTPS_CLONE / 'scene.unity').read_bytes()
assert (HTTPS_CLONE / 'data.bytes').read_bytes() == b'\x00\r\n\xff\n'
CHECKS.append('private authenticated HTTPS clone checks out LF text and exact binary bytes')

remote_head = control('advance')['head']
native('fetch', 'origin')
assert native('rev-parse', 'origin/main').decode().strip() == remote_head
native('pull', '--ff-only', 'origin')
assert native('rev-parse', 'HEAD').decode().strip() == remote_head
assert (WORKTREE / 'from-server.txt').read_bytes() == b'fixture server advance\n'
CHECKS.append('private authenticated HTTPS incremental fetch and ff-only pull preserve the migrated repository')

before_remote = control('inspect')['head']
saved_login = {file:file.read_bytes() for file in (HOME/'.gcm/store').rglob('*') if file.is_file()}
control('reject-password')
if FULL:
    before = stable_repository_snapshot()
    native('fetch','origin',success=False)
    native('push','origin','main',success=False)
    assert stable_repository_snapshot()==before
else:
    preserved_failure('fetch', 'origin')
    preserved_failure('push', 'origin', 'main')
control('restore-password')
if FULL:
    # Git may erase a rejected login; restore only this generated fixture for later cases.
    for file,data in saved_login.items():
        file.parent.mkdir(parents=True,exist_ok=True)
        file.write_bytes(data)
assert control('inspect')['head'] == before_remote
CHECKS.append('rejected HTTPS login preserves repository/remote refs; generated fixture login restored after helper invalidation' if FULL else 'rejected saved HTTPS login leaves repository/profile/remote refs unchanged and retains the saved login')
preserved_failure('fetch', 'origin', extra={'GCM_PLAINTEXT_STORE_PATH': '/tmp/missing-login'})
CHECKS.append('missing login refuses without prompting, repository changes or credential deletion')
preserved_failure('-c', 'http.sslCAInfo=/etc/ssl/certs/ca-certificates.crt', 'fetch', 'origin')
CHECKS.append('private untrusted TLS certificate refuses; verification remains enabled')
if FULL:
    helper = GCM/'git-credential-manager'
    disabled = helper.with_suffix('.fixture-disabled')
    helper.rename(disabled)
    try: preserved_failure('fetch','origin')
    finally: disabled.rename(helper)
else:
    LOCATOR.write_text(json.dumps({**locator, 'native_callback_directory': '/tmp/missing-native-callback'}))
    preserved_failure('fetch', 'origin')
    LOCATOR.write_text(json.dumps(locator) + '\n')
CHECKS.append('incomplete managed support refuses without original Git fallback or profile/repository writes')

expected = control('inspect')['refs']
assert sorted(native('ls-remote', READY['ssh_url']).decode().splitlines()) == expected
SSH_CLONE = ROOT / 'ssh-worktree'
native('clone', READY['ssh_url'], SSH_CLONE, cwd=ROOT)
assert native('rev-parse', 'HEAD', cwd=SSH_CLONE).decode().strip() == remote_head
(SSH_CLONE / 'ssh.txt').write_bytes(b'native SSH post-migration\n')
native('add', 'ssh.txt', cwd=SSH_CLONE)
native('commit', '-m', 'fixture: native post-migration SSH', cwd=SSH_CLONE)
native('push', 'origin', 'main', cwd=SSH_CLONE)
assert control('inspect')['head'] == native('rev-parse', 'HEAD', cwd=SSH_CLONE).decode().strip()
native('fetch', 'ssh')
CHECKS.append('existing SSH config/private key/known_hosts authenticate read, clone, fetch and push with packaged primary SSH' if FULL else 'existing SSH config/private key/known_hosts authenticate native read, clone, fetch and push without OpenSSH')
for hosts in ('unknown-hosts', 'changed-hosts'):
    if FULL:
        command = shlex.quote(str(NATIVE/'runtime/usr/bin/ssh'))+' -oStrictHostKeyChecking=yes -oUserKnownHostsFile='+shlex.quote(str(ROOT/hosts))
        preserved_failure('-c','core.sshCommand='+command,'fetch','ssh')
    else: preserved_failure('--ssh-known-hosts', ROOT / hosts, 'fetch', 'ssh')
if FULL:
    port = re.search(r'^ Port (\d+)$',(HOME/'.ssh/config').read_text(),re.M)[1]
    command = shlex.quote(str(NATIVE/'runtime/usr/bin/ssh'))+' -F /dev/null -oHostName=127.0.0.1 -l fixture -p '+port+' -oIdentitiesOnly=yes -oStrictHostKeyChecking=yes -oUserKnownHostsFile='+shlex.quote(str(HOME/'.ssh/known_hosts'))+' -i '+shlex.quote(str(ROOT/'wrong-identity'))
    preserved_failure('-c','core.sshCommand='+command,'fetch','ssh')
else: preserved_failure('--ssh-identity', ROOT / 'wrong-identity', 'fetch', 'ssh')
CHECKS.append('unknown/changed SSH host keys and rejected private identity refuse while preserving repository/profile')

assert snapshot(HOME) == READY['profile_before'], 'migration changed original global settings/credentials/SSH bytes'
assert not list(ROOT.rglob('*.lock'))
final = control('finish')
assert final['https_authenticated_requests'] > 0
assert final['ssh_authenticated_sessions'] > 0 and all(final['ssh_commands'].values())
CHECKS.append('reference fsck passes for the server and all three working repositories; no leftover writer locks')

trace = Path('/tmp/client-exec.trace').read_text()
executables = sorted(set(re.findall(r'execve\("([^"\n]+)"', trace)))
for path in executables:
    if Path(path).name in ('git', 'git-next', 'git-lfs', 'git-next-lfs-filter', 'git-terminal'):
        assert path.startswith(str(NATIVE) + '/'), 'client executed a non-native Git binary'
    if FULL and Path(path).name in ('ssh','git-upload-pack','git-receive-pack','git-http-backend'):
        assert path.startswith(str(NATIVE)+'/'), 'unowned transport entrypoint executed'
    elif not FULL: assert Path(path).name not in ('ssh', 'git-upload-pack', 'git-receive-pack', 'git-http-backend')
assert str(CLIENT) in executables and str(GCM / 'git-credential-manager') in executables
CHECKS.append('client OS excludes installed Git/OpenSSH; execution audit verifies packaged primary core/SSH/GCM paths' if FULL else 'client image physically excludes original Git/OpenSSH; execve audit contains native Git and standalone GCM only')
print(json.dumps({'status': 'pass', 'scope': 'Linux x64 isolated Docker migration/authentication/LF subset; generated accounts only',
                  'binary_sha256': hashlib.sha256(CLIENT.read_bytes()).hexdigest(),
                  'gcm_binary_sha256': hashlib.sha256((GCM / 'git-credential-manager').read_bytes()).hexdigest(),
                  'native_version': native('--version').decode().strip(),
                  'reference_version': READY['reference_version'], 'checks': CHECKS,
                  'https_authenticated_requests': final['https_authenticated_requests'],
                  'ssh_authenticated_sessions': final['ssh_authenticated_sessions'], 'ssh_commands': final['ssh_commands'],
                  'client_executables': executables, 'profile_bytes_preserved': True,
                  'real_account_accessed': False, 'full_git_takeover_accepted': False}, indent=2), flush=True)
