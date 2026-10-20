"""Extract data-only runtime SDK archives with bounded, confined writes on Python 3.11+."""
import argparse
from pathlib import Path, PurePosixPath
import shutil
import tarfile


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('archive', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    root = args.destination.resolve(strict=True)
    names = set()
    total = 0
    with tarfile.open(args.archive) as source:
        for item in source:
            relative = PurePosixPath(item.name)
            if item.isdir() and not relative.parts: continue
            if (relative.is_absolute() or '..' in relative.parts or not relative.parts
                    or any(':' in part or '\\' in part for part in relative.parts)):
                raise ValueError('Unsafe runtime member')
            name = relative.as_posix()
            if name in names: raise ValueError('Duplicate runtime member')
            names.add(name)
            total += item.size
            if len(names) > 50000 or total > 1536*1024*1024:
                raise ValueError('Runtime SDK exceeds extraction limits')
            target = root.joinpath(*relative.parts)
            if item.isdir(): target.mkdir(parents=True, exist_ok=True)
            elif item.isfile():
                target.parent.mkdir(parents=True, exist_ok=True)
                with source.extractfile(item) as stream, target.open('xb') as output:
                    shutil.copyfileobj(stream, output)
                target.chmod(item.mode & 0o777)
            else: raise ValueError('Runtime SDK may contain data files only; links remain declared in its manifest')


if __name__ == '__main__': main()
