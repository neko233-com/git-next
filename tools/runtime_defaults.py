"""The product's runtime configuration defaults and their renderings.

The fragment under packaging/runtime-defaults/gitconfig is the only place a
product default is written down. Everything else - the payload configuration,
the migration override and the MSI evidence reuse check - is rendered from it by
this module, so a default cannot quietly differ between an installed payload and
a migrated system configuration.
"""

import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
FRAGMENT = ROOT / "packaging" / "runtime-defaults" / "gitconfig"


def _entries():
    text = FRAGMENT.read_bytes().decode("utf-8").replace("\r\n", "\n")
    sections = []
    for line in text.split("\n"):
        if not line or line.startswith("#"):
            continue
        if line.startswith("["):
            sections.append([line, []])
        else:
            sections[-1][1].append(line)
    return sections


def settings():
    """Every default as (section, key, value), in the order the file lists them."""
    out = []
    for header, body in _entries():
        section = header.strip("[]")
        for line in body:
            key, _, value = line.strip().partition(" = ")
            out.append((section, key, value))
    return out


def core_body():
    """The [core] lines, used to replace the vendor's autocrlf line in place.

    The payload configuration keeps the vendor's own [core] section for the
    settings the vendor owns, so the product's core values are spliced into that
    section rather than appended as a second one that would have to win.
    """
    for header, body in _entries():
        if header == "[core]":
            return "".join(line + "\n" for line in body)
    raise ValueError("The defaults fragment has no [core] section")


def tail():
    """Every section after [core], as it is appended to the payload configuration."""
    return "".join(
        header + "\n" + "".join(line + "\n" for line in body)
        for header, body in _entries()
        if header != "[core]"
    )


def block_text():
    """The whole fragment as a configuration block, comments removed.

    This is what an installer writes into a configuration file, and what
    tools/native_msi_evidence.py expects a packaged payload to end with.
    """
    return "[core]\n" + core_body() + tail()


def powershell_literal():
    """The double-quoted PowerShell literal the migration writes, without its quotes.

    The migration configuration is not a file the installer copies, so its text
    has to exist as a literal in the module. tools/check_runtime_defaults.py
    compares that literal against this rendering, which is what stops the literal
    from drifting away from the file.
    """
    parts = []
    for header, body in _entries():
        parts.append(header)
        parts.extend(body)
    return "".join(part.replace("\t", "`t") + "`n" for part in parts)
