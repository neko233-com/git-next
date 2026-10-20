"""Read the independent native product identity, without changing the Rust preview."""
import json
from pathlib import Path
import re


def product_version(root: Path) -> str:
    value = json.loads((root / 'packaging/product.json').read_text(encoding='utf-8'))
    if (value.get('schema_version') != 1 or value.get('product') != 'git-next'
            or value.get('engine') != 'source-built-git-next-native-core'
            or not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[A-Za-z0-9.-]+)?', value.get('version', ''))):
        raise ValueError('Invalid native product version metadata')
    return value['version']


def native_compatibility(root: Path) -> dict:
    path = root/'packaging/native-compatibility.json'
    value = json.loads(path.read_text(encoding='utf-8'))
    if (value.get('schema_version') != 1 or value.get('product') != 'git-next'
            or not isinstance(value.get('complete_git_parity'), bool)
            or value.get('external_git_fallback') is not False or value.get('external_ssh_fallback') is not False):
        raise ValueError('Invalid native compatibility declaration')
    return value
