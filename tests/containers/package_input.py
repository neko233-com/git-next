"""Bounded package extraction on the Linux fixture filesystem; never load target code."""
from pathlib import Path, PurePosixPath
import shutil
import tarfile


def unpack(archive, destination):
    destination=Path(destination).resolve();destination.mkdir()
    names=set();pending=[];total=0
    with tarfile.open(archive) as source:
        for item in source:
            name=PurePosixPath(item.name)
            if item.isdir() and not name.parts: continue
            if name.is_absolute() or '..' in name.parts or not name.parts or any(':' in part or '\\' in part for part in name.parts):
                raise ValueError('Unsafe package member')
            canonical=name.as_posix();total+=item.size
            if canonical in names or len(names)>=75000 or total>4*1024*1024*1024: raise ValueError('Package extraction limit/duplicate')
            names.add(canonical);target=destination.joinpath(*name.parts)
            if item.isdir(): target.mkdir(parents=True,exist_ok=True)
            elif item.isfile():
                target.parent.mkdir(parents=True,exist_ok=True)
                with source.extractfile(item) as stream,target.open('xb') as output: shutil.copyfileobj(stream,output)
                target.chmod(item.mode&0o777)
            elif item.issym():
                if Path(item.linkname).is_absolute() or not (target.parent/item.linkname).resolve().is_relative_to(destination): raise ValueError('Escaping package link')
                pending.append((target,item.linkname))
            else: raise ValueError('Unsupported package member')
    for target,value in pending:
        target.parent.mkdir(parents=True,exist_ok=True);target.symlink_to(value)
    if (destination/'package.json').is_file(): return destination
    candidates=list(destination.glob('*/package.json'))
    if len(candidates)!=1: raise ValueError('Missing/ambiguous package root')
    return candidates[0].parent
