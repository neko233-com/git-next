"""Build pinned C dependencies for owned cores; no product or test execution."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import subprocess
import tarfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ('windows-x64','windows-arm64','linux-x64','linux-arm64','macos-arm64')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_archive(component, cache):
    target = cache/component['archive']
    if not target.exists():
        temporary = target.with_suffix(target.suffix+'.new')
        request = urllib.request.Request(component['url'],headers={'User-Agent':'git-next-source-builder'})
        try:
            with urllib.request.urlopen(request,timeout=60) as source, temporary.open('xb') as output:
                total = 0
                while chunk := source.read(1024*1024):
                    total += len(chunk)
                    if total > component['bytes']: raise ValueError('Dependency source exceeds its pinned size')
                    output.write(chunk)
            if sha(temporary) != component['sha256']: raise ValueError('Dependency source digest differs from its pin')
            temporary.rename(target)
        finally:
            temporary.unlink(missing_ok=True)
    if target.stat().st_size != component['bytes'] or sha(target) != component['sha256']:
        raise ValueError('Dependency source differs from its lock: '+target.name)
    return target


def prepare_source(archive, component, destination):
    if destination.exists(): raise ValueError('Dependency source destination must be new')
    destination.mkdir(parents=True)
    skipped = []
    total = 0
    def member(name,data,mode=0o644):
        nonlocal total
        relative = PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts):
            raise ValueError('Unsafe dependency source member')
        if '.github' in relative.parts and 'workflows' in relative.parts:
            skipped.append(name); return
        if not relative.parts or relative.parts[0] != component['source_prefix']:
            raise ValueError('Dependency source root differs from its pin')
        total += len(data)
        if total > 256*1024*1024: raise ValueError('Dependency source extraction exceeds its limit')
        target = destination.joinpath(*relative.parts)
        target.parent.mkdir(parents=True,exist_ok=True)
        with target.open('xb') as output: output.write(data)
        if os.name != 'nt': target.chmod(mode & 0o777)
    if zipfile.is_zipfile(archive):
        with zipfile.ZipFile(archive) as source:
            for info in source.infolist():
                if info.is_dir(): continue
                if (info.external_attr >> 16) & 0o170000 == 0o120000: raise ValueError('Unexpected dependency ZIP symlink')
                member(info.filename,source.read(info),((info.external_attr >> 16) & 0o777) or 0o644)
    else:
        with tarfile.open(archive) as source:
            for info in source:
                if info.isdir(): continue
                if not info.isfile(): raise ValueError('Unexpected dependency source link or device')
                with source.extractfile(info) as value: member(info.name,value.read(),info.mode)
    return destination/component['source_prefix'], skipped


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',required=True,choices=TARGETS)
    parser.add_argument('--work-dir',required=True,type=Path,help='new source/build workspace')
    parser.add_argument('--cache',required=True,type=Path)
    parser.add_argument('--prefix',required=True,type=Path,help='new dependency installation prefix')
    parser.add_argument('--cmake',default='cmake')
    parser.add_argument('--generator')
    parser.add_argument('--make-program',type=Path)
    parser.add_argument('--toolchain',type=Path)
    parser.add_argument('--psl',type=Path,help='Windows native libpsl import prefix')
    parser.add_argument('--tls-prefix',type=Path,help='POSIX GnuTLS/libpsl dependency prefix with receipt.json')
    parser.add_argument('--jobs',type=int,default=4,choices=range(1,17))
    args = parser.parse_args()
    windows = args.platform.startswith('windows-')
    macos = args.platform.startswith('macos-')
    if windows != (os.name == 'nt') and not args.toolchain: parser.error('Cross-compilation requires an explicit native toolchain file')
    if macos and platform.system() != 'Darwin' and not args.toolchain: parser.error('macOS requires a Mac or an explicit Apple SDK/toolchain')
    work = args.work_dir.resolve(); prefix = args.prefix.resolve(); cache = args.cache.resolve()
    if work.exists() or prefix.exists(): parser.error('Build workspace and installation prefix must be new')
    lock = json.loads((ROOT/'packaging/native-dependencies.lock.json').read_text())
    tls = None
    if windows:
        if not args.psl: parser.error('Windows requires the pinned libpsl import prefix')
        psl = args.psl.resolve(strict=True)
        tls = json.loads((psl/'receipt.json').read_text())
        if tls['platform'] != args.platform or sha(psl/'lib/psl.lib') != tls['import_library_sha256']: parser.error('libpsl inputs do not match the target receipt')
    else:
        if not args.tls_prefix: parser.error('POSIX requires a recorded GnuTLS/libpsl prefix')
        tls_prefix = args.tls_prefix.resolve(strict=True)
        tls = json.loads((tls_prefix/'receipt.json').read_text())
        if tls.get('platform') != args.platform or tls.get('tls_backend') != 'GnuTLS' or not tls.get('files'):
            parser.error('POSIX TLS prerequisite does not identify this target and GnuTLS')
        for name,digest in tls['files'].items():
            relative = PurePosixPath(name)
            if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts) or sha(tls_prefix.joinpath(*relative.parts)) != digest:
                parser.error('POSIX TLS prerequisite differs from its receipt')
    cache.mkdir(parents=True,exist_ok=True); work.mkdir(parents=True); prefix.mkdir(parents=True)
    environment = dict(os.environ,GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL=os.devnull,GIT_TERMINAL_PROMPT='0',
                       SOURCE_DATE_EPOCH='0',GIT_DATE='1970-01-01',MSBUILDDISABLENODEREUSE='1')
    if not windows:
        environment['PKG_CONFIG_PATH'] = str(prefix/'lib/pkgconfig') + os.pathsep + str(tls_prefix/'lib/pkgconfig') + os.pathsep + environment.get('PKG_CONFIG_PATH','')
    recipes = [
        ('zlib',['-DZLIB_BUILD_SHARED=OFF','-DZLIB_BUILD_STATIC=ON','-DZLIB_BUILD_TESTING=OFF']),
        ('expat',['-DEXPAT_BUILD_TOOLS=OFF','-DEXPAT_BUILD_EXAMPLES=OFF','-DEXPAT_BUILD_TESTS=OFF','-DEXPAT_SHARED_LIBS=OFF']),
        ('pcre2',['-DBUILD_SHARED_LIBS=OFF','-DPCRE2_BUILD_PCRE2GREP=OFF','-DPCRE2_BUILD_TESTS=OFF','-DPCRE2_BUILD_PCRE2_16=OFF','-DPCRE2_BUILD_PCRE2_32=OFF','-DPCRE2_SUPPORT_JIT=ON']),
        ('nghttp2',['-DBUILD_SHARED_LIBS=OFF','-DBUILD_STATIC_LIBS=ON','-DENABLE_LIB_ONLY=ON','-DENABLE_STATIC_CRT=OFF']),
        ('curl',['-DBUILD_CURL_EXE=OFF','-DBUILD_SHARED_LIBS=OFF','-DBUILD_STATIC_LIBS=ON','-DBUILD_TESTING=OFF',
                 '-DCURL_USE_OPENSSL=OFF','-DCURL_USE_SCHANNEL='+('ON' if windows else 'OFF'),
                 '-DCURL_USE_GNUTLS='+('OFF' if windows else 'ON'),'-DCURL_USE_LIBPSL=ON','-DUSE_NGHTTP2=ON',
                 '-DNGHTTP2_USE_STATIC_LIBS=ON','-DCURL_USE_LIBSSH2=OFF','-DCURL_USE_LIBSSH=OFF',
                 '-DUSE_WIN32_IDN='+('ON' if windows else 'OFF'),'-DUSE_LIBIDN2='+('OFF' if windows else 'ON'),
                 '-DCURL_USE_GSSAPI='+('OFF' if windows else 'ON'),'-DCURL_ZLIB=ON','-DZLIB_USE_STATIC_LIBS=ON',
                 '-DUSE_APPLE_SECTRUST='+('ON' if macos else 'OFF')])]
    builds = []; excluded = []
    for name,options in recipes:
        component = lock['components'][name]
        archive = source_archive(component,cache)
        source,skipped = prepare_source(archive,component,work/('source-'+name)); excluded.extend(skipped)
        build = work/('build-'+name)
        command = [args.cmake,'-S',str(source),'-B',str(build),'-DCMAKE_BUILD_TYPE=Release',
                   '-DCMAKE_INSTALL_PREFIX='+str(prefix),'-DCMAKE_POSITION_INDEPENDENT_CODE=ON',
                   '-DCMAKE_PREFIX_PATH='+prefix.as_posix()+';'+(psl.as_posix() if windows else tls_prefix.as_posix()),*options]
        if args.generator: command += ['-G',args.generator]
        elif windows and not args.toolchain: command += ['-G','Visual Studio 18 2026','-A','ARM64' if args.platform.endswith('arm64') else 'x64']
        if args.toolchain: command += ['-DCMAKE_TOOLCHAIN_FILE='+str(args.toolchain.resolve(strict=True))]
        if args.make_program: command += ['-DCMAKE_MAKE_PROGRAM='+str(args.make_program.resolve(strict=True))]
        if name == 'curl' and windows:
            command += ['-DLIBPSL_INCLUDE_DIR='+str(psl/'include'),'-DLIBPSL_LIBRARY='+str(psl/'lib/psl.lib')]
        if name == 'curl' and macos: command += ['-DCMAKE_OSX_DEPLOYMENT_TARGET=14.0','-DCURL_GSS_FLAVOR=Apple']
        subprocess.run(command,env=environment,check=True)
        subprocess.run([args.cmake,'--build',str(build),'--config','Release','--target','install','--parallel',str(args.jobs)],env=environment,check=True)
        if name == 'curl':
            config = (build/'lib/curl_config.h').read_text()
            features = ['USE_NGHTTP2','USE_LIBPSL','USE_SCHANNEL' if windows else 'USE_GNUTLS',
                        'USE_WIN32_IDN' if windows else 'HAVE_LIBIDN2']
            if not windows: features.append('HAVE_GSSAPI')
            if macos: features += ['USE_APPLE_SECTRUST','HAVE_GSSAPPLE']
            for feature in features:
                if '#define '+feature+' 1' not in config: raise ValueError('Required curl feature was not compiled: '+feature)
        builds.append(dict(name=name,source=component,configure=command))
    files = {path.relative_to(prefix).as_posix():sha(path) for path in sorted(prefix.rglob('*')) if path.is_file()}
    receipt = dict(schema_version=1,status='dependencies-compiled-not-runtime-accepted',platform=args.platform,
                   tls_backend='Schannel' if windows else 'GnuTLS',apple_sectrust=macos,
                   lock_sha256=sha(ROOT/'packaging/native-dependencies.lock.json'),recipes=builds,
                   prerequisite=tls,files=files,excluded_workflows=excluded,
                   prerequisite_prefix=str(psl if windows else tls_prefix),
                   utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),product_executed=False,tests_executed=False,
                   installed_into_product=False,release_eligible=False)
    (prefix/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'status':receipt['status'],'platform':args.platform,'files':len(files),'tests_executed':False}))


if __name__ == '__main__': main()
