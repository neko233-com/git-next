"""Bind unaffected actual acceptance lanes to a terminal-only Windows package update."""
import argparse
import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EXPECTED = {'windows-core-contracts', 'windows-common-cli-contracts', 'windows-owned-integrations',
            'windows-dpapi-migration', 'windows-update-contracts', 'windows-read-thread-contracts',
            'windows-bootstrap-contracts', 'windows-game-lfs-contracts', 'terminal-rust-contracts',
            'terminal-native-desktop', 'linux-x64', 'linux-arm64', 'macos-arm64',
            'docker-login-migration', 'posix-updater-race-contracts', 'msi-user', 'msi-machine', 'frozen-input-readback'}
REFERENCE_CASES = {'windows-core-contracts', 'windows-common-cli-contracts', 'windows-dpapi-migration'}
GUI_BINARIES = {'bin/git-terminal.exe', 'bin/git-terminal-shell.exe'}
TERMINAL_METADATA = 'LICENSES/git-terminal/git-terminal-package.json'
REPORT_ONLY = {'tools/run_native_repair_cases.py', 'tools/complete_native_repair_batch.py',
               'tests/native_repair_evidence_test.py', 'tools/freeze_native_batch.py'}
UI_SOURCES = {'../git-terminal/Cargo.toml', '../git-terminal/build.rs', '../git-terminal/src/assets.rs',
              '../git-terminal/src/main.rs', '../git-terminal/src/presentation.rs',
              '../git-terminal/tests/windows_desktop_acceptance.py', '../git-terminal/tests/clipboard_snapshot.py',
              '../git-terminal/assets/git-terminal.svg', '../git-terminal/assets/git-terminal.png',
              '../git-terminal/assets/git-terminal.ico'}


def sha(path):
    with Path(path).open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def read(path): return json.loads(Path(path).read_text(encoding='utf-8-sig'))


def documentation(name):
    return name in ('README.md', 'README.zh-CN.md') or name.startswith(('docs/', 'LICENSES/git-terminal/manuals/'))


def validate_windows_files(previous, current, compiled):
    changed = {name for name in set(previous)|set(current) if previous.get(name) != current.get(name)}
    if any(name not in GUI_BINARIES | {TERMINAL_METADATA} and not documentation(name) for name in changed):
        raise ValueError('A core/runtime/installer file changed outside the terminal UI')
    if TERMINAL_METADATA in current and current[TERMINAL_METADATA] != compiled.get('git-terminal-package.json'):
        raise ValueError('Terminal provenance metadata differs from the actual compilation receipt')
    for name in GUI_BINARIES:
        if current.get(name) != compiled.get(Path(name).name):
            raise ValueError('Terminal executable does not match its committed compilation receipt')
    return sorted(changed)


def validate_harnesses(previous, current):
    changed = {name for name in set(previous)|set(current) if previous.get(name) != current.get(name)}
    if changed - REPORT_ONLY - UI_SOURCES:
        raise ValueError('An unaffected functional or installer validation source changed')
    return sorted(changed)


