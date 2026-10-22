"""Read the tables of an MSI package without installing it.

`msiexec` is the only thing that installs a package, but the database is a plain
structured file: opening it with the installer API is enough to see what the package
claims it will do. That is how a build is inspected before it is allowed near a
machine - properties, scope, product identity, directory layout and the file table.

Usage:
    python tools/msi_inspect.py <package.msi> [--files] [--dirs] [--properties]
"""
from __future__ import annotations

import argparse
import ctypes
import ctypes.wintypes as wintypes
import sys

MSIDBOPEN_READONLY = 0
MSI_NULL_INTEGER = 0x80000000


class _Reader(ctypes.Structure):
    """Opaque MSIHANDLE types used by the installer API."""

    _fields_ = [('value', ctypes.c_uint)]


msi = ctypes.WinDLL('msi', use_last_error=True)
msi.MsiOpenDatabaseW.argtypes = [wintypes.LPCWSTR, ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint)]
msi.MsiOpenDatabaseW.restype = ctypes.c_uint
msi.MsiDatabaseOpenViewW.argtypes = [ctypes.c_uint, wintypes.LPCWSTR, ctypes.POINTER(ctypes.c_uint)]
msi.MsiDatabaseOpenViewW.restype = ctypes.c_uint
msi.MsiViewExecute.argtypes = [ctypes.c_uint, ctypes.c_uint]
msi.MsiViewExecute.restype = ctypes.c_uint
msi.MsiViewFetch.argtypes = [ctypes.c_uint, ctypes.POINTER(ctypes.c_uint)]
msi.MsiViewFetch.restype = ctypes.c_uint
msi.MsiRecordGetStringW.argtypes = [
    ctypes.c_uint,
    ctypes.c_uint,
    wintypes.LPWSTR,
    ctypes.POINTER(ctypes.c_uint),
]
msi.MsiRecordGetStringW.restype = ctypes.c_uint
msi.MsiRecordGetInteger.argtypes = [ctypes.c_uint, ctypes.c_uint]
msi.MsiRecordGetInteger.restype = ctypes.c_int
msi.MsiCloseHandle.argtypes = [ctypes.c_uint]
msi.MsiCloseHandle.restype = ctypes.c_uint

ERROR_NO_MORE_ITEMS = 259
ERROR_MORE_DATA = 234
ERROR_SUCCESS = 0


def _field(record: int, index: int) -> str:
    """Read one string field of a record, growing the buffer as needed."""
    size = ctypes.c_uint(0)
    msi.MsiRecordGetStringW(record, index, None, ctypes.byref(size))
    buffer = ctypes.create_unicode_buffer(size.value + 1)
    size = ctypes.c_uint(len(buffer))
    code = msi.MsiRecordGetStringW(record, index, buffer, ctypes.byref(size))
    if code not in (ERROR_SUCCESS, ERROR_MORE_DATA):
        return ''
    return buffer.value


def query(database: int, sql: str) -> list[list[str]]:
    """Run a query and return every row as a list of strings."""
    view = ctypes.c_uint(0)
    if msi.MsiDatabaseOpenViewW(database, sql, ctypes.byref(view)) != ERROR_SUCCESS:
        raise SystemExit(f'cannot open a view for: {sql}')
    msi.MsiViewExecute(view, 0)
    rows: list[list[str]] = []
    while True:
        record = ctypes.c_uint(0)
        code = msi.MsiViewFetch(view, ctypes.byref(record))
        if code == ERROR_NO_MORE_ITEMS:
            break
        if code != ERROR_SUCCESS:
            raise SystemExit(f'cannot fetch a row for: {sql}')
        fields = ctypes.c_uint(0)
        # The record knows how many fields it has; probe until a read fails.
        values: list[str] = []
        index = 1
        while True:
            value = _field(record, index)
            if value == '' and index > 64:
                break
            values.append(value)
            index += 1
            if index > 32:
                break
        rows.append(values)
        msi.MsiCloseHandle(record)
    msi.MsiCloseHandle(view)
    return rows


def open_database(path: str) -> int:
    """Open a package read-only."""
    handle = ctypes.c_uint(0)
    code = msi.MsiOpenDatabaseW(path, ctypes.c_void_p(MSIDBOPEN_READONLY), ctypes.byref(handle))
    if code != ERROR_SUCCESS:
        raise SystemExit(f'cannot open {path}: installer error {code}')
    return handle


def properties(database: int) -> dict[str, str]:
    """The Property table as a mapping."""
    return {row[0]: row[1] if len(row) > 1 else '' for row in query(database, 'SELECT `Property`, `Value` FROM `Property`')}


def main() -> int:
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('package')
    parser.add_argument('--properties', action='store_true', help='print every property')
    parser.add_argument('--dirs', action='store_true', help='print the directory table')
    parser.add_argument('--files', type=int, default=0, help='print the first N files')
    arguments = parser.parse_args()

    database = open_database(arguments.package)
    values = properties(database)
    for key in (
        'ProductName',
        'ProductVersion',
        'ProductCode',
        'UpgradeCode',
        'Manufacturer',
        'ALLUSERS',
        'MSIINSTALLPERUSER',
        'INSTALLFOLDER',
        'OFFICIALGIT',
        'REPLACEGIT',
    ):
        if key in values:
            print(f'{key:20} {values[key]}')

    counts = {}
    for table in ('File', 'Component', 'Directory', 'Registry', 'Shortcut', 'CustomAction', 'InstallExecuteSequence'):
        rows = query(database, f'SELECT * FROM `{table}`')
        counts[table] = len(rows)
    print('tables: ' + ', '.join(f'{name}={count}' for name, count in counts.items()))

    if arguments.properties:
        print()
        for key in sorted(values):
            print(f'{key:32} {values[key]}')

    if arguments.dirs:
        print()
        for row in query(database, 'SELECT `Directory`, `Directory_Parent`, `DefaultDir` FROM `Directory` ORDER BY `Directory`'):
            print('  ' + ' | '.join(row))

    if arguments.files:
        print()
        rows = query(database, 'SELECT `File`, `Component_`, `FileName`, `FileSize` FROM `File` ORDER BY `File`')
        for row in rows[: arguments.files]:
            print('  ' + ' | '.join(row))

    msi.MsiCloseHandle(database)
    return 0


if __name__ == '__main__':
    sys.exit(main())
