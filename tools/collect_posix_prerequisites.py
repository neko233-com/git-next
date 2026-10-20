"""Snapshot native GnuTLS/libpsl build inputs; never load libraries or run the product."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess


def output(*command):
    return subprocess.check_output(command,text=True).strip()


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',required=True,choices=['linux-x64','linux-arm64','macos-arm64'])
    parser.add_argument('--destination',required=True,type=Path)
    parser.add_argument('--base-image',help='official pinned Linux builder image digest')
    args = parser.parse_args()
    native = ('macos' if platform.system() == 'Darwin' else 'linux')+('-arm64' if platform.machine().lower() in ('arm64','aarch64') else '-x64')
    if native != args.platform: parser.error('Collect prerequisites on the target build OS/architecture')
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Prerequisite prefix must be new')
    destination.mkdir(parents=True)
    components = {}; provenance = {}
    requested = [('gnutls','gnutls',['gnutls']),('libpsl','psl',['libpsl.h']),('libidn2','idn2',['idn2.h'])]
    if args.platform.startswith('linux-'): requested.append(('mit-krb5-gssapi','gssapi_krb5',['gssapi','gssapi.h','krb5','krb5.h']))
    for name,library,headers in requested:
        libdir = Path(output('pkg-config','--variable=libdir',name)).resolve(strict=True)
        include = Path(output('pkg-config','--variable=includedir',name)).resolve(strict=True)
        pcdir = Path(output('pkg-config','--variable=pcfiledir',name)).resolve(strict=True)
        version = output('pkg-config','--modversion',name)
        libraries = sorted({path for pattern in ('lib'+library+'.so*','lib'+library+'*.dylib','lib'+library+'.a') for path in libdir.glob(pattern) if path.is_file()})
        if not libraries: raise ValueError('Native prerequisite libraries are missing: '+name)
        for source in libraries:
            target = destination/'lib'/source.name
            target.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(source,target)
            provenance[target.relative_to(destination).as_posix()] = dict(source=str(source.resolve()),sha256=sha(source))
        for item in headers:
            source = include/item
            paths = [source] if source.is_file() else sorted(path for path in source.rglob('*') if path.is_file())
            for path in paths:
                relative = path.relative_to(include)
                if '.github' in relative.parts and 'workflows' in relative.parts: continue
                target = destination/'include'/relative
                target.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(path,target)
        pc = (pcdir/(name+'.pc')).read_text()
        lines = []
        for line in pc.splitlines():
            key = line.split('=',1)[0]
            if key == 'prefix': line = 'prefix='+destination.as_posix()
            elif key == 'exec_prefix': line = 'exec_prefix=${prefix}'
            elif key == 'libdir': line = 'libdir=${prefix}/lib'
            elif key == 'includedir': line = 'includedir=${prefix}/include'
            lines.append(line)
        target = destination/'lib/pkgconfig'/(name+'.pc')
        target.parent.mkdir(parents=True,exist_ok=True); target.write_text('\n'.join(lines)+'\n')
        components[name] = dict(version=version,pkg_config_sha256=sha(pcdir/(name+'.pc')))
    if args.platform.startswith('linux-'):
        packages = output('dpkg-query','-W','-f=${binary:Package}\t${Version}\t${source:Package}\t${source:Version}\n',
                          'libgnutls28-dev','libgnutls30','libpsl-dev','libpsl5','libidn2-dev','libidn2-0','libkrb5-dev','libgssapi-krb5-2')
        for name in ('libgnutls30','libgnutls28-dev','libpsl5','libpsl-dev','libidn2-0','libidn2-dev','libkrb5-dev','libgssapi-krb5-2'):
            source = Path('/usr/share/doc')/name/'copyright'
            if source.is_file():
                target = destination/'licenses'/(name+'.copyright'); target.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(source,target)
        producer = dict(kind='debian-installed-build-prerequisites',base_image=args.base_image,packages=packages.splitlines(),
                        corresponding_source_status='required-before-release',source_service='https://deb.debian.org/debian/')
    else:
        info = json.loads(output('brew','info','--json=v2','gnutls','libpsl','libidn2'))
        components['gssapi'] = dict(provider='Apple system GSS.framework')
        producer = dict(kind='homebrew-native-build-prerequisites',formulae=info,
                        corresponding_source_status='required-before-release')
    files = {path.relative_to(destination).as_posix():sha(path) for path in sorted(destination.rglob('*')) if path.is_file()}
    record = dict(schema_version=1,status='native-prerequisite-inputs-collected-not-runtime-accepted',platform=args.platform,
                  tls_backend='GnuTLS',components=components,producer=producer,library_provenance=provenance,
                  files=files,product_executed=False,tests_executed=False,release_eligible=False)
    (destination/'receipt.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'platform':args.platform,'files':len(files),'tls_backend':'GnuTLS','product_executed':False}))


if __name__ == '__main__': main()
