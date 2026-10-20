"""Collect declared macOS producer files and inspect their native dependency closure."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import struct
import subprocess
import sysconfig

from build_native_posix import image_platform
from posix_images import macho_needed, macho_system


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def output(*command):
    return subprocess.check_output(command, text=True).strip()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--destination', required=True, type=Path)
    parser.add_argument('--core-stage', required=True, type=Path)
    parser.add_argument('--primary-helpers', required=True, type=Path)
    args = parser.parse_args()
    if platform.system() != 'Darwin' or platform.machine() != 'arm64':
        parser.error('Collect on the declared native Apple Silicon producer')
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Runtime output must be new')
    core = args.core_stage.resolve(strict=True)
    helpers = args.primary_helpers.resolve(strict=True)
    helper_manifest = json.loads((helpers/'git-next-posix-helpers.json').read_text())
    if helper_manifest['platform'] != 'macos-arm64': parser.error('Wrong helper architecture')
    destination.mkdir(parents=True)
    files, origins, mappings, pending = {}, {}, {}, []
    total = 0

    def copy(source, name, component, mode=None):
        nonlocal total
        source = Path(source).resolve(strict=True)
        relative = PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts or '\\' in name or ':' in name:
            raise ValueError('Unsafe SDK member')
        if name in files:
            if origins[name]['sha256'] != sha(source): raise ValueError('Conflicting runtime member: '+name)
            return
        total += source.stat().st_size
        if total > 1536*1024*1024 or len(files) >= 40000: raise ValueError('Runtime SDK exceeds bounds')
        target = destination/name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
        if component == 'producer-source-recipe' and source.name == 'INSTALL_RECEIPT.json':
            value = json.loads(target.read_text())
            if isinstance(value.get('source'), dict) and 'path' in value['source']:
                value['source']['path'] = 'producer/Homebrew/'+source.parent.parent.name+'/'+source.parent.name
            target.write_text(json.dumps(value,indent=2)+'\n',encoding='utf-8',newline='\n')
        with target.open('rb') as stream: header = stream.read(32)
        if header[:4] in (b'\xca\xfe\xba\xbe', b'\xbe\xba\xfe\xca', b'\xca\xfe\xba\xbf', b'\xbf\xba\xfe\xca'):
            architectures = output('lipo', '-archs', str(source)).split()
            arch = 'arm64' if 'arm64' in architectures else 'arm64e' if 'arm64e' in architectures else None
            if not arch: raise ValueError('Producer has no ARM64 image: '+name)
            target.unlink()
            subprocess.run(['lipo', str(source), '-thin', arch, '-output', str(target)], check=True)
        machine = image_platform(target)
        if machine:
            if machine != 'macos-arm64': raise ValueError('Foreign runtime image: '+name)
            pending.append((source, target))
        permissions = mode if mode is not None else (0o755 if source.stat().st_mode & 0o111 else 0o644)
        target.chmod(permissions)
        files[name] = dict(kind='file', sha256=sha(target), bytes=target.stat().st_size,
                           mode=permissions, component=component)
        origin = str(source)
        if origin.startswith(str(Path.home())+'/'):
            origin = 'producer-home/'+source.relative_to(Path.home()).as_posix()
        origins[name] = dict(source=origin, sha256=sha(source))

    def link(name, target, component):
        if name in files: raise ValueError('Duplicate runtime link')
        files[name] = dict(kind='symlink', target=target, component=component)

    def tree(source_root, target_root, component):
        source_root = Path(source_root).resolve(strict=True)
        for source in sorted(source_root.rglob('*')):
            relative = source.relative_to(source_root)
            if '__pycache__' in relative.parts or 'site-packages' in relative.parts or '.github' in relative.parts:
                continue
            if source.is_file(): copy(source, target_root+'/'+relative.as_posix(), component)

    formulae = ('bash','python@3.14','tcl-tk@8','gnupg')
    prefixes = {name:Path(output('brew', '--prefix', name)).resolve(strict=True) for name in formulae}
    programs = {name:Path('/usr/bin')/name for name in ('ssh','ssh-add','ssh-agent','ssh-keygen','scp','sftp','perl')}
    programs['bash'] = prefixes['bash']/'bin/bash'
    programs['python3'] = Path(shutil.which('python3.14')).resolve(strict=True)
    for name in ('tclsh','wish'):
        choices = sorted((prefixes['tcl-tk@8']/'bin').glob(name+'*'))
        choices = [path for path in choices if path.is_file()]
        if not choices: raise ValueError('Tcl/Tk producer program missing')
        programs[name] = choices[0]
    for name in ('gpg','gpgv','gpgsm','gpg-agent','gpgconf','dirmngr'):
        programs[name] = prefixes['gnupg']/'bin'/name
    for name, source in programs.items(): copy(source, 'usr/bin/'+name, 'declared-native-producer', 0o755)
    link('usr/bin/sh', 'bash', 'owned-command-shell')
    for path in sorted((prefixes['gnupg']/'libexec').glob('*')):
        if path.is_file(): copy(path, 'usr/lib/gnupg/'+path.name, 'native-signing')
    pinentry = shutil.which('pinentry')
    if pinentry: copy(pinentry, 'usr/bin/pinentry', 'native-signing', 0o755)
    tree(sysconfig.get_path('stdlib'), 'usr/lib/python'+platform.python_version_tuple()[0]+'.'+platform.python_version_tuple()[1], 'python-standard-library')
    # Only Apple-distributed standard modules are collected; no user/vendor modules.
    for root in (Path('/System/Library/Perl'),):
        tree(root, 'usr/lib/perl', 'apple-perl-standard-library')
    for pattern in ('tcl8.*','tk8.*'):
        roots = [path for path in (prefixes['tcl-tk@8']/'lib').glob(pattern) if path.is_dir()]
        if len(roots) != 1: raise ValueError('Ambiguous Tcl/Tk standard library')
        tree(roots[0], 'usr/lib/'+roots[0].name, 'tcl-tk-standard-library')
    for name, item in helper_manifest['files'].items():
        if item['kind'] == 'file':
            if sha(helpers/name) != item['sha256']: raise ValueError('Primary helper input changed')
            copy(helpers/name, name, 'primary-'+item['component'], item['mode'])
        elif item['kind'] == 'symlink': link(name, item['target'], 'primary-'+item['component'])
        else: raise ValueError('Unknown helper member')
    # Producer lookup is bounded to already declared images and their load commands.
    for source in core.rglob('*'):
        if source.is_file() and image_platform(source): pending.append((source, source))
    seen, system_libraries = set(), set()
    index = 0
    while index < len(pending):
        source, image = pending[index]
        index += 1
        if source in seen: continue
        seen.add(source)
        rpaths = []
        load_commands = output('otool', '-l', str(image)).splitlines()
        for number, line in enumerate(load_commands):
            if line.strip() == 'cmd LC_RPATH':
                rpaths.append(load_commands[number+2].strip().split(' (offset ',1)[0].removeprefix('path '))
        for name in macho_needed(image):
            if macho_system(name):
                system_libraries.add(name)
                continue
            candidate = None
            if name.startswith('/'):
                candidate = Path(name)
            elif name.startswith(('@loader_path/','@executable_path/')):
                candidate = source.parent/name.split('/',1)[1]
            elif name.startswith('@rpath/'):
                for prefix in rpaths:
                    prefix = prefix.replace('@loader_path',str(source.parent)).replace('@executable_path',str(source.parent))
                    trial = Path(prefix)/name[len('@rpath/'):]
                    if trial.is_file(): candidate = trial; break
            if candidate is None or not candidate.is_file(): raise ValueError('Unresolved native producer dependency: '+name)
            candidate = candidate.resolve(strict=True)
            library = 'lib/'+candidate.name
            copy(candidate, library, 'native-runtime-library')
            if name in mappings and mappings[name] != library: raise ValueError('Ambiguous native dependency mapping')
            mappings[name] = library
    notice_roots = set(prefixes.values())
    for item in origins.values():
        source = Path(item['source'])
        parts = source.parts
        if '/Cellar/' in str(source):
            position = parts.index('Cellar')
            notice_roots.add(Path(*parts[:position+3]))
    for root in sorted(notice_roots):
        for path in [root/'INSTALL_RECEIPT.json',*sorted((root/'.brew').glob('*.rb'))]:
            if path.is_file():
                copy(path,'share/source-recipes/homebrew/'+root.parent.name+'/'+root.name+'/'+path.name,'producer-source-recipe',0o644)
        for path in root.rglob('*'):
            if path.is_file() and path.name.lower().startswith(('copying','license','notice','authors')):
                copy(path, 'share/licenses/homebrew/'+root.parent.name+'/'+root.name+'/'+path.relative_to(root).as_posix(), 'producer-notice', 0o644)
    info = json.loads(output('brew','info','--json=v2',*formulae))
    record = dict(schema_version=1,status='macos-runtime-collected-not-runtime-accepted',platform='macos-arm64',
                  producer=dict(os_version=output('/usr/bin/sw_vers','-productVersion'), formulae=info),
                  files=files,source_paths=origins,macho_library_map=mappings,
                  components=helper_manifest['components'],operating_system_libraries=sorted(system_libraries),
                  platform_requirements=['macOS 15.5 or newer on Apple Silicon',
                    'Apple system frameworks, POSIX utilities and OS-managed SSH/Keychain services',
                    'existing SSH/GnuPG profiles and agents retain their OS paths'],
                  native_images_inspected=len(seen),physical_symlinks_created=False,
                  corresponding_sources='required-before-release',product_executed=False,tests_executed=False,
                  installed=False,release_eligible=False)
    (destination/'git-next-posix-runtime.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(dict(status=record['status'],files=len(files),native_images=len(seen),product_executed=False)))


if __name__ == '__main__': main()
