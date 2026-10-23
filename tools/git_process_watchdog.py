#!/usr/bin/env python3
# Copyright 2026 git-next contributors. MIT license.
"""Keep the number of live Git processes bounded, whatever started them.

A build script, an IDE or a broken caller can start Git faster than the processes finish.
Nothing in a single Git process can prevent that: the decision to start a thousand copies
belongs to the caller. This supervisor is the only place where a ceiling can be enforced,
because it looks at the whole machine instead of one invocation.

It never touches the oldest processes: those are the ones a person or a build is waiting
for. The newest excess is terminated, which is exactly the set a runaway caller produced.

    python tools/git_process_watchdog.py                 # report only
    python tools/git_process_watchdog.py --apply         # terminate the newest excess
    python tools/git_process_watchdog.py --apply --watch # keep watching

A second mode exists for a different problem. Git's builtin file-system monitor speeds up
status, but it leaves one daemon per repository behind and that daemon outlives the
repository itself: deleting the repository does not stop it. Measured on this workstation,
six repositories deleted still had six daemons alive fifteen seconds later, each holding
about 12 MB. The monitor is therefore not a product default, but a person who turns it on
needs a way to take the daemons back:

    python tools/git_process_watchdog.py --daemons               # report the orphans
    python tools/git_process_watchdog.py --daemons --apply       # stop them
    python tools/git_process_watchdog.py --daemons --min-age 300 # only the older ones

It only ever considers a Git process whose parent is gone and that has been alive longer
than the threshold, which is what a daemon looks like; a clone or a gc still has the
process that asked for it and is left alone.
"""

from __future__ import annotations

import argparse
import ctypes
import ctypes.wintypes as wintypes
import json
import os
import sys
import time

FAMILY = ("git.exe", "git-next.exe", "git-lfs.exe")


class FILETIME(ctypes.Structure):
    _fields_ = [("low", wintypes.DWORD), ("high", wintypes.DWORD)]


class PROCESS_ENTRY(ctypes.Structure):
    _fields_ = [
        ("dwSize", wintypes.DWORD), ("cntUsage", wintypes.DWORD), ("th32ProcessID", wintypes.DWORD),
        ("th32DefaultHeapID", ctypes.POINTER(ctypes.c_ulong)), ("th32ModuleID", wintypes.DWORD),
        ("cntThreads", wintypes.DWORD), ("th32ParentProcessID", wintypes.DWORD),
        ("pcPriClassBase", ctypes.c_long), ("dwFlags", wintypes.DWORD),
        ("szExeFile", ctypes.c_wchar * 260),
    ]


kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
kernel32.OpenProcess.restype = wintypes.HANDLE
kernel32.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
kernel32.QueryFullProcessImageNameW.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR,
                                                ctypes.POINTER(wintypes.DWORD)]
kernel32.GetProcessTimes.argtypes = [wintypes.HANDLE, ctypes.POINTER(FILETIME), ctypes.POINTER(FILETIME),
                                     ctypes.POINTER(FILETIME), ctypes.POINTER(FILETIME)]
kernel32.GetSystemTimeAsFileTime.argtypes = [ctypes.POINTER(FILETIME)]

PROCESS_QUERY_LIMITED = 0x1000
PROCESS_TERMINATE = 0x0001


def image(pid: int) -> str:
    handle = kernel32.OpenProcess(PROCESS_QUERY_LIMITED, False, pid)
    if not handle:
        return ""
    try:
        buffer = ctypes.create_unicode_buffer(32768)
        size = wintypes.DWORD(32768)
        if kernel32.QueryFullProcessImageNameW(handle, 0, buffer, ctypes.byref(size)):
            return buffer.value
        return ""
    finally:
        kernel32.CloseHandle(handle)


def started_at(pid: int) -> int:
    handle = kernel32.OpenProcess(PROCESS_QUERY_LIMITED, False, pid)
    if not handle:
        return 0
    try:
        created, exited, kernel_time, user_time = FILETIME(), FILETIME(), FILETIME(), FILETIME()
        if not kernel32.GetProcessTimes(handle, ctypes.byref(created), ctypes.byref(exited),
                                        ctypes.byref(kernel_time), ctypes.byref(user_time)):
            return 0
        return (created.high << 32) | created.low
    finally:
        kernel32.CloseHandle(handle)


