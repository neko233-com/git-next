"""Build a CRT-free Windows Installer launcher without executing the result."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
from prepare_tls_provider import imports
from assemble_native_windows import machine

ROOT=Path(__file__).resolve().parents[1]


def sha(path):
    return hashlib.file_digest(Path(path).open('rb'),'sha256').hexdigest()


def main():
    parser=argparse.ArgumentParser(__doc__)
    parser.add_argument('--llvm-root',type=Path,required=True)
    parser.add_argument('--platform',choices=('windows-x64','windows-arm64'),required=True)
    parser.add_argument('--version',required=True)
    parser.add_argument('--destination',type=Path,required=True)
    args=parser.parse_args();sdk=args.llvm_root.resolve(strict=True);out=args.destination.resolve()
    if out.exists() or not re.fullmatch(r'\d+\.\d+\.\d+',args.version):parser.error('Use a new destination and numeric product version')
    target='x86_64-w64-mingw32' if args.platform=='windows-x64' else 'aarch64-w64-mingw32'
    compiler=sdk/'bin'/f'{target}-clang.exe';windres=sdk/'bin'/f'{target}-windres.exe'
    pin=json.loads((ROOT/'packaging/git-terminal-runtime.lock.json').read_text())
    receipt=json.loads((sdk/'receipt.json').read_text())
    if receipt['source']['sha256']!=pin['source_archive']['sha256']:parser.error('Compiler SDK source pin mismatch')
    for path in (compiler,windres):
        if sha(path)!=receipt['files'][path.relative_to(sdk).as_posix()]:parser.error('Compiler SDK bytes changed')
    out.mkdir(parents=True)
    source=ROOT/'native-core/launcher/windows-msi-worker.c';template=ROOT/'native-core/launcher/windows-msi-worker.rc.in'
    resource=out/'worker.rc';resource.write_text(template.read_text().replace('@FILE_VERSION@',args.version.replace('.',',')+',0').replace('@PRODUCT_VERSION@',args.version),encoding='utf-8',newline='\n')
    env={**os.environ,'PATH':str(sdk/'bin')+os.pathsep+os.environ['PATH']}
    subprocess.run([str(windres),'-I',str(sdk/target/'include'),str(resource),str(out/'worker.o')],env=env,check=True)
    binary=out/'msi-worker.exe'
    subprocess.run([str(compiler),'-O2','-fno-stack-protector','-ffreestanding','-fno-builtin','-nostdlib','-Wall','-Wextra','-Werror','-DUNICODE','-D_UNICODE',str(source),str(out/'worker.o'),'-o',str(binary),'-Wl,--entry,worker_entry','-Wl,--subsystem,windows','-Wl,--dynamicbase','-Wl,--nxcompat','-Wl,--no-insert-timestamp','-lkernel32'],env=env,check=True)
    expected=0x8664 if args.platform=='windows-x64' else 0xaa64
    import_machine,libraries=imports(binary.read_bytes())
    if machine(binary)!=expected or import_machine!=expected or set(libraries)!={'kernel32.dll'}:parser.error('Unexpected worker architecture or runtime import')
    record=dict(schema_version=1,product='git-next-msi-worker',platform=args.platform,version=args.version,
                source_sha256=sha(source),resource_sha256=sha(template),binary_sha256=sha(binary),imports=libraries,
                compiler_archive_sha256=pin['source_archive']['sha256'],executed=False)
    (out/'receipt.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps(record))


if __name__=='__main__':main()
