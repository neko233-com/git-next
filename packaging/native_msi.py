"""Compile a full native-distribution MSI without running any product commands."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import struct
import subprocess
import tempfile
import uuid
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
NAMESPACE = uuid.UUID('57e363d4-5f82-4cd9-bd55-b37566f07458')
WIX = 'http://wixtoolset.org/schemas/v4/wxs'
ET.register_namespace('', WIX)
UPGRADES = {
    'windows-x64': '{981F8880-E13B-4D22-A36E-A1B166601663}',
    'windows-arm64': '{D21CC33B-CC02-41AF-95E4-3813F3F8E702}',
}


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def json_file(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True)+'\n',encoding='utf-8',newline='\n')


def safe_file(root, name):
    relative = PurePosixPath(name)
    if relative.is_absolute() or not relative.parts or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts):
        raise ValueError('Unsafe distribution path')
    path = root.joinpath(*relative.parts)
    if path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(root):
        raise ValueError('Distribution file is missing or escapes the root: '+name)
    return path


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--distribution',type=Path,required=True)
    parser.add_argument('--wix',type=Path,required=True)
    parser.add_argument('--output-dir',type=Path,default=ROOT/'dist/staged-native')
    parser.add_argument('--source-commit')
    parser.add_argument('--scope',choices=['user','machine'],default='machine')
    parser.add_argument('--allow-experimental-local',action='store_true')
    args = parser.parse_args()
    if os.name != 'nt':
        parser.error('The native MSI compiler requires Windows.')
    distribution = args.distribution.resolve(strict=True)
    manifest_file = distribution/'git-next-distribution.json'
    manifest_sha = sha(manifest_file)
    manifest = json.loads(manifest_file.read_text())
    if manifest.get('schema_version') != 1 or manifest.get('product') != 'git-next' or manifest.get('engine') != 'source-built-git-next-native-core' or manifest.get('upstream_git_core_deployed') is not False:
        parser.error('A complete owned native distribution manifest is required.')
    platform, version = manifest['platform'], manifest['version']
    if platform not in UPGRADES:
        parser.error('Unsupported MSI platform.')
    match = re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([A-Za-z0-9.-]+))?',version)
    if not match:
        parser.error('Invalid native distribution version.')
    numbers = tuple(int(match[i]) for i in range(1,4))
    if any(a>b for a,b in zip(numbers,(255,255,65535))):
        parser.error('Version exceeds Windows Installer limits.')
    complete = manifest.get('complete_git_parity') is True
    if not args.allow_experimental_local and (not complete or not manifest.get('release_eligible') or match[4] or not args.source_commit or not re.fullmatch('[a-f0-9]{40}',args.source_commit)):
        parser.error('Release packaging requires the accepted complete distribution, stable version and immutable source commit.')
    if len(manifest['files']) > 60000:
        parser.error('Distribution exceeds the supported MSI file count.')
    files = {}
    folded = set()
    for name,record in manifest['files'].items():
        if name.casefold() in folded:
            parser.error('Case-colliding distribution files.')
        folded.add(name.casefold())
        path = safe_file(distribution,name)
        if sha(path) != record['sha256'] or path.stat().st_size != record['bytes']:
            parser.error('Distribution byte mismatch: '+name)
        files[name] = path
    required = ('bin/git.exe','bin/git-next.exe','cmd/git.exe','runtime/etc/gitconfig',
                'updater/update.ps1','updater/Update.Core.psm1',
                'components/install-tls-provider.ps1','components/tls-providers.lock.json','LICENSE')
    required += ('updater/Runtime.Configuration.ps1','runtime/defaults/manifest.json','migration/takeover.ps1','migration/Takeover.Core.psm1',
                 'migration/git-next-bridge.exe','migration/git-next-bridge-gui.exe')
    if any(name not in files for name in required):
        parser.error('The complete core/runtime/configuration/update payload is required.')
    for folder,names in [('updater',('update.ps1','Update.Core.psm1','Scope.Core.ps1','Scope.Native.cs','Runtime.Configuration.ps1','messages.zh-CN.json')),
                         ('migration',('takeover.ps1','Takeover.Core.psm1')),
                         ('components',('install-tls-provider.ps1',))]:
        for name in names:
            if sha(files[folder+'/'+name]) != sha(ROOT/'packaging/windows'/name):
                parser.error('The distribution and MSI integration sources differ: '+folder+'/'+name)
    expected_machine = 0x8664 if platform == 'windows-x64' else 0xaa64
    for name in manifest['core_entrypoints']:
        data = files[name].read_bytes()
        pe = struct.unpack_from('<I',data,0x3c)[0]
        if data[:2] != b'MZ' or data[pe:pe+4] != b'PE\0\0' or struct.unpack_from('<H',data,pe+4)[0] != expected_machine:
            parser.error('Native primary entrypoint architecture mismatch.')
    wix = args.wix.resolve(strict=True)
    environment = dict(os.environ,DOTNET_ROLL_FORWARD='Major')
    tool_version = subprocess.check_output([str(wix),'--version'],env=environment,text=True).strip()
    if not tool_version.startswith('5.0.2+'):
        parser.error('Use the pinned WiX 5.0.2 compiler.')
    output = args.output_dir.resolve()
    output.mkdir(parents=True,exist_ok=True)
    asset = f'git-next-{version}-{platform}' + ('-user' if args.scope == 'user' else '') + '.msi'
    destination = output/asset
    if destination.exists() or destination.with_suffix('.receipt.json').exists():
        parser.error('Refusing to overwrite a package or its receipt.')
    identity_suffix = '/machine' if args.scope == 'machine' else ''
    upgrade_code = UPGRADES[platform] if args.scope == 'user' else '{'+str(uuid.uuid5(NAMESPACE,'upgrade/'+platform+'/machine')).upper()+'}'
    product_code = '{'+str(uuid.uuid5(NAMESPACE,version+'/'+platform+identity_suffix)).upper()+'}'
    registry_hive = 'HKLM' if args.scope == 'machine' else 'HKCU'
    package = dict(schema_version=1,product='git-next',install_kind='msi',install_scope=args.scope,engine=manifest['engine'],
                   version=version,platform=platform,msi_version='.'.join(map(str,numbers)),
                   product_code=product_code,upgrade_code=upgrade_code,source_commit=args.source_commit,
                   source_tree_sha256=manifest['source_tree_sha256'],complete_git_parity=complete,
                   builtins=manifest['builtins'],
                   system_git_replaced=False,release_eligible=not args.allow_experimental_local,
                   files={name:record['sha256'] for name,record in manifest['files'].items()})
    package['files']['git-next-distribution.json'] = manifest_sha
    packaging = {name:sha(ROOT/'packaging'/name) for name in ('native_msi.py','windows/Product.wxs')}
    with tempfile.TemporaryDirectory(prefix='git-next-native-msi-') as temporary:
        stage = Path(temporary)
        json_file(stage/'package.json',package)
        files = dict(files,**{'git-next-distribution.json':manifest_file,'package.json':stage/'package.json'})
        wix_root = ET.Element('{'+WIX+'}Wix')
        fragment = ET.SubElement(wix_root,'Fragment')
        ref = ET.SubElement(fragment,'DirectoryRef',Id='INSTALLFOLDER')
        group = ET.SubElement(fragment,'ComponentGroup',Id='Payload')
        directories = {'.':('INSTALLFOLDER',ref)}

        def directory(name):
            if name not in directories:
                relative = PurePosixPath(name)
                parent_id,parent = directory(relative.parent.as_posix())
                ident = 'Dir_'+hashlib.sha256(name.encode()).hexdigest()[:24]
                element = ET.SubElement(parent,'Directory',Id=ident,Name=relative.name)
                directories[name] = ident,element
            return directories[name]

        for name,path in sorted(files.items()):
            relative = PurePosixPath(name)
            parent = relative.parent.as_posix()
            # Product.wxs owns these shared integration directories.
            if parent == 'bin':
                directory_id = 'BINDIR'
            elif parent == 'updater':
                directory_id = 'UPDATERDIR'
            else:
                directory_id,_ = directory(parent)
            ident = 'Payload_'+hashlib.sha256(name.encode()).hexdigest()[:24]
            guid = '{'+str(uuid.uuid5(NAMESPACE,'component/'+platform+'/'+name+identity_suffix)).upper()+'}'
            component = ET.SubElement(group,'Component',Id=ident,Guid=guid,Directory=directory_id)
            ET.SubElement(component,'RegistryValue',Root=registry_hive,Key='Software\\git-next\\MSI\\'+platform+'\\Files',Name=name,Type='string',Value=version,KeyPath='yes')
            attributes = dict(Id=ident+'_File',Source=str(path),Name=relative.name)
            if manifest['files'].get(name,{}).get('windows_attributes',0) & 4:
                attributes['System'] = 'yes'
            element = ET.SubElement(component,'File',**attributes)
            if args.scope == 'machine':
                ET.SubElement(element,'PermissionEx',Sddl='O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;BU)')
        for name,(ident,_) in directories.items():
            if name == '.':
                continue
            key = 'Remove_'+hashlib.sha256(name.encode()).hexdigest()[:24]
            component = ET.SubElement(group,'Component',Id=key,Directory=ident,Guid='{'+str(uuid.uuid5(NAMESPACE,'directory/'+platform+'/'+name+identity_suffix)).upper()+'}')
            ET.SubElement(component,'RegistryValue',Root=registry_hive,Key='Software\\git-next\\MSI\\'+platform+'\\Directories',Name=name,Type='string',Value=version,KeyPath='yes')
            ET.SubElement(component,'RemoveFolder',Id=key+'_Folder',Directory=ident,On='uninstall')
        ET.ElementTree(wix_root).write(stage/'Payload.wxs',encoding='utf-8',xml_declaration=True)
        terminal = 'bin/git-terminal.exe' if 'bin/git-terminal.exe' in files else 'runtime/git-bash.exe'
        if terminal not in files:
            parser.error('The distribution does not include an owned terminal launcher.')
        defines = dict(MsiVersion=package['msi_version'],ProductCode=product_code,UpgradeCode=package['upgrade_code'],
                       InstallScope=args.scope,PackageScope='perMachine' if args.scope == 'machine' else 'perUser',
                       RegistryHive=registry_hive,PathSystem='yes' if args.scope == 'machine' else 'no',
                       TaskImpersonate='no' if args.scope == 'machine' else 'yes',
                       Platform=platform,TerminalRelativePath=terminal.replace('/','\\'),
                       ProductDescription='git-next complete native runtime; '+('accepted release' if complete else 'unpublished candidate; takeover blocked'))
        command = [str(wix),'build',str(ROOT/'packaging/windows/Product.wxs'),str(stage/'Payload.wxs'),
                   '-arch','x64' if platform == 'windows-x64' else 'arm64','-pdbtype','none',
                   '-intermediatefolder',str(stage/'obj'),'-o',str(stage/asset)]
        for key,value in defines.items():
            command += ['-d',key+'='+value]
        subprocess.run(command,env=environment,check=True)
        if sha(manifest_file) != manifest_sha or any(sha(path) != package['files'][name]
                for name,path in files.items() if name != 'package.json') or any(sha(ROOT/'packaging'/name) != digest for name,digest in packaging.items()):
            raise RuntimeError('Native MSI inputs changed during compilation.')
        shutil.copyfile(stage/asset,destination)
    result = dict(status='built-not-accepted',asset=asset,sha256=sha(destination),size=destination.stat().st_size,
                  version=version,platform=platform,product_code=product_code,upgrade_code=package['upgrade_code'],
                  install_scope=args.scope,
                  msi_version=package['msi_version'],source_tree_sha256=manifest['source_tree_sha256'],
                  distribution_manifest_sha256=manifest_sha,payload_files=len(files),packaging_files=packaging,
                  complete_git_parity=complete,release_eligible=package['release_eligible'],
                  wix_version=tool_version,product_executed=False,tests_executed=False,
                  installation_runtime_accepted=False)
    json_file(destination.with_suffix('.receipt.json'),result)
    print(json.dumps(result,indent=2))


if __name__ == '__main__':
    main()
