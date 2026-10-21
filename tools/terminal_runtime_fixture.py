"""Stage the installer-initialized runtime without mutating frozen package inputs."""
import hashlib
import json
import os
from pathlib import Path
import shutil


def prepare_terminal_runtime(source, destination):
    source=Path(source).resolve(strict=True)
    destination=Path(destination).resolve()
    if destination.exists():raise ValueError('Use a new owned terminal runtime fixture')
    shutil.copytree(source,destination,copy_function=os.link,symlinks=True)
    manifest=json.loads((destination/'runtime/defaults/manifest.json').read_text())
    if manifest['product']!='git-next':raise ValueError('Unexpected runtime defaults')
    for item in manifest['files']:
        target=destination/item['target'];default=destination/item['source']
        if not target.resolve().is_relative_to(destination) or not default.resolve(strict=True).is_relative_to(destination):
            raise ValueError('Runtime default escaped the fixture')
        if hashlib.file_digest(default.open('rb'),'sha256').hexdigest()!=item['sha256']:
            raise ValueError('Runtime default changed')
        if target.exists():raise ValueError('An initial runtime configuration already exists')
        target.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(default,target)
        if item.get('attributes',0)&4:
            import ctypes
            if not ctypes.windll.kernel32.SetFileAttributesW(str(target),4):
                raise OSError('Cannot restore owned runtime configuration attributes')
    return destination
