"""Prepare pinned libpsl ABI imports and its owned DLL closure; never load it."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

from prepare_windows_iconv import pe_exports
from prepare_tls_provider import imports, SYSTEM

ROOT = Path(__file__).resolve().parents[1]
API = {'psl_builtin', 'psl_latest', 'psl_free', 'psl_is_public_suffix',
       'psl_is_cookie_domain_acceptable', 'psl_check_version_number', 'psl_get_version'}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--runtime', type=Path, required=True)
    parser.add_argument('--destination', type=Path, required=True)
    parser.add_argument('--lib', type=Path, required=True)
    args = parser.parse_args()
    runtime = args.runtime.resolve(strict=True)
    receipt = json.loads((runtime/'git-next-runtime-assets.json').read_text())
    platform = receipt['platform']
    expected = 0x8664 if platform == 'windows-x64' else 0xaa64
    prefix = receipt['native_prefix']+'/bin/'
    available = {Path(name).name.lower():name for name in receipt['files']
                 if name.startswith(prefix) and '/' not in name[len(prefix):] and name.endswith('.dll')}
    pending, files = ['libpsl-5.dll'], {}
    while pending:
        name = pending.pop()
        if name in files:
            continue
        relative = available[name]
        data = (runtime/relative).read_bytes()
        digest = hashlib.sha256(data).hexdigest()
        machine, dependencies = imports(data)
        if machine != expected or digest != receipt['files'][relative]['sha256']:
            raise ValueError('Pinned libpsl dependency changed: '+name)
        if name == 'libpsl-5.dll' and not API <= pe_exports(data)[1]:
            raise ValueError('Pinned libpsl exports do not implement the required ABI')
        files[name] = dict(sha256=digest,archive_member=relative)
        for dependency in dependencies:
            if dependency in available:
                pending.append(dependency)
            elif dependency not in SYSTEM and not dependency.startswith(('api-ms-win-', 'ext-ms-win-')):
                raise ValueError('Unresolved libpsl dependency: '+dependency)
    destination = args.destination.resolve()
    if destination.exists():
        parser.error('Import destination must not exist.')
    header = ROOT/'native-core/vendor/libpsl-abi/libpsl.h.in'
    if hashlib.sha256(header.read_bytes()).hexdigest() != '6591fd46b9846403f59d436b11cbb1017f3031801181a7b258a1d3729ad57be9':
        raise ValueError('Pinned public libpsl ABI header changed')
    template = header.read_text(encoding='utf-8')
    for key, value in dict(LIBPSL_VERSION='0.21.5', LIBPSL_VERSION_MAJOR='0',
                           LIBPSL_VERSION_MINOR='21', LIBPSL_VERSION_PATCH='5',
                           LIBPSL_VERSION_NUMBER='0x001505').items():
        template = template.replace('@'+key+'@', value)
    if '@LIBPSL_' in template:
        raise ValueError('Unresolved libpsl header variables')
    for folder in ('include','lib','bin'):
        (destination/folder).mkdir(parents=True,exist_ok=True)
    (destination/'include/libpsl.h').write_text(template,encoding='utf-8',newline='\n')
    definition = destination/'lib/psl.def'
    definition.write_text('LIBRARY libpsl-5.dll\nEXPORTS\n'+'\n'.join(sorted(API))+'\n',encoding='ascii',newline='\n')
    library = destination/'lib/psl.lib'
    subprocess.run([str(args.lib.resolve(strict=True)),'/nologo','/def:'+str(definition),
                    '/machine:'+('x64' if expected == 0x8664 else 'arm64'), '/out:'+str(library)],check=True)
    for name, record in files.items():
        shutil.copyfile(runtime/record['archive_member'],destination/'bin'/name)
    report = dict(schema_version=1,status='libpsl-imports-prepared-not-runtime-accepted',
                  platform=platform,version='0.21.5',header_source='https://raw.githubusercontent.com/rockdaboot/libpsl/0.21.5/include/libpsl.h.in',
                  header_sha256=hashlib.sha256(header.read_bytes()).hexdigest(),
                  import_library_sha256=hashlib.sha256(library.read_bytes()).hexdigest(),
                  upstream_runtime_archive_sha256=receipt['archive_sha256'],files=files,
                  exported_api=sorted(API),product_executed=False,tests_executed=False)
    (destination/'receipt.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({key:report[key] for key in ('status','platform','version','product_executed','tests_executed')}))


if __name__ == '__main__':
    main()
