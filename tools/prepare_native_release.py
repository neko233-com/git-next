"""Stage one native release after immutable local correctness and timing gates."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import copy
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tarfile

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def encoded(value):
    return (json.dumps(value, indent=2, sort_keys=True) + '\n').encode()


def documents():
    names = {'README.md','README.zh-CN.md','AGENTS.md','docs/DELIVERY_STATUS.json'}
    names.update(path.relative_to(ROOT).as_posix() for path in (ROOT/'docs').rglob('*')
                 if path.is_file() and (path.suffix in ('.md','.html','.gitignore','.gitattributes') or (path.suffix == '.json' and path.name.startswith('native-'))))
    result = {}
    for name in names:
        path = ROOT/name
        if path.is_symlink() or not path.resolve(strict=True).is_relative_to(ROOT): raise ValueError('Unsafe release documentation path')
        result[name] = path.read_bytes()
    return result


def main():
    parser = argparse.ArgumentParser(__doc__)
    for name in ('native-packages', 'correctness', 'performance', 'destination', 'wix', 'git'):
        parser.add_argument('--' + name, type=Path, required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--windows-runtime-acl-fix', type=Path,
                        help='Passed updater acceptance for the exact inherited-ACE conversion repair')
    parser.add_argument('--windows-tls-rename-fix', action='store_true',
                        help='Apply only the child-handle release fix to accepted Windows payloads; actual machine TLS acceptance is required before publication')
    args = parser.parse_args()
    matrix = json.loads(args.native_packages.read_text())
    identity = sha(args.native_packages)
    gates = [json.loads(path.read_text()) for path in (args.correctness, args.performance)]
    if any(item['status'] != 'pass' or item['matrix_sha256'] != identity for item in gates):
        parser.error('Passed correctness and performance receipts must identify this exact matrix')
    if gates[0]['phase'] != 'correctness' or gates[1]['phase'] != 'performance':
        parser.error('Both local acceptance phases are required')
    expected = {'windows-core-contracts', 'windows-owned-integrations', 'windows-dpapi-migration',
                'windows-update-contracts', 'linux-x64', 'linux-arm64', 'macos-arm64',
                'docker-login-migration', 'posix-updater-race-contracts', 'msi-user', 'msi-machine', 'frozen-input-readback',
                'windows-read-thread-contracts','windows-bootstrap-contracts','windows-game-lfs-contracts','terminal-rust-contracts','terminal-native-desktop','windows-common-cli-contracts'}
    if {item['case'] for item in gates[0]['results']} != expected or any(item['status'] != 'pass' for gate in gates for item in gate['results']):
        parser.error('Required local acceptance cases are missing or failed')
    if {item['case'] for item in gates[1]['results']} != {'performance-1000','performance-10000','performance-100000','performance-network','frozen-input-readback'}:
        parser.error('The complete paired performance batch is required')
    if gates[0]['reference_sha256'] != gates[1]['reference_sha256']:
        parser.error('Correctness and timings must use the same reference')
    if not re.fullmatch('[a-f0-9]{40}', args.source_commit): parser.error('A full project source commit is required')
    actual = subprocess.check_output([str(args.git.resolve()), 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    if actual != args.source_commit: parser.error('Project HEAD differs from the release source commit')
    for name, digest in matrix['files'].items():
        if sha(name) != digest: parser.error('Accepted package input changed: ' + name)
    tls_script = 'packaging/windows/install-tls-provider.ps1'
    if args.windows_tls_rename_fix:
        added = (b'    # Windows cannot rename a directory while its child file handles remain open.\n'
                 b'    # The protected ACL and exclusive platform lock still guard publication.\n'
                 b'    foreach ($handle in $held) { $handle.Dispose() }\n    $held.Clear()\n')
        current_tls = (ROOT/tls_script).read_bytes().replace(b'\r\n', b'\n')
        if current_tls.count(added) != 1 or hashlib.sha256(current_tls.replace(added,b'')).hexdigest() != matrix['harnesses'][tls_script]:
            parser.error('Only the exact accepted TLS child-handle fix may reuse unaffected gates')
    for name, digest in matrix['harnesses'].items():
        if args.windows_tls_rename_fix and name in (tls_script, 'tools/prepare_native_release.py'): continue
        if args.windows_runtime_acl_fix and name in ('packaging/windows/Runtime.Configuration.ps1','tests/updater_acceptance.ps1'): continue
        if sha(ROOT/name) != digest: parser.error('Accepted source/harness input changed: ' + name)
    acl_script = 'packaging/windows/Runtime.Configuration.ps1'
    if args.windows_runtime_acl_fix:
        replacement = (b'        $inherited = @($security.GetAccessRules($false,$true,[Security.Principal.SecurityIdentifier]))\n'
                       b'        $security.SetAccessRuleProtection($true,$false)\n'
                       b'        foreach ($rule in $inherited) {\n'
                       b'            $security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($rule.IdentityReference,$rule.FileSystemRights,$rule.AccessControlType))\n'
                       b'        }\n')
        current_acl = (ROOT/acl_script).read_bytes().replace(b'\r\n',b'\n')
        previous_acl = current_acl.replace(replacement,b'        $security.SetAccessRuleProtection($true,$true)\n')
        if not args.windows_tls_rename_fix or current_acl.count(replacement) != 1 or hashlib.sha256(previous_acl).hexdigest() != matrix['harnesses'][acl_script]:
            parser.error('Only the exact inherited-ACE repair may reuse unaffected gates')
        updater_gate = json.loads(args.windows_runtime_acl_fix.read_text(encoding='utf-8-sig'))
        if updater_gate['status'] != 'pass' or not {'inherited user full control becomes read-only machine configuration','machine configuration ACL is protected after inherited ACE conversion'} <= set(updater_gate['checks']):
            parser.error('Actual inherited-ACE updater regression must pass')
    output = args.destination.resolve()
    if output.exists(): parser.error('Release staging directory must be new')
    output.mkdir(parents=True)
    proof = dict(schema_version=1, product='git-next', source_commit=args.source_commit,
                 source_tree_sha256=matrix['source_tree_sha256'], matrix_sha256=identity,
                 correctness_sha256=sha(args.correctness), performance_sha256=sha(args.performance),
                 native_mac_product_executed=False, personal_account_accessed=False,
                 scope='accepted core/runtime bytes; release metadata, documentation and archive root finalized after local gates')
    if args.windows_tls_rename_fix:
        proof['windows_tls_rename_fix'] = dict(before_sha256=matrix['harnesses'][tls_script],
            after_sha256=sha(ROOT/tls_script), payload_path='components/install-tls-provider.ps1',
            scope='Only release verified child handles before directory rename; all core, runtime, updater, GUI and MSI recipes remain accepted unchanged',
            required_before_publication='Fresh real machine TLS preparation, idempotence, pinned-file/ACL verification and actual workstation takeover')
    if args.windows_runtime_acl_fix:
        proof['windows_runtime_acl_fix'] = dict(before_sha256=matrix['harnesses'][acl_script],after_sha256=sha(ROOT/acl_script),
            payload_path='updater/Runtime.Configuration.ps1',updater_acceptance_sha256=sha(args.windows_runtime_acl_fix),
            scope='Convert inherited ACEs into explicit rules before removing non-administrator writes; preserve read/deny restrictions',
            required_before_publication='Actual configuration migration and workstation takeover using this installed script')
    (output/'native-release-acceptance.json').write_bytes(encoded(proof))
    version = matrix['version']
    manuals = documents()
    platforms = {}
    windows = []
    for platform, item in matrix['platforms'].items():
        if platform.startswith('windows-'):
            source = Path(item['directory'])
            target = output/'distributions'/platform
            shutil.copytree(source, target)
            path = target/'git-next-distribution.json'
            manifest = json.loads(path.read_text())
            if not manifest['complete_git_parity'] or manifest['source_tree_sha256'] != matrix['source_tree_sha256']:
                parser.error('Native package must embed the accepted complete identity')
            manifest.update(status='accepted-release-distribution', source_commit=args.source_commit,
                            release_eligible=True, remaining=[], acceptance=proof)
            if args.windows_tls_rename_fix:
                name = 'components/install-tls-provider.ps1'
                if sha(target/name) != proof['windows_tls_rename_fix']['before_sha256']:
                    parser.error('Accepted Windows TLS installer differs from the exact repaired baseline')
                shutil.copyfile(ROOT/tls_script, target/name)
                manifest['files'][name].update(sha256=sha(target/name), bytes=(target/name).stat().st_size)
            if args.windows_runtime_acl_fix:
                name = 'updater/Runtime.Configuration.ps1'
                if sha(target/name) != proof['windows_runtime_acl_fix']['before_sha256']:
                    parser.error('Accepted runtime configuration helper differs from the exact repaired baseline')
                shutil.copyfile(ROOT/acl_script, target/name)
                manifest['files'][name].update(sha256=sha(target/name), bytes=(target/name).stat().st_size)
            for name, data in manuals.items():
                document = target/name
                document.parent.mkdir(parents=True, exist_ok=True)
                document.write_bytes(data)
                record = manifest['files'].setdefault(name, dict(kind='release-documentation'))
                record.update(sha256=hashlib.sha256(data).hexdigest(), bytes=len(data))
            path.write_bytes(encoded(manifest))
            windows.append((platform, target))
            continue
        archive = Path(item['archive'])
        prefix = 'git-next-' + version + '-' + platform
        destination = output/(prefix + '.tar.gz')
        with tarfile.open(archive) as original:
            manifest = json.load(original.extractfile('./package.json'))
            report = json.load(original.extractfile('./git-next-distribution.json'))
            if not manifest['complete_git_parity'] or manifest['source_tree_sha256'] != matrix['source_tree_sha256']:
                parser.error('Native package must embed the accepted complete identity')
            manifest.update(source_commit=args.source_commit, release_eligible=True, acceptance=proof)
            for name, data in manuals.items():
                record = dict(kind='file', sha256=hashlib.sha256(data).hexdigest(), bytes=len(data), mode=0o644,
                              component='release-documentation')
                manifest['files'][name] = record
                report['files'][name] = record.copy()
            replacements = dict(manuals)
            replacements['package.json'] = encoded(manifest)
            report['files']['package.json'].update(sha256=hashlib.sha256(replacements['package.json']).hexdigest(), bytes=len(replacements['package.json']))
            report.update(status='accepted-release-distribution', source_commit=args.source_commit, release_eligible=True, acceptance=proof)
            replacements['git-next-distribution.json'] = encoded(report)
            seen = set()
            with tarfile.open(destination, 'w:gz', compresslevel=6) as staged:
                for member in original:
                    relative = PurePosixPath(member.name)
                    if relative.is_absolute() or '..' in relative.parts:
                        raise ValueError('Unsafe accepted archive member')
                    name = relative.as_posix()
                    if name in seen: raise ValueError('Duplicate accepted archive member')
                    seen.add(name)
                    value = copy.copy(member)
                    # PAX long-name records otherwise override the canonical archive root.
                    value.pax_headers = {key: data for key, data in member.pax_headers.items() if key not in ('path', 'linkpath')}
                    value.name = prefix if name == '.' else prefix + '/' + name
                    value.uid = value.gid = 0
                    value.uname = value.gname = ''
                    if value.islnk():
                        link = PurePosixPath(value.linkname)
                        if link.is_absolute() or '..' in link.parts: raise ValueError('Unsafe archive hard link')
                        value.linkname = prefix + '/' + link.as_posix()
                    if name in replacements:
                        data = replacements[name]
                        value.size = len(data)
                        value.mode = 0o644
                        staged.addfile(value, io.BytesIO(data))
                    else:
                        staged.addfile(value, original.extractfile(member) if member.isfile() else None)
                for name in sorted(replacements.keys()-seen):
                    data = replacements[name]
                    value = tarfile.TarInfo(prefix+'/'+name)
                    value.size = len(data)
                    value.mode = 0o644
                    staged.addfile(value, io.BytesIO(data))
        platforms[platform] = dict(asset=destination.name, sha256=sha(destination), size=destination.stat().st_size, install_kind='tar')
    def compile_msi(job):
        platform, distribution, scope = job
        subprocess.run([sys.executable, str(ROOT/'packaging/native_msi.py'), '--distribution', str(distribution),
                        '--wix', str(args.wix.resolve()), '--output-dir', str(output), '--scope', scope,
                        '--source-commit', args.source_commit], cwd=ROOT, check=True,
                       stdout=(output/(platform+'-'+scope+'-packaging.log')).open('wb'))
        suffix = '-user' if scope == 'user' else ''
        path = output/('git-next-'+version+'-'+platform+suffix+'.receipt.json')
        receipt = json.loads(path.read_text())
        package = output/receipt['asset']
        if sha(package) != receipt['sha256'] or not receipt['release_eligible']: raise ValueError('MSI packaging gate failed')
        return platform, scope, {key:receipt[key] for key in ('asset','sha256','install_scope','msi_version','product_code','upgrade_code')} | dict(size=package.stat().st_size, install_kind='msi')
    with ThreadPoolExecutor(4) as pool:
        for platform, scope, entry in pool.map(compile_msi, [(platform, path, scope) for platform, path in windows for scope in ('machine','user')]):
            if scope == 'machine': platforms.setdefault(platform, {}).update(entry)
            else: platforms.setdefault(platform, {})['variants'] = {'user':entry}
    manifest = dict(schema_version=1, product='git-next', version=version, source_commit=args.source_commit,
                    complete_git_parity=True, platforms=platforms)
    (output/'git-next-update.json').write_bytes(encoded(manifest))
    for source, name in ((args.correctness,'native-correctness.json'), (args.performance,'native-performance.json')):
        shutil.copyfile(source, output/name)
    for name in ('performance-1000.json','performance-10000.json','performance-100000.json','performance-network.json'):
        shutil.copyfile(args.performance.parent/name, output/name)
    bundles = [Path(name) for name in matrix['files'] if Path(name).name.startswith('git-next-corresponding-sources-') and Path(name).suffix == '.zip']
    if len(bundles) != 4: parser.error('Four accepted corresponding-source bundles are required')
    for path in bundles: shutil.copyfile(path, output/path.name)
    shutil.copyfile(ROOT/'install.ps1', output/'install.ps1')
    assets = sorted([path for path in output.iterdir() if path.suffix in ('.msi','.gz','.zip','.json','.ps1') and not path.name.endswith('.receipt.json')])
    (output/'SHA256SUMS').write_text(''.join(sha(path)+'  '+path.name+'\n' for path in assets), encoding='utf-8', newline='\n')
    print(json.dumps(dict(status='release-staged-not-published', source_commit=args.source_commit, platforms=len(platforms), assets=len(assets))))


if __name__ == '__main__': main()
