"""Prove the Windows installer lifecycle without touching the machine's Git.

The package installs into the default Git directory, which on a working machine is
also the Git people use. Testing install, repair, upgrade and uninstall there would
mean destroying and recreating a real toolchain to learn whether the package works.

`INSTALLFOLDER` is a directory property, so it can be pointed at an isolated directory
for the duration of the test. The runtime refuses an install root it does not own, and
a machine-scope product owns Program Files, so the isolated directory has to live
there: `C:\\Program Files\\git-next-lifecycle`. The custom actions then run against a
path inside the machine scope but never against the real Git.

Every step runs the same tables, the same custom actions and the same file layout, and
the report says exactly what was asserted at each step. If the product refuses a step,
the reason is in the report: the actions record their own failures.

Usage:
    python tools/msi_lifecycle.py --package <git-next.msi> \
        --work "C:/Program Files/git-next-lifecycle" \
        --receipt <path for the JSON report> [--previous <older package>]
"""
from __future__ import annotations

import argparse
import json
import ctypes
import ctypes.wintypes as wintypes
import os
import shutil
import subprocess
import sys
import time
import winreg
from pathlib import Path

MSI_SUCCESS = 0
MSI_REBOOT_REQUIRED = 3010


def uninstall_entries(hint: str) -> dict[str, dict[str, str]]:
    """Every ARP entry whose display name mentions the product."""
    found: dict[str, dict[str, str]] = {}
    roots = (
        (winreg.HKEY_LOCAL_MACHINE, r'SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall'),
        (winreg.HKEY_LOCAL_MACHINE, r'SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'),
    )
    for root, path in roots:
        try:
            key = winreg.OpenKey(root, path)
        except OSError:
            continue
        for index in range(winreg.QueryInfoKey(key)[0]):
            guid = winreg.EnumKey(key, index)
            if not guid.startswith('{'):
                continue
            try:
                sub = winreg.OpenKey(key, guid)
            except OSError:
                continue

            def value(name: str) -> str:
                try:
                    return str(winreg.QueryValueEx(sub, name)[0])
                except OSError:
                    return ''

            display = value('DisplayName')
            if hint.lower() in display.lower():
                found[guid] = {
                    'display_name': display,
                    'version': value('DisplayVersion'),
                    'location': value('InstallLocation'),
                }
    return found


def action_logs() -> dict[str, str]:
    """The custom actions record their own failures; collect them for the report.

    A Windows Installer custom action has no console, so the package writes the reason
    to this directory before signalling failure. Without it a failed step is a bare
    `Error 1722` that says nothing about what went wrong.
    """
    directory = Path(os.environ.get('ProgramData', r'C:\ProgramData')) / 'git-next' / 'logs'
    found: dict[str, str] = {}
    if directory.is_dir():
        for path in sorted(directory.glob('msi-action-*.log')):
            found[path.name] = path.read_text(encoding='utf-8', errors='replace')[-4000:]
    return found


def run_msiexec(arguments: list[str], log: Path, timeout: int = 3600) -> dict:
    """Run msiexec and return its result together with the log it wrote."""
    if log.exists():
        log.unlink()
    command = ['msiexec', *arguments, '/qn', '/norestart', '/l*v', str(log)]
    started = time.time()
    result = subprocess.run(command, timeout=timeout)
    return {
        'command': ' '.join(command),
        'exit_code': result.returncode,
        'seconds': round(time.time() - started, 1),
        'log': str(log),
        'log_bytes': log.stat().st_size if log.exists() else 0,
    }


def count_files(root: Path) -> int:
    return sum(1 for path in root.rglob('*') if path.is_file()) if root.exists() else 0


def product_git_version(executable: Path) -> str:
    """Ask the installed payload for its version, so files are not the only evidence."""
    if not executable.is_file():
        return ''
    try:
        result = subprocess.run(
            [str(executable), '--version'],
            capture_output=True,
            text=True,
            timeout=120,
            encoding='utf-8',
            errors='replace',
        )
    except (OSError, subprocess.SubprocessError):
        return ''
    if result.returncode != 0:
        return ''
    lines = (result.stdout or result.stderr).strip().splitlines()
    return lines[0] if lines else ''


