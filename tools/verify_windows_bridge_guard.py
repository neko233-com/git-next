#!/usr/bin/env python3
# Copyright 2026 git-next contributors. MIT license.
"""Prove that a legacy bridge refuses to start itself.

The bridge decides what to run from its own registration. When that registration names
the bridge file, the guard has to recognise *the file* and not merely the same text: a
recorded install root ending with a separator turns ``...\\Git`` + ``cmd\\git.exe`` into
``...\\Git\\\\cmd\\git.exe``, which is the same file spelled differently. The old guard
compared strings, so it started a copy of itself, and the copy did the same.

Every scenario runs inside a job object with an active-process limit, so a regression
produces a bounded number of processes instead of a frozen machine. The registry keys the
test writes live under HKCU and are removed afterwards.
"""

from __future__ import annotations

import argparse
import ctypes
import ctypes.wintypes as wintypes
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import winreg

BRIDGE_KEY = r"Software\git-next\Bridges"
MSI_KEY = r"Software\git-next\MSI\windows-x64"
PROCESS_LIMIT = 64

kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
kernel32.OpenProcess.restype = wintypes.HANDLE


class JOBOBJECT_BASIC_LIMIT_INFORMATION(ctypes.Structure):
    _fields_ = [
        ("PerProcessUserTimeLimit", ctypes.c_int64),
        ("PerJobUserTimeLimit", ctypes.c_int64),
        ("LimitFlags", wintypes.DWORD),
        ("MinimumWorkingSetSize", ctypes.c_size_t),
        ("MaximumWorkingSetSize", ctypes.c_size_t),
        ("ActiveProcessLimit", wintypes.DWORD),
        ("Affinity", ctypes.c_size_t),
        ("PriorityClass", wintypes.DWORD),
        ("SchedulingClass", wintypes.DWORD),
    ]


class IO_COUNTERS(ctypes.Structure):
    _fields_ = [(name, ctypes.c_uint64) for name in (
        "ReadOperationCount", "WriteOperationCount", "OtherOperationCount",
        "ReadTransferCount", "WriteTransferCount", "OtherTransferCount")]


class JOBOBJECT_EXTENDED_LIMIT_INFORMATION(ctypes.Structure):
    _fields_ = [
        ("BasicLimitInformation", JOBOBJECT_BASIC_LIMIT_INFORMATION),
        ("IoInfo", IO_COUNTERS),
        ("ProcessMemoryLimit", ctypes.c_size_t),
        ("JobMemoryLimit", ctypes.c_size_t),
        ("PeakProcessMemoryUsed", ctypes.c_size_t),
        ("PeakJobMemoryUsed", ctypes.c_size_t),
    ]


class JOBOBJECT_BASIC_ACCOUNTING_INFORMATION(ctypes.Structure):
    _fields_ = [
        ("TotalUserTime", ctypes.c_int64),
        ("TotalKernelTime", ctypes.c_int64),
        ("ThisPeriodTotalUserTime", ctypes.c_int64),
        ("ThisPeriodTotalKernelTime", ctypes.c_int64),
        ("TotalPageFaultCount", wintypes.DWORD),
        ("TotalProcesses", wintypes.DWORD),
        ("ActiveProcesses", wintypes.DWORD),
        ("TotalTerminatedProcesses", wintypes.DWORD),
    ]


def processes_started(job: int) -> int:
    """Every process the job ever held. One forward is one process; a chain is dozens."""
    info = JOBOBJECT_BASIC_ACCOUNTING_INFORMATION()
    if not kernel32.QueryInformationJobObject(job, 1, ctypes.byref(info), ctypes.sizeof(info), None):
        return -1
    return int(info.TotalProcesses)


def remove_tree(path: str) -> None:
    """rmtree fails above MAX_PATH, so the extended-length form is used for the cleanup."""
    shutil.rmtree("\\\\?\\" + path, ignore_errors=True)


def long_path(path: str) -> str:
    """The bridge registers itself under its long path, so the harness must too."""
    buffer = ctypes.create_unicode_buffer(32768)
    size = kernel32.GetLongPathNameW(path, buffer, 32768)
    return buffer.value if size else path


def confined_job():
    job = kernel32.CreateJobObjectW(None, None)
    info = JOBOBJECT_EXTENDED_LIMIT_INFORMATION()
    info.BasicLimitInformation.LimitFlags = 0x00002000 | 0x00000008  # kill on close, active process limit
    info.BasicLimitInformation.ActiveProcessLimit = PROCESS_LIMIT
    if not kernel32.SetInformationJobObject(job, 9, ctypes.byref(info), ctypes.sizeof(info)):
        raise OSError(ctypes.get_last_error(), "cannot configure the job object")
    return job


def run_confined(job: int, command: list[str], timeout: float) -> tuple[int, float, int]:
    started = time.monotonic()
    process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                               creationflags=0x00000004)  # CREATE_SUSPENDED
    kernel32.AssignProcessToJobObject(job, int(process._handle))  # noqa: SLF001 - the job needs the raw handle
    ctypes.windll.ntdll.NtResumeProcess(int(process._handle))
    try:
        process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        kernel32.TerminateJobObject(job, 1)
        process.wait(timeout=10)
        return -1, time.monotonic() - started, processes_started(job)
    return process.returncode, time.monotonic() - started, processes_started(job)


