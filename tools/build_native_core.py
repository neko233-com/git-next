"""Compile integrated Windows native core inputs; never execute product/tests/installers."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
from native_product import product_version

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_identity(source):
    original = json.loads((source / 'git-next-source-receipt.json').read_text())
    integrated = json.loads((source / 'git-next-integration-receipt.json').read_text())
    names = set(original['files']) | set(integrated['overlay'])
    names.update(['git-next-catalog.h', 'git-next-identity.h'])
    values = {name:digest(source / name) for name in sorted(names)}
    return hashlib.sha256(json.dumps(values, sort_keys=True).encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform', choices=('windows-x64','windows-arm64'), default='windows-x64')
    parser.add_argument('--prepared-source', action='store_true', help='use the frozen integrated tree; do not mutate shared build inputs')
    parser.add_argument('--toolchain',type=Path,help='explicit isolated LLVM-MinGW target toolchain')
    parser.add_argument('--make-program',type=Path)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--build-dir', type=Path, required=True)
    parser.add_argument('--dependencies', type=Path, required=True)
    parser.add_argument('--iconv', type=Path, required=True, help='owned GNU iconv import prefix prepared from the pinned runtime')
    parser.add_argument('--psl', type=Path, required=True, help='owned libpsl imports for the native curl dependency')
    parser.add_argument('--build-shell', type=Path, required=True, help='build-time shell only; not a runtime dependency')
    parser.add_argument('--cmake', type=Path, required=True)
    parser.add_argument('--receipt', type=Path, required=True, help='new compilation receipt; never overwrite accepted evidence')
    parser.add_argument('--jobs', type=int, default=4, choices=range(1,17))
    args = parser.parse_args()
    if os.name != 'nt':
        parser.error('This build adapter requires native Windows MSVC tools.')
    if args.receipt.exists():
        parser.error('Compilation receipt already exists.')
    source = args.source.resolve(strict=True)
    build = args.build_dir.resolve()
    dependencies = args.dependencies.resolve(strict=True)
    shell = args.build_shell.resolve(strict=True)
    cmake = args.cmake.resolve(strict=True)
    psl = args.psl.resolve(strict=True)
    psl_receipt = json.loads((psl/'receipt.json').read_text())
    if psl_receipt['platform'] != args.platform or digest(psl/'lib/psl.lib') != psl_receipt['import_library_sha256']:
        parser.error('libpsl import prefix does not identify the requested target dependency.')
    if not args.prepared_source:
        subprocess.run([os.sys.executable, str(ROOT / 'tools/integrate_native_core.py'),
                        '--source', str(source)], check=True, cwd=ROOT)
    version = product_version(ROOT)
    pin = json.loads((ROOT / 'packaging/native-core.lock.json').read_text())
    compatibility_version = pin['tag'].removeprefix('v') + '.git-next.' + version
    environment = dict(os.environ, GIT_VERSION=compatibility_version,
                       GIT_BUILT_FROM_COMMIT=pin['commit'], GIT_CONFIG_NOSYSTEM='1',
                       GIT_DATE='1970-01-01', SOURCE_DATE_EPOCH='0',MSBUILDDISABLENODEREUSE='1',
                       GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0',
                       PATH=str(shell.parent) + os.pathsep + os.environ.get('PATH', ''))
    before = source_identity(source)
    llvm = args.toolchain is not None
    if llvm and args.platform != 'windows-arm64': parser.error('The isolated LLVM adapter currently targets Windows ARM64')
    configured = [str(cmake), '-S', str(source / 'contrib/buildsystems'), '-B', str(build),
                  '-DUSE_VCPKG=OFF',
                  '-DCMAKE_PREFIX_PATH=' + dependencies.as_posix() + ';' + psl.as_posix(),
                  '-DLIBPSL_INCLUDE_DIR=' + (psl/'include').as_posix(),
                  '-DLIBPSL_LIBRARY=' + (psl/'lib/psl.lib').as_posix(),
                  '-DNGHTTP2_USE_STATIC_LIBS:BOOL=ON',
                  '-DZLIB_LIBRARY=' + (dependencies / ('lib/libzs.a' if llvm else 'lib/zs.lib')).as_posix(),
                  '-DSH_EXE=' + shell.as_posix(),
                  '-DCMAKE_C_FLAGS='+('-DCURL_STATICLIB -DXML_STATIC -DUSE_LIBPCRE2 -DPCRE2_STATIC' if llvm else '/DCURL_STATICLIB /DXML_STATIC /DUSE_LIBPCRE2 /DPCRE2_STATIC'),
                  '-DBUILD_TESTING=ON', '-DPCRE2_FOUND=ON',
                  '-DPCRE2_LIBRARIES=' + (dependencies / ('lib/libpcre2-8.a' if llvm else 'lib/pcre2-8-static.lib')).as_posix(),
                  '-DCMAKE_INSTALL_PREFIX=' + (build.parent / 'runtime').as_posix()]
    if llvm:
        configured += ['-G','Ninja','-DCMAKE_BUILD_TYPE=Release','-DCMAKE_TOOLCHAIN_FILE='+str(args.toolchain.resolve(strict=True))]
        if not args.make_program: parser.error('The isolated LLVM build requires an explicit Ninja executable')
        configured += ['-DCMAKE_MAKE_PROGRAM='+str(args.make_program.resolve(strict=True))]
        llvm_root=Path(os.environ['GIT_NEXT_LLVM_MINGW_ROOT']).resolve(strict=True)
        configured += ['-DWINDRES_EXE='+str(llvm_root/'bin/aarch64-w64-mingw32-windres.exe')]
        environment['CPATH'] = str(source) + (os.pathsep+environment['CPATH'] if environment.get('CPATH') else '')
    else: configured += ['-G','Visual Studio 18 2026','-A','ARM64' if args.platform.endswith('arm64') else 'x64']
    if args.iconv:
        iconv = args.iconv.resolve(strict=True)
        iconv_receipt = json.loads((iconv / 'receipt.json').read_text())
        if iconv_receipt['platform'] != args.platform:
            parser.error('GNU iconv imports do not identify the requested target.')
        if digest(iconv / 'lib/iconv.lib') != iconv_receipt['import_library_sha256']:
            parser.error('GNU iconv import library differs from its receipt.')
        configured += ['-DIconv_INCLUDE_DIR=' + (iconv / 'include').as_posix(),
                       '-DIconv_LIBRARY=' + (iconv / 'lib/iconv.lib').as_posix(),
                       '-DIconv_IS_BUILT_IN=OFF', '-DHAVE_NEW_ICONV:BOOL=ON',
                       '-DICONV_DOESNOT_OMIT_BOM:BOOL=ON']
    # Upstream configuration needs BUILD_TESTING for generated build options, but
    # only production targets are built and no CTest or product command is run.
    targets = ['git', 'git-remote-http', 'git-http-fetch', 'git-http-push',
               'git-http-backend', 'git-daemon', 'git-shell', 'scalar',
               'git-imap-send', 'git-sh-i18n--envsubst']
    command = [str(cmake), '--build', str(build), '--config', 'Release',
               '--target', *targets, '--parallel', str(args.jobs)]
    if llvm: command += ['--','-k','0']
    subprocess.run(configured, env=environment, check=True, cwd=ROOT)
    subprocess.run(command, env=environment, check=True, cwd=ROOT)
    generated_version = (build / 'version-def.h').read_text(encoding='utf-8')
    if '#define GIT_VERSION "' + compatibility_version + '"' not in generated_version:
        raise RuntimeError('Generated version header does not identify the requested native compatibility version')
    if source_identity(source) != before:
        raise RuntimeError('Native source inputs changed during compilation')
    binaries = {}
    for name in targets:
        path = build / (name + '.exe')
        data = path.read_bytes()
        pe = struct.unpack_from('<I',data,0x3c)[0]
        if data[:2] != b'MZ' or data[pe:pe+4] != b'PE\0\0' or struct.unpack_from('<H',data,pe+4)[0] != (0xaa64 if args.platform.endswith('arm64') else 0x8664):
            raise RuntimeError('Compiled image does not match the requested target: '+name)
        binaries[path.name] = dict(sha256=digest(path), bytes=path.stat().st_size)
    receipt = dict(schema_version=1, status='integrated-core-compiled-not-runtime-accepted',
                   utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                   source_tree_sha256=before, upstream_commit=pin['commit'], platform=args.platform,
                   product_version=version, binaries=binaries,
                   git_compatibility_version=compatibility_version,
                   generated_version_header_sha256=digest(build / 'version-def.h'),
                   cmake_sha256=digest(cmake), build_shell_sha256=digest(shell),
                   dependency_libraries={path.name:digest(path) for path in (dependencies / 'lib').iterdir() if path.suffix in ('.lib','.a')},
                   public_suffix_dependency=psl_receipt,
                   configure_command=configured, build_command=command,
                   product_executed=False, tests_executed=False, installed=False,
                   complete_git_parity=False, release_eligible=False,
                   remaining='Complete owned runtime/iconv, installer/update/platform packages and consolidated acceptance')
    args.receipt.parent.mkdir(parents=True, exist_ok=True)
    with args.receipt.open('x', encoding='utf-8', newline='\n') as output:
        output.write(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({key:receipt[key] for key in ('status','source_tree_sha256','product_executed','tests_executed')}))


if __name__ == '__main__':
    main()
