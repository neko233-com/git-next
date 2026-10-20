"""Fetch pinned third-party Windows assets and inventory them without executing/extracting."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def checksum(path):
    result = hashlib.sha256()
    with path.open('rb') as source:
        while block := source.read(1024 * 1024):
            result.update(block)
    return result.hexdigest()


def fetch(platform, pin, cache):
    destination = cache / (platform + '-' + pin['archive_sha256'] + '.tar.bz2')
    if not destination.exists():
        request = urllib.request.Request(pin['archive_url'], headers={'User-Agent':'git-next-owned-runtime-builder'})
        temporary = None
        try:
            started = time.monotonic()
            with urllib.request.urlopen(request, timeout=60) as response:
                final = urllib.parse.urlsplit(response.geturl())
                if final.scheme != 'https' or final.hostname not in ('github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'):
                    raise ValueError('Unexpected official asset redirect')
                with tempfile.NamedTemporaryFile(prefix=platform + '-', suffix='.part', dir=cache, delete=False) as output:
                    temporary = Path(output.name)
                    total = 0
                    while block := response.read(1024 * 1024):
                        total += len(block)
                        if total > 512 * 1024 * 1024 or time.monotonic() - started > 600:
                            raise ValueError('Official runtime archive exceeds download bounds')
                        output.write(block)
            if checksum(temporary) != pin['archive_sha256']:
                raise ValueError('Official runtime archive checksum mismatch')
            if destination.exists():
                if checksum(destination) != pin['archive_sha256']:
                    raise ValueError('Existing runtime archive differs from its pin')
            else:
                temporary.rename(destination)
        finally:
            if temporary:
                temporary.unlink(missing_ok=True)
    if checksum(destination) != pin['archive_sha256']:
        raise ValueError('Cached runtime archive checksum mismatch')
    records = []
    skipped_workflows = []
    with tarfile.open(destination, 'r:bz2') as archive:
        total = 0
        for item in archive:
            relative = PurePosixPath(item.name)
            if relative.is_absolute() or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts):
                raise ValueError('Unsafe runtime archive member')
            if '.github' in relative.parts and 'workflows' in relative.parts:
                skipped_workflows.append(item.name)
                continue
            total += item.size
            if len(records) >= 100000 or total > 2 * 1024 ** 3:
                raise ValueError('Runtime archive exceeds unpacked capacity')
            records.append(dict(path=relative.as_posix(), bytes=item.size, mode=item.mode,
                                type='file' if item.isfile() else 'directory' if item.isdir() else
                                     'hardlink' if item.islnk() else 'symlink' if item.issym() else 'unsupported',
                                link=item.linkname if item.islnk() or item.issym() else None))
    report = dict(schema_version=1, status='assets-inventoried-not-integrated', platform=platform,
                  archive_url=pin['archive_url'], archive_sha256=pin['archive_sha256'],
                  archive_bytes=destination.stat().st_size, unpacked_bytes=total, members=records,
                  excluded_workflow_members=skipped_workflows, extracted=False, executed=False,
                  release_eligible=False, upstream_git_core_must_be_excluded=True)
    (cache / (platform + '-inventory.json')).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8', newline='\n')
    print(json.dumps({key:value for key,value in report.items() if key != 'members'}), flush=True)
    return platform


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--platform', choices=('windows-x64','windows-arm64','all'), default='all')
    parser.add_argument('--cache', type=Path, default=ROOT / '.evidence/windows-runtime-assets')
    args = parser.parse_args()
    lock = json.loads((ROOT / 'packaging/windows-runtime.lock.json').read_text())
    cache = args.cache.resolve()
    cache.mkdir(parents=True, exist_ok=True)
    platforms = lock['platforms'] if args.platform == 'all' else [args.platform]
    failures = []
    with ThreadPoolExecutor(max_workers=2) as pool:
        pending = {platform:pool.submit(fetch, platform, lock['platforms'][platform], cache) for platform in platforms}
        for platform, future in pending.items():
            try:
                future.result()
            except Exception as error:
                failures.append(dict(platform=platform,error=str(error)))
    if failures:
        raise SystemExit(json.dumps(failures))


if __name__ == '__main__':
    main()
