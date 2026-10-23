"""Compile CRT-free console/GUI legacy bridges; never run the output.

MSVC 14.44 at full /O2 emits memcpy, memset, memmove and memcmp. Those symbols
come from native-core/launcher/freestanding-memory.c. Do not pass the inline
limit that hides the calls: it also disables inlining of the forwarder.
"""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import re
from native_product import product_version

ROOT = Path(__file__).resolve().parents[1]
OPTIMIZER = 'O2'
OB1_WORKAROUND = False
MINGW_FLAGS = ['-O2','-fno-stack-protector','-ffreestanding','-nostdlib','-Wall','-Wextra','-Werror',
               '-DUNICODE','-D_UNICODE','-DWINVER=0x0A00','-D_WIN32_WINNT=0x0A00']
MSVC_COMPILE = ['/nologo','/O2','/GS-','/utf-8','/TC','/Zl','/W4','/WX']


def reject_reduced_inlining(command):
    """Ob1 is the flag that hides compiler helper calls by turning inlining off."""
    if any('Ob1' in str(arg) for arg in command):
        raise RuntimeError('Reduced inlining is not the CRT-free link fix.')


def required_imports(name):
    libraries = {'kernel32.dll','advapi32.dll'}
    if 'gui' in name.lower():
        libraries.add('user32.dll')
    return libraries


def pe_imports(data):
    if data[:2] != b'MZ':
        raise ValueError('Bridge is not a PE image.')
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if data[pe:pe + 4] != b'PE\0\0':
        raise ValueError('Invalid bridge PE signature.')
    machine, sections, _, _, _, optional_size, _ = struct.unpack_from('<HHIIIHH', data, pe + 4)
    optional = pe + 24
    if struct.unpack_from('<H', data, optional)[0] != 0x20b:
        raise ValueError('Bridge must be a 64-bit PE image.')
    table = []
    for index in range(sections):
        size, address, raw_size, raw = struct.unpack_from('<IIII', data, optional + optional_size + 40 * index + 8)
        table.append((address, max(size, raw_size), raw, raw_size))

    def offset(rva):
        for address, size, raw, raw_size in table:
            if address <= rva < address + size and rva - address < raw_size:
                return raw + rva - address
        raise ValueError('Invalid bridge import RVA.')

    rva = struct.unpack_from('<I', data, optional + 112 + 8)[0]
    if not rva:
        return machine, []
    position = offset(rva)
    values = []
    while any(data[position:position + 20]):
        name_rva = struct.unpack_from('<I', data, position + 12)[0]
        start = offset(name_rva)
        end = data.find(b'\0', start, start + 256)
        if end < 0:
            raise ValueError('Unterminated bridge import name.')
        name = data[start:end].decode('ascii').lower()
        if not name.endswith('.dll') or any(mark in name for mark in ('/', '\\', ':')):
            raise ValueError('Unexpected bridge import.')
        values.append(name)
        position += 20
        if len(values) > 32:
            raise ValueError('Unexpected bridge import count.')
    return machine, values


def assert_crt_free(path, name, expected_machine):
    machine, libraries = pe_imports(Path(path).read_bytes())
    if machine != expected_machine or set(libraries) != required_imports(name):
        raise RuntimeError('Bridge import set is not the CRT-free OS set: %s imports %s' % (name, ','.join(libraries)))
    return sorted(set(libraries))


def mingw_link_command(compiler, source, memory, resource, output, gui):
    command = [str(compiler), *MINGW_FLAGS]
    if gui:
        command.append('-DGIT_NEXT_GUI_BRIDGE')
    command += [str(source), str(memory)]
    if resource is not None:
        command.append(str(resource))
    command += ['-o', str(output), '-Wl,--entry,bridge_entry',
                '-Wl,--subsystem,' + ('windows' if gui else 'console'),
                '-Wl,--dynamicbase', '-Wl,--nxcompat', '-Wl,--no-insert-timestamp',
                '-lkernel32', '-ladvapi32', '-luser32']
    reject_reduced_inlining(command)
    return command


def msvc_memory_command(compiler, memory, output):
    command = [str(compiler), *MSVC_COMPILE, '/c', '/Fo' + str(output), str(memory)]
    reject_reduced_inlining(command)
    return command