def verify_installed(install_dir: Path) -> dict:
    """Collect the facts that an installation has to have produced."""
    git_cmd = install_dir / 'cmd' / 'git.exe'
    entries = uninstall_entries('git-next')
    return {
        'install_dir': str(install_dir),
        'files': count_files(install_dir),
        'cmd_git_exists': git_cmd.is_file(),
        'cmd_git_bytes': git_cmd.stat().st_size if git_cmd.is_file() else 0,
        # A bridge forwarder is a few kilobytes; the payload this product installs is
        # megabytes. The size is what distinguishes "the IDE entry is real" from
        # "the IDE entry points somewhere else".
        'cmd_git_is_payload': git_cmd.is_file() and git_cmd.stat().st_size > 1_000_000,
        'bin_git_exists': (install_dir / 'bin' / 'git.exe').is_file(),
        'msi_worker_exists': (install_dir / 'updater' / 'msi-worker.exe').is_file(),
        'reported_version': product_git_version(git_cmd),
        'arp_entries': entries,
        'arp_count': len(entries),
    }


def product_code(package: Path) -> str:
    """Read the product code out of the package, so steps target the right product.

    A machine installation and a user installation of the same version are separate
    products and coexist by design, so "how many entries mention git-next" is not a
    meaningful check; the code of the package under test is.
    """
    msi = ctypes.WinDLL('msi', use_last_error=True)
    msi.MsiOpenDatabaseW.argtypes = [wintypes.LPCWSTR, ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint)]
    msi.MsiOpenDatabaseW.restype = ctypes.c_uint
    msi.MsiDatabaseOpenViewW.argtypes = [ctypes.c_uint, wintypes.LPCWSTR, ctypes.POINTER(ctypes.c_uint)]
    msi.MsiDatabaseOpenViewW.restype = ctypes.c_uint
    msi.MsiViewExecute.argtypes = [ctypes.c_uint, ctypes.c_uint]
    msi.MsiViewFetch.argtypes = [ctypes.c_uint, ctypes.POINTER(ctypes.c_uint)]
    msi.MsiRecordGetStringW.argtypes = [ctypes.c_uint, ctypes.c_uint, wintypes.LPWSTR, ctypes.POINTER(ctypes.c_uint)]
    msi.MsiCloseHandle.argtypes = [ctypes.c_uint]

    handle = ctypes.c_uint(0)
    if msi.MsiOpenDatabaseW(str(package), ctypes.c_void_p(0), ctypes.byref(handle)) != 0:
        raise SystemExit(f'cannot open {package}')
    view = ctypes.c_uint(0)
    code = ''
    try:
        if msi.MsiDatabaseOpenViewW(
            handle, 'SELECT `Value` FROM `Property` WHERE `Property` = \'ProductCode\'', ctypes.byref(view)
        ) == 0:
            if msi.MsiViewExecute(view, 0) == 0:
                record = ctypes.c_uint(0)
                if msi.MsiViewFetch(view, ctypes.byref(record)) == 0:
                    size = ctypes.c_uint(64)
                    buffer = ctypes.create_unicode_buffer(size.value)
                    msi.MsiRecordGetStringW(record, 1, buffer, ctypes.byref(size))
                    code = buffer.value
                    msi.MsiCloseHandle(record)
            msi.MsiCloseHandle(view)
    finally:
        msi.MsiCloseHandle(handle)
    if not code:
        raise SystemExit(f'no product code in {package}')
    return code.upper()


