"""Install the product's runtime defaults onto this machine.

Shipping a default is not the same as installing one. The defaults live in
packaging/runtime-defaults/gitconfig, and this tool takes them the last centimetre:

1. The **migrated system configuration** (`%ProgramData%\\git-next\\config\\system.gitconfig`)
   is where they belong. It applies to every account and to callers that run without a
   user profile. Writing it is an administrator action, so the tool relaunches itself
   elevated when it is not already running that way.
2. If elevation is refused - a consent policy can block it, and that must not be a reason
   the defaults never take effect - it falls back to the **user's global configuration**,
   which is always writable and, for that user, always beats the system configuration.
3. It then asks Git what each key resolves to. "Installed" here means Git reports the
   value, not that a file was written.

The fallback is chosen only when the system write did not happen. If the system
configuration already carries a value, the global pass finds it set and writes nothing,
so the two never disagree and a later product change is not frozen into a personal file.

A value a person chose is never overwritten unless --force is given. That matters here:
core.fsmonitor is false in the product and true in this workstation's global
configuration, and the person's own choice is left alone.

    python tools/install_runtime_defaults.py            # install, then verify
    python tools/install_runtime_defaults.py --plan     # show what is still missing
    python tools/install_runtime_defaults.py --force    # override values already set
    python tools/install_runtime_defaults.py --json out.json
"""

import argparse
import ctypes
import ctypes.wintypes as wintypes
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import runtime_defaults  # noqa: E402

# Written by the takeover during migration; see packaging/windows/Takeover.Core.psm1.
SYSTEM = Path(os.environ.get("ProgramData", r"C:\ProgramData")) / "git-next" / "config" / "system.gitconfig"
SELF = str(Path(__file__).resolve())
# The payload's own entry point, which is what a taken-over workstation calls git.
INSTALLED_GIT = Path(os.environ.get("ProgramFiles", r"C:\Program Files")) / "Git" / "cmd" / "git.exe"


class ShellExecuteInfo(ctypes.Structure):
    """The struct behind ShellExecuteExW, which is how Windows is asked to elevate."""

    _fields_ = [
        ("cbSize", wintypes.DWORD), ("fMask", ctypes.c_ulong), ("hwnd", wintypes.HANDLE),
        ("lpVerb", wintypes.LPCWSTR), ("lpFile", wintypes.LPCWSTR), ("lpParameters", wintypes.LPCWSTR),
        ("lpDirectory", wintypes.LPCWSTR), ("nShow", ctypes.c_int), ("hInstApp", wintypes.HANDLE),
        ("lpIDList", ctypes.c_void_p), ("lpClass", wintypes.LPCWSTR), ("hkeyClass", wintypes.HANDLE),
        ("dwHotKey", wintypes.DWORD), ("hIcon", wintypes.HANDLE), ("hProcess", wintypes.HANDLE),
    ]


def is_admin() -> bool:
    return bool(ctypes.windll.shell32.IsUserAnAdmin())


def relaunch_elevated(extra_args) -> bool:
    """Run this script again as an administrator, and wait for it to finish.

    Returns False when Windows refuses to even ask, which is not an error this tool can
    fix: the caller then falls back to the global configuration. The elevated instance
    writes the file and exits; the original process does the verifying and reporting, so
    there is exactly one report no matter which path runs.
    """
    shell32 = ctypes.WinDLL("shell32", use_last_error=True)
    kernel32 = ctypes.WinDLL("kernel32")
    info = ShellExecuteInfo()
    info.cbSize = ctypes.sizeof(info)
    # NOCLOSEPROCESS so the child can be waited on, NOASYNC so the outcome is known here.
    info.fMask = 0x00000040 | 0x00000100
    info.lpVerb = "runas"
    info.lpFile = sys.executable
    info.lpParameters = '"%s" %s' % (SELF, " ".join(["--elevated"] + list(extra_args)))
    info.nShow = 0
    if not shell32.ShellExecuteExW(ctypes.byref(info)):
        return False
    kernel32.WaitForSingleObject(info.hProcess, 300000)
    code = wintypes.DWORD()
    kernel32.GetExitCodeProcess(info.hProcess, ctypes.byref(code))
    kernel32.CloseHandle(info.hProcess)
    return code.value == 0


def discover_git(explicit=None) -> str:
    """The Git binary to verify with: the installed git-next, else whatever is on PATH."""
    if explicit:
        return explicit
    if INSTALLED_GIT.exists():
        return str(INSTALLED_GIT)
    return shutil.which("git") or "git"


def resolved(git: str, key: str) -> str:
    """What Git says a key resolves to, after includes and all levels of precedence.

    '<default>' is what `git config --get` prints for a key with no value at any level, so
    that and a missing key both mean unset.
    """
    result = subprocess.run([git, "config", "--get", key], capture_output=True, text=True)
    if result.returncode != 0:
        return ""
    value = result.stdout.strip()
    return "" if value == "<default>" else value


