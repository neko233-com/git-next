"""Finalize unchanged batch evidence after an external desktop focus interruption."""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import time

from native_msi_evidence import load_reuse

ROOT=Path(__file__).resolve().parents[1]
EXPECTED={'windows-core-contracts','windows-owned-integrations','windows-dpapi-migration',
          'windows-update-contracts','windows-read-thread-contracts','windows-bootstrap-contracts',
          'windows-game-lfs-contracts','terminal-rust-contracts','terminal-native-desktop',
          'linux-x64','linux-arm64','macos-arm64','docker-login-migration',
          'posix-updater-race-contracts','msi-user','msi-machine','frozen-input-readback'}
REPORT_ONLY={'tools/native_msi_evidence.py','tests/native_msi_evidence_test.py','tools/complete_native_batch.py'}


def sha(path):
    with Path(path).open('rb') as stream:return hashlib.file_digest(stream,'sha256').hexdigest()


def validate_rebinding(previous,current,report):
    for key in ('product','version','source_tree_sha256','files','platforms'):
        if previous[key]!=current[key]:raise ValueError('Accepted product inputs changed: '+key)
    if report.get('source_tree_sha256')!=current['source_tree_sha256']:
        raise ValueError('Original report identifies different core sources')
    changed={name for name in set(previous['harnesses'])|set(current['harnesses'])
             if previous['harnesses'].get(name)!=current['harnesses'].get(name)}
    if changed-REPORT_ONLY:raise ValueError('A functional validation source changed')
    results=report['results']
    if report['phase']!='correctness' or len(results)!=len(EXPECTED) or {r['case'] for r in results}!=EXPECTED:
        raise ValueError('The original complete correctness batch is required')
    if {r['case'] for r in results if r['status']!='pass'}!={'terminal-native-desktop'}:
        raise ValueError('Only an interrupted desktop case may be finalized')
    for r in results:
        if r['case'] not in ('terminal-native-desktop','frozen-input-readback') and r.get('exit_code')!=0:
            raise ValueError('A functional case has no successful process result')


def main():
    parser=argparse.ArgumentParser(__doc__)
    for key in ('batch','previous-matrix','native-packages','desktop-batch','desktop-matrix','reference','output'):
        parser.add_argument('--'+key,type=Path,required=True)
    args=parser.parse_args();started=time.monotonic()
    original=json.loads(args.batch.read_text());previous=json.loads(args.previous_matrix.read_text())
    current=json.loads(args.native_packages.read_text())
    if original['matrix_sha256']!=sha(args.previous_matrix) or original['reference_sha256']!=sha(args.reference):
        parser.error('Original batch identity does not match')
    validate_rebinding(previous,current,original)
    focus_log=args.batch.parent/'terminal-native-desktop.log'
    failure=focus_log.read_text(encoding='utf-8')
    if not all(text in failure for text in ('u.GetForegroundWindow()!=hwnd:activate_owned(hwnd)',
                                          'in activate_owned','Owned terminal action did not complete in time')):
        parser.error('The failed desktop case was not the known foreground acquisition interruption')
    desktop=load_reuse(args.desktop_batch,args.desktop_matrix,current,original['reference_sha256'],('terminal-native-desktop',))
    if not desktop:parser.error('No dependency-matched passed desktop receipt')
    for name,digest in current['files'].items():
        if sha(name)!=digest:parser.error('Frozen product bytes changed: '+name)
    for name,digest in current['harnesses'].items():
        if sha(ROOT/name)!=digest:parser.error('Frozen validation bytes changed: '+name)
    result=copy.deepcopy(original)
    result.update(status='pass',matrix_sha256=sha(args.native_packages),release_eligible=False,
                  original_batch_sha256=sha(args.batch),original_matrix_sha256=sha(args.previous_matrix),
                  external_desktop_focus_failure_log_sha256=sha(focus_log),
                  desktop_reusing_unchanged_passed_evidence=True,
                  desktop_evidence_scope='identical GUI/core/Bash/runtime bytes and desktop tests; exact stable-read config transition only; installer/docs excluded',
                  product_cases_rerun_during_receipt_finalization=False,
                  receipt_finalization_seconds=round(time.monotonic()-started,3))
    result['results']=[dict(**desktop['terminal-native-desktop'],started_seconds_from_batch=r.get('started_seconds_from_batch',0),
                            finished_seconds_from_batch=r.get('started_seconds_from_batch',0)) if r['case']=='terminal-native-desktop' else r
                       for r in result['results']]
    args.output.parent.mkdir(parents=True,exist_ok=True)
    with args.output.open('x',encoding='utf-8',newline='\n') as stream:stream.write(json.dumps(result,indent=2)+'\n')
    print(json.dumps(dict(status='pass',items=len(result['results']),product_cases_rerun=False,
                          original_batch_elapsed_seconds=original['elapsed_seconds'],receipt_finalization_seconds=result['receipt_finalization_seconds'])))


if __name__=='__main__':main()
