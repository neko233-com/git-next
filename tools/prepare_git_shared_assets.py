"""Stage portable Git help/GUI data from pinned producer bytes; never run scripts or viewers."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def safe(name):
    relative = PurePosixPath(name)
    if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts): raise ValueError('Unsafe shared asset path')
    return relative


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--windows-runtime-assets',required=True,type=Path)
    parser.add_argument('--cache',required=True,type=Path)
    parser.add_argument('--destination',required=True,type=Path)
    args = parser.parse_args()
    lock = json.loads((ROOT/'packaging/git-shared-runtime.lock.json').read_text())
    source = args.windows_runtime_assets.resolve(strict=True)
    runtime = json.loads((source/'git-next-runtime-assets.json').read_text())
    if runtime['archive_sha256'] != lock['portable_ui_and_html']['archive_sha256']: parser.error('Portable producer inputs differ from their source pin')
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Shared asset destination must be new')
    destination.mkdir(parents=True); args.cache.mkdir(parents=True,exist_ok=True)
    files = {}; total = 0
    def write(name,data,mode,origin):
        nonlocal total
        safe(name)
        if name in files: raise ValueError('Duplicate shared asset path')
        total += len(data)
        if total > 256*1024*1024 or len(files) > 20000: raise ValueError('Shared assets exceed their size/file limit')
        if data[:2] == b'MZ' or data[:4] in (b'\x7fELF',b'\xcf\xfa\xed\xfe'): raise ValueError('A native executable entered portable data assets')
        output = destination/name; output.parent.mkdir(parents=True,exist_ok=True); output.write_bytes(data)
        files[name] = dict(kind='file',sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),mode=mode,component=origin)
    native = runtime['native_prefix']
    exact = {native+'/libexec/git-core/git-gui':'usr/libexec/git-core/git-gui',
             native+'/libexec/git-core/git-gui.tcl':'usr/libexec/git-core/git-gui.tcl',native+'/bin/gitk':'usr/bin/gitk'}
    prefixes = {native+'/share/git-gui/':'usr/share/git-gui/',native+'/share/doc/git-doc/':'usr/share/doc/git-doc/',
                native+'/share/locale/':'usr/share/locale/'}
    for name,item in runtime['files'].items():
        output = exact.get(name)
        if not output:
            for prefix,target in prefixes.items():
                if name.startswith(prefix): output = target+name[len(prefix):]; break
        if not output: continue
        path = source/name
        if not path.resolve(strict=True).is_relative_to(source) or sha(path) != item['sha256']: raise ValueError('Shared producer bytes changed')
        write(output,path.read_bytes(),0o755 if output in exact.values() else 0o644,'pinned-portable-git-data')
    pin = lock['manuals']; archive = args.cache/pin['asset']
    if not archive.exists():
        temporary = archive.with_suffix(archive.suffix+'.new')
        try:
            request = urllib.request.Request(pin['url'],headers={'User-Agent':'git-next-runtime-material-builder'})
            with urllib.request.urlopen(request,timeout=60) as response,temporary.open('xb') as output:
                count = 0
                while data := response.read(1024*1024):
                    count += len(data)
                    if count > pin['bytes']: raise ValueError('Manual archive exceeds its pinned size')
                    output.write(data)
            if sha(temporary) != pin['sha256']: raise ValueError('Manual archive digest differs from its pin')
            temporary.rename(archive)
        finally: temporary.unlink(missing_ok=True)
    if archive.stat().st_size != pin['bytes'] or sha(archive) != pin['sha256']: raise ValueError('Manual archive differs from its pin')
    with tarfile.open(archive) as source_archive:
        for item in source_archive:
            relative = safe(item.name)
            if item.isdir(): continue
            if not relative.parts or relative.parts[0] not in ('man1','man5','man7'): continue
            if not item.isfile(): raise ValueError('Unexpected manual archive link/device')
            with source_archive.extractfile(item) as stream: write('usr/share/man/'+relative.as_posix(),stream.read(),0o644,'pinned-git-manual')
    for name in ('usr/libexec/git-core/git-gui','usr/libexec/git-core/git-gui.tcl','usr/bin/gitk',
                 'usr/share/man/man1/git.1','usr/share/doc/git-doc/git.html'):
        if name not in files: raise ValueError('Required portable Git asset is missing: '+name)
    record = dict(schema_version=1,status='shared-git-assets-staged-not-runtime-accepted',version=lock['version'],
                  files=files,source=lock,producer_receipt_sha256=sha(args.windows_runtime_assets/'git-next-runtime-assets.json'),
                  platform_requirements=['Tcl/Tk 8.6 or newer for Git GUI/gitk','normal platform manual/browser viewers'],
                  product_executed=False,tests_executed=False,installed=False,release_eligible=False)
    (destination/'git-next-shared-runtime.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'files':len(files),'bytes':total,'product_executed':False,'tests_executed':False}))


if __name__ == '__main__': main()
