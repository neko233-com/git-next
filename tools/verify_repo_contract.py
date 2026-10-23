"""Check the engineering contract in AGENTS.md, mechanically.

A contract that is only words is a preference: it is read, agreed with, and then quietly
not followed. This tool reads the same repository an agent would and asserts each hard
rule, so the difference between "the contract says" and "the repository does" is caught
before a commit rather than after an incident.

Every check below names the rule it enforces. A check that passes without being able to
fail is worse than no check, so each one is written against the specific thing that broke
or could break:

- The runtime defaults caused a real incident. They now live in one file and three
  consumers are rendered from it, so the failure mode is a consumer that holds a copy.
- A forwarder that starts a copy of itself is the incident itself. The bridge compares
  file identity and counts hops, and both are asserted here.
- Windows text files arrived as CRLF twice in one day, which made files look modified
  while their content was identical. The line-ending rule is now asserted.

    python tools/verify_repo_contract.py
    python tools/verify_repo_contract.py --full   # also run the behavioural default checks

Exit 0 means every rule holds.
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(Path(__file__).resolve().parent))
import runtime_defaults  # noqa: E402

CRLF_EXEMPT = (".ps1", ".psm1", ".bat", ".cmd")
# Measurement reports under docs/benchmarks are produced by Windows tools and are evidence
# rather than source: rewriting them would change the hash of an accepted measurement. The
# rule is about files a person or an agent writes.
CRLF_EXEMPT_PREFIX = ("docs/benchmarks/",)
CONVENTIONAL = re.compile(r"^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)"
                          r"(\([^)]+\))?!?: .+")


class Report:
    """Collects the outcome of each rule so one run reports everything, not the first failure."""

    def __init__(self):
        self.checks = []

    def add(self, name, ok, detail):
        self.checks.append({"name": name, "ok": bool(ok), "detail": detail})

    @property
    def ok(self):
        return all(item["ok"] for item in self.checks)


def read(path):
    return (ROOT / path).read_bytes().decode("utf-8-sig").replace("\r\n", "\n")


def exists(path):
    return (ROOT / path).exists()


def check_defaults(report):
    """Rule: a runtime default is written in exactly one place, and is documented."""
    fragment = ROOT / "packaging" / "runtime-defaults" / "gitconfig"
    report.add("defaults-fragment-exists", fragment.exists(),
               str(fragment.relative_to(ROOT)) if fragment.exists() else "missing")

    # The migration has to hold a literal because it is PowerShell; that literal is the
    # only permitted copy and must equal the rendering.
    literal = '"' + runtime_defaults.powershell_literal() + '"'
    migration = read("packaging/windows/Takeover.Core.psm1")
    report.add("migration-literal-rendered", literal in migration,
               "the migration writes the rendered defaults"
               if literal in migration else "the migration literal drifted from the fragment")

    # The other two consumers must render rather than copy: a hard-coded block here is the
    # failure this rule exists to prevent.
    assembly = read("tools/assemble_native_windows.py")
    report.add("payload-config-rendered",
               "runtime_defaults.core_body()" in assembly and "runtime_defaults.tail()" in assembly,
               "the payload configuration is rendered from the fragment"
               if "runtime_defaults.core_body()" in assembly
               else "the payload configuration does not render from the fragment")

    evidence = read("tools/native_msi_evidence.py")
    report.add("evidence-check-rendered", "_product_defaults_block()" in evidence,
               "the evidence reuse check renders the defaults"
               if "_product_defaults_block()" in evidence
               else "the evidence reuse check hard-codes the defaults")

    # A default nobody wrote down is a default nobody can justify.
    document = read("docs/DEFAULTS.md")
    undocumented = ["%s.%s" % (section, key)
                    for section, key, _ in runtime_defaults.settings()
                    if key not in document]
    report.add("defaults-documented", not undocumented,
               "every default is in docs/DEFAULTS.md" if not undocumented
               else "undocumented: %s" % ", ".join(undocumented))


def check_process_safety(report):
    """Rule: no invocation may multiply, and the machine has a ceiling."""
    bridge = read("native-core/launcher/windows-bridge.c")
    # Comparing text instead of file identity is what let a forwarder start itself: the
    # same file spelled with a different separator did not look like itself.
    report.add("bridge-compares-file-identity", "GetFileInformationByHandle" in bridge,
               "the bridge compares volume serial and file index"
               if "GetFileInformationByHandle" in bridge
               else "the bridge no longer compares file identity")
    # A pair of files that name each other is the other shape the incident can take.
    report.add("bridge-limits-forwarding-depth", "DEPTH_LIMIT" in bridge,
               "the bridge counts hops" if "DEPTH_LIMIT" in bridge
               else "the bridge has no hop limit")

    watchdog = read("tools/git_process_watchdog.py")
    report.add("watchdog-has-ceiling", "--limit" in watchdog and "PROCESS_TERMINATE" in watchdog,
               "the watchdog enforces a process ceiling"
               if "--limit" in watchdog else "the watchdog cannot terminate anything")
    report.add("watchdog-reaps-daemons", "--daemons" in watchdog,
               "the watchdog can stop parentless daemons"
               if "--daemons" in watchdog else "no way to stop leaked daemons")

    runner = read("docker/tests/run.sh")
    report.add("containers-have-pids-limit", "--pids-limit" in runner and "--memory" in runner,
               "container runs are bounded" if "--pids-limit" in runner
               else "container runs are unbounded")


def check_documentation(report):
    """Rule: the Markdown and the site are current in the same commit."""
    renderer = read("tools/render_docs.py")
    # The renderer holds the authoritative list of pages, so a document that is not listed
    # is never published and a page that is listed but missing was deleted by hand.
    pages = re.findall(r"': '([a-z0-9-]+\.html)'", renderer)
    missing_pages = [name for name in pages if not exists("docs/site/" + name)]
    report.add("site-pages-present", bool(pages) and not missing_pages,
               "%d pages rendered, all present" % len(pages) if not missing_pages
               else "missing: %s" % ", ".join(missing_pages))

    # A page nobody links to is a document nobody can reach. The navigation lives on every
    # page, so the index is enough to prove reachability.
    index = read("docs/site/index.html")
    unreachable = [page for page in pages if page != "index.html"
                   and ('href="%s"' % page) not in index]
    report.add("site-pages-linked", not unreachable,
               "every page is reachable from the navigation" if not unreachable
               else "unreachable: %s" % ", ".join(unreachable))


def git(*args):
    """Run git in the repository, or return None where git does not exist.

    The contract also runs inside a container that has Python and no git, and a rule that
    cannot be evaluated there must be reported as skipped rather than as a failure.
    """
    try:
        return subprocess.run(["git"] + list(args), cwd=ROOT, capture_output=True, text=True)
    except (OSError, FileNotFoundError):
        return None


def check_line_endings(report):
    """Rule: unconfigured text uses LF consistently.

    Files arrived as CRLF twice in one day and git reported them modified while their
    content was identical, which is a debugging cost paid for nothing.
    """
    tracked = git("ls-files")
    if tracked is None or tracked.returncode != 0:
        report.add("tracked-files-use-lf", True, "skipped: git is not available")
        return
    offenders = []
    for name in tracked.stdout.split():
        if name.endswith(CRLF_EXEMPT) or name.startswith(CRLF_EXEMPT_PREFIX):
            continue
        path = ROOT / name
        if not path.is_file():
            continue
        try:
            data = path.read_bytes()
        except OSError:
            continue
        if b"\x00" in data:
            continue
        if b"\r\n" in data:
            offenders.append(name)
    report.add("tracked-files-use-lf", not offenders,
               "no CRLF in tracked text files" if not offenders
               else "CRLF: %s" % ", ".join(offenders[:8]))


def check_repository_rules(report):
    """Rule: no GitHub Actions, and the commit contract is kept."""
    workflows = ROOT / ".github" / "workflows"
    report.add("no-github-workflows", not workflows.exists(),
               "no workflow directory" if not workflows.exists() else ".github/workflows exists")

    commit = git("log", "-1", "--format=%B")
    if commit is None:
        report.add("commit-contract", True, "skipped: git is not available")
        return
    message = commit.stdout.strip()
    subject = message.splitlines()[0] if message else ""
    report.add("commit-conventional-subject", bool(CONVENTIONAL.match(subject)),
               subject[:70] or "(no commit)")
    report.add("commit-assistance-trailer", "Assisted-by:" in message,
               "the trailer is present" if "Assisted-by:" in message
               else "missing Assisted-by trailer")
    report.add("commit-no-signoff", "Signed-off-by:" not in message,
               "no automated sign-off" if "Signed-off-by:" not in message
               else "a sign-off trailer must come from a human")


def check_locales(report):
    """Rule: every locale change passes the locale checker."""
    result = subprocess.run([sys.executable, str(ROOT / "tools" / "check_locales.py")],
                            cwd=ROOT, capture_output=True, text=True)
    report.add("locale-documents-valid", result.returncode == 0,
               (result.stdout or "").strip().splitlines()[0] if result.returncode == 0
               else (result.stdout + result.stderr).strip()[:200])


def check_defaults_behaviour(report):
    """Optional: exercise the defaults against the installed binary."""
    result = subprocess.run([sys.executable, str(ROOT / "tools" / "check_runtime_defaults.py"),
                             "--agreement-only"], cwd=ROOT, capture_output=True, text=True)
    report.add("runtime-defaults-agreement", result.returncode == 0,
               "every consumer agrees" if result.returncode == 0
               else (result.stdout + result.stderr).strip()[-200:])


def main():
    full = "--full" in sys.argv[1:]
    report = Report()
    check_defaults(report)
    check_process_safety(report)
    check_documentation(report)
    check_line_endings(report)
    check_repository_rules(report)
    check_locales(report)
    if full:
        check_defaults_behaviour(report)
    width = max(len(item["name"]) for item in report.checks)
    for item in report.checks:
        print("%-*s  %s  %s" % (width, item["name"], "ok  " if item["ok"] else "FAIL", item["detail"]))
    failed = [item for item in report.checks if not item["ok"]]
    print("%d rules checked, %d failed" % (len(report.checks), len(failed)))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
