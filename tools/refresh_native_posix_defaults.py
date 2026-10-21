"""Refresh defaults/notices in accepted POSIX runtime bytes without recompiling the core."""
import argparse
import copy
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile

from prepare_native_release import ROOT, documents, encoded, sha


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists(): parser.error('Use a new POSIX package path')
    with tarfile.open(args.archive) as archive:
        package = json.load(archive.extractfile('./package.json'))
        report = json.load(archive.extractfile('./git-next-distribution.json'))
        original_source = package['source_tree_sha256']
        replacements = documents()
        for name in ('unity.gitignore','unity.gitattributes','unreal.gitignore','unreal.gitattributes'):
            replacements['docs/game-profiles/'+name] = (ROOT/'docs/game-profiles'/name).read_bytes()
        for name in ('pre-push','post-checkout','post-commit','post-merge'):
            replacements['runtime/usr/share/git-core/templates/hooks/'+name] = (ROOT/'packaging/lfs-hooks'/name).read_bytes()
        for name in ('LICENSE.md','source.json'):
            replacements['LICENSES/git-lfs-hook-templates/'+name] = (ROOT/'packaging/lfs-hooks'/name).read_bytes()
        config = archive.extractfile('./runtime/etc/gitconfig').read()
        if b'[filter "lfs"]' not in config:
            config += b'[filter "lfs"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n'
        replacements['runtime/etc/gitconfig'] = config
    hook_root = 'runtime/usr/share/git-core/templates/hooks/'
    for name,data in replacements.items():
        mode = 0o755 if name.startswith(hook_root) else 0o644
        record = dict(kind='file',sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),mode=mode,component='product-defaults-and-documentation')
        package['files'][name] = record
        report['files'][name] = record.copy()
    for item in (package,report):
        item['release_eligible'] = False
        item['source_commit'] = ''
        item['packaging_delta'] = dict(source_archive_sha256=sha(args.archive),core_source_tree_sha256=original_source,
                                       native_core_and_runtime_images_changed=False,builtin_lfs_filters_and_template_hooks=True)
    replacements['package.json'] = encoded(package)
    report['files']['package.json'].update(sha256=hashlib.sha256(replacements['package.json']).hexdigest(),bytes=len(replacements['package.json']))
    replacements['git-next-distribution.json'] = encoded(report)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    seen = set()
    with tarfile.open(args.archive,'r|gz') as original, tarfile.open(args.output,'w:gz',compresslevel=6) as output:
        for member in original:
            relative = PurePosixPath(member.name)
            if relative.is_absolute() or '..' in relative.parts: raise ValueError('Unsafe source archive member')
            name = relative.as_posix()
            if name in seen: raise ValueError('Duplicate source archive member')
            seen.add(name)
            value = copy.copy(member)
            value.pax_headers = {key:data for key,data in member.pax_headers.items() if key not in ('path','linkpath')}
            value.name = '.' if name == '.' else './'+name
            if name in replacements:
                data = replacements[name];value.size=len(data);value.mode=0o755 if name.startswith(hook_root) else 0o644
                output.addfile(value,io.BytesIO(data))
            else:
                output.addfile(value,original.extractfile(member) if member.isfile() else None)
        for name in sorted(replacements.keys()-seen):
            data = replacements[name];value=tarfile.TarInfo('./'+name);value.size=len(data);value.mode=0o755 if name.startswith(hook_root) else 0o644
            output.addfile(value,io.BytesIO(data))
    print(json.dumps(dict(status='defaults-refreshed-not-runtime-accepted',platform=package['platform'],source_tree_sha256=original_source,sha256=sha(args.output))))


if __name__ == '__main__': main()
