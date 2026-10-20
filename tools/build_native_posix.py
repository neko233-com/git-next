"""Compile/stage full POSIX native cores without running product commands or tests."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import shlex
import struct
import subprocess
from native_product import product_version as native_version_value

from build_native_core import source_identity

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ('linux-x64','linux-arm64','macos-arm64')
PROGRAMS = ('git','git-remote-http','git-http-fetch','git-http-push','git-http-backend',
            'git-daemon','git-shell','scalar','git-imap-send','git-sh-i18n--envsubst')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def image_platform(path):
    with path.open('rb') as stream: data = stream.read(64)
    if data[:4] == b'\x7fELF' and data[4:6] == b'\x02\x01':
        return {62:'linux-x64',183:'linux-arm64'}.get(struct.unpack_from('<H',data,18)[0])
    if data[:4] == b'\xcf\xfa\xed\xfe' and struct.unpack_from('<I',data,4)[0] == 0x100000c:
        return 'macos-arm64'
    return None


def receipt_files(prefix, receipt):
    if not receipt.get('files'): raise ValueError('Dependency receipt contains no immutable inputs')
    for name,expected in receipt['files'].items():
        relative = PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts):
            raise ValueError('Unsafe dependency receipt path')
        path = prefix.joinpath(*relative.parts)
        if not path.resolve(strict=True).is_relative_to(prefix) or sha(path) != expected:
            raise ValueError('Dependency receipt does not identify current inputs: '+name)


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',choices=TARGETS,required=True)
    parser.add_argument('--source',type=Path,required=True,help='already integrated source tree')
    parser.add_argument('--build-dir',type=Path,required=True)
    parser.add_argument('--dependencies',type=Path,required=True)
    parser.add_argument('--stage',type=Path,required=True,help='new core-only staging prefix, not a live installation')
    parser.add_argument('--receipt',type=Path,required=True)
    parser.add_argument('--cmake',default='cmake')
    parser.add_argument('--shell',default='/bin/sh')
    parser.add_argument('--toolchain',type=Path)
    parser.add_argument('--generator',default='Ninja')
    parser.add_argument('--jobs',type=int,default=4,choices=range(1,17))
    args = parser.parse_args()
    if os.name != 'posix': parser.error('Run in a native POSIX build environment or owned Linux build container')
    native = ('macos' if platform.system() == 'Darwin' else 'linux') + ('-arm64' if platform.machine().lower() in ('arm64','aarch64') else '-x64')
    if args.platform != native and not args.toolchain: parser.error('A different target requires an explicit toolchain/SDK')
    source = args.source.resolve(strict=True); dependencies = args.dependencies.resolve(strict=True)
    build = args.build_dir.resolve(); stage = args.stage.resolve(); receipt_path = args.receipt.resolve()
    if stage.exists() or receipt_path.exists(): parser.error('Staging prefix and compilation receipt must be new')
    dependency = json.loads((dependencies/'receipt.json').read_text())
    if dependency.get('platform') != args.platform or dependency.get('tls_backend') != 'GnuTLS': parser.error('Native POSIX dependencies must identify the target and GnuTLS')
    if args.platform.startswith('macos-') and dependency.get('apple_sectrust') is not True: parser.error('macOS dependency receipt must enable Apple SecTrust')
    receipt_files(dependencies,dependency)
    prerequisite = Path(dependency['prerequisite_prefix']).resolve(strict=True)
    receipt_files(prerequisite,dependency['prerequisite'])
    pin = json.loads((ROOT/'packaging/native-core.lock.json').read_text())
    product_version = native_version_value(ROOT)
    compatibility_version = pin['tag'].removeprefix('v').split('.windows.',1)[0]+'.git-next.'+product_version
    before = source_identity(source)
    environment = dict(os.environ,GIT_VERSION=compatibility_version,GIT_BUILT_FROM_COMMIT=pin['commit'],
                       GIT_DATE='1970-01-01',SOURCE_DATE_EPOCH='0',GIT_CONFIG_NOSYSTEM='1',
                       GIT_CONFIG_GLOBAL=os.devnull,GIT_TERMINAL_PROMPT='0')
    environment['PKG_CONFIG_PATH'] = str(dependencies/'lib/pkgconfig')+os.pathsep+str(prerequisite/'lib/pkgconfig')+os.pathsep+environment.get('PKG_CONFIG_PATH','')
    cmake = shutil.which(args.cmake)
    if not cmake: parser.error('CMake is unavailable in this build environment')
    configure = [cmake,'-S',str(source/'contrib/buildsystems'),'-B',str(build),'-G',args.generator,
                 '-DCMAKE_BUILD_TYPE=Release','-DCMAKE_INSTALL_PREFIX='+str(stage),
                 '-DCMAKE_PREFIX_PATH='+str(dependencies)+';'+str(prerequisite),'-DSH_EXE='+args.shell,
                 '-DBUILD_TESTING=ON','-DUSE_VCPKG=OFF','-DCMAKE_C_FLAGS=-DPCRE2_STATIC',
                 '-DMSGFMT_EXE=MSGFMT_EXE-NOTFOUND']
    if args.toolchain: configure += ['-DCMAKE_TOOLCHAIN_FILE='+str(args.toolchain.resolve(strict=True))]
    if args.platform.startswith('macos-'):
        linker = subprocess.check_output(['pkg-config','--libs-only-L','--static','libcurl'],env=environment,text=True).strip()
        linker += ' -L'+shlex.quote(str(prerequisite/'lib'))
        configure += ['-DCMAKE_OSX_DEPLOYMENT_TARGET=15.5','-DCMAKE_OSX_ARCHITECTURES=arm64','-DCMAKE_EXE_LINKER_FLAGS='+linker]
    subprocess.run(configure,env=environment,check=True,cwd=ROOT)
    programs=list(PROGRAMS)
    if args.platform.startswith('macos-'): programs.append('git-credential-osxkeychain')
    command = [cmake,'--build',str(build),'--target',*programs,'shell-gen','perl-gen','python-gen','--parallel',str(args.jobs)]
    if args.generator == 'Ninja': command += ['--','-k','0']
    subprocess.run(command,env=environment,check=True,cwd=ROOT)
    header = build/'version-def.h'
    if '#define GIT_VERSION "'+compatibility_version+'"' not in header.read_text(): raise ValueError('Generated native version differs from this build')
    for name in programs:
        if image_platform(build/name) != args.platform: raise ValueError('Native output architecture differs from the target: '+name)
    # This only copies compiled files/scripts into a new owned staging prefix.
    subprocess.run([cmake,'--install',str(build),'--config','Release'],env=environment,check=True,cwd=ROOT)
    if args.platform.startswith('linux-'):
        helper=stage/'libexec/git-core/git-credential-libsecret'
        flags=shlex.split(subprocess.check_output(['pkg-config','--cflags','--libs','libsecret-1','glib-2.0'],env=environment,text=True))
        subprocess.run(['cc','-O2',str(source/'contrib/credential/libsecret/git-credential-libsecret.c'),'-o',str(helper),*flags],env=environment,check=True)
        if image_platform(helper)!=args.platform: raise ValueError('Credential helper ISA differs from the core')
        programs.append('git-credential-libsecret')
    if source_identity(source) != before: raise ValueError('Native sources changed during compilation')
    receipt_files(dependencies,dependency)
    files = {}
    for path in sorted(stage.rglob('*')):
        if path.is_symlink():
            if not path.resolve(strict=True).is_relative_to(stage): raise ValueError('Core staging link escapes its prefix')
            files[path.relative_to(stage).as_posix()] = dict(kind='symlink',target=os.readlink(path))
        elif path.is_file(): files[path.relative_to(stage).as_posix()] = dict(kind='file',sha256=sha(path),bytes=path.stat().st_size,mode=path.stat().st_mode & 0o777)
    receipt = dict(schema_version=1,status='posix-core-compiled-staged-not-runtime-accepted',platform=args.platform,
                   product_version=product_version,git_compatibility_version=compatibility_version,
                   source_tree_sha256=before,upstream_commit=pin['commit'],
                   dependency_receipt_sha256=sha(dependencies/'receipt.json'),generated_version_header_sha256=sha(header),
                   builder_source_sha256=sha(Path(__file__)),
                   binaries={name:dict(sha256=sha((stage/'libexec/git-core'/name) if name=='git-credential-libsecret' else build/name),bytes=((stage/'libexec/git-core'/name) if name=='git-credential-libsecret' else build/name).stat().st_size) for name in programs},
                   staged_files=files,configure_command=configure,build_command=command,
                   cmake_sha256=sha(Path(cmake)),build_shell_sha256=sha(Path(args.shell)),
                   utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),product_executed=False,tests_executed=False,
                   installed=False,staged=True,complete_git_parity=False,release_eligible=False,
                   remaining=['D02 owned POSIX helper/runtime closure','D06 updater integration','D07 complete packages/source-license delivery',
                              'D08 consolidated acceptance','D09 paired performance batch','D10 one release and workstation takeover'])
    receipt_path.parent.mkdir(parents=True,exist_ok=True)
    with receipt_path.open('x',encoding='utf-8',newline='\n') as output: output.write(json.dumps(receipt,indent=2)+'\n')
    print(json.dumps({'status':receipt['status'],'platform':args.platform,'staged_files':len(files),'tests_executed':False}))


if __name__ == '__main__': main()
