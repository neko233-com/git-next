"""Build the committed peer GPUI Fast terminal and collect its dependency notices."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tomllib


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--destination', type=Path, required=True)
    parser.add_argument('--platform', choices=('windows-x64','windows-arm64'), required=True)
    parser.add_argument('--cargo', type=Path, required=True)
    parser.add_argument('--git', type=Path, required=True)
    parser.add_argument('--llvm-mingw', type=Path, help='pinned isolated SDK for the ARM64 GNU LLVM target')
    args = parser.parse_args()
    source = args.source.resolve(strict=True)
    output = args.destination.resolve()
    if output.exists(): parser.error('Use a new terminal package destination')
    git = str(args.git.resolve(strict=True))
    commit = subprocess.check_output([git,'rev-parse','HEAD'],cwd=source,text=True).strip()
    dirty = subprocess.check_output([git,'status','--porcelain','--untracked-files=no'],cwd=source,text=True)
    if dirty: parser.error('Commit the complete terminal implementation before packaging')
    package = tomllib.loads((source/'Cargo.toml').read_text())['package']
    if package['name'] != 'git-terminal': parser.error('The independent Git-Terminal project is required')
    target = {'windows-x64':'x86_64-pc-windows-msvc','windows-arm64':'aarch64-pc-windows-gnullvm'}[args.platform]
    environment = dict(os.environ)
    environment['PATH'] = str(args.cargo.resolve().parent) + os.pathsep + environment['PATH']
    runtime_pin = None
    if args.platform == 'windows-arm64':
        if not args.llvm_mingw: parser.error('ARM64 uses the pinned isolated LLVM MinGW SDK')
        sdk = args.llvm_mingw.resolve(strict=True)
        catalog = json.loads((Path(__file__).resolve().parents[1]/'packaging/git-terminal-runtime.lock.json').read_text())
        runtime_pin = catalog['platforms'][args.platform]
        sdk_receipt = json.loads((sdk/'receipt.json').read_text())
        if sdk_receipt['source']['sha256'] != catalog['source_archive']['sha256']: parser.error('Compiler SDK pin mismatch')
        for name in (runtime_pin['path'],runtime_pin['license_path'],'bin/aarch64-w64-mingw32-clang.exe','bin/llvm-ar.exe'):
            if sha(sdk/name) != sdk_receipt['files'][name]: parser.error('Isolated SDK bytes changed')
        if sha(sdk/runtime_pin['path']) != runtime_pin['sha256'] or sha(sdk/runtime_pin['license_path']) != runtime_pin['license_sha256']: parser.error('Pinned unwind runtime changed')
        environment['PATH'] = str(sdk/'bin') + os.pathsep + environment['PATH']
        environment['CARGO_TARGET_AARCH64_PC_WINDOWS_GNULLVM_LINKER'] = str(sdk/'bin/aarch64-w64-mingw32-clang.exe')
        environment['CC_aarch64_pc_windows_gnullvm'] = str(sdk/'bin/aarch64-w64-mingw32-clang.exe')
        environment['AR_aarch64_pc_windows_gnullvm'] = str(sdk/'bin/llvm-ar.exe')
    command = [str(args.cargo.resolve()),'build','--locked','--release','--target',target,'--target-dir',str(source/'target')]
    subprocess.run(command,cwd=source,env=environment,check=True)
    metadata = json.loads(subprocess.check_output([str(args.cargo.resolve()),'metadata','--locked','--format-version','1','--filter-platform',target],cwd=source,env=environment))
    nodes = {node['id']:node for node in metadata['resolve']['nodes']}
    pending = [metadata['resolve']['root']] if metadata['resolve']['root'] else metadata['workspace_default_members'][:]
    reached = set()
    while pending:
        identity = pending.pop()
        if identity in reached: continue
        reached.add(identity)
        pending.extend(nodes[identity]['dependencies'])
    output.mkdir(parents=True)
    for name in ('git-terminal.exe','git-terminal-shell.exe','git-terminal-askpass.exe'):
        shutil.copyfile(source/'target'/target/'release'/name,output/name)
    if runtime_pin:
        shutil.copyfile(sdk/runtime_pin['path'],output/'libunwind.dll')
        (output/'licenses/llvm-unwind').mkdir(parents=True)
        shutil.copyfile(sdk/runtime_pin['license_path'],output/'licenses/llvm-unwind/LICENSE.TXT')
    dependencies = []
    (output/'shell-integration').mkdir()
    for path in (source/'shell-integration').iterdir():
        if path.is_file():
            shutil.copyfile(path,output/'shell-integration'/path.name)
    for item in metadata['packages']:
        if item['id'] not in reached: continue
        root = Path(item['manifest_path']).parent.resolve(strict=True)
        notices = [p for p in root.iterdir() if p.is_file() and p.name.upper().startswith(('LICENSE','COPYING','NOTICE','AUTHORS'))]
        if item.get('license_file'):
            path = Path(item['license_file'])
            if not path.is_absolute(): path = root/path
            if path not in notices: notices.append(path)
        names = []
        for path in notices:
            if path.is_symlink() or not path.resolve(strict=True).is_relative_to(root): parser.error('Unsafe dependency notice')
            relative = 'licenses/'+item['name']+'-'+item['version']+'/'+path.name
            destination = output/relative
            destination.parent.mkdir(parents=True,exist_ok=True)
            shutil.copyfile(path,destination)
            names.append(relative)
        dependencies.append(dict(name=item['name'],version=item['version'],license=item.get('license'),notices=names))
    for name in ('README.md','AGENTS.md','LICENSE','docs/site/index.html','vendor/gpui-terminal/MODIFICATIONS.md','vendor/portable-pty/MODIFICATIONS.md'):
        destination = output/'manuals'/name
        destination.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(source/name,destination)
    (output/'DEPENDENCIES.json').write_text(json.dumps(dependencies,indent=2)+'\n',encoding='utf-8',newline='\n')
    receipt = dict(schema_version=1,product='git-terminal',version=package['version'],platform=args.platform,
                   renderer='gpui-fast',renderer_version='0.1.10',source_repository=package['repository'],source_commit=commit,
                   source_built=True,profile='release',tests_executed=False,
                   rust_target=target,additional_runtime=runtime_pin,
                   files={path.relative_to(output).as_posix():sha(path) for path in output.rglob('*') if path.is_file()})
    (output/'git-terminal-package.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(dict(status='built-not-runtime-accepted',product='git-terminal',platform=args.platform,source_commit=commit,files=len(receipt['files']))))


if __name__ == '__main__': main()
