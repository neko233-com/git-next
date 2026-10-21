"""Refresh only the standalone bootstrap asset; accepted MSI/runtime bytes stay fixed."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--stage', type=Path, required=True)
    parser.add_argument('--acceptance', type=Path, required=True)
    parser.add_argument('--git', type=Path, required=True)
    args = parser.parse_args()
    stage = args.stage.resolve(strict=True)
    proof_path = stage/'native-release-acceptance.json'
    proof = json.loads(proof_path.read_text())
    checks = json.loads(args.acceptance.read_text(encoding='utf-8-sig'))
    if checks['status'] != 'pass' or not any(item['case'] == 'real-gui-terminal-version-is-awaited-and-captured' and item['status'] == 'pass' for item in checks['results']):
        parser.error('Actual GUI-subsystem terminal acceptance is required')
    git = str(args.git.resolve(strict=True))
    commit = subprocess.check_output([git,'rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    changed = subprocess.check_output([git,'diff','--name-only',proof['source_commit']+'..'+commit],cwd=ROOT,text=True).splitlines()
    allowed = {'install.ps1','tests/bootstrap_acceptance.ps1','tools/refresh_release_bootstrap.py',
               'docs/INSTALLATION.md','docs/site/installation.html'}
    if set(changed)-allowed or not {'install.ps1','tests/bootstrap_acceptance.ps1'} <= set(changed):
        parser.error('A runtime/payload change cannot reuse the accepted package bytes')
    sums = stage/'SHA256SUMS'
    names = []
    for line in sums.read_text().splitlines():
        digest,name = line.split('  ',1)
        if Path(name).name != name or sha(stage/name) != digest:
            parser.error('A staged asset changed before the bounded bootstrap refresh')
        names.append(name)
    shutil.copyfile(ROOT/'install.ps1',stage/'install.ps1')
    proof['standalone_bootstrap'] = dict(source_commit=commit,sha256=sha(stage/'install.ps1'),
        acceptance_sha256=sha(args.acceptance),included_in_msi_payload=False,
        scope='Explicit GUI process wait/capture and preflight before original removal; all previously accepted MSI and archive bytes unchanged')
    proof_path.write_text(json.dumps(proof,indent=2,sort_keys=True)+'\n',encoding='utf-8',newline='\n')
    sums.write_text(''.join(sha(stage/name)+'  '+name+'\n' for name in names),encoding='utf-8',newline='\n')
    print(json.dumps(dict(status='standalone-bootstrap-refreshed',runtime_source_commit=proof['source_commit'],bootstrap_source_commit=commit,unchanged_runtime_assets=len(names)-2)))


if __name__ == '__main__':
    main()
