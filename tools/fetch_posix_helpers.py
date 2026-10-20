"""Download/stage pinned primary LFS/GCM helpers; never execute or register them."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile
import urllib.parse
import urllib.request
import zipfile
from build_native_posix import image_platform

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def download(pin,cache):
    target = cache/pin['asset']
    if not target.exists():
        url = urllib.parse.urlsplit(pin['url'])
        if url.scheme != 'https' or url.hostname != 'github.com' or url.username or url.password:
            raise ValueError('Unexpected helper release URL')
        request = urllib.request.Request(pin['url'],headers={'User-Agent':'git-next-runtime-builder'})
        temporary = target.with_suffix(target.suffix+'.new')
        try:
            with urllib.request.urlopen(request,timeout=60) as response, temporary.open('xb') as output:
                length = 0
                while chunk := response.read(1024*1024):
                    length += len(chunk)
                    if length > pin['bytes']: raise ValueError('Helper archive exceeds its pinned size')
                    output.write(chunk)
            if temporary.stat().st_size != pin['bytes'] or digest(temporary) != pin['sha256']: raise ValueError('Helper archive differs from its pin')
            temporary.rename(target)
        finally: temporary.unlink(missing_ok=True)
    if target.stat().st_size != pin['bytes'] or digest(target) != pin['sha256']: raise ValueError('Cached helper archive differs from its pin')
    return target


def members(path):
    if zipfile.is_zipfile(path):
        with zipfile.ZipFile(path) as source:
            for info in source.infolist():
                if info.is_dir(): continue
                if (info.external_attr >> 16) & 0o170000 == 0o120000: raise ValueError('Unexpected helper ZIP link')
                yield info.filename,source.read(info),((info.external_attr >> 16) & 0o777) or 0o644
    else:
        with tarfile.open(path) as source:
            for info in source:
                if info.isdir(): continue
                if not info.isfile(): raise ValueError('Unexpected helper archive link/device')
                with source.extractfile(info) as stream: yield info.name,stream.read(),info.mode & 0o777


def safe(name):
    path = PurePosixPath(name)
    if path.is_absolute() or '..' in path.parts or any(':' in part or '\\' in part for part in path.parts):
        raise ValueError('Unsafe helper archive path')
    return path


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform',required=True,choices=['linux-x64','linux-arm64','macos-arm64'])
    parser.add_argument('--cache',required=True,type=Path)
    parser.add_argument('--destination',required=True,type=Path)
    args = parser.parse_args()
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Helper staging prefix must be new')
    destination.mkdir(parents=True); args.cache.mkdir(parents=True,exist_ok=True)
    lock = json.loads((ROOT/'packaging/posix-helpers.lock.json').read_text())
    pins = lock['platforms'][args.platform]
    files = {}; components = {}; total = 0
    def write(relative,data,mode,component):
        nonlocal total
        safe(relative)
        if relative in files: raise ValueError('Duplicate primary helper asset')
        total += len(data)
        if total > 768*1024*1024 or len(files) > 20000: raise ValueError('Helper extraction exceeds its size/file limit')
        target = destination/relative
        target.parent.mkdir(parents=True,exist_ok=True); target.write_bytes(data)
        files[relative] = dict(kind='file',sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),mode=mode,component=component)
    def link(relative,target,component):
        safe(relative)
        files[relative] = dict(kind='symlink',target=target,component=component)
    for component in ('lfs','gcm'):
        pin = pins[component]
        archive = download(pin,args.cache)
        entries = [(safe(name).as_posix(),data,mode) for name,data,mode in members(archive)]
        names = [name for name,_,_ in entries]
        program = 'git-lfs' if component == 'lfs' else 'git-credential-manager'
        candidates = [name for name in names if PurePosixPath(name).name == program]
        if len(candidates) != 1: raise ValueError('Primary helper executable is missing or ambiguous')
        main = candidates[0]; parent = PurePosixPath(main).parent
        for name,data,mode in entries:
            path = safe(name)
            if '.github' in path.parts and 'workflows' in path.parts: continue
            if component == 'lfs':
                if name == main: write('usr/bin/git-lfs',data,0o755,'lfs')
                elif path.name.upper().startswith(('LICENSE','COPYING','NOTICE')): write('share/licenses/git-lfs/'+path.name,data,mode,'lfs')
            else:
                try: relative = path.relative_to(parent)
                except ValueError: raise ValueError('GCM archive has unrelated top-level payload')
                write('usr/lib/git-next/gcm/'+relative.as_posix(),data,0o755 if name == main else mode,'gcm')
        if component == 'gcm':
            link('usr/bin/git-credential-manager','../lib/git-next/gcm/git-credential-manager','gcm')
            link('usr/bin/git-credential-manager-core','git-credential-manager','gcm')
        executable = destination/('usr/bin/git-lfs' if component == 'lfs' else 'usr/lib/git-next/gcm/git-credential-manager')
        if image_platform(executable) != args.platform: raise ValueError('Primary helper machine type differs from its target')
        components[component] = pin
    record = dict(schema_version=1,status='primary-posix-helpers-staged-not-runtime-accepted',platform=args.platform,
                  components=components,files=files,physical_symlinks_created=False,lock_sha256=digest(ROOT/'packaging/posix-helpers.lock.json'),
                  product_executed=False,tests_executed=False,installed=False,release_eligible=False)
    (destination/'git-next-posix-helpers.json').write_text(json.dumps(record,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'platform':args.platform,'files':len(files),'bytes':total,'product_executed':False}))


if __name__ == '__main__': main()
