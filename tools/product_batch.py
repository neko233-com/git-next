"""One local correctness/install/performance batch; collect failures before repair."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]


def require_implementation_complete(parser):
    path = ROOT / 'docs/DELIVERY_STATUS.json'
    try:
        state = json.loads(path.read_text(encoding='utf-8'))
        items = state['deliverables']
        expected = {'D01', 'D02', 'D03', 'D04', 'D05', 'D06', 'D07'}
        implementation = [item for item in items if item['phase'] == 'implementation']
        if (state['schema_version'] != 1 or not state['scope_frozen']
                or state['policy']['all_implementation_before_regression'] is not True
                or len(implementation) != len(expected)
                or {item['id'] for item in implementation} != expected):
            raise ValueError('Invalid fixed implementation scope')
        pending = [item['id'] for item in implementation
                   if item['status'] != 'complete' or not item['completion_evidence']]
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.error('Cannot establish implementation readiness: ' + str(error))
    if pending:
        parser.error('All features must be implemented before regression. Pending: '
                     + ', '.join(pending) + '. See docs/DELIVERY_STATUS.md.')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tree_identity():
    digest = hashlib.sha256()
    paths = [ROOT / 'Cargo.toml', ROOT / 'Cargo.lock', ROOT / 'compatibility.json', ROOT / 'docs/COMPATIBILITY.md',
             *ROOT.joinpath('src').rglob('*.rs'), *ROOT.joinpath('vendor').rglob('*.rs'), *ROOT.joinpath('vendor').rglob('Cargo.toml')]
    for path in sorted(paths):
        digest.update(path.relative_to(ROOT).as_posix().encode() + b'\0' + path.read_bytes())
    return digest.hexdigest()


def json_write(path, data):
    path.write_text(json.dumps(data, indent=2) + '\n', encoding='utf-8', newline='\n')


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--build-receipt', type=Path, required=True)
    parser.add_argument('--reference', type=Path, required=True)
    parser.add_argument('--gcm', type=Path)
    parser.add_argument('--output', type=Path, required=True, help='new owned result directory; never overwrite a batch')
    parser.add_argument('--workers', type=int, default=4, choices=(1,2,3,4))
    parser.add_argument('--performance', action='store_true')
    parser.add_argument('--msi', type=Path, help='staged Windows MSI for the isolated install/upgrade harness')
    parser.add_argument('--msi-base', type=Path, help='retained older native MSI fixture for real version upgrade/rollback checks')
    parser.add_argument('--docker', action='store_true', help='include prepared Linux runtime and generated-account migration fixtures')
    args = parser.parse_args()
    require_implementation_complete(parser)
    binary = args.binary.resolve(strict=True)
    reference = args.reference.resolve(strict=True)
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    source_before = tree_identity()
    receipt = json.loads(args.build_receipt.read_text(encoding='utf-8-sig'))
    if receipt.get('status') != 'pass' or receipt.get('source_tree_sha256') != source_before:
        parser.error('Build receipt does not identify the current native sources.')
    native_hashes = receipt['binaries']
    for name, expected in native_hashes.items():
        if digest(binary.parent / name) != expected:
            parser.error('Native artifacts differ from the pinned build receipt: ' + name)
    if digest(binary) != native_hashes[binary.name]:
        parser.error('Main binary is absent from the build receipt.')
    reference_hash = digest(reference)
    harnesses = {path.relative_to(ROOT).as_posix():digest(path) for folder in ('tests','packaging','tools')
                 for path in ROOT.joinpath(folder).rglob('*') if path.is_file() and path.suffix in ('.py','.ps1','.psm1','.wxs','.json')}
    environment = dict(os.environ, LC_ALL='C', LANG='C', GIT_TERMINAL_PROMPT='0',
                       PATH=str(reference.parent) + os.pathsep + os.environ.get('PATH',''))
    results = []

    def run(case):
        name, command, timeout = case
        started = time.monotonic()
        log = output / (name + '.log')
        try:
            process = subprocess.run(command, cwd=ROOT, env=environment, capture_output=True, timeout=timeout)
            log.write_bytes(process.stdout + b'\n' + process.stderr)
            entry = dict(case=name, status='pass' if process.returncode == 0 else 'fail', exit_code=process.returncode)
        except subprocess.TimeoutExpired as error:
            log.write_bytes((error.stdout or b'') + b'\n' + (error.stderr or b''))
            entry = dict(case=name, status='fail', reason='aggregate case timeout')
        except OSError as error:
            log.write_text(str(error), encoding='utf-8')
            entry = dict(case=name, status='fail', reason='could not launch local gate')
        entry.update(seconds=round(time.monotonic() - started, 3), timeout_seconds=timeout, log=log.name)
        print(name + ' => ' + entry['status'], flush=True)
        return entry

    # Build/lint gates finish before any process timing begins.
    for case in (('format',['cargo','fmt','--all','--check'],180),
                 ('clippy',['cargo','clippy','--locked','--all-targets','--','-D','warnings'],900),
                 ('rust-tests',['cargo','test','--locked'],900)):
        results.append(run(case))
    ordinary = ('config','status_cache','status_head','status_head_paths','status_ignored','status_long',
                'status_operation','status_directory','add_directory','diff','restore','eol','worktree',
                'commit','repository_format','push','lfs','lfs_transfer','ssh','language')
    cases = [(name,[sys.executable,'tests/' + name + '_acceptance.py',str(binary)],600) for name in ordinary]
    cases += [('status-format',[sys.executable,'tests/status_format_acceptance.py',str(binary),'--include-long'],600),
              ('status-v2',[sys.executable,'tests/status_v2_acceptance.py',str(binary),'--include-long'],900),
              ('wide-status',[sys.executable,'tests/status_wide_acceptance.py',str(binary),'--files','10000','--directories','1000','--runs','12'],900),
              ('merge-sha1',[sys.executable,'tests/merge_acceptance.py',str(binary),'--object-format','sha1'],600),
              ('merge-sha256',[sys.executable,'tests/merge_acceptance.py',str(binary),'--object-format','sha256'],600),
              ('merge-message',[sys.executable,'tests/merge_message_acceptance.py',str(binary)],600),
              ('merge-pending',[sys.executable,'tests/merge_pending_acceptance.py',str(binary)],600),
              ('acceptance-https',[sys.executable,'tests/acceptance.py',str(binary),'--https'],900),
              ('acceptance-system',[sys.executable,'tests/acceptance.py',str(binary),'--https','--system-config-enabled'],900),
              ('terminal',[sys.executable,'tests/terminal_acceptance.py',str(binary.with_name('git-terminal' + binary.suffix))],120)]
    if args.gcm:
        cases.append(('managed-gcm',[sys.executable,'tests/gcm_acceptance.py',str(binary),'--helper',str(args.gcm.resolve(strict=True))],600))
    else:
        results.append(dict(case='managed-gcm',status='missing',reason='Standalone owned helper required'))
    install_cases = []
    if os.name == 'nt':
        shell = str(Path(os.environ['SystemRoot']) / 'System32/WindowsPowerShell/v1.0/powershell.exe')
        cases += [('native-discovery',[sys.executable,'tests/native_discovery_acceptance.py',str(binary)],120),
                  ('metadata-api',[sys.executable,'tests/windows_metadata_api_acceptance.py'],120),
                  ('script-installer',[shell,'-NoProfile','-ExecutionPolicy','Bypass','-File','tests/install_acceptance.ps1'],300),
                  ('updater-contracts',[shell,'-NoProfile','-ExecutionPolicy','Bypass','-File','tests/updater_acceptance.ps1'],300)]
        if args.msi:
            msi_command = [shell,'-NoProfile','-ExecutionPolicy','Bypass','-File','tests/msi_acceptance.ps1','-Package',str((args.msi_base or args.msi).resolve(strict=True))]
            if args.msi_base:
                msi_command += ['-UpgradePackage',str(args.msi.resolve(strict=True))]
            install_cases.append(('msi-install-upgrade-rollback-uninstall', msi_command,900))
        else:
            results.append(dict(case='msi-install-upgrade-rollback-uninstall',status='missing',reason='Staged MSI required'))
    else:
        cases.append(('native-discovery',[sys.executable,'tests/native_discovery_unix_acceptance.py',str(binary)],120))
    # Independent disposable correctness fixtures may run concurrently; collect every failure.
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        results += list(pool.map(run, cases))
    for case in install_cases:
        results.append(run(case))
    if args.docker:
        for case in (('linux-runtime-x64',[sys.executable,'tests/containers/host.py'],2100),
                     ('docker-login-migration',[sys.executable,'tests/containers/migration_host.py','--output',str(output / 'docker-migration.json')],1800),
                     ('linux-runtime-arm64',[sys.executable,'tests/containers/host.py','--arm64','--timeout','3600'],3900)):
            results.append(run(case))
    if args.performance:
        # Performance is one sequential phase; never overlap it with tests/builds.
        if all(item['status'] == 'pass' for item in results):
            checkpoint = output / 'native-identity.json'
            json_write(checkpoint, dict(binary_sha256=digest(binary),source_tree_sha256=source_before))
            for files, directories in ((1000,16),(10000,1000),(100000,1000)):
                command = [sys.executable,'tests/benchmark.py',str(binary),'--reference',str(reference),
                           '--runs','40','--files',str(files),'--directories',str(directories),
                           '--read-commands','--system-config-enabled','--source-checkpoint',str(checkpoint),
                           '--output',str(output / ('performance-' + str(files) + '.json'))]
                results.append(run(('performance-local-' + str(files), command, 1800)))
            results.append(run(('performance-network',[sys.executable,'tests/network_benchmark.py',str(binary),'--reference',str(reference),
                                '--runs','20','--output',str(output / 'performance-network.json')],1800)))
        else:
            results.append(dict(case='performance',status='blocked',reason='Repair collected correctness/install failures before timings'))
    unchanged = source_before == tree_identity() and digest(reference) == reference_hash
    unchanged = unchanged and all(digest(binary.parent / name) == expected for name,expected in native_hashes.items())
    unchanged = unchanged and all((ROOT / path).exists() and digest(ROOT / path) == expected for path,expected in harnesses.items())
    results.append(dict(case='immutable-input-readback',status='pass' if unchanged else 'fail'))
    complete = json.loads((ROOT / 'compatibility.json').read_text())['complete_git_parity'] is True
    required_remaining = ['all-command/IDE/signing/submodule/worktree and recovery coverage',
                          'supported platform build/package/runtime receipts and configured signatures',
                          'actual workstation takeover and independent rollback acceptance']
    report = dict(schema_version=1,utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  status='pass' if all(item['status'] == 'pass' for item in results) else 'fail',
                  source_tree_sha256=source_before,native_binaries=native_hashes,reference_sha256=reference_hash,
                  harnesses=harnesses,results=results,complete_git_parity=complete,
                  release_eligible=False,remaining_release_gates=required_remaining,
                  scope='one consolidated local native-subset correctness/install/performance batch; never authorizes full parity or release by itself')
    json_write(output / 'batch.json',report)
    print(json.dumps({key:report[key] for key in ('status','complete_git_parity','release_eligible','remaining_release_gates')},indent=2))
    raise SystemExit(0 if report['status'] == 'pass' else 1)


if __name__ == '__main__':
    if '--native-packages' in sys.argv:
        from native_product_batch import main as native_main
        raise SystemExit(native_main())
    main()
