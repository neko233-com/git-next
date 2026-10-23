"""Assemble owned Windows core/runtime bytes; never execute or install the product."""
import argparse
import ctypes
import hashlib
import html
import json
from pathlib import Path
import re
import shutil
import struct

from build_native_core import source_identity
from prepare_windows_runtime import CORE_NAMES, system_attribute
from prepare_tls_provider import imports
from native_product import native_compatibility
import runtime_defaults
import os

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def machine(path):
    data = path.read_bytes()
    if data[:2] != b'MZ':
        raise ValueError('Expected native PE input: ' + path.name)
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if data[pe:pe+4] != b'PE\0\0':
        raise ValueError('Invalid native PE signature: ' + path.name)
    return struct.unpack_from('<H', data, pe+4)[0]


def file_version(path):
    library = ctypes.WinDLL('version', use_last_error=True)
    library.GetFileVersionInfoSizeW.argtypes = [ctypes.c_wchar_p, ctypes.POINTER(ctypes.c_ulong)]
    library.GetFileVersionInfoW.argtypes = [ctypes.c_wchar_p, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_void_p]
    library.VerQueryValueW.argtypes = [ctypes.c_void_p, ctypes.c_wchar_p, ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(ctypes.c_uint)]
    unused = ctypes.c_ulong()
    size = library.GetFileVersionInfoSizeW(str(path), ctypes.byref(unused))
    if not size: raise ValueError('Native core lacks its numeric product version: '+path.name)
    buffer = ctypes.create_string_buffer(size)
    value = ctypes.c_void_p(); length = ctypes.c_uint()
    if (not library.GetFileVersionInfoW(str(path), 0, size, buffer)
            or not library.VerQueryValueW(buffer, '\\', ctypes.byref(value), ctypes.byref(length)) or length.value < 52):
        raise ValueError('Native core version resource is unreadable: '+path.name)
    signature, _, major_minor, build_revision = struct.unpack('<4I', ctypes.string_at(value, 16))
    if signature != 0xfeef04bd: raise ValueError('Invalid fixed version resource')
    return (major_minor >> 16, major_minor & 65535, build_revision >> 16, build_revision & 65535)


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--build-dir', required=True, type=Path)
    parser.add_argument('--build-receipt', required=True, type=Path)
    parser.add_argument('--runtime-assets', required=True, type=Path)
    parser.add_argument('--crt-dir', type=Path, help='licensed Microsoft CRT when required by the selected compiler')
    parser.add_argument('--psl', required=True, type=Path, help='verified owned libpsl runtime/import prefix')
    parser.add_argument('--bridge', required=True, type=Path, help='owned compiled legacy command bridge directory')
    parser.add_argument('--terminal', type=Path, help='source-built peer Git-Terminal package directory')
    parser.add_argument('--msi-worker',type=Path,required=True,help='source-built no-console Windows Installer worker')
    parser.add_argument('--destination', required=True, type=Path, help='new owned package root')
    args = parser.parse_args()
    source = args.source.resolve(strict=True)
    build = args.build_dir.resolve(strict=True)
    assets = args.runtime_assets.resolve(strict=True)
    crt = args.crt_dir.resolve(strict=True) if args.crt_dir else None
    psl = args.psl.resolve(strict=True)
    bridge = args.bridge.resolve(strict=True)
    destination = args.destination.resolve()
    if destination.exists():
        parser.error('Distribution destination must not exist.')
    receipt = json.loads(args.build_receipt.read_text())
    compatibility = native_compatibility(ROOT)
    declared = '#define GIT_NEXT_COMPLETE_GIT_PARITY ' + ('1' if compatibility['complete_git_parity'] else '0')
    if declared not in (source/'git-next-identity.h').read_text():
        parser.error('Native compatibility metadata differs from the compiled source declaration.')
    asset_receipt = json.loads((assets / 'git-next-runtime-assets.json').read_text())
    platform = asset_receipt['platform']
    expected_machine = 0x8664 if platform == 'windows-x64' else 0xaa64
    worker=args.msi_worker.resolve(strict=True)
    worker_receipt=json.loads((worker/'receipt.json').read_text())
    if (worker_receipt['platform']!=platform or worker_receipt['version']!=receipt['product_version']
            or worker_receipt['source_sha256']!=sha(ROOT/'native-core/launcher/windows-msi-worker.c')
            or worker_receipt['resource_sha256']!=sha(ROOT/'native-core/launcher/windows-msi-worker.rc.in')
            or worker_receipt['binary_sha256']!=sha(worker/'msi-worker.exe')
            or machine(worker/'msi-worker.exe')!=expected_machine):
        parser.error('Windows Installer worker does not match its owned source/build receipt')
    bridge_receipt = json.loads((bridge/'receipt.json').read_text())
    if (bridge_receipt['platform'] != platform or bridge_receipt['source_sha256'] != sha(ROOT/'native-core/launcher/windows-bridge.c')
            or bridge_receipt['resource_sha256'] != sha(ROOT/'native-core/launcher/windows-bridge.rc.in') or bridge_receipt['product_version'] != receipt['product_version']
            or any(sha(bridge/name) != record['sha256'] or machine(bridge/name) != expected_machine for name,record in bridge_receipt['binaries'].items())):
        parser.error('Owned migration bridge differs from its source/build receipt.')
    psl_receipt = json.loads((psl/'receipt.json').read_text())
    if psl_receipt['platform'] != platform or any(sha(psl/'bin'/name) != record['sha256']
            for name, record in psl_receipt['files'].items()):
        parser.error('Owned libpsl runtime differs from its receipt.')
    if receipt['source_tree_sha256'] != source_identity(source):
        parser.error('Core build receipt does not identify current integrated sources.')
    for name, record in receipt['binaries'].items():
        if sha(build / name) != record['sha256'] or machine(build / name) != expected_machine:
            parser.error('Core byte/machine mismatch: ' + name)
        if file_version(build/name) != tuple(map(int,receipt['product_version'].split('-',1)[0].split('.'))) + (0,):
            parser.error('Native numeric file version must follow git-next upgrades: '+name)
    for relative, record in asset_receipt['files'].items():
        if sha(assets / relative) != record['sha256']:
            parser.error('Runtime asset differs from its source receipt: ' + relative)
        if Path(relative).name.lower() in CORE_NAMES:
            parser.error('Upstream Git core remains in the runtime asset inputs.')
    crt_files = sorted(crt.glob('vcruntime140*.dll')) if crt else []
    if platform == 'windows-x64' and not any(path.name.lower() == 'vcruntime140.dll' for path in crt_files):
        parser.error('Microsoft redistributable vcruntime140.dll is required.')
    if any(machine(path) != expected_machine for path in crt_files):
        parser.error('VC redistributable machine mismatch.')
    destination.mkdir(parents=True)
    provenance = {}

    def copy(path, relative, kind, attributes=0):
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, target)
        if attributes & 4:
            system_attribute(target)
        provenance[relative] = dict(sha256=sha(target), bytes=target.stat().st_size,
                                    kind=kind, windows_attributes=attributes)

    def write(relative, data, kind):
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        provenance[relative] = dict(sha256=sha(target), bytes=len(data), kind=kind, windows_attributes=0)

    copy(ROOT/'packaging/corresponding-sources.lock.json','LICENSES/corresponding-sources.lock.json','corresponding-source-manifest')
    terminal_identity = None
    if args.terminal:
        terminal_root = args.terminal.resolve(strict=True)
        terminal_identity = json.loads((terminal_root/'git-terminal-package.json').read_text())
        if (terminal_identity.get('product') != 'git-terminal' or terminal_identity.get('platform') != platform
                or terminal_identity.get('renderer') != 'gpui-fast' or not re.fullmatch('[a-f0-9]{40}',terminal_identity.get('source_commit',''))):
            parser.error('The matching source-built GPUI Fast terminal package is required.')
        for name, digest in terminal_identity['files'].items():
            path = terminal_root/name
            if path.is_symlink() or not path.resolve(strict=True).is_relative_to(terminal_root) or sha(path) != digest:
                parser.error('Git-Terminal package input changed.')
            relative = 'bin/'+name if name in ('git-terminal.exe','git-terminal-shell.exe','git-terminal-askpass.exe','libunwind.dll') or name.startswith('shell-integration/') else 'LICENSES/git-terminal/'+name
            kind = 'source-built-git-terminal' if name in ('git-terminal.exe','git-terminal-shell.exe','git-terminal-askpass.exe') else ('pinned-git-terminal-runtime' if name == 'libunwind.dll' else 'git-terminal-notice')
            copy(path,relative,kind)
        copy(terminal_root/'git-terminal-package.json','LICENSES/git-terminal/git-terminal-package.json','git-terminal-source-identity')
        if machine(destination/'bin/git-terminal.exe') != expected_machine:
            parser.error('Git-Terminal architecture mismatch.')

    default_configurations = []
    for relative, record in asset_receipt['files'].items():
        if Path(relative).name.lower() in ('libcurl-openssl-4.dll','git-update-git-for-windows'):
            continue
        if re.match(r'^(?:etc|usr/etc|mingw64/etc|clangarm64/etc)/',relative) and relative != 'etc/gitconfig':
            default = 'runtime/defaults/' + relative
            copy(assets / relative, default, 'pinned-runtime-configuration-default', record['windows_attributes'])
            default_configurations.append(dict(target='runtime/'+relative,source=default,
                                               sha256=record['sha256'],attributes=record['windows_attributes']))
        else:
            copy(assets / relative, 'runtime/' + relative, 'pinned-third-party-runtime', record['windows_attributes'])
    write('runtime/defaults/manifest.json',(json.dumps(dict(schema_version=1,product='git-next',
          version=receipt['product_version'],platform=platform,files=default_configurations),indent=2)+'\n').encode(),
          'product-runtime-configuration-manifest')
    native = 'runtime/' + asset_receipt['native_prefix']
    for hook in ('pre-push','post-checkout','post-commit','post-merge'):
        copy(ROOT/'packaging/lfs-hooks'/hook,native+'/share/git-core/templates/hooks/'+hook,'builtin-standard-lfs-hook')
    copy(ROOT/'packaging/lfs-hooks/LICENSE.md','LICENSES/git-lfs-hook-templates/LICENSE.md','lfs-hook-license')
    copy(ROOT/'packaging/lfs-hooks/source.json','LICENSES/git-lfs-hook-templates/source.json','lfs-hook-source-identity')
    helper = native + '/libexec/git-core'
    shell_i18n = (source/'git-sh-i18n.sh').read_text(encoding='utf-8')
    if 'GIT_INTERNAL_GETTEXT_SH_SCHEME=git-next' not in shell_i18n:
        parser.error('The source tree lacks native shell language integration.')
    shell_i18n = shell_i18n.replace('@LOCALEDIR@',native+'/share/locale').replace('@USE_GETTEXT_SCHEME@','')
    write(helper+'/git-sh-i18n',shell_i18n.encode('utf-8'),'product-native-shell-language')
    perl_i18n = (source/'perl/Git/I18N.pm').read_text(encoding='utf-8')
    if '__native_translation' not in perl_i18n:
        parser.error('The source tree lacks native Perl language integration.')
    perl_i18n = perl_i18n.replace('@NO_GETTEXT@','1').replace('@LOCALEDIR@',native+'/share/locale')
    write(native+'/share/perl5/Git/I18N.pm',perl_i18n.encode('utf-8'),'product-native-perl-language')
    core = build / 'git.exe'
    command_table = (source/'git.c').read_text(encoding='utf-8').split('static struct cmd_struct commands[] = {',1)[1].split('\n};',1)[0]
    builtins = sorted(set(re.findall(r'\{\s*"([a-z][a-z0-9-]*)"\s*,',command_table)))
    if len(builtins) < 100:
        raise ValueError('The pinned compiled command table was not identified')
    # All public/internal Git entrypoints use these exact owned core bytes.
    for directory in ('bin', 'cmd', 'runtime/bin', 'runtime/cmd', native + '/bin', helper):
        copy(core, directory + '/git.exe', 'source-built-git-next-core')
    copy(core, 'bin/git-next.exe', 'source-built-git-next-core')
    for name in builtins:
        copy(core,helper+'/git-'+name+'.exe','source-built-git-next-builtin-alias')
    for name in ('receive-pack', 'upload-pack', 'upload-archive'):
        for directory in (native + '/bin', helper):
            copy(core, directory + '/git-' + name + '.exe', 'source-built-git-next-builtin-alias')
    for name in receipt['binaries']:
        if name == 'git.exe':
            continue
        for directory in (native + '/bin', helper):
            copy(build / name, directory + '/' + name, 'source-built-git-next-helper')
    for protocol in ('https','ftp','ftps'):
        copy(build / 'git-remote-http.exe', helper + '/git-remote-' + protocol + '.exe', 'source-built-git-next-transport-alias')
    executable_dirs = {'bin', 'cmd', 'runtime/bin', 'runtime/cmd', native + '/bin', helper}
    iconv = assets / asset_receipt['native_prefix'] / 'bin/libiconv-2.dll'
    for directory in executable_dirs:
        copy(iconv, directory + '/libiconv-2.dll', 'pinned-gnu-iconv-runtime')
        for path in sorted((psl/'bin').glob('*.dll')):
            copy(path, directory + '/' + path.name, 'pinned-public-suffix-runtime')
        for path in crt_files:
            copy(path, directory + '/' + path.name, 'microsoft-vc-redistributable')
    # The vendor default is software configuration, never a user's config file.
    # The product defaults are spliced in from packaging/runtime-defaults/gitconfig so
    # the payload and the migration cannot disagree about a single value.
    config = (assets / 'etc/gitconfig').read_text(encoding='utf-8')
    core, tail = runtime_defaults.core_body(), runtime_defaults.tail()
    config, count = re.subn(r'(?m)^\s*autocrlf\s*=\s*true\s*$', lambda _: core, config)
    if count != 1:
        raise ValueError('Pinned vendor LF configuration anchor differs')
    config += '\n[http]\n\tsslBackend = schannel\n' + tail
    write('runtime/etc/gitconfig', config.encode('utf-8'), 'managed-product-lf-defaults')
    copy(ROOT / 'native-core/LICENSE.GPL-2.0', 'LICENSE', 'native-core-license')
    copy(ROOT / 'native-core/vendor/yyjson/LICENSE', 'LICENSES/yyjson/LICENSE', 'third-party-license')
    copy(ROOT / 'README.md', 'README.md', 'product-documentation')
    copy(ROOT / 'README.zh-CN.md', 'README.zh-CN.md', 'product-documentation')
    copy(ROOT / 'packaging/native-compatibility.json', 'compatibility.json', 'declared-native-compatibility')
    for folder in ('docs', 'native-core'):
        for path in ROOT.joinpath(folder).rglob('*'):
            if path.is_file() and '__pycache__' not in path.parts:
                copy(path, path.relative_to(ROOT).as_posix(), 'product-documentation-and-source-notices')
    copy(ROOT / 'AGENTS.md', 'AGENTS.md', 'engineering-documentation')
    # Git's ordinary `help <extension>` browser entrypoints remain discoverable.
    for command, page in [('language','language.html'),('eol','line-endings.html'),
                          ('doctor','takeover.html'),('compatibility','compatibility.html')]:
        relative = '../../../../../docs/site/' + page
        document = '<!doctype html><html lang="en"><meta charset="utf-8"><title>git-next ' + command + '</title><p><a href="' + html.escape(relative, quote=True) + '">git-next ' + command + ' manual</a></p></html>\n'
        write(native + '/share/doc/git-doc/git-' + command + '.html',document.encode('utf-8'),'product-help-entrypoint')
    # Installation/update integration is completed by the MSI stage, not here.
    for name in ('update.ps1','Update.Core.psm1','Scope.Core.ps1','Scope.Native.cs','Runtime.Configuration.ps1','messages.zh-CN.json'):
        copy(ROOT / 'packaging/windows' / name,'updater/' + name,'product-updater')
    copy(ROOT / 'packaging/windows/install-tls-provider.ps1','components/install-tls-provider.ps1','independent-component-installer')
    copy(ROOT / 'packaging/tls-providers.lock.json','components/tls-providers.lock.json','independent-component-catalog')
    for name in ('takeover.ps1','Takeover.Core.psm1'):
        copy(ROOT/'packaging/windows'/name,'migration/'+name,'product-migration')
    for name in bridge_receipt['binaries']:
        copy(bridge/name,'migration/'+name,'source-built-git-next-migration-bridge')
    copy(worker/'msi-worker.exe','updater/msi-worker.exe','source-built-no-console-msi-worker')
    copy(worker/'receipt.json','LICENSES/git-next-msi-worker/source.json','msi-worker-source-identity')
    aliases = [name for name, record in provenance.items() if record['kind'] == 'source-built-git-next-core']
    if any(provenance[name]['sha256'] != receipt['binaries']['git.exe']['sha256'] for name in aliases):
        raise ValueError('A Git entrypoint differs from the owned primary core')
    system_dlls = {path.name.lower() for path in Path(os.environ['SystemRoot']).joinpath('System32').glob('*.dll')}
    seen = set()

    def inspect(path):
        if path in seen:
            return
        seen.add(path)
        isa, dependencies = imports(path.read_bytes())
        if isa != expected_machine:
            raise ValueError('Owned native dependency architecture mismatch: '+path.name)
        for name in dependencies:
            if name in system_dlls or name.startswith(('api-ms-win-','ext-ms-win-')):
                continue
            sibling = path.parent/name
            if not sibling.is_file():
                raise ValueError('Missing owned native DLL beside '+path.name+': '+name)
            inspect(sibling)

    for name, record in provenance.items():
        if record['kind'].startswith('source-built-git-next') or record['kind'] == 'source-built-git-terminal':
            inspect(destination/name)
    # Verify every copied byte once; no executable, DLL or installer is loaded.
    if any(sha(destination / name) != record['sha256'] for name,record in provenance.items()):
        raise ValueError('Assembled distribution changed during preparation')
    if source_identity(source) != receipt['source_tree_sha256']:
        raise ValueError('Core source inputs changed during assembly')
    report = dict(schema_version=1,status='distribution-assembled-not-runtime-accepted',
                  product='git-next',version=receipt['product_version'],platform=platform,
                  engine='source-built-git-next-native-core',source_tree_sha256=receipt['source_tree_sha256'],
                  upstream_runtime_archive_sha256=asset_receipt['archive_sha256'],files=provenance,
                  core_entrypoints=aliases,upstream_git_core_deployed=False,
                  builtins=builtins,
                  native_import_images_inspected=len(seen),
                  runtime_configuration_defaults=len(default_configurations),
                  initialize_runtime_configuration_before_execution=True,
                  product_executed=False,tests_executed=False,installed=False,
                  complete_git_parity=compatibility['complete_git_parity'],release_eligible=False,
                  remaining=['D08 consolidated acceptance','D09 paired performance acceptance',
                             'D10 single release and workstation takeover'])
    if terminal_identity:
        report['git_terminal'] = {key:terminal_identity[key] for key in ('product','version','source_commit','platform','renderer')}
    with (destination / 'git-next-distribution.json').open('x',encoding='utf-8',newline='\n') as output:
        output.write(json.dumps(report,indent=2)+'\n')
    print(json.dumps({key:value for key,value in report.items() if key != 'files'}))


if __name__ == '__main__':
    main()
