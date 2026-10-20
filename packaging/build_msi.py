"""Build an owned per-user MSI from pinned local native artifacts; never publish."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
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


def source_hash():
    digest = hashlib.sha256()
    paths = [ROOT / 'Cargo.toml', ROOT / 'Cargo.lock', ROOT / 'compatibility.json', ROOT / 'docs/COMPATIBILITY.md',
             *ROOT.joinpath('src').rglob('*.rs'), *ROOT.joinpath('vendor').rglob('*.rs'), *ROOT.joinpath('vendor').rglob('Cargo.toml')]
    for path in sorted(paths):
        digest.update(path.relative_to(ROOT).as_posix().encode() + b'\0' + path.read_bytes())
    return digest.hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n', encoding='utf-8', newline='\n')


def main():
    if '--distribution' in sys.argv[1:]:
        from native_msi import main as native_main
        return native_main()
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--binary-dir', type=Path, required=True)
    parser.add_argument('--build-receipt', type=Path, required=True)
    parser.add_argument('--wix', type=Path, required=True)
    parser.add_argument('--platform', choices=UPGRADES, default='windows-x64')
    parser.add_argument('--output-dir', type=Path, default=ROOT / 'dist/staged')
    parser.add_argument('--source-commit', help='full immutable source commit; mandatory for release packaging')
    parser.add_argument('--allow-experimental-local', action='store_true', help='build an unpublished package with replacement blocked')
    args = parser.parse_args()
    if os.name != 'nt':
        parser.error('Local MSI compilation requires Windows and the pinned WiX toolchain.')
    inputs = args.binary_dir.resolve(strict=True)
    receipt = json.loads(args.build_receipt.read_text(encoding='utf-8-sig'))
    before = source_hash()
    if receipt.get('status') != 'pass' or receipt.get('source_tree_sha256') != before:
        parser.error('The native build receipt does not identify current source inputs.')
    version = re.search(r'^version = "([^"]+)"', (ROOT / 'Cargo.toml').read_text(), re.M)[1]
    match = re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([A-Za-z0-9.-]+))?', version)
    if not match:
        parser.error('Invalid package version.')
    numbers = tuple(int(match[i]) for i in range(1, 4))
    if any(a > b for a, b in zip(numbers, (255, 255, 65535))):
        parser.error('Version exceeds Windows Installer limits.')
    compatibility = json.loads((ROOT / 'compatibility.json').read_text())
    complete = compatibility['complete_git_parity'] is True
    if not args.allow_experimental_local and (not complete or match[4] or not args.source_commit or not re.fullmatch('[a-f0-9]{40}', args.source_commit)):
        parser.error('Release MSI packaging requires complete parity, stable version and an immutable source commit.')
    wix = args.wix.resolve(strict=True)
    environment = dict(os.environ, DOTNET_ROLL_FORWARD='Major')
    tool_version = subprocess.check_output([str(wix), '--version'], env=environment, text=True).strip()
    if not tool_version.startswith('5.0.2+'):
        parser.error('Use the pinned WiX 5.0.2 build tool.')
    machine = 0x8664 if args.platform == 'windows-x64' else 0xaa64
    binaries = ('git-next.exe', 'git.exe', 'git-terminal.exe', 'git-lfs.exe', 'git-next-lfs-filter.exe')
    for name in binaries:
        path = inputs / name
        if sha(path) != receipt['binaries'][name]:
            parser.error('Native binary differs from its build receipt: ' + name)
        data = path.read_bytes()
        pe = struct.unpack_from('<I', data, 0x3c)[0]
        if data[:2] != b'MZ' or data[pe:pe + 4] != b'PE\0\0' or struct.unpack_from('<H', data, pe + 4)[0] != machine:
            parser.error('Executable architecture mismatch: ' + name)
    if args.platform == 'windows-x64':
        identity = json.loads(subprocess.check_output([str(inputs / 'git-next.exe'), 'version', '--json']))
        if identity['product'] != 'git-next' or identity['version'] != version or identity['external_git_fallback'] or identity['external_ssh_fallback']:
            parser.error('Unrecognized native client.')
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    name = f'git-next-{version}-{args.platform}.msi'
    destination = output / name
    if destination.exists() or destination.with_suffix('.receipt.json').exists():
        parser.error('Refusing to overwrite a staged MSI or its receipt.')
    source_files = {path.relative_to(ROOT).as_posix(): sha(path) for path in sorted((ROOT / 'packaging').rglob('*')) if path.is_file() and path.suffix in ('.py','.ps1','.psm1','.wxs','.json')}
    product_code = '{' + str(uuid.uuid5(NAMESPACE, version + '/' + args.platform)).upper() + '}'
    package = dict(schema_version=1, product='git-next', install_kind='msi', version=version,
                   platform=args.platform, msi_version='.'.join(map(str, numbers)), product_code=product_code,
                   upgrade_code=UPGRADES[args.platform], source_commit=args.source_commit,
                   source_tree_sha256=before, complete_git_parity=complete, system_git_replaced=False,
                   release_eligible=not args.allow_experimental_local)
    with tempfile.TemporaryDirectory(prefix='git-next-msi-') as directory:
        stage = Path(directory)
        payload = stage / 'payload'
        files = {
            'bin/git-next.exe': inputs / 'git-next.exe',
            'bin/git-terminal.exe': inputs / 'git-terminal.exe',
            'bin/git-next-lfs.exe': inputs / 'git-lfs.exe',
            'bin/git-next-lfs-filter.exe': inputs / 'git-next-lfs-filter.exe',
            'preview/bin/git.exe': inputs / 'git-next.exe',
            'preview/bin/git-lfs.exe': inputs / 'git-lfs.exe',
            'preview/bin/git-next-lfs-filter.exe': inputs / 'git-next-lfs-filter.exe',
            'updater/update.ps1': ROOT / 'packaging/windows/update.ps1',
            'updater/Update.Core.psm1': ROOT / 'packaging/windows/Update.Core.psm1',
            'updater/Scope.Core.ps1': ROOT / 'packaging/windows/Scope.Core.ps1',
            'updater/Scope.Native.cs': ROOT / 'packaging/windows/Scope.Native.cs',
            'updater/Runtime.Configuration.ps1': ROOT / 'packaging/windows/Runtime.Configuration.ps1',
            'updater/messages.zh-CN.json': ROOT / 'packaging/windows/messages.zh-CN.json',
        }
        package['files'] = {}
        for relative, source in files.items():
            target = payload / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)
            package['files'][relative] = sha(target)
        write_json(payload / 'package.json', package)
        shutil.copyfile(ROOT / 'LICENSE', payload / 'LICENSE')
        wix_root = ET.Element('{' + WIX + '}Wix')
        fragment = ET.SubElement(wix_root, 'Fragment')
        group = ET.SubElement(fragment, 'ComponentGroup', Id='Payload')
        directories = {'bin': 'BINDIR', 'preview/bin': 'PREVIEWBINDIR', 'updater': 'UPDATERDIR', '.': 'INSTALLFOLDER'}
        for path in sorted(payload.rglob('*')):
            if not path.is_file():
                continue
            relative = path.relative_to(payload).as_posix()
            ident = 'Payload_' + hashlib.sha256(relative.encode()).hexdigest()[:24]
            # HKCU key paths give per-user components correct repair semantics.
            component_guid = '{' + str(uuid.uuid5(NAMESPACE, 'component/' + args.platform + '/' + relative)).upper() + '}'
            component = ET.SubElement(group, 'Component', Id=ident, Guid=component_guid, Directory=directories[path.relative_to(payload).parent.as_posix()])
            ET.SubElement(component, 'RegistryValue', Root='HKCU', Key='Software\\git-next\\MSI\\' + args.platform + '\\Files', Name=relative, Type='string', Value=package['version'], KeyPath='yes')
            ET.SubElement(component, 'File', Id=ident + '_File', Source=str(path), Name=path.name)
        ET.ElementTree(wix_root).write(stage / 'Payload.wxs', encoding='utf-8', xml_declaration=True)
        command = [str(wix), 'build', str(ROOT / 'packaging/windows/Product.wxs'), str(stage / 'Payload.wxs'),
                   '-arch', 'x64' if args.platform == 'windows-x64' else 'arm64', '-pdbtype', 'none',
                   '-intermediatefolder', str(stage / 'obj'), '-o', str(stage / name)]
        defines = dict(MsiVersion=package['msi_version'], ProductCode=product_code, UpgradeCode=package['upgrade_code'],
                       InstallScope='user',PackageScope='perUser',RegistryHive='HKCU',PathSystem='no',TaskImpersonate='yes',
                       TerminalRelativePath='bin\\git-terminal.exe',
                       Platform=args.platform, ProductDescription='git-next native client; ' + ('accepted release' if complete else 'experimental preview; Git replacement blocked'))
        for key, value in defines.items():
            command += ['-d', key + '=' + value]
        subprocess.run(command, env=environment, check=True)
        if source_hash() != before or any(sha(ROOT / path) != digest for path, digest in source_files.items()) or any(sha(inputs / name) != receipt['binaries'][name] for name in binaries):
            raise RuntimeError('Package inputs changed during MSI compilation.')
        shutil.copyfile(stage / name, destination)
    result = dict(status='built-not-accepted', asset=name, sha256=sha(destination), size=destination.stat().st_size,
                  version=version, platform=args.platform, product_code=product_code, upgrade_code=package['upgrade_code'],
                  msi_version=package['msi_version'], source_tree_sha256=before, packaging_files=source_files,
                  source_commit=args.source_commit, complete_git_parity=complete, release_eligible=package['release_eligible'],
                  wix_version=tool_version, native_build_receipt_sha256=sha(args.build_receipt),
                  installation_runtime_accepted=False)
    write_json(destination.with_suffix('.receipt.json'), result)
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
