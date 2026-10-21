"""Assemble owned POSIX cores, runtime and updater; inspect/relocate images without executing them."""
import argparse
import hashlib
import html
import json
from native_product import native_compatibility
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
import shutil
import subprocess

from build_native_core import source_identity
from build_native_posix import image_platform
from posix_images import ELF_SYSTEM_LIBRARIES, elf_needed, macho_needed, macho_system, relocate_elf, relocate_macho

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def safe(name):
    relative = PurePosixPath(name)
    if relative.is_absolute() or not relative.parts or '..' in relative.parts or any(':' in part or '\\' in part for part in relative.parts): raise ValueError('Unsafe POSIX distribution path')
    return relative


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--source',required=True,type=Path)
    parser.add_argument('--core-stage',required=True,type=Path)
    parser.add_argument('--core-receipt',required=True,type=Path)
    parser.add_argument('--runtime-assets',required=True,type=Path)
    parser.add_argument('--shared-assets',required=True,type=Path,help='pinned portable manuals, GUI scripts and HTML data')
    parser.add_argument('--updater',required=True,type=Path,help='compiled owned POSIX updater; never a shell download launcher')
    parser.add_argument('--destination',required=True,type=Path)
    parser.add_argument('--source-commit',help='immutable project source commit for final release metadata')
    args = parser.parse_args()
    if os.name != 'posix': parser.error('Assembly needs native POSIX filesystem/image tools')
    source = args.source.resolve(strict=True); core = args.core_stage.resolve(strict=True)
    assets = args.runtime_assets.resolve(strict=True); updater = args.updater.resolve(strict=True)
    shared = args.shared_assets.resolve(strict=True)
    destination = args.destination.resolve()
    if destination.exists(): parser.error('Distribution root must be new')
    build = json.loads(args.core_receipt.read_text())
    runtime = json.loads((assets/'git-next-posix-runtime.json').read_text())
    shared_manifest = json.loads((shared/'git-next-shared-runtime.json').read_text())
    if shared_manifest['schema_version'] != 1 or shared_manifest['version'] != '2.54.0': parser.error('Shared Git data does not match the compatibility generation')
    target = build['platform']
    if target not in ('linux-x64','linux-arm64','macos-arm64') or runtime['platform'] != target: parser.error('Core and runtime targets differ')
    if build['source_tree_sha256'] != source_identity(source): parser.error('Core receipt does not identify current integrated sources')
    if image_platform(updater) != target: parser.error('Updater machine type differs from the package')
    compatibility = native_compatibility(ROOT)
    if args.source_commit and not re.fullmatch(r'[0-9a-f]{40}',args.source_commit): parser.error('Use a complete immutable source SHA')
    destination.mkdir(parents=True)
    records = {}; images = []; links = []
    def reserve(name):
        safe(name)
        if name in records: raise ValueError('Duplicate distribution member: '+name)
    def copy(path,name,kind,mode=0o644):
        reserve(name)
        output = destination/name; output.parent.mkdir(parents=True,exist_ok=True); shutil.copyfile(path,output); output.chmod(mode)
        machine = image_platform(output)
        with output.open('rb') as stream: magic = stream.read(4)
        if not machine and magic in (b'\x7fELF',b'\xfe\xed\xfa\xce',b'\xce\xfa\xed\xfe',b'\xfe\xed\xfa\xcf',b'\xca\xfe\xba\xbe',b'\xbe\xba\xfe\xca',b'\xca\xfe\xba\xbf',b'\xbf\xba\xfe\xca'):
            raise ValueError('Native SDK images must have the inspected target architecture; provide thin arm64 Mach-O inputs')
        if machine:
            if machine != target: raise ValueError('Mixed native machine types in the distribution')
            images.append(output)
        records[name] = dict(kind='file',sha256=sha(output),bytes=output.stat().st_size,mode=mode,component=kind,input_sha256=sha(path))
    def write(name,value,kind,mode=0o644):
        reserve(name)
        output = destination/name; output.parent.mkdir(parents=True,exist_ok=True); output.write_bytes(value); output.chmod(mode)
        records[name] = dict(kind='file',sha256=sha(output),bytes=len(value),mode=mode,component=kind)
    def link(name,value,kind):
        reserve(name)
        if PurePosixPath(value).is_absolute() or '\\' in value: raise ValueError('Distribution links must remain relative')
        resolved = posixpath.normpath(posixpath.join(PurePosixPath(name).parent.as_posix(),value))
        safe(resolved)
        links.append((name,value,resolved))
        records[name] = dict(kind='symlink',target=value,component=kind)
    for name,item in runtime['files'].items():
        path = assets.joinpath(*safe(name).parts)
        if item['kind'] == 'file':
            if not path.resolve(strict=True).is_relative_to(assets) or sha(path) != item['sha256'] or path.stat().st_size != item['bytes']: parser.error('Runtime input differs from its receipt: '+name)
            copy(path,'runtime/'+name,'owned-runtime/'+item.get('component','native'),item['mode'])
        elif item['kind'] == 'symlink': link('runtime/'+name,item['target'],'owned-runtime-link')
        else: parser.error('Unsupported runtime member type')
    # GnuPG's compiled producer prefix must not select a missing producer agent.
    for command in ('gpg','gpgsm','gpg-agent'):
        name = 'runtime/usr/bin/'+command
        if name not in records or records[name]['kind'] != 'file': continue
        actual = 'runtime/usr/bin/.gnupg/'+command
        path = destination/name; moved = destination/actual
        moved.parent.mkdir(parents=True,exist_ok=True); path.rename(moved)
        records[actual] = records.pop(name)
        if path in images: images.remove(path); images.append(moved)
        argument = ' --agent-program "$directory/gpg-agent"' if command != 'gpg-agent' else ''
        if command == 'gpg-agent':
            if 'runtime/usr/bin/pinentry' in records: argument += ' --pinentry-program "$directory/pinentry"'
            if 'runtime/usr/lib/gnupg/scdaemon' in records: argument += ' --scdaemon-program "$directory/../lib/gnupg/scdaemon"'
        launcher = ('#!/bin/sh\nset -eu\ndirectory=$(CDPATH= cd -- "${0%/*}" && pwd -P)\n'
                    'exec "$directory/.gnupg/'+command+'"'+argument+' "$@"\n')
        write(name,launcher.encode(),'owned-signing-agent-paths',0o755)
    main_hash = build['staged_files']['bin/git']['sha256']
    if sha(core/'bin/git') != main_hash: parser.error('Staged core does not match its compiler receipt')
    copy(core/'bin/git','bin/git','source-built-git-next-core',0o755)
    link('bin/git-next','git','git-next-command-alias')
    for name,item in build['staged_files'].items():
        path = core.joinpath(*safe(name).parts)
        output = 'runtime/usr/'+name
        if output in records: parser.error('A runtime asset attempts to replace a Git core/helper member')
        if item['kind'] == 'file':
            if not path.resolve(strict=True).is_relative_to(core) or sha(path) != item['sha256'] or path.stat().st_size != item['bytes']: parser.error('Staged core member changed: '+name)
            if item['sha256'] == main_hash:
                link(output,posixpath.relpath('bin/git',str(PurePosixPath(output).parent)),'owned-core-alias')
            else: copy(path,output,'source-built-git-next-core-helper',item['mode'])
        elif item['kind'] == 'symlink': link(output,item['target'],'source-built-core-link')
        else: parser.error('Unexpected core staging member')
        if name.startswith('bin/') and name != 'bin/git': link(name,posixpath.relpath(output,'bin'),'public-git-tool')
    for name,item in shared_manifest['files'].items():
        path = shared.joinpath(*safe(name).parts)
        if item['kind'] != 'file' or not path.resolve(strict=True).is_relative_to(shared) or sha(path) != item['sha256'] or path.stat().st_size != item['bytes']: parser.error('Shared Git data differs from its receipt: '+name)
        copy(path,'runtime/'+name,'portable-git-data',item['mode'])
    for command in ('gitk','git-lfs','git-credential-manager','git-credential-manager-core'):
        launcher = ('#!/bin/sh\n# Copyright 2026 git-next contributors. MIT license.\nset -eu\n'
                    'self=$0\ncase "$self" in */*) ;; *) self=$(command -v "$self");; esac\n'
                    'base=${self%/*}\nroot=$(CDPATH= cd -- "$base/.." && pwd -P)\n'
                    'PATH="$root/bin:$root/runtime/usr/bin${PATH:+:$PATH}"\nexport PATH\n'
                    'exec "$root/runtime/usr/bin/'+command+'" "$@"\n')
        write('bin/'+command,launcher.encode(),'public-owned-helper-launcher',0o755)
    command_table = (source/'git.c').read_text().split('static struct cmd_struct commands[] = {',1)[1].split('\n};',1)[0]
    builtins = sorted(set(re.findall(r'\{\s*"([a-z][a-z0-9-]*)"\s*,',command_table)))
    if len(builtins) < 100: parser.error('The integrated builtin table is incomplete')
    for command in builtins:
        name = 'runtime/usr/libexec/git-core/git-'+command
        if name not in records: link(name,posixpath.relpath('bin/git',str(PurePosixPath(name).parent)),'owned-builtin-alias')
    for name in ('usr/bin/ssh','usr/bin/sh','usr/bin/git-lfs','usr/bin/git-credential-manager','usr/bin/gpg'):
        if 'runtime/'+name not in records: parser.error('Required owned runtime entrypoint is missing: '+name)
    copy(updater,'libexec/git-next/updater','owned-native-updater',0o755)
    copy(ROOT/'packaging/posix/install.sh','install.sh','native-package-installer',0o755)
    perl_roots = {'runtime/'+name.rsplit('/',1)[0] for name in runtime['files']
                  if name.endswith(('/FindBin.pm','/strict.pm')) and ('/perl' in name.lower())}
    for name in runtime['files']:
        if name.endswith('/Config.pm') and name.removesuffix('Config.pm')+'Config_heavy.pl' in runtime['files']:
            perl_roots.add('runtime/'+name.rsplit('/',1)[0])
    for name in runtime['files']:
        if '/auto/' in name and '/perl' in name.lower(): perl_roots.add('runtime/'+name.split('/auto/',1)[0])
    for name in ('usr/share/perl5','usr/lib/perl5'):
        if any(member.startswith(name+'/') for member in runtime['files']): perl_roots.add('runtime/'+name)
    if not perl_roots: parser.error('The owned interpreter SDK has no Perl standard library roots')
    interpreter_environment = ['PERL5LIB='+name for name in sorted(perl_roots)]
    interpreter_environment.append('PYTHONHOME=runtime/usr')
    for sentinel,variable in [('init.tcl','TCL_LIBRARY'),('tk.tcl','TK_LIBRARY')]:
        roots = sorted({'runtime/'+name.rsplit('/',1)[0] for name in runtime['files'] if name.endswith('/'+sentinel)})
        if len(roots) != 1: parser.error('The SDK must provide one matching Tcl/Tk standard library: '+sentinel)
        interpreter_environment.append(variable+'='+roots[0])
    write('runtime/interpreter-environment',('\n'.join(interpreter_environment)+'\n').encode(),'owned-interpreter-paths')
    write('runtime/etc/gitconfig',b'[core]\n\tautocrlf = input\n\teol = lf\n[init]\n\tdefaultBranch = main\n[filter "lfs"]\n\tclean = git-lfs clean -- %f\n\tsmudge = git-lfs smudge -- %f\n\tprocess = git-lfs filter-process\n\trequired = true\n','managed-product-defaults')
    for hook in ('pre-push','post-checkout','post-commit','post-merge'):
        copy(ROOT/'packaging/lfs-hooks'/hook,'runtime/usr/share/git-core/templates/hooks/'+hook,'builtin-standard-lfs-hook',0o755)
    copy(ROOT/'packaging/lfs-hooks/LICENSE.md','LICENSES/git-lfs-hook-templates/LICENSE.md','lfs-hook-license')
    copy(ROOT/'packaging/lfs-hooks/source.json','LICENSES/git-lfs-hook-templates/source.json','lfs-hook-source-identity')
    for name in ('README.md','README.zh-CN.md','AGENTS.md'):
        copy(ROOT/name,name,'product-documentation')
    copy(ROOT/'packaging/native-compatibility.json','compatibility.json','declared-native-compatibility')
    copy(ROOT/'native-core/LICENSE.GPL-2.0','LICENSE','native-core-license')
    copy(ROOT/'packaging/corresponding-sources.lock.json','LICENSES/corresponding-sources.lock.json','corresponding-source-manifest')
    for folder in ('docs','native-core'):
        for path in sorted((ROOT/folder).rglob('*')):
            if path.is_file() and '__pycache__' not in path.parts and not ('.github' in path.parts and 'workflows' in path.parts):
                copy(path,path.relative_to(ROOT).as_posix(),'product-documentation-and-source')
    for command,page in [('language','language.html'),('eol','line-endings.html'),('update','updates.html'),
                          ('doctor','takeover.html'),('compatibility','compatibility.html')]:
        document = '<!doctype html><meta charset="utf-8"><title>git-next '+command+'</title><a href="../../../../../docs/site/'+html.escape(page,quote=True)+'">git-next '+command+' manual</a>\n'
        write('runtime/usr/share/doc/git-doc/git-'+command+'.html',document.encode(),'product-help-entrypoint')
        synopsis = {'language':'get [--json] | set chinese|english|auto | list','eol':'[--apply]',
                    'update':'check|apply|status|enable|disable [--json]','doctor':'[--json]','compatibility':'[--json]'}[command]
        manual = ('.TH GIT-'+command.upper()+' 1 "2026" "git-next" "Git-next Manual"\n'
                  '.SH NAME\ngit-'+command+' \\- git-next '+command+' command\n'
                  '.SH SYNOPSIS\n.B git '+command+'\n'+synopsis+'\n'
                  '.SH DESCRIPTION\nSee the installed git-next '+command+' guide under docs/site/'+page+'.\n')
        write('runtime/usr/share/man/man1/git-'+command+'.1',manual.encode(),'product-help-entrypoint')
    pending = links
    while pending:
        progress = False; next_pending = []
        for name,value,resolved in pending:
            if resolved not in records: raise ValueError('Distribution link target is not owned: '+resolved)
            target_path = destination/resolved
            if not target_path.exists(): next_pending.append((name,value,resolved)); continue
            output = destination/name; output.parent.mkdir(parents=True,exist_ok=True); output.symlink_to(value)
            if not output.resolve(strict=True).is_relative_to(destination): raise ValueError('Distribution link escaped its root')
            progress = True
        if not progress: raise ValueError('Circular/unresolved distribution links')
        pending = next_pending
    if target.startswith('linux-'):
        for image in images:
            # The managed launcher supplies its library path without changing single-file bundle offsets.
            if records[image.relative_to(destination).as_posix()]['component'] != 'owned-runtime/primary-gcm': relocate_elf(image,destination/'runtime/lib')
        bundled = {name[len('runtime/lib/'):] for name in records if name.startswith('runtime/lib/')}
        for image in images:
            for name in elf_needed(image):
                if name not in ELF_SYSTEM_LIBRARIES and name not in bundled: raise ValueError('Unresolved owned ELF dependency: '+name)
    else:
        mapping = {key:'runtime/'+value for key,value in runtime.get('macho_library_map',{}).items()}
        for image in images: relocate_macho(image,destination,mapping)
        for image in images:
            for name in macho_needed(image):
                if macho_system(name): continue
                if not name.startswith('@loader_path/') or not (image.parent/name[len('@loader_path/'):]).resolve(strict=True).is_relative_to(destination): raise ValueError('Unresolved owned Mach-O dependency: '+name)
    for name,item in records.items():
        if item['kind'] == 'file': item.update(sha256=sha(destination/name),bytes=(destination/name).stat().st_size)
    package = dict(schema_version=1,product='git-next',engine='source-built-git-next-native-core',install_kind='tar',
                   version=build['product_version'],platform=target,source_commit=args.source_commit,
                   source_tree_sha256=build['source_tree_sha256'],files=records,builtins=builtins,
                   complete_git_parity=compatibility['complete_git_parity'],release_eligible=False,
                   operating_system_libraries=runtime.get('operating_system_libraries',[]),
                   platform_requirements=runtime.get('platform_requirements',[]))
    write('package.json',(json.dumps(package,indent=2)+'\n').encode(),'product-package-manifest')
    report = dict(schema_version=1,status='posix-distribution-assembled-not-runtime-accepted',product='git-next',platform=target,
                  version=build['product_version'],source_tree_sha256=build['source_tree_sha256'],files=records,
                  native_images_inspected=len(images),core_receipt_sha256=sha(args.core_receipt),
                  runtime_receipt_sha256=sha(assets/'git-next-posix-runtime.json'),upstream_git_core_deployed=False,
                  shared_receipt_sha256=sha(shared/'git-next-shared-runtime.json'),
                  product_executed=False,tests_executed=False,installed=False,complete_git_parity=compatibility['complete_git_parity'],release_eligible=False)
    (destination/'git-next-distribution.json').write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({'status':report['status'],'platform':target,'files':len(records),'product_executed':False}))


if __name__ == '__main__': main()