def system_carries_block() -> bool:
    """True when the migrated system configuration already ends with our block."""
    if not SYSTEM.exists():
        return False
    try:
        text = SYSTEM.read_bytes().decode("utf-8-sig")
    except OSError:
        return False
    return text.rstrip("\r\n").endswith(runtime_defaults.block_text().rstrip("\n"))


def write_system() -> tuple:
    """Replace the default block of the migrated system configuration.

    Everything from the first non-include section onwards is the product's own block, so it
    is replaced wholesale. The header comment and the include of the payload configuration
    are kept, because the include is how the rest of the configuration arrives and moving
    it would change precedence.
    """
    if not SYSTEM.exists():
        return False, "the migrated system configuration does not exist: %s" % SYSTEM
    try:
        text = SYSTEM.read_bytes().decode("utf-8-sig").replace("\r\n", "\n")
    except OSError as error:
        return False, "cannot read %s: %s" % (SYSTEM, error)
    lines = text.split("\n")
    cut = len(lines)
    for index, line in enumerate(lines):
        if line.startswith("[") and line.strip() != "[include]":
            cut = index
            break
    head = [line for line in lines[:cut] if line != ""]
    block = runtime_defaults.block_text().split("\n")
    backup = SYSTEM.with_name(SYSTEM.name + ".bak")
    try:
        backup.write_bytes(text.encode("utf-8"))
        SYSTEM.write_bytes(("\n".join(head + block).rstrip("\n") + "\n").encode("utf-8"))
    except OSError as error:
        return False, "cannot write %s: %s" % (SYSTEM, error)
    return True, "wrote %d defaults; backup at %s" % (len(runtime_defaults.settings()), backup)


def apply_global(git: str, force: bool, commit: bool):
    """Fill in the keys that are still unset in the user's global configuration."""
    changes, kept = [], []
    for section, key, value in runtime_defaults.settings():
        full = "%s.%s" % (section, key)
        current = resolved(git, full)
        if current and current.lower() == value.lower():
            kept.append("%s=%s (already)" % (full, current))
            continue
        if current and not force:
            # A person's own value wins; only an unset key is filled in.
            kept.append("%s=%s (kept)" % (full, current))
            continue
        changes.append((full, value))
        if commit:
            subprocess.run([git, "config", "--global", full, value], capture_output=True)
    return changes, kept


def emit(report, path=None) -> None:
    if path:
        Path(path).write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n",
                              encoding="utf-8", newline="\n")
    print(json.dumps(report, indent=2, ensure_ascii=False))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--plan", action="store_true", help="report what is missing, change nothing")
    parser.add_argument("--force", action="store_true", help="override values a person already set")
    parser.add_argument("--json", help="write a machine readable report here")
    parser.add_argument("--git", help="the Git binary to verify with")
    parser.add_argument("--elevated", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    git = discover_git(args.git)

    # The elevated instance exists only to write the file. Verification and reporting stay
    # with the process the person actually ran, so there is one report either way.
    if args.elevated:
        done, detail = write_system()
        emit({"mode": "elevated", "system_applied": done, "system_detail": detail})
        return 0 if done else 1

    if args.plan:
        changes, kept = apply_global(git, args.force, commit=False)
        emit({"mode": "plan", "pending": [list(item) for item in changes],
              "left_alone": kept, "pending_count": len(changes)})
        return 0

    if os.name != "nt":
        # There is no migrated system configuration to write on POSIX, and the packaged
        # installer ships the defaults with the payload.
        emit({"mode": "unsupported", "problem": "Windows only"})
        return 1

    report = {"system": str(SYSTEM), "git": git}
    if is_admin():
        report["system_applied"], report["system_detail"] = write_system()
        report["mode"] = "system"
    else:
        asked = relaunch_elevated(["--force"] if args.force else [])
        # Whether it was granted is read back from the file rather than assumed: consent
        # can be refused after a successful launch.
        if asked and system_carries_block():
            report.update({"mode": "system", "system_applied": True,
                           "system_detail": "written by the elevated instance"})
        elif not asked:
            report.update({"mode": "global", "system_applied": False,
                           "system_detail": "elevation was refused by policy; using the user global configuration"})
        else:
            report.update({"mode": "global", "system_applied": False,
                           "system_detail": "the system configuration was not written; using the user global configuration"})

    changes, kept = apply_global(git, args.force, commit=True)
    report["global_applied"] = [list(item) for item in changes]
    report["left_alone"] = kept

    # Verification is against Git itself. A key that resolves to the intended value is
    # installed; one that resolves to something else is a personal value left in place and
    # is reported rather than silently corrected.
    effective, mismatched, unresolved = {}, [], []
    for section, key, want in runtime_defaults.settings():
        full = "%s.%s" % (section, key)
        got = resolved(git, full)
        effective[full] = got or "(unset)"
        if not got:
            unresolved.append(full)
        elif got.lower() != want.lower():
            mismatched.append({"key": full, "shipped": want, "resolved": got})
    report["effective"] = effective
    report["unresolved"] = unresolved
    report["differs_from_shipped"] = mismatched
    report["ok"] = not unresolved
    emit(report, args.json)
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
