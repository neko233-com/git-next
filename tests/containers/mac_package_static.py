"""Inspect a macOS package and parse its shell entrypoints in disposable Linux Docker."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import struct
import subprocess
from package_input import unpack


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream,'sha256').hexdigest()


def managed_assembly(path):
    with path.open('rb') as stream:
        data = stream.read(64)
        if len(data) != 64 or data[:2] != b'MZ': return False
        offset = struct.unpack_from('<I',data,60)[0]
        if offset < 64 or offset > 1024*1024: return False
        stream.seek(offset)
        header = stream.read(264)
    if len(header) < 248 or header[:4] != b'PE\0\0': return False
    magic = struct.unpack_from('<H',header,24)[0]
    directory = 24 + (112 if magic == 0x20b else 96 if magic == 0x10b else 0)
    if directory == 24 or struct.unpack_from('<I',header,directory-4)[0] < 15: return False
    rva,size = struct.unpack_from('<II',header,directory+14*8)
    return rva != 0 and size >= 72


def main():
    parser = argparse.ArgumentParser(__doc__)
    source=parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--package',type=Path)
    source.add_argument('--archive',type=Path)
    parser.add_argument('--output',required=True,type=Path)
    args = parser.parse_args()
    root = args.package.resolve(strict=True) if args.package else unpack(args.archive,Path('/tmp/mac-package'))
    manifest = json.loads((root/'package.json').read_text())
    if manifest['schema_version'] != 1 or manifest['product'] != 'git-next' or manifest['platform'] != 'macos-arm64': raise ValueError('Unexpected package identity')
    failures = []; images = []; scripts = []
    for name,item in manifest['files'].items():
        relative = PurePosixPath(name)
        try:
            if relative.is_absolute() or '..' in relative.parts or '\\' in name or ':' in name: raise ValueError('Unsafe package member')
            path = root.joinpath(*relative.parts)
            if not path.resolve(strict=True).is_relative_to(root): raise ValueError('Package member escaped its root')
            if item['kind'] == 'symlink':
                if not path.is_symlink() or os.readlink(path) != item['target']: raise ValueError('Package link differs')
                continue
            if path.is_symlink() or path.stat().st_size != item['bytes'] or digest(path) != item['sha256']: raise ValueError('Package bytes differ')
            if path.stat().st_mode & 0o777 != item['mode']: raise ValueError('Package mode differs')
            with path.open('rb') as stream: header = stream.read(64); prefix = header + stream.read(8192)
            if header[:4] == b'\xcf\xfa\xed\xfe':
                if len(header) < 32 or struct.unpack_from('<I',header,4)[0] != 0x100000c: raise ValueError('Native image is not ARM64 Mach-O')
                images.append(name)
            elif header.startswith(b'MZ'):
                if (not name.startswith('runtime/usr/lib/git-next/gcm/') or not name.endswith('.dll')
                        or not managed_assembly(path)):
                    raise ValueError('Unexpected native Windows image')
            elif header.startswith(b'\x7fELF') or header[:4] in (b'\xca\xfe\xba\xbe',b'\xbe\xba\xfe\xca',b'\xca\xfe\xba\xbf'):
                raise ValueError('Unexpected or uninspected native image')
            elif prefix.startswith((b'#!/bin/sh',b'#!/bin/bash',b'#!/usr/bin/env bash',b'#!/usr/bin/env sh')):
                data = path.read_bytes()
                # Tcl dual-language entrypoints contain a shell preamble, then Tcl data.
                hybrid = re.compile(rb'exec\s+[\'\"]?(wish|tclsh)(?:[0-9]+(?:\.[0-9]+)*)?[\'\"]?\s')
                if hybrid.search(data[:8192]):
                    lines = data.splitlines(keepends=True); preamble = []
                    for line in lines:
                        preamble.append(line)
                        if hybrid.search(line): break
                    data = b''.join(preamble)
                parsed = subprocess.run(['/bin/bash','-n'],input=data,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                if parsed.returncode: raise ValueError('Shell syntax refused')
                scripts.append(name)
        except (OSError,ValueError,KeyError):
            failures.append(name)
    if not images or not scripts: failures.append('missing-native-or-shell-inputs')
    report = dict(schema_version=1,status='passed' if not failures else 'failed',platform='macos-arm64',
                  acceptance_scope='linux-docker-package-hashes-arm64-mach-o-and-shell-syntax',
                  package_manifest_sha256=digest(root/'package.json'),files_inspected=len(manifest['files']),
                  native_images=images,shell_entrypoints=scripts,failures=failures,
                  macos_product_executed=False,macos_kernel_keychain_runtime_verified=False,
                  host_profiles_mounted=False)
    if args.output.exists(): raise ValueError('Acceptance output must be new')
    args.output.write_text(json.dumps(report,indent=2)+'\n',encoding='utf-8',newline='\n')
    print(json.dumps({key:report[key] for key in ('status','platform','acceptance_scope','macos_product_executed')}))
    return bool(failures)


if __name__ == '__main__': raise SystemExit(main())
