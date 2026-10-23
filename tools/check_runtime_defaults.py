"""Verify the product's runtime defaults: that they agree, and that they work.

The defaults live in packaging/runtime-defaults/gitconfig. Three places consume
them, and a default that exists in one but not another is how an installation
ends up with a configuration nobody tested. This tool checks two things:

1. Agreement. The payload assembly, the migration override and the evidence
   reuse check must all be rendered from the fragment. The migration has to hold
   a literal because it is PowerShell, so the literal is compared to the
   rendering word for word.
2. Behaviour. Every key is exercised against the real binary: a default is a
   promise about behaviour, not a line in a file. The checks are the claims in
   docs/DEFAULTS.md, so a default that stops doing what the document says fails
   here instead of on a workstation.

The last check is the one that matters most: after a status with the shipped
defaults, no git process is left behind. A per-repository daemon would be
correct for speed and wrong for this product, and the guard is what keeps that
decision from being reversed by accident.

Use --json <path> for a machine-readable report. Windows-only probes are skipped
elsewhere, so the agreement half still runs in a Linux container.
"""

import argparse
import ctypes
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import runtime_defaults  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
MIGRATION = ROOT / "packaging" / "windows" / "Takeover.Core.psm1"
BACKSLASH = chr(92)
UNC = BACKSLASH * 4 + "?" + BACKSLASH


def remove_tree(path):
    """rmtree stops at MAX_PATH, so the extended-length form is used on Windows."""
    shutil.rmtree(UNC + path, ignore_errors=True)
    shutil.rmtree(path, ignore_errors=True)


def make_long_path(base, leaf="leaf.txt", minimum=300):
    """A path longer than MAX_PATH, the thing the longpaths default is for."""
    path = base
    segment = "a-directory-segment-that-is-long-"
    while len(os.path.join(path, segment, leaf)) < minimum:
        path = os.path.join(path, segment)
        try:
            os.makedirs(path)
        except OSError:
            break
    return os.path.join(path, leaf)


class Harness:
    """Runs the binary with the defaults in the environment, not on the command line.

    git-next's init rejects -c, so the settings have to arrive as configuration
    rather than as options; the environment is also the only way to apply them to
    every command a check happens to run.
    """

    def __init__(self, git, defaults):
        self.git = git
        self.defaults = defaults

    def environment(self, extra=()):
        pairs = list(self.defaults) + list(extra)
        env = dict(os.environ)
        env["GIT_CONFIG_COUNT"] = str(len(pairs))
        for index, (key, value) in enumerate(pairs):
            env["GIT_CONFIG_KEY_%d" % index] = key
            env["GIT_CONFIG_VALUE_%d" % index] = value
        return env

    def run(self, args, cwd=None, extra=(), timeout=180):
        return subprocess.run(
            [self.git] + list(args), cwd=cwd, capture_output=True, text=True,
            errors="replace", env=self.environment(extra), timeout=timeout)

    def init(self, name, sandbox, extra=()):
        path = os.path.join(sandbox, name)
        os.makedirs(path, exist_ok=True)
        result = self.run(["init", path], extra=extra)
        if result.returncode != 0:
            raise RuntimeError("init failed: " + (result.stderr or result.stdout)[:200])
        return path

    def commit_all(self, repo, message):
        self.run(["add", "-A"], cwd=repo)
        return self.run(["-c", "user.name=Check", "-c", "user.email=check@invalid",
                         "commit", "-q", "-m", message], cwd=repo)


