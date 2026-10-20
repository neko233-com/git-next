"""Create MSVC imports for the pinned owned GNU iconv DLL; do not execute it."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import struct
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def pe_exports(data):
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if data[pe:pe+4] != b'PE\0\0':
        raise ValueError('Invalid iconv PE signature')
    machine, sections, _, _, _, optional_size, _ = struct.unpack_from('<HHIIIHH', data, pe+4)
    optional = pe + 24
    if struct.unpack_from('<H', data, optional)[0] != 0x20b:
        raise ValueError('Expected a 64-bit GNU iconv DLL')
    tables = []
    for i in range(sections):
        base = optional + optional_size + 40 * i
        size, address, raw_size, raw_offset = struct.unpack_from('<IIII', data, base+8)
        tables.append((address, max(size,raw_size), raw_offset, raw_size))

    def offset(rva):
        for address, size, raw, raw_size in tables:
            if address <= rva < address + size and rva-address < raw_size:
                return raw + rva - address
        raise ValueError('Invalid iconv export RVA')

    export_rva = struct.unpack_from('<I', data, optional + 112)[0]
    directory = offset(export_rva)
    count = struct.unpack_from('<I', data, directory+24)[0]
    names = offset(struct.unpack_from('<I', data, directory+32)[0])
    if count > 10000:
        raise ValueError('Unexpected iconv export count')
    exports = set()
    for i in range(count):
        start = offset(struct.unpack_from('<I', data, names+4*i)[0])
        end = data.find(b'\0', start, start+256)
        if end < 0:
            raise ValueError('Invalid iconv export name')
        exports.add(data[start:end].decode('ascii'))
    return machine, exports


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--runtime', type=Path, required=True)
    parser.add_argument('--destination', type=Path, required=True)
    parser.add_argument('--lib', type=Path, required=True, help='MSVC lib.exe build tool')
    args = parser.parse_args()
    runtime = args.runtime.resolve(strict=True)
    receipt = json.loads((runtime / 'git-next-runtime-assets.json').read_text())
    relative = receipt['native_prefix'] + '/bin/libiconv-2.dll'
    dll = runtime / relative
    data = dll.read_bytes()
    sha = hashlib.sha256(data).hexdigest()
    if sha != receipt['files'][relative]['sha256']:
        raise ValueError('Owned GNU iconv DLL differs from its pinned runtime receipt')
    platform = receipt['platform']
    machine, exports = pe_exports(data)
    expected = 0x8664 if platform == 'windows-x64' else 0xaa64
    required = {'libiconv', 'libiconv_open', 'libiconv_close'}
    if machine != expected or not required <= exports:
        raise ValueError('GNU iconv machine/export ABI mismatch')
    destination = args.destination.resolve()
    if destination.exists():
        raise ValueError('Iconv import destination must not exist')
    for folder in ('include','lib','bin'):
        (destination / folder).mkdir(parents=True,exist_ok=True)
    header = ROOT / 'native-core/overlay/git-next-iconv.h'
    shutil.copyfile(header,destination / 'include/iconv.h')
    definition = destination / 'lib/iconv.def'
    definition.write_text('LIBRARY libiconv-2.dll\nEXPORTS\n' + '\n'.join(sorted(required)) + '\n',encoding='ascii',newline='\n')
    library = destination / 'lib/iconv.lib'
    subprocess.run([str(args.lib.resolve(strict=True)), '/nologo', '/def:' + str(definition),
                    '/machine:' + ('x64' if expected == 0x8664 else 'arm64'), '/out:' + str(library)],check=True)
    shutil.copyfile(dll,destination / 'bin/libiconv-2.dll')
    report = dict(schema_version=1,status='iconv-imports-prepared-not-runtime-accepted',platform=platform,
                  upstream_runtime_archive_sha256=receipt['archive_sha256'],dll_sha256=sha,
                  header_sha256=hashlib.sha256(header.read_bytes()).hexdigest(),
                  import_library_sha256=hashlib.sha256(library.read_bytes()).hexdigest(),
                  exported_api=sorted(required),errno_bridge='native-core/overlay/git-next-iconv.c',
                  product_executed=False,tests_executed=False,installed=False,release_eligible=False)
    (destination / 'receipt.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(report))


if __name__ == '__main__':
    main()
