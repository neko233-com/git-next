"""Apply pinned git-next core patches and generate native inputs; never run the core."""
import argparse
import ast
import difflib
import hashlib
import json
from pathlib import Path
import re
from native_product import product_version as native_version_value, native_compatibility

ROOT = Path(__file__).resolve().parents[1]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def write_json(path, value):
    write_changed(path, (json.dumps(value, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))


def write_changed(path, data):
    if not path.exists() or path.read_bytes() != data:
        path.write_bytes(data)


def apply_patch(source, patch):
    lines = patch.splitlines(keepends=True)
    original = source.splitlines(keepends=True)
    result = []
    position = 0
    i = 2
    while i < len(lines):
        match = re.fullmatch(r'@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@[^\n]*\n?', lines[i])
        if not match:
            raise ValueError('Unexpected native patch hunk header')
        start = int(match[1]) - 1
        if start < position:
            raise ValueError('Overlapping native patch hunks')
        result.extend(original[position:start])
        position = start
        removed = added = 0
        i += 1
        while i < len(lines) and not lines[i].startswith('@@ '):
            line = lines[i]
            if not line or line[0] not in ' +-':
                raise ValueError('Unexpected native patch line')
            if line[0] in ' -':
                if position >= len(original) or original[position] != line[1:]:
                    raise ValueError('Native patch context does not match its pinned source')
                position += 1
                removed += 1
            if line[0] in ' +':
                result.append(line[1:])
                added += 1
            i += 1
        if removed != int(match[2] or 1) or added != int(match[4] or 1):
            raise ValueError('Native patch hunk count mismatch')
    result.extend(original[position:])
    return ''.join(result)


def po_messages(text):
    entry = {}
    field = None
    fuzzy = False
    for line in text.splitlines() + ['']:
        if not line.strip():
            if entry.get('msgid') and not fuzzy and 'msgctxt' not in entry:
                translated = entry.get('msgstr', entry.get('msgstr[0]', ''))
                if translated:
                    yield entry['msgid'], translated
            entry, field, fuzzy = {}, None, False
        elif line.startswith('#,'):
            fuzzy |= 'fuzzy' in [flag.strip() for flag in line.split(',')]
        elif line.startswith('#'):
            continue
        elif line.startswith('"'):
            if field is None:
                raise ValueError('PO continuation without field')
            entry[field] += ast.literal_eval(line)
        else:
            field, quoted = line.split(' ', 1)
            if field not in ('msgid', 'msgid_plural', 'msgstr', 'msgstr[0]', 'msgctxt'):
                if field.startswith('msgstr['):
                    entry[field] = ast.literal_eval(quoted)
                    continue
                raise ValueError('Unsupported PO field: ' + field)
            entry[field] = ast.literal_eval(quoted)


def c_literal(value):
    # xgettext represents C's platform-specific integer format macros this way.
    parts = re.split(r'(<PRI[A-Za-z0-9_]+>)', value)
    return ' '.join(part[1:-1] if re.fullmatch(r'<PRI[A-Za-z0-9_]+>', part)
                    else json.dumps(part, ensure_ascii=False) for part in parts)


def generate_catalog(source):
    po = source / 'po/zh_CN.po'
    messages = dict(po_messages(po.read_text(encoding='utf-8')))
    additional = ROOT / 'native-core/locale/zh_CN.json'
    upstream_count = len(messages)
    messages.update(json.loads(additional.read_text(encoding='utf-8')))
    ordinary = []
    portable = []
    for english, chinese in messages.items():
        if '\0' in english or '\0' in chinese:
            raise ValueError('NUL in native translation catalog')
        (portable if re.search(r'<PRI[A-Za-z0-9_]+>', english) else ordinary).append((english, chinese))
    header = ['/* Generated from pinned Git po/zh_CN.po. SPDX-License-Identifier: GPL-2.0-only */',
              '/* Contributor attribution and source remain in po/zh_CN.po and the source receipt. */',
              'struct git_next_translation { const char *english; const char *chinese; };']
    for name, entries in [('git_next_catalog', ordinary), ('git_next_portable_catalog', portable)]:
        header.append('static const struct git_next_translation ' + name + '[] = {')
        for english, chinese in sorted(entries, key=lambda item: item[0].encode('utf-8')):
            header.append('\t{ ' + c_literal(english) + ', ' + c_literal(chinese) + ' },')
        header.append('};')
    data = ('\n'.join(header) + '\n').encode('utf-8')
    write_changed(source / 'git-next-catalog.h', data)
    return dict(source='po/zh_CN.po', source_sha256=digest(po.read_bytes()),
                generated_sha256=digest(data), translations=len(messages), upstream_translations=upstream_count,
                additional_source='native-core/locale/zh_CN.json', additional_source_sha256=digest(additional.read_bytes()),
                portable_formats=len(portable), fuzzy_entries_excluded=True)


def integrate(source):
    root = source.resolve(strict=True)
    pin = json.loads((ROOT / 'packaging/native-core.lock.json').read_text())
    prepared = json.loads((root / 'git-next-source-receipt.json').read_text())
    if prepared['commit'] != pin['commit'] or prepared['archive_sha256'] != pin['archive_sha256']:
        raise ValueError('Native source preparation receipt does not match the pin')
    series = json.loads((ROOT / 'native-core/patches/series.json').read_text())
    current = {}
    # Validate all original files before writing, including previously applied declared patches.
    allowed = {}
    chains = {}
    for entry in series:
        allowed.setdefault(entry['path'], set()).update([entry['before_sha256'], entry['after_sha256']])
        chain = chains.setdefault(entry['path'], [entry['before_sha256']])
        if chain[-1] != entry['before_sha256']:
            raise ValueError('Discontinuous native patch chain: ' + entry['path'])
        chain.append(entry['after_sha256'])
    for relative, expected in prepared['files'].items():
        path = root / relative
        actual = digest(path.read_bytes())
        if actual != expected and actual not in allowed.get(relative, set()):
            raise ValueError('Undeclared upstream modification: ' + relative)
        current[relative] = actual
    patches = []
    ordinals = {}
    for entry in series:
        patch_path = ROOT / 'native-core/patches' / entry['patch']
        patch_bytes = patch_path.read_bytes()
        if digest(patch_bytes) != entry['patch_sha256']:
            raise ValueError('Native patch checksum mismatch')
        target = root / entry['path']
        actual = digest(target.read_bytes())
        ordinal = ordinals.get(entry['path'], 0) + 1
        ordinals[entry['path']] = ordinal
        applied = chains[entry['path']].index(actual)
        if applied >= ordinal:
            pass
        elif actual == entry['before_sha256']:
            changed = apply_patch(target.read_text(encoding='utf-8'), patch_bytes.decode('utf-8'))
            data = changed.encode('utf-8')
            if digest(data) != entry['after_sha256']:
                raise ValueError('Native patch result checksum mismatch')
            target.write_bytes(data)
        elif actual != entry['after_sha256']:
            raise ValueError('Native patch input checksum mismatch: ' + entry['path'])
        patches.append(entry)
    overlay = {}
    for path in (ROOT / 'native-core/overlay').glob('*'):
        if not path.is_file():
            continue
        data = path.read_bytes()
        write_changed(root / path.name, data)
        overlay[path.name] = digest(data)
    for path in (ROOT / 'native-core/generated').glob('*.h'):
        data = path.read_bytes()
        write_changed(root / path.name, data)
        overlay[path.name] = digest(data)
    vendor = ROOT / 'native-core/vendor/yyjson'
    provenance = json.loads((vendor / 'SOURCE.json').read_text())
    for name, expected in provenance['files'].items():
        data = (vendor / name).read_bytes()
        if digest(data) != expected:
            raise ValueError('yyjson pinned source checksum mismatch')
        if name != 'LICENSE':
            write_changed(root / name, data)
            overlay[name] = expected
    product_version = native_version_value(ROOT)
    native_version = (pin['tag'].removeprefix('v') + '.git-next.' + product_version + '\n').encode('ascii')
    write_changed(root / 'version', native_version)
    overlay['version'] = digest(native_version)
    compatibility = native_compatibility(ROOT)
    version = dict(product='git-next', version=product_version, engine='git-next native C core',
                   compatibility_baseline=pin['tag'], complete_git_parity=compatibility['complete_git_parity'],
                   external_git_fallback=False, external_ssh_fallback=False)
    header = '/* Generated native product identity. SPDX-License-Identifier: GPL-2.0-only */\n'
    for name, value in [('GIT_NEXT_PRODUCT_VERSION', product_version),
                        ('GIT_NEXT_VERSION_JSON', json.dumps(version, separators=(',', ':'))),
                        ('GIT_NEXT_COMPATIBILITY_JSON', json.dumps(compatibility, separators=(',', ':')))]:
        header += '#define ' + name + ' ' + json.dumps(value) + '\n'
    header += '#define GIT_NEXT_COMPLETE_GIT_PARITY ' + ('1' if compatibility['complete_git_parity'] else '0') + '\n'
    resource_version = product_version.split('-', 1)[0].split('.')
    header += '#define GIT_NEXT_RESOURCE_VERSION ' + ','.join(resource_version + ['0']) + '\n'
    header += '#define GIT_NEXT_RESOURCE_VERSION_STRING ' + json.dumps(product_version) + '\n'
    identity_bytes = header.encode('utf-8')
    write_changed(root / 'git-next-identity.h', identity_bytes)
    catalog = generate_catalog(root)
    record = dict(status='native-core-integrated-not-runtime-accepted', upstream_commit=pin['commit'],
                  archive_sha256=pin['archive_sha256'], patches=patches, overlay=overlay,
                  identity_sha256=digest(identity_bytes), catalog=catalog, tests_executed=False,
                  runtime_accepted=False, installed=False, complete_git_parity=False)
    write_json(root / 'git-next-integration-receipt.json', record)
    print(json.dumps({key:value for key,value in record.items() if key not in ('overlay', 'patches')}, indent=2))


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--source', type=Path, required=True)
    args = parser.parse_args()
    integrate(args.source)


if __name__ == '__main__':
    main()