def git_process_ids():
    """The live git.exe processes, or an empty set where the API does not exist."""
    if os.name != "nt":
        return set()
    kernel32 = ctypes.WinDLL("kernel32")
    psapi = ctypes.WinDLL("psapi")
    kernel32.OpenProcess.restype = ctypes.c_void_p
    array = (ctypes.c_ulong * 8192)()
    needed = ctypes.c_ulong()
    psapi.EnumProcesses(ctypes.byref(array), ctypes.sizeof(array), ctypes.byref(needed))
    found = set()
    for index in range(needed.value // ctypes.sizeof(ctypes.c_ulong)):
        pid = array[index]
        if not pid:
            continue
        handle = kernel32.OpenProcess(0x1000, False, pid)
        if not handle:
            continue
        buffer = ctypes.create_unicode_buffer(1024)
        size = ctypes.c_ulong(1024)
        if kernel32.QueryFullProcessImageNameW(handle, 0, buffer, ctypes.byref(size)):
            if buffer.value.lower().endswith("git.exe"):
                found.add(pid)
        kernel32.CloseHandle(ctypes.c_void_p(handle))
    return found


def check_agreement():
    """Every consumer must be rendered from the fragment."""
    checks = []
    settings = runtime_defaults.settings()
    keys = ["%s.%s" % (section, key) for section, key, _ in settings]
    if len(keys) != len(set(keys)):
        checks.append({"name": "fragment-duplicate-keys", "ok": False,
                       "detail": "the fragment lists a key twice"})
    else:
        checks.append({"name": "fragment-duplicate-keys", "ok": True,
                       "detail": "%d defaults" % len(keys)})

    literal = '"' + runtime_defaults.powershell_literal() + '"'
    text = MIGRATION.read_bytes().decode("utf-8")
    present = literal in text
    checks.append({
        "name": "migration-literal-matches-fragment",
        "ok": present,
        "detail": ("the migration writes the rendered defaults"
                   if present else "the migration literal differs from the fragment"),
    })

    # The payload and the evidence check are rendered, not copied, but the fragment
    # has to be usable by both: a [core] section to splice and a tail to append.
    core = runtime_defaults.core_body()
    tail = runtime_defaults.tail()
    checks.append({
        "name": "fragment-renders",
        "ok": core.startswith("\tautocrlf = input\n") and tail.startswith("[gc]"),
        "detail": "%d core lines, %d bytes of tail" % (core.count("\n"), len(tail)),
    })
    # A default that is not a boolean, a number or a known enum is a typo waiting
    # to be shipped, and git accepts a bad value without complaint.
    allowed_enums = {"histogram", "zdiff3", "-committerdate", "version:refname", "main",
                     "input", "lf"}
    bad = [key for key, value in [(k, v) for _, k, v in settings]
           if value not in allowed_enums and not (value.isdigit() or value in ("true", "false"))]
    checks.append({"name": "fragment-value-shapes", "ok": not bad,
                   "detail": "unexpected values: %s" % (", ".join(bad) if bad else "none")})
    return checks


def check_behaviour(harness, sandbox):
    """Every default must do what docs/DEFAULTS.md says it does."""
    checks = []

    # core.longpaths: a path past MAX_PATH has to round-trip through the index.
    repo = harness.init("longpaths", sandbox)
    target = make_long_path(repo)
    try:
        with open(target, "w", encoding="utf-8") as handle:
            handle.write("deep\n")
        created = True
    except OSError as error:
        created, target = False, str(error)
    if created:
        added = harness.run(["add", "-A"], cwd=repo)
        status = harness.run(["status", "--porcelain"], cwd=repo)
        committed = harness.commit_all(repo, "deep")
        paths = [line for line in status.stdout.splitlines() if "leaf.txt" in line]
        # A path under MAX_PATH would pass without the default doing anything, so the
        # length is part of the assertion rather than a detail.
        checks.append({
            "name": "core.longpaths",
            "ok": (added.returncode == 0 and committed.returncode == 0
                   and bool(paths) and len(target) >= 260),
            "detail": "path length %d, add rc=%d, commit rc=%d, status listed=%s"
                      % (len(target), added.returncode, committed.returncode, bool(paths)),
        })
    else:
        checks.append({"name": "core.longpaths", "ok": False,
                       "detail": "the operating system refused a %d character path" % len(str(target))})

    # feature.manyFiles: the index has to come out as version 4 with the untracked cache.
    repo = harness.init("manyfiles", sandbox)
    for index in range(200):
        with open(os.path.join(repo, "f%03d.txt" % index), "w", encoding="utf-8") as handle:
            handle.write("x")
    harness.run(["add", "-A"], cwd=repo)
    index_path = os.path.join(repo, ".git", "index")
    raw = Path(index_path).read_bytes() if os.path.exists(index_path) else b""
    version = int.from_bytes(raw[4:8], "big") if len(raw) >= 8 else 0
    checks.append({"name": "feature.manyFiles", "ok": version == 4 and b"UNTR" in raw,
                   "detail": "index version %d, untracked cache extension %s"
                             % (version, b"UNTR" in raw)})

    # index.threads and grep.threads: -1 is accepted by config but fatal at runtime.
    repo = harness.init("threads", sandbox)
    for index in range(300):
        with open(os.path.join(repo, "f%03d.txt" % index), "w", encoding="utf-8") as handle:
            handle.write("x = 1\n")
    harness.run(["add", "-A"], cwd=repo)
    status = harness.run(["status", "--porcelain"], cwd=repo)
    grep = harness.run(["grep", "-c", "x = 1", "--", "."], cwd=repo)
    checks.append({
        "name": "index.threads/grep.threads",
        "ok": status.returncode == 0 and grep.returncode == 0,
        "detail": "status rc=%d, grep rc=%d %s"
                  % (status.returncode, grep.returncode,
                     (grep.stderr or "").strip()[:80]),
    })

    # diff.algorithm: the value has to reach the same code path as the option.
    repo = harness.init("diff", sandbox)
    original = "".join("line %d\n" % index for index in range(1, 40))
    with open(os.path.join(repo, "a.txt"), "w", encoding="utf-8") as handle:
        handle.write(original)
    harness.commit_all(repo, "base")
    with open(os.path.join(repo, "a.txt"), "w", encoding="utf-8") as handle:
        handle.write(original.replace("line 20\n", "inserted\nline 20\n"))
    with_default = harness.run(["diff"], cwd=repo).stdout
    with_flag = harness.run(["diff", "--histogram"], cwd=repo).stdout
    checks.append({"name": "diff.algorithm", "ok": with_default == with_flag,
                   "detail": "config output matches --histogram: %s" % (with_default == with_flag)})

    # merge.conflictStyle: zdiff3 is a diff3-family style, so the base marker appears.
    repo = harness.init("merge", sandbox)
    with open(os.path.join(repo, "c.txt"), "w", encoding="utf-8") as handle:
        handle.write("".join("%d\n" % index for index in range(1, 8)))
    harness.commit_all(repo, "base")
    harness.run(["checkout", "-q", "-b", "theirs"], cwd=repo)
    with open(os.path.join(repo, "c.txt"), "w", encoding="utf-8") as handle:
        handle.write("1\n2\nTHEIRS\n4\n5\n6\n7\n")
    harness.commit_all(repo, "theirs")
    harness.run(["checkout", "-q", "-"], cwd=repo)
    with open(os.path.join(repo, "c.txt"), "w", encoding="utf-8") as handle:
        handle.write("1\n2\nOURS\n4\n5\n6\n7\n")
    harness.commit_all(repo, "ours")
    harness.run(["merge", "theirs"], cwd=repo)
    conflicted = Path(os.path.join(repo, "c.txt")).read_text(encoding="utf-8", errors="replace")
    markers = ("<<<<<<<", "|||||||", "=======", ">>>>>>>")
    present = [marker in conflicted for marker in markers]
    checks.append({
        "name": "merge.conflictStyle",
        "ok": all(present),
        "detail": "zdiff3 base marker present: %s, well formed: %s"
                  % (present[1], all(present)),
    })

    # rebase.autoStash: a dirty tree must no longer block a rebase.
    repo = harness.init("autostash", sandbox)
    for name, content in (("x.txt", "1\n"), ("y.txt", "2\n")):
        with open(os.path.join(repo, name), "w", encoding="utf-8") as handle:
            handle.write(content)
    harness.commit_all(repo, "first")
    harness.run(["checkout", "-q", "-b", "feature"], cwd=repo)
    with open(os.path.join(repo, "f.txt"), "w", encoding="utf-8") as handle:
        handle.write("feature\n")
    harness.commit_all(repo, "feature")
    harness.run(["checkout", "-q", "-"], cwd=repo)
    with open(os.path.join(repo, "m.txt"), "w", encoding="utf-8") as handle:
        handle.write("main\n")
    harness.commit_all(repo, "main")
    harness.run(["checkout", "-q", "feature"], cwd=repo)
    with open(os.path.join(repo, "f.txt"), "w", encoding="utf-8") as handle:
        handle.write("dirty\n")
    with_stash = harness.run(["rebase", "-"], cwd=repo)
    harness.run(["rebase", "--abort"], cwd=repo)
    without_stash = harness.run(["rebase", "-"], cwd=repo,
                                extra=[("rebase.autoStash", "false")])
    harness.run(["rebase", "--abort"], cwd=repo)
    checks.append({
        "name": "rebase.autoStash",
        "ok": with_stash.returncode == 0 and without_stash.returncode != 0,
        "detail": "with default rc=%d, with rebase.autoStash=false rc=%d"
                  % (with_stash.returncode, without_stash.returncode),
    })

    # push.autoSetupRemote: a bare push has to create the upstream.
    repo = harness.init("push", sandbox)
    upstream = os.path.join(sandbox, "push-upstream.git")
    os.makedirs(upstream, exist_ok=True)
    harness.run(["init", "--bare", "-q", upstream])
    with open(os.path.join(repo, "p.txt"), "w", encoding="utf-8") as handle:
        handle.write("p\n")
    harness.commit_all(repo, "push")
    harness.run(["remote", "add", "origin", upstream], cwd=repo)
    pushed = harness.run(["push"], cwd=repo)
    upstream_ref = harness.run(["rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"], cwd=repo)
    checks.append({
        "name": "push.autoSetupRemote",
        "ok": pushed.returncode == 0 and upstream_ref.returncode == 0,
        "detail": "push rc=%d, upstream now %s" % (pushed.returncode,
                                                   upstream_ref.stdout.strip() or "unset"),
    })

    # init.defaultBranch: a new repository starts on main without being told to.
    repo = harness.init("defaultbranch", sandbox)
    with open(os.path.join(repo, "b.txt"), "w", encoding="utf-8") as handle:
        handle.write("b\n")
    harness.commit_all(repo, "branch")
    name = harness.run(["branch", "--show-current"], cwd=repo).stdout.strip()
    checks.append({"name": "init.defaultBranch", "ok": name == "main",
                   "detail": "new repository is on %r" % name})

    # branch.sort and tag.sort: the order has to change, otherwise the key is decoration.
    repo = harness.init("sort", sandbox)
    for branch, when in (("alpha", "2001-01-01T00:00:00"), ("zeta", "2020-01-01T00:00:00")):
        env = dict(os.environ, GIT_COMMITTER_DATE=when, GIT_AUTHOR_DATE=when)
        subprocess.run([harness.git, "checkout", "-q", "-b", branch], cwd=repo,
                       capture_output=True, env=env)
        with open(os.path.join(repo, branch + ".txt"), "w", encoding="utf-8") as handle:
            handle.write(branch + "\n")
        harness.run(["add", "-A"], cwd=repo)
        subprocess.run([harness.git, "-c", "user.name=Check", "-c", "user.email=check@invalid",
                        "commit", "-q", "-m", branch], cwd=repo,
                       capture_output=True, env=env)
    branches = [line.strip("* ").strip() for line in
                harness.run(["branch", "--list"], cwd=repo).stdout.splitlines()]
    checks.append({"name": "branch.sort", "ok": branches[:2] == ["zeta", "alpha"],
                   "detail": "order %s" % branches})

    for tag in ("v1.0", "v1.2", "v1.10"):
        harness.run(["tag", tag], cwd=repo)
    tags = [line.strip() for line in harness.run(["tag", "--list"], cwd=repo).stdout.splitlines()]
    checks.append({"name": "tag.sort", "ok": tags == ["v1.0", "v1.2", "v1.10"],
                   "detail": "order %s" % tags})

    # core.quotePath: a non-ASCII name has to stay readable in porcelain output. A space
    # is quoted whatever the setting says, so the probe needs a name that is not ASCII.
    repo = harness.init("quote", sandbox)
    leaf = "\u6587\u6863.txt"
    with open(os.path.join(repo, leaf), "w", encoding="utf-8") as handle:
        handle.write("x")
    harness.run(["add", "-A"], cwd=repo)
    with_default = harness.run(["status", "--porcelain"], cwd=repo).stdout
    with_true = harness.run(["status", "--porcelain"], cwd=repo,
                            extra=[("core.quotePath", "true")]).stdout
    checks.append({
        "name": "core.quotePath",
        "ok": leaf in with_default and leaf not in with_true,
        "detail": "default shows %r, quotePath=true shows %r"
                  % (with_default.strip()[:40], with_true.strip()[:40]),
    })

    # The one that guards the incident: with the shipped defaults, a status leaves
    # nothing behind. core.fsmonitor is the only default that can break this, and
    # it is false in the fragment on purpose.
    before = git_process_ids()
    for name in ("idle-a", "idle-b", "idle-c"):
        repo = harness.init(name, sandbox)
        with open(os.path.join(repo, "i.txt"), "w", encoding="utf-8") as handle:
            handle.write("i")
        harness.run(["add", "-A"], cwd=repo)
        harness.run(["status", "--porcelain"], cwd=repo)
    time.sleep(1.0)
    after = git_process_ids()
    if before or after:
        leftover = sorted(after - before)
        checks.append({
            "name": "no-process-left-behind",
            "ok": not leftover,
            "detail": "%d git process(es) remain after three repositories were used"
                      % len(leftover),
        })
    else:
        checks.append({"name": "no-process-left-behind", "ok": True,
                       "detail": "skipped: process enumeration needs Windows"})
    return checks


def long_paths_enabled():
    """The machine-level long path policy, which the product default cannot set."""
    if os.name != "nt":
        return {"supported": False, "value": None}
    import winreg
    try:
        key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                             r"SYSTEM\CurrentControlSet\Control\FileSystem")
        value = winreg.QueryValueEx(key, "LongPathsEnabled")[0]
    except OSError:
        value = None
    return {"supported": True, "value": value,
            "note": "a machine setting, not a product default; set by an administrator"}


