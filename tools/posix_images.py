"""Static native image inspection and relocation; never load target code."""
from pathlib import Path
import re
import struct
import subprocess

from build_native_posix import image_platform

ELF_SYSTEM_LIBRARIES = {'libc.so.6','libm.so.6','libpthread.so.0','libdl.so.2','librt.so.1',
                        'libutil.so.1','libresolv.so.2','ld-linux-x86-64.so.2','ld-linux-aarch64.so.1'}


def text(*command):
    return subprocess.check_output(command,text=True,stderr=subprocess.PIPE)


def elf_needed(path):
    data = text('readelf','--wide','--dynamic',str(path))
    return re.findall(r'\(NEEDED\).*?\[([^\]]+)\]',data)


def macho_needed(path):
    values = [line.strip().split(' (compatibility version',1)[0] for line in text('otool','-L',str(path)).splitlines()[1:] if line.strip()]
    with path.open('rb') as stream: header = stream.read(16)
    if header[:4] == b'\xcf\xfa\xed\xfe' and struct.unpack_from('<I',header,12)[0] == 6:
        identifiers = text('otool','-D',str(path)).splitlines()[1:]
        values = [value for value in values if value not in identifiers]
    return values


def macho_system(name):
    return name.startswith(('/usr/lib/','/System/Library/'))


def image_needed(path):
    target = image_platform(path)
    if target and target.startswith('linux-'): return elf_needed(path)
    if target == 'macos-arm64': return macho_needed(path)
    return []


def relocate_elf(path,libraries):
    import os
    with path.open('rb') as stream:
        header = stream.read(64)
        offset = struct.unpack_from('<Q',header,32)[0]
        width,count = struct.unpack_from('<HH',header,54)
        if width < 4 or count > 4096: raise ValueError('Invalid ELF program header table')
        stream.seek(offset); headers = stream.read(width*count)
    if len(headers) != width*count: raise ValueError('Truncated ELF program header table')
    if not any(struct.unpack_from('<I',headers,index*width)[0] == 2 for index in range(count)): return
    relative = os.path.relpath(libraries,path.parent).replace(os.sep,'/')
    subprocess.run(['patchelf','--set-rpath','$ORIGIN/'+relative,str(path)],check=True)


def relocate_macho(path,root,library_map):
    import os
    changed = False
    for dependency in macho_needed(path):
        if macho_system(dependency): continue
        if dependency.startswith('@loader_path/'):
            candidate = (path.parent/dependency[len('@loader_path/'):]).resolve()
            if candidate.is_relative_to(root) and candidate.is_file(): continue
        target = library_map.get(dependency)
        if not target: raise ValueError('Missing explicit owned Mach-O library mapping: '+dependency)
        candidate = (root/target).resolve(strict=True)
        if not candidate.is_relative_to(root): raise ValueError('Mach-O library mapping escapes its package')
        replacement = '@loader_path/'+os.path.relpath(candidate,path.parent).replace(os.sep,'/')
        subprocess.run(['install_name_tool','-change',dependency,replacement,str(path)],check=True)
        changed = True
    # Relocation changes native signatures; no signing identity is fabricated.
    if changed: subprocess.run(['codesign','--force','--sign','-','--timestamp=none','--preserve-metadata=entitlements,flags,runtime',str(path)],check=True)
