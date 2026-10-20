"""Prepare an isolated pinned compiler SDK; never modify an existing Visual Studio installation."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--destination',required=True,type=Path)
    parser.add_argument('--cache',required=True,type=Path)
    args = parser.parse_args()
    pin = json.loads((ROOT/'packaging/llvm-mingw.lock.json').read_text())
    destination = args.destination.resolve(); cache = args.cache.resolve()
    if destination.exists(): parser.error('Compiler workspace must be new')
    cache.mkdir(parents=True,exist_ok=True); archive=cache/pin['asset']
    if not archive.exists():
        temporary=archive.with_suffix('.download')
        try:
            request=urllib.request.Request(pin['url'],headers={'User-Agent':'git-next-build-inputs'})
            with urllib.request.urlopen(request,timeout=60) as response, temporary.open('xb') as output:
                while chunk:=response.read(1024*1024):
                    output.write(chunk)
                    if output.tell()>pin['bytes']: raise ValueError('Compiler asset exceeds the pinned size')
            if temporary.stat().st_size!=pin['bytes'] or sha(temporary)!=pin['sha256']: raise ValueError('Compiler asset digest differs')
            temporary.rename(archive)
        finally:
            if temporary.exists(): temporary.unlink()
    if archive.stat().st_size!=pin['bytes'] or sha(archive)!=pin['sha256']: raise ValueError('Cached compiler differs from its pin')
    destination.mkdir(parents=True); prefix=pin['asset'].removesuffix('.zip')+'/'
    excluded=[]; total=0; names=set()
    with zipfile.ZipFile(archive) as source:
        if len(source.infolist())>100000: raise ValueError('Compiler archive has too many members')
        for item in source.infolist():
            if item.is_dir(): continue
            if not item.filename.startswith(prefix): raise ValueError('Unexpected compiler archive root')
            relative=PurePosixPath(item.filename[len(prefix):])
            if relative.is_absolute() or '..' in relative.parts or not relative.parts or any(':' in p or '\\' in p for p in relative.parts): raise ValueError('Unsafe compiler archive member')
            if '.github' in relative.parts and 'workflows' in relative.parts: excluded.append(str(relative)); continue
            if str(relative) in names: raise ValueError('Duplicate compiler archive member')
            names.add(str(relative)); total+=item.file_size
            if total>4*1024**3: raise ValueError('Compiler archive expands beyond its size limit')
            output=destination.joinpath(*relative.parts); output.parent.mkdir(parents=True,exist_ok=True)
            with source.open(item) as value, output.open('xb') as target: shutil.copyfileobj(value,target)
    files={path.relative_to(destination).as_posix():sha(path) for path in sorted(destination.rglob('*')) if path.is_file()}
    for name in ('aarch64-w64-mingw32-clang.exe','aarch64-w64-mingw32-windres.exe','llvm-ar.exe'):
        if 'bin/'+name not in files: raise ValueError('Required native compiler tool missing: '+name)
    receipt=dict(schema_version=1,status='isolated-compiler-prepared',source=pin,files=files,excluded_workflows=excluded,
                 product_executed=False,tests_executed=False,compiler_executed=False,installed_into_product=False)
    (destination/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'status':receipt['status'],'files':len(files),'payload_bytes':total,'product_executed':False}))


if __name__=='__main__': main()