def now_filetime() -> int:
    stamp = FILETIME()
    kernel32.GetSystemTimeAsFileTime(ctypes.byref(stamp))
    return (stamp.high << 32) | stamp.low


def family() -> list[dict]:
    snapshot = kernel32.CreateToolhelp32Snapshot(0x2, 0)
    if snapshot == wintypes.HANDLE(-1).value:
        return []
    found: list[dict] = []
    self_pid = os.getpid()
    try:
        entry = PROCESS_ENTRY()
        entry.dwSize = ctypes.sizeof(PROCESS_ENTRY)
        more = kernel32.Process32FirstW(snapshot, ctypes.byref(entry))
        while more:
            pid = int(entry.th32ProcessID)
            name = entry.szExeFile.lower()
            if pid != self_pid and name in FAMILY:
                found.append({"pid": pid, "name": name, "parent": int(entry.th32ParentProcessID),
                              "started": started_at(pid), "path": image(pid)})
            more = kernel32.Process32NextW(snapshot, ctypes.byref(entry))
    finally:
        kernel32.CloseHandle(snapshot)
    return found


def terminate(pid: int) -> bool:
    handle = kernel32.OpenProcess(PROCESS_TERMINATE, False, pid)
    if not handle:
        return False
    try:
        return bool(kernel32.TerminateProcess(handle, 1))
    finally:
        kernel32.CloseHandle(handle)


def sweep(limit: int, apply: bool) -> dict:
    running = family()
    excess = max(0, len(running) - limit)
    newest = sorted(running, key=lambda item: item["started"], reverse=True)[:excess]
    killed = []
    if apply:
        for item in newest:
            if terminate(item["pid"]):
                killed.append(item["pid"])
    return {
        "limit": limit,
        "live": len(running),
        "excess": excess,
        "applied": apply,
        "terminated": killed,
        "candidates": [{"pid": item["pid"], "path": item["path"]} for item in newest],
    }


def reap(min_age: float, apply: bool) -> dict:
    """Stop the Git processes a file-system monitor left behind.

    A daemon is reparented to nothing the moment the invocation that started it exits, so
    it is the only kind of Git process that is both long-lived and parentless. A clone or
    a gc keeps its caller, and a process that only just started may simply be finishing,
    which is what the age threshold is for.
    """
    running = family()
    live = {item["pid"] for item in running}
    now = now_filetime()
    candidates = []
    for item in running:
        if item["parent"] in live or item["parent"] in (0, 4) or not item["started"]:
            continue
        age = (now - item["started"]) / 1e7
        if age >= min_age:
            candidates.append({"pid": item["pid"], "path": item["path"], "age": round(age)})
    candidates.sort(key=lambda item: item["age"], reverse=True)
    stopped = []
    if apply:
        for item in candidates:
            if terminate(item["pid"]):
                stopped.append(item["pid"])
    return {
        "min_age": min_age,
        "live": len(running),
        "orphans": len(candidates),
        "applied": apply,
        "terminated": stopped,
        "candidates": candidates,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--limit", type=int, default=64, help="live Git processes allowed at once")
    parser.add_argument("--apply", action="store_true", help="terminate instead of reporting")
    parser.add_argument("--watch", action="store_true", help="keep sweeping")
    parser.add_argument("--interval", type=float, default=5.0, help="seconds between sweeps")
    parser.add_argument("--once", action="store_true", help="single sweep, then exit")
    parser.add_argument("--daemons", action="store_true",
                        help="target parentless long-lived Git processes instead of excess")
    parser.add_argument("--min-age", type=float, default=120.0,
                        help="seconds a parentless process must have lived to be a daemon")
    args = parser.parse_args()
    if args.limit < 1:
        parser.error("--limit must be positive")
    if not sys.platform.startswith("win"):
        parser.error("this supervisor reads the Windows process table")
    if args.daemons:
        result = reap(args.min_age, args.apply)
        print(json.dumps(result, indent=2, ensure_ascii=False))
        return 0 if result["orphans"] == 0 else 1
    if args.watch and not args.once:
        try:
            while True:
                result = sweep(args.limit, args.apply)
                if result["excess"] or result["terminated"]:
                    print(json.dumps(result, ensure_ascii=False), flush=True)
                time.sleep(max(0.5, args.interval))
        except KeyboardInterrupt:
            return 0
    result = sweep(args.limit, args.apply)
    print(json.dumps(result, indent=2, ensure_ascii=False))
    return 0 if result["excess"] == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