def msvc_link_command(compiler, source, memory_object, resource, output, includes, libraries, gui):
    command = [str(compiler), *MSVC_COMPILE,
               *(('/D' + item) for item in ['UNICODE', '_UNICODE', 'WINVER=0x0A00', '_WIN32_WINNT=0x0A00']),
               *(('/I' + str(path)) for path in includes),
               '/Fo' + str(output) + '.obj', '/Fe' + str(output)]
    if gui:
        command.append('/DGIT_NEXT_GUI_BRIDGE')
    command += [str(source), str(memory_object), '/link', str(resource), '/NODEFAULTLIB', '/ENTRY:bridge_entry',
                '/SUBSYSTEM:' + ('WINDOWS' if gui else 'CONSOLE'), '/DYNAMICBASE', '/NXCOMPAT', '/HIGHENTROPYVA',
                *(('/LIBPATH:' + str(path)) for path in libraries), 'kernel32.lib', 'advapi32.lib', 'user32.lib']
    reject_reduced_inlining(command)
    return command


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--compiler', type=Path)
    parser.add_argument('--sdk', type=Path)
    parser.add_argument('--sdk-version')
    parser.add_argument('--msvc', type=Path)
    parser.add_argument('--llvm-root', type=Path, help='isolated LLVM-MinGW compiler for ARM64')
    parser.add_argument('--platform', choices=['windows-x64', 'windows-arm64'], default='windows-x64')
    parser.add_argument('--destination', type=Path, required=True)
    args = parser.parse_args()
    output = args.destination.resolve()
    if output.exists():
        parser.error('Bridge output directory must not exist.')
    source = ROOT / 'native-core/launcher/windows-bridge.c'
    memory = ROOT / 'native-core/launcher/freestanding-memory.c'
    digest = hashlib.sha256(source.read_bytes()).hexdigest()
    memory_sha = hashlib.sha256(memory.read_bytes()).hexdigest()
    resource = ROOT / 'native-core/launcher/windows-bridge.rc.in'
    resource_sha = hashlib.sha256(resource.read_bytes()).hexdigest()
    version = product_version(ROOT)
    match = re.fullmatch(r'([0-9]+)\.([0-9]+)\.([0-9]+)(?:-[A-Za-z0-9.-]+)?', version)
    if not match:
        parser.error('Invalid resource version.')
    architecture = 'x64' if args.platform == 'windows-x64' else 'arm64'
    expected_machine = 0x8664 if args.platform == 'windows-x64' else 0xaa64
    llvm = args.llvm_root.resolve(strict=True) if args.llvm_root else None
    if llvm:
        if args.platform != 'windows-arm64':
            parser.error('LLVM bridge adapter targets ARM64')
        compiler = llvm / 'bin/aarch64-w64-mingw32-clang.exe'
    else:
        if not all((args.compiler, args.sdk, args.sdk_version, args.msvc)):
            parser.error('Explicit MSVC and Windows SDK inputs are required')
        compiler = args.compiler.resolve(strict=True)
        sdk = args.sdk.resolve(strict=True)
        msvc = args.msvc.resolve(strict=True)
        includes = [msvc / 'include', *(sdk / 'Include' / args.sdk_version / name for name in ['um', 'shared', 'ucrt'])]
        libraries = [msvc / 'lib' / architecture, sdk / 'Lib' / args.sdk_version / 'um' / architecture]
        if not all(path.is_dir() for path in includes + libraries):
            parser.error('The matching MSVC and Windows SDK architecture is required.')
    output.mkdir(parents=True)
    resource_file = output / 'bridge.rc'
    resource_file.write_text(resource.read_text().replace('@FILE_VERSION@', ','.join(match.groups()) + ',0').replace('@PRODUCT_VERSION@', version), encoding='utf-8', newline='\n')
    resource_object = output / ('bridge.o' if llvm else 'bridge.res')
    if llvm:
        resource_command = [str(llvm / 'bin/aarch64-w64-mingw32-windres.exe'), '-I', str(llvm / 'aarch64-w64-mingw32/include'), str(resource_file), str(resource_object)]
    else:
        resource_compiler = sdk / 'bin' / args.sdk_version / 'x64/rc.exe'
        resource_command = [str(resource_compiler.resolve(strict=True)), '/nologo',
                            *(argument for path in includes for argument in ['/i', str(path)]),
                            '/fo', str(resource_object), str(resource_file)]
    reject_reduced_inlining(resource_command)
    subprocess.run(resource_command, check=True, cwd=ROOT)
    commands, files, import_map = [], {}, {}
    memory_object = None
    if not llvm:
        memory_object = output / 'freestanding-memory.obj'
        memory_command = msvc_memory_command(compiler, memory, memory_object)
        subprocess.run(memory_command, check=True, cwd=ROOT)
        commands.append(memory_command)
    for gui in [False, True]:
        name = 'git-next-bridge-gui.exe' if gui else 'git-next-bridge.exe'
        binary = output / name
        if llvm:
            command = mingw_link_command(compiler, source, memory, resource_object, binary, gui)
        else:
            command = msvc_link_command(compiler, source, memory_object, resource_object, binary, includes, libraries, gui)
        subprocess.run(command, check=True, cwd=ROOT)
        commands.append(command)
        import_map[name] = assert_crt_free(binary, name, expected_machine)
        files[name] = dict(sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), bytes=binary.stat().st_size)
    if hashlib.sha256(source.read_bytes()).hexdigest() != digest or hashlib.sha256(resource.read_bytes()).hexdigest() != resource_sha or hashlib.sha256(memory.read_bytes()).hexdigest() != memory_sha:
        raise RuntimeError('Bridge source changed during compilation.')
    receipt = dict(schema_version=1, status='bridge-compiled-not-runtime-accepted', platform=args.platform,
                   source_sha256=digest, memory_source_sha256=memory_sha, binaries=files, imports=import_map,
                   optimizer=OPTIMIZER, ob1_workaround=OB1_WORKAROUND,
                   freestanding_memory=['memcmp', 'memcpy', 'memmove', 'memset'],
                   compile_commands=commands, product_executed=False,
                   resource_sha256=resource_sha, product_version=version, resource_command=resource_command,
                   tests_executed=False, installed=False, release_eligible=False)
    (output / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n', encoding='utf-8', newline='\n')
    print(json.dumps({key: value for key, value in receipt.items() if key != 'compile_commands'}))


if __name__ == '__main__':
    main()
