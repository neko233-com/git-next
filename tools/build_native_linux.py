"""Compile native Linux cores in disposable Docker resources; no regression or product execution."""
import argparse
import ast
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import uuid
import zipfile

from build_native_core import source_identity

ROOT = Path(__file__).resolve().parents[1]
OWNER = 'git-next-native-core-build'


def linux_recipe_identity(path):
    tree = ast.parse(path.read_text())
    if path.name == 'build_native_core.py':
        tree.body = [item for item in tree.body if isinstance(item, ast.FunctionDef) and item.name in ('digest','source_identity')]
    else:
        class LinuxOnly(ast.NodeTransformer):
            def visit_If(self, node):
                if ast.unparse(node.test) == "args.platform.startswith('macos-')": return None
                return self.generic_visit(node)
        tree = LinuxOnly().visit(tree)
    return ast.dump(tree, include_attributes=False)


def run(*command, **kwargs):
    return subprocess.run(command,check=True,**kwargs)


def metadata(*command):
    return json.loads(subprocess.check_output(command,text=True))


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',choices=['linux-x64','linux-arm64'],required=True)
    parser.add_argument('--source',type=Path,required=True)
    parser.add_argument('--core-archive',type=Path,required=True,help='pinned original archive used only for source permission metadata')
    parser.add_argument('--dependency-cache',type=Path,required=True)
    parser.add_argument('--base-image',required=True,help='already available official Debian bookworm image pinned with @sha256')
    parser.add_argument('--destination',type=Path,required=True,help='new isolated host build-input/output directory')
    parser.add_argument('--jobs',type=int,default=4,choices=range(1,17))
    parser.add_argument('--reuse-dependencies',type=Path,help='owned source-built library cache; never a cached core')
    parser.add_argument('--dependency-producer',type=Path,help='matching cleaned-up Docker compilation receipt')
    parser.add_argument('--reuse-core-build',type=Path,help='owned compiler object cache for identity/resource metadata changes')
    parser.add_argument('--core-producer',type=Path,help='matching compiler receipt with its frozen inputs/source sibling')
    args = parser.parse_args()
    if not args.base_image.startswith('debian@sha256:') or len(args.base_image.split(':')[-1]) != 64: parser.error('Use an official immutable Debian image reference')
    image = metadata('docker','image','inspect',args.base_image)[0]
    arch = 'amd64' if args.platform == 'linux-x64' else 'arm64'
    if image['Os'] != 'linux' or image['Architecture'] != arch: parser.error('The available base image does not match the requested build platform')
    reused = None
    core_cache = None
    if bool(args.reuse_core_build) != bool(args.core_producer): parser.error('Core cache requires its producer receipt')
    if args.reuse_core_build:
        if args.reuse_dependencies: parser.error('Select one cache producer')
        producer = json.loads(args.core_producer.read_text())
        previous = args.core_producer.resolve().parent.parent/'inputs/source'
        if (producer['platform'] != args.platform or producer['base_image'] != args.base_image
                or not all(producer['cleanup'].values()) or source_identity(previous) != producer['source_tree_sha256']):
            parser.error('Core cache producer/frozen source identity differs')
        if hashlib.file_digest(args.reuse_core_build.open('rb'),'sha256').hexdigest() != producer['artifact']['sha256']:
            parser.error('Core cache archive changed')
        recipe_changes = []
        for name in ('build_native_core.py','build_native_posix.py','build_native_dependencies.py','collect_posix_prerequisites.py'):
            if producer['copied_tool_inputs'][name] != hashlib.sha256((ROOT/'tools'/name).read_bytes()).hexdigest():
                prior_tool = previous.parent/'tools'/name
                if (name not in ('build_native_core.py','build_native_posix.py')
                        or hashlib.sha256(prior_tool.read_bytes()).hexdigest() != producer['copied_tool_inputs'][name]
                        or linux_recipe_identity(prior_tool) != linux_recipe_identity(ROOT/'tools'/name)):
                    parser.error('Linux compiler recipe changed since cache creation: '+name)
                recipe_changes.append(dict(tool=name, prior_sha256=producer['copied_tool_inputs'][name],
                                           current_sha256=hashlib.sha256((ROOT/'tools'/name).read_bytes()).hexdigest(),
                                           linux_recipe_ast_unchanged=True))
        previous_files = {path.relative_to(previous).as_posix(): path for path in previous.rglob('*') if path.is_file()}
        current_files = {path.relative_to(args.source).as_posix(): path for path in args.source.rglob('*') if path.is_file()}
        if previous_files.keys() != current_files.keys(): parser.error('Identity-only cache cannot add/remove source files')
        changes = [name for name in previous_files if previous_files[name].read_bytes() != current_files[name].read_bytes()]
        if not changes or any(name not in ('git-next-identity.h','git-next-integration-receipt.json','git.rc.in') for name in changes):
            parser.error('Core cache permits identity/resource metadata changes only')
        if 'git.rc.in' in changes:
            patches = [item for item in json.loads((ROOT/'native-core/patches/series.json').read_text()) if item['path']=='git.rc.in']
            if (hashlib.sha256(previous_files['git.rc.in'].read_bytes()).hexdigest() != patches[-1]['before_sha256']
                    or hashlib.sha256(current_files['git.rc.in'].read_bytes()).hexdigest() != patches[-1]['after_sha256']):
                parser.error('Resource metadata change does not match the declared source patch')
        core_cache = dict(archive_sha256=producer['artifact']['sha256'],
                          producer_receipt_sha256=hashlib.sha256(args.core_producer.read_bytes()).hexdigest(),
                          previous_source_tree_sha256=producer['source_tree_sha256'], changed_sources=changes,
                          unrelated_platform_recipe_changes=recipe_changes,
                          final_executables_relinked=True)
    if bool(args.reuse_dependencies) != bool(args.dependency_producer): parser.error('Dependency cache requires its producer receipt')
    if args.reuse_dependencies:
        producer = json.loads(args.dependency_producer.read_text())
        if producer['platform'] != args.platform or producer['base_image'] != args.base_image or not all(producer['cleanup'].values()): parser.error('Dependency producer identity/cleanup differs')
        for name in ('build_native_dependencies.py','collect_posix_prerequisites.py'):
            if producer['copied_tool_inputs'][name] != hashlib.sha256((ROOT/'tools'/name).read_bytes()).hexdigest(): parser.error('Dependency recipe changed since cache creation')
        with tarfile.open(args.reuse_dependencies) as cache:
            if any(item.name.split('/')[0] not in ('dependencies','tls') for item in cache.getmembers()): parser.error('Cache may contain libraries/prerequisites only')
            dependency = json.load(cache.extractfile('dependencies/receipt.json'))
        if dependency['platform'] != args.platform or dependency['lock_sha256'] != hashlib.sha256((ROOT/'packaging/native-dependencies.lock.json').read_bytes()).hexdigest(): parser.error('Cache dependency pins differ')
        reused = dict(archive_sha256=hashlib.file_digest(args.reuse_dependencies.open('rb'),'sha256').hexdigest(),
                      producer_receipt_sha256=hashlib.sha256(args.dependency_producer.read_bytes()).hexdigest())
    source = args.source.resolve(strict=True)
    before = source_identity(source)
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Host build input/output directory must be new')
    if ',' in str(destination): parser.error('Docker bind paths containing commas are unsupported')
    pin = json.loads((ROOT/'packaging/native-core.lock.json').read_text())
    if hashlib.sha256(args.core_archive.read_bytes()).hexdigest() != pin['archive_sha256']: parser.error('Original core archive does not match its pin')
    destination.mkdir(parents=True)
    inputs = destination/'inputs'; outputs = destination/'output'
    inputs.mkdir(); outputs.mkdir(); (inputs/'tools').mkdir(); (inputs/'packaging').mkdir(); (inputs/'archives').mkdir()
    shutil.copytree(source,inputs/'source')
    for path in (inputs/'source').rglob('*'):
        if '.github' in path.parts and 'workflows' in path.parts: raise ValueError('Workflow source entered the build context')
    if source_identity(inputs/'source') != before: raise ValueError('Copied core build inputs differ from their source receipt')
    for name in ('build_native_core.py','build_native_dependencies.py','build_native_posix.py','collect_posix_prerequisites.py','extract_build_archive.py','native_product.py'):
        shutil.copyfile(ROOT/'tools'/name,inputs/'tools'/name)
    for name in ('native-core.lock.json','native-dependencies.lock.json','product.json'):
        shutil.copyfile(ROOT/'packaging'/name,inputs/'packaging'/name)
    shutil.copyfile(ROOT/'Cargo.toml',inputs/'Cargo.toml')
    lock = json.loads((ROOT/'packaging/native-dependencies.lock.json').read_text())
    for component in lock['components'].values():
        archive = args.dependency_cache/component['archive']
        if archive.stat().st_size != component['bytes'] or hashlib.sha256(archive.read_bytes()).hexdigest() != component['sha256']:
            raise ValueError('Cached dependency input differs from its pin')
        shutil.copyfile(archive,inputs/'archives'/archive.name)
    modes = {}
    with zipfile.ZipFile(args.core_archive) as archive:
        for info in archive.infolist():
            if info.is_dir() or not info.filename.startswith(pin['archive_prefix']): continue
            name = info.filename[len(pin['archive_prefix']):]
            if (inputs/'source'/name).is_file(): modes[name] = (info.external_attr >> 16) & 0o777 or 0o644
    (inputs/'source-modes.json').write_text(json.dumps(modes))
    if reused:
        shutil.copyfile(args.reuse_dependencies,inputs/'cached-dependencies.tar.gz')
        if hashlib.file_digest((inputs/'cached-dependencies.tar.gz').open('rb'),'sha256').hexdigest() != reused['archive_sha256']: raise ValueError('Dependency cache changed during copy')
    if core_cache:
        shutil.copyfile(args.reuse_core_build,inputs/'cached-core-build.tar.gz')
        if hashlib.file_digest((inputs/'cached-core-build.tar.gz').open('rb'),'sha256').hexdigest() != core_cache['archive_sha256']: raise ValueError('Core cache changed during copy')
    script = '''#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive HOME=/tmp/git-next-build-home USERPROFILE=/tmp/git-next-build-home SSH_ROOT_PATH=/tmp/git-next-build-ssh
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0
mkdir -p "$HOME" "$SSH_ROOT_PATH"
apt-get update
apt-get install -y --no-install-recommends ca-certificates build-essential cmake ninja-build python3 pkg-config libgnutls28-dev libpsl-dev libidn2-dev libkrb5-dev libsecret-1-dev binutils
trap 'result=$?; if [ "$result" -ne 0 ] && [ -f /build/dependencies/receipt.json ] && [ -f /build/tls/receipt.json ]; then tar -C /build -czf /output/failed-build-dependencies.tar.gz dependencies tls; fi' EXIT
cp -a /inputs/source /build/source
python3 -c 'import json,pathlib; root=pathlib.Path("/build/source"); [(root/name).chmod(mode) for name,mode in json.load(open("/inputs/source-modes.json")).items()]'
if [ -f /inputs/cached-core-build.tar.gz ]; then
    python3 /inputs/tools/extract_build_archive.py /inputs/cached-core-build.tar.gz /build
    python3 -c 'import pathlib,shutil; root=pathlib.Path("/build"); shutil.rmtree(root/"core-stage"); [(root/"core-build"/name).unlink(missing_ok=True) for name in ("git","git-remote-http","git-http-fetch","git-http-push","git-http-backend","git-daemon","git-shell","scalar","git-imap-send","git-sh-i18n--envsubst")]; (root/"source/git-next-identity.h").touch()'
elif [ -f /inputs/cached-dependencies.tar.gz ]; then
    python3 /inputs/tools/extract_build_archive.py /inputs/cached-dependencies.tar.gz /build
else
    python3 /inputs/tools/collect_posix_prerequisites.py --platform "$GIT_NEXT_BUILD_TARGET" --destination /build/tls --base-image "$GIT_NEXT_BUILD_BASE"
    python3 /inputs/tools/build_native_dependencies.py --platform "$GIT_NEXT_BUILD_TARGET" --work-dir /build/dependency-work --cache /inputs/archives --prefix /build/dependencies --tls-prefix /build/tls --jobs "$GIT_NEXT_BUILD_JOBS"
fi
python3 /inputs/tools/build_native_posix.py --platform "$GIT_NEXT_BUILD_TARGET" --source /build/source --build-dir /build/core-build --dependencies /build/dependencies --stage /build/core-stage --receipt /output/core-build.json --jobs "$GIT_NEXT_BUILD_JOBS"
cp /build/dependencies/receipt.json /output/dependency-build.json
cp /build/tls/receipt.json /output/tls-prerequisites.json
dpkg-query -W '-f=${binary:Package}\\t${Version}\\t${source:Package}\\t${source:Version}\\n' > /output/build-environment-packages.tsv
tar -C /build -czf /output/native-build-products.tar.gz core-stage dependencies tls core-build
python3 -c 'import hashlib,json,pathlib; p=pathlib.Path("/output/native-build-products.tar.gz"); print(json.dumps({"archive_sha256":hashlib.file_digest(p.open("rb"),"sha256").hexdigest(),"bytes":p.stat().st_size,"product_executed":False,"tests_executed":False}))'
'''
    (inputs/'compile.sh').write_text(script,encoding='utf-8',newline='\n')
    scope = uuid.uuid4().hex
    volume = 'git-next-native-build-'+scope
    container = 'git-next-native-compile-'+scope
    labels = {'git-next.owner':OWNER,'git-next.scope':scope}
    created = False; cleanup = {}; status = 'failed'
    try:
        run('docker','volume','create','--label','git-next.owner='+OWNER,'--label','git-next.scope='+scope,volume)
        created = True
        run('docker','run','--rm','--pull','never','--platform','linux/'+arch,'--name',container,
            '--label','git-next.owner='+OWNER,'--label','git-next.scope='+scope,
            '--mount','type=bind,source='+str(inputs)+',target=/inputs,readonly',
            '--mount','type=bind,source='+str(outputs)+',target=/output',
            '--mount','type=volume,source='+volume+',target=/build',
            '--env','GIT_NEXT_BUILD_TARGET='+args.platform,'--env','GIT_NEXT_BUILD_BASE='+args.base_image,
            '--env','GIT_NEXT_BUILD_JOBS='+str(args.jobs),args.base_image,'/bin/sh','/inputs/compile.sh')
        if source_identity(inputs/'source') != before: raise ValueError('Frozen compiler source changed while the compiler was running')
        status = 'compiled-not-runtime-accepted'
    finally:
        probe = subprocess.run(['docker','inspect',container],capture_output=True,text=True)
        if probe.returncode == 0:
            value = json.loads(probe.stdout)[0]
            if any(value['Config']['Labels'].get(key) != item for key,item in labels.items()): raise ValueError('Refusing cleanup of an unrelated container')
            run('docker','rm','-f',container)
        cleanup['container_absent'] = subprocess.run(['docker','inspect',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
        if created:
            value = metadata('docker','volume','inspect',volume)[0]
            if any(value['Labels'].get(key) != item for key,item in labels.items()): raise ValueError('Refusing cleanup of an unrelated volume')
            run('docker','volume','rm',volume)
        cleanup['volume_absent'] = subprocess.run(['docker','volume','inspect',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
        record = dict(schema_version=1,status=status,platform=args.platform,base_image=args.base_image,base_image_id=image['Id'],
                      source_tree_sha256=before,scope=scope,cleanup=cleanup,product_executed=False,tests_executed=False,
                      copied_tool_inputs={path.name:hashlib.sha256(path.read_bytes()).hexdigest() for path in (inputs/'tools').glob('*.py')},
                      dependency_cache=reused,
                      identity_only_core_cache=core_cache,
                      installed=False,release_eligible=False)
        archive = outputs/'native-build-products.tar.gz'
        if archive.exists(): record['artifact'] = dict(name=archive.name,bytes=archive.stat().st_size,sha256=hashlib.file_digest(archive.open('rb'),'sha256').hexdigest())
        (outputs/'docker-compilation.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'status':status,'platform':args.platform,'cleanup':cleanup,'tests_executed':False}))


if __name__ == '__main__': main()
