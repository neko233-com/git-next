"""Verify and exercise the complete Linux package, with no host profile mounts."""
import hashlib
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import tarfile

from package_input import unpack

root=unpack('/inputs/package.tar.gz',Path('/tmp/package'))
manifest=json.loads((root/'package.json').read_text())
for name,item in manifest['files'].items():
    target=root/name
    assert target.resolve(strict=True).is_relative_to(root)
    if item['kind']=='symlink': assert target.is_symlink() and os.readlink(target)==item['target']
    else:
        assert not target.is_symlink() and target.stat().st_size==item['bytes']
        with target.open('rb') as stream: assert hashlib.file_digest(stream,'sha256').hexdigest()==item['sha256']
        assert target.stat().st_mode&0o777==item['mode']
def check(case):
    name,script=case
    command=['python3','/tests/'+script,str(root/'bin/git'),'--output','/results/'+name+'.json']
    if name in ('native-contracts','native-common-cli'): command+=['--reference','/usr/bin/git']
    result=subprocess.run(command,capture_output=True,timeout=1800)
    Path('/results/'+name+'.log').write_bytes(result.stdout+b'\n'+result.stderr)
    return dict(case=name,status='pass' if result.returncode==0 else 'fail',exit_code=result.returncode)
with ThreadPoolExecutor(max_workers=4) as pool:
    checks=list(pool.map(check,[('native-contracts','native_contract_acceptance.py'),('native-common-cli','native_common_cli_acceptance.py'),('native-integrations','native_integrations_acceptance.py'),('native-read-threads','native_read_threads_acceptance.py')]))
report=dict(schema_version=1,status='pass' if all(item['status']=='pass' for item in checks) else 'fail',
            platform=manifest['platform'],package_manifest_sha256=hashlib.sha256((root/'package.json').read_bytes()).hexdigest(),
            files_verified=len(manifest['files']),cases=checks,personal_profile_mounted=False)
Path('/results/linux-native.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
raise SystemExit(report['status']!='pass')
