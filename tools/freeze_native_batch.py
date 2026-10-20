"""Freeze complete package and harness bytes before the native acceptance batch."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BASES = {
    'linux-x64': 'debian@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587',
    'linux-arm64': 'debian@sha256:a1b86db52ce3daef089e45aabe36dfec4091f82464c25c1fdcf03de197cbe82a',
}


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    for name in ('windows-x64', 'windows-arm64', 'linux-x64', 'linux-arm64', 'macos-arm64',
                 'msi-directory', 'base-msi-directory', 'source-bundles', 'race-binary', 'output'):
        parser.add_argument('--'+name, required=True, type=Path)
    args = parser.parse_args()
    if args.output.exists():
        parser.error('Use a new frozen matrix path')
    files, platforms, hashes = {}, {}, set()

    def pin(path):
        path = path.resolve(strict=True)
        files[str(path)] = sha(path)
        return str(path)

    version = json.loads((ROOT/'packaging/product.json').read_text())['version']
    for platform in ('windows-x64', 'windows-arm64'):
        root = getattr(args, platform.replace('-', '_')).resolve(strict=True)
        manifest = json.loads((root/'git-next-distribution.json').read_text())
        if manifest['platform'] != platform or manifest['version'] != version:
            parser.error('Wrong Windows package identity')
        hashes.add(manifest['source_tree_sha256'])
        for name, item in manifest['files'].items():
            path = root/name
            if path.is_symlink() or not path.resolve(strict=True).is_relative_to(root) or sha(path) != item['sha256']:
                parser.error('Windows package member differs: '+name)
            pin(path)
        pin(root/'git-next-distribution.json')
        record = dict(directory=str(root))
        prefix = 'mingw64' if platform == 'windows-x64' else 'clangarm64'
        candidates = [root/'runtime'/prefix/'bin/git-credential-manager.exe', root/'runtime/mingw64/bin/git-credential-manager.exe']
        record['gcm'] = str(next(path for path in candidates if path.is_file()))
        for scope in ('user', 'machine'):
            suffix = '-user' if scope == 'user' else ''
            path = args.msi_directory/('git-next-'+version+'-'+platform+suffix+'.msi')
            receipt = path.with_suffix('.receipt.json')
            value = json.loads(receipt.read_text())
            if value['sha256'] != sha(path) or value['install_scope'] != scope:
                parser.error('MSI receipt differs')
            record['msi_'+scope] = pin(path)
            pin(receipt)
            if platform == 'windows-x64':
                older = args.base_msi_directory/('git-next-0.2.999-'+platform+suffix+'.msi')
                record['base_msi_'+scope] = pin(older)
                pin(older.with_suffix('.receipt.json'))
        platforms[platform] = record
    for platform in ('linux-x64', 'linux-arm64'):
        directory = getattr(args, platform.replace('-', '_')).resolve(strict=True)
        manifest = json.loads((directory/'package.json').read_text())
        receipt = json.loads((directory/'assembly.json').read_text())
        if manifest['platform'] != platform or not all(receipt['cleanup'].values()):
            parser.error('Wrong or uncleaned POSIX package input')
        hashes.add(manifest['source_tree_sha256'])
        archive = directory/'distribution.tar.gz'
        if sha(archive) != receipt['artifact']['sha256']:
            parser.error('POSIX package archive changed')
        platforms[platform] = dict(archive=pin(archive), base_image=BASES[platform])
        pin(directory/'package.json')
        pin(directory/'assembly.json')
    mac = args.macos_arm64.resolve(strict=True)
    platforms['macos-arm64'] = dict(archive=pin(mac/'macos-distribution.tar.gz'), base_image=BASES['linux-x64'])
    hashes.add(json.loads((mac/'core-build.json').read_text())['source_tree_sha256'])
    if len(hashes) != 1:
        parser.error('Five platform cores do not share one frozen source identity')
    sources = json.loads((args.source_bundles/'sources.json').read_text())
    for item in sources['bundles']:
        path = args.source_bundles/item['name']
        if sha(path) != item['sha256']:
            parser.error('Corresponding source bundle changed')
        pin(path)
    pin(args.source_bundles/'sources.json')
    harnesses = {}
    for folder in ('tools', 'tests', 'packaging'):
        for path in sorted((ROOT/folder).rglob('*')):
            if path.is_file() and path.suffix in ('.py', '.ps1', '.psm1', '.cs', '.go', '.mod', '.json', '.wxs', '.sh', '.Dockerfile') and '__pycache__' not in path.parts:
                harnesses[path.relative_to(ROOT).as_posix()] = sha(path)
    for name in ('tests/containers/Dockerfile_mac',):
        harnesses[name] = sha(ROOT/name)
    report = dict(schema_version=1, product='git-next', version=version, source_tree_sha256=hashes.pop(),
                  platforms=platforms, files=files, harnesses=harnesses, race_binary=pin(args.race_binary),
                  go_race_image='golang@sha256:55395706e9703db746cc507abfc4eb2aea75918f8a8024e4848e2cb81004f5ad',
                  product_executed=False, tests_executed=False, release_eligible=False)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2)+'\n', encoding='utf-8', newline='\n')
    print(json.dumps(dict(status='native-batch-inputs-frozen', platforms=len(platforms), files=len(files), harnesses=len(harnesses))))


if __name__ == '__main__':
    main()
