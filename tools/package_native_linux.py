"""Assemble a source-matched Linux distribution in owned disposable Docker storage."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import uuid

from build_native_core import source_identity

ROOT = Path(__file__).resolve().parents[1]
OWNER = 'git-next-linux-package-assembly'


def sha(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--source',required=True,type=Path)
    parser.add_argument('--core-artifacts',required=True,type=Path)
    parser.add_argument('--runtime-artifacts',required=True,type=Path)
    parser.add_argument('--shared-assets',required=True,type=Path)
    parser.add_argument('--updater',required=True,type=Path)
    parser.add_argument('--destination',required=True,type=Path)
    args = parser.parse_args()
    source = args.source.resolve(strict=True)
    core = args.core_artifacts.resolve(strict=True)
    runtime = args.runtime_artifacts.resolve(strict=True)
    producer = json.loads((core/'docker-compilation.json').read_text())
    materials = json.loads((runtime/'docker-runtime-materials.json').read_text())
    build = json.loads((core/'core-build.json').read_text())
    if (producer['status'] != 'compiled-not-runtime-accepted'
            or not all(producer['cleanup'].values()) or not all(materials['cleanup'].values())
            or build['source_tree_sha256'] != source_identity(source)
            or materials['platform'] != build['platform']): parser.error('Current source-matched core and cleaned runtime producer are required')
    for base,receipt in ((core,producer),(runtime,materials)):
        path = base/receipt['artifact']['name']
        if sha(path) != receipt['artifact']['sha256'] or path.stat().st_size != receipt['artifact']['bytes']:
            parser.error('Producer archive bytes changed')
    destination = args.destination.resolve()
    if destination.exists() or ',' in str(destination): parser.error('Use a new unambiguous task directory')
    inputs = destination/'inputs'; outputs = destination/'output'
    project = inputs/'project'; project.mkdir(parents=True); outputs.mkdir()
    shutil.copytree(source,project/'source')
    for folder in ('tools','packaging','docs','native-core'):
        shutil.copytree(ROOT/folder,project/folder,ignore=shutil.ignore_patterns('__pycache__'))
    for name in ('compatibility.json','README.md','README.zh-CN.md','AGENTS.md'):
        shutil.copyfile(ROOT/name,project/name)
    shutil.copytree(args.shared_assets,project/'shared')
    shutil.copyfile(args.updater,project/'updater')
    shutil.copyfile(core/'core-build.json',inputs/'core-build.json')
    shutil.copyfile(core/producer['artifact']['name'],inputs/'core.tar.gz')
    shutil.copyfile(runtime/materials['artifact']['name'],inputs/'runtime.tar.gz')
    script = '''set -eu
export DEBIAN_FRONTEND=noninteractive HOME=/tmp/git-next-package-home USERPROFILE=/tmp/git-next-package-home SSH_ROOT_PATH=/tmp/git-next-package-ssh
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
mkdir -p "$HOME" "$SSH_ROOT_PATH"
apt-get update
apt-get install -y --no-install-recommends python3 binutils patchelf
python3 /inputs/project/tools/extract_build_archive.py /inputs/core.tar.gz /build
mkdir /build/runtime
python3 /inputs/project/tools/extract_runtime_archive.py /inputs/runtime.tar.gz /build/runtime
python3 /inputs/project/tools/assemble_native_posix.py --source /inputs/project/source --core-stage /build/core-stage --core-receipt /inputs/core-build.json --runtime-assets /build/runtime --shared-assets /inputs/project/shared --updater /inputs/project/updater --destination /build/distribution
cp /build/distribution/package.json /output/package.json
cp /build/distribution/git-next-distribution.json /output/git-next-distribution.json
tar -C /build/distribution -czf /output/distribution.tar.gz .
'''
    (inputs/'assemble.sh').write_text(script,newline='\n')
    scope = uuid.uuid4().hex
    volume = 'git-next-package-'+scope; container = 'git-next-package-assemble-'+scope
    label = ['--label','git-next.owner='+OWNER,'--label','git-next.scope='+scope]
    cleanup = {}; status = 'failed'; created = False
    try:
        subprocess.run(['docker','volume','create',*label,volume],check=True); created = True
        subprocess.run(['docker','run','--rm','--pull','never','--name',container,*label,
                        '--platform','linux/'+('amd64' if build['platform']=='linux-x64' else 'arm64'),
                        '--mount','type=bind,source='+str(inputs)+',target=/inputs,readonly',
                        '--mount','type=bind,source='+str(outputs)+',target=/output',
                        '--mount','type=volume,source='+volume+',target=/build',
                        producer['base_image'],'/bin/sh','/inputs/assemble.sh'],check=True)
        status = 'assembled-not-runtime-accepted'
    finally:
        for kind,name,remove in [('container',container,['rm','-f',container]),('volume',volume,['volume','rm',volume])]:
            probe = subprocess.run(['docker',kind,'inspect',name],capture_output=True,text=True)
            if probe.returncode == 0:
                item = json.loads(probe.stdout)[0]
                labels = item['Config']['Labels'] if kind=='container' else item['Labels']
                if labels.get('git-next.owner') != OWNER or labels.get('git-next.scope') != scope:
                    raise ValueError('Refusing cleanup of an unrelated resource')
                subprocess.run(['docker',*remove],check=True)
            cleanup[kind+'_absent'] = subprocess.run(['docker',kind,'inspect',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
        receipt = dict(schema_version=1,status=status,platform=build['platform'],source_tree_sha256=build['source_tree_sha256'],
                       inputs=dict(core=sha(inputs/'core.tar.gz'),runtime=sha(inputs/'runtime.tar.gz'),updater=sha(project/'updater')),
                       cleanup=cleanup,product_executed=False,tests_executed=False,installed=False,release_eligible=False)
        archive = outputs/'distribution.tar.gz'
        if archive.exists(): receipt['artifact'] = dict(name=archive.name,bytes=archive.stat().st_size,sha256=sha(archive))
        (outputs/'assembly.json').write_text(json.dumps(receipt,indent=2)+'\n',newline='\n')
    print(json.dumps(dict(status=status,platform=build['platform'],cleanup=cleanup)))


if __name__ == '__main__': main()
