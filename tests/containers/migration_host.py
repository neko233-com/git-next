"""Build isolated client/server fixtures, run migration acceptance, then remove every owned Docker resource."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[2]
HARNESS = Path(__file__).resolve().parent
GCM_URL = 'https://github.com/git-ecosystem/git-credential-manager/releases/download/v2.7.3/gcm-linux-x64-2.7.3.tar.gz'
GCM_SHA = 'c7935c3e1b22681d56011b580086aeb6d60f3f0aaf6ef9c581480356988ff804'
parser = argparse.ArgumentParser(__doc__)
parser.add_argument('--output', type=Path, required=True, help='new report path; existing reports are never overwritten')
parser.add_argument('--native-package', type=Path, help='final source-built distribution archive')
args = parser.parse_args()
started = time.monotonic()
assert not args.output.exists(), 'choose a new report filename'
if args.native_package:
    native = args.native_package.resolve(strict=True)
    assembly = json.loads((native.parent/'assembly.json').read_text())
    assert assembly['status']=='assembled-not-runtime-accepted' and all(assembly['cleanup'].values())
    assert hashlib.sha256(native.read_bytes()).hexdigest()==assembly['artifact']['sha256']
    with tarfile.open(native) as package:
        manifest = json.load(package.extractfile('./package.json'))
    assert manifest['platform']=='linux-x64' and manifest['engine']=='source-built-git-next-native-core'
    accepted = dict(source_tree_sha256=manifest['source_tree_sha256'],binaries={'git-next':manifest['files']['bin/git']['sha256']})
else:
    native = ROOT / 'target/x86_64-unknown-linux-musl/release'
    accepted = json.loads((ROOT / '.evidence/cross-x86_64-unknown-linux-musl.json').read_text())
    for name, digest in accepted['binaries'].items():
        assert hashlib.sha256((native / name).read_bytes()).hexdigest() == digest, 'cross package differs from accepted binaries'
archive = ROOT / '.evidence/gcm-linux-x64-2.7.3.tar.gz'
if not archive.exists():
    with urllib.request.urlopen(urllib.request.Request(GCM_URL, headers={'User-Agent': 'git-next-isolated-acceptance'}), timeout=40) as response:
        data = response.read(100 * 1024 * 1024 + 1)
    assert len(data) <= 100 * 1024 * 1024
    assert hashlib.sha256(data).hexdigest() == GCM_SHA
    with archive.open('xb') as output:
        output.write(data)
assert hashlib.sha256(archive.read_bytes()).hexdigest() == GCM_SHA
identity = 'git-next-migration-' + uuid.uuid4().hex[:12]
network, volume, server, client = [identity + suffix for suffix in ('-network', '-volume', '-server', '-client')]
tags = [identity + ':server', identity + ':client']
created_containers, created_networks, created_volumes, created_images = [], [], [], []
report = {'status': 'fail', 'scope': 'Docker Linux x64 fixture accounts; no real account/profile mounts',
          'source_tree_sha256': accepted['source_tree_sha256'], 'binary_sha256': accepted['binaries']['git-next'],
          'gcm_archive_sha256': GCM_SHA, 'gcm_archive_url': GCM_URL,
          'harnesses': {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                        for path in [Path(__file__), HARNESS / 'migration.Dockerfile',
                                     HARNESS / 'migration_server.py', HARNESS / 'migration_client.py',
                                     ROOT / 'tests/push_acceptance.py']},
          'isolation': {'runtime_network_internal': True, 'published_ports': False,
                        'real_profile_mounted': False, 'read_only_rootfs': True,
                        'fixture_data_in_disposable_volume_and_tmpfs': True},
          'images': {}, 'cleanup': []}
report['process_trace'] = 'strace -f execve with seccomp-bpf filtering; tracing assertions unchanged'


def docker(*arguments, timeout=60, check=True):
    result = subprocess.run(['docker', *map(str, arguments)], capture_output=True, timeout=timeout)
    if check and result.returncode:
        # Build errors contain no runtime secrets; runtime failures are intentionally summarized.
        detail = result.stderr.decode(errors='replace')[-3500:] if arguments[0] == 'build' else ''
        raise AssertionError(f'Docker {arguments[0]} failed with exit {result.returncode}\n{detail}')
    return result


def mount(source, target):
    return ['--mount', f'type=bind,source={source.resolve()},target={target},readonly']


try:
    with tempfile.TemporaryDirectory(prefix='git-next-gcm-package-') as temporary:
        package = Path(temporary)
        with tarfile.open(archive) as upstream:
            members = upstream.getmembers()
            assert {member.name for member in members} == {'NOTICE', 'git-credential-manager', 'libHarfBuzzSharp.so', 'libSkiaSharp.so'}
            assert all(member.isfile() and member.size < 100 * 1024 * 1024 for member in members)
            upstream.extractall(package, filter='data')
        base = 'git-next-local-acceptance:20261020'
        base_id = docker('image', 'inspect', base, '--format', '{{.Id}}').stdout.decode().strip()
        report['reference_base_image'] = base_id
        for tag, stage in zip(tags, ('server', 'client')):
            assert docker('image', 'inspect', tag, check=False).returncode != 0
            print(f'Building isolated {stage} image', flush=True)
            # Register the unique tag before building, including failure cleanup.
            created_images.append(tag)
            result = docker('build', '--label', f'git-next.acceptance={identity}', '--target', stage,
                            '--build-arg', f'SERVER_BASE={base}', '-t', tag,
                            '-f', HARNESS / 'migration.Dockerfile', HARNESS, timeout=300)
            report['images'][stage] = docker('image', 'inspect', tag, '--format', '{{.Id}}').stdout.decode().strip()
        assert docker('image', 'inspect', base, '--format', '{{.Id}}').stdout.decode().strip() == base_id
        docker('network', 'create', '--internal', '--label', f'git-next.acceptance={identity}', network)
        created_networks.append(network)
        docker('volume', 'create', '--label', f'git-next.acceptance={identity}', volume)
        created_volumes.append(volume)
        common = ['--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges',
                  '--tmpfs', '/tmp:rw,exec,size=512m', '--mount', f'type=volume,source={volume},target=/migration',
                  *mount(package, '/inputs/gcm'), *mount(HARNESS, '/harness'), *mount(ROOT / 'tests', '/tests'),
                  '--env', 'PYTHONPATH=/tests', '--env', 'PYTHONDONTWRITEBYTECODE=1',
                  '--label', f'git-next.acceptance={identity}']
        created_containers.append(server)
        docker('run', '-d', '--name', server, '--network', network, *common, tags[0],
               'python', '/harness/migration_server.py')
        print('Seeding old Git repository and disposable login', flush=True)
        deadline = time.monotonic() + 75
        while docker('exec', server, 'test', '-f', '/migration/ready.json', check=False).returncode:
            alive = docker('inspect', server, '--format', '{{.State.Running}}').stdout.strip()
            assert alive == b'true', 'reference fixture stopped before readiness'
            assert time.monotonic() < deadline, 'reference fixture readiness deadline exceeded'
            time.sleep(0.2)
        created_containers.append(client)
        print('Running native client with no original Git/OpenSSH installed', flush=True)
        result = docker('run', '--name', client, '--network', f'container:{server}', *common,
                        *mount(native, '/inputs/native-package.tar.gz' if args.native_package else '/inputs/native'),
                        '--env','GIT_NEXT_NATIVE_PACKAGE='+('1' if args.native_package else '0'), tags[1],
                        'strace', '--seccomp-bpf', '-f', '-qq', '-e', 'trace=execve', '-o', '/tmp/client-exec.trace',
                        '/usr/local/bin/python', '/harness/migration_client.py', timeout=420, check=False)
        if result.returncode:
            # The fixtures never print credential values; preserve only the assertion/traceback here.
            print(result.stderr.decode(errors='replace')[-2200:], flush=True)
            raise AssertionError(f'native client acceptance failed, exit={result.returncode}')
        report['acceptance'] = json.loads(result.stdout)
        docker('wait', server, timeout=30)
        assert docker('inspect', server, '--format', '{{.State.ExitCode}}').stdout.strip() == b'0'
        report['status'] = 'pass'
except Exception as error:
    report['failure'] = str(error)
    raise
finally:
    # Remove only names created in this run, never prune shared Docker state.
    resources = ([('container', name, ['rm', '-f', name]) for name in reversed(created_containers)]
                 + [('volume', name, ['volume', 'rm', name]) for name in reversed(created_volumes)]
                 + [('network', name, ['network', 'rm', name]) for name in reversed(created_networks)]
                 + [('image', tag, ['image', 'rm', tag]) for tag in reversed(created_images)])
    for kind, name, command in resources:
        result = docker(*command, check=False)
        absent = docker(kind, 'inspect', name, check=False).returncode != 0
        report['cleanup'].append({'kind': kind, 'name': name, 'absent_after_cleanup': absent})
    report['all_owned_resources_removed'] = all(entry['absent_after_cleanup'] for entry in report['cleanup'])
    report['elapsed_seconds'] = round(time.monotonic() - started, 3)
    if not report['all_owned_resources_removed']:
        report['status'] = 'fail'
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open('x', encoding='utf-8', newline='\n') as output:
        output.write(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'status': report['status'], 'report': str(args.output),
                      'all_owned_resources_removed': report['all_owned_resources_removed']}), flush=True)
if report['status'] != 'pass':
    raise SystemExit(1)