def main():
    parser = argparse.ArgumentParser(__doc__)
    for key in ('batch', 'previous-matrix', 'native-packages', 'repair', 'desktop', 'terminal-rust',
                'terminal-builds', 'reference', 'output'):
        parser.add_argument('--'+key, type=Path, required=True)
    args = parser.parse_args()
    original, previous, current, repair = map(read, (args.batch,args.previous_matrix,args.native_packages,args.repair))
    if original['matrix_sha256'] != sha(args.previous_matrix) or repair['matrix_sha256'] != sha(args.previous_matrix):
        parser.error('The actual original and reference repair runs must identify the same matrix')
    if any(previous[key] != current[key] for key in ('product','version','source_tree_sha256')):
        parser.error('Core source/product identity changed; full core regression is required')
    changed_harnesses = validate_harnesses(previous['harnesses'],current['harnesses'])
    if original['phase'] != 'correctness' or len(original['results']) != len(EXPECTED) or {x['case'] for x in original['results']} != EXPECTED:
        parser.error('The complete original 18-case batch is required')
    failures = {x['case'] for x in original['results'] if x['status'] != 'pass'}
    if failures != {'windows-dpapi-migration','terminal-native-desktop','frozen-input-readback'}:
        parser.error('Unexpected failures cannot be hidden by a terminal-only repair')
    if original['source_tree_sha256'] != current['source_tree_sha256'] or repair['source_tree_sha256'] != current['source_tree_sha256']:
        parser.error('Actual acceptance source identity differs')
    if repair['reference_sha256'] != sha(args.reference) or not repair['frozen_input_readback'] or repair['harnesses'] != previous['harnesses']:
        parser.error('The complete-reference rerun inputs differ')
    replaced = {x['case']:x for x in repair['results'] if x['case'] in REFERENCE_CASES}
    if set(replaced) != REFERENCE_CASES or any(x['status'] != 'pass' or x['exit_code'] != 0 for x in replaced.values()):
        parser.error('All three reference-dependent CLI/DPAPI cases must actually pass')
    for item in replaced.values():
        if sha(args.repair.parent/(item['case']+'.log')) != item['log_sha256']:
            parser.error('A reference repair execution log changed')
    gcm_failure=(args.batch.parent/'windows-dpapi-migration.log').read_text(encoding='utf-8')
    if not all(x in gcm_failure for x in ('push_acceptance.py','FileNotFoundError: [WinError 2]','subprocess.run([backend]')):
        parser.error('Original credential failure is outside the verified fixture repair')
    deltas = {}
    for platform in ('windows-x64','windows-arm64'):
        old=previous['platforms'][platform]; new=current['platforms'][platform]
        before=read(Path(old['directory'])/'git-next-distribution.json'); after=read(Path(new['directory'])/'git-next-distribution.json')
        compiled=read(args.terminal_builds/platform/'git-terminal-package.json')
        if after['git_terminal']['source_commit'] != compiled['source_commit']:
            parser.error('The terminal source identity differs from its actual compilation')
        maps=[{name:value['sha256'] for name,value in manifest['files'].items()} for manifest in (before,after)]
        deltas[platform]=validate_windows_files(*maps,{**compiled['files'],'git-terminal-package.json':sha(args.terminal_builds/platform/'git-terminal-package.json')})
        for scope in ('user','machine'):
            key='msi_'+scope; a=read(Path(old[key]).with_suffix('.receipt.json')); b=read(Path(new[key]).with_suffix('.receipt.json'))
            for field in ('product_code','upgrade_code','version','msi_version','install_scope','platform','packaging_files','source_tree_sha256'):
                if a[field] != b[field]: parser.error('The previously exercised MSI contract changed: '+field)
            if sha(new[key]) != b['sha256']: parser.error('Current MSI bytes differ from their local packaging receipt')
    for platform in ('linux-x64','linux-arm64','macos-arm64'):
        if sha(previous['platforms'][platform]['archive']) != sha(current['platforms'][platform]['archive']):
            parser.error('A POSIX package changed and must be rerun')
    desktop=read(args.desktop); rust=read(args.terminal_rust)
    win=Path(current['platforms']['windows-x64']['directory']); binary=win/'bin/git.exe'
    if (desktop['status'] != 'pass' or desktop['scope'] != 'all' or len(desktop['results']) != 15
            or any(x['status'] != 'pass' for x in desktop['results'])
            or desktop['binary_sha256'] != sha(win/'bin/git-terminal.exe') or desktop['git_sha256'] != sha(binary)
            or desktop['harness_sha256'] != sha(ROOT.parent/'git-terminal/tests/windows_desktop_acceptance.py')
            or desktop['clipboard_harness_sha256'] != sha(ROOT.parent/'git-terminal/tests/clipboard_snapshot.py')):
        parser.error('The complete actual 15-case desktop run must identify the current GUI, Git and fixtures')
    if rust['status'] != 'pass' or {x['case'] for x in rust['results']} != {'format','clippy','unit','shell-integration'}:
        parser.error('Current terminal Rust and real shell contracts must pass')
    common=read(args.repair.parent/'windows-common-cli.json')
    if common['status'] != 'pass' or len(common['results']) != 32 or common['binary_sha256'] != sha(binary):
        parser.error('The full 32-case common CLI result identifies different bytes')
    # Rehash both artifact sets: the failed old readback involved only the declared UI/report sources.
    for matrix in (previous,current):
        for name,digest in matrix['files'].items():
            if sha(name) != digest: parser.error('A product artifact changed: '+name)
    for name,digest in current['harnesses'].items():
        if sha(ROOT/name) != digest: parser.error('A frozen current source changed: '+name)
    result=copy.deepcopy(original)
    result.update(status='pass',matrix_sha256=sha(args.native_packages),reference_sha256=repair['reference_sha256'],
                  original_reference_sha256=original['reference_sha256'],original_batch_sha256=sha(args.batch),
                  original_matrix_sha256=sha(args.previous_matrix),repair_receipt_sha256=sha(args.repair),
                  desktop_receipt_sha256=sha(args.desktop),terminal_rust_receipt_sha256=sha(args.terminal_rust),
                  terminal_only_payload_deltas=deltas,validation_source_deltas=changed_harnesses,
                  msi_scope='previous actual complete lifecycle retained for identical installer/worker/configuration contracts; current payload changes restricted to verified terminal executables and manuals; actual final machine installation and takeover remain D10',
                  reference_transition='Windows CLI/DPAPI rerun with complete official Git 2.54; unaffected lanes retain original reference hashes',
                  original_batch_elapsed_seconds=original['elapsed_seconds'],repair_seconds=repair['seconds'],
                  desktop_seconds=desktop['seconds'],unaffected_product_cases_rerun=False)
    replacements={}
    for name,item in replaced.items():
        replacements[name]={key:item[key] for key in ('case','status','exit_code','seconds','log_sha256')}
        replacements[name]['actual_repair_run']=True
    replacements['terminal-native-desktop']=dict(case='terminal-native-desktop',status='pass',exit_code=0,seconds=desktop['seconds'],actual_current_gui_run=True)
    replacements['terminal-rust-contracts']=dict(case='terminal-rust-contracts',status='pass',exit_code=0,seconds=sum(x['seconds'] for x in rust['results']),actual_current_source_run=True)
    replacements['frozen-input-readback']=dict(case='frozen-input-readback',status='pass',current_and_prior_artifact_sets_rehashed=True)
    result['results']=[replacements.get(x['case'],dict(x,retained_actual_lifecycle_or_unchanged_runtime=True)) for x in original['results']]
    args.output.parent.mkdir(parents=True,exist_ok=True)
    with args.output.open('x',encoding='utf-8',newline='\n') as stream:stream.write(json.dumps(result,indent=2)+'\n')
    print(json.dumps(dict(status='pass',items=len(result['results']),desktop_cases=15,common_cli_cases=32)))


if __name__ == '__main__':main()