def register(bridge: str, platform: str, relative: str) -> None:
    key = winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, BRIDGE_KEY + "\\" + bridge, 0,
                             winreg.KEY_ALL_ACCESS | winreg.KEY_WOW64_64KEY)
    with key:
        winreg.SetValueEx(key, "Platform", 0, winreg.REG_SZ, platform)
        winreg.SetValueEx(key, "RelativePath", 0, winreg.REG_SZ, relative)


def forget(bridge: str) -> None:
    parent, leaf = (BRIDGE_KEY + "\\" + bridge).rsplit("\\", 1)
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, parent, 0, winreg.KEY_ALL_ACCESS | winreg.KEY_WOW64_64KEY) as key:
            winreg.DeleteKey(key, leaf)
    except OSError:
        pass


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bridge", required=True, type=str, help="git-next-bridge.exe to test")
    args = parser.parse_args()
    bridge = os.path.abspath(args.bridge)
    results: list[dict] = []
    sandbox = long_path(tempfile.mkdtemp(prefix="gn-bridge-"))
    created: list[str] = []
    msi_created = False
    try:
        first = os.path.join(sandbox, "first")
        second = os.path.join(sandbox, "second")
        for directory in (first, second):
            os.makedirs(directory)
            shutil.copyfile(bridge, os.path.join(directory, "git.exe"))

        with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, MSI_KEY, 0, winreg.KEY_ALL_ACCESS | winreg.KEY_WOW64_64KEY) as key:
            # The trailing separator is the condition that broke the string comparison.
            winreg.SetValueEx(key, "InstallRoot", 0, winreg.REG_SZ, sandbox + "\\")
        msi_created = True

        first_exe = os.path.join(first, "git.exe")
        second_exe = os.path.join(second, "git.exe")
        for path in (first_exe, second_exe):
            register(path, "windows-x64", os.path.relpath(path, sandbox))
            created.append(path)

        # 1. The entry point forwards to itself through a root with a trailing separator.
        code, elapsed, started = run_confined(job := confined_job(), [first_exe, "version"], timeout=20)
        kernel32.TerminateJobObject(job, 1)
        kernel32.CloseHandle(job)
        results.append({"scenario": "self", "exit": code, "seconds": round(elapsed, 3),
                        "processes": started, "ok": code == 127 and started == 1})

        # 2. Two files that name each other: the depth guard has to stop the cycle.
        register(first_exe, "windows-x64", os.path.relpath(second_exe, sandbox))
        register(second_exe, "windows-x64", os.path.relpath(first_exe, sandbox))
        code, elapsed, started = run_confined(job := confined_job(), [first_exe, "version"], timeout=20)
        kernel32.TerminateJobObject(job, 1)
        kernel32.CloseHandle(job)
        # The bridge stops at depth four, so a cycle costs at most five images: the
        # launched one plus four hops. Anything beyond that means the guard is gone.
        results.append({"scenario": "cycle", "exit": code, "seconds": round(elapsed, 3),
                        "processes": started, "ok": code == 127 and started <= 5})

        # 3. A real forward still works and returns the target's status. The target stays
        # inside the install root: a bridge refuses traversal on purpose.
        target = os.path.join(sandbox, "target")
        os.makedirs(target, exist_ok=True)
        shutil.copyfile(os.path.join(os.environ["SystemRoot"], "System32", "cmd.exe"),
                        os.path.join(target, "cmd.exe"))
        register(first_exe, "windows-x64", r"target\cmd.exe")
        code, elapsed, started = run_confined(job := confined_job(), [first_exe, "/c", "exit", "7"], timeout=20)
        kernel32.TerminateJobObject(job, 1)
        kernel32.CloseHandle(job)
        results.append({"scenario": "forward", "exit": code, "seconds": round(elapsed, 3),
                        "processes": started, "ok": code == 7})

        # 4. A target beyond MAX_PATH still starts, which needs the extended-length marker.
        deep = sandbox
        while len(os.path.join(deep, "git.exe")) < 300:
            deep = os.path.join(deep, "directory-with-a-long-name-for-the-limit-" + str(len(deep)))
        os.makedirs(deep, exist_ok=True)
        shutil.copyfile(os.path.join(os.environ["SystemRoot"], "System32", "cmd.exe"),
                        os.path.join(deep, "cmd.exe"))
        register(first_exe, "windows-x64", os.path.relpath(os.path.join(deep, "cmd.exe"), sandbox))
        code, elapsed, started = run_confined(job := confined_job(), [first_exe, "/c", "exit", "5"], timeout=20)
        kernel32.TerminateJobObject(job, 1)
        kernel32.CloseHandle(job)
        results.append({"scenario": "longpath", "exit": code, "seconds": round(elapsed, 3),
                        "processes": started, "ok": code == 5})

        ok = all(item["ok"] for item in results)
        print(json.dumps({"ok": ok, "bridge": bridge, "process_limit": PROCESS_LIMIT, "scenarios": results},
                         indent=2, ensure_ascii=False))
        return 0 if ok else 1
    finally:
        for path in created:
            forget(path)
        if msi_created:
            try:
                winreg.DeleteKey(winreg.HKEY_CURRENT_USER, MSI_KEY)
            except OSError:
                pass
        remove_tree(sandbox)


if __name__ == "__main__":
    sys.exit(main())