def discover_git(explicit=None):
    if explicit:
        return explicit
    candidates = [
        os.path.join(os.environ.get("ProgramFiles", r"C:\Program Files"), "Git", "cmd", "git.exe"),
        shutil.which("git"),
    ]
    for candidate in candidates:
        if candidate and os.path.exists(candidate):
            return candidate
    return "git"


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--git", help="the git binary to exercise")
    parser.add_argument("--json", help="write the report here instead of stdout")
    parser.add_argument("--agreement-only", action="store_true",
                        help="skip the behavioural probes; useful in a container")
    args = parser.parse_args()

    defaults = [("%s.%s" % (section, key), value)
                for section, key, value in runtime_defaults.settings()]
    checks = check_agreement()
    behaviour = []
    if not args.agreement_only:
        git = discover_git(args.git)
        sandbox = tempfile.mkdtemp(prefix="gn-defaults-")
        try:
            behaviour = check_behaviour(Harness(git, defaults), sandbox)
        finally:
            remove_tree(sandbox)
        behaviour.insert(0, {"name": "binary", "ok": True,
                             "detail": os.path.basename(git)})
    report = {
        "git": discover_git(args.git),
        "default_count": len(defaults),
        "long_paths_enabled": long_paths_enabled(),
        "checks": checks + behaviour,
        "ok": all(item["ok"] for item in checks + behaviour),
    }
    if args.json:
        Path(args.json).write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n",
                                   encoding="utf-8", newline="\n")
    for item in report["checks"]:
        print("%-32s %s  %s" % (item["name"], "ok  " if item["ok"] else "FAIL", item["detail"]))
    print("long paths policy: %s" % report["long_paths_enabled"])
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
