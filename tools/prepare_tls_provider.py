"""Pin an independently obtained TLS provider; private staging only, never MSI payload."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import struct

ROOT = Path(__file__).resolve().parents[1]
SYSTEM = {'advapi32.dll','bcrypt.dll','bcryptprimitives.dll','crypt32.dll','cryptnet.dll',
          'dnsapi.dll','iphlpapi.dll','kernel32.dll','msvcrt.dll','ncrypt.dll','ntdll.dll',
          'normaliz.dll','secur32.dll','shell32.dll','shlwapi.dll','user32.dll','version.dll',
          'winmm.dll','wldap32.dll','ws2_32.dll','wsock32.dll','cryptbase.dll'}


def imports(data):
    pe = struct.unpack_from('<I', data, 0x3c)[0]
    if data[pe:pe+4] != b'PE\0\0':
        raise ValueError('Invalid provider PE image')
    machine, sections, _, _, _, optional_size, _ = struct.unpack_from('<HHIIIHH',data,pe+4)
    optional = pe + 24
    if struct.unpack_from('<H',data,optional)[0] != 0x20b:
        raise ValueError('Expected 64-bit provider PE image')
    table = []
    for i in range(sections):
        size, address, raw_size, raw = struct.unpack_from('<IIII',data,optional+optional_size+40*i+8)
        table.append((address,max(size,raw_size),raw,raw_size))

    def offset(rva):
        for address,size,raw,raw_size in table:
            if address <= rva < address+size and rva-address < raw_size:
                return raw+rva-address
        raise ValueError('Invalid provider import RVA')

    rva = struct.unpack_from('<I',data,optional+112+8)[0]
    if not rva:
        return machine, []
    position = offset(rva)
    values = []
    while any(data[position:position+20]):
        name_rva = struct.unpack_from('<I',data,position+12)[0]
        start = offset(name_rva)
        end = data.find(b'\0',start,start+256)
        if end < 0:
            raise ValueError('Unterminated provider import name')
        name = data[start:end].decode('ascii').lower()
        if '/' in name or '\\' in name or ':' in name or not name.endswith('.dll'):
            raise ValueError('Unsafe provider import name')
        values.append(name)
        position += 20
        if len(values)>1024:
            raise ValueError('Unexpected provider import count')
    return machine, values


def collect(platform, assets):
    record = json.loads((assets/'git-next-runtime-assets.json').read_text())
    runtime_pin = json.loads((ROOT/'packaging/windows-runtime.lock.json').read_text())['platforms'][platform]
    if record['platform'] != platform or record['archive_sha256'] != runtime_pin['archive_sha256']:
        raise ValueError('Provider assets do not match the independent producer pin')
    prefix = record['native_prefix']+'/bin/'
    available = {Path(name).name.lower():name for name in record['files']
                 if name.startswith(prefix) and '/' not in name[len(prefix):] and name.lower().endswith('.dll')}
    pending = ['libcurl-openssl-4.dll']
    selected = {}
    visited = set()
    system = set()
    while pending:
        name = pending.pop()
        if name in visited:
            continue
        visited.add(name)
        relative = available.get(name)
        if not relative:
            raise ValueError('Missing native provider dependency: '+name)
        path = assets/relative
        data = path.read_bytes()
        digest = hashlib.sha256(data).hexdigest()
        if digest != record['files'][relative]['sha256']:
            raise ValueError('Provider asset differs from its receipt: '+name)
        arch, dependencies = imports(data)
        if arch != (0x8664 if platform=='windows-x64' else 0xaa64):
            raise ValueError('Provider dependency machine mismatch')
        selected[relative] = dict(archive_member=relative,sha256=digest,size=len(data))
        for dependency in dependencies:
            if dependency in available:
                pending.append(dependency)
            elif dependency in SYSTEM or dependency.startswith(('api-ms-win-','ext-ms-win-')):
                system.add(dependency)
            else:
                raise ValueError('Unresolved provider dependency: '+dependency)
    for relative, entry in record['files'].items():
        if (relative in (record['native_prefix']+'/etc/ssl/openssl.cnf', record['native_prefix']+'/etc/ssl/openssl.cnf.dist')
                or relative.startswith(record['native_prefix']+'/lib/ossl-modules/') and relative.endswith('.dll')):
            data = (assets/relative).read_bytes()
            digest = hashlib.sha256(data).hexdigest()
            if digest != entry['sha256']:
                raise ValueError('Provider configuration/module checksum mismatch')
            selected[relative] = dict(archive_member=relative,sha256=digest,size=len(data))
    archive_size = json.loads((ROOT/'.evidence/windows-runtime-assets'/(platform+'-inventory.json')).read_text())['archive_bytes']
    return dict(archive_url=runtime_pin['archive_url'],archive_sha256=runtime_pin['archive_sha256'],
                archive_size=archive_size,provider='openssl',curl_version='8.19.0',tls_version='3.5.6',
                entrypoint=prefix+'libcurl-openssl-4.dll',native_prefix=record['native_prefix'],
                files=selected,system_dependencies=sorted(system))


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--assets-root',type=Path,required=True)
    parser.add_argument('--private-stage',type=Path,help='new private fixture/component directory; never publish')
    args = parser.parse_args()
    if args.private_stage and args.private_stage.exists():
        parser.error('Private provider stage already exists.')
    platforms = {name:collect(name,args.assets_root/name) for name in ['windows-x64','windows-arm64']}
    pin = dict(schema_version=1,component='tls-openssl',distribution='independent-producer-download-only',
               bundled_in_git_next_release=False,platforms=platforms)
    target = ROOT/'packaging/tls-providers.lock.json'
    target.write_text(json.dumps(pin,indent=2)+'\n',encoding='utf-8',newline='\n')
    header = '/* Generated public provider checksums. SPDX-License-Identifier: GPL-2.0-only */\n'
    header += 'struct git_next_tls_pin { const char *name; const char *sha256; };\n'
    # Select ARM64 first; the public catalog order remains stable.
    for i,name in enumerate(['windows-arm64','windows-x64']):
        platform = platforms[name]
        header += '#if defined(_M_ARM64) || defined(__aarch64__)\n' if i==0 else '#else\n'
        header += '#define GIT_NEXT_TLS_PLATFORM '+json.dumps(name)+'\n'
        header += '#define GIT_NEXT_TLS_ARCHIVE_SHA256 '+json.dumps(platform['archive_sha256'])+'\n'
        header += '#define GIT_NEXT_TLS_ENTRYPOINT '+json.dumps(platform['entrypoint'])+'\n'
        header += '#define GIT_NEXT_TLS_NATIVE_PREFIX '+json.dumps(platform['native_prefix'])+'\n'
        header += 'static const struct git_next_tls_pin git_next_tls_pins[] = {\n'
        for filename,record in sorted(platform['files'].items()):
            header += '\t{ '+json.dumps(filename)+', '+json.dumps(record['sha256'])+' },\n'
        header += '};\n'
    header += '#endif\n'
    generated = ROOT/'native-core/generated'
    generated.mkdir(parents=True,exist_ok=True)
    (generated/'git-next-tls-pins.h').write_text(header,encoding='utf-8',newline='\n')
    if args.private_stage:
        for name,platform in platforms.items():
            folder = args.private_stage/name
            folder.mkdir(parents=True)
            for filename,record in platform['files'].items():
                target=folder/filename
                target.parent.mkdir(parents=True,exist_ok=True)
                shutil.copyfile(args.assets_root/name/record['archive_member'],target)
            (folder/'provider.json').write_text(json.dumps(dict(schema_version=1,platform=name,archive_sha256=platform['archive_sha256'],private_component=True),indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(dict(status='independent-provider-pins-prepared',dlls={name:len(p['files']) for name,p in platforms.items()},bundled_in_release=False,executed=False)))


if __name__ == '__main__':
    main()
