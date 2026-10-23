#!/usr/bin/env python3
# Copyright 2026 git-next contributors. MIT license.
"""Prove the CRT-free memory helpers and, when MinGW is present, the bridge link.

MSVC 14.44 /O2 emits memcpy for struct copies. A helper compiled at the same
optimization level can be rewritten into a call to itself, which links and then
overflows the stack. This script compiles the helpers, runs them, forces the
compiler to emit an external memcpy, and links the Windows bridge without a CRT
when x86_64-w64-mingw32-gcc is installed. It does not execute the bridge and it
does not claim an MSVC 14.44 packaging run.

    python tools/verify_freestanding_bridge.py
    python tools/verify_freestanding_bridge.py --receipt native-core/receipts/windows-bridge-freestanding-20261023.json
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build_windows_bridge as bridge  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
MEMORY = ROOT / "native-core/launcher/freestanding-memory.c"
DRIVER = ROOT / "tests/freestanding_memory_driver.c"
SOURCE = ROOT / "native-core/launcher/windows-bridge.c"
LIBCALL = r"""
struct block { unsigned char bytes[4096]; };
struct block copy_block(struct block value) { return value; }
int check(void) {
    struct block value, copied;
    unsigned index;
    for (index = 0; index < 4096; index++) value.bytes[index] = (unsigned char)index;
    copied = copy_block(value);
    return copied.bytes[0] == 0 && copied.bytes[1] == 1 && copied.bytes[255] == 255
        && copied.bytes[4095] == (unsigned char)4095;
}
#if defined(__linux__) && defined(__x86_64__)
__attribute__((force_align_arg_pointer, noreturn)) void _start(void) {
    long status = check() ? 0 : 3;
    __asm__ volatile("syscall" : : "a"(60L), "D"(status) : "rcx", "r11", "memory");
    __builtin_unreachable();
}
#endif
"""


def run(command, **kwargs):
    result = subprocess.run(command, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kwargs)
    if result.returncode != 0:
        raise RuntimeError("%s failed\n%s%s" % (" ".join(command), result.stdout, result.stderr))
    return result


def undefined_symbols(path):
    listed = run(["nm", str(path)]).stdout.splitlines()
    return sorted(line.split()[-1] for line in listed if line.split()[:1] == ["U"] or (len(line.split()) > 1 and line.split()[0] == "U"))


def compile_host(work: Path):
    helper = work / "memory.o"
    run(["gcc", "-c", "-O2", "-ffreestanding", "-fno-builtin", "-fno-stack-protector",
         "-Wall", "-Wextra", "-Werror", "-std=c11", str(MEMORY), "-o", str(helper)])
    undefined = undefined_symbols(helper)
    forbidden = [name for name in undefined if name.lstrip("_") in {"memcpy", "memset", "memmove", "memcmp"}]
    if forbidden:
        raise RuntimeError("helper object calls itself: %s" % ", ".join(forbidden))
    driver = work / "driver"
    run(["gcc", "-nostdlib", "-ffreestanding", "-O2", "-fno-stack-protector", "-Wall", "-Wextra", "-Werror",
         "-std=c11", str(DRIVER), str(MEMORY), "-o", str(driver)])
    driver_status = subprocess.run([str(driver)], check=False).returncode
    if driver_status != 0:
        raise RuntimeError("freestanding driver exited %s" % driver_status)
    libcall_source = work / "libcall.c"
    libcall_source.write_text(LIBCALL, encoding="utf-8", newline="\n")
    libcall_object = work / "libcall.o"
    run(["gcc", "-c", "-O2", "-mstringop-strategy=libcall", "-fno-stack-protector", "-std=c11",
         str(libcall_source), "-o", str(libcall_object)])
    if "memcpy" not in {name.lstrip("_") for name in undefined_symbols(libcall_object)}:
        raise RuntimeError("the libcall probe did not emit memcpy; the proof would not cover MSVC's helper call")
    libcall = work / "libcall"
    run(["gcc", "-nostdlib", "-O2", "-mstringop-strategy=libcall", "-fno-stack-protector", "-std=c11",
         str(libcall_source), str(MEMORY), "-o", str(libcall)])
    libcall_status = subprocess.run([str(libcall)], check=False).returncode
    if libcall_status != 0:
        raise RuntimeError("compiler-emitted memcpy exited %s" % libcall_status)
    gcc_version = run(["gcc", "--version"]).stdout.splitlines()[0].strip()
    return {"compiler_version": gcc_version, "helper_undefined": undefined,
            "driver_exit": driver_status, "compiler_libcall_exit": libcall_status}


def link_mingw(work: Path):
    compiler = shutil.which("x86_64-w64-mingw32-gcc")
    if not compiler:
        return None
    binaries = {}
    imports = {}
    for gui in (False, True):
        name = "git-next-bridge-gui.exe" if gui else "git-next-bridge.exe"
        binary = work / name
        command = bridge.mingw_link_command(compiler, SOURCE, MEMORY, None, binary, gui)
        run(command, cwd=ROOT)
        imports[name] = bridge.assert_crt_free(binary, name, 0x8664)
        data = binary.read_bytes()
        binaries[name] = {"sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data)}
    version = run([compiler, "--version"]).stdout.splitlines()[0].strip()
    return {"compiler": compiler, "compiler_version": version, "resource_linked": False,
            "binaries": binaries, "imports": imports, "product_executed": False}


def guard_rejects_reduced_inlining():
    sample = bridge.msvc_link_command("cl.exe", SOURCE, "memory.obj", "bridge.res",
                                      Path("git-next-bridge.exe"), [], [], False)
    if "/O2" not in sample or "/NODEFAULTLIB" not in sample or "/Ob1" in sample:
        raise RuntimeError("MSVC bridge command is not full /O2 without a CRT")
    try:
        bridge.reject_reduced_inlining(["/O2", "/Ob1"])
    except RuntimeError:
        return
    raise RuntimeError("the inline-limit guard did not fire")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--receipt", type=Path, help="write the host/MinGW proof; refuses to overwrite")
    args = parser.parse_args()
    if args.receipt and args.receipt.exists():
        parser.error("Receipt destination must not exist.")
    guard_rejects_reduced_inlining()
    if not shutil.which("gcc"):
        print(json.dumps({"ok": True, "host_proof": "skipped", "reason": "gcc is not installed"}))
        return 0
    with tempfile.TemporaryDirectory(prefix="gn-bridge-") as directory:
        work = Path(directory)
        host = compile_host(work)
        mingw = link_mingw(work)
        if args.receipt and mingw is None:
            parser.error("A MinGW x64 compiler is required before writing the bridge receipt.")
        proof = {
            "schema_version": 1,
            "status": "freestanding-bridge-proved-not-msvc-packaged",
            "decision": "full-O2-freestanding-memory",
            "rejected_alternative": "O2-plus-inline-limit",
            "optimizer": bridge.OPTIMIZER,
            "ob1_workaround": bridge.OB1_WORKAROUND,
            "msvc_14_44_link_executed": False,
            "product_executed": False,
            "release_eligible": False,
            "bridge_source_sha256": hashlib.sha256(SOURCE.read_bytes()).hexdigest(),
            "memory_source_sha256": hashlib.sha256(MEMORY.read_bytes()).hexdigest(),
            "driver_source_sha256": hashlib.sha256(DRIVER.read_bytes()).hexdigest(),
            "host_proof": host,
            "mingw_x64": mingw,
        }
        if args.receipt:
            args.receipt.parent.mkdir(parents=True, exist_ok=True)
            args.receipt.write_text(json.dumps(proof, indent=2) + "\n", encoding="utf-8", newline="\n")
        print(json.dumps({"ok": True, "driver_exit": host["driver_exit"],
                          "compiler_libcall_exit": host["compiler_libcall_exit"],
                          "helper_undefined": host["helper_undefined"],
                          "mingw_imports": None if mingw is None else mingw["imports"],
                          "receipt": None if args.receipt is None else str(args.receipt)}, indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except RuntimeError as error:
        print(error, file=sys.stderr)
        sys.exit(1)
