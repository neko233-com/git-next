#!/usr/bin/env python3
# Copyright 2026 git-next contributors. MIT license.
"""Restore the git-next payload at the Windows entry points and drop looping bridges.

A bridge forwarder resolves what to run from its own registration. When that
registration names the forwarder itself, every invocation starts another copy of the
same process and the chain never ends: thousands of ``git.exe`` images, one per link.
The forwarder compares the resolved target with its own module path as strings, so a
recorded ``InstallRoot`` that ends with a separator (``C:\\Program Files\\Git\\``)
produces ``...\\Git\\\\cmd\\git.exe``, which is the same file but not the same string.

This tool repairs a machine that already reached that state:

* it verifies the payload digest before copying anything,
* it replaces ``cmd/git.exe`` and ``bin/git.exe`` when they are still forwarders,
* it deletes every registration whose target resolves to its own forwarder file.

It is idempotent and supports ``--plan`` to report without writing.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import sys
import winreg

BRIDGE_ROOT = r"Software\git-next\Bridges"
MSI_ROOT = r"Software\git-next\MSI"
ENTRY_POINTS = (r"cmd\git.exe", r"bin\git.exe")


def sha256(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def canonical(path: str) -> str:
    """Full path with separators collapsed and any trailing separator removed."""
    return os.path.normcase(os.path.normpath(os.path.abspath(path))).rstrip("\\")


def install_root(explicit: str | None) -> str:
    if explicit:
        return canonical(explicit)
    try:
        with winreg.OpenKey(
            winreg.HKEY_LOCAL_MACHINE, MSI_ROOT + r"\windows-x64", 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY
        ) as key:
            recorded = winreg.QueryValueEx(key, "InstallRoot")[0]
            if recorded:
                return canonical(recorded)
    except OSError:
        pass
    return r"C:\Program Files\Git"


def roots_for_platform(platform: str) -> str:
    try:
        with winreg.OpenKey(
            winreg.HKEY_LOCAL_MACHINE, MSI_ROOT + "\\" + platform, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY
        ) as key:
            return canonical(winreg.QueryValueEx(key, "InstallRoot")[0])
    except OSError:
        return ""


def walk_bridges(hive: int, hive_name: str, path: str):
    """Yield (full_key_path, platform, relative) for every bridge registration."""
    try:
        node = winreg.OpenKey(hive, path, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY)
    except OSError:
        return
    with node:
        try:
            platform = winreg.QueryValueEx(node, "Platform")[0]
            relative = winreg.QueryValueEx(node, "RelativePath")[0]
        except OSError:
            platform = relative = None
        if platform and relative:
            yield path, str(platform), str(relative)
        index = 0
        while True:
            try:
                name = winreg.EnumKey(node, index)
            except OSError:
                break
            index += 1
            yield from walk_bridges(hive, hive_name, path + "\\" + name)


def delete_bridge(hive: int, path: str) -> None:
    parent, leaf = path.rsplit("\\", 1)
    with winreg.OpenKey(hive, parent, 0, winreg.KEY_ALL_ACCESS | winreg.KEY_WOW64_64KEY) as key:
        winreg.DeleteKey(key, leaf)


def emit(report: dict, path: str | None) -> None:
    text = json.dumps(report, indent=2, ensure_ascii=False)
    print(text)
    if path:
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(text)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", help="installation root; defaults to the registered root")
    parser.add_argument("--payload", help="verified git-next payload; defaults to <root>\\runtime\\cmd\\git.exe")
    parser.add_argument("--expected-sha256", help="refuse to copy a payload that does not match this digest")
    parser.add_argument("--plan", action="store_true", help="report only, change nothing")
    parser.add_argument("--report", help="also write the report to this file (an elevated run has no console)")
    args = parser.parse_args()

    root = install_root(args.root)
    payload = os.path.abspath(args.payload) if args.payload else os.path.join(root, "runtime", "cmd", "git.exe")
    report: dict[str, object] = {
        "ok": False,
        "install_root": root,
        "payload": payload,
        "replaced": [],
        "removed_registrations": [],
        "problems": [],
    }

    if not os.path.isfile(payload):
        report["problems"].append(f"payload is missing: {payload}")  # type: ignore[union-attr]
        emit(report, args.report)
        return 1
    payload_digest = sha256(payload)
    report["payload_sha256"] = payload_digest
    if args.expected_sha256 and payload_digest != args.expected_sha256.lower():
        report["problems"].append("payload digest does not match --expected-sha256")  # type: ignore[union-attr]
        emit(report, args.report)
        return 1

    for relative in ENTRY_POINTS:
        entry = os.path.join(root, relative)
        if not os.path.isfile(entry):
            report["problems"].append(f"entry point is missing: {entry}")  # type: ignore[union-attr]
            continue
        before = sha256(entry)
        if before == payload_digest:
            continue
        record = {"path": entry, "before": before, "after": payload_digest if not args.plan else "planned"}
        if not args.plan:
            try:
                shutil.copyfile(payload, entry)
            except PermissionError:
                # A running image holds its own file. The caller has to stop the chain first.
                report["problems"].append(f"{entry} is in use; stop the running git.exe processes first")  # type: ignore[union-attr]
                continue
            if sha256(entry) != payload_digest:
                report["problems"].append(f"{entry} still differs after the copy")  # type: ignore[union-attr]
                continue
        report["replaced"].append(record)  # type: ignore[union-attr]

    scanned = 0
    for hive, hive_name in ((winreg.HKEY_LOCAL_MACHINE, "machine"), (winreg.HKEY_CURRENT_USER, "user")):
        for path, platform, relative in walk_bridges(hive, hive_name, BRIDGE_ROOT):
            scanned += 1
            bridge = path[len(BRIDGE_ROOT) + 1 :]
            platform_root = roots_for_platform(platform) or root
            target = canonical(os.path.join(platform_root, relative))
            if target != canonical(bridge):
                continue
            if not args.plan:
                try:
                    delete_bridge(hive, path)
                except OSError as error:
                    report["problems"].append(f"cannot delete {hive_name} bridge {bridge}: {error}")  # type: ignore[union-attr]
                    continue
            report["removed_registrations"].append(  # type: ignore[union-attr]
                {"hive": hive_name, "bridge": bridge, "relative": relative, "target": target}
            )

    report["bridges_scanned"] = scanned
    report["ok"] = not report["problems"]
    emit(report, args.report)
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