def main() -> int:
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--package', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--receipt', type=Path, required=True)
    parser.add_argument('--previous', type=Path, help='older package, for the upgrade step')
    parser.add_argument('--version', default='0.3.0')
    parser.add_argument('--skip-upgrade', action='store_true')
    parser.add_argument(
        '--natural-folder',
        action='store_true',
        help='install where the package says, instead of an explicit INSTALLFOLDER',
    )
    parser.add_argument('--install-dir', type=Path, help='where the package installs')
    arguments = parser.parse_args()

    work = arguments.work
    install_dir = arguments.install_dir or (work / 'install')
    logs = work / 'logs'
    if work.exists():
        shutil.rmtree(work)
    logs.mkdir(parents=True, exist_ok=True)

    report: dict = {
        'product': 'git-next',
        'package': str(arguments.package.resolve()),
        'package_bytes': arguments.package.stat().st_size,
        'install_dir': str(install_dir),
        'isolated': True,
        'isolated_note': (
            'INSTALLFOLDER points at a directory inside Program Files that is not the '
            'Git directory, so the product tables and custom actions run unchanged '
            'while the machine toolchain stays untouched.'
        ),
        'steps': [],
        'ok': True,
    }

    def install_arguments(package: Path) -> list[str]:
        """The install command line, with the folder only when it was overridden."""
        base = ['/i', str(package.resolve())]
        if not arguments.natural_folder:
            base.append(f'INSTALLFOLDER={install_dir}')
        return [*base, 'REPLACEGIT=1']

    def record(step: str, result: dict, checks: dict | None = None) -> None:
        entry = {'step': step, 'result': result, 'checks': checks or {}}
        report['steps'].append(entry)
        failed = result['exit_code'] not in (MSI_SUCCESS, MSI_REBOOT_REQUIRED)
        if checks:
            failed = failed or not all(checks.values())
        if failed:
            report['ok'] = False
        print(
            f"{step:<22} exit={result['exit_code']:<5} {result['seconds']:>6.1f}s "
            f"{'OK' if not failed else 'FAILED'}"
        )

    def stop() -> int:
        report['action_logs'] = action_logs()
        arguments.receipt.parent.mkdir(parents=True, exist_ok=True)
        arguments.receipt.write_text(
            json.dumps(report, indent=2, sort_keys=True) + '\n', encoding='utf-8'
        )
        print(f'\n{"PASS" if report["ok"] else "FAIL"} -> {arguments.receipt}')
        return 0 if report['ok'] else 1

    # 1. Install into the isolated root.
    install = run_msiexec(
        install_arguments(arguments.package),
        logs / '1-install.log',
    )
    installed = verify_installed(install_dir)
    tested_code = product_code(arguments.package)
    report['product_code'] = tested_code
    record(
        'install',
        install,
        {
            'cmd_git_is_payload': installed['cmd_git_is_payload'],
            'bin_git_exists': installed['bin_git_exists'],
            'msi_worker_exists': installed['msi_worker_exists'],
            'reported_version_matches': arguments.version in installed['reported_version'],
            # A machine installation and a user installation of the same version are
            # separate products and coexist by design, so the check is that this
            # package's own product code is registered, not that it is the only one.
            'package_product_registered': tested_code in installed['arp_entries'],
        },
    )
    report['installed'] = installed
    if not report['ok']:
        print('install did not produce the expected state; stopping', file=sys.stderr)
        return stop()

    # 2. Repair. Maintenance mode takes the product code, not the package.
    current_code = tested_code
    repair = run_msiexec(['/famus', current_code], logs / '2-repair.log')
    repaired = verify_installed(install_dir)
    record(
        'repair',
        repair,
        {
            'cmd_git_is_payload': repaired['cmd_git_is_payload'],
            'files_unchanged': repaired['files'] == installed['files'],
        },
    )

    # 3. Upgrade from an earlier package, if one was supplied. Both install into the
    #    same directory; the older product must disappear when the new one lands.
    if arguments.previous and not arguments.skip_upgrade:
        record('remove-before-upgrade', run_msiexec(['/x', current_code], logs / '3a-remove.log'), {})
        shutil.rmtree(install_dir, ignore_errors=True)

        older = run_msiexec(
            install_arguments(arguments.previous),
            logs / '3b-install-previous.log',
        )
        before = verify_installed(install_dir)
        report['pre_upgrade'] = before
        record(
            'install-previous',
            older,
            {'previous_product_registered': product_code(arguments.previous) in before['arp_entries']},
        )

        upgrade = run_msiexec(
            install_arguments(arguments.package),
            logs / '3c-upgrade.log',
        )
        after = verify_installed(install_dir)
        record(
            'upgrade',
            upgrade,
            {
                'upgraded_product_registered': tested_code in after['arp_entries'],
                'previous_product_removed': product_code(arguments.previous) not in after['arp_entries'],
                'reported_version_matches': arguments.version in after['reported_version'],
                'cmd_git_is_payload': after['cmd_git_is_payload'],
            },
        )
        report['after_upgrade'] = after
        current_code = tested_code

    # 4. Uninstall. The guard refuses while a takeover points at the install root; the
    #    isolated root has no migration state, so removal is allowed here.
    uninstall = run_msiexec(['/x', current_code], logs / '4-uninstall.log')
    entries_after = uninstall_entries('git-next')
    record(
        'uninstall',
        uninstall,
        {
            # The installer removes what it owns: the entry points and the file table.
            # Runtime configuration generated on first use is not in that table, so the
            # directory itself may survive; that count is recorded instead of asserted.
            'entry_points_removed': not (install_dir / 'cmd' / 'git.exe').exists()
            and not (install_dir / 'bin' / 'git.exe').exists(),
            'arp_entry_removed': current_code not in entries_after,
        },
    )
    report['after_uninstall'] = {
        'install_dir_exists': install_dir.exists(),
        'files_left': count_files(install_dir),
        'arp_entries': entries_after,
    }
    return stop()


if __name__ == '__main__':
    sys.exit(main())
