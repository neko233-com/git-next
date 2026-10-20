"""Prepare a complete owned Linux runtime in disposable Docker resources; no product execution."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import uuid

ROOT = Path(__file__).resolve().parents[1]
OWNER = 'git-next-linux-runtime-preparation'


def sha(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--core-artifacts',required=True,type=Path)
    parser.add_argument('--primary-helpers',required=True,type=Path)
    parser.add_argument('--destination',required=True,type=Path,help='new host material input/output directory')
    args = parser.parse_args()
    core = args.core_artifacts.resolve(strict=True)
    build = json.loads((core/'core-build.json').read_text())
    producer = json.loads((core/'docker-compilation.json').read_text())
    target = build['platform']
    if target not in ('linux-x64','linux-arm64') or producer['status'] != 'compiled-not-runtime-accepted' or not all(producer['cleanup'].values()): parser.error('A completed owned Linux build receipt is required')
    archive = core/producer['artifact']['name']
    if sha(archive) != producer['artifact']['sha256'] or archive.stat().st_size != producer['artifact']['bytes']: parser.error('Compiled input archive differs from its receipt')
    helpers = args.primary_helpers.resolve(strict=True)
    helper_manifest = json.loads((helpers/'git-next-posix-helpers.json').read_text())
    if helper_manifest['platform'] != target: parser.error('Helper inputs differ from the native core platform')
    destination = args.destination.resolve()
    if destination.exists() or ',' in str(destination): parser.error('Material directory must be new and have no commas in its path')
    inputs = destination/'inputs'; outputs = destination/'output'
    inputs.mkdir(parents=True); outputs.mkdir(); (inputs/'tools').mkdir(); (inputs/'helpers').mkdir()
    for name in ('collect_linux_runtime.py','posix_images.py','build_native_posix.py','build_native_core.py','extract_build_archive.py','native_product.py'):
        shutil.copyfile(ROOT/'tools'/name,inputs/'tools'/name)
    shutil.copyfile(archive,inputs/'core-build.tar.gz')
    shutil.copyfile(helpers/'git-next-posix-helpers.json',inputs/'helpers/git-next-posix-helpers.json')
    for name,item in helper_manifest['files'].items():
        if item['kind'] != 'file': continue
        source = helpers/name
        if not source.resolve(strict=True).is_relative_to(helpers) or sha(source) != item['sha256']: parser.error('A primary helper input changed')
        output = inputs/'helpers'/name; output.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(source,output)
    script = '''#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive HOME=/tmp/git-next-runtime-home USERPROFILE=/tmp/git-next-runtime-home SSH_ROOT_PATH=/tmp/git-next-runtime-ssh
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
mkdir -p "$HOME" "$SSH_ROOT_PATH"
apt-get update
apt-get install -y --no-install-recommends ca-certificates python3 binutils openssh-client gnupg scdaemon pinentry-curses perl tcl tk coreutils findutils diffutils gawk sed grep patch tar gzip less file libicu72 libsecret-1-0 libssl3 libstdc++6
python3 /inputs/tools/extract_build_archive.py /inputs/core-build.tar.gz /build
python3 /inputs/tools/collect_linux_runtime.py --platform "$GIT_NEXT_RUNTIME_PLATFORM" --destination /build/runtime-assets --core-stage /build/core-stage --tls-prefix /build/tls --primary-helpers /inputs/helpers --base-image "$GIT_NEXT_RUNTIME_BASE"
cp /build/runtime-assets/git-next-posix-runtime.json /output/runtime-assets.json
tar -C /build/runtime-assets -czf /output/runtime-assets.tar.gz .
'''
    (inputs/'prepare.sh').write_text(script,encoding='utf-8',newline='\n')
    scope = uuid.uuid4().hex; volume = 'git-next-runtime-'+scope; container = 'git-next-runtime-prepare-'+scope
    labels = {'git-next.owner':OWNER,'git-next.scope':scope}
    created = False; cleanup = {}; status = 'failed'
    try:
        subprocess.run(['docker','volume','create','--label','git-next.owner='+OWNER,'--label','git-next.scope='+scope,volume],check=True); created = True
        command = ['docker','run','--rm','--pull','never','--platform','linux/'+('amd64' if target == 'linux-x64' else 'arm64'),
                   '--name',container,'--label','git-next.owner='+OWNER,'--label','git-next.scope='+scope,
                   '--mount','type=bind,source='+str(inputs)+',target=/inputs,readonly',
                   '--mount','type=bind,source='+str(outputs)+',target=/output',
                   '--mount','type=volume,source='+volume+',target=/build',
                   '--env','GIT_NEXT_RUNTIME_PLATFORM='+target,'--env','GIT_NEXT_RUNTIME_BASE='+producer['base_image'],
                   producer['base_image'],'/bin/sh','/inputs/prepare.sh']
        subprocess.run(command,check=True)
        status = 'runtime-materials-prepared-not-runtime-accepted'
    finally:
        probe = subprocess.run(['docker','inspect',container],capture_output=True,text=True)
        if probe.returncode == 0:
            value = json.loads(probe.stdout)[0]
            if any(value['Config']['Labels'].get(key) != item for key,item in labels.items()): raise ValueError('Unrelated container prevents cleanup')
            subprocess.run(['docker','rm','-f',container],check=True)
        cleanup['container_absent'] = subprocess.run(['docker','inspect',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
        if created:
            value = json.loads(subprocess.check_output(['docker','volume','inspect',volume],text=True))[0]
            if any(value['Labels'].get(key) != item for key,item in labels.items()): raise ValueError('Unrelated volume prevents cleanup')
            subprocess.run(['docker','volume','rm',volume],check=True)
        cleanup['volume_absent'] = subprocess.run(['docker','volume','inspect',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
        record = dict(schema_version=1,status=status,platform=target,scope=scope,base_image=producer['base_image'],
                      input_core_archive_sha256=sha(archive),helper_manifest_sha256=sha(helpers/'git-next-posix-helpers.json'),
                      copied_tool_inputs={path.name:sha(path) for path in (inputs/'tools').glob('*.py')},cleanup=cleanup,
                      product_executed=False,tests_executed=False,installed=False,release_eligible=False)
        output = outputs/'runtime-assets.tar.gz'
        if output.exists(): record['artifact'] = dict(name=output.name,sha256=sha(output),bytes=output.stat().st_size)
        (outputs/'docker-runtime-materials.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'status':status,'platform':target,'cleanup':cleanup,'product_executed':False}))


if __name__ == '__main__': main()
