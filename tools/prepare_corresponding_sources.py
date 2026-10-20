"""Fetch checksum-pinned corresponding sources without extracting upstream workflows."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path
import re
import time
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--lock', type=Path, default=ROOT/'packaging/corresponding-sources.lock.json')
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--workers', type=int, default=8, choices=range(1, 17))
    args = parser.parse_args()
    lock = json.loads(args.lock.read_text(encoding='utf-8'))
    if lock['schema_version'] != 1 or lock['product'] != 'git-next':
        parser.error('Unsupported source lock')
    args.cache.mkdir(parents=True, exist_ok=True)
    args.output.mkdir(parents=True, exist_ok=False)
    names = set()
    for item in lock['sources']:
        name = item['file']
        if Path(name).name != name or name in names or not re.fullmatch('[a-f0-9]{64}', item['sha256']):
            parser.error('Duplicate or unsafe source entry')
        names.add(name)

    def fetch(item):
        path = args.cache/item['file']
        if path.is_file() and path.stat().st_size == item['bytes'] and digest(path) == item['sha256']:
            return
        if path.exists():
            raise ValueError('Existing source bytes differ: '+item['file'])
        if not item['url'].startswith('https://'):
            raise ValueError('Source download must use HTTPS')
        temporary = path.with_name(path.name+'.partial')
        for attempt in range(3):
            try:
                request = urllib.request.Request(item['url'], headers={'User-Agent': 'git-next-source-delivery'})
                with urllib.request.urlopen(request, timeout=45) as response, temporary.open('xb') as stream:
                    size = 0
                    while block := response.read(1024*1024):
                        size += len(block)
                        if size > item['bytes']:
                            raise ValueError('Source exceeds pinned size')
                        stream.write(block)
                if size != item['bytes'] or digest(temporary) != item['sha256']:
                    raise ValueError('Source checksum mismatch')
                temporary.rename(path)
                return
            except Exception:
                temporary.unlink(missing_ok=True)
                if attempt == 2:
                    raise
                time.sleep(attempt+1)

    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        list(pool.map(fetch, lock['sources']))
    bundles = []
    groups = sorted({item['group'] for item in lock['sources']})
    for group in groups:
        if not re.fullmatch('[a-z0-9-]+', group):
            raise ValueError('Invalid source group')
        path = args.output/('git-next-corresponding-sources-'+group+'.zip')
        selected = [item for item in lock['sources'] if item['group'] == group]
        with zipfile.ZipFile(path, 'x', compression=zipfile.ZIP_STORED, allowZip64=True) as archive:
            archive.writestr('SOURCES.json', json.dumps(selected, indent=2)+'\n')
            archive.writestr('README.txt', 'Original source archives and producer recipes are opaque, unmodified files.\n'
                              'Git-next patches, overlay, build scripts and locks are in the matching product source release.\n'
                              'No GitHub workflow is extracted, enabled or executed by this tool.\n')
            for item in selected:
                archive.write(args.cache/item['file'], 'archives/'+item['file'])
        bundles.append(dict(name=path.name, bytes=path.stat().st_size, sha256=digest(path), components=len(selected)))
    receipt = dict(schema_version=1, status='source-materials-packaged', lock_sha256=digest(args.lock),
                   components=len(lock['sources']), bundles=bundles, upstream_archives_extracted=False)
    (args.output/'sources.json').write_text(json.dumps(receipt, indent=2)+'\n', encoding='utf-8', newline='\n')
    print(json.dumps(receipt))


if __name__ == '__main__':
    main()
