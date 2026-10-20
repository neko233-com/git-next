"""Stage pinned runtime assets, excluding upstream Git cores; no execution or installation."""
import argparse
import ctypes
from ctypes import wintypes
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import tarfile

from fetch_windows_runtime import checksum

ROOT = Path(__file__).resolve().parents[1]
CORE_NAMES = {
    'git.exe', 'scalar.exe', 'git-daemon.exe', 'git-shell.exe',
    'git-http-backend.exe', 'git-http-fetch.exe', 'git-http-push.exe',
    'git-imap-send.exe', 'git-remote-ftp.exe', 'git-remote-ftps.exe',
    'git-remote-http.exe', 'git-remote-https.exe', 'git-receive-pack.exe',
    'git-upload-archive.exe', 'git-upload-pack.exe',
}
VIRTUAL_LINKS = {'dev/fd':'/proc/self/fd', 'dev/stderr':'/proc/self/fd/2',
                 'dev/stdin':'/proc/self/fd/0', 'dev/stdout':'/proc/self/fd/1',
                 'etc/mtab':'/proc/mounts'}


def system_attribute(path):
    if os.name != 'nt':
        return
    # MSYS symlinks use a regular file, UTF-16 cookie and the System bit:
    # https://github.com/msys2/msys2-runtime/blob/master/winsup/cygwin/path.cc
    get_attributes = ctypes.windll.kernel32.GetFileAttributesW
    get_attributes.argtypes = [wintypes.LPCWSTR]
    get_attributes.restype = wintypes.DWORD
    set_attributes = ctypes.windll.kernel32.SetFileAttributesW
    set_attributes.argtypes = [wintypes.LPCWSTR, wintypes.DWORD]
    set_attributes.restype = wintypes.BOOL
    attributes = get_attributes(str(path))
    if attributes == 0xffffffff or not set_attributes(str(path), attributes | 4):
        raise OSError('Could not preserve MSYS virtual-link attributes')


def relative_name(name):
    relative = PurePosixPath(name)
    if relative.is_absolute() or '..' in relative.parts or any(':' in p or '\\' in p for p in relative.parts):
        raise ValueError('Unsafe runtime member path')
    return relative


def prepare(platform, cache, destination):
    lock = json.loads((ROOT / 'packaging/windows-runtime.lock.json').read_text())
    pin = lock['platforms'][platform]
    archive = cache / (platform + '-' + pin['archive_sha256'] + '.tar.bz2')
    if checksum(archive) != pin['archive_sha256']:
        raise ValueError('Pinned runtime source archive checksum mismatch')
    if destination.exists():
        raise ValueError('Runtime asset destination must not exist')
    destination.mkdir(parents=True)
    seen = set()
    files = {}
    excluded = []
    excluded_data = {}
    pending = []
    total = 0

    def register(relative, data, kind='file', link=None):
        target = destination.joinpath(*relative.parts)
        target.parent.mkdir(parents=True, exist_ok=True)
        with target.open('xb') as output:
            output.write(data)
        if kind == 'msys-virtual-symlink':
            system_attribute(target)
        files[relative.as_posix()] = dict(sha256=hashlib.sha256(data).hexdigest(), bytes=len(data),
                                         kind=kind, link=link, windows_attributes=4 if kind == 'msys-virtual-symlink' else 0)

    with tarfile.open(archive, 'r|bz2') as source:
        for item in source:
            relative = relative_name(item.name)
            name = relative.as_posix()
            if '.github' in relative.parts and 'workflows' in relative.parts:
                excluded.append(dict(path=name, reason='workflow excluded'))
                continue
            if item.isdir():
                destination.joinpath(*relative.parts).mkdir(parents=True, exist_ok=True)
                continue
            key = name.casefold()
            if key in seen:
                raise ValueError('Duplicate/case-colliding runtime member: ' + name)
            seen.add(key)
            total += item.size
            if len(seen) > 100000 or total > 2 * 1024 ** 3 or item.size > 128 * 1024 ** 2:
                raise ValueError('Runtime source capacity exceeded')
            if relative.name.lower() in CORE_NAMES:
                excluded.append(dict(path=name, reason='upstream Git core/entrypoint; replace with git-next build output'))
                # Some independent GUI launchers are hardlinks to the small upstream
                # wrapper, not to the Git engine. Retain bytes only in build memory.
                if item.isfile():
                    excluded_data[name] = source.extractfile(item).read()
                continue
            if item.isfile():
                register(relative, source.extractfile(item).read())
            elif item.islnk():
                link = relative_name(item.linkname)
                pending.append((relative, link))
            elif item.issym():
                if VIRTUAL_LINKS.get(name) != item.linkname:
                    raise ValueError('Unexpected MSYS virtual symlink: ' + name)
                data = b'!<symlink>\xff\xfe' + (item.linkname + '\0').encode('utf-16-le')
                register(relative, data, 'msys-virtual-symlink', item.linkname)
            else:
                raise ValueError('Unsupported runtime archive member: ' + name)
    while pending:
        deferred = []
        for relative, link in pending:
            target = destination.joinpath(*link.parts)
            if target.is_file():
                data = target.read_bytes()
            elif link.as_posix() in excluded_data:
                if relative.name.lower() not in {'git-gui.exe', 'gitk.exe', 'git-wrapper.exe'}:
                    raise ValueError('Unapproved helper references excluded Git core bytes')
                data = excluded_data[link.as_posix()]
            else:
                deferred.append((relative, link))
                continue
            register(relative, data, 'materialized-hardlink', link.as_posix())
        if len(deferred) == len(pending):
            raise ValueError('Unresolved runtime archive hardlinks')
        pending = deferred
    prefix = 'mingw64' if platform == 'windows-x64' else 'clangarm64'
    required = ['usr/bin/sh.exe', 'usr/bin/ssh.exe', 'usr/bin/perl.exe', 'usr/bin/gpg.exe',
                prefix + '/bin/git-lfs.exe', prefix + '/bin/git-credential-manager.exe',
                prefix + '/bin/libiconv-2.dll', 'etc/package-versions.txt']
    if not all(name in files for name in required):
        raise ValueError('Required native runtime components are absent')
    record = dict(schema_version=1, status='assets-staged-not-core-integrated', platform=platform,
                  archive_url=pin['archive_url'], archive_sha256=pin['archive_sha256'],
                  native_prefix=prefix, files=files, excluded=excluded, tests_executed=False,
                  product_executed=False, installed=False, release_eligible=False,
                  remaining='Attach own core/helper paths, iconv imports and source/license package; then final consolidated acceptance')
    (destination / 'git-next-runtime-assets.json').write_text(json.dumps(record, indent=2) + '\n', encoding='utf-8', newline='\n')
    print(json.dumps(dict(platform=platform, status=record['status'], files=len(files),
                          excluded_core_entries=len(excluded), upstream_core_deployed=False,
                          product_executed=False)), flush=True)


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform', required=True, choices=('windows-x64','windows-arm64'))
    parser.add_argument('--cache', type=Path, default=ROOT / '.evidence/windows-runtime-assets')
    parser.add_argument('--destination', type=Path, required=True)
    args = parser.parse_args()
    prepare(args.platform, args.cache.resolve(), args.destination.resolve())


if __name__ == '__main__':
    main()
