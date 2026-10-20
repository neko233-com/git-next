"""Extract verified build products into a new POSIX staging volume."""
import argparse
import os
from pathlib import Path, PurePosixPath
import posixpath
import shutil
import tarfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('archive',type=Path)
    parser.add_argument('destination',type=Path)
    args = parser.parse_args()
    root = args.destination.resolve(strict=True)
    links = []; names = set(); total = 0
    with tarfile.open(args.archive) as source:
        for item in source:
            relative = PurePosixPath(item.name)
            if relative.is_absolute() or '..' in relative.parts or not relative.parts or relative.parts[0] not in ('core-stage','core-build','dependencies','tls') or any(':' in part or '\\' in part for part in relative.parts): raise ValueError('Unexpected build archive path')
            name = relative.as_posix()
            if name in names: raise ValueError('Duplicate build archive member')
            names.add(name); total += item.size
            if len(names) > 50000 or total > 2*1024*1024*1024: raise ValueError('Build archive exceeds its size/file limit')
            target = root.joinpath(*relative.parts)
            if item.isdir(): target.mkdir(parents=True,exist_ok=True)
            elif item.isfile():
                target.parent.mkdir(parents=True,exist_ok=True)
                with source.extractfile(item) as value, target.open('xb') as output: shutil.copyfileobj(value,output)
                target.chmod(item.mode & 0o777)
                os.utime(target, (item.mtime, item.mtime))
            elif item.issym() or item.islnk():
                base = relative.parent.as_posix() if item.issym() else ''
                resolved = PurePosixPath(posixpath.normpath(posixpath.join(base,item.linkname)))
                if PurePosixPath(item.linkname).is_absolute() or '..' in resolved.parts or not resolved.parts or resolved.parts[0] not in ('core-stage','core-build','dependencies','tls'): raise ValueError('Build archive link escapes its roots')
                links.append((item,target,root.joinpath(*resolved.parts)))
            else: raise ValueError('Unexpected build archive member type')
    pending = links
    while pending:
        progress = False; next_pending = []
        for item,target,resolved in pending:
            if item.islnk() and not resolved.is_file(): next_pending.append((item,target,resolved)); continue
            target.parent.mkdir(parents=True,exist_ok=True)
            if target.exists() or target.is_symlink(): raise ValueError('Build archive link destination is occupied')
            if item.issym(): target.symlink_to(item.linkname)
            else: target.hardlink_to(resolved)
            progress = True
        if not progress: raise ValueError('Unresolved build archive hard links')
        pending = next_pending


if __name__ == '__main__': main()
