"""Prepare a pinned, unmodified native compatibility-core source tree; no build/install."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import posixpath
import shutil
import stat
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--archive', type=Path, help='optional already downloaded official source archive')
    parser.add_argument('--destination', type=Path, required=True, help='new dedicated source directory; never merge or overwrite')
    args = parser.parse_args()
    lock = json.loads(ROOT.joinpath('packaging/native-core.lock.json').read_text())
    destination = args.destination.resolve()
    if destination.exists():
        parser.error('The source destination must not exist.')
    archive = args.archive
    if not archive:
        cache = ROOT / '.evidence/native-core-download'
        cache.mkdir(parents=True, exist_ok=True)
        archive = cache / (lock['archive_sha256'] + '.zip')
        if not archive.exists():
            temporary = archive.with_suffix('.zip.new')
            request = urllib.request.Request(lock['archive_url'], headers={'User-Agent':'git-next-local-core-builder'})
            try:
                with urllib.request.urlopen(request, timeout=120) as response, temporary.open('xb') as output:
                    length = 0
                    while data := response.read(1024 * 1024):
                        length += len(data)
                        if length > 64 * 1024 * 1024:
                            raise RuntimeError('Native source archive exceeds the download size limit')
                        output.write(data)
                if hashlib.sha256(temporary.read_bytes()).hexdigest() != lock['archive_sha256']:
                    raise RuntimeError('Official native source archive checksum mismatch')
                temporary.rename(archive)
            finally:
                temporary.unlink(missing_ok=True)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != lock['archive_sha256']:
        parser.error('Pinned native source archive checksum mismatch.')
    records = []
    skipped = []
    links = []
    with zipfile.ZipFile(archive) as source:
        if source.comment.decode() != lock['commit']:
            parser.error('Source archive commit does not match its pin.')
        if len(source.infolist()) > 20000 or sum(info.file_size for info in source.infolist()) > 256 * 1024 * 1024:
            parser.error('Unexpected source archive capacity.')
        for info in source.infolist():
            if not info.filename.startswith(lock['archive_prefix']):
                parser.error('Unexpected source archive root.')
            relative = PurePosixPath(info.filename[len(lock['archive_prefix']):])
            if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts):
                parser.error('Unsafe source archive member.')
            if '.github' in relative.parts and 'workflows' in relative.parts:
                skipped.append(relative.as_posix())
                continue
            if stat.S_ISLNK(info.external_attr >> 16):
                link = source.read(info).decode('utf-8')
                resolved = PurePosixPath(posixpath.normpath(str(relative.parent / link)))
                if PurePosixPath(link).is_absolute() or '..' in resolved.parts or any(':' in part or '\\' in part for part in resolved.parts):
                    parser.error('Unsafe source archive symlink.')
                links.append((relative, resolved))
                continue
            records.append((info, relative))
        destination.mkdir(parents=True)
        files = {}
        for info, relative in records:
            if not relative.parts:
                continue
            target = destination.joinpath(*relative.parts)
            if info.is_dir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                data = source.read(info)
                target.write_bytes(data)
                if os_name_is_unix():
                    # Codeload archives store a zero Unix mode. Applying it makes the
                    # tree unreadable and the later symlink copy fails. 0644 is the
                    # same fallback the Linux build uses for those members.
                    target.chmod(stat.S_IMODE(info.external_attr >> 16) or 0o644)
                files[relative.as_posix()] = hashlib.sha256(data).hexdigest()
    for relative, resolved in links:
        source_path = destination.joinpath(*resolved.parts)
        target = destination.joinpath(*relative.parts)
        if not source_path.exists() or target.exists():
            raise RuntimeError('Unexpected source symlink target')
        target.parent.mkdir(parents=True,exist_ok=True)
        if source_path.is_dir():
            shutil.copytree(source_path,target)
            for path in target.rglob('*'):
                if path.is_file():
                    files[path.relative_to(destination).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
        else:
            shutil.copyfile(source_path,target)
            files[relative.as_posix()] = hashlib.sha256(target.read_bytes()).hexdigest()
    receipt = dict(status='source-prepared-not-accepted',upstream=lock['upstream'],commit=lock['commit'],
                   archive_sha256=lock['archive_sha256'],license=lock['license'],files=files,
                   excluded_workflow_members=skipped,modified_upstream_files=[],runtime_accepted=False,
                   materialized_symlinks=[dict(path=path.as_posix(),target=target.as_posix()) for path,target in links])
    destination.joinpath('git-next-source-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({key:value for key,value in receipt.items() if key != 'files'},indent=2))


def os_name_is_unix():
    import os
    return os.name != 'nt'


if __name__ == '__main__':
    main()
