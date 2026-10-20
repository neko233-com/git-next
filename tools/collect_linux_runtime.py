"""Collect an owned Linux SDK and ELF closure from a disposable producer environment."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import subprocess

from build_native_posix import image_platform
from posix_images import ELF_SYSTEM_LIBRARIES, elf_needed


def sha(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def safe(name):
    relative = PurePosixPath(name)
    if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts): raise ValueError('Unsafe runtime asset path')
    return relative


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',required=True,choices=['linux-x64','linux-arm64'])
    parser.add_argument('--destination',required=True,type=Path)
    parser.add_argument('--core-stage',required=True,type=Path)
    parser.add_argument('--tls-prefix',required=True,type=Path)
    parser.add_argument('--primary-helpers',required=True,type=Path)
    parser.add_argument('--base-image',required=True,help='official digest of this disposable producer image')
    args = parser.parse_args()
    expected = 'linux-arm64' if platform.machine().lower() in ('arm64','aarch64') else 'linux-x64'
    if os.name != 'posix' or platform.system() != 'Linux' or expected != args.platform: parser.error('Collect on the declared Linux producer architecture')
    if not args.base_image.startswith('debian@sha256:'): parser.error('Record the immutable official Debian producer image')
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Runtime SDK destination must be new')
    destination.mkdir(parents=True)
    core = args.core_stage.resolve(strict=True); tls = args.tls_prefix.resolve(strict=True)
    helper_root = args.primary_helpers.resolve(strict=True)
    helpers = json.loads((helper_root/'git-next-posix-helpers.json').read_text())
    if helpers['platform'] != args.platform: parser.error('Primary helper architecture differs from the SDK')
    files = {}; origins = {}; native = []; total = 0
    def copy(source,name,component,mode=None):
        nonlocal total
        safe(name)
        source = Path(source)
        if not source.is_file(): raise ValueError('Missing required runtime producer file: '+str(source))
        actual = sha(source)
        if name in files:
            if files[name].get('sha256') != actual: raise ValueError('Conflicting runtime asset: '+name)
            return
        total += source.stat().st_size
        if total > 1024*1024*1024 or len(files) >= 30000: raise ValueError('Runtime SDK exceeds its file/size limit')
        target = destination/name; target.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(source,target)
        permissions = mode if mode is not None else (0o755 if source.stat().st_mode & 0o111 else 0o644)
        files[name] = dict(kind='file',sha256=actual,bytes=source.stat().st_size,mode=permissions,component=component)
        origins[name] = str(source)
        machine = image_platform(source)
        if machine:
            if machine != args.platform: raise ValueError('Mixed native machine types in the SDK')
            native.append(source)
    def link(name,target,component):
        safe(name)
        if name in files: raise ValueError('Conflicting runtime link: '+name)
        files[name] = dict(kind='symlink',target=target,component=component)
    programs = {'bash':'/bin/bash','ssh':'/usr/bin/ssh','ssh-add':'/usr/bin/ssh-add','ssh-agent':'/usr/bin/ssh-agent',
                'ssh-keygen':'/usr/bin/ssh-keygen','scp':'/usr/bin/scp','sftp':'/usr/bin/sftp',
                'gpg':'/usr/bin/gpg','gpgv':'/usr/bin/gpgv','gpgsm':'/usr/bin/gpgsm','gpg-agent':'/usr/bin/gpg-agent',
                'gpgconf':'/usr/bin/gpgconf','dirmngr':'/usr/bin/dirmngr','pinentry-curses':'/usr/bin/pinentry-curses',
                'perl':'/usr/bin/perl','python3':'/usr/bin/python3','tclsh':'/usr/bin/tclsh','wish':'/usr/bin/wish'}
    utilities = ('sed','grep','cut','dirname','basename','find','sort','uniq','head','tail','tr','cat','wc','ls','cp','mv',
                 'rm','mkdir','rmdir','mktemp','env','xargs','readlink','realpath','touch','chmod','date','expr','uname',
                 'id','whoami','tee','install','du','od','sleep','timeout','yes','diff','patch','tar','gzip','less','file','awk')
    for name in utilities:
        source = shutil.which(name)
        if not source: raise ValueError('Missing runtime producer utility: '+name)
        programs[name] = source
    for name,source in programs.items(): copy(source,'usr/bin/'+name,'debian-native-runtime',0o755)
    link('usr/bin/sh','bash','owned-command-shell')
    link('usr/bin/pinentry','pinentry-curses','native-signing')
    for source in sorted(Path('/usr/lib/gnupg').glob('*')):
        if source.is_file(): copy(source,'usr/lib/gnupg/'+source.name,'debian-native-signing')
    # These are producer standard libraries, never HOME, keyrings or SSH profiles.
    data_roots = [Path('/usr/share/perl5'),Path('/usr/share/perl'),Path('/usr/share/tcltk'),Path('/usr/share/terminfo'),Path('/lib/terminfo')]
    data_roots += [path for path in Path('/usr/lib').glob('python3.*') if path.is_dir()]
    for triplet in Path('/usr/lib').glob('*-linux-gnu'):
        data_roots += [path for path in (triplet/'perl',triplet/'perl5') if path.is_dir()]
    for source_root in data_roots:
        relative_root = source_root.relative_to('/usr' if str(source_root).startswith('/usr/') else '/')
        target_root = 'usr/'+relative_root.as_posix() if str(source_root).startswith('/usr/') else relative_root.as_posix()
        for source in sorted(source_root.rglob('*')):
            relative = source.relative_to(source_root)
            if '__pycache__' in relative.parts or ('.github' in relative.parts and 'workflows' in relative.parts): continue
            if source.is_file(): copy(source,target_root+'/'+relative.as_posix(),'debian-interpreter-data')
    for name,item in helpers['files'].items():
        if item['kind'] == 'file':
            if sha(helper_root/name) != item['sha256']: raise ValueError('Primary helper inputs changed')
            copy(helper_root/name,name,'primary-'+item['component'],item['mode'])
        elif item['kind'] == 'symlink': link(name,item['target'],'primary-'+item['component'])
        else: raise ValueError('Unexpected primary helper asset type')
    cache = subprocess.check_output(['ldconfig','-p'],text=True)
    libraries = {}
    for line in cache.splitlines():
        match = re.match(r'\s*(\S+)\s+\([^)]*\)\s+=>\s+(\/\S+)',line)
        if match: libraries.setdefault(match[1],Path(match[2]))
    for path in sorted((tls/'lib').glob('*.so*')):
        if path.is_file(): libraries[path.name] = path
    # .NET single-file hosts discover these providers at runtime, not via DT_NEEDED.
    for name in ('libssl.so.3','libcrypto.so.3','libicuuc.so.72','libicui18n.so.72','libicudata.so.72',
                 'libsecret-1.so.0','libkrb5.so.3','libgssapi_krb5.so.2'):
        if name not in libraries: raise ValueError('Missing declared managed/provider runtime library: '+name)
        copy(libraries[name],'lib/'+name,'native-runtime-library')
    for path in core.rglob('*'):
        if path.is_file() and image_platform(path): native.append(path)
    seen = set(); index = 0
    while index < len(native):
        source = native[index]; index += 1
        key = source.resolve()
        if key in seen: continue
        seen.add(key)
        for name in elf_needed(source):
            if name in ELF_SYSTEM_LIBRARIES: continue
            if '/' in name or name not in libraries: raise ValueError('Missing explicit ELF dependency: '+name)
            copy(libraries[name],'lib/'+name,'native-runtime-library')
    packages = subprocess.check_output(['dpkg-query','-W','-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n'],text=True).splitlines()
    queries = set()
    for origin in origins.values():
        if not origin.startswith(('/usr/','/bin/','/lib/','/sbin/')): continue
        queries.add(origin)
        if origin.startswith('/usr/'): queries.add(origin[4:])
        else: queries.add('/usr'+origin)
    ownership = subprocess.run(['dpkg-query','-S',*sorted(queries)],capture_output=True,text=True)
    owners = sorted({line.split(': ',1)[0] for line in ownership.stdout.splitlines() if ': ' in line and re.fullmatch(r'[a-z0-9][a-z0-9+.-]*(?::(?:amd64|arm64|all))?',line.split(': ',1)[0])})
    for package in owners:
        name = package.split(':',1)[0]
        source = Path('/usr/share/doc')/name/'copyright'
        if source.is_file(): copy(source,'share/licenses/debian/'+name+'.copyright','producer-notice',0o644)
    for source in sorted((tls/'licenses').glob('*')):
        if source.is_file(): copy(source,'share/licenses/native-tls/'+source.name,'native-tls-producer-notice',0o644)
    # Restrict library search to this helper process and its children.
    launcher = '''#!/bin/sh
set -eu
self=$0
case "$self" in */*) ;; *) self=$(command -v "$self");; esac
base=${self%/*}
runtime=$(CDPATH= cd -- "$base/../.." && pwd -P)
LD_LIBRARY_PATH="$runtime/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export LD_LIBRARY_PATH
exec "$runtime/usr/lib/git-next/gcm/git-credential-manager" "$@"
'''.encode()
    name = 'usr/bin/git-credential-manager'
    target = destination/name; target.parent.mkdir(parents=True,exist_ok=True); target.write_bytes(launcher)
    files[name] = dict(kind='file',sha256=hashlib.sha256(launcher).hexdigest(),bytes=len(launcher),mode=0o755,component='owned-managed-helper-launcher')
    record = dict(schema_version=1,status='linux-runtime-collected-not-runtime-accepted',platform=args.platform,
                  producer=dict(base_image=args.base_image,packages=packages,source_file_ownership=ownership.stdout.splitlines(),
                                unresolved_file_ownership=ownership.stderr.splitlines()),
                  components=helpers['components'],files=files,source_paths=origins,
                  operating_system_libraries=sorted(ELF_SYSTEM_LIBRARIES),
                  platform_requirements=['glibc 2.36 or newer','normal POSIX kernel/loader and system services',
                                         'existing SSH/GnuPG agents and system configuration keep their OS paths'],
                  elf_images_inspected=len(seen),physical_symlinks_created=False,
                  corresponding_sources='required-before-release',product_executed=False,tests_executed=False,
                  installed=False,release_eligible=False)
    (destination/'git-next-posix-runtime.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'platform':args.platform,'files':len(files),'native_images':len(seen),'bytes':total,'product_executed':False}))


if __name__ == '__main__': main()
