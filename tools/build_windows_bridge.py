"""Compile CRT-free console/GUI legacy bridges; never run the output."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import re
from native_product import product_version

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--compiler',type=Path)
    parser.add_argument('--sdk',type=Path)
    parser.add_argument('--sdk-version')
    parser.add_argument('--msvc',type=Path)
    parser.add_argument('--llvm-root',type=Path,help='isolated LLVM-MinGW compiler for ARM64')
    parser.add_argument('--platform',choices=['windows-x64','windows-arm64'],default='windows-x64')
    parser.add_argument('--destination',type=Path,required=True)
    args = parser.parse_args()
    output = args.destination.resolve()
    if output.exists():
        parser.error('Bridge output directory must not exist.')
    source = ROOT/'native-core/launcher/windows-bridge.c'
    digest = hashlib.sha256(source.read_bytes()).hexdigest()
    resource = ROOT/'native-core/launcher/windows-bridge.rc.in'
    resource_sha = hashlib.sha256(resource.read_bytes()).hexdigest()
    version = product_version(ROOT)
    match = re.fullmatch(r'([0-9]+)\.([0-9]+)\.([0-9]+)(?:-[A-Za-z0-9.-]+)?',version)
    if not match:
        parser.error('Invalid resource version.')
    architecture = 'x64' if args.platform == 'windows-x64' else 'arm64'
    llvm = args.llvm_root.resolve(strict=True) if args.llvm_root else None
    if llvm:
        if args.platform != 'windows-arm64': parser.error('LLVM bridge adapter targets ARM64')
        compiler = llvm/'bin/aarch64-w64-mingw32-clang.exe'
    else:
        if not all((args.compiler,args.sdk,args.sdk_version,args.msvc)): parser.error('Explicit MSVC and Windows SDK inputs are required')
        compiler = args.compiler.resolve(strict=True)
        sdk = args.sdk.resolve(strict=True)
        msvc = args.msvc.resolve(strict=True)
        includes = [msvc/'include',*(sdk/'Include'/args.sdk_version/name for name in ['um','shared','ucrt'])]
        libraries = [msvc/'lib'/architecture, sdk/'Lib'/args.sdk_version/'um'/architecture]
        if not all(path.is_dir() for path in includes+libraries):
            parser.error('The matching MSVC and Windows SDK architecture is required.')
    output.mkdir(parents=True)
    resource_file = output/'bridge.rc'
    resource_file.write_text(resource.read_text().replace('@FILE_VERSION@',','.join(match.groups())+',0').replace('@PRODUCT_VERSION@',version),encoding='utf-8',newline='\n')
    resource_object = output/('bridge.o' if llvm else 'bridge.res')
    if llvm:
        resource_command = [str(llvm/'bin/aarch64-w64-mingw32-windres.exe'),'-I',str(llvm/'aarch64-w64-mingw32/include'),str(resource_file),str(resource_object)]
    else:
        resource_compiler = sdk/'bin'/args.sdk_version/'x64/rc.exe'
        resource_command = [str(resource_compiler.resolve(strict=True)),'/nologo',
                        *(argument for path in includes for argument in ['/i',str(path)]),
                        '/fo',str(resource_object),str(resource_file)]
    subprocess.run(resource_command,check=True,cwd=ROOT)
    commands, files = [], {}
    for gui in [False,True]:
        name = 'git-next-bridge-gui.exe' if gui else 'git-next-bridge.exe'
        if llvm:
            command = [str(compiler),'-O2','-fno-stack-protector','-ffreestanding','-nostdlib','-Wall','-Wextra','-Werror',
                       '-DUNICODE','-D_UNICODE','-DWINVER=0x0A00','-D_WIN32_WINNT=0x0A00']
            if gui: command += ['-DGIT_NEXT_GUI_BRIDGE']
            command += [str(source),str(resource_object),'-o',str(output/name),'-Wl,--entry,bridge_entry',
                        '-Wl,--subsystem,'+('windows' if gui else 'console'),'-Wl,--dynamicbase','-Wl,--nxcompat',
                        '-lkernel32','-ladvapi32','-luser32']
        else:
            command = [str(compiler),'/nologo','/O2','/GS-','/utf-8','/TC','/Zl','/W4','/WX',
                   *(('/D'+name) for name in ['UNICODE','_UNICODE','WINVER=0x0A00','_WIN32_WINNT=0x0A00']),
                   *(('/I'+str(path)) for path in includes),'/Fo'+str(output/(name+'.obj')),
                   '/Fe'+str(output/name)]
            if gui:
                command += ['/DGIT_NEXT_GUI_BRIDGE']
            command += [str(source),'/link',str(resource_object),'/NODEFAULTLIB','/ENTRY:bridge_entry',
                    '/SUBSYSTEM:'+('WINDOWS' if gui else 'CONSOLE'),'/DYNAMICBASE','/NXCOMPAT','/HIGHENTROPYVA',
                    *(('/LIBPATH:'+str(path)) for path in libraries),'kernel32.lib','advapi32.lib','user32.lib']
        subprocess.run(command,check=True,cwd=ROOT)
        commands.append(command)
        path = output/name
        files[name] = dict(sha256=hashlib.sha256(path.read_bytes()).hexdigest(),bytes=path.stat().st_size)
    if hashlib.sha256(source.read_bytes()).hexdigest() != digest or hashlib.sha256(resource.read_bytes()).hexdigest() != resource_sha:
        raise RuntimeError('Bridge source changed during compilation.')
    receipt = dict(schema_version=1,status='bridge-compiled-not-runtime-accepted',platform=args.platform,
                   source_sha256=digest,binaries=files,compile_commands=commands,product_executed=False,
                   resource_sha256=resource_sha,product_version=version,resource_command=resource_command,
                   tests_executed=False,installed=False,release_eligible=False)
    (output/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({key:value for key,value in receipt.items() if key != 'compile_commands'}))


if __name__ == '__main__':
    main()
