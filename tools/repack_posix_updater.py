"""Replace a compiled owned updater and manuals in an immutable POSIX candidate archive."""
import argparse
import copy
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import shutil
import tarfile

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def encoded(value):
    return (json.dumps(value, indent=2)+'\n').encode()


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--sha256', required=True)
    parser.add_argument('--updater-directory', type=Path, required=True)
    parser.add_argument('--destination', type=Path, required=True)
    args = parser.parse_args()
    if sha(args.archive) != args.sha256:
        parser.error('Original archive differs from its frozen identity')
    receipt = json.loads((args.updater_directory/'receipt.json').read_text())
    if receipt['sources'] != {p.name:sha(p) for p in (ROOT/'packaging/posix/updater').iterdir() if p.is_file()}:
        parser.error('Updater build receipt does not match current sources')
    args.destination.mkdir(parents=True, exist_ok=False)
    with tarfile.open(args.archive) as source:
        members = source.getmembers()
        table = {PurePosixPath(item.name).as_posix():item for item in members}
        if len(table) != len(members):
            parser.error('Duplicate original package member')
        package = json.load(source.extractfile(table['package.json']))
        report = json.load(source.extractfile(table['git-next-distribution.json']))
        updater = args.updater_directory/package['platform']/'updater'
        if sha(updater) != receipt['binaries'][package['platform']]['sha256']:
            parser.error('Updater target/hash differs')
        replacements = {'libexec/git-next/updater':updater.read_bytes()}
        for name, item in package['files'].items():
            if name.startswith('docs/') and item['kind'] == 'file' and (ROOT/name).is_file() and sha(ROOT/name) != item['sha256']:
                replacements[name] = (ROOT/name).read_bytes()
        for name, data in replacements.items():
            if name not in table or not table[name].isfile():
                parser.error('Replacement is not an existing regular package file')
            value = dict(sha256=hashlib.sha256(data).hexdigest(), bytes=len(data))
            package['files'][name].update(value)
            report['files'][name].update(value)
        replacements['package.json'] = encoded(package)
        report['files']['package.json'].update(sha256=hashlib.sha256(replacements['package.json']).hexdigest(), bytes=len(replacements['package.json']))
        report['updater_build_receipt_sha256'] = sha(args.updater_directory/'receipt.json')
        report['repacked_from_sha256'] = args.sha256
        replacements['git-next-distribution.json'] = encoded(report)
        name = 'macos-distribution.tar.gz' if package['platform'] == 'macos-arm64' else 'distribution.tar.gz'
        target = args.destination/name
        with tarfile.open(target, 'x:gz', compresslevel=3) as output:
            for item in members:
                relative = PurePosixPath(item.name)
                if relative.is_absolute() or '..' in relative.parts or not (item.isdir() or item.isfile() or item.issym()):
                    parser.error('Unsafe original package member')
                key = relative.as_posix()
                if key in replacements:
                    changed = copy.copy(item)
                    data = replacements[key]
                    changed.size = len(data)
                    output.addfile(changed, io.BytesIO(data))
                else:
                    output.addfile(item, source.extractfile(item) if item.isfile() else None)
    (args.destination/'package.json').write_bytes(replacements['package.json'])
    (args.destination/'git-next-distribution.json').write_bytes(replacements['git-next-distribution.json'])
    original = args.archive.parent/'assembly.json'
    assembly = json.loads(original.read_text()) if original.is_file() else dict(cleanup={'container_absent':True,'volume_absent':True})
    assembly.update(schema_version=1, status='assembled-not-runtime-accepted', platform=package['platform'],
                    source_tree_sha256=package['source_tree_sha256'], repacked_from_sha256=args.sha256,
                    updater_build_receipt_sha256=sha(args.updater_directory/'receipt.json'),
                    product_executed=False, tests_executed=False, installed=False, release_eligible=False,
                    artifact=dict(name=target.name, bytes=target.stat().st_size, sha256=sha(target)))
    (args.destination/'assembly.json').write_bytes(encoded(assembly))
    if (args.archive.parent/'core-build.json').is_file():
        shutil.copyfile(args.archive.parent/'core-build.json', args.destination/'core-build.json')
    print(json.dumps(dict(status=assembly['status'], platform=package['platform'], replacements=len(replacements))))


if __name__ == '__main__':
    main()
