"""Extract the hard-coded catalog into locale files.

The catalog used to be a Rust `match` over message templates. Turning it into data
means adding a language no longer requires touching Rust code, so the entries are
moved into `locales/<code>.json` and the Rust module only loads them.

The parser understands the subset of Rust syntax the catalog used: `"key" => "value",`
arms, where both sides are ordinary Rust string literals, and the value may be wrapped
in a braced block. The extractor is literal-aware, so a message that contains escaped
quotes or newlines is moved verbatim instead of being dropped or rewritten.
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CATALOG = ROOT / 'src' / 'language' / 'catalog.rs'

SIMPLE_ESCAPES = {'n': '\n', 't': '\t', 'r': '\r', '"': '"', '\\': '\\', '0': '\0'}


def read_literal(text: str, start: int) -> tuple[str, int]:
    """Read one Rust string literal starting at `start` and return its value."""
    if text[start] != '"':
        raise ValueError(f'not a string literal at {start}: {text[start : start + 20]!r}')
    index = start + 1
    out: list[str] = []
    while index < len(text):
        char = text[index]
        if char == '\\':
            nxt = text[index + 1]
            out.append(SIMPLE_ESCAPES.get(nxt, nxt))
            index += 2
            continue
        if char == '"':
            return ''.join(out), index + 1
        out.append(char)
        index += 1
    raise ValueError('unterminated string literal')


def parse_arms(body: str) -> list[tuple[str, str]]:
    """Return the `(key, value)` pairs of every `"key" => "value"` arm in `body`."""
    arms: list[tuple[str, str]] = []
    index = 0
    while index < len(body):
        if body[index] != '"':
            index += 1
            continue
        try:
            key, after_key = read_literal(body, index)
        except ValueError:
            index += 1
            continue
        # Only a literal that is directly followed by `=>` opens an arm.
        cursor = after_key
        while cursor < len(body) and body[cursor] in ' \t\r\n':
            cursor += 1
        if not body.startswith('=>', cursor):
            index = after_key
            continue
        cursor += 2
        while cursor < len(body) and body[cursor] in ' \t\r\n':
            cursor += 1
        if cursor >= len(body) or body[cursor] != '"':
            # A braced value: take the first literal inside the braces.
            if cursor < len(body) and body[cursor] == '{':
                depth = 0
                scan = cursor
                while scan < len(body):
                    if body[scan] == '{':
                        depth += 1
                    elif body[scan] == '}':
                        depth -= 1
                        if depth == 0:
                            break
                    scan += 1
                inner = body[cursor : scan + 1]
                value, _ = read_literal(inner, inner.index('"'))
                arms.append((key, value))
                index = scan + 1
                continue
            index = after_key
            continue
        value, after_value = read_literal(body, cursor)
        arms.append((key, value))
        index = after_value
    return arms


def function_body(source: str, name: str) -> str:
    """Return the body of the `fn <name>` function in `source`."""
    start = source.find(f'fn {name}')
    if start < 0:
        raise ValueError(f'function {name} not found')
    brace = source.find('{', start)
    depth = 0
    for index in range(brace, len(source)):
        if source[index] == '{':
            depth += 1
        elif source[index] == '}':
            depth -= 1
            if depth == 0:
                return source[brace + 1 : index]
    raise ValueError(f'unbalanced braces in {name}')


def main() -> int:
    source = CATALOG.read_text(encoding='utf-8')
    messages = dict(parse_arms(function_body(source, 'chinese')))
    abouts = dict(parse_arms(function_body(source, 'command_about')))

    # The command summary table holds the English text, which is the key the
    # message catalog translates. Storing the pair keeps one source of truth.
    commands: dict[str, str] = {}
    for name, english in abouts.items():
        commands[name] = messages.get(english, english)

    target = ROOT / 'locales' / 'zh-Hans.json'
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        json.dumps(
            {
                'code': 'zh-Hans',
                'name': '简体中文',
                'english_name': 'Simplified Chinese',
                'commands': dict(sorted(commands.items())),
                'messages': dict(sorted(messages.items())),
            },
            ensure_ascii=False,
            indent=2,
        )
        + '\n',
        encoding='utf-8',
    )
    print(
        f'wrote {target} with {len(messages)} messages and {len(commands)} command summaries'
    )
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
